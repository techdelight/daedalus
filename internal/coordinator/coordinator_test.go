// Copyright (C) 2026 Techdelight BV

package coordinator

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/executor"
)

// spyExec wraps MockExecutor so a test can run side effects when a
// command fires — used here to make the "runner socket" appear on
// disk in response to the docker compose run, simulating what the
// container would do once daedalus-runner binds inside it.
type spyExec struct {
	*executor.MockExecutor
	onRunWithEnv func(env []string, name string, args ...string)
}

func newSpyExec() *spyExec {
	return &spyExec{MockExecutor: executor.NewMockExecutor()}
}

func (s *spyExec) RunWithEnv(env []string, name string, args ...string) error {
	if s.onRunWithEnv != nil {
		s.onRunWithEnv(env, name, args...)
	}
	return s.MockExecutor.RunWithEnv(env, name, args...)
}

// configFor builds a minimal Config whose RunnerSocketPath() lands
// inside the test's TempDir, so tests can both inspect the path the
// coordinator picked and write a fake socket at it.
func configFor(t *testing.T, projectName string) *core.Config {
	t.Helper()
	dataDir := t.TempDir()
	return &core.Config{
		ProjectName: projectName,
		ProjectDir:  filepath.Join(dataDir, "src"),
		DataDir:     dataDir,
		Target:      "dev",
		ImagePrefix: "test/claude-runner",
	}
}

// touchSocket creates an empty file at path, mkdir -p'ing the parent.
// Coordinator.Start polls for the file's existence, not its socketness,
// so a regular file is sufficient to satisfy the wait.
func touchSocket(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("touch socket: %v", err)
	}
	f.Close()
}

func newTestCoordinator(t *testing.T, exec executor.Executor) *Coordinator {
	t.Helper()
	c := New(Options{
		Executor:    exec,
		ComposeFile: "/fake/docker-compose.yml",
		SocketWait:  500 * time.Millisecond,
	})
	c.pollEvery = 5 * time.Millisecond
	return c
}

