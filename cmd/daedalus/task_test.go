// Copyright (C) 2026 Techdelight BV

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/control"
)

// mapResolver resolves project names to directories from a map (no registry).
type mapResolver map[string]string

func (m mapResolver) ProjectDir(name string) (string, error) {
	dir, ok := m[name]
	if !ok {
		return "", errors.New("project not registered: " + name)
	}
	return dir, nil
}

// newTestService builds an in-process control.Service backed by a temp DB and a
// stub (success) runner — the same TaskAPI the CLI drives via the socket client.
func newTestService(t *testing.T, resolver control.ProjectResolver) *control.Service {
	t.Helper()
	dataDir := t.TempDir()
	store, err := control.Open(filepath.Join(dataDir, "control.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	wt := control.NewWorktreeManager(dataDir)
	runner := control.StubRunner{Result: control.ExecSuccess, WriteFile: true}
	verifier := control.StubVerifyRunner{Pass: true}
	return control.NewService(store, resolver, wt, runner, verifier, nil)
}

// makeGitRepo creates a temp git repo with one commit.
func makeGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "init")
	return dir
}

func TestCLI_TaskCreate_HappyPath(t *testing.T) {
	dir := makeGitRepo(t)
	svc := newTestService(t, mapResolver{"app": dir})

	if err := runTaskCommand(svc, []string{"create", "--project", "app", "--objective", "fix the bug", "--acceptance", "policy@x"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	tasks, _ := svc.ListTasks()
	if len(tasks) != 1 || tasks[0].Project != "app" || len(tasks[0].BaseSHA) != 40 {
		t.Fatalf("task not created correctly: %+v", tasks)
	}
}

func TestCLI_TaskCreate_NonGitRejected(t *testing.T) {
	svc := newTestService(t, mapResolver{"plain": t.TempDir()})
	err := runTaskCommand(svc, []string{"create", "--project", "plain", "--objective", "x"})
	var notGit *control.ErrNotGitRepo
	if !errors.As(err, &notGit) {
		t.Errorf("create on non-git = %v, want *ErrNotGitRepo", err)
	}
}

// TestCLI_TaskCreate_SeveralActiveAllowed: since Sprint 61 a project may have
// several active Tasks. Concurrency is decided at DISPATCH by the scheduler,
// where capacity is actually consumed — planning work ahead is not the thing that
// needed limiting.
func TestCLI_TaskCreate_SeveralActiveAllowed(t *testing.T) {
	dir := makeGitRepo(t)
	svc := newTestService(t, mapResolver{"app": dir})
	for _, objective := range []string{"first", "second", "third"} {
		if err := runTaskCommand(svc, []string{"create", "--project", "app", "--objective", objective}); err != nil {
			t.Fatalf("create %q: %v", objective, err)
		}
	}
	tasks, err := svc.ListTasks()
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 3 {
		t.Errorf("tasks = %d, want 3 active on one project", len(tasks))
	}
}

func TestCLI_TaskListStatusCancel(t *testing.T) {
	dir := makeGitRepo(t)
	svc := newTestService(t, mapResolver{"app": dir})
	if err := runTaskCommand(svc, []string{"create", "--project", "app", "--objective", "x"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := runTaskCommand(svc, []string{"list"}); err != nil {
		t.Errorf("list: %v", err)
	}
	if err := runTaskCommand(svc, []string{"status", "T-1"}); err != nil {
		t.Errorf("status: %v", err)
	}
	if err := runTaskCommand(svc, []string{"status", "T-404"}); err == nil {
		t.Error("status missing = nil, want error")
	}
	if err := runTaskCommand(svc, []string{"cancel", "T-1"}); err != nil {
		t.Errorf("cancel: %v", err)
	}
	tasks, _ := svc.ListTasks()
	if tasks[0].State != control.StateCancelled {
		t.Errorf("state after cancel = %q, want cancelled", tasks[0].State)
	}
}

func TestCLI_TaskDispatch(t *testing.T) {
	dir := makeGitRepo(t)
	svc := newTestService(t, mapResolver{"app": dir})
	if err := runTaskCommand(svc, []string{"create", "--project", "app", "--objective", "do it"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := runTaskCommand(svc, []string{"dispatch", "T-1"}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	view, _ := svc.TaskStatus("T-1")
	if view.Task.State != control.StateCandidate {
		t.Errorf("task state = %q, want candidate", view.Task.State)
	}
	if len(view.Jobs) != 1 || view.Jobs[0].Job.State != control.StateCandidate {
		t.Fatalf("job not candidate: %+v", view.Jobs)
	}
	if len(view.Jobs[0].Artifacts) != 1 {
		t.Errorf("want 1 artifact, got %d", len(view.Jobs[0].Artifacts))
	}
}

func TestCLI_TaskUnknownAndMissingFlags(t *testing.T) {
	svc := newTestService(t, mapResolver{})
	if err := runTaskCommand(svc, []string{"frobnicate"}); err == nil {
		t.Error("unknown subcommand = nil, want error")
	}
	if err := runTaskCommand(svc, []string{"create", "--objective", "x"}); err == nil {
		t.Error("create without --project = nil, want error")
	}
	if err := runTaskCommand(svc, []string{"create", "--project", "p"}); err == nil {
		t.Error("create without --objective = nil, want error")
	}
	if err := runTaskCommand(svc, []string{"create", "--project", "p", "--objective", "x", "--nope"}); err == nil {
		t.Error("create unknown flag = nil, want error")
	}
}

func TestManageTasks_NoSubcommand(t *testing.T) {
	// The empty-args guard fires before controlClient, so no daemon is needed.
	if err := manageTasks(&core.Config{}); err == nil {
		t.Error("no subcommand = nil, want error")
	}
}

// --- governance (Sprint 58) ---------------------------------------------------

// newRejectingService is newTestService with a verifier that always fails, so a
// dispatched task lands in `rejected` and the retry/replan paths are reachable.
func newRejectingService(t *testing.T, resolver control.ProjectResolver) *control.Service {
	t.Helper()
	dataDir := t.TempDir()
	store, err := control.Open(filepath.Join(dataDir, "control.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	wt := control.NewWorktreeManager(dataDir)
	runner := control.StubRunner{Result: control.ExecSuccess, WriteFile: true}
	return control.NewService(store, resolver, wt, runner, control.StubVerifyRunner{Pass: false}, nil)
}

func TestCLI_TaskCreate_BudgetFlags(t *testing.T) {
	dir := makeGitRepo(t)
	svc := newTestService(t, mapResolver{"app": dir})
	if err := runTaskCommand(svc, []string{
		"create", "--project", "app", "--objective", "x",
		"--wall-clock", "120", "--max-attempts", "2", "--max-review-cycles", "1", "--concurrency", "1",
	}); err != nil {
		t.Fatalf("create with budget flags: %v", err)
	}
	tasks, _ := svc.ListTasks()
	got := tasks[0].Budget
	if got.WallClockSeconds != 120 || got.MaxAttempts != 2 || got.MaxReviewCycles != 1 || got.Concurrency != 1 {
		t.Errorf("budget = %+v, want the requested narrowing", got)
	}
}

func TestCLI_TaskCreate_BadBudgetFlags(t *testing.T) {
	dir := makeGitRepo(t)
	svc := newTestService(t, mapResolver{"app": dir})
	tests := [][]string{
		{"create", "--project", "app", "--objective", "x", "--max-attempts"},
		{"create", "--project", "app", "--objective", "x", "--max-attempts", "banana"},
		{"create", "--project", "app", "--objective", "x", "--wall-clock", "-5"},
	}
	for _, args := range tests {
		if err := runTaskCommand(svc, args); err == nil {
			t.Errorf("%v = nil, want an error", args)
		}
	}
}

// TestCLI_TaskCreate_OverBudgetIsRefused proves the CLI surfaces a policy refusal
// as a *control.RejectionError — which is what main.go turns into exit code 3.
func TestCLI_TaskCreate_OverBudgetIsRefused(t *testing.T) {
	dir := makeGitRepo(t)
	svc := newTestService(t, mapResolver{"app": dir})
	svc.SetBudgetSource(control.StaticBudget(control.Budget{
		WallClockSeconds: 60, MaxAttempts: 1, MaxReviewCycles: 1, Concurrency: 1,
	}))
	err := runTaskCommand(svc, []string{
		"create", "--project", "app", "--objective", "x", "--max-attempts", "50",
	})
	reason, refused := control.Rejected(err)
	if !refused {
		t.Fatalf("over-budget create err = %v, want a policy refusal", err)
	}
	if reason != control.ReasonOverBudget {
		t.Errorf("reason = %q, want %q", reason, control.ReasonOverBudget)
	}
	if code := exitCodeFor(err); code != exitRefused {
		t.Errorf("exit code = %d, want %d", code, exitRefused)
	}
}

func TestCLI_TaskRetryAndReplan(t *testing.T) {
	dir := makeGitRepo(t)
	svc := newRejectingService(t, mapResolver{"app": dir})
	if err := runTaskCommand(svc, []string{"create", "--project", "app", "--objective", "first go"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := runTaskCommand(svc, []string{"dispatch", "T-1"}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := runTaskCommand(svc, []string{"verify", "T-1"}); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// retry → a fresh Job; the chain keeps both attempts.
	if err := runTaskCommand(svc, []string{"retry", "T-1"}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	view, _ := svc.TaskStatus("T-1")
	if len(view.Jobs) != 2 {
		t.Errorf("job chain = %d, want 2 preserved attempts", len(view.Jobs))
	}
	// replan needs the task rejected again.
	if err := runTaskCommand(svc, []string{"verify", "T-1"}); err != nil {
		t.Fatalf("verify 2: %v", err)
	}
	if err := runTaskCommand(svc, []string{"replan", "T-1", "--objective", "second go"}); err != nil {
		t.Fatalf("replan: %v", err)
	}
	view, _ = svc.TaskStatus("T-1")
	if view.Task.Objective != "second go" || view.Task.State != control.StatePlanned {
		t.Errorf("after replan: %+v, want 'second go' in planned", view.Task)
	}
	if len(view.Jobs) != 2 {
		t.Errorf("replan must preserve the job chain, got %d jobs", len(view.Jobs))
	}
}

func TestCLI_TaskRetryReplanGuards(t *testing.T) {
	dir := makeGitRepo(t)
	svc := newRejectingService(t, mapResolver{"app": dir})
	if err := runTaskCommand(svc, []string{"create", "--project", "app", "--objective", "x"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	tests := []struct {
		name string
		args []string
	}{
		{"retry without an id", []string{"retry"}},
		{"retry with an unknown flag", []string{"retry", "T-1", "--nope"}},
		{"retry a planned task", []string{"retry", "T-1"}},
		{"replan without an id", []string{"replan"}},
		{"replan without an objective", []string{"replan", "T-1"}},
		{"replan with an unknown flag", []string{"replan", "T-1", "--nope", "x"}},
		{"events without an id", []string{"events"}},
		{"events for an unknown task", []string{"events", "T-404"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := runTaskCommand(svc, tc.args); err == nil {
				t.Errorf("%v = nil, want an error", tc.args)
			}
		})
	}
}

func TestCLI_TaskEvents(t *testing.T) {
	dir := makeGitRepo(t)
	svc := newTestService(t, mapResolver{"app": dir})
	if err := runTaskCommand(svc, []string{"create", "--project", "app", "--objective", "x"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := runTaskCommand(svc, []string{"dispatch", "T-1"}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := runTaskCommand(svc, []string{"events", "T-1"}); err != nil {
		t.Fatalf("events: %v", err)
	}
	// The alias reads the same log.
	if err := runTaskCommand(svc, []string{"log", "T-1"}); err != nil {
		t.Fatalf("log alias: %v", err)
	}
	events, err := svc.TaskEvents("T-1")
	if err != nil || len(events) == 0 {
		t.Fatalf("TaskEvents = (%d events, %v), want a populated log", len(events), err)
	}
}

// TestExitCodeFor pins the exit-code contract: a policy refusal is 3, everything
// else is 1. A script driving `daedalus task` branches on this.
func TestExitCodeFor(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"a plain failure", errors.New("boom"), exitFailure},
		{"a not-found", control.ErrNotFound, exitFailure},
		{"a policy refusal", &control.RejectionError{Reason: control.ReasonAttemptsExhausted}, exitRefused},
		{"a wrapped policy refusal",
			fmt.Errorf("dispatching: %w", &control.RejectionError{Reason: control.ReasonOverBudget}), exitRefused},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := exitCodeFor(tc.err); got != tc.want {
				t.Errorf("exitCodeFor(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

// --- integration + approval (Sprint 59) ----------------------------------------

// newApprovalService builds a Service that requires approval and has a passing
// reviewer, so the whole gate is exercised through the CLI.
func newApprovalService(t *testing.T, resolver control.ProjectResolver, approval bool) *control.Service {
	t.Helper()
	dataDir := t.TempDir()
	store, err := control.Open(filepath.Join(dataDir, "control.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	svc := control.NewService(store, resolver, control.NewWorktreeManager(dataDir),
		control.StubRunner{Result: control.ExecSuccess, WriteFile: true, MarkerName: "a.txt"},
		control.StubVerifyRunner{Pass: true}, nil)
	svc.SetPolicySource(control.StaticPolicy{Budget: control.DefaultBudget(), Approval: approval})
	return svc
}

func TestCLI_TaskIntegrate_FullGate(t *testing.T) {
	dir := makeGitRepo(t)
	svc := newApprovalService(t, mapResolver{"app": dir}, true)

	for _, args := range [][]string{
		{"create", "--project", "app", "--objective", "land it"},
		{"dispatch", "T-1"},
		{"verify", "T-1"},
	} {
		if err := runTaskCommand(svc, args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	// The approvals view lists it.
	if err := runTaskCommand(svc, []string{"approvals"}); err != nil {
		t.Fatalf("approvals: %v", err)
	}
	// Integrating before approval is refused with exit code 3.
	err := runTaskCommand(svc, []string{"integrate", "T-1"})
	reason, refused := control.Rejected(err)
	if !refused || reason != control.ReasonApprovalRequired {
		t.Fatalf("integrate before approval = %v, want an approval_required refusal", err)
	}
	if code := exitCodeFor(err); code != exitRefused {
		t.Errorf("exit code = %d, want %d", code, exitRefused)
	}
	// Approve, then land.
	if err := runTaskCommand(svc, []string{"approve", "T-1", "--note", "ship it"}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := runTaskCommand(svc, []string{"integrate", "T-1"}); err != nil {
		t.Fatalf("integrate: %v", err)
	}
	view, _ := svc.TaskStatus("T-1")
	if view.Task.State != control.StateIntegrated {
		t.Errorf("state = %q, want integrated", view.Task.State)
	}
}

func TestCLI_TaskReject(t *testing.T) {
	dir := makeGitRepo(t)
	svc := newApprovalService(t, mapResolver{"app": dir}, true)
	for _, args := range [][]string{
		{"create", "--project", "app", "--objective", "x"},
		{"dispatch", "T-1"},
		{"verify", "T-1"},
	} {
		if err := runTaskCommand(svc, args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if err := runTaskCommand(svc, []string{"reject", "T-1", "--note", "wrong approach"}); err != nil {
		t.Fatalf("reject: %v", err)
	}
	view, _ := svc.TaskStatus("T-1")
	if view.Task.State != control.StateRejected {
		t.Errorf("state = %q, want rejected", view.Task.State)
	}
}

func TestCLI_TaskTarget(t *testing.T) {
	dir := makeGitRepo(t)
	svc := newApprovalService(t, mapResolver{"app": dir}, false)
	if err := runTaskCommand(svc, []string{"create", "--project", "app", "--objective", "x"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := runTaskCommand(svc, []string{"target"}); err != nil {
		t.Fatalf("target: %v", err)
	}
	if err := runTaskCommand(svc, []string{"target", "app", "--sync"}); err != nil {
		t.Fatalf("target --sync: %v", err)
	}
	// `target <project>` without --sync is a usage error, not a silent no-op.
	if err := runTaskCommand(svc, []string{"target", "app"}); err == nil {
		t.Error("target without --sync should be a usage error")
	}
}

func TestCLI_IntegrationGuards(t *testing.T) {
	dir := makeGitRepo(t)
	svc := newApprovalService(t, mapResolver{"app": dir}, false)
	if err := runTaskCommand(svc, []string{"create", "--project", "app", "--objective", "x"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	tests := []struct {
		name string
		args []string
	}{
		{"integrate without an id", []string{"integrate"}},
		{"integrate a planned task", []string{"integrate", "T-1"}},
		{"approve without an id", []string{"approve"}},
		{"approve with an unknown flag", []string{"approve", "T-1", "--nope"}},
		{"approve a planned task", []string{"approve", "T-1"}},
		{"reject without an id", []string{"reject"}},
		{"review without an id", []string{"review"}},
		{"review with no reviewer configured", []string{"review", "T-1"}},
		{"approvals with an argument", []string{"approvals", "extra"}},
		{"target with an unknown flag", []string{"target", "app", "--nope"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := runTaskCommand(svc, tc.args); err == nil {
				t.Errorf("%v = nil, want an error", tc.args)
			}
		})
	}
}

func TestCLI_TaskReview(t *testing.T) {
	dir := makeGitRepo(t)
	svc := newApprovalService(t, mapResolver{"app": dir}, false)
	svc.SetReviewRunner(control.StubReviewRunner{Pass: true})
	for _, args := range [][]string{
		{"create", "--project", "app", "--objective", "x"},
		{"dispatch", "T-1"},
		{"verify", "T-1"},
		{"review", "T-1"},
	} {
		if err := runTaskCommand(svc, args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	view, _ := svc.TaskStatus("T-1")
	if len(view.Jobs) == 0 || len(view.Jobs[0].Artifacts) == 0 {
		t.Fatal("expected an artifact")
	}
	if got := view.Jobs[0].Artifacts[0].Review; got != control.ReviewPass {
		t.Errorf("artifact review = %q, want pass", got)
	}
}

// --- dependency graph (Sprint 62) ------------------------------------------------

func TestCLI_TaskDepends(t *testing.T) {
	dirA, dirB := makeGitRepo(t), makeGitRepo(t)
	svc := newTestService(t, mapResolver{"alpha": dirA, "beta": dirB})

	if err := runTaskCommand(svc, []string{"create", "--project", "alpha", "--objective", "upstream"}); err != nil {
		t.Fatalf("create alpha: %v", err)
	}
	if err := runTaskCommand(svc, []string{"create", "--project", "beta", "--objective", "downstream"}); err != nil {
		t.Fatalf("create beta: %v", err)
	}
	// Declare the edge.
	if err := runTaskCommand(svc, []string{"depends", "T-2", "--on", "T-1"}); err != nil {
		t.Fatalf("depends: %v", err)
	}
	view, err := svc.TaskDependencies("T-2")
	if err != nil {
		t.Fatalf("TaskDependencies: %v", err)
	}
	if len(view.DependsOn) != 1 || view.DependsOn[0] != "T-1" {
		t.Errorf("DependsOn = %v, want [T-1]", view.DependsOn)
	}
	// It is blocked, so dispatch is refused.
	if err := runTaskCommand(svc, []string{"dispatch", "T-2"}); err == nil {
		t.Error("a blocked task should not be dispatchable")
	}
	// Both views render.
	if err := runTaskCommand(svc, []string{"depends", "T-2"}); err != nil {
		t.Errorf("depends (show): %v", err)
	}
	if err := runTaskCommand(svc, []string{"status", "T-2"}); err != nil {
		t.Errorf("status: %v", err)
	}
}

func TestCLI_TaskDependsGuards(t *testing.T) {
	dir := makeGitRepo(t)
	svc := newTestService(t, mapResolver{"app": dir})
	if err := runTaskCommand(svc, []string{"create", "--project", "app", "--objective", "x"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	tests := []struct {
		name string
		args []string
	}{
		{"no id", []string{"depends"}},
		{"unknown flag", []string{"depends", "T-1", "--nope"}},
		{"--on without a value", []string{"depends", "T-1", "--on"}},
		{"self dependency", []string{"depends", "T-1", "--on", "T-1"}},
		{"unknown dependency", []string{"depends", "T-1", "--on", "T-404"}},
		{"unknown task", []string{"depends", "T-404"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := runTaskCommand(svc, tc.args); err == nil {
				t.Errorf("%v = nil, want an error", tc.args)
			}
		})
	}
}

// --- M17: steering and the programme board --------------------------------------

// TestCLI_TaskSteer covers the whole steering surface from the CLI, including the
// outcome that matters most: the default runner has NO steering boundary, so an
// operator asking for a steer is told plainly that nothing was delivered.
func TestCLI_TaskSteer(t *testing.T) {
	dir := makeGitRepo(t)
	svc := newTestService(t, mapResolver{"app": dir})
	if err := runTaskCommand(svc, []string{"create", "--project", "app", "--objective", "x"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Drive the task into working with a live Job, which is the only state in which
	// an instruction could reach anything.
	store := svc.Store()
	if _, err := store.TransitionTask("T-1", control.StateQueued, false, ""); err != nil {
		t.Fatalf("→queued: %v", err)
	}
	if _, err := store.TransitionTask("T-1", control.StateWorking, false, ""); err != nil {
		t.Fatalf("→working: %v", err)
	}
	job, err := store.CreateJob("T-1", "abc", "claude", 0, control.StateWorking)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	// Issuing succeeds as a COMMAND even though delivery failed: the plane recorded
	// the instruction and reported the truth about it, which is not a CLI error.
	if err := runTaskCommand(svc, []string{"steer", job.ID, "--instruction", "try the other approach"}); err != nil {
		t.Fatalf("steer: %v", err)
	}
	steers, err := svc.JobSteering(job.ID)
	if err != nil {
		t.Fatalf("JobSteering: %v", err)
	}
	if len(steers) != 1 || steers[0].State != control.SteerUndeliverable {
		t.Fatalf("steering = %+v, want one undeliverable instruction", steers)
	}
	// The history view renders.
	if err := runTaskCommand(svc, []string{"steer", job.ID}); err != nil {
		t.Errorf("steer (show): %v", err)
	}
}

func TestCLI_TaskSteerGuards(t *testing.T) {
	dir := makeGitRepo(t)
	svc := newTestService(t, mapResolver{"app": dir})
	tests := []struct {
		name string
		args []string
	}{
		{"no job id", []string{"steer"}},
		{"unknown flag", []string{"steer", "J-1", "--nope"}},
		{"--instruction without text", []string{"steer", "J-1", "--instruction"}},
		{"--withdraw without an id", []string{"steer", "--withdraw"}},
		{"unknown steering id", []string{"steer", "--withdraw", "S-404"}},
		{"unknown job", []string{"steer", "J-404"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := runTaskCommand(svc, tc.args); err == nil {
				t.Errorf("%v = nil, want an error", tc.args)
			}
		})
	}
}

func TestCLI_TaskBoard(t *testing.T) {
	dir := makeGitRepo(t)
	svc := newTestService(t, mapResolver{"app": dir})
	// An empty board still renders — "no tasks yet" is an answer.
	if err := runTaskCommand(svc, []string{"board"}); err != nil {
		t.Fatalf("board (empty): %v", err)
	}
	if err := runTaskCommand(svc, []string{"create", "--project", "app", "--objective", "x"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := runTaskCommand(svc, []string{"board"}); err != nil {
		t.Fatalf("board: %v", err)
	}
	if err := runTaskCommand(svc, []string{"board", "extra"}); err == nil {
		t.Error("board with an unexpected argument = nil, want an error")
	}
}

// TestCLI_TaskReverify_FlagsAndRouting covers the surface the operator actually
// types: the subcommand routes, the flag parses, and an unknown flag is refused
// with the usage rather than being silently ignored — a `--amend` typo that
// quietly ran a plain replay would re-grade under the wrong policy and report
// success.
func TestCLI_TaskReverify_FlagsAndRouting(t *testing.T) {
	repo := makeGitRepo(t)
	svc := newTestService(t, mapResolver{"app": repo})

	if err := runTaskCommand(svc, []string{"reverify"}); err == nil {
		t.Error("reverify with no id should be a usage error")
	}
	err := runTaskCommand(svc, []string{"reverify", "T-1", "--amend"})
	if err == nil {
		t.Fatal("an unknown flag must be refused")
	}
	if !strings.Contains(err.Error(), "--amend") || !strings.Contains(err.Error(), "reverify <id>") {
		t.Errorf("error should name the bad flag and the usage, got %q", err)
	}

	// A task that was never rejected cannot be re-graded: there is no verdict to
	// set aside. Routed through the real service, so this also proves the CLI
	// reaches ReverifyTask rather than falling through to `unknown subcommand`.
	if err := runTaskCommand(svc, []string{"create", "--project", "app", "--objective", "work"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	err = runTaskCommand(svc, []string{"reverify", "T-1"})
	if err == nil || !errors.Is(err, control.ErrWrongState) {
		t.Errorf("reverify of a planned task: err = %v, want ErrWrongState", err)
	}
}
