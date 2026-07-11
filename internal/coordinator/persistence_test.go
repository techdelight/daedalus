// Copyright (C) 2026 Techdelight BV

package coordinator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/techdelight/daedalus/internal/executor"
)

// newPersistingCoordinator constructs a Coordinator with persistence
// pointed at sessions.json under a shared temp dir. The Coordinator
// and the file path are returned so tests can inspect on-disk state
// directly.
func newPersistingCoordinator(t *testing.T, dir string, exec executor.Executor) (*Coordinator, string) {
	t.Helper()
	sessionsFile := filepath.Join(dir, "sessions.json")
	c := New(Options{
		Executor:     exec,
		ComposeFile:  "/fake/docker-compose.yml",
		SocketWait:   500 * time.Millisecond,
		SessionsFile: sessionsFile,
	})
	c.pollEvery = 5 * time.Millisecond
	return c, sessionsFile
}

// readSessionsFile decodes the on-disk sessions.json for assertion.
// A missing file counts as an empty list, matching how the daemon
// would see it.
func readSessionsFile(t *testing.T, path string) []Session {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read sessions file: %v", err)
	}
	var out []Session
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal sessions file: %v", err)
	}
	return out
}

func TestPersistence_StartWritesSessionsFile(t *testing.T) {
	dir := t.TempDir()
	cfg := configFor(t, "alpha")
	cfg.DataDir = dir // keep the runner socket under the same tree
	exec := newSpyExec()
	exec.onRunWithEnv = func(_ []string, _ string, _ ...string) {
		touchSocket(t, cfg.RunnerSocketPath())
	}

	c, sessionsFile := newPersistingCoordinator(t, dir, exec)
	if _, err := c.Start(cfg); err != nil {
		t.Fatalf("Start: %v", err)
	}

	stored := readSessionsFile(t, sessionsFile)
	if len(stored) != 1 {
		t.Fatalf("stored len = %d, want 1 (contents: %+v)", len(stored), stored)
	}
	if stored[0].ProjectName != "alpha" {
		t.Errorf("stored ProjectName = %q, want alpha", stored[0].ProjectName)
	}
}

func TestPersistence_StopRewritesSessionsFile(t *testing.T) {
	dir := t.TempDir()
	cfg := configFor(t, "alpha")
	cfg.DataDir = dir
	exec := newSpyExec()
	exec.onRunWithEnv = func(_ []string, _ string, _ ...string) {
		touchSocket(t, cfg.RunnerSocketPath())
	}

	c, sessionsFile := newPersistingCoordinator(t, dir, exec)
	if _, err := c.Start(cfg); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := c.Stop("alpha"); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	stored := readSessionsFile(t, sessionsFile)
	if len(stored) != 0 {
		t.Errorf("stored len = %d, want 0 after Stop", len(stored))
	}
}

// A restarted coordinator should reconstruct its map from
// sessions.json, filtered by what's still in `docker ps`.
func TestPersistence_LoadReconcilesWithDockerPS(t *testing.T) {
	dir := t.TempDir()
	sessionsFile := filepath.Join(dir, "sessions.json")

	// Seed the file with three sessions.
	seed := []Session{
		{ProjectName: "alive-1", ContainerName: "claude-run-alive-1", SocketPath: "/tmp/a1.sock", StartedAt: time.Now()},
		{ProjectName: "dead", ContainerName: "claude-run-dead", SocketPath: "/tmp/d.sock", StartedAt: time.Now()},
		{ProjectName: "alive-2", ContainerName: "claude-run-alive-2", SocketPath: "/tmp/a2.sock", StartedAt: time.Now()},
	}
	data, _ := json.Marshal(seed)
	if err := os.WriteFile(sessionsFile, data, 0o644); err != nil {
		t.Fatalf("seed sessions.json: %v", err)
	}

	// docker ps returns only alive-1 and alive-2. `dead` is the one
	// reconciliation must drop.
	exec := newSpyExec()
	exec.Results["docker"] = executor.MockResult{
		Output: "claude-run-alive-1\nclaude-run-alive-2\n",
	}

	c := New(Options{
		Executor:     exec,
		ComposeFile:  "/fake/docker-compose.yml",
		SessionsFile: sessionsFile,
	})

	if _, ok := c.Get("alive-1"); !ok {
		t.Error("alive-1 missing after reconcile")
	}
	if _, ok := c.Get("alive-2"); !ok {
		t.Error("alive-2 missing after reconcile")
	}
	if _, ok := c.Get("dead"); ok {
		t.Error("dead survived reconcile despite not being in docker ps")
	}

	// The reconciled state should have been persisted back — the file
	// no longer lists `dead`.
	after := readSessionsFile(t, sessionsFile)
	names := map[string]bool{}
	for _, s := range after {
		names[s.ProjectName] = true
	}
	if names["dead"] {
		t.Error("sessions.json still contains dead entry after reconcile")
	}
}

