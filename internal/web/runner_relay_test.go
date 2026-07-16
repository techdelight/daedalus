// Copyright (C) 2026 Techdelight BV

package web

import (
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/techdelight/daedalus/internal/runclient"
	"github.com/techdelight/daedalus/internal/runproto"

	"github.com/gorilla/websocket"
)

// fakeRunner is a minimal stand-in for daedalus-runner: it binds a Unix
// socket, accepts one connection, sends a hello frame, and then exposes
// its codec so a test can push or read further frames.
type fakeRunner struct {
	listener net.Listener
	conn     net.Conn
	enc      *runproto.Encoder
	dec      *runproto.Decoder
	ready    chan struct{}
}

func startFakeRunner(t *testing.T, hello runproto.Message) (*fakeRunner, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "runner.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	fr := &fakeRunner{listener: l, ready: make(chan struct{})}
	t.Cleanup(fr.Close)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		fr.conn = c
		fr.enc = runproto.NewEncoder(c)
		fr.dec = runproto.NewDecoder(c)
		_ = fr.enc.Encode(hello)
		close(fr.ready)
	}()
	return fr, path
}

func (fr *fakeRunner) waitReady(t *testing.T) {
	t.Helper()
	select {
	case <-fr.ready:
	case <-time.After(2 * time.Second):
		t.Fatal("fakeRunner did not accept in time")
	}
}

func (fr *fakeRunner) Close() {
	if fr.conn != nil {
		fr.conn.Close()
	}
	if fr.listener != nil {
		fr.listener.Close()
	}
}

// startRelayServer wires runnerRelay into an httptest server: each
// incoming WebSocket connection dials the given runner socket and runs
// the relay until either side closes.
func startRelayServer(t *testing.T, sockPath string) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer ws.Close()
		rc, err := runclient.Dial(sockPath)
		if err != nil {
			t.Errorf("dial runner: %v", err)
			return
		}
		defer rc.Close()
		newRunnerRelay(rc, newSafeConn(ws), "test-project").Run()
	}))
	t.Cleanup(srv.Close)
	return srv
}

func dialWS(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestRunnerRelay_HelloScrollbackFlowsToWebSocket(t *testing.T) {
	fr, sock := startFakeRunner(t, runproto.NewHello([]byte("OLD"), 80, 24))
	srv := startRelayServer(t, sock)
	ws := dialWS(t, srv)
	fr.waitReady(t)

	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	mt, data, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if mt != websocket.BinaryMessage {
		t.Errorf("message type = %d, want BinaryMessage", mt)
	}
	if string(data) != "OLD" {
		t.Errorf("payload = %q, want %q", string(data), "OLD")
	}
}

func TestRunnerRelay_RunnerOutputForwardedToWebSocket(t *testing.T) {
	fr, sock := startFakeRunner(t, runproto.NewHello(nil, 80, 24))
	srv := startRelayServer(t, sock)
	ws := dialWS(t, srv)
	fr.waitReady(t)

	if err := fr.enc.Encode(runproto.NewOutput([]byte("hello"))); err != nil {
		t.Fatalf("server encode: %v", err)
	}

	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("payload = %q, want %q", string(data), "hello")
	}
}

func TestRunnerRelay_BinaryWebSocketBecomesInputFrame(t *testing.T) {
	fr, sock := startFakeRunner(t, runproto.NewHello(nil, 80, 24))
	srv := startRelayServer(t, sock)
	ws := dialWS(t, srv)
	fr.waitReady(t)

	if err := ws.WriteMessage(websocket.BinaryMessage, []byte("typed")); err != nil {
		t.Fatalf("ws write: %v", err)
	}

	m := readNextFromRunner(t, fr)
	if m.Type != runproto.TypeInput {
		t.Fatalf("frame type = %q, want %q", m.Type, runproto.TypeInput)
	}
	if string(m.Data) != "typed" {
		t.Errorf("input data = %q, want %q", string(m.Data), "typed")
	}
}

func TestRunnerRelay_ResizeJSONBecomesResizeFrame(t *testing.T) {
	fr, sock := startFakeRunner(t, runproto.NewHello(nil, 80, 24))
	srv := startRelayServer(t, sock)
	ws := dialWS(t, srv)
	fr.waitReady(t)

	if err := ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"resize","cols":132,"rows":50}`)); err != nil {
		t.Fatalf("ws write: %v", err)
	}

	m := readNextFromRunner(t, fr)
	if m.Type != runproto.TypeResize {
		t.Fatalf("frame type = %q, want %q", m.Type, runproto.TypeResize)
	}
	if m.Cols != 132 || m.Rows != 50 {
		t.Errorf("size = (%d,%d), want (132,50)", m.Cols, m.Rows)
	}
}

func TestRunnerRelay_NonResizeTextBecomesInput(t *testing.T) {
	fr, sock := startFakeRunner(t, runproto.NewHello(nil, 80, 24))
	srv := startRelayServer(t, sock)
	ws := dialWS(t, srv)
	fr.waitReady(t)

	// Anything that isn't a "resize" message is treated as keystrokes.
	// Mirrors the legacy PTY path so existing terminal.js stays compatible.
	payload := []byte("plain text")
	if err := ws.WriteMessage(websocket.TextMessage, payload); err != nil {
		t.Fatalf("ws write: %v", err)
	}

	m := readNextFromRunner(t, fr)
	if m.Type != runproto.TypeInput {
		t.Fatalf("frame type = %q, want %q", m.Type, runproto.TypeInput)
	}
	if string(m.Data) != string(payload) {
		t.Errorf("input data = %q, want %q", string(m.Data), string(payload))
	}
}

// readNextFromRunner blocks until the fake runner has buffered a frame,
// decodes one, and returns it. The decode runs on a goroutine so a stuck
// codec can't hang the whole test process.
func readNextFromRunner(t *testing.T, fr *fakeRunner) runproto.Message {
	t.Helper()
	type result struct {
		m   runproto.Message
		err error
	}
	ch := make(chan result, 1)
	go func() {
		m, err := fr.dec.Decode()
		ch <- result{m, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("server decode: %v", r.err)
		}
		return r.m
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for runner frame")
	}
	return runproto.Message{}
}
