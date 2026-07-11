// Copyright (C) 2026 Techdelight BV

package coordinator

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/executor"
)

// newClientAgainstDaemon wires a Client against an httptest.Server
// that serves the real daemon Handler. Both the Server and the
// Coordinator behind it are returned so tests can inspect state and
// arm the spy executor. This is the client equivalent of the daemon
// test's newTestServer + startTest pair.
func newClientAgainstDaemon(t *testing.T) (*Client, *httptest.Server, *spyExec) {
	t.Helper()
	server, exec := newTestServer(t)
	srv := startTest(t, server)
	client := newClient(srv.URL, srv.Client())
	return client, srv, exec
}

func mockConfig(t *testing.T, exec *spyExec, projectName string) *core.Config {
	t.Helper()
	dataDir := t.TempDir()
	cfg := &core.Config{
		ProjectName: projectName,
		ProjectDir:  filepath.Join(dataDir, "src"),
		DataDir:     dataDir,
		Target:      "dev",
		ImagePrefix: "test/claude-runner",
	}
	exec.onRunWithEnv = func(_ []string, _ string, _ ...string) {
		touchSocket(t, cfg.RunnerSocketPath())
	}
	return cfg
}

func TestClient_StartHappyPath(t *testing.T) {
	client, _, exec := newClientAgainstDaemon(t)
	cfg := mockConfig(t, exec, "alpha")

	sess, err := client.Start(cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if sess.ProjectName != "alpha" {
		t.Errorf("ProjectName = %q, want alpha", sess.ProjectName)
	}
	if sess.SocketPath == "" {
		t.Error("SocketPath empty")
	}
	if sess.StartedAt.IsZero() {
		t.Error("StartedAt zero")
	}
}

// The 409 must surface as ErrAlreadyRunning specifically so callers
// that used to switch on the in-process sentinel keep working after
// swapping Coordinator for Client.
func TestClient_StartReturnsErrAlreadyRunningOn409(t *testing.T) {
	client, _, exec := newClientAgainstDaemon(t)
	cfg := mockConfig(t, exec, "alpha")

	if _, err := client.Start(cfg); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	_, err := client.Start(cfg)
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("second Start error = %v, want ErrAlreadyRunning", err)
	}
}

func TestClient_StartSurfacesDockerError(t *testing.T) {
	client, _, exec := newClientAgainstDaemon(t)
	exec.Results["docker"] = executor.MockResult{Err: errors.New("daemon not running")}
	cfg := mockConfig(t, exec, "alpha")

	_, err := client.Start(cfg)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Should be a generic error, not one of the sentinels.
	if errors.Is(err, ErrAlreadyRunning) || errors.Is(err, ErrNotFound) {
		t.Errorf("error should be generic, got sentinel: %v", err)
	}
	if !strings.Contains(err.Error(), "daemon not running") {
		t.Errorf("error missing daemon message: %v", err)
	}
}

func TestClient_ListEmpty(t *testing.T) {
	client, _, _ := newClientAgainstDaemon(t)

	sessions, err := client.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if sessions == nil {
		t.Error("List returned nil; want empty slice")
	}
	if len(sessions) != 0 {
		t.Errorf("len = %d, want 0", len(sessions))
	}
}

func TestClient_ListTracksSessions(t *testing.T) {
	client, _, exec := newClientAgainstDaemon(t)

	for _, name := range []string{"alpha", "beta"} {
		if _, err := client.Start(mockConfig(t, exec, name)); err != nil {
			t.Fatalf("Start %s: %v", name, err)
		}
	}

	sessions, err := client.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("len = %d, want 2 (sessions: %+v)", len(sessions), sessions)
	}
}

func TestClient_GetKnown(t *testing.T) {
	client, _, exec := newClientAgainstDaemon(t)
	if _, err := client.Start(mockConfig(t, exec, "alpha")); err != nil {
		t.Fatalf("Start: %v", err)
	}

	sess, err := client.Get("alpha")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if sess.ProjectName != "alpha" {
		t.Errorf("ProjectName = %q, want alpha", sess.ProjectName)
	}
}

