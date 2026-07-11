// Copyright (C) 2026 Techdelight BV

package coordinator

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// bindTestDaemon boots the daemon HTTP surface on a real Unix socket
// AND writes an "alive" pidfile pointing at the test process. This is
// the state EnsureRunning should hit its fast-path on: pidfile alive +
// socket dialable → return a Client without spawning anything.
func bindTestDaemon(t *testing.T) (sockPath, pidPath string) {
	t.Helper()
	dir := t.TempDir()
	sockPath = filepath.Join(dir, "d.sock")
	pidPath = filepath.Join(dir, "d.pid")

	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server, _ := newTestServer(t)
	srv := &http.Server{Handler: server.Handler()}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})

	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644); err != nil {
		t.Fatalf("write pidfile: %v", err)
	}
	return sockPath, pidPath
}

func TestEnsureRunning_FastPathWhenDaemonAlreadyUp(t *testing.T) {
	sockPath, pidPath := bindTestDaemon(t)

	// No DaemonBin set — proves the fast path doesn't spawn.
	client, err := EnsureRunning(BootstrapOptions{
		SocketPath: sockPath,
		PIDPath:    pidPath,
	})
	if err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	// Prove the returned Client actually reaches our test daemon.
	sessions, err := client.List()
	if err != nil {
		t.Fatalf("client.List: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("len(sessions) = %d, want 0", len(sessions))
	}
}

func TestEnsureRunning_MissingBinaryOnSpawnPath(t *testing.T) {
	dir := t.TempDir()

	// No pidfile, no socket → fast path fails → spawn path runs.
	// With no DaemonBin, we expect an actionable error.
	_, err := EnsureRunning(BootstrapOptions{
		SocketPath: filepath.Join(dir, "d.sock"),
		PIDPath:    filepath.Join(dir, "d.pid"),
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "daemon binary path is required") {
		t.Errorf("error message = %q, want mention of daemon binary path", err)
	}
}

func TestEnsureRunning_BinaryPathDoesNotExist(t *testing.T) {
	dir := t.TempDir()

	_, err := EnsureRunning(BootstrapOptions{
		SocketPath: filepath.Join(dir, "d.sock"),
		PIDPath:    filepath.Join(dir, "d.pid"),
		DaemonBin:  filepath.Join(dir, "nonexistent-daedalus-coordinator"),
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not found at") {
		t.Errorf("error message = %q, want 'not found at' hint", err)
	}
}

// A stale pidfile (numeric, but no such process) must NOT trigger
// the fast path — otherwise a crashed prior daemon would leave the
// system permanently unable to auto-recover.
func TestIsDaemonRunning_StalePIDForcesRespawn(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "d.sock")
	pidPath := filepath.Join(dir, "d.pid")

	// Write an improbably-high pid that almost certainly isn't
	// allocated. Also create a socket file so we don't dodge the
	// pid check on socket absence.
	if err := os.WriteFile(pidPath, []byte("2147483000\n"), 0o644); err != nil {
		t.Fatalf("write pidfile: %v", err)
	}
	if err := os.WriteFile(sockPath, nil, 0o644); err != nil {
		t.Fatalf("write socket file: %v", err)
	}

	if isDaemonRunning(sockPath, pidPath) {
		t.Error("isDaemonRunning = true for stale pid, want false")
	}
}

// A live pid whose socket doesn't accept connections (e.g. the socket
// file exists but nothing is listening) must also fail the check.
func TestIsDaemonRunning_LivePIDButDeadSocket(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "d.sock")
	pidPath := filepath.Join(dir, "d.pid")

	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644); err != nil {
		t.Fatalf("write pidfile: %v", err)
	}
	// Create the socket file as a regular file so DialTimeout fails
	// with something other than ENOENT.
	if err := os.WriteFile(sockPath, nil, 0o644); err != nil {
		t.Fatalf("write socket file: %v", err)
	}

	if isDaemonRunning(sockPath, pidPath) {
		t.Error("isDaemonRunning = true for live pid + un-listening socket, want false")
	}
}
