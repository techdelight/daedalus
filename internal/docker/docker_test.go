// Copyright (C) 2026 Techdelight BV

package docker

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/executor"
)

func TestIsContainerRunning_Found(t *testing.T) {
	mock := executor.NewMockExecutor()
	mock.Results["docker"] = executor.MockResult{Output: "claude-run-myapp\nother-container\n"}
	docker := NewDocker(mock, "/path/to/compose.yml")

	running, err := docker.IsContainerRunning("claude-run-myapp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !running {
		t.Error("IsContainerRunning = false, want true")
	}
}

func TestIsContainerRunning_NotFound(t *testing.T) {
	mock := executor.NewMockExecutor()
	mock.Results["docker"] = executor.MockResult{Output: "other-container\n"}
	docker := NewDocker(mock, "/path/to/compose.yml")

	running, err := docker.IsContainerRunning("claude-run-myapp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if running {
		t.Error("IsContainerRunning = true, want false")
	}
}

func TestIsContainerRunning_Empty(t *testing.T) {
	mock := executor.NewMockExecutor()
	mock.Results["docker"] = executor.MockResult{Output: ""}
	docker := NewDocker(mock, "/path/to/compose.yml")

	running, err := docker.IsContainerRunning("claude-run-myapp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if running {
		t.Error("IsContainerRunning = true, want false")
	}
}

func TestImageExists_True(t *testing.T) {
	mock := executor.NewMockExecutor()
	docker := NewDocker(mock, "/path/to/compose.yml")

	if !docker.ImageExists("myimage:dev") {
		t.Error("ImageExists = false, want true")
	}

	call := mock.FindCall("docker")
	if call == nil {
		t.Fatal("expected docker call")
	}
	if call.Args[0] != "image" || call.Args[1] != "inspect" || call.Args[2] != "myimage:dev" {
		t.Errorf("unexpected args: %v", call.Args)
	}
}

func TestImageExists_False(t *testing.T) {
	mock := executor.NewMockExecutor()
	mock.Results["docker"] = executor.MockResult{Err: errors.New("exit 1")}
	docker := NewDocker(mock, "/path/to/compose.yml")

	if docker.ImageExists("myimage:dev") {
		t.Error("ImageExists = true, want false")
	}
}

func TestBuild(t *testing.T) {
	mock := executor.NewMockExecutor()
	docker := NewDocker(mock, "/path/to/compose.yml")

	err := docker.Build("dev", "myimage:dev", "1000", "/src")
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	call := mock.FindCall("docker")
	if call == nil {
		t.Fatal("expected docker call")
	}

	args := strings.Join(call.Args, " ")
	if !strings.Contains(args, "--target dev") {
		t.Errorf("missing --target: %s", args)
	}
	if !strings.Contains(args, "--build-arg CLAUDE_UID=1000") {
		t.Errorf("missing --build-arg: %s", args)
	}
	if !strings.Contains(args, "-t myimage:dev") {
		t.Errorf("missing -t: %s", args)
	}
}

func TestComposeRun(t *testing.T) {
	mock := executor.NewMockExecutor()
	docker := NewDocker(mock, "/path/to/compose.yml")

	env := map[string]string{
		"PROJECT_DIR": "/my/project",
	}
	claudeArgs := []string{"--resume", "abc123"}

	err := docker.ComposeRun("claude-run-myapp", env, claudeArgs, nil, "")
	if err != nil {
		t.Fatalf("ComposeRun failed: %v", err)
	}

	call := mock.FindCall("docker")
	if call == nil {
		t.Fatal("expected docker call")
	}

	args := strings.Join(call.Args, " ")
	if !strings.Contains(args, "compose -f /path/to/compose.yml run --rm --name claude-run-myapp") {
		t.Errorf("unexpected compose command: %s", args)
	}
	if !strings.Contains(args, "claude --resume abc123") {
		t.Errorf("missing claude args: %s", args)
	}
}

