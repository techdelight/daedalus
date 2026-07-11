// Copyright (C) 2026 Techdelight BV

package main

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/techdelight/daedalus/internal/runproto"
)

// fakePty captures writes (PTY stdin) and resize calls (Setsize).
type fakePty struct {
	mu      sync.Mutex
	writes  bytes.Buffer
	resizes []resizeCall
}

type resizeCall struct{ cols, rows int }

func (f *fakePty) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes.Write(p)
}

func (f *fakePty) writtenBytes() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.writes.Bytes()...)
}

func (f *fakePty) setSize(cols, rows int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resizes = append(f.resizes, resizeCall{cols, rows})
	return nil
}

func (f *fakePty) lastResize() (resizeCall, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.resizes) == 0 {
		return resizeCall{}, false
	}
	return f.resizes[len(f.resizes)-1], true
}

// drain reads everything currently on c.Out() into a slice without
// blocking on more frames than are immediately available. Polls for
// up to 200ms so the hub goroutine has time to process.
func drain(t *testing.T, c *Client, expectAtLeast int) []runproto.Message {
	t.Helper()
	deadline := time.Now().Add(200 * time.Millisecond)
	var got []runproto.Message
	for {
		select {
		case m, ok := <-c.Out():
			if !ok {
				return got
			}
			got = append(got, m)
			if len(got) >= expectAtLeast {
				// Try to grab anything else without waiting much longer.
				for {
					select {
					case m, ok := <-c.Out():
						if !ok {
							return got
						}
						got = append(got, m)
					case <-time.After(20 * time.Millisecond):
						return got
					}
				}
			}
		case <-time.After(time.Until(deadline)):
			return got
		}
		if time.Now().After(deadline) {
			return got
		}
	}
}

func TestHub_HelloOnConnect(t *testing.T) {
	pty := &fakePty{}
	h := NewHub(pty, pty.setSize, 1024, 0, 0)
	go h.Run()
	defer h.Stop()

	// Append some scrollback before the client arrives.
	h.FromPty([]byte("welcome\r\n"))
	time.Sleep(20 * time.Millisecond)

	c := NewClient()
	h.Add(c)

	got := drain(t, c, 1)
	if len(got) == 0 {
		t.Fatal("client received no frames")
	}
	if got[0].Type != runproto.TypeHello {
		t.Fatalf("first frame type = %q, want %q", got[0].Type, runproto.TypeHello)
	}
	if !bytes.Equal(got[0].Scrollback, []byte("welcome\r\n")) {
		t.Errorf("scrollback = %q, want %q", got[0].Scrollback, "welcome\r\n")
	}
}

func TestHub_InitialSizeAppliedOnStartup(t *testing.T) {
	pty := &fakePty{}
	// Non-zero initial dimensions: the hub must size the PTY at startup,
	// before any client connects, so the agent renders into a real
	// terminal instead of the 0x0 default (see hub.applyInitialSize).
	h := NewHub(pty, pty.setSize, 1024, 80, 24)
	go h.Run()
	defer h.Stop()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if last, ok := pty.lastResize(); ok && last.cols == 80 && last.rows == 24 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	last, ok := pty.lastResize()
	t.Errorf("startup resize = %+v (ok=%v), want cols=80 rows=24", last, ok)
}

func TestHub_NoInitialResizeWhenZero(t *testing.T) {
	pty := &fakePty{}
	// Zero on either axis disables startup sizing; nothing should reach
	// setSize until a client negotiates real dimensions.
	h := NewHub(pty, pty.setSize, 1024, 0, 0)
	go h.Run()
	defer h.Stop()

	time.Sleep(50 * time.Millisecond)
	if last, ok := pty.lastResize(); ok {
		t.Errorf("unexpected startup resize %+v; want none", last)
	}
}

func TestHub_BroadcastsToMultipleClients(t *testing.T) {
	pty := &fakePty{}
	h := NewHub(pty, pty.setSize, 1024, 0, 0)
	go h.Run()
	defer h.Stop()

	c1, c2 := NewClient(), NewClient()
	h.Add(c1)
	h.Add(c2)
	time.Sleep(20 * time.Millisecond)
	// Drain initial hello frames.
	drain(t, c1, 1)
	drain(t, c2, 1)

	h.FromPty([]byte("hi"))

	for i, c := range []*Client{c1, c2} {
		got := drain(t, c, 1)
		if len(got) == 0 {
			t.Fatalf("client %d got no output frame", i)
		}
		if got[0].Type != runproto.TypeOutput || string(got[0].Data) != "hi" {
			t.Errorf("client %d frame = %+v", i, got[0])
		}
	}
}

