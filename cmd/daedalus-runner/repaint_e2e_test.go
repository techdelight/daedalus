// Copyright (C) 2026 Techdelight BV

//go:build e2e

// End-to-end verification for Sprint 41 item 4 (Backlog #38): the
// repaint-on-attach behaviour of the runner path. Unlike the hub unit
// tests (which stub the PTY), this drives the *real* pieces end to end —
// a real PTY running a subprocess, the real Hub with Layer 2a startup
// sizing, the real serveConn over a real Unix-domain socket, and the
// real runclient host-side client — so it reproduces #38's actual
// mechanism instead of a model of it.
//
// The subprocess is a self-exec "fake agent" (see TestMain /
// fakeAgentMain) that mimics Claude's one-shot full-screen dialog: it
// draws a recognizable banner once at startup and *repaints only on
// SIGWINCH*, then idles. That is precisely the shape of UI that #38
// breaks on — drawn once, never volunteered again — so whether a newly
// attached client sees it live tells us whether repaint-on-attach works.
//
// It needs a real PTY + signals, so it is gated behind the `e2e` build
// tag and excluded from the default `go test ./...`. Run it with
// `./e2e/run-repaint.sh` or `go test -tags e2e ./cmd/daedalus-runner`.
package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/techdelight/daedalus/internal/runclient"
)

const (
	// fakeAgentEnv, when set on the re-exec'd test binary, makes it run
	// as the fake agent instead of the test suite.
	fakeAgentEnv = "DAEDALUS_FAKE_AGENT"
	// dialogMarker is the sentinel the fake agent draws; each draw also
	// carries the size it rendered at, so tests can tell a stale scrollback
	// draw (80x24) apart from a fresh live repaint (e.g. 120x30).
	dialogMarker = "TRUST-PROMPT"
)

// TestMain re-purposes the compiled test binary as the fake agent when
// fakeAgentEnv is set (the standard Go "helper process" pattern), so the
// e2e test needs no separate fixture binary to build or ship.
func TestMain(m *testing.M) {
	if os.Getenv(fakeAgentEnv) == "1" {
		fakeAgentMain()
		return
	}
	os.Exit(m.Run())
}

// fakeAgentMain stands in for `claude`. Its stdin/stdout are the PTY
// slave. It draws the dialog once, then repaints only when it receives
// SIGWINCH — the same "render once and idle" behaviour that makes #38's
// trust prompt vanish for a client that attaches without provoking a
// resize.
func fakeAgentMain() {
	draw := func() {
		// pty.Getsize reports the current window size of our controlling
		// terminal (the PTY slave on fd 0). Mirrors how a real TUI reads
		// its size to lay out a full-screen dialog.
		rows, cols, err := pty.Getsize(os.Stdin)
		if err != nil {
			rows, cols = 0, 0
		}
		// Enter the alternate screen, clear, home the cursor, then the
		// banner — a realistic one-shot full-screen dialog. The banner
		// embeds the size so a fresh repaint is distinguishable from the
		// startup draw replayed out of scrollback.
		fmt.Fprintf(os.Stdout, "\x1b[?1049h\x1b[2J\x1b[H%s cols=%d rows=%d\r\n", dialogMarker, cols, rows)
	}

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM, syscall.SIGINT)

	draw() // initial render at the PTY's startup size
	for {
		select {
		case <-winch:
			draw()
		case <-term:
			fmt.Fprint(os.Stdout, "\x1b[?1049l")
			os.Exit(0)
		}
	}
}

// banner returns the exact string the fake agent emits for a given size.
func banner(cols, rows int) string {
	return fmt.Sprintf("%s cols=%d rows=%d", dialogMarker, cols, rows)
}

// harness wires the real runner internals around a fake-agent PTY and
// exposes a socket path for clients to attach to.
type harness struct {
	sockPath string
	sizes    *sizeRecorder
}

