// Copyright (C) 2026 Techdelight BV

package control

import (
	"fmt"
	"log"
	"sort"
	"strings"
)

// ADOPTING LANDED WORK — the other half of "a landing moves no branch".
//
// The plane lands on `refs/daedalus/target`, which nobody checks out. That is
// the property the plane-owned target exists for (target.go): landing can never
// disturb a working tree, and no worker-writable ref decides anything. It is
// also, reliably, the thing an operator does not expect — "I integrated it and
// my branch is unchanged" is the sentence LandedNote, BranchAdvice and the
// Ledger's Landed footnote were all written to answer.
//
// They answer it by TELLING. This file is the part that ACTS.
//
// WHY IT IS PER PROJECT AND NOT PER TASK. `integrate --into-branch` is per task,
// and it has to be: it is a courtesy attached to one landing transaction. But a
// branch does not lag by a task, it lags by a COMMIT — six tasks landing onto one
// target leave the checkout one fast-forward behind, not six. Offering the move
// once per landed task would present six buttons where five are no-ops, and the
// sixth is indistinguishable from the other five until it is pressed. So the unit
// here is the project: one row, one branch, one action.
//
// WHAT IT REUSES, AND WHY THAT IS THE POINT. The move itself is
// advanceCheckoutBranch, unchanged and uncopied. Every refusal it makes — fast
// forward only, never on a dirty tree, never on a detached HEAD, never --force —
// holds here by construction rather than by a second implementation remembering
// to. A reimplementation would be a second set of guards to keep in step, and the
// guards are the whole reason the courtesy is safe to offer at all.

// Adoption is one PROJECT's answer to "is the work the plane has landed in my
// checkout branch yet?".
//
// It is a read, and it refuses nothing. A branch deliberately left behind the
// target is a normal way to work — the operator is the one who knows whether the
// gap is wanted, and the failure worth fixing is only that they could not see it
// (the same argument TargetLag makes for the opposite direction).
type Adoption struct {
	Project string `json:"project"`
	// Branch is the checkout's CURRENT branch — the one an adoption would move.
	// Empty on a detached HEAD, which Note then explains rather than leaving a
	// blank field to be read as "no branch is involved".
	Branch string `json:"branch,omitempty"`
	// TargetSHA is the commit the branch would move TO: the plane's landed trunk.
	TargetSHA string `json:"targetSha,omitempty"`
	HeadSHA   string `json:"headSha,omitempty"`
	// Behind is how many landed commits the branch does not have. Zero whenever
	// the branch already contains the target, which is what Adopted says.
	Behind int `json:"behind,omitempty"`
	// Landed is the Tasks this project has landed, oldest first. Carried so a row
	// can say WHAT is waiting to be adopted without the caller running a second
	// query — and so the count is visibly one adoption over several tasks rather
	// than one adoption per task.
	Landed []string `json:"landed,omitempty"`
	// Adopted means THE BRANCH HAS THE LANDED COMMIT — it is at it, or ahead of
	// it. Reported as the outcome the operator cares about, not as "did anything
	// move", for the same reason advanceCheckoutBranch reports `advanced` that
	// way: the distinction the other version collapses produces a wrong sentence
	// (see RV-8 there).
	Adopted bool `json:"adopted"`
	// Adoptable means the plane could wind this branch forward right now. False
	// for a diverged branch and for a detached HEAD — neither is something a
	// fast-forward can fix, and Note says so in words.
	//
	// A DIRTY TREE IS DELIBERATELY NOT CHECKED HERE. It is the one obstacle that
	// changes minute to minute: a report is a snapshot, and an offer withdrawn
	// because somebody had a file open when the poll ran would be wrong again by
	// the time it was read. The refusal that matters is the one at the moment of
	// the move, and advanceCheckoutBranch makes it — with the sentence that says
	// to commit or stash, which is more use than a missing button.
	Adoptable bool `json:"adoptable"`
	Diverged  bool `json:"diverged,omitempty"`
	// Unknown carries why the comparison could not be made. Silence would read as
	// "nothing to adopt", which is the one answer this must never give wrongly.
	Unknown string `json:"unknown,omitempty"`
	// Note is the sentence every surface prints, filled on EVERY path including
	// the one where there is nothing to do. Same reason advanceCheckoutBranch
	// fills its own: silence about a branch is the complaint this whole area
	// exists to answer, and a row that says nothing reproduces it.
	Note string `json:"note"`
}

// Pending reports whether this project has landed work its branch does not have.
func (a Adoption) Pending() bool { return !a.Adopted && a.Unknown == "" }