// A missing sessions.json is expected on first boot — must not error.
func TestPersistence_MissingFileIsCleanSlate(t *testing.T) {
	dir := t.TempDir()
	sessionsFile := filepath.Join(dir, "does-not-exist.json")
	exec := newSpyExec()

	c := New(Options{
		Executor:     exec,
		ComposeFile:  "/fake/docker-compose.yml",
		SessionsFile: sessionsFile,
	})
	if len(c.List()) != 0 {
		t.Errorf("List = %d entries, want 0", len(c.List()))
	}
	// docker ps must NOT be invoked when there's nothing to reconcile
	// against — no need to spend a subprocess call on an empty startup.
	if exec.MockExecutor.HasCall("docker") {
		t.Error("docker ps invoked despite empty startup")
	}
}

// A corrupt sessions.json should not prevent the daemon from starting.
// The daemon logs the error and boots with an empty map — better to
// lose tracking than to be unbootable.
func TestPersistence_CorruptFileIsCleanSlate(t *testing.T) {
	dir := t.TempDir()
	sessionsFile := filepath.Join(dir, "sessions.json")
	if err := os.WriteFile(sessionsFile, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write bogus: %v", err)
	}
	exec := newSpyExec()

	c := New(Options{
		Executor:     exec,
		ComposeFile:  "/fake/docker-compose.yml",
		SessionsFile: sessionsFile,
	})
	if len(c.List()) != 0 {
		t.Errorf("List = %d, want 0 after corrupt file", len(c.List()))
	}
}

// If `docker ps` fails during reconciliation, the coordinator must
// start empty rather than trust the possibly-stale file. Guards
// against silently inheriting sessions whose containers may or may
// not be running.
func TestPersistence_DockerFailureDropsRecoveredState(t *testing.T) {
	dir := t.TempDir()
	sessionsFile := filepath.Join(dir, "sessions.json")
	seed := []Session{{ProjectName: "alpha", ContainerName: "claude-run-alpha", SocketPath: "/tmp/a.sock", StartedAt: time.Now()}}
	data, _ := json.Marshal(seed)
	if err := os.WriteFile(sessionsFile, data, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	exec := newSpyExec()
	exec.Results["docker"] = executor.MockResult{Err: os.ErrPermission}

	c := New(Options{
		Executor:     exec,
		ComposeFile:  "/fake/docker-compose.yml",
		SessionsFile: sessionsFile,
	})
	if len(c.List()) != 0 {
		t.Errorf("List = %d, want 0 after docker-ps failure", len(c.List()))
	}
}

// Atomic-write hygiene: after a Start the .tmp file must not linger,
// and the primary file must be readable JSON.
func TestPersistence_AtomicWriteLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	cfg := configFor(t, "alpha")
	cfg.DataDir = dir
	exec := newSpyExec()
	exec.onRunWithEnv = func(_ []string, _ string, _ ...string) {
		touchSocket(t, cfg.RunnerSocketPath())
	}

	c, sessionsFile := newPersistingCoordinator(t, dir, exec)
	if _, err := c.Start(cfg); err != nil {
		t.Fatalf("Start: %v", err)
	}

	tmp := sessionsFile + ".tmp"
	if _, err := os.Stat(tmp); err == nil {
		t.Errorf("leftover temp file at %s", tmp)
	}
	if _, err := os.Stat(sessionsFile); err != nil {
		t.Errorf("primary file missing: %v", err)
	}
}