// startHarness launches the fake agent on a real PTY sized to
// (initCols, initRows), builds the real Hub configured with the same
// initial size, and serves the real runner socket.
//
// The PTY is started already at the initial size so the *startup* render
// is deterministic; Layer 2a's 0x0->NxN startup sizing is already covered
// by hub_test.go's applyInitialSize tests. What this e2e exercises is the
// *attach* path: whether a client connecting after the one-shot draw sees
// the dialog. The hub is still constructed with initCols/initRows so its
// notion of the current size matches the PTY and the delta logic under
// test is the production one.
func startHarness(t *testing.T, initCols, initRows int) *harness {
	t.Helper()

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("locating test binary: %v", err)
	}
	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(), fakeAgentEnv+"=1")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(initCols), Rows: uint16(initRows)})
	if err != nil {
		t.Fatalf("starting fake-agent pty: %v", err)
	}

	sizes := &sizeRecorder{}
	// The real resizer, wrapped to record every size the hub applies so
	// tests can assert whether an attach produced a genuine SIGWINCH.
	resizer := func(cols, rows int) error {
		sizes.record(cols, rows)
		return pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	}

	hub := NewHub(ptmx, resizer, defaultScrollback, initCols, initRows)
	go hub.Run()

	// PTY -> hub output pump (a copy of main.go's pump).
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := ptmx.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				hub.FromPty(chunk)
			}
			if rerr != nil {
				return
			}
		}
	}()

	sockPath := filepath.Join(t.TempDir(), "runner.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listening on socket: %v", err)
	}
	go func() {
		for {
			conn, aerr := l.Accept()
			if aerr != nil {
				return
			}
			go serveConn(conn, hub)
		}
	}()

	t.Cleanup(func() {
		l.Close()
		hub.Stop()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = ptmx.Close()
		_, _ = cmd.Process.Wait()
	})

	// Give the fake agent a beat to perform its initial draw so the
	// startup dialog is in scrollback before any client attaches.
	waitForScrollback(t, sockPath, banner(initCols, initRows))

	return &harness{sockPath: sockPath, sizes: sizes}
}

