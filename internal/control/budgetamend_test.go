// Copyright (C) 2026 Techdelight BV

package control

import (
	"errors"
	"strings"
	"testing"
)

// AN EXHAUSTED TASK IS RECOVERABLE WITHOUT DESTROYING IT (#95 item 4).
//
// Two tasks died on 2026-08-25 for want of this. The envelope is frozen at
// create so nothing can widen the bound on its own work — right — and the
// operator could not move it either, so the only route was cancel-and-recreate,
// which throws away the history, the reviews, the rationale, and the attempt
// count itself: the one number that would justify a different ceiling next time.
func TestAmendBudget_RaisesAnExhaustedTaskWithoutRecreatingIt(t *testing.T) {
	repo := gitRepo(t)
	// A failing verifier, so the one attempt ends in `rejected` — the state the
	// retry ladder starts from, and where an operator meets the wall.
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, StubVerifyRunner{Pass: false})
	svc.SetPolicySource(StaticBudget(Budget{MaxAttempts: 5, MaxReviewCycles: 3}))

	task, err := svc.CreateTask(CreateTaskRequest{
		Project: "app", Objective: "x", Budget: &Budget{MaxAttempts: 1},
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if _, err := svc.VerifyTask(task.ID, VerifyRequest{}); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// The wall the operator hits.
	_, err = svc.RetryTask(task.ID, RetryRequest{})
	var rej *RejectionError
	if !errors.As(err, &rej) || rej.Reason != ReasonAttemptsExhausted {
		t.Fatalf("retry = %v, want attempts_exhausted — the fixture needs the stuck state", err)
	}

	updated, err := svc.AmendTaskBudget(task.ID, AmendBudgetRequest{MaxAttempts: 3})
	if err != nil {
		t.Fatalf("AmendTaskBudget: %v", err)
	}
	if updated.Budget.MaxAttempts != 3 {
		t.Errorf("attempts = %d, want 3", updated.Budget.MaxAttempts)
	}
	// Untouched axes stay as they were: this raises a limit, it does not replace
	// the envelope.
	if updated.Budget.WallClockSeconds != task.Budget.WallClockSeconds {
		t.Errorf("wall clock moved: %d → %d", task.Budget.WallClockSeconds, updated.Budget.WallClockSeconds)
	}
	// Durable, and the task is whole: same id, same objective, same history.
	reread, _ := store.GetTask(task.ID)
	if reread.Budget.MaxAttempts != 3 || reread.Objective != "x" {
		t.Errorf("the amendment did not survive, or the task did not: %+v", reread)
	}
	// And the wall is gone.
	if _, err := svc.RetryTask(task.ID, RetryRequest{}); err != nil {
		t.Fatalf("retry after the amendment: %v", err)
	}

	// RECORDED WITH ITS LINEAGE, both the before and the after. A budget that
	// changed silently explains nothing later.
	events, _ := store.ListEventsForTask(task.ID)
	var noted bool
	for _, e := range events {
		if strings.Contains(e.Note, "budget amended by hand") && strings.Contains(e.Note, "1 → 3") {
			noted = true
		}
	}
	if !noted {
		t.Error("the amendment is not in the record with what it was before")
	}
}

// AN AGENT MAY NOT AMEND ITS OWN ENVELOPE, and may not propose it either.
// A bound an agent can ask to move is a negotiation, and §6's whole claim is
// that it is not one.
func TestAmendBudget_RefusesAnAgentOutright(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)
	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	_, err = svc.WithCaller(Agent()).AmendTaskBudget(task.ID, AmendBudgetRequest{MaxAttempts: 99})
	var rej *RejectionError
	if !errors.As(err, &rej) {
		t.Fatalf("agent amendment = %v, want a typed refusal", err)
	}
	if rej.Reason == ReasonProposalRecorded {
		t.Fatal("an agent's budget amendment was recorded as a PROPOSAL — a bound a human can be " +
			"asked to widen on the agent's behalf is still a negotiation")
	}
	if rej.Reason != ReasonForbidden {
		t.Errorf("reason = %q, want %q", rej.Reason, ReasonForbidden)
	}
	// Nothing moved.
	after, _ := svc.store.GetTask(task.ID)
	if after.Budget.MaxAttempts == 99 {
		t.Error("the agent's amendment took effect")
	}
}

// THE PROJECT CEILING STILL BINDS. This moves a Task within the envelope the
// operator has already set; raising THAT is an edit to the host-side policy,
// which is where authority over spend lives.
func TestAmendBudget_NeverExceedsTheProjectCeiling(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)
	svc.SetPolicySource(StaticBudget(Budget{MaxAttempts: 2, MaxReviewCycles: 2}))

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	_, err = svc.AmendTaskBudget(task.ID, AmendBudgetRequest{MaxAttempts: 10})
	var rej *RejectionError
	if !errors.As(err, &rej) || rej.Reason != ReasonOverBudget {
		t.Fatalf("amendment above the ceiling = %v, want an over_budget refusal", err)
	}
	if !strings.Contains(err.Error(), "host-side") {
		t.Errorf("the refusal does not name where the ceiling is raised: %v", err)
	}
	// Up to the ceiling is fine.
	if _, err := svc.AmendTaskBudget(task.ID, AmendBudgetRequest{MaxAttempts: 2}); err != nil {
		t.Errorf("an amendment AT the ceiling was refused: %v", err)
	}
}

// A finished task has nothing to spend, and an operator raising its budget has
// misunderstood something. Said now rather than accepted as a no-op.
func TestAmendBudget_RefusesATerminalTask(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)
	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.CancelTask(task.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if _, err := svc.AmendTaskBudget(task.ID, AmendBudgetRequest{MaxAttempts: 5}); err == nil {
		t.Error("a cancelled task accepted a budget amendment")
	}
	// …and asking for nothing is an error, not a silent success.
	if _, err := svc.AmendTaskBudget(task.ID, AmendBudgetRequest{}); err == nil {
		t.Error("an empty amendment was accepted")
	}
}
