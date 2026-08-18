// Copyright (C) 2026 Techdelight BV

package control

import (
	"strings"
	"testing"
)

// TestAmendChecks_FixesABrokenCheckWithoutANewTask is the case this exists for:
// a check aimed at the wrong thing can never pass, and before this the Task had
// to be abandoned and recreated — losing its history and its budget to a typo.
func TestAmendChecks_FixesABrokenCheckWithoutANewTask(t *testing.T) {
	rv := &recordingVerifier{pass: false}
	svc, store, task := dispatchToCandidate(t, "AGENT_RAN.txt", rv)
	if _, err := svc.VerifyTask(task.ID, VerifyRequest{}); err != nil {
		t.Fatalf("verify: %v", err)
	}

	updated, err := svc.AmendTaskChecks(task.ID, AmendChecksRequest{
		Checks: []string{"test -f README.md"},
	})
	if err != nil {
		t.Fatalf("AmendTaskChecks: %v", err)
	}
	if len(updated.Checks) != 1 || updated.Checks[0] != "test -f README.md" {
		t.Errorf("checks = %v, want the amended one", updated.Checks)
	}
	// Durable, not just returned.
	reread, _ := store.GetTask(task.ID)
	if len(reread.Checks) != 1 {
		t.Errorf("re-read checks = %v, want the amended set", reread.Checks)
	}
	// And the lineage is on the record — old set → new set.
	events, _ := store.ListEventsForTask(task.ID)
	var found bool
	for _, e := range events {
		if e.Kind == EventChecksAmend && strings.Contains(e.Note, "→") {
			found = true
		}
	}
	if !found {
		t.Error("no amendment event with a before→after lineage; an unrecorded amendment is the one outcome that must be impossible")
	}
}

// TestAmendChecks_ForbiddenForAgents. The rule is not new — an agent may not
// supply checks at create either — but amending is supplying, so it must be the
// same door with the same lock. Note this is a REFUSAL, not a proposal: a
// proposal would let the agent author a command and have a human wave it into
// the verifier.
func TestAmendChecks_ForbiddenForAgents(t *testing.T) {
	rv := &recordingVerifier{pass: true}
	svc, store, task := dispatchToCandidate(t, "AGENT_RAN.txt", rv)

	agent := svc.WithCaller(Agent())
	_, err := agent.AmendTaskChecks(task.ID, AmendChecksRequest{Checks: []string{"true"}})
	if err == nil {
		t.Fatal("an agent must not amend per-task checks")
	}
	var rej *RejectionError
	if !asRejection(err, &rej) {
		t.Fatalf("want a typed rejection, got %T: %v", err, err)
	}
	if rej.Reason != ReasonForbidden {
		t.Errorf("reason = %q, want %q — and NOT proposal_recorded", rej.Reason, ReasonForbidden)
	}
	if got, _ := store.GetTask(task.ID); len(got.Checks) != 0 {
		t.Errorf("checks changed despite the refusal: %v", got.Checks)
	}
}

// TestAmendChecks_RefusedWhileVerifying — changing the criteria while they are
// being applied is a race with no correct answer.
func TestAmendChecks_RefusedInWrongStates(t *testing.T) {
	rv := &recordingVerifier{pass: true}
	svc, store, task := dispatchToCandidate(t, "AGENT_RAN.txt", rv)
	if _, err := svc.VerifyTask(task.ID, VerifyRequest{}); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// Now `verified` (or approval_required): past the point where amending is
	// coherent — the record would show a pass against criteria no longer carried.
	got, _ := store.GetTask(task.ID)
	if got.State != StateVerified && got.State != StateApprovalRequired {
		t.Fatalf("precondition: want verified/approval_required, got %s", got.State)
	}
	if _, err := svc.AmendTaskChecks(task.ID, AmendChecksRequest{Checks: []string{"true"}}); err == nil {
		t.Errorf("amending a %s task should be refused", got.State)
	}
}

// TestAmendChecks_WithdrawsTheFreeReplay is the accounting guard, and the reason
// amendable checks do not become an unlimited re-roll.
//
// A plain re-verification is free, because a verdict from a broken harness judged
// nothing. Once a check has MOVED that reasoning is gone: the next grading is a
// new grading against a different oracle, and it is charged like any other.
// Without this, an operator could soften a check, replay for free, and repeat
// until something passed, with the budget never noticing.
func TestAmendChecks_WithdrawsTheFreeReplay(t *testing.T) {
	sv := &sequenceVerifier{verdicts: []VerifyOutcome{{Passed: false}, {Passed: true}}}
	svc, store, task := dispatchToCandidate(t, "AGENT_RAN.txt", sv)

	if _, err := svc.VerifyTask(task.ID, VerifyRequest{}); err != nil {
		t.Fatal(err)
	}
	spent, _ := store.CountReviewCycles(task.ID)
	if spent != 1 {
		t.Fatalf("precondition: want 1 cycle spent, got %d", spent)
	}

	if _, err := svc.AmendTaskChecks(task.ID, AmendChecksRequest{Checks: []string{"true"}}); err != nil {
		t.Fatalf("AmendTaskChecks: %v", err)
	}
	if _, err := svc.ReverifyTask(task.ID, ReverifyRequest{}); err != nil {
		t.Fatalf("ReverifyTask: %v", err)
	}

	after, _ := store.CountReviewCycles(task.ID)
	if after != 2 {
		t.Errorf("review cycles = %d, want 2 — a replay after an amendment is a new grading "+
			"against a changed oracle and must be charged", after)
	}
}