// attach dials the runner and, like the real CLI (attach.go), immediately
// declares its terminal size. Returns the connection and the size-delta
// count observed on the hub *before* this attach, so callers can tell
// whether the attach itself provoked a new setSize.
func (h *harness) attach(t *testing.T, cols, rows int) (*runclient.Conn, int) {
	t.Helper()
	before := h.sizes.count()
	c, err := runclient.Dial(h.sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := c.Resize(cols, rows); err != nil {
		t.Fatalf("resize: %v", err)
	}
	return c, before
}

// live returns the bytes this connection delivered *after* the replayed
// scrollback — i.e. genuine post-attach output. runclient replays the
// hello scrollback as the first bytes of the stream, so everything past
// len(Hello().Scrollback) is live. Collected over a fixed window because
// the whole point of the test is that some attaches produce no live
// output at all.
func live(t *testing.T, c *runclient.Conn, d time.Duration) string {
	t.Helper()
	scrollbackLen := len(c.Hello().Scrollback)
	all := collectFor(c, d)
	if len(all) < scrollbackLen {
		return "" // not even the scrollback fully drained; no live output
	}
	return string(all[scrollbackLen:])
}

// collectFor drains a connection for d, returning everything read.
func collectFor(c *runclient.Conn, d time.Duration) []byte {
	var mu sync.Mutex
	var buf bytes.Buffer
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := c.Read(b)
			if n > 0 {
				mu.Lock()
				buf.Write(b[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	time.Sleep(d)
	mu.Lock()
	defer mu.Unlock()
	return append([]byte(nil), buf.Bytes()...)
}

// waitForScrollback polls attaches until the runner's replayed scrollback
// contains want, so tests don't race the fake agent's initial draw.
func waitForScrollback(t *testing.T, sockPath, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := runclient.Dial(sockPath)
		if err == nil {
			sb := string(c.Hello().Scrollback)
			c.Close()
			if strings.Contains(sb, want) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for startup draw %q in scrollback", want)
}

// sizeRecorder records the sizes the hub forwards to the PTY.
type sizeRecorder struct {
	mu    sync.Mutex
	calls []resizeCall // resizeCall is defined in hub_test.go
}

func (s *sizeRecorder) record(cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, resizeCall{cols, rows})
}

func (s *sizeRecorder) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// deltaSince reports whether any new size was applied after index `since`.
func (s *sizeRecorder) deltaSince(since int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls) > since
}

const settle = 400 * time.Millisecond

// startsAtScreenBoundary reports whether b begins with a screen-establishing
// sequence — i.e. whether ScreenSnapshot trimmed it to a boundary so a fresh
// terminal renders it cleanly instead of mid-scrollback.
func startsAtScreenBoundary(b []byte) bool {
	for _, m := range append(append([][]byte{}, altScreenEnter...), screenClears...) {
		if bytes.HasPrefix(b, m) {
			return true
		}
	}
	return false
}

// assertReconstructs is the property #38 needs: whatever else happened, an
// attaching client's snapshot both begins at a screen boundary and contains
// the current dialog, so rendering it reproduces the live screen.
func assertReconstructs(t *testing.T, c *runclient.Conn, cols, rows int) {
	t.Helper()
	sb := c.Hello().Scrollback
	if !startsAtScreenBoundary(sb) {
		t.Fatalf("snapshot should begin at a screen boundary; got prefix %q", headStr(sb, 12))
	}
	if want := banner(cols, rows); !bytes.Contains(sb, []byte(want)) {
		t.Fatalf("snapshot should reconstruct the dialog %q; got %q", want, sb)
	}
}

func headStr(b []byte, n int) string {
	if len(b) < n {
		n = len(b)
	}
	return string(b[:n])
}

// TestRepaint_ResizedAttach_LiveRepaint is the SIGWINCH path: a client whose
// terminal differs from the startup size (e.g. a maximized 120x30 window)
// provokes a size delta on attach, the kernel raises SIGWINCH, and the agent
// repaints live at the new size. Confirms Layers 1+2a end to end; the snapshot
// (captured at Dial, before the resize) still reconstructs the startup screen.
func TestRepaint_ResizedAttach_LiveRepaint(t *testing.T) {
	h := startHarness(t, defaultCols, defaultRows) // 80x24 startup
	c, before := h.attach(t, 120, 30)
	defer c.Close()

	assertReconstructs(t, c, defaultCols, defaultRows) // snapshot = startup screen
	out := live(t, c, settle)

	if !h.sizes.deltaSince(before) {
		t.Fatalf("expected a size delta on resized attach, got none")
	}
	if want := banner(120, 30); !strings.Contains(out, want) {
		t.Fatalf("expected live repaint %q after resized attach; live output = %q", want, out)
	}
}

// TestRepaint_SameSizeAttach_ReconstructsViaSnapshot is the case that used to
// be #38: a client attaching at exactly the runner's current size produces no
// delta and no SIGWINCH, so there is no *live* repaint. It sees the dialog
// anyway because ScreenSnapshot replays from the last screen boundary — the
// fix, proven end to end over a real socket.
func TestRepaint_SameSizeAttach_ReconstructsViaSnapshot(t *testing.T) {
	h := startHarness(t, defaultCols, defaultRows)
	c, before := h.attach(t, defaultCols, defaultRows) // identical size
	defer c.Close()

	assertReconstructs(t, c, defaultCols, defaultRows)

	// The reconstruction is via the snapshot, not a live repaint: confirm we
	// did NOT resort to poking the shared PTY.
	if h.sizes.deltaSince(before) {
		t.Fatalf("same-size attach should not resize the shared PTY, but the hub did")
	}
	if out := live(t, c, settle); strings.Contains(out, dialogMarker) {
		t.Fatalf("expected reconstruction via snapshot, not a live repaint; live output = %q", out)
	}
}

// TestRepaint_SecondClientSameSize_ReconstructsViaSnapshot is the multi-viewer
// case: with a first client holding the PTY at 120x30, a second client
// attaching at the same 120x30 gets its own snapshot reconstruction — without
// any resize, so the first client is never disturbed. This is exactly what a
// per-attach snapshot buys that a shared-PTY nudge (Options A/B) cannot.
func TestRepaint_SecondClientSameSize_ReconstructsViaSnapshot(t *testing.T) {
	h := startHarness(t, defaultCols, defaultRows)

	first, _ := h.attach(t, 120, 30) // drives the PTY to 120x30 and repaints
	defer first.Close()
	_ = live(t, first, settle) // let the repaint settle into scrollback

	second, before := h.attach(t, 120, 30) // same negotiated size
	defer second.Close()

	assertReconstructs(t, second, 120, 30)

	if h.sizes.deltaSince(before) {
		t.Fatalf("a same-size second client should not resize the shared PTY, but the hub did")
	}
	if out := live(t, second, settle); strings.Contains(out, dialogMarker) {
		t.Fatalf("second client should reconstruct via snapshot, not a live repaint; got: %q", out)
	}
}

// TestRepaint_StockTerminalSeesPrompt is the user-visible acceptance criterion:
// a user on a stock 80x24 terminal — the size that collides with the 80x24
// startup and used to hang the Web UI (#38) — sees the live trust prompt on
// attach. With smart replay this holds unconditionally, via the snapshot, with
// no dependence on the startup size dodging any particular client dimension.
func TestRepaint_StockTerminalSeesPrompt(t *testing.T) {
	const stockCols, stockRows = 80, 24
	h := startHarness(t, defaultCols, defaultRows)

	c, _ := h.attach(t, stockCols, stockRows)
	defer c.Close()

	assertReconstructs(t, c, defaultCols, defaultRows)
}
