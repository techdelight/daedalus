// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"errors"
	"testing"
)

// recordingReviewer records the spec it was given and returns a fixed verdict.
type recordingReviewer struct {
	called bool
	pass   bool
	detail string
	spec   ReviewSpec
}

func (r *recordingReviewer) Review(_ context.Context, spec ReviewSpec) ReviewOutcome {
	r.called = true
	r.spec = spec
	out := ReviewOutcome{Passed: r.pass, Detail: r.detail, Reasoning: r.detail, Reviewer: "recording"}
	if !r.pass {
		out.Findings = []Finding{
			{Severity: SeverityBlocking, File: "a.txt", Line: 3, What: r.detail, Why: "it is already in helpers.go"},
			{Severity: SeverityNote, What: "no test covers the new branch"},
		}
	}
	return out
}

// TestReview_DoesNotGateIntegration.
//
// Until M20 a configured reviewer blocked integration until it had passed. That
// gave a language model a veto over a human's work — and, worse in the other
// direction, made its PASS load-bearing, so a diff that talked the reviewer round
// would have talked its way into the trunk. The judgement is evidence now; the
// human at the approval gate is the gate.
func TestReview_DoesNotGateIntegration(t *testing.T) {
	repo := gitRepo(t)
	rev := &recordingReviewer{pass: true, detail: "reads fine"}
	svc, store, task := verifiedTask(t, repo, StubVerifyRunner{Pass: true}, "a.txt")
	svc.SetReviewRunner(rev)

	// Unreviewed, and it lands: an artifact nobody has read is a decision for the
	// human, not a refusal from a component that has not run.
	if _, err := svc.IntegrateTask(task.ID, IntegrateRequest{}); err != nil {
		t.Fatalf("integrate without a review = %v, want it allowed", err)
	}
	got, _ := store.GetTask(task.ID)
	if got.State != StateIntegrated {
		t.Fatalf("state = %q, want integrated", got.State)
	}
}

// TestReview_ReportsAndDoesNotAct is the property the whole rung rests on.
//
// A failing review must record everything it found and move NOTHING. A reviewer
// that could reject is an oracle nobody bounded: nothing constrains it, nothing
// reproduces it, and the diff it reads is untrusted input that can address it
// directly.
func TestReview_ReportsAndDoesNotAct(t *testing.T) {
	repo := gitRepo(t)
	rev := &recordingReviewer{pass: false, detail: "this duplicates an existing helper"}
	svc, store, task := verifiedTask(t, repo, StubVerifyRunner{Pass: true}, "a.txt")
	svc.SetReviewRunner(rev)

	res, err := svc.ReviewTask(task.ID)
	if err != nil {
		t.Fatalf("ReviewTask: %v", err)
	}
	if !rev.called {
		t.Fatal("the reviewer was not called")
	}
	if res.Passed {
		t.Error("res.Passed = true, want the reviewer's actual verdict")
	}
	if res.Reason != "" {
		t.Errorf("res.Reason = %q, want empty — a review rejects nothing, so it has no rejection reason", res.Reason)
	}

	// NOTHING MOVED.
	got, _ := store.GetTask(task.ID)
	if got.State != StateVerified {
		t.Errorf("task state = %q, want it UNCHANGED at verified — the reviewer must not transition it", got.State)
	}
	if job, _ := s2job(t, store, task.ID); job.State != StateVerified {
		t.Errorf("job state = %q, want it unchanged at verified", job.State)
	}
	// …and the work is still there to be looked at. Reclaiming the worktree on a
	// model's opinion was the most expensive half of the old behaviour.
	if job, _ := s2job(t, store, task.ID); !svc.worktrees.Exists(job.ID) {
		t.Error("the worktree was reclaimed on a failed review; the artifact must survive the reading")
	}

	// EVERYTHING WAS RECORDED.
	if res.Artifact == nil || res.Artifact.Review != ReviewFail {
		t.Errorf("artifact review = %+v, want fail recorded", res.Artifact)
	}
	if len(res.Findings) != 2 || res.Findings[0].Severity != SeverityBlocking {
		t.Fatalf("findings = %+v, want the reviewer's two, blocking first", res.Findings)
	}
	if res.Findings[0].Why == "" || res.Findings[0].File == "" {
		t.Error("a finding must carry where to look and why it matters; without both it is an opinion")
	}
	if res.Reviewer != "recording" {
		t.Errorf("reviewer = %q, want it attributed — an unattributed verdict is a rumour", res.Reviewer)
	}

	stored, err := store.ReviewsForTask(task.ID)
	if err != nil || len(stored) != 1 {
		t.Fatalf("ReviewsForTask = %d (%v), want 1", len(stored), err)
	}
	if len(stored[0].Findings) != 2 || stored[0].Passed || stored[0].Reviewer != "recording" {
		t.Errorf("stored review = %+v, want the full judgement", stored[0])
	}

	// A second reading accumulates rather than overwriting: the earlier one is
	// part of the record.
	rev.pass = true
	if _, err := svc.ReviewTask(task.ID); err != nil {
		t.Fatalf("second ReviewTask: %v", err)
	}
	stored, _ = store.ReviewsForTask(task.ID)
	if len(stored) != 2 {
		t.Fatalf("%d reviews after two passes, want 2 — an earlier reading must not be overwritten", len(stored))
	}
	if !stored[1].Passed || stored[0].Passed {
		t.Errorf("stored order/verdicts = %+v, want the failing one first", stored)
	}

	// And the log carries it as a DECISION, never as a rejection.
	events, _ := store.ListEventsForTask(task.ID)
	if !hasKind(events, EventReview) {
		t.Error("the review pass itself should be recorded")
	}
	if hasEvent(events, EventRejection, ReasonReviewFailed) {
		t.Error("the log carries a review_failed rejection; nothing was rejected")
	}
}

