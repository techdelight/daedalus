// Copyright (C) 2026 Techdelight BV

package runclient

import (
	"bytes"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/techdelight/daedalus/internal/runproto"
)

// fakeServer accepts one Unix-socket connection and exposes the encode
// and decode endpoints to a test. It's just enough scaffolding to drive
// runclient against a real socket without standing up daedalus-runner.
type fakeServer struct {
	listener net.Listener
	conn     net.Conn
	enc      *runproto.Encoder
	dec      *runproto.Decoder
	accepted chan struct{}
}

func startFakeServer(t *testing.T) (*fakeServer, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeServer{listener: l, accepted: make(chan struct{})}
	t.Cleanup(func() { s.Close() })
	return s, path
}

// acceptLoop blocks until a connection is established and then sends
// the supplied initial frames in order. Subsequent test code can use
// s.Encode / s.Decode for further frame-level interaction.
func (s *fakeServer) acceptLoop(t *testing.T, initial ...runproto.Message) {
	t.Helper()
	conn, err := s.listener.Accept()
	if err != nil {
		t.Errorf("accept: %v", err)
		return
	}
	s.conn = conn
	s.enc = runproto.NewEncoder(conn)
	s.dec = runproto.NewDecoder(conn)
	for _, m := range initial {
		if err := s.enc.Encode(m); err != nil {
			t.Errorf("encode initial: %v", err)
			return
		}
	}
	close(s.accepted)
}

func (s *fakeServer) waitAccepted(t *testing.T) {
	t.Helper()
	select {
	case <-s.accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for server to accept")
	}
}

func (s *fakeServer) Close() {
	if s.conn != nil {
		s.conn.Close()
	}
	if s.listener != nil {
		s.listener.Close()
	}
}

func TestDial_CapturesHello(t *testing.T) {
	srv, path := startFakeServer(t)
	scroll := []byte("previous output\r\n")
	go srv.acceptLoop(t, runproto.NewHello(scroll, 120, 40))

	conn, err := Dial(path)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	srv.waitAccepted(t)

	h := conn.Hello()
	if !bytes.Equal(h.Scrollback, scroll) {
		t.Errorf("Scrollback = %q, want %q", h.Scrollback, scroll)
	}
	if h.Cols != 120 || h.Rows != 40 {
		t.Errorf("size = (%d,%d), want (120,40)", h.Cols, h.Rows)
	}
}

func TestRead_ReplaysScrollbackThenLiveOutput(t *testing.T) {
	srv, path := startFakeServer(t)
	go srv.acceptLoop(t, runproto.NewHello([]byte("OLD"), 80, 24))

	conn, err := Dial(path)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	srv.waitAccepted(t)

	// Push live output from the server.
	if err := srv.enc.Encode(runproto.NewOutput([]byte("NEW"))); err != nil {
		t.Fatalf("server encode: %v", err)
	}

	got := readAtLeast(t, conn, 6)
	if string(got) != "OLDNEW" {
		t.Errorf("Read = %q, want %q", got, "OLDNEW")
	}
}