func TestStart_HappyPath(t *testing.T) {
	cfg := configFor(t, "my-app")
	exec := newSpyExec()
	exec.onRunWithEnv = func(_ []string, _ string, _ ...string) {
		touchSocket(t, cfg.RunnerSocketPath())
	}
	c := newTestCoordinator(t, exec)

	sess, err := c.Start(cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if sess.ProjectName != "my-app" {
		t.Errorf("ProjectName = %q, want %q", sess.ProjectName, "my-app")
	}
	if sess.ContainerName != cfg.ContainerName() {
		t.Errorf("ContainerName = %q, want %q", sess.ContainerName, cfg.ContainerName())
	}
	if sess.SocketPath != cfg.RunnerSocketPath() {
		t.Errorf("SocketPath = %q, want %q", sess.SocketPath, cfg.RunnerSocketPath())
	}
	if sess.StartedAt.IsZero() {
		t.Error("StartedAt is zero")
	}
}

func TestStart_PassesRunnerEnv(t *testing.T) {
	cfg := configFor(t, "my-app")
	cfg.Resume = "abc123"
	cfg.Prompt = "fix the bug"
	cfg.Debug = true

	var capturedEnv []string
	exec := newSpyExec()
	exec.onRunWithEnv = func(env []string, _ string, _ ...string) {
		capturedEnv = append([]string(nil), env...)
		touchSocket(t, cfg.RunnerSocketPath())
	}
	c := newTestCoordinator(t, exec)

	if _, err := c.Start(cfg); err != nil {
		t.Fatalf("Start: %v", err)
	}

	want := map[string]bool{
		"DAEDALUS_RUNNER=1":                              false,
		"DAEDALUS_SOCKET=/home/claude/.daedalus/runner.sock": false,
		"DAEDALUS_DEBUG=1":                               false,
		"DAEDALUS_RESUME=abc123":                         false,
		"DAEDALUS_PROMPT=fix the bug":                    false,
	}
	for _, e := range capturedEnv {
		if _, ok := want[e]; ok {
			want[e] = true
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("missing env var %q", k)
		}
	}
}

func TestStart_RemovesStaleSocket(t *testing.T) {
	cfg := configFor(t, "my-app")
	stalePath := cfg.RunnerSocketPath()
	touchSocket(t, stalePath) // stale from previous run

	staleStat, err := os.Stat(stalePath)
	if err != nil {
		t.Fatalf("stat stale: %v", err)
	}

	exec := newSpyExec()
	exec.onRunWithEnv = func(_ []string, _ string, _ ...string) {
		// Recreate the socket so Start's poll completes.
		touchSocket(t, stalePath)
	}
	c := newTestCoordinator(t, exec)

	// Sleep so the new file gets a strictly later mtime.
	time.Sleep(10 * time.Millisecond)
	if _, err := c.Start(cfg); err != nil {
		t.Fatalf("Start: %v", err)
	}
	freshStat, err := os.Stat(stalePath)
	if err != nil {
		t.Fatalf("stat fresh: %v", err)
	}
	if !freshStat.ModTime().After(staleStat.ModTime()) {
		t.Errorf("socket was not replaced (stale mtime %v, current %v)", staleStat.ModTime(), freshStat.ModTime())
	}
}

func TestStart_TimesOutWhenSocketNeverAppears(t *testing.T) {
	cfg := configFor(t, "my-app")
	exec := newSpyExec() // no onRunWithEnv: socket never appears
	c := newTestCoordinator(t, exec)

	_, err := c.Start(cfg)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	// Failing Start must not record a session.
	if _, ok := c.Get("my-app"); ok {
		t.Error("session recorded despite failed Start")
	}
}

func TestStart_RejectsDoubleStart(t *testing.T) {
	cfg := configFor(t, "my-app")
	exec := newSpyExec()
	exec.onRunWithEnv = func(_ []string, _ string, _ ...string) {
		touchSocket(t, cfg.RunnerSocketPath())
	}
	c := newTestCoordinator(t, exec)

	if _, err := c.Start(cfg); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	_, err := c.Start(cfg)
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("second Start error = %v, want ErrAlreadyRunning", err)
	}
}

func TestStart_DockerErrorPropagates(t *testing.T) {
	cfg := configFor(t, "my-app")
	exec := newSpyExec()
	exec.Results["docker"] = executor.MockResult{Err: errors.New("daemon not running")}
	c := newTestCoordinator(t, exec)

	_, err := c.Start(cfg)
	if err == nil {
		t.Fatal("expected error from docker compose run, got nil")
	}
	if _, ok := c.Get("my-app"); ok {
		t.Error("session recorded despite docker error")
	}
}