func TestClient_GetUnknownReturnsErrNotFound(t *testing.T) {
	client, _, _ := newClientAgainstDaemon(t)

	_, err := client.Get("nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Get unknown error = %v, want ErrNotFound", err)
	}
}

// A project name containing slashes would break the URL if the client
// didn't escape it; verifies url.PathEscape is applied.
func TestClient_GetEscapesPathSegment(t *testing.T) {
	client, _, _ := newClientAgainstDaemon(t)

	// The daemon won't have this session, so we expect 404 — but the
	// important thing is that the request reaches the daemon at all
	// (no client-side URL parse error).
	_, err := client.Get("weird/name with space")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Get on escaped name error = %v, want ErrNotFound", err)
	}
}

func TestClient_StopKnown(t *testing.T) {
	client, _, exec := newClientAgainstDaemon(t)
	if _, err := client.Start(mockConfig(t, exec, "alpha")); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := client.Stop("alpha"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Round-trip: session should be gone now.
	_, err := client.Get("alpha")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("post-Stop Get error = %v, want ErrNotFound", err)
	}
}

func TestClient_StopUnknownReturnsErrNotFound(t *testing.T) {
	client, _, _ := newClientAgainstDaemon(t)

	err := client.Stop("nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Stop unknown error = %v, want ErrNotFound", err)
	}
}

// Docker-stop failure keeps the session findable server-side (that's
// tested in coordinator_test.go). The client must surface a generic
// error — not ErrNotFound — so callers know retry is possible.
func TestClient_StopSurfacesDockerError(t *testing.T) {
	client, _, exec := newClientAgainstDaemon(t)
	if _, err := client.Start(mockConfig(t, exec, "alpha")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	exec.Results["docker"] = executor.MockResult{Err: errors.New("daemon hiccup")}

	err := client.Stop("alpha")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, ErrNotFound) {
		t.Errorf("error should be generic, got ErrNotFound: %v", err)
	}
	if !strings.Contains(err.Error(), "daemon hiccup") {
		t.Errorf("error missing docker message: %v", err)
	}
}

// Transport failure (nothing listening at the URL) must not be
// confused with ErrNotFound — nothing was even asked.
func TestClient_TransportErrorSurfaces(t *testing.T) {
	client := newClient("http://127.0.0.1:1", &http.Client{})
	_, err := client.List()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("transport error masqueraded as sentinel: %v", err)
	}
}

// TestClient_EndToEndOverRealUDS is the full-stack smoke test: bind a
// real Unix socket, run the daemon's http.Server on it, use the real
// NewClient(sockPath) constructor. Guards against transport-shape
// regressions that httptest can't catch (URL scheme, dialer, etc.).
func TestClient_EndToEndOverRealUDS(t *testing.T) {
	server, exec := newTestServer(t)

	sockPath := filepath.Join(t.TempDir(), "d.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	httpSrv := &http.Server{Handler: server.Handler()}
	go func() { _ = httpSrv.Serve(l) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
	})

	client := NewClient(sockPath)

	// Start → List → Get → Stop, all via the wire.
	cfg := mockConfig(t, exec, "alpha")
	sess, err := client.Start(cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if sess.ProjectName != "alpha" {
		t.Errorf("ProjectName = %q, want alpha", sess.ProjectName)
	}

	list, err := client.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("len(List) = %d, want 1", len(list))
	}

	got, err := client.Get("alpha")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SocketPath != sess.SocketPath {
		t.Errorf("Get SocketPath = %q, want %q", got.SocketPath, sess.SocketPath)
	}

	if err := client.Stop("alpha"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := client.Get("alpha"); !errors.Is(err, ErrNotFound) {
		t.Errorf("post-Stop Get error = %v, want ErrNotFound", err)
	}
}
