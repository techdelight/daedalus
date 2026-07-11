// Copyright (C) 2026 Techdelight BV

package coordinator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/executor"
)

// newTestServer returns a Server backed by a Coordinator wired to a
// spy executor. The wrapped Coordinator has short timeouts so the
// happy-path tests don't sit on the 30s default socket wait.
func newTestServer(t *testing.T) (*Server, *spyExec) {
	t.Helper()
	exec := newSpyExec()
	coord := newTestCoordinator(t, exec)
	return NewServer(coord), exec
}

// startTest returns an httptest.Server serving the daemon handler.
// Preferable over ListenAndServeUDS for tests that only care about
// the wire format; the real UDS path is covered by its own test
// below.
func startTest(t *testing.T, s *Server) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv
}

// mockStartConfig arms the spy so the runner "socket" appears the
// moment docker compose run fires — mirrors the pattern the coord
// tests already use.
func mockStartConfig(t *testing.T, exec *spyExec, projectName string) *StartRequest {
	t.Helper()
	dataDir := t.TempDir()
	req := &StartRequest{
		ProjectName: projectName,
		ProjectDir:  filepath.Join(dataDir, "src"),
		DataDir:     dataDir,
		Target:      "dev",
		ImagePrefix: "test/claude-runner",
	}
	sockPath := (&core.Config{DataDir: dataDir, ProjectName: projectName}).RunnerSocketPath()
	exec.onRunWithEnv = func(_ []string, _ string, _ ...string) {
		touchSocket(t, sockPath)
	}
	return req
}

