// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// recordingVerifier records whether Verify was called, the spec it received, and
// returns a fixed verdict.
type recordingVerifier struct {
	called bool
	pass   bool
	detail string
	spec   VerifySpec
}

func (r *recordingVerifier) Verify(_ context.Context, spec VerifySpec) VerifyOutcome {
	r.called = true
	r.spec = spec
	return VerifyOutcome{Passed: r.pass, Detail: r.detail}
}

// dispatchToCandidate creates a task and dispatches it to a candidate with a job
// whose diff writes the given marker file (use a *_test.go name to trip the gate).
func dispatchToCandidate(t *testing.T, marker string, verifier VerifyRunner) (*Service, *Store, Task) {
	t.Helper()
	repo := gitRepo(t)
	runner := StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: marker}
	svc, _, store := newService(t, mapResolver{"app": repo}, runner, nil, verifier)
	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "do work"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	got, _ := store.GetTask(task.ID)
	if got.State != StateCandidate {
		t.Fatalf("precondition: task should be candidate, got %s", got.State)
	}
	return svc, store, got
}

func TestVerify_GateClean_Passes(t *testing.T) {
	rv := &recordingVerifier{pass: true, detail: "clean checkout ok"}
	svc, store, task := dispatchToCandidate(t, "AGENT_RAN.txt", rv) // non-test marker → gate clean

	res, err := svc.VerifyTask(task.ID, VerifyRequest{})
	if err != nil {
		t.Fatalf("VerifyTask: %v", err)
	}
	if !rv.called {
		t.Error("verifier should be called when the gate is clean")
	}
	if !res.Verified || res.GateTouched {
		t.Errorf("expected verified, got verified=%v gate=%v", res.Verified, res.GateTouched)
	}
	gotTask, _ := store.GetTask(task.ID)
	if gotTask.State != StateVerified {
		t.Errorf("task state = %s, want verified", gotTask.State)
	}
	if res.Job.State != StateVerified {
		t.Errorf("job state = %s, want verified", res.Job.State)
	}
	if res.Artifact == nil || res.Artifact.Verify != VerifyPass {
		t.Errorf("artifact verify = %v, want pass", res.Artifact)
	}
}

func TestVerify_IntegrityGate_RejectsWithoutVerifier(t *testing.T) {
	rv := &recordingVerifier{pass: true} // would pass — but must never be called
	svc, store, task := dispatchToCandidate(t, "sneaky_test.go", rv)

	res, err := svc.VerifyTask(task.ID, VerifyRequest{})
	if err != nil {
		t.Fatalf("VerifyTask: %v", err)
	}
	if rv.called {
		t.Error("integrity gate must short-circuit BEFORE the verifier — it was called")
	}
	if !res.GateTouched {
		t.Error("expected the integrity gate to trip")
	}
	if res.Verified {
		t.Error("a gate-tripped job must not be verified")
	}
	if len(res.TouchedFiles) == 0 {
		t.Error("expected the touched files to be reported")
	}
	gotTask, _ := store.GetTask(task.ID)
	if gotTask.State != StateRejected {
		t.Errorf("task state = %s, want rejected", gotTask.State)
	}
	if res.Job.State != StateRejected {
		t.Errorf("job state = %s, want rejected", res.Job.State)
	}
	if res.Artifact != nil && res.Artifact.Verify != VerifyFail {
		t.Errorf("artifact verify = %v, want fail", res.Artifact.Verify)
	}
}

func TestVerify_VerifierFail_Rejects(t *testing.T) {
	rv := &recordingVerifier{pass: false, detail: "tests failed in clean checkout"}
	svc, store, task := dispatchToCandidate(t, "AGENT_RAN.txt", rv) // gate clean

	res, err := svc.VerifyTask(task.ID, VerifyRequest{})
	if err != nil {
		t.Fatalf("VerifyTask: %v", err)
	}
	if !rv.called {
		t.Error("verifier should be called when the gate is clean")
	}
	if res.Verified || res.GateTouched {
		t.Errorf("expected verifier-driven rejection, got verified=%v gate=%v", res.Verified, res.GateTouched)
	}
	gotTask, _ := store.GetTask(task.ID)
	if gotTask.State != StateRejected {
		t.Errorf("task state = %s, want rejected", gotTask.State)
	}
}

func TestVerify_RejectThenRetryDispatch(t *testing.T) {
	rv := &recordingVerifier{pass: false}
	svc, store, task := dispatchToCandidate(t, "AGENT_RAN.txt", rv)
	if _, err := svc.VerifyTask(task.ID, VerifyRequest{}); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// Rejected → a retry dispatch is allowed (rejected → queued → working).
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("retry dispatch after rejection: %v", err)
	}
	got, _ := store.GetTask(task.ID)
	if got.State != StateCandidate {
		t.Errorf("after retry, task = %s, want candidate", got.State)
	}
	// A second job now exists.
	jobs, _ := store.ListJobsForTask(task.ID)
	if len(jobs) != 2 {
		t.Errorf("want 2 jobs after retry, got %d", len(jobs))
	}
}

func TestVerify_NotCandidate_Rejected(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)
	task, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"}) // planned
	if _, err := svc.VerifyTask(task.ID, VerifyRequest{}); err == nil {
		t.Error("verifying a planned (non-candidate) task should error")
	}
	_ = store
}

// --- freeze semantics ---------------------------------------------------------

func TestAcceptanceHash_FrozenAtCreate(t *testing.T) {
	repo := gitRepo(t)
	// Commit an initial verify policy so base_sha carries it.
	commitFile(t, repo, ".daedalus/verify.json",
		`{"checks":["go test ./..."],"acceptanceGlobs":["**/*_test.go"]}`)

	svc, _, store := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)
	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	frozen := task.AcceptanceHash
	if frozen == "" {
		t.Fatal("acceptance hash should be captured at create")
	}
	// The frozen hash matches the policy AS COMMITTED at base_sha.
	base, _ := ReadHeadSHA(repo)
	atBase, _ := ReadAcceptancePolicyAt(repo, base)
	if frozen != atBase.Hash() {
		t.Errorf("frozen hash %s != policy@base %s", frozen, atBase.Hash())
	}

	// Now EDIT the working-tree policy (uncommitted) to something different.
	if err := os.WriteFile(filepath.Join(repo, ".daedalus", "verify.json"),
		[]byte(`{"checks":["echo cheat"],"acceptanceGlobs":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// The stored task hash is unchanged — freeze holds.
	reread, _ := store.GetTask(task.ID)
	if reread.AcceptanceHash != frozen {
		t.Errorf("stored hash changed after a working-tree edit: %s → %s", frozen, reread.AcceptanceHash)
	}
	// The working-tree policy now hashes differently (proving the edit took).
	wt, _ := ReadAcceptancePolicy(repo)
	if wt.Hash() == frozen {
		t.Error("working-tree edit should change the working-tree hash (test setup issue)")
	}
	// Reading AT base_sha still yields the frozen value (immutable commit).
	atBaseAfter, _ := ReadAcceptancePolicyAt(repo, base)
	if atBaseAfter.Hash() != frozen {
		t.Error("policy@base_sha must be immutable to working-tree edits")
	}
}