func TestWrite_EmitsInputFrame(t *testing.T) {
	srv, path := startFakeServer(t)
	go srv.acceptLoop(t, runproto.NewHello(nil, 80, 24))

	conn, err := Dial(path)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	srv.waitAccepted(t)

	if _, err := conn.Write([]byte("ls\r")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	m := mustDecode(t, srv)
	if m.Type != runproto.TypeInput {
		t.Errorf("Type = %q, want %q", m.Type, runproto.TypeInput)
	}
	if string(m.Data) != "ls\r" {
		t.Errorf("Data = %q, want %q", m.Data, "ls\r")
	}
}

func TestResize_EmitsResizeFrame(t *testing.T) {
	srv, path := startFakeServer(t)
	go srv.acceptLoop(t, runproto.NewHello(nil, 80, 24))

	conn, err := Dial(path)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	srv.waitAccepted(t)

	if err := conn.Resize(132, 50); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	m := mustDecode(t, srv)
	if m.Type != runproto.TypeResize || m.Cols != 132 || m.Rows != 50 {
		t.Errorf("frame = %+v, want resize 132x50", m)
	}
}

func TestDetach_SendsDetachAndCloses(t *testing.T) {
	srv, path := startFakeServer(t)
	go srv.acceptLoop(t, runproto.NewHello(nil, 80, 24))

	conn, err := Dial(path)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	srv.waitAccepted(t)

	if err := conn.Detach(); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	m := mustDecode(t, srv)
	if m.Type != runproto.TypeDetach {
		t.Errorf("Type = %q, want %q", m.Type, runproto.TypeDetach)
	}

	// Read should return io.EOF after detach closes the pipe.
	if _, err := conn.Read(make([]byte, 4)); !errors.Is(err, io.EOF) {
		t.Errorf("Read after Detach: err = %v, want io.EOF", err)
	}
}

func TestWait_ReturnsExitCode(t *testing.T) {
	srv, path := startFakeServer(t)
	go srv.acceptLoop(t, runproto.NewHello(nil, 80, 24))

	conn, err := Dial(path)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	srv.waitAccepted(t)

	if err := srv.enc.Encode(runproto.NewExit(7)); err != nil {
		t.Fatalf("server encode exit: %v", err)
	}

	done := make(chan int, 1)
	go func() { done <- conn.Wait() }()
	select {
	case code := <-done:
		if code != 7 {
			t.Errorf("Wait = %d, want 7", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after exit frame")
	}
}

func TestWait_ReturnsMinusOneOnConnectionDrop(t *testing.T) {
	srv, path := startFakeServer(t)
	go srv.acceptLoop(t, runproto.NewHello(nil, 80, 24))

	conn, err := Dial(path)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	srv.waitAccepted(t)

	// Drop the server side without sending exit.
	srv.conn.Close()

	done := make(chan int, 1)
	go func() { done <- conn.Wait() }()
	select {
	case code := <-done:
		if code != -1 {
			t.Errorf("Wait = %d, want -1", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after server drop")
	}
}

func TestRead_ReturnsEOFAfterClose(t *testing.T) {
	srv, path := startFakeServer(t)
	go srv.acceptLoop(t, runproto.NewHello(nil, 80, 24))

	conn, err := Dial(path)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	srv.waitAccepted(t)

	conn.Close()
	if _, err := conn.Read(make([]byte, 4)); !errors.Is(err, io.EOF) {
		t.Errorf("err = %v, want io.EOF", err)
	}
}

func TestDial_RejectsNonHelloFirstFrame(t *testing.T) {
	srv, path := startFakeServer(t)
	go srv.acceptLoop(t, runproto.NewOutput([]byte("not hello")))

	_, err := Dial(path)
	if err == nil {
		t.Fatal("Dial accepted non-hello first frame")
	}
	if !strings.Contains(err.Error(), "hello") {
		t.Errorf("error should mention hello: %v", err)
	}
}

func TestDial_NonexistentSocket(t *testing.T) {
	_, err := Dial("/does/not/exist.sock")
	if err == nil {
		t.Fatal("Dial of nonexistent socket returned nil error")
	}
}

func TestRunnerEvent_DroppedSilently(t *testing.T) {
	srv, path := startFakeServer(t)
	go srv.acceptLoop(t, runproto.NewHello(nil, 80, 24))

	conn, err := Dial(path)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	srv.waitAccepted(t)

	// runner-event then output — output should still arrive on Read
	// even though we discarded the event.
	if err := srv.enc.Encode(runproto.NewRunnerEvent("tool_use", map[string]any{"name": "Read"})); err != nil {
		t.Fatalf("server encode event: %v", err)
	}
	if err := srv.enc.Encode(runproto.NewOutput([]byte("after"))); err != nil {
		t.Fatalf("server encode output: %v", err)
	}

	got := readAtLeast(t, conn, 5)
	if string(got) != "after" {
		t.Errorf("Read = %q, want %q", got, "after")
	}
}

// readAtLeast reads from r until at least n bytes have been collected
// or the read deadline elapses. Useful when the producer writes in
// pieces and the test wants the concatenation.
func readAtLeast(t *testing.T, r io.Reader, n int) []byte {
	t.Helper()
	buf := make([]byte, 0, n)
	tmp := make([]byte, 256)
	deadline := time.Now().Add(2 * time.Second)
	for len(buf) < n && time.Now().Before(deadline) {
		// Read in a goroutine so we can time-bound it.
		done := make(chan int, 1)
		go func() {
			k, _ := r.Read(tmp)
			done <- k
		}()
		select {
		case k := <-done:
			if k > 0 {
				buf = append(buf, tmp[:k]...)
			}
		case <-time.After(time.Until(deadline)):
			return buf
		}
	}
	return buf
}

func mustDecode(t *testing.T, s *fakeServer) runproto.Message {
	t.Helper()
	type result struct {
		m   runproto.Message
		err error
	}
	ch := make(chan result, 1)
	go func() {
		m, err := s.dec.Decode()
		ch <- result{m, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("server decode: %v", r.err)
		}
		return r.m
	case <-time.After(2 * time.Second):
		t.Fatal("server decode timed out")
		return runproto.Message{}
	}
}
