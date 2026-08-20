// Copyright (C) 2026 Techdelight BV

package control

import (
	"strings"
	"testing"
)

// TestVerify_IgnoreResult_WaivesWithoutClaimingAPass is the whole design of the
// waiver in one test: the operator gets to proceed, and nothing in the record
// says the artifact passed.
//
// The two halves are asserted separately because either alone would be a
// different feature. That the Task reaches the approval gate is what the flag is
// FOR. That the Task is never `verified` and the artifact still reads
// verify=fail is what keeps the log honest — approval, integration and
// dependency satisfaction all read `verified` as "the plane applied its oracle
// and it passed", and a waiver that wrote it would make that false everywhere.
func TestVerify_IgnoreResult_WaivesWithoutClaimingAPass(t *testing.T) {
	rv := &recordingVerifier{pass: false, detail: "check 2/2 failed: the check itself is wrong"}
	svc, store, task := dispatchToCandidate(t, "AGENT_RAN.txt", rv)

	res, err := svc.VerifyTask(task.ID, VerifyRequest{IgnoreResult: true})
	if err != nil {
		t.Fatalf("VerifyTask: %v", err)
	}

	if !res.Waived {
		t.Fatal("the result should be reported as waived")
	}
	if res.Verified {
		t.Error("a waived artifact must never be reported as verified")
	}
	// It moved on…
	got, _ := store.GetTask(task.ID)
	if got.State != StateApprovalRequired {
		t.Errorf("task state = %s, want approval_required — the waiver exists to let work proceed", got.State)
	}
	// …without anything claiming it passed.
	if got.State == StateVerified {
		t.Error("a waived task reached `verified`, which is the one thing it must never do")
	}
	arts, _ := store.ListArtifactsForJob(res.Job.ID)
	if len(arts) == 0 {
		t.Fatal("expected an artifact")
	}
	if arts[0].Verify != VerifyFail {
		t.Errorf("artifact verify = %q, want fail — the finding stands", arts[0].Verify)
	}

	// Both facts are in the log, in that order: the failure, then the override.
	events, err := store.ListEventsForTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var rejectedAt, waivedAt = -1, -1
	for i, e := range events {
		if e.Kind == EventRejection && e.Reason == ReasonVerifyFailed {
			rejectedAt = i
		}
		if e.Kind == EventWaiver || strings.Contains(e.Note, "WAIVED") {
			waivedAt = i
		}
	}
	if rejectedAt < 0 {
		t.Error("the rejection must stay on the record — a waiver adds a fact, it does not erase one")
	}
	if waivedAt < 0 {
		t.Error("the waiver must be recorded")
	}
	if rejectedAt >= 0 && waivedAt >= 0 && waivedAt < rejectedAt {
		t.Error("the waiver is recorded before the failure it waives; the log should read finding-then-override")
	}
}

// TestVerify_IgnoreResult_IsANoOpOnAPass — waiving something that passed should
// change nothing at all, least of all the state it reaches.
func TestVerify_IgnoreResult_IsANoOpOnAPass(t *testing.T) {
	rv := &recordingVerifier{pass: true}
	svc, store, task := dispatchToCandidate(t, "AGENT_RAN.txt", rv)

	res, err := svc.VerifyTask(task.ID, VerifyRequest{IgnoreResult: true})
	if err != nil {
		t.Fatalf("VerifyTask: %v", err)
	}
	if res.Waived {
		t.Error("nothing was waived: the verification passed")
	}
	if !res.Verified {
		t.Error("a passing verification must still verify when the flag is set")
	}
	if got, _ := store.GetTask(task.ID); got.State == StateRejected {
		t.Error("a passing verification was rejected")
	}
}