// TestReview_SeesThePromiseAndTheReason: a diff answers "did it do the thing".
// Only the objective, the rationale and the programme let a reviewer ask whether
// the thing was worth doing — which is the question it exists for.
func TestReview_SeesThePromiseAndTheReason(t *testing.T) {
	repo := gitRepo(t)
	rev := &recordingReviewer{pass: true, detail: "fine"}
	svc, _, task := verifiedTask(t, repo, StubVerifyRunner{Pass: true}, "a.txt")
	svc.SetReviewRunner(rev)

	prog, err := svc.CreateProgramme(ProgrammeRequest{Name: "fluency", Description: "get fluent by spring"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.SetTaskProgramme(task.ID, prog.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.db.Exec(`UPDATE tasks SET rationale = ?, rationale_by = ? WHERE id = ?`,
		"the daily review hangs off it", string(CallerHuman), task.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.ReviewTask(task.ID); err != nil {
		t.Fatalf("ReviewTask: %v", err)
	}
	// Commits, never a working tree — a reviewer must not be shown something
	// different from what would land.
	if rev.spec.HeadSHA == "" || rev.spec.BaseSHA == "" {
		t.Errorf("spec names no commits: %+v", rev.spec)
	}
	if rev.spec.Objective != task.Objective {
		t.Errorf("objective = %q, want %q", rev.spec.Objective, task.Objective)
	}
	if rev.spec.Rationale != "the daily review hangs off it" || rev.spec.RationaleBy != CallerHuman {
		t.Errorf("rationale = %q by %q, want it carried through with its author",
			rev.spec.Rationale, rev.spec.RationaleBy)
	}
	if rev.spec.ProgrammeName != "fluency" || rev.spec.ProgrammeFor != "get fluent by spring" {
		t.Errorf("programme = %q/%q, want the name and what it is for",
			rev.spec.ProgrammeName, rev.spec.ProgrammeFor)
	}
}

// TestReview_NoReviewerSaysSo: asking for a review with no reviewer configured is
// an error rather than a silent pass — the one thing worse than no review is a
// record claiming one happened. Integration is unaffected either way now.
func TestReview_NoReviewerSaysSo(t *testing.T) {
	repo := gitRepo(t)
	svc, store, task := verifiedTask(t, repo, StubVerifyRunner{Pass: true}, "a.txt")
	if _, err := svc.ReviewTask(task.ID); err == nil {
		t.Error("ReviewTask with no reviewer should say so")
	}
	if _, err := svc.IntegrateTask(task.ID, IntegrateRequest{}); err != nil {
		t.Fatalf("integration must not be gated when no reviewer is configured: %v", err)
	}
	got, _ := store.GetTask(task.ID)
	if got.State != StateIntegrated {
		t.Errorf("state = %q, want integrated", got.State)
	}
}

// TestReview_BoundedByBudget: review passes are bounded by max-review-cycles, and
// counted SEPARATELY from verification cycles rather than summed.
func TestReview_BoundedByBudget(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil, StubVerifyRunner{Pass: true})
	svc.SetPolicySource(StaticBudget(Budget{MaxAttempts: 5, MaxReviewCycles: 1, Concurrency: 1}))
	svc.SetReviewRunner(StubReviewRunner{Pass: true})

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	// One verification cycle is consumed here…
	if _, err := svc.VerifyTask(task.ID, VerifyRequest{}); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// …and a review pass is still available, because the two are not summed.
	if _, err := svc.ReviewTask(task.ID); err != nil {
		t.Fatalf("first review should be allowed despite the verification cycle: %v", err)
	}
	// The second review is over budget.
	_, err = svc.ReviewTask(task.ID)
	var rej *RejectionError
	if !errors.As(err, &rej) || rej.Reason != ReasonReviewCyclesExhausted {
		t.Fatalf("second review = %v, want a review_cycles_exhausted refusal", err)
	}
	if n, _ := store.CountReviewRuns(task.ID); n != 1 {
		t.Errorf("review runs = %d, want 1 (a refused pass must not count)", n)
	}
}

func TestReview_Guards(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)
	svc.SetReviewRunner(StubReviewRunner{Pass: true})
	task, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"}) // planned
	if _, err := svc.ReviewTask(task.ID); !errors.Is(err, ErrWrongState) {
		t.Errorf("reviewing a planned task = %v, want ErrWrongState", err)
	}
	if _, err := svc.ReviewTask("T-404"); !errors.Is(err, ErrNotFound) {
		t.Errorf("reviewing an unknown task = %v, want ErrNotFound", err)
	}
}

func TestStubReviewRunner(t *testing.T) {
	for _, pass := range []bool{true, false} {
		out := StubReviewRunner{Pass: pass}.Review(context.Background(), ReviewSpec{})
		if out.Passed != pass || out.Detail == "" {
			t.Errorf("StubReviewRunner{%v} = %+v", pass, out)
		}
	}
}