// AdoptionResult is what one adoption did. Note is always present — see
// Adoption.Note, and advanceCheckoutBranch, which is where the words come from.
type AdoptionResult struct {
	Project   string `json:"project"`
	Branch    string `json:"branch,omitempty"`
	TargetSHA string `json:"targetSha"`
	// Adopted means the branch IS at the landed commit — moved here by this call,
	// or found already there. Already-there is a SUCCESS: nothing was needed and
	// nothing is wrong, and reporting it as a failure would tell an operator whose
	// branch is exactly right that it is not.
	Adopted bool   `json:"adopted"`
	Note    string `json:"note"`
}

// Adoptions reports, per project, whether the plane's landed work is in that
// project's checkout branch — one entry per project that has landed anything.
//
// Projects with NOTHING landed are absent: there is no work to adopt and a row
// saying so would be a row about nothing. A project that has landed work and is
// already at its target IS present, with Adopted true, because "you are up to
// date" is an answer worth giving and its absence would read as a project the
// plane had forgotten.
//
// KNOWN COST, stated rather than hidden, as queuesByProject states its own: this
// shells out to git a handful of times per project that has landed work, and the
// Ledger polls it with the board. It is bounded by the number of projects, not by
// the number of tasks — which is the same reason the rows are per project.
func (s *Service) Adoptions() ([]Adoption, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tasks, err := s.store.ListTasks()
	if err != nil {
		return nil, err
	}
	// ONE ENTRY PER PROJECT, however many Tasks landed into it. The map is the
	// whole of that rule; everything downstream renders what it produces.
	landed := map[string][]string{}
	for _, t := range tasks {
		if t.State != StateIntegrated {
			continue
		}
		landed[t.Project] = append(landed[t.Project], t.ID)
	}
	projects := make([]string, 0, len(landed))
	for name := range landed {
		projects = append(projects, name)
	}
	sort.Strings(projects)

	out := make([]Adoption, 0, len(projects))
	for _, project := range projects {
		out = append(out, s.adoptionFor(project, landed[project]))
	}
	// What needs doing first, then the widest gap. Stable, so projects with the
	// same standing stay in name order rather than in map order.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Pending() != out[j].Pending() {
			return out[i].Pending()
		}
		return out[i].Behind > out[j].Behind
	})
	return out, nil
}

// adoptionFor compares one project's checkout branch against the plane's target.
// Called with s.mu held.
//
// Every failure answers Unknown rather than an error, for the reason
// targetLagLocked gives: this is a report over several projects, and one whose
// repository has been moved must not empty the list for the rest.
func (s *Service) adoptionFor(project string, landed []string) Adoption {
	a := Adoption{Project: project, Landed: landed}
	dir, err := s.projects.ProjectDir(project)
	if err != nil {
		return a.unknown(fmt.Sprintf("could not resolve %s to a checkout: %v", project, err))
	}
	// A pure read of the plane's target, exactly as integrateOnce does it: a
	// missing one here is a real fault, never a cue to adopt the checkout's HEAD.
	target, err := s.Target(project)
	if err != nil {
		return a.unknown(err.Error())
	}
	a.TargetSHA = target.SHA
	if branch, err := runGit(dir, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		a.Branch = strings.TrimSpace(branch)
	}
	head, err := ReadHeadSHA(dir)
	if err != nil {
		return a.unknown(fmt.Sprintf("could not read HEAD of %s: %v", project, err))
	}
	a.HeadSHA = head

	if head == target.SHA {
		a.Adopted = true
		a.Note = a.where() + " is already at the landed commit " + shortSHA(target.SHA)
		return a
	}
	// AHEAD COUNTS AS ADOPTED. The branch contains every landed commit and some of
	// its own on top — the normal state of a checkout somebody is working in. It
	// has nothing to adopt, and offering it a fast-forward that git would refuse
	// would be the dead end this row exists to remove.
	if contains, err := IsAncestor(dir, target.SHA, head); err != nil {
		return a.unknown(err.Error())
	} else if contains {
		a.Adopted = true
		ahead, _ := CountCommitsBetween(dir, target.SHA, head)
		a.Note = fmt.Sprintf("%s already has the landed commit %s, and %s of its own on top",
			a.where(), shortSHA(target.SHA), commits(ahead))
		return a
	}
	onHistory, err := IsAncestor(dir, head, target.SHA)
	if err != nil {
		return a.unknown(err.Error())
	}
	if !onHistory {
		// Not adoptable and not up to date: the branch carries commits the landed
		// target does not, so winding it forward is impossible and anything else is
		// a merge decision belonging to the operator. Same judgement, same wording,
		// as advanceCheckoutBranch — which is what would refuse it.
		a.Diverged = true
		a.Note = fmt.Sprintf("%s has diverged from the landed commit — it cannot be wound forward; merge it yourself (`git merge %s`)",
			a.where(), targetRefName)
		return a
	}
	behind, err := CountCommitsBetween(dir, head, target.SHA)
	if err != nil {
		return a.unknown(err.Error())
	}
	a.Behind = behind
	if a.Branch == "" {
		// A detached HEAD is behind the target and still has no branch to move.
		// Said here rather than left to the refusal, so the row never offers an
		// action whose only possible answer is "there is no branch".
		a.Note = "the checkout has a detached HEAD, so there is no branch to advance — " + AdoptTarget
		return a
	}
	a.Adoptable = true
	a.Note = fmt.Sprintf("%s is %s behind the landed commit %s",
		a.Branch, commits(behind), shortSHA(target.SHA))
	return a
}

