// Copyright (C) 2026 Techdelight BV

package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/techdelight/daedalus/internal/runclient"

	"golang.org/x/term"
)

// ctrlD is the byte the CLI intercepts to mean "detach from this
// session" (the runner is unaffected; the container keeps going).
// Once intercepted, Ctrl-D never reaches the runner — see CHANGELOG.
const ctrlD = 0x04

// attachToRunner connects to a daedalus-runner Unix socket, puts the
// local terminal in raw mode, and bridges stdin/stdout/SIGWINCH until
// the runner exits or the user presses Ctrl-D.
//
// Returns the runner's exit code (0 on a clean detach with no exit
// frame; -1 if the connection ended unexpectedly) and any error.
func attachToRunner(sockPath string) (int, error) {
	if err := waitForSocket(sockPath, 30*time.Second); err != nil {
		return -1, err
	}

	conn, err := runclient.Dial(sockPath)
	if err != nil {
		return -1, fmt.Errorf("dial %s: %w", sockPath, err)
	}
	defer conn.Close()

	fdIn := int(os.Stdin.Fd())
	if term.IsTerminal(fdIn) {
		oldState, err := term.MakeRaw(fdIn)
		if err != nil {
			return -1, fmt.Errorf("make raw: %w", err)
		}
		defer func() { _ = term.Restore(fdIn, oldState) }()

		// Initial size + SIGWINCH forwarding. Done only when stdin is
		// a real terminal; piped input (e.g. headless -p) has no size.
		if cols, rows, err := term.GetSize(fdIn); err == nil {
			_ = conn.Resize(cols, rows)
		}
		sigWinch := make(chan os.Signal, 1)
		signal.Notify(sigWinch, syscall.SIGWINCH)
		defer signal.Stop(sigWinch)
		go func() {
			for range sigWinch {
				if cols, rows, err := term.GetSize(fdIn); err == nil {
					_ = conn.Resize(cols, rows)
				}
			}
		}()
	}

	// Output: runner → local stdout. Runs until conn returns io.EOF
	// (runner exit or socket close).
	go func() { _, _ = io.Copy(os.Stdout, conn) }()

	// Input: local stdin → runner, with Ctrl-D as the local detach
	// trigger. Anything before the Ctrl-D byte in a single read is
	// still forwarded.
	detached := make(chan struct{})
	go func() {
		defer close(detached)
		buf := make([]byte, 1024)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				if i := bytes.IndexByte(buf[:n], ctrlD); i >= 0 {
					if i > 0 {
						_, _ = conn.Write(buf[:i])
					}
					_ = conn.Detach()
					return
				}
				if _, err := conn.Write(buf[:n]); err != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	code := conn.Wait()
	if code == -1 {
		// No exit frame. Could be a clean detach (we triggered it) or
		// a connection drop. Either way the runner state is opaque to
		// us; report success on detach, generic failure otherwise.
		select {
		case <-detached:
			return 0, nil
		default:
			return -1, fmt.Errorf("connection ended without exit frame")
		}
	}
	return code, nil
}

// waitForSocket polls for path to appear on disk. The runner-detached
// docker compose run returns as soon as the container ID is known; the
// daedalus-runner inside still has to bind its socket.
func waitForSocket(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("daedalus-runner socket %s did not appear within %s", path, timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
