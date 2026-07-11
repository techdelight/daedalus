// Copyright (C) 2026 Techdelight BV

//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/coordinator"
)

// TestIntegration_DaemonBinary is the full-stack smoke test for
// Sprint 40. It builds the daedalus-coordinator binary from source,
// starts it against a mock docker on PATH, and drives it end-to-end
// with the real coordinator.Client:
//
//	Start("alpha")  →  List() shows alpha  →  Get("alpha")  →  Stop("alpha")
//
// Everything below the client — HTTP-over-UDS, JSON encoding,
// Coordinator lifecycle, session persistence, signal-handled
// shutdown — is exercised as a real binary. Regressions in the wire
// contract, the daemon startup path, or the shutdown path all show
// up here.
//
// Skipped when `go` isn't on PATH (rare) or under -short (the build
// is the expensive part).
func TestIntegration_DaemonBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: skipped under -short")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; cannot build daemon binary")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("integration test only runs on linux/darwin, not %s", runtime.GOOS)
	}

	root := t.TempDir()
	binPath := buildDaemonBinary(t, root)
	dockerBin, stateFile := writeMockDocker(t, root)

	// Layout under root: data/ holds sessions.json, coordinator.sock,
	// pidfile, and the per-project runner-socket dirs. compose/ holds
	// a minimal docker-compose.yml the daemon points at (contents
	// don't matter — the mock docker never actually reads it).
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	composeFile := filepath.Join(root, "docker-compose.yml")
	if err := os.WriteFile(composeFile, []byte("services:\n  claude: {}\n"), 0o644); err != nil {
		t.Fatalf("write compose file: %v", err)
	}

	sockPath := coordinator.DefaultSocketPath(dataDir)
	pidPath := filepath.Join(dataDir, ".daedalus", "coordinator.pid")

	daemon := exec.Command(binPath,
		"--socket", sockPath,
		"--data-dir", dataDir,
		"--compose", composeFile,
		"--pid-file", pidPath,
	)
	// Prepend our mock docker's dir to PATH so the daemon spawns the
	// mock, not the real one. MOCK_DOCKER_STATE is what the mock reads
	// to answer `docker ps`.
	daemon.Env = append(os.Environ(),
		"PATH="+filepath.Dir(dockerBin)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"MOCK_DOCKER_STATE="+stateFile,
	)
	// Route daemon stderr into the test's log so a failure shows the
	// daemon's own diagnostics inline.
	daemon.Stdout = testLogWriter{t}
	daemon.Stderr = testLogWriter{t}

	if err := daemon.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = daemon.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- daemon.Wait() }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = daemon.Process.Kill()
			<-done
		}
	})

	waitForSocket(t, sockPath, 3*time.Second)

	client := coordinator.NewClient(sockPath)

	// Tell the mock `docker ps` that this container is up. Not strictly
	// needed for Start (which only calls compose run + waits for the
	// socket), but it's what a subsequent daemon boot would see if
	// we ever added a reconciliation-on-restart pass to this test.
	writeState(t, stateFile, "claude-run-alpha")

	cfg := &core.Config{
		ProjectName: "alpha",
		ProjectDir:  filepath.Join(root, "src"),
		DataDir:     dataDir,
		Target:      "dev",
		ImagePrefix: "test/claude-runner",
	}

	// Start → the mock docker touches the runner socket at CACHE_DIR/
	// .daedalus/runner.sock, which is exactly cfg.RunnerSocketPath()
	// on the host side. The daemon's waitForSocket sees the file and
	// records the session.
	sess, err := client.Start(cfg)
	if err != nil {
		t.Fatalf("client.Start: %v", err)
	}
	if sess.ProjectName != "alpha" {
		t.Errorf("Start returned ProjectName = %q, want alpha", sess.ProjectName)
	}
	if sess.SocketPath != cfg.RunnerSocketPath() {
		t.Errorf("Start returned SocketPath = %q, want %q", sess.SocketPath, cfg.RunnerSocketPath())
	}

	list, err := client.List()
	if err != nil {
		t.Fatalf("client.List: %v", err)
	}
	if len(list) != 1 || list[0].ProjectName != "alpha" {
		t.Errorf("List = %+v, want single alpha session", list)
	}

	got, err := client.Get("alpha")
	if err != nil {
		t.Fatalf("client.Get: %v", err)
	}
	if got.SocketPath != sess.SocketPath {
		t.Errorf("Get SocketPath = %q, want %q", got.SocketPath, sess.SocketPath)
	}

	// Duplicate Start must surface as ErrAlreadyRunning through the
	// wire (guards against future refactors that lose sentinel fidelity).
	if _, err := client.Start(cfg); !errors.Is(err, coordinator.ErrAlreadyRunning) {
		t.Errorf("duplicate Start error = %v, want ErrAlreadyRunning", err)
	}

	if err := client.Stop("alpha"); err != nil {
		t.Fatalf("client.Stop: %v", err)
	}
	if _, err := client.Get("alpha"); !errors.Is(err, coordinator.ErrNotFound) {
		t.Errorf("post-Stop Get error = %v, want ErrNotFound", err)
	}

	// sessions.json must now show an empty list — the on-disk state
	// tracks the map.
	assertSessionsFileEmpty(t, coordinator.DefaultSessionsFile(dataDir))
}

