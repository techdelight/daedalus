// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"fmt"
	"log"
)

// The independent reviewer pass (docs/guild-master-plan.md §6, rung 5 of the
// defence ladder).
//
// Verification answers "does this committed artifact, in this pinned
// environment, make the frozen procedure report success". Review answers a
// different question — "is this change *acceptable*" — which tests cannot, and
// which §12 is blunt about: tests are an incomplete oracle, so "verified" is
// evidence and not proof. A reviewer is another rung, not a replacement.
//
// It is a seam, exactly as VerifyRunner was in Sprint 56: a stub here, a real
// implementation later. Nothing in this package assumes the reviewer is an agent,
// a human, or a linter — only that it returns a verdict on a committed artifact.

// ReviewSpec is the input to a ReviewRunner: the committed artifact to review and
// the context needed to look at it. Like VerifySpec it names commits, never a
// mutable working tree, so a reviewer cannot be shown something different from
// what would land.
type ReviewSpec struct {
	TaskID    string
	JobID     string
	Project   string
	RepoDir   string
	BaseSHA   string
	HeadSHA   string
	Branch    string
	Objective string // what the Task set out to do, for judging fitness
}

// ReviewOutcome is a ReviewRunner's verdict.
type ReviewOutcome struct {
	Passed bool
	Detail string
}

// ReviewRunner performs an independent review of a committed artifact. It is
// injectable so the control-plane logic (gating, budget, transitions) is
// host-tested without one, and so a real reviewer can be added without touching
// any of that logic.
type ReviewRunner interface {
	Review(ctx context.Context, spec ReviewSpec) ReviewOutcome
}

// StubReviewRunner is a dependency-free ReviewRunner returning a fixed verdict —
// the Sprint-56 StubVerifyRunner pattern. Exported so the daemon can select it
// via DAEDALUS_CONTROL_FAKE_REVIEW and so tests can drive both outcomes.
type StubReviewRunner struct {
	Pass   bool
	Detail string
}

// Review implements ReviewRunner.
func (r StubReviewRunner) Review(_ context.Context, _ ReviewSpec) ReviewOutcome {
	detail := r.Detail
	if detail == "" {
		if r.Pass {
			detail = "stub reviewer: pass"
		} else {
			detail = "stub reviewer: fail"
		}
	}
	return ReviewOutcome{Passed: r.Pass, Detail: detail}
}

// SetReviewRunner installs the independent reviewer. Nil means no reviewer is
// configured, and review is then not a gate at all — governance stays opt-in
// (§9), so a project that has not asked for a reviewer is not blocked by one.
func (s *Service) SetReviewRunner(r ReviewRunner) { s.reviewer = r }

// ReviewResult is the outcome of a plane-owned review pass.
type ReviewResult struct {
	Task     Task            `json:"task"`
	Artifact *Artifact       `json:"artifact,omitempty"`
	Passed   bool            `json:"passed"`
	Reason   RejectionReason `json:"reason,omitempty"`
	Detail   string          `json:"detail"`
	Cycles   int             `json:"cycles"`    // review passes used
	MaxCycle int             `json:"maxCycles"` // 0 = unbounded
}