// where names the thing that would move, in a form that reads in a sentence.
func (a Adoption) where() string {
	if a.Branch == "" {
		return "the checkout of " + a.Project + " (detached HEAD)"
	}
	return a.Branch
}

// unknown fills both the machine-readable field and the sentence, so a caller
// that renders only Note never silently shows an empty row.
func (a Adoption) unknown(why string) Adoption {
	a.Unknown = why
	a.Note = "could not tell whether the landed work is in this checkout: " + why
	return a
}

// commits renders a count with its noun, because "1 commits behind" is the kind
// of thing that makes a reader stop trusting the rest of the sentence.
func commits(n int) string {
	if n == 1 {
		return "1 commit"
	}
	return fmt.Sprintf("%d commits", n)
}

// AdoptLanded advances a project's checkout branch to the plane's landed target
// — one action per project, for a HUMAN caller.
//
// It is the Ledger's Adopt button and `daedalus task adopt <project>`, and it is
// deliberately a thin wrapper over advanceCheckoutBranch: this function decides
// WHICH commit (the plane's target, read and never invented) and records that it
// happened; the move, and every refusal guarding it, is that function's.
//
// ALREADY THERE IS A SUCCESS. Nothing was needed, nothing is wrong, and the note
// says which branch and why there was nothing to do. Reporting it as an error
// would tell an operator whose branch is exactly right that it is not — the same
// mistake RV-8 fixed one layer down, and it would arrive here the moment two
// people press Adopt on the same project.
func (s *Service) AdoptLanded(project string) (AdoptionResult, error) {
	return s.adoptLanded(Human(), project)
}

// adoptLanded is AdoptLanded with an explicit caller identity, so the event log
// records who moved somebody's branch.
func (s *Service) adoptLanded(caller Caller, project string) (AdoptionResult, error) {
	// Serialized with integration on the same lock: an adoption and a landing both
	// read the target and touch the same repository, and reading a target that a
	// compare-and-swap is halfway through replacing is exactly the race the
	// transaction takes this lock to avoid. A fast-forward is cheap enough to hold
	// it for.
	s.mu.Lock()
	defer s.mu.Unlock()

	// A PURE READ of the target, and a missing one is an error rather than a cue
	// to adopt the checkout's HEAD (Target's contract). Inventing a target here
	// would let this operation move a branch to a commit the plane never landed.
	target, err := s.Target(project)
	if err != nil {
		return AdoptionResult{}, err
	}
	branch, advanced, note := s.advanceCheckoutBranch(project, target.SHA)
	res := AdoptionResult{
		Project: project, Branch: branch, TargetSHA: target.SHA,
		Adopted: advanced, Note: note,
	}
	// Recorded either way. Moving somebody's working checkout is the most visible
	// thing the plane does to a machine it does not own, and a refusal to do it is
	// just as much a part of the record as the move.
	if err := s.store.LogDecision("project", project,
		EventMeta{Kind: EventIntegration, Actor: caller.Actor()},
		fmt.Sprintf("adopt %s into %s: %s", shortSHA(target.SHA), project, note)); err != nil {
		log.Printf("control: recording the adoption of %s: %v", project, err)
	}
	if !advanced {
		// A REFUSAL, not a failure — and the note travels as the message, so the
		// operator reads advanceCheckoutBranch's own sentence ("commit or stash,
		// then …") rather than a status code. Unlike the branch step of an
		// integration, this IS the whole operation: there is no landing behind it
		// that succeeded, so reporting it as anything other than "no" would be a
		// claim that the branch moved.
		return res, &RejectionError{Reason: ReasonBranchNotAdvanced, Message: note, Entity: project}
	}
	return res, nil
}