func TestHub_InputForwardedToPty(t *testing.T) {
	pty := &fakePty{}
	h := NewHub(pty, pty.setSize, 1024, 0, 0)
	go h.Run()
	defer h.Stop()

	h.Input([]byte("ls\r"))
	// Allow the event loop to process.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := pty.writtenBytes(); bytes.Equal(got, []byte("ls\r")) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("pty stdin = %q, want %q", pty.writtenBytes(), "ls\r")
}

func TestHub_ResizePicksMin(t *testing.T) {
	pty := &fakePty{}
	h := NewHub(pty, pty.setSize, 1024, 0, 0)
	go h.Run()
	defer h.Stop()

	c1, c2 := NewClient(), NewClient()
	h.Add(c1)
	h.Add(c2)
	time.Sleep(20 * time.Millisecond)

	h.Resize(c1, 200, 80)
	h.Resize(c2, 100, 40)

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		last, ok := pty.lastResize()
		if ok && last.cols == 100 && last.rows == 40 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	last, _ := pty.lastResize()
	t.Errorf("last resize = %+v, want cols=100 rows=40", last)
}

func TestHub_ResizeIgnoredUntilTwoDimensions(t *testing.T) {
	pty := &fakePty{}
	h := NewHub(pty, pty.setSize, 1024, 0, 0)
	go h.Run()
	defer h.Stop()

	c := NewClient()
	h.Add(c)
	time.Sleep(20 * time.Millisecond)

	// Cols only; should NOT trigger a setSize since rows == 0.
	h.Resize(c, 80, 0)
	time.Sleep(50 * time.Millisecond)
	if _, ok := pty.lastResize(); ok {
		t.Errorf("expected no setSize call for partial dimensions")
	}
}

func TestHub_DropsSlowClient(t *testing.T) {
	pty := &fakePty{}
	h := NewHub(pty, pty.setSize, 1024, 0, 0)
	go h.Run()
	defer h.Stop()

	// Build a tiny-queue client manually so we can fill it.
	slow := &Client{
		out:    make(chan runproto.Message, 1),
		closed: make(chan struct{}),
	}
	h.Add(slow)
	// Don't drain — the hello frame stays queued. Now any further
	// broadcast hits a full channel.
	time.Sleep(20 * time.Millisecond)

	// First broadcast fills capacity already (hello sat there). Send
	// more — the hub should give up on this client.
	for i := 0; i < 10; i++ {
		h.FromPty([]byte{byte('a' + i)})
	}

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		select {
		case <-slow.Closed():
			return
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	t.Errorf("slow client was not dropped")
}

func TestHub_RunnerExitBroadcasts(t *testing.T) {
	pty := &fakePty{}
	h := NewHub(pty, pty.setSize, 1024, 0, 0)
	go h.Run()

	c := NewClient()
	h.Add(c)
	time.Sleep(20 * time.Millisecond)
	drain(t, c, 1) // hello

	h.RunnerExited(7)

	got := drain(t, c, 1)
	if len(got) == 0 {
		t.Fatal("no exit frame")
	}
	last := got[len(got)-1]
	if last.Type != runproto.TypeExit {
		t.Fatalf("last frame type = %q, want %q", last.Type, runproto.TypeExit)
	}
	if last.Code == nil || *last.Code != 7 {
		t.Errorf("Code = %v, want 7", last.Code)
	}

	// After exit, the client's Out channel must close.
	select {
	case _, ok := <-c.Out():
		// Drain the exit frame if not yet drained, then expect close.
		if ok {
			select {
			case _, ok2 := <-c.Out():
				if ok2 {
					t.Errorf("Out channel still open after exit")
				}
			case <-time.After(200 * time.Millisecond):
				t.Errorf("Out channel did not close after exit")
			}
		}
	case <-time.After(200 * time.Millisecond):
		t.Errorf("Out channel did not close after exit")
	}
}