// buildDaemonBinary compiles the daedalus-coordinator binary into a
// path under root and returns the path.
func buildDaemonBinary(t *testing.T, root string) string {
	t.Helper()
	binPath := filepath.Join(root, "daedalus-coordinator")
	cmd := exec.Command("go", "build", "-o", binPath, "github.com/techdelight/daedalus/cmd/daedalus-coordinator")
	// Avoid contaminating GOPATH/GOCACHE with the test's temp dir.
	cmd.Env = os.Environ()
	cmd.Stdout = testLogWriter{t}
	cmd.Stderr = testLogWriter{t}
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build daedalus-coordinator: %v", err)
	}
	return binPath
}

// writeMockDocker drops a shell script `docker` into a dedicated bin
// directory, returns (dockerBinPath, stateFilePath). The mock answers
// enough of the docker surface for the coordinator's happy path:
//   - `docker compose … run --rm --detach --name X claude` touches
//     the runner socket at $CACHE_DIR/.daedalus/runner.sock so the
//     coordinator's waitForSocket succeeds.
//   - `docker ps --format {{.Names}}` prints whatever
//     $MOCK_DOCKER_STATE contains (one container per line).
//   - `docker stop X` is a no-op.
func writeMockDocker(t *testing.T, root string) (string, string) {
	t.Helper()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	stateFile := filepath.Join(root, "docker-state")

	// The mock is a POSIX shell script. Keeps portability (linux + macOS
	// both have /bin/sh) without pulling extra deps.
	script := `#!/bin/sh
set -eu
case "$1" in
  compose)
    # docker compose -f FILE run --rm --detach --name NAME claude
    # CACHE_DIR is set by coordinator's composeEnv; the runner socket
    # goes at $CACHE_DIR/.daedalus/runner.sock (matches core.Config.
    # RunnerSocketPath layout).
    mkdir -p "$CACHE_DIR/.daedalus"
    touch "$CACHE_DIR/.daedalus/runner.sock"
    exit 0
    ;;
  ps)
    # docker ps --format {{.Names}} — reply from state file.
    if [ -f "$MOCK_DOCKER_STATE" ]; then
      cat "$MOCK_DOCKER_STATE"
    fi
    exit 0
    ;;
  stop)
    # docker stop NAME
    exit 0
    ;;
esac
echo "mock docker: unhandled args: $*" >&2
exit 1
`
	binPath := filepath.Join(binDir, "docker")
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write mock docker: %v", err)
	}
	return binPath, stateFile
}

// writeState overwrites the mock docker's state file with the given
// container names (one per line).
func writeState(t *testing.T, path string, names ...string) {
	t.Helper()
	body := ""
	for _, n := range names {
		body += n + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
}

// waitForSocket polls until the daemon socket file exists or timeout.
func waitForSocket(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon socket %s did not appear within %s", path, timeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func assertSessionsFileEmpty(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return // never written is also "empty"
		}
		t.Fatalf("read sessions file: %v", err)
	}
	// Encoded form of an empty slice from json.MarshalIndent is "[]".
	trimmed := trimSpace(string(data))
	if trimmed != "[]" {
		t.Errorf("sessions file = %q, want []", trimmed)
	}
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\n' || s[0] == '\t' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\n' || s[len(s)-1] == '\t' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// testLogWriter routes subprocess output into the test log so a
// failure shows the daemon or `go build` diagnostics inline instead
// of hiding them at stderr.
type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}
