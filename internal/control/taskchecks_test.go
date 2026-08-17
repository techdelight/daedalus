// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// checkOrderVerifier captures the effective policy it was handed, and fails any
// check containing "FAIL" so a test can make one specific command the reason.
// (verify_test.go's recordingVerifier returns a fixed verdict; this one has to
// answer differently per check.)
type checkOrderVerifier struct {
	seen *[]string
}

func (r checkOrderVerifier) Verify(_ context.Context, spec VerifySpec) VerifyOutcome {
	*r.seen = append([]string(nil), spec.Policy.Checks...)
	for _, c := range spec.Policy.Checks {
		if strings.Contains(c, "FAIL") {
			return VerifyOutcome{Passed: false, Detail: "check failed: " + c}
		}
	}
	return VerifyOutcome{Passed: true, Detail: "all checks passed"}
}

// writeVerifyPolicy commits a project verify.json, so the project's own checks
// exist to be appended to.
func writeVerifyPolicy(t *testing.T, repo string, body string) {
	t.Helper()
	dir := filepath.Join(repo, ".daedalus")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "verify.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runGit(repo, "add", "-A"); err != nil {
		t.Fatalf("git add: %v %s", err, out)
	}
	if out, err := runGit(repo, "commit", "-m", "add verify policy"); err != nil {
		t.Fatalf("git commit: %v %s", err, out)
	}
}

// The ordering is the security property: the PROJECT's checks run first, against
// a checkout no task-supplied command has touched yet. A task check can only ever
// sabotage itself.
func TestTaskChecks_AppendedAfterTheProjectPolicy(t *testing.T) {
	repo := gitRepo(t)
	writeVerifyPolicy(t, repo, `{"checks":["project-check-1","project-check-2"],"acceptanceGlobs":["**/*_test.go"]}`)

	var seen []string
	svc, _, _ := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, checkOrderVerifier{seen: &seen})

	task, err := svc.CreateTask(CreateTaskRequest{
		Project:   "app",
		Objective: "add pagination",
		Checks:    []string{"task-check-A", "task-check-B"},
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	res, err := svc.VerifyTask(task.ID)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !res.Verified {
		t.Fatalf("expected a pass, got %+v", res)
	}

	want := []string{"project-check-1", "project-check-2", "task-check-A", "task-check-B"}
	if strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Errorf("verifier ran %v, want %v (project first, task appended)", seen, want)
	}
}

// A task check that fails rejects the artifact — which is the entire point:
// "delivered what was promised" now has an answer that is not a human's opinion.
func TestTaskChecks_AFailingTaskCheckRejectsTheArtifact(t *testing.T) {
	repo := gitRepo(t)
	writeVerifyPolicy(t, repo, `{"checks":["project-check"],"acceptanceGlobs":["**/*_test.go"]}`)

	var seen []string
	svc, _, _ := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, checkOrderVerifier{seen: &seen})

	task, err := svc.CreateTask(CreateTaskRequest{
		Project: "app", Objective: "add pagination",
		Checks: []string{"assert-pagination-FAIL"},
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	res, err := svc.VerifyTask(task.ID)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.Verified {
		t.Fatal("a failing task check must reject the artifact")
	}
	if res.Task.State != StateRejected {
		t.Errorf("task state = %s, want rejected", res.Task.State)
	}
}

// Per-task checks are commands executed inside the verifier, so the party being
// graded does not get to put them there. An agent asking is refused with a typed
// rejection, not silently ignored.
func TestTaskChecks_AgentCallerIsRefused(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{Result: ExecSuccess}, nil)

	_, err := svc.WithCaller(Agent()).CreateTask(CreateTaskRequest{
		Project: "app", Objective: "grade me leniently", Checks: []string{"true"},
	})
	if err == nil {
		t.Fatal("an agent supplied its own acceptance checks and was allowed")
	}
	var rej *RejectionError
	if !errors.As(err, &rej) || rej.Reason != ReasonForbidden {
		t.Fatalf("want a typed forbidden rejection, got %v", err)
	}

	// The same request without checks is still allowed: creation stays a
	// bounded-write the Guild Master may perform.
	if _, err := svc.WithCaller(Agent()).CreateTask(CreateTaskRequest{
		Project: "app", Objective: "ordinary work",
	}); err != nil {
		t.Fatalf("agent creation without checks should still be allowed: %v", err)
	}
}

func TestTaskChecks_HumanCallerMayNotExceedTheLimit(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{Result: ExecSuccess}, nil)

	many := make([]string, maxTaskChecks+1)
	for i := range many {
		many[i] = "check"
	}
	_, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x", Checks: many})
	var rej *RejectionError
	if !errors.As(err, &rej) || rej.Reason != ReasonInvalidCheck {
		t.Fatalf("want invalid_check, got %v", err)
	}
}

// Blank --check values are typos, not instructions, and must not become a task
// check that runs an empty command in the verifier.
func TestTaskChecks_EmptyValuesAreDropped(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{Result: ExecSuccess}, nil)

	task, err := svc.CreateTask(CreateTaskRequest{
		Project: "app", Objective: "x", Checks: []string{"  ", "", "real-check", "   "},
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if len(task.Checks) != 1 || task.Checks[0] != "real-check" {
		t.Errorf("checks = %v, want [real-check]", task.Checks)
	}
}

// The checks are deliberately OUTSIDE the frozen acceptance hash: that hash is
// the project's policy, and a task's own additions must not read as policy drift
// at verify time.
func TestTaskChecks_DoNotCountAsPolicyDrift(t *testing.T) {
	repo := gitRepo(t)
	writeVerifyPolicy(t, repo, `{"checks":["project-check"],"acceptanceGlobs":["**/*_test.go"]}`)

	var seen []string
	svc, _, _ := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, checkOrderVerifier{seen: &seen})

	withChecks, err := svc.CreateTask(CreateTaskRequest{
		Project: "app", Objective: "x", Checks: []string{"task-check"},
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	plain, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "y"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if withChecks.AcceptanceHash != plain.AcceptanceHash {
		t.Errorf("task checks changed the frozen policy hash (%q vs %q) — they must not",
			withChecks.AcceptanceHash, plain.AcceptanceHash)
	}

	if _, err := svc.DispatchTask(withChecks.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	res, err := svc.VerifyTask(withChecks.ID)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.Reason == ReasonPolicyDrift {
		t.Fatal("a task with its own checks was rejected as policy drift")
	}
}

// Round-trip through a reopened database: the column is added by an idempotent
// migration, so an existing control.db gains it without losing rows, and a task
// written before it existed reads back with no checks rather than failing to
// scan.
func TestTaskChecks_SurviveAReopenedDatabase(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "control.db")

	store, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	created, err := store.CreateTask(NewTask{
		Project: "app", Objective: "x", BaseSHA: "abc123",
		Checks: []string{"check-one", "check two with spaces"},
	}, StatePlanned)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	legacy, err := store.CreateTask(NewTask{Project: "app", Objective: "y", BaseSHA: "abc123"}, StatePlanned)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store.Close()

	reopened, err := Open(path) // migrate() runs again
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	got, err := reopened.GetTask(created.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if strings.Join(got.Checks, "|") != "check-one|check two with spaces" {
		t.Errorf("checks after reopen = %v", got.Checks)
	}
	old, err := reopened.GetTask(legacy.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if len(old.Checks) != 0 {
		t.Errorf("a task with no checks read back %v", old.Checks)
	}
}