func postJSON(t *testing.T, srv *httptest.Server, path string, body interface{}) *http.Response {
	t.Helper()
	buf := &bytes.Buffer{}
	if err := json.NewEncoder(buf).Encode(body); err != nil {
		t.Fatalf("encode: %v", err)
	}
	resp, err := http.Post(srv.URL+path, "application/json", buf)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func decodeSession(t *testing.T, resp *http.Response) Session {
	t.Helper()
	var s Session
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	return s
}

func decodeSessions(t *testing.T, resp *http.Response) []Session {
	t.Helper()
	var s []Session
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	return s
}

func decodeError(t *testing.T, resp *http.Response) string {
	t.Helper()
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	return body["error"]
}

func TestDaemon_StartCreatesSession(t *testing.T) {
	s, exec := newTestServer(t)
	srv := startTest(t, s)

	req := mockStartConfig(t, exec, "alpha")
	resp := postJSON(t, srv, "/sessions", req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body: %s)", resp.StatusCode, http.StatusCreated, decodeError(t, resp))
	}
	sess := decodeSession(t, resp)
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

func TestDaemon_StartRejectsEmptyProjectName(t *testing.T) {
	s, _ := newTestServer(t)
	srv := startTest(t, s)

	resp := postJSON(t, srv, "/sessions", &StartRequest{ProjectName: ""})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestDaemon_StartRejectsInvalidJSON(t *testing.T) {
	s, _ := newTestServer(t)
	srv := startTest(t, s)

	resp, err := http.Post(srv.URL+"/sessions", "application/json", strings.NewReader("{not-json"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestDaemon_StartReturns409OnDuplicate(t *testing.T) {
	s, exec := newTestServer(t)
	srv := startTest(t, s)

	req := mockStartConfig(t, exec, "alpha")
	resp := postJSON(t, srv, "/sessions", req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first Start status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	resp2 := postJSON(t, srv, "/sessions", req)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("second Start status = %d, want %d", resp2.StatusCode, http.StatusConflict)
	}
	if msg := decodeError(t, resp2); !strings.Contains(msg, "already running") {
		t.Errorf("error body %q missing 'already running'", msg)
	}
}

func TestDaemon_StartReturns500OnDockerError(t *testing.T) {
	s, exec := newTestServer(t)
	srv := startTest(t, s)

	exec.Results["docker"] = executor.MockResult{Err: errors.New("daemon not running")}

	req := mockStartConfig(t, exec, "alpha")
	resp := postJSON(t, srv, "/sessions", req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}
}

func TestDaemon_ListReturnsEmptyArrayNotNull(t *testing.T) {
	s, _ := newTestServer(t)
	srv := startTest(t, s)

	resp, err := http.Get(srv.URL + "/sessions")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	// Read the raw body first: the JSON payload matters, not just
	// the decoded slice — clients that switch on presence would break
	// on a `null` response.
	buf := &bytes.Buffer{}
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read: %v", err)
	}
	got := strings.TrimSpace(buf.String())
	if got != "[]" {
		t.Errorf("body = %q, want []", got)
	}
}

func TestDaemon_ListReturnsTrackedSessions(t *testing.T) {
	s, exec := newTestServer(t)
	srv := startTest(t, s)

	for _, name := range []string{"alpha", "beta"} {
		req := mockStartConfig(t, exec, name)
		resp := postJSON(t, srv, "/sessions", req)
		resp.Body.Close()
		time.Sleep(2 * time.Millisecond)
	}

	resp, err := http.Get(srv.URL + "/sessions")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	sessions := decodeSessions(t, resp)
	if len(sessions) != 2 {
		t.Fatalf("len = %d, want 2 (sessions: %+v)", len(sessions), sessions)
	}
	if sessions[0].ProjectName != "alpha" || sessions[1].ProjectName != "beta" {
		t.Errorf("names = %q,%q, want alpha,beta", sessions[0].ProjectName, sessions[1].ProjectName)
	}
}

func TestDaemon_GetKnown(t *testing.T) {
	s, exec := newTestServer(t)
	srv := startTest(t, s)

	req := mockStartConfig(t, exec, "alpha")
	resp := postJSON(t, srv, "/sessions", req)
	resp.Body.Close()

	resp2, err := http.Get(srv.URL + "/sessions/alpha")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp2.StatusCode, http.StatusOK)
	}
	sess := decodeSession(t, resp2)
	if sess.ProjectName != "alpha" {
		t.Errorf("ProjectName = %q, want alpha", sess.ProjectName)
	}
}

func TestDaemon_GetUnknown(t *testing.T) {
	s, _ := newTestServer(t)
	srv := startTest(t, s)

	resp, err := http.Get(srv.URL + "/sessions/nope")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestDaemon_StopKnown(t *testing.T) {
	s, exec := newTestServer(t)
	srv := startTest(t, s)

	req := mockStartConfig(t, exec, "alpha")
	resp := postJSON(t, srv, "/sessions", req)
	resp.Body.Close()

	dreq, _ := http.NewRequest(http.MethodDelete, srv.URL+"/sessions/alpha", nil)
	dresp, err := http.DefaultClient.Do(dreq)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer dresp.Body.Close()
	if dresp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", dresp.StatusCode, http.StatusNoContent)
	}

	// A follow-up GET should now 404 — verifies the session actually
	// left the map, not that DELETE returned success by mistake.
	gresp, err := http.Get(srv.URL + "/sessions/alpha")
	if err != nil {
		t.Fatalf("post-stop GET: %v", err)
	}
	defer gresp.Body.Close()
	if gresp.StatusCode != http.StatusNotFound {
		t.Errorf("post-stop GET status = %d, want 404", gresp.StatusCode)
	}
}

func TestDaemon_StopUnknown(t *testing.T) {
	s, _ := newTestServer(t)
	srv := startTest(t, s)

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/sessions/nope", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestDaemon_StopReturns500OnDockerError(t *testing.T) {
	s, exec := newTestServer(t)
	srv := startTest(t, s)

	req := mockStartConfig(t, exec, "alpha")
	resp := postJSON(t, srv, "/sessions", req)
	resp.Body.Close()

	exec.Results["docker"] = executor.MockResult{Err: errors.New("daemon hiccup")}

	dreq, _ := http.NewRequest(http.MethodDelete, srv.URL+"/sessions/alpha", nil)
	dresp, err := http.DefaultClient.Do(dreq)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer dresp.Body.Close()
	if dresp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", dresp.StatusCode, http.StatusInternalServerError)
	}
	// Coordinator.Stop deliberately keeps the session on docker error
	// so an operator can retry — the daemon must not paper over that.
	gresp, err := http.Get(srv.URL + "/sessions/alpha")
	if err != nil {
		t.Fatalf("post-failed-stop GET: %v", err)
	}
	defer gresp.Body.Close()
	if gresp.StatusCode != http.StatusOK {
		t.Errorf("session removed despite docker error; GET status = %d, want 200", gresp.StatusCode)
	}
}

// TestDaemon_ServeOverRealUDS is the integration smoke test: bind a
// real Unix socket, POST a session over HTTP-over-UDS, verify the
// coordinator actually recorded it. Guards against any future
// refactor that silently drops the ListenAndServeUDS wiring or
// changes the on-wire path shape.
func TestDaemon_ServeOverRealUDS(t *testing.T) {
	s, exec := newTestServer(t)

	// Bind under a short path — macOS caps sun_path at 104 chars,
	// and t.TempDir() paths can be long.
	sockPath := filepath.Join(t.TempDir(), "d.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: s.Handler()}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sockPath)
			},
		},
	}

	req := mockStartConfig(t, exec, "alpha")
	buf := &bytes.Buffer{}
	if err := json.NewEncoder(buf).Encode(req); err != nil {
		t.Fatalf("encode: %v", err)
	}
	resp, err := client.Post("http://coordinator/sessions", "application/json", buf)
	if err != nil {
		t.Fatalf("POST over UDS: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	sess := decodeSession(t, resp)
	if sess.ProjectName != "alpha" {
		t.Errorf("ProjectName = %q, want alpha", sess.ProjectName)
	}
}

func TestDefaultSocketPath(t *testing.T) {
	got := DefaultSocketPath("/data/daedalus")
	want := "/data/daedalus/.daedalus/coordinator.sock"
	if got != want {
		t.Errorf("DefaultSocketPath = %q, want %q", got, want)
	}
}

// ListenAndServeUDS is exercised indirectly by TestDaemon_ServeOverRealUDS
// (which stitches together the same net.Listen + http.Server pieces).
// A direct test that then Close()s the server would be redundant.
// Keep this comment as a signpost so a future reader doesn't add one.
func TestDaemon_ListenAndServeUDS_StaleSocketReplaced(t *testing.T) {
	// The one thing not covered above: the stale-socket-cleanup
	// branch. A pre-existing file at the target path must be removed
	// before bind, otherwise Listen fails with EADDRINUSE.
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "d.sock")
	if err := os.WriteFile(sockPath, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale: %v", err)
	}

	s, _ := newTestServer(t)
	errCh := make(chan error, 1)
	go func() { errCh <- s.ListenAndServeUDS(sockPath) }()

	// Poll for the socket to become a real UDS. Once we get ECONNREFUSED
	// or a successful dial the server is up; a stale-file bind failure
	// would surface on errCh instead.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if time.Now().After(deadline) {
			select {
			case err := <-errCh:
				t.Fatalf("Serve exited: %v", err)
			default:
				t.Fatal("timeout waiting for socket to become listener")
			}
		}
		c, dialErr := net.Dial("unix", sockPath)
		if dialErr == nil {
			c.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Best-effort shutdown — we don't have the *http.Server handle,
	// so close by removing the socket + letting the goroutine leak
	// beyond test end. In real code the daemon binary owns lifecycle.
	// For hygiene, close via a client request that Server.Serve will
	// process, then remove the socket.
	_ = os.Remove(sockPath)
	select {
	case <-errCh:
	case <-time.After(500 * time.Millisecond):
		// Serve blocking is fine; goroutine ends when the process ends.
	}
}
