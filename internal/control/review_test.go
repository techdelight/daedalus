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
	return ReviewOutcome{Passed: r.pass, Detail: r.detail}
}

func TestReview_Pass_GatesIntegrationOpen(t *testing.T) {
	repo := gitRepo(t)
	rev := &recordingReviewer{pass: true, detail: "reads fine"}
	svc, store, task := verifiedTask(t, repo, StubVerifyRunner{Pass: true}, "a.txt")
	svc.SetReviewRunner(rev)

	// With a reviewer configured, integration is gated until the review runs.
	_, err := svc.IntegrateTask(task.ID, IntegrateRequest{})
	var rej *RejectionError
	if !errors.As(err, &rej) || rej.Reason != ReasonReviewRequired {
		t.Fatalf("integrate before review = %v, want a review_required refusal", err)
	}

	res, err := svc.ReviewTask(task.ID)
	if err != nil {
		t.Fatalf("ReviewTask: %v", err)
	}
	if !rev.called {
		t.Fatal("the reviewer was not called")
	}
	if !res.Passed || res.Reason != "" {
		t.Errorf("res = %+v, want a pass", res)
	}
	if res.Artifact == nil || res.Artifact.Review != ReviewPass {
		t.Errorf("artifact review = %+v, want pass", res.Artifact)
	}
	// The reviewer sees the committed artifact, never a working tree.
	if rev.spec.HeadSHA == "" || rev.spec.BaseSHA == "" || rev.spec.Objective != task.Objective {
		t.Errorf("review spec is incomplete: %+v", rev.spec)
	}
	// Now it lands.
	if _, err := svc.IntegrateTask(task.ID, IntegrateRequest{}); err != nil {
		t.Fatalf("IntegrateTask after review: %v", err)
	}
	got, _ := store.GetTask(task.ID)
	if got.State != StateIntegrated {
		t.Errorf("state = %q, want integrated", got.State)
	}
}

func TestReview_Fail_RejectsAndFeedsRetry(t *testing.T) {
	repo := gitRepo(t)
	rev := &recordingReviewer{pass: false, detail: "this duplicates an existing helper"}
	svc, store, task := verifiedTask(t, repo, StubVerifyRunner{Pass: true}, "a.txt")
	svc.SetReviewRunner(rev)

	res, err := svc.ReviewTask(task.ID)
	if err != nil {
		t.Fatalf("ReviewTask: %v", err)
	}
	if res.Passed || res.Reason != ReasonReviewFailed {
		t.Errorf("res = %+v, want a review_failed rejection", res)
	}
	if res.Artifact == nil || res.Artifact.Review != ReviewFail {
		t.Errorf("artifact review = %+v, want fail", res.Artifact)
	}
	got, _ := store.GetTask(task.ID)
	if got.State != StateRejected {
		t.Errorf("state = %q, want rejected", got.State)
	}
	if job, _ := s2job(t, store, task.ID); job.State != StateRejected {
		t.Errorf("job state = %q, want rejected", job.State)
	}
	// The Sprint-58 ladder picks it up.
	if _, err := svc.RetryTask(task.ID, RetryRequest{}); err != nil {
		t.Errorf("retry after a failed review: %v", err)
	}
	events, _ := store.ListEventsForTask(task.ID)
	if !hasEvent(events, EventRejection, ReasonReviewFailed) {
		t.Error("the rejection should carry review_failed")
	}
	if !hasKind(events, EventReview) {
		t.Error("the review pass itself should be recorded")
	}
}

// TestReview_NoReviewerIsNotAGate: review is opt-in, so a plane without one must
// not block every landing forever.
func TestReview_NoReviewerIsNotAGate(t *testing.T) {
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
