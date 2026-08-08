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
	"github.com/techdelight/daedalus/internal/registry"
)

// newTaskTestConfig returns a Config rooted at a temp data dir.
func newTaskTestConfig(t *testing.T) *core.Config {
	t.Helper()
	return &core.Config{DataDir: t.TempDir()}
}

// registerGitProject creates a temp git repo with one commit and registers it.
func registerGitProject(t *testing.T, cfg *core.Config, name string) string {
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

	reg := registry.NewRegistry(cfg.RegistryPath())
	if err := reg.Init(); err != nil {
		t.Fatalf("reg init: %v", err)
	}
	if err := reg.AddProject(name, dir, "dev"); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	return dir
}

func TestTaskCreate_HappyPath(t *testing.T) {
	cfg := newTaskTestConfig(t)
	registerGitProject(t, cfg, "app")

	cfg.TaskArgs = []string{"create", "--project", "app", "--objective", "fix the bug", "--acceptance", "policy@x"}
	if err := manageTasks(cfg); err != nil {
		t.Fatalf("manageTasks create: %v", err)
	}

	// Verify it landed in the store with a captured base_sha.
	s, err := control.Open(cfg.ControlDBPath())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	task, err := s.GetTask("T-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Project != "app" || task.Objective != "fix the bug" || task.AcceptanceRef != "policy@x" {
		t.Errorf("task fields wrong: %+v", task)
	}
	if len(task.BaseSHA) != 40 {
		t.Errorf("base sha not captured: %q", task.BaseSHA)
	}
	if task.State != control.StatePlanned {
		t.Errorf("state = %q, want planned", task.State)
	}
}

func TestTaskCreate_NonGitRejected(t *testing.T) {
	cfg := newTaskTestConfig(t)
	// Register a plain (non-git) directory.
	dir := t.TempDir()
	reg := registry.NewRegistry(cfg.RegistryPath())
	if err := reg.Init(); err != nil {
		t.Fatalf("reg init: %v", err)
	}
	if err := reg.AddProject("plain", dir, "dev"); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	cfg.TaskArgs = []string{"create", "--project", "plain", "--objective", "x"}
	err := manageTasks(cfg)
	if err == nil {
		t.Fatal("create on non-git dir = nil, want error")
	}
	var notGit *control.ErrNotGitRepo
	if !errors.As(err, &notGit) {
		t.Errorf("err = %v, want *ErrNotGitRepo", err)
	}
}

func TestTaskCreate_SecondActiveRejected(t *testing.T) {
	cfg := newTaskTestConfig(t)
	registerGitProject(t, cfg, "app")

	cfg.TaskArgs = []string{"create", "--project", "app", "--objective", "first"}
	if err := manageTasks(cfg); err != nil {
		t.Fatalf("first create: %v", err)
	}
	cfg.TaskArgs = []string{"create", "--project", "app", "--objective", "second"}
	err := manageTasks(cfg)
	var active *control.ErrActiveTaskExists
	if !errors.As(err, &active) {
		t.Fatalf("second create err = %v, want ErrActiveTaskExists", err)
	}
}

func TestTaskCancel_MovesToCancelled(t *testing.T) {
	cfg := newTaskTestConfig(t)
	registerGitProject(t, cfg, "app")
	cfg.TaskArgs = []string{"create", "--project", "app", "--objective", "x"}
	if err := manageTasks(cfg); err != nil {
		t.Fatalf("create: %v", err)
	}

	cfg.TaskArgs = []string{"cancel", "T-1"}
	if err := manageTasks(cfg); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	s, _ := control.Open(cfg.ControlDBPath())
	defer s.Close()
	task, _ := s.GetTask("T-1")
	if task.State != control.StateCancelled {
		t.Errorf("state = %q, want cancelled", task.State)
	}
	// After cancel the project is free again.
	cfg.TaskArgs = []string{"create", "--project", "app", "--objective", "again"}
	if err := manageTasks(cfg); err != nil {
		t.Errorf("create after cancel: %v", err)
	}
}

func TestTaskList_And_Status(t *testing.T) {
	cfg := newTaskTestConfig(t)
	registerGitProject(t, cfg, "app")
	cfg.TaskArgs = []string{"create", "--project", "app", "--objective", "x"}
	if err := manageTasks(cfg); err != nil {
		t.Fatalf("create: %v", err)
	}
	cfg.TaskArgs = []string{"list"}
	if err := manageTasks(cfg); err != nil {
		t.Errorf("list: %v", err)
	}
	cfg.TaskArgs = []string{"status", "T-1"}
	if err := manageTasks(cfg); err != nil {
		t.Errorf("status: %v", err)
	}
	cfg.TaskArgs = []string{"status", "T-404"}
	if err := manageTasks(cfg); err == nil {
		t.Error("status of missing task = nil, want error")
	}
}

func TestTaskCreate_UnknownProject(t *testing.T) {
	cfg := newTaskTestConfig(t)
	reg := registry.NewRegistry(cfg.RegistryPath())
	if err := reg.Init(); err != nil {
		t.Fatalf("reg init: %v", err)
	}
	cfg.TaskArgs = []string{"create", "--project", "ghost", "--objective", "x"}
	if err := manageTasks(cfg); err == nil {
		t.Error("create with unregistered project = nil, want error")
	}
}

func TestTaskCreate_MissingFlags(t *testing.T) {
	cfg := newTaskTestConfig(t)
	cfg.TaskArgs = []string{"create", "--objective", "x"}
	if err := manageTasks(cfg); err == nil {
		t.Error("create without --project = nil, want error")
	}
	cfg.TaskArgs = []string{"create", "--project", "p"}
	if err := manageTasks(cfg); err == nil {
		t.Error("create without --objective = nil, want error")
	}
}

func TestTaskCreate_UnknownFlag(t *testing.T) {
	cfg := newTaskTestConfig(t)
	cfg.TaskArgs = []string{"create", "--project", "p", "--objective", "x", "--nope"}
	if err := manageTasks(cfg); err == nil {
		t.Error("create with unknown flag = nil, want error")
	}
}

func TestManageTasks_UnknownSubcommand(t *testing.T) {
	cfg := newTaskTestConfig(t)
	cfg.TaskArgs = []string{"frobnicate"}
	if err := manageTasks(cfg); err == nil {
		t.Error("unknown subcommand = nil, want error")
	}
	cfg.TaskArgs = nil
	if err := manageTasks(cfg); err == nil {
		t.Error("no subcommand = nil, want error")
	}
}
