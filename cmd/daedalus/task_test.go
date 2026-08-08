// Copyright (C) 2026 Techdelight BV

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

func TestCLI_TaskCreate_SecondActiveRejected(t *testing.T) {
	dir := makeGitRepo(t)
	svc := newTestService(t, mapResolver{"app": dir})
	if err := runTaskCommand(svc, []string{"create", "--project", "app", "--objective", "first"}); err != nil {
		t.Fatalf("first: %v", err)
	}
	err := runTaskCommand(svc, []string{"create", "--project", "app", "--objective", "second"})
	var active *control.ErrActiveTaskExists
	if !errors.As(err, &active) {
		t.Errorf("second create = %v, want ErrActiveTaskExists", err)
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