func TestGet_ReturnsTrackedSession(t *testing.T) {
	cfg := configFor(t, "my-app")
	exec := newSpyExec()
	exec.onRunWithEnv = func(_ []string, _ string, _ ...string) {
		touchSocket(t, cfg.RunnerSocketPath())
	}
	c := newTestCoordinator(t, exec)

	started, err := c.Start(cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	got, ok := c.Get("my-app")
	if !ok {
		t.Fatal("Get returned not found")
	}
	if got.SocketPath != started.SocketPath {
		t.Errorf("Get returned different session: %+v vs %+v", got, started)
	}
}

func TestGet_UnknownProject(t *testing.T) {
	c := newTestCoordinator(t, newSpyExec())
	if _, ok := c.Get("nonexistent"); ok {
		t.Error("Get returned ok=true for unknown project")
	}
}

func TestList_OrdersByStartedAt(t *testing.T) {
	exec := newSpyExec()
	c := newTestCoordinator(t, exec)

	for _, name := range []string{"first", "second", "third"} {
		cfg := configFor(t, name)
		exec.onRunWithEnv = func(_ []string, _ string, _ ...string) {
			touchSocket(t, cfg.RunnerSocketPath())
		}
		if _, err := c.Start(cfg); err != nil {
			t.Fatalf("Start %s: %v", name, err)
		}
		// Spread StartedAt enough to be observably different.
		time.Sleep(2 * time.Millisecond)
	}

	got := c.List()
	if len(got) != 3 {
		t.Fatalf("List length = %d, want 3", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].StartedAt.Before(got[i-1].StartedAt) {
			t.Errorf("List not ordered by StartedAt: %d=%v, %d=%v", i-1, got[i-1].StartedAt, i, got[i].StartedAt)
		}
	}
}

func TestStop_RemovesSessionAndCallsDockerStop(t *testing.T) {
	cfg := configFor(t, "my-app")
	exec := newSpyExec()
	exec.onRunWithEnv = func(_ []string, _ string, _ ...string) {
		touchSocket(t, cfg.RunnerSocketPath())
	}
	c := newTestCoordinator(t, exec)

	if _, err := c.Start(cfg); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := c.Stop("my-app"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, ok := c.Get("my-app"); ok {
		t.Error("session still tracked after Stop")
	}
	// First call is `docker compose run --detach`, want a later
	// `docker stop` too.
	calls := exec.FindCalls("docker")
	if len(calls) < 2 {
		t.Fatalf("want >=2 docker calls, got %d", len(calls))
	}
	last := calls[len(calls)-1]
	if len(last.Args) < 2 || last.Args[0] != "stop" || last.Args[1] != cfg.ContainerName() {
		t.Errorf("last docker call args = %v, want [stop %s ...]", last.Args, cfg.ContainerName())
	}
}

func TestStop_UnknownProject(t *testing.T) {
	c := newTestCoordinator(t, newSpyExec())
	err := c.Stop("nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Stop unknown = %v, want ErrNotFound", err)
	}
}

// Stop must not forget a session whose `docker stop` failed — otherwise
// a transient docker error would orphan the container with no way for
// the operator to find or retry it.
func TestStop_KeepsSessionWhenDockerStopFails(t *testing.T) {
	cfg := configFor(t, "my-app")
	exec := newSpyExec()
	exec.onRunWithEnv = func(_ []string, _ string, _ ...string) {
		touchSocket(t, cfg.RunnerSocketPath())
	}
	c := newTestCoordinator(t, exec)

	if _, err := c.Start(cfg); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Make the next `docker` call fail. Results is shared between Run
	// and RunWithEnv, but Start has already fired, so this only affects
	// the upcoming `docker stop`.
	exec.Results["docker"] = executor.MockResult{Err: errors.New("daemon hiccup")}

	if err := c.Stop("my-app"); err == nil {
		t.Fatal("expected error from docker stop, got nil")
	}
	if _, ok := c.Get("my-app"); !ok {
		t.Fatal("session removed even though docker stop failed; retry is now impossible")
	}

	// Clear the canned failure and retry — second Stop should succeed
	// and clean up.
	exec.Results["docker"] = executor.MockResult{}
	if err := c.Stop("my-app"); err != nil {
		t.Fatalf("retried Stop: %v", err)
	}
	if _, ok := c.Get("my-app"); ok {
		t.Error("session still tracked after successful retry")
	}
}

// Smoke test: a real Unix socket bind satisfies waitForSocket. Guards
// against any future change that conflates "file exists" with "socket
// is listening" — the current contract is the former.
func TestStart_RealUnixSocketSatisfiesWait(t *testing.T) {
	cfg := configFor(t, "my-app")
	exec := newSpyExec()
	exec.onRunWithEnv = func(_ []string, _ string, _ ...string) {
		if err := os.MkdirAll(filepath.Dir(cfg.RunnerSocketPath()), 0o755); err != nil {
			t.Errorf("mkdir: %v", err)
			return
		}
		l, err := net.Listen("unix", cfg.RunnerSocketPath())
		if err != nil {
			t.Errorf("listen: %v", err)
			return
		}
		t.Cleanup(func() { l.Close() })
	}
	c := newTestCoordinator(t, exec)

	if _, err := c.Start(cfg); err != nil {
		t.Fatalf("Start: %v", err)
	}
}
