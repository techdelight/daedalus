// Copyright (C) 2026 Techdelight BV

// Package runclient is the host-side client for a daedalus-runner Unix
// socket. It hides the runproto wire protocol behind an io.Reader /
// io.Writer surface so the CLI, TUI, and Web bridge can attach to a
// runner with the same primitives they used for tmux.
//
// Usage sketch:
//
//	conn, err := runclient.Dial("/var/lib/daedalus/sockets/foo.sock")
//	if err != nil { ... }
//	defer conn.Close()
//
//	go io.Copy(stdout, conn)            // PTY output → terminal
//	go io.Copy(conn, stdin)             // keystrokes → PTY
//	conn.Resize(cols, rows)             // forward SIGWINCH
//	code := conn.Wait()                 // blocks until runner exits
package runclient

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/techdelight/daedalus/internal/runproto"
)

// Hello is the initial state the server reports on connect. Captured
// synchronously by Dial so callers can repaint scrollback and size the
// terminal before any subsequent output arrives.
type Hello struct {
	Scrollback []byte
	Cols       int
	Rows       int
}

// Conn is one attached client of a daedalus-runner socket. It is safe
// for separate goroutines to call Read / Write / Resize concurrently;
// the underlying runproto.Encoder serialises outbound frames and Read
// is backed by an io.Pipe.
type Conn struct {
	sock net.Conn
	enc  *runproto.Encoder
	dec  *runproto.Decoder

	hello Hello

	// pipe holds output bytes inbound from the server until a Read
	// caller drains them. Closing pipeW from the read goroutine on
	// EOF makes pipeR.Read return io.EOF, signalling the consumer
	// that no more output will come.
	pipeR *io.PipeReader
	pipeW *io.PipeWriter

	exitMu   sync.Mutex
	exitCode int
	exitSet  bool
	exitDone chan struct{}

	closeOnce sync.Once
	closeErr  error
}

// Dial opens a connection to the daedalus-runner socket at path. It
// blocks until the server's hello frame is received so callers can
// rely on Hello() returning a populated value immediately.
func Dial(path string) (*Conn, error) {
	sock, err := net.Dial("unix", path)
	if err != nil {
		return nil, err
	}

	c := &Conn{
		sock:     sock,
		enc:      runproto.NewEncoder(sock),
		dec:      runproto.NewDecoder(sock),
		exitDone: make(chan struct{}),
		exitCode: -1,
	}
	c.pipeR, c.pipeW = io.Pipe()

	m, err := c.dec.Decode()
	if err != nil {
		sock.Close()
		return nil, fmt.Errorf("reading hello: %w", err)
	}
	if m.Type != runproto.TypeHello {
		sock.Close()
		return nil, fmt.Errorf("expected hello as first frame, got %q", m.Type)
	}
	c.hello = Hello{Scrollback: m.Scrollback, Cols: m.Cols, Rows: m.Rows}

	go c.readLoop(m.Scrollback)
	return c, nil
}

// Hello returns the initial state captured during Dial.
func (c *Conn) Hello() Hello { return c.hello }

// Read returns PTY output bytes. The first bytes returned are the
// scrollback the server replayed in its hello frame; subsequent reads
// return live output. Read returns io.EOF after the runner exits or
// the connection is closed.
func (c *Conn) Read(p []byte) (int, error) {
	return c.pipeR.Read(p)
}

// Write delivers bytes as input to the runner's PTY. With multi-writer
// fan-in on the server, bytes from all attached clients interleave in
// arrival order.
func (c *Conn) Write(p []byte) (int, error) {
	if err := c.enc.Encode(runproto.NewInput(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Resize informs the server of this client's terminal dimensions.
// The server picks the smallest cols/rows across all attached
// clients when sizing the PTY.
func (c *Conn) Resize(cols, rows int) error {
	return c.enc.Encode(runproto.NewResize(cols, rows))
}

// Detach sends a graceful detach frame and closes the connection.
// The runner subprocess is unaffected; other attached clients
// continue normally.
func (c *Conn) Detach() error {
	// Best-effort: if the encode fails the socket is already gone.
	_ = c.enc.Encode(runproto.NewDetach())
	return c.Close()
}

// Close tears down the connection without sending detach. Idempotent.
func (c *Conn) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.sock.Close()
		// Closing pipeW unblocks any in-flight Read with io.EOF.
		c.pipeW.Close()
	})
	return c.closeErr
}

// Wait blocks until the runner has exited or the connection is torn
// down. Returns the runner's exit code; -1 if the connection ended
// before an exit frame arrived.
func (c *Conn) Wait() int {
	<-c.exitDone
	c.exitMu.Lock()
	defer c.exitMu.Unlock()
	return c.exitCode
}

// readLoop owns the pipe writer for the lifetime of the connection.
// It first replays the hello scrollback (so it appears as ordinary
// PTY output to Read callers), then dispatches each subsequent frame
// to the appropriate sink. When the connection ends the loop closes
// the pipe writer, which makes the next Read return io.EOF.
func (c *Conn) readLoop(initialScrollback []byte) {
	defer c.pipeW.Close()
	defer c.markExitIfUnset(-1)

	if len(initialScrollback) > 0 {
		if _, err := c.pipeW.Write(initialScrollback); err != nil {
			return
		}
	}

	for {
		m, err := c.dec.Decode()
		if err != nil {
			if !isExpectedDisconnect(err) {
				// Quiet unless it's actually unexpected; the CLI will
				// see io.EOF on Read and the Wait sentinel exit code.
			}
			return
		}
		switch m.Type {
		case runproto.TypeOutput:
			if _, err := c.pipeW.Write(m.Data); err != nil {
				return
			}
		case runproto.TypeExit:
			code := -1
			if m.Code != nil {
				code = *m.Code
			}
			c.markExitIfUnset(code)
			return
		case runproto.TypeRunnerEvent:
			// No consumer for runner events yet; drop. A future phase
			// will expose these via an Events channel once an adapter
			// produces them.
		case runproto.TypeHello:
			// Server should send hello exactly once, before readLoop
			// starts. A second hello mid-stream is a protocol error;
			// ignore rather than crash the client.
		default:
			// Server should never send client-bound types like input
			// or detach. Ignore.
		}
	}
}

func (c *Conn) markExitIfUnset(code int) {
	c.exitMu.Lock()
	defer c.exitMu.Unlock()
	if c.exitSet {
		return
	}
	c.exitCode = code
	c.exitSet = true
	close(c.exitDone)
}

func isExpectedDisconnect(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed)
}
