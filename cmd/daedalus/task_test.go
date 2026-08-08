// Copyright (C) 2026 Techdelight BV

package main

import (
	"errors"
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
	return control.NewService(store, resolver, wt, runner, nil)
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