// TestVerify_IgnoreResult_ForbiddenForAgents is the guard that matters most. An
// agent that could waive its own grading has no oracle at all — every other
// protection in the system routes through a verdict it would then be able to set
// aside by itself.
func TestVerify_IgnoreResult_ForbiddenForAgents(t *testing.T) {
	rv := &recordingVerifier{pass: false, detail: "nope"}
	svc, store, task := dispatchToCandidate(t, "AGENT_RAN.txt", rv)

	agent := svc.WithCaller(Agent())
	_, err := agent.VerifyTask(task.ID, VerifyRequest{IgnoreResult: true})
	if err == nil {
		t.Fatal("an agent must not be able to waive a verification result")
	}
	var rej *RejectionError
	if !asRejection(err, &rej) || rej.Reason != ReasonForbidden {
		t.Fatalf("want a typed forbidden rejection, got %T: %v", err, err)
	}
	// And crucially it must be a refusal, not a PROPOSAL — a proposal would put the
	// agent's own waiver in front of a human as a routine confirmation.
	if rej.Reason == ReasonProposalRecorded {
		t.Error("a waiver must not be proposable: confirming it would launder the agent's authority")
	}
	if got, _ := store.GetTask(task.ID); got.State == StateApprovalRequired {
		t.Error("the task advanced despite the refusal")
	}
}

// asRejection is errors.As specialised, kept local so the test reads as one idea.
func asRejection(err error, target **RejectionError) bool {
	for err != nil {
		if r, ok := err.(*RejectionError); ok {
			*target = r
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// TestWaiver_CanActuallyLand is the other half of the waiver, and it was missing.
//
// `verify --ignore-result` records the failure honestly and moves the Task to the
// approval gate on a named human's authority — but integration looked for a Job
// in `verified`, and a waived Job is in `approval_required` and must never be in
// `verified` (writing that would put a false statement into a log that approval
// and dependency satisfaction read as true). So the override got a human to the
// gate and then refused to let them through: the legitimate path missing exactly
// where the override was supposed to be.
//
// The waiver is checked, not inferred from the state. A Job standing in
// `approval_required` for any other reason has nobody's name against it.
func TestWaiver_CanActuallyLand(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil,
		StubVerifyRunner{Pass: false, Detail: "the linter is broken, not the change"})

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatal(err)
	}
	res, err := svc.VerifyTask(task.ID, VerifyRequest{IgnoreResult: true})
	if err != nil {
		t.Fatalf("waived verify: %v", err)
	}
	if !res.Waived || res.Verified {
		t.Fatalf("res = %+v, want waived and NOT verified", res)
	}
	// The artifact still says it failed. A waiver changes who is answerable, not
	// what is true.
	if res.Artifact != nil && res.Artifact.Verify == VerifyPass {
		t.Error("the waiver marked a failing artifact as passing")
	}

	if _, err := svc.ApproveTask(task.ID, "the check is wrong, I checked by hand"); err != nil {
		t.Fatalf("approve after waiver: %v", err)
	}
	if _, err := svc.IntegrateTask(task.ID, IntegrateRequest{}); err != nil {
		t.Fatalf("integrate after a waiver = %v; the waiver led to a gate with no way through", err)
	}
	got, _ := store.GetTask(task.ID)
	if got.State != StateIntegrated {
		t.Errorf("state = %q, want integrated", got.State)
	}
}

// TestUnwaivedJobCannotLandOnItsStateAlone: the guard above must key on the
// WAIVER and not on the state, or any Job that reached the approval gate could
// land without ever having been verified.
func TestUnwaivedJobCannotLandOnItsStateAlone(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil,
		StubVerifyRunner{Pass: true})

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatal(err)
	}
	job, ok, err := svc.jobInState(task.ID, StateCandidate)
	if err != nil || !ok {
		t.Fatalf("no candidate job: %v", err)
	}
	// Put the Job at the approval gate WITHOUT a verification and without a
	// waiver — the shape the guard must refuse.
	if _, err := store.TransitionJob(job.ID, StateVerifying, false, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionJob(job.ID, StateVerified, false, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionJob(job.ID, StateApprovalRequired, false, "test"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := svc.jobToIntegrate(task.ID); err != nil || ok {
		t.Errorf("jobToIntegrate found a job with no waiver against it (ok=%v, err=%v)", ok, err)
	}
}