// ReviewTask runs the independent reviewer over a Task's verified artifact.
//
// It is deliberately a separate, explicit operation rather than something folded
// into verification: the two answer different questions, they can fail for
// different reasons, and a reviewer may be slow or expensive. A failed review
// routes the Task to `rejected` with reason review_failed, which feeds the
// existing retry/replan ladder.
func (s *Service) ReviewTask(id string) (ReviewResult, error) {
	if s.reviewer == nil {
		return ReviewResult{}, fmt.Errorf("control: no reviewer configured for this control plane")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	task, err := s.store.GetTask(id)
	if err != nil {
		return ReviewResult{}, err
	}
	// Reviewable once verification has passed and before the artifact lands:
	// `verified` (no approval required) or `approval_required`/`approved`.
	if task.State != StateVerified && task.State != StateApprovalRequired && task.State != StateApproved {
		return ReviewResult{}, fmt.Errorf("%w: task %s is %s, not reviewable (want verified/approval_required/approved)",
			ErrWrongState, id, task.State)
	}
	job, ok, err := s.jobInState(id, StateVerified)
	if err != nil {
		return ReviewResult{}, err
	}
	if !ok {
		return ReviewResult{}, fmt.Errorf("%w: task %s has no verified job to review", ErrWrongState, id)
	}
	art := s.firstArtifact(job.ID)
	if art == nil {
		return ReviewResult{}, fmt.Errorf("%w: task %s has no artifact to review", ErrWrongState, id)
	}
	repoDir, err := s.projects.ProjectDir(task.Project)
	if err != nil {
		return ReviewResult{}, err
	}

	// Bounded by the max-review-cycles budget. The SAME LIMIT is applied to
	// verification cycles and to review passes, but they are counted SEPARATELY
	// and not summed — a task is allowed N verifications and N reviews, not N of
	// both combined. Stated explicitly because "the existing budget" could
	// reasonably be read either way, and a silent shared counter would make a
	// verification failure quietly consume a review the operator was expecting.
	used, err := s.store.CountReviewRuns(id)
	if err != nil {
		return ReviewResult{}, err
	}
	max := task.Budget.MaxReviewCycles
	if max > 0 && used >= max {
		return ReviewResult{}, s.refuse("task", id, EventBudget, ReasonReviewCyclesExhausted, fmt.Sprintf(
			"task %s has used all %d review pass(es); the artifact is unchanged — cancel it or raise the project budget",
			id, max))
	}

	spec := ReviewSpec{
		TaskID: id, JobID: job.ID, Project: task.Project, RepoDir: repoDir,
		BaseSHA: art.BaseSHA, HeadSHA: art.HeadSHA,
		Branch: BranchName(id, job.ID), Objective: task.Objective,
	}
	// Released across the reviewer for the same reason as the verifier: it may be
	// slow, and cancel/reconcile must stay responsive. Both the claim and the
	// unlock are scoped helpers (claim.go), so a panic in the reviewer cannot leak
	// either.
	var outcome ReviewOutcome
	if err := s.withClaim(id, inflightOp{kind: "review", jobID: job.ID, project: task.Project}, func() error {
		s.unlockedDuring(func() { outcome = s.reviewer.Review(context.Background(), spec) })
		return nil
	}); err != nil {
		return ReviewResult{}, err
	}

	// Record the pass itself before acting on it, so the budget counts a review
	// that ran even if the verdict handling then fails.
	if err := s.store.LogDecision("task", id,
		EventMeta{Kind: EventReview}, fmt.Sprintf("review pass %d: passed=%v (%s)", used+1, outcome.Passed, outcome.Detail)); err != nil {
		log.Printf("control: logging review of %s: %v", id, err)
	}

	res := ReviewResult{Passed: outcome.Passed, Detail: outcome.Detail, Cycles: used + 1, MaxCycle: max}

	if !outcome.Passed {
		note := "independent review rejected the artifact: " + outcome.Detail
		if a, err := s.store.SetArtifactReview(art.ID, ReviewFail); err == nil {
			art = &a
		}
		meta := EventMeta{Kind: EventRejection, Reason: ReasonReviewFailed}
		s.driveJob(job.ID, []State{StateRejected}, meta, note)
		tk, err := s.store.TransitionTaskWith(id, StateRejected, false, meta, note)
		if err != nil {
			return ReviewResult{}, err
		}
		if s.worktrees != nil {
			_ = s.worktrees.Remove(repoDir, job.ID)
		}
		res.Task, res.Artifact, res.Reason = tk, art, ReasonReviewFailed
		return res, nil
	}

	if a, err := s.store.SetArtifactReview(art.ID, ReviewPass); err == nil {
		art = &a
	}
	tk, _ := s.store.GetTask(id)
	res.Task, res.Artifact = tk, art
	return res, nil
}

// requireReviewPassed gates integration on the independent review when a reviewer
// is configured. s.mu must be held.
//
// When no reviewer is configured this is a no-op: review is opt-in, and a plane
// with no reviewer must not block every landing forever.
func (s *Service) requireReviewPassed(task Task, art *Artifact) error {
	if s.reviewer == nil || art == nil {
		return nil
	}
	switch art.Review {
	case ReviewPass:
		return nil
	case ReviewFail:
		return s.refuse("task", task.ID, EventRejection, ReasonReviewRequired, fmt.Sprintf(
			"artifact %s failed independent review; it cannot be integrated", art.ID))
	default:
		return s.refuse("task", task.ID, EventRejection, ReasonReviewRequired, fmt.Sprintf(
			"artifact %s has not been reviewed (run `daedalus task review %s`)", art.ID, task.ID))
	}
}
