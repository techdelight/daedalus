// Copyright (C) 2026 Techdelight BV

package control

import (
	"fmt"
	"sort"
	"strings"
)

// Refine — continue an existing artifact instead of starting over (#91).
//
// THE GAP IT CLOSES. Until now a Job could only ever begin from nothing:
// `worktrees.Add(repoDir, id, job.ID, task.BaseSHA)` checks out clean at the
// Task's base, so every attempt rebuilt the objective from scratch. That is
// right for a first attempt and right for a retry — an attempt that inherits a
// bad one is an attempt nobody can reason about.
//
// It is wrong for the case a REVIEW creates. RV-8 read a good artifact and found
// four things: one wrong sentence, one test that passed on a comment, a footnote
// under the wrong column, a colour that had stopped meaning anything. The work
// was sound. The routes available were to replan — which re-dispatches from a
// clean tree, so the agent would rebuild the whole feature to get four
// corrections — or to fix it by hand outside the plane, which leaves the record
// saying nothing about why the change happened. Neither keeps the work and acts
// on the findings, which is the ordinary thing to want after a reading.
//
// WHAT REFINE DOES NOT DO, and each absence is load-bearing:
//
//   - It does not change the OBJECTIVE. That is replan, and the difference is
//     what the record needs most: "the instruction was right and the work was
//     nearly there" is a different fact from "I asked for the wrong thing".
//   - It does not change BASE_SHA. The Job starts from the artifact and is still
//     graded from the base, so the original work stays inside the diff the oracle
//     sees. Moving the base would let an artifact carry itself past the verifier
//     by being declared the new starting point — the laundering shape.
//   - It does not act on a review by itself. A human names the review. An
//     automatic refine-on-failed-review would close the loop into agent writes /
//     agent reviews / agent fixes with nobody in it, and the reviewer's verdict
//     would silently become the gate that M20 spent a milestone establishing it
//     is not.

// RefineRequest asks for a continuation of a Task's existing artifact.
type RefineRequest struct {
	// ReviewID names the review whose findings this attempt is answering. Optional
	// — a human may have their own correction — but when given it is what puts the
	// findings in front of the agent.
	ReviewID string `json:"reviewId,omitempty"`
	// Note is a human's own instruction for the attempt, added to the prompt
	// alongside any findings.
	Note string `json:"note,omitempty"`
}

// refinableStates are the states with an artifact worth continuing.
//
// `verified` and `approval_required` are the interesting additions: they are
// where a Task sits after a review, and neither retry nor replan opens from
// them — so until now a reading of good work led nowhere the plane could act on.
// `working` and `verifying` are excluded because an attempt is in flight and its
// result is about to land.
var refinableStates = map[State]bool{
	StateCandidate:        true,
	StateRejected:         true,
	StateVerified:         true,
	StateApprovalRequired: true,
	StateApproved:         true,
}

func refinableStateNames() string {
	names := make([]string, 0, len(refinableStates))
	for s := range refinableStates {
		names = append(names, string(s))
	}
	sort.Strings(names)
	return strings.Join(names, "/")
}

// RefineTask arms a Task to continue from its latest artifact and returns it to
// `planned`, ready to dispatch.
func (s *Service) RefineTask(id string, req RefineRequest) (Task, error) {
	return s.refineTask(Human(), id, req)
}

func (s *Service) refineTask(caller Caller, id string, req RefineRequest) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, err := s.store.GetTask(id)
	if err != nil {
		return Task{}, err
	}
	if !refinableStates[task.State] {
		return Task{}, fmt.Errorf("%w: task %s is %s, not refinable (want %s)",
			ErrWrongState, id, task.State, refinableStateNames())
	}
	// An artifact to continue FROM. Without one there is nothing to refine and the
	// operator wants `dispatch` or `retry`; saying so is better than arming a
	// continuation that silently behaves like an ordinary attempt.
	job, art, ok, err := s.jobToReview(id)
	if err != nil {
		return Task{}, err
	}
	if !ok || art.HeadSHA == "" {
		return Task{}, fmt.Errorf("%w: task %s has no artifact to continue from — "+
			"nothing has been produced yet, so this is a dispatch rather than a refine", ErrWrongState, id)
	}
	// Refused NOW rather than at the next dispatch: a `planned` Task armed to
	// continue, which then cannot run, is a worse state than a refusal here.
	if err := s.checkDispatchBudget(task); err != nil {
		return Task{}, err
	}
	// A named review must belong to THIS task. Refining T-18 against another
	// task's findings would hand an agent instructions about code it is not
	// looking at.
	var findings int
	if req.ReviewID != "" {
		reviews, err := s.store.ReviewsForTask(id)
		if err != nil {
			return Task{}, err
		}
		found := false
		for _, r := range reviews {
			if r.ID == req.ReviewID {
				found, findings = true, len(r.Findings)
				break
			}
		}
		if !found {
			return Task{}, fmt.Errorf("%w: review %s is not a review of task %s",
				ErrNotFound, req.ReviewID, id)
		}
	}

	note := fmt.Sprintf("refine: continuing from artifact %s (%s)", art.ID, shortSHA(art.HeadSHA))
	if req.ReviewID != "" {
		note += fmt.Sprintf(", answering %s (%d finding(s))", req.ReviewID, findings)
	}
	if strings.TrimSpace(req.Note) != "" {
		note += ": " + strings.TrimSpace(req.Note)
	}
	// The note is stored on the Task so the next dispatch can put it in the
	// prompt, and recorded on the event so the record says the work was CORRECTED
	// after a reading rather than got right on the second attempt.
	if strings.TrimSpace(req.Note) != "" {
		if err := s.store.SetRefineNote(id, strings.TrimSpace(req.Note)); err != nil {
			return Task{}, err
		}
	}
	_ = job
	return s.store.RefineTask(id, art.HeadSHA, req.ReviewID, governanceMetaFor(caller), note)
}
