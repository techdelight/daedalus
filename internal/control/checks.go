// Copyright (C) 2026 Techdelight BV

package control

import (
	"fmt"
	"strings"
)

// AmendChecksRequest replaces a Task's per-task acceptance checks.
//
// Replace-the-whole-set rather than add-or-remove-by-index: the set is short, an
// index into a list the caller cannot see is a footgun, and "here is what this
// Task must satisfy" is a statement worth making in full each time it changes.
type AmendChecksRequest struct {
	Checks []string `json:"checks"`
}

// amendableStates are the states in which a Task's checks may be changed.
//
// The exclusions matter more than the inclusions. `verifying` is refused because
// changing the criteria while they are being applied is a race with no correct
// outcome. Everything from `verified` onward is refused because it would make the
// record incoherent rather than because it would change a verdict already given:
// a Task shown as having passed criteria it no longer carries is a worse artefact
// than a wrong verdict, since nothing about it looks wrong. A human who wants a
// stricter bar after a pass has the approval gate, which is designed to say no.
var amendableStates = map[State]bool{
	StatePlanned:   true,
	StateBlocked:   true,
	StateQueued:    true,
	StateCandidate: true,
	StateRejected:  true,
}

// AmendTaskChecks replaces a Task's per-task acceptance checks.
//
// The case for it: a check is written by a human before the work exists, and a
// check that is wrong — aimed at the wrong file, or asserting something the
// objective never asked for — cannot be corrected. Every attempt runs the same
// broken command, `retry` re-runs it against new work and `reverify` re-runs it
// against the same work, so the Task can never pass however good the artifact is.
// The only escape was to abandon the Task and recreate it, losing its history and
// its budget to a typo.
//
// Why this does not touch the laundering argument. There are two sets of criteria
// in play and only one of them is frozen. The project's policy lives in a
// committed `.daedalus/verify.json`, is read at base_sha, and is hashed onto the
// Task precisely so nobody can lower the bar after seeing the work — that stays
// exactly as it was. Per-task checks were deliberately kept OUTSIDE that hash
// when they were built (they are the Task's own addition, not the project's
// standard, and folding them in would make every task with a check look like
// drift). Amending one therefore changes nothing the freeze protects.
//
// What it does risk is behavioural: softening a check until the work slips
// through. Three things answer that, and none of them is a refusal — humans only,
// every amendment recorded with its lineage, and the free-replay discount
// withdrawn for a Task whose checks moved (see ChecksAmendedSinceLastVerdict),
// so a re-roll costs a review cycle like any other real grading.
func (s *Service) AmendTaskChecks(id string, req AmendChecksRequest) (Task, error) {
	return s.amendTaskChecks(Human(), id, req)
}

func (s *Service) amendTaskChecks(caller Caller, id string, req AmendChecksRequest) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Validation AND the human-only rule come from the same function the create
	// path uses. A second copy would be a second place for "the party being graded
	// does not choose the commands run inside the verifier" to be forgotten.
	checks, err := resolveTaskChecks(caller, req.Checks)
	if err != nil {
		return Task{}, err
	}

	task, err := s.store.GetTask(id)
	if err != nil {
		return Task{}, err
	}
	if !amendableStates[task.State] {
		return Task{}, fmt.Errorf("%w: task %s is %s, its checks are not amendable (want one of planned/blocked/queued/candidate/rejected)",
			ErrWrongState, id, task.State)
	}

	note := fmt.Sprintf("checks amended: %s → %s", renderChecks(task.Checks), renderChecks(checks))
	meta := EventMeta{Kind: EventChecksAmend, Actor: governanceMetaFor(caller).Actor}
	updated, err := s.store.SetTaskChecks(id, checks, meta, note)
	if err != nil {
		return Task{}, err
	}
	return updated, nil
}

// renderChecks formats a check set for a log line: the commands, or an explicit
// "none" so that adding the first check and removing the last are both legible.
func renderChecks(checks []string) string {
	if len(checks) == 0 {
		return "(none)"
	}
	return "[" + strings.Join(checks, " ; ") + "]"
}
