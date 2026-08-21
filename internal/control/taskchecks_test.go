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
	// The project's checks and the Task's arrive in SEPARATE fields, because the
	// baseline has to tell them apart. The effective sequence is still project-then-
	// task, so that is what this reassembles — the same order CleanVerifier runs.
	effective := append(append([]string(nil), spec.Policy.Checks...), spec.TaskChecks...)
	*r.seen = effective
	for _, c := range effective {
		if strings.Contains(c, "FAIL") {
			return VerifyOutcome{Passed: false, Detail: "check failed: " + c}
		}
	}
	return VerifyOutcome{Passed: true, Detail: "all checks passed"}
}

// TestTaskChecks_TravelSeparatelyFromTheProjectPolicy.
//
// The split is load-bearing, not organisational: CleanVerifier baselines the
// project's checks (a check that was already failing says nothing about the
// change) and never baselines the Task's (a per-task check is SUPPOSED to fail at
// the base — that is what it asserts). Merging them back into one list would
// silently re-enable baselining for task checks, and a Job that did nothing would
// then be excused for failing the check that describes the thing it did not do.
// The merge is exactly one line to reintroduce, so it is pinned here.
func TestTaskChecks_TravelSeparatelyFromTheProjectPolicy(t *testing.T) {
	repo := gitRepo(t)
	writeVerifyPolicy(t, repo, `{"checks":["project-check"],"acceptanceGlobs":["**/*_test.go"]}`)

	var got VerifySpec
	capture := specCapturingVerifier{spec: &got, outcome: VerifyOutcome{Passed: true, Detail: "ok"}}
	svc, _, _ := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, capture)

	task, err := svc.CreateTask(CreateTaskRequest{
		Project: "app", Objective: "x", Checks: []string{"task-check"},
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err := svc.VerifyTask(task.ID, VerifyRequest{}); err != nil {
		t.Fatalf("verify: %v", err)
	}

	if strings.Join(got.TaskChecks, "|") != "task-check" {
		t.Errorf("spec.TaskChecks = %v, want the task's own checks", got.TaskChecks)
	}
	for _, c := range got.Policy.Checks {
		if c == "task-check" {
			t.Fatal("the task's check was merged into the PROJECT policy — the baseline can no longer tell them apart")
		}
	}
	if strings.Join(got.Policy.Checks, "|") != "project-check" {
		t.Errorf("spec.Policy.Checks = %v, want only the project's", got.Policy.Checks)
	}
}

// TestVerifySpec_BaselinesAgainstTheJOBsBase.
//
// The baseline asks "was this check already failing on the tree this Job was
// handed", and only the JOB's base answers it. `reverify --amended` re-pins the
// TASK to a newer commit while keeping the existing artifact, so against the
// Task's base a check that trunk FIXED after the fact would read as a check this
// artifact broke — the bug this milestone removed, running backwards. The
// integrity gate above it made the same choice for the same reason.
// The two bases are EQUAL on the ordinary path, so this drives the one operation
// that separates them — `reverify --amended`, which re-pins the Task while keeping
// the existing artifact. Asserting on a normal verify would be a tautology that
// passes whichever base the spec carries.
func TestVerifySpec_BaselinesAgainstTheJOBsBase(t *testing.T) {
	repo := gitRepo(t)
	var got VerifySpec
	capture := &sequenceSpecVerifier{spec: &got, verdicts: []VerifyOutcome{
		{Passed: false, Detail: "the oracle was wrong"}, // → rejected, so reverify is legal
		{Passed: true, Detail: "ok"},
	}}
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, capture)

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err := svc.VerifyTask(task.ID, VerifyRequest{}); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	jobs, err := store.ListJobsForTask(task.ID)
	if err != nil || len(jobs) == 0 {
		t.Fatalf("ListJobsForTask: %v", err)
	}
	jobBase := jobs[0].BaseSHA

	// Trunk moves on — somebody fixes the very thing the checks were failing on —
	// and the plane's integration target advances to it.
	landed := commitFileOn(t, repo, jobBase, "trunk-moved.txt", "a fix the Job never saw")
	if _, err := store.AdvanceTarget(repoKey(t, repo), jobBase, landed,
		"test: a concurrent integration landed"); err != nil {
		t.Fatalf("AdvanceTarget: %v", err)
	}

	if _, err := svc.ReverifyTask(task.ID, ReverifyRequest{Amended: true}); err != nil {
		t.Fatalf("ReverifyTask: %v", err)
	}

	after, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.BaseSHA == jobBase {
		t.Fatalf("precondition: --amended should have re-pinned the task off the job's base %q", jobBase)
	}
	if got.BaseSHA != jobBase {
		t.Errorf("spec.BaseSHA = %q, want the JOB's base %q (the task is now pinned at %q, a commit the job never saw — "+
			"baselining there would read a fix trunk made as a check this artifact broke)",
			got.BaseSHA, jobBase, after.BaseSHA)
	}
}

// specCapturingVerifier records the spec it was handed and returns a fixed
// verdict.
type specCapturingVerifier struct {
	spec    *VerifySpec
	outcome VerifyOutcome
}

func (r specCapturingVerifier) Verify(_ context.Context, spec VerifySpec) VerifyOutcome {
	*r.spec = spec
	return r.outcome
}

// sequenceSpecVerifier records the LAST spec it was handed and returns a
// different verdict per call, so a test can drive a task through reject → replay.
type sequenceSpecVerifier struct {
	spec     *VerifySpec
	verdicts []VerifyOutcome
	n        int
}

func (r *sequenceSpecVerifier) Verify(_ context.Context, spec VerifySpec) VerifyOutcome {
	*r.spec = spec
	out := VerifyOutcome{Passed: true}
	if r.n < len(r.verdicts) {
		out = r.verdicts[r.n]
	}
	r.n++
	return out
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
	res, err := svc.VerifyTask(task.ID, VerifyRequest{})
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
	res, err := svc.VerifyTask(task.ID, VerifyRequest{})
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
	res, err := svc.VerifyTask(withChecks.ID, VerifyRequest{})
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