func TestComposeRun_WithExtraArgs(t *testing.T) {
	mock := executor.NewMockExecutor()
	docker := NewDocker(mock, "/path/to/compose.yml")

	env := map[string]string{
		"PROJECT_DIR": "/my/project",
	}
	extraArgs := []string{"-v", "/var/run/docker.sock:/var/run/docker.sock"}

	err := docker.ComposeRun("claude-run-myapp", env, nil, extraArgs, "")
	if err != nil {
		t.Fatalf("ComposeRun failed: %v", err)
	}

	call := mock.FindCall("docker")
	if call == nil {
		t.Fatal("expected docker call")
	}

	args := strings.Join(call.Args, " ")
	if !strings.Contains(args, "/var/run/docker.sock:/var/run/docker.sock") {
		t.Errorf("missing docker socket volume in args: %s", args)
	}

	sockIdx := -1
	serviceIdx := -1
	for i, a := range call.Args {
		if a == "/var/run/docker.sock:/var/run/docker.sock" {
			sockIdx = i
		}
		if a == "claude" {
			serviceIdx = i
		}
	}
	if sockIdx == -1 || serviceIdx == -1 {
		t.Fatalf("could not find socket volume or service name in args: %v", call.Args)
	}
	if sockIdx > serviceIdx {
		t.Errorf("extraArgs must appear before service name 'claude' in args: %v", call.Args)
	}
}

func TestComposeRunCommand_WithExtraArgs(t *testing.T) {
	mock := executor.NewMockExecutor()
	docker := NewDocker(mock, "/path/to/compose.yml")

	claudeArgs := []string{"--resume", "abc123"}
	extraArgs := []string{"-v", "/var/run/docker.sock:/var/run/docker.sock"}

	cmd := docker.ComposeRunCommand("claude-run-myapp", claudeArgs, extraArgs)

	args := strings.Join(cmd, " ")
	if !strings.Contains(args, "/var/run/docker.sock:/var/run/docker.sock") {
		t.Errorf("missing docker socket volume in args: %s", args)
	}

	sockIdx := -1
	serviceIdx := -1
	for i, a := range cmd {
		if a == "/var/run/docker.sock:/var/run/docker.sock" {
			sockIdx = i
		}
		if a == "claude" {
			serviceIdx = i
		}
	}
	if sockIdx == -1 || serviceIdx == -1 {
		t.Fatalf("could not find socket volume or service name in cmd: %v", cmd)
	}
	if sockIdx > serviceIdx {
		t.Errorf("extraArgs must appear before service name 'claude' in cmd: %v", cmd)
	}

	if !strings.Contains(args, "claude --resume abc123") {
		t.Errorf("claudeArgs should follow service name: %s", args)
	}

	_ = mock
}

func TestSetupProjectDirs(t *testing.T) {
	dir := t.TempDir()
	cfg := &core.Config{ProjectDir: dir}

	if err := SetupProjectDirs(cfg); err != nil {
		t.Fatalf("SetupProjectDirs: %v", err)
	}

	for _, sub := range []string{".daedalus", ".claude/skills"} {
		info, err := os.Stat(dir + "/" + sub)
		if err != nil {
			t.Errorf("%s not created: %v", sub, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", sub)
		}
	}

	// Idempotent: calling again must not fail.
	if err := SetupProjectDirs(cfg); err != nil {
		t.Fatalf("SetupProjectDirs (idempotent): %v", err)
	}
}

func TestComposeRun_DoesNotPolluteEnv(t *testing.T) {
	mock := executor.NewMockExecutor()
	docker := NewDocker(mock, "/path/to/compose.yml")

	const key = "DAEDALUS_TEST_SENTINEL"
	env := map[string]string{
		key: "should-not-leak",
	}

	err := docker.ComposeRun("claude-run-test", env, nil, nil, "")
	if err != nil {
		t.Fatalf("ComposeRun failed: %v", err)
	}

	if got := os.Getenv(key); got != "" {
		t.Errorf("env var %s leaked to parent process: %q", key, got)
	}
}
