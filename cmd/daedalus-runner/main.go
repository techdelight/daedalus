// Copyright (C) 2026 Techdelight BV

// Command daedalus-runner is the in-container PID 1 process that
// wraps an AI CLI (claude, copilot, ...) and exposes its terminal
// I/O over a Unix-domain socket using the runproto wire protocol.
//
// Usage:
//
//	daedalus-runner --adapter <name> --socket <path>
//	                [--workdir <dir>] [--scrollback <bytes>]
//	                [--debug] [--resume <id>] [--prompt <text>]
//
// The binary is intended to live inside the project's container,
// launched by the container's ENTRYPOINT. Multiple host-side UI
// clients (CLI, TUI, Web bridge) connect to the socket to share
// the runner's PTY: output is fanned out, input is interleaved
// in arrival order, window-size is the smallest of all attached
// dimensions.
package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/creack/pty"
	"github.com/techdelight/daedalus/internal/runner"
)

const (
	defaultScrollback = 1 << 20 // 1 MiB
	// Default PTY dimensions applied at startup, before any UI client
	// negotiates a size. Without a non-zero initial size the agent would
	// render into the 0x0 terminal creack/pty leaves until the first
	// client attaches — which garbles one-shot startup prompts.
	defaultCols = 80
	defaultRows = 24
)

func main() {
	cfg := parseFlags()

	adapter, err := runner.Lookup(cfg.adapter)
	if err != nil {
		fatalf("resolving runner: %v", err)
	}

	bin, args, env := adapter.Command(runner.LaunchOptions{
		Debug:  cfg.debug,
		Resume: cfg.resume,
		Prompt: cfg.prompt,
	})
	if _, err := os.Stat(bin); err != nil {
		fatalf("runner binary %q: %v", bin, err)
	}

	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Dir = cfg.workdir

	ptmx, err := pty.Start(cmd)
	if err != nil {
		fatalf("starting pty: %v", err)
	}

	hub := NewHub(ptmx, ptyResizer(ptmx), cfg.scrollback, cfg.cols, cfg.rows)
	go hub.Run()

	// PTY → hub: stream the runner's output bytes into the broadcast
	// pipe. EOF on the PTY means the runner exited.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				hub.FromPty(chunk)
			}
			if err != nil {
				return
			}
		}
	}()

	// Forward host-side TERM/INT to the runner so the container's
	// `docker stop` translates into a graceful runner shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		for s := range sigCh {
			if cmd.Process != nil {
				_ = cmd.Process.Signal(s)
			}
		}
	}()

	listener, err := listenSocket(cfg.socket)
	if err != nil {
		fatalf("listening on %s: %v", cfg.socket, err)
	}
	defer func() {
		listener.Close()
		os.Remove(cfg.socket)
	}()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serveConn(conn, hub)
		}
	}()

	// Wait for the runner to exit, propagate its status to the hub
	// (which broadcasts the exit frame) and surface it as our own
	// process exit code.
	waitErr := cmd.Wait()
	code := exitCode(waitErr)
	hub.RunnerExited(code)

	// Give the hub a moment to flush exit frames before the listener
	// is yanked out from under it.
	listener.Close()
	hub.Stop()

	os.Exit(code)
}

// ptyResizer returns a setsize closure for the hub. Wrapping pty.Setsize
// here hides the *pty.Winsize struct from the hub, keeping the hub's
// tests free of the creack/pty dependency.
func ptyResizer(ptmx *os.File) func(cols, rows int) error {
	return func(cols, rows int) error {
		return pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	}
}

// listenSocket creates a Unix domain socket at path, removing any
// stale file at that location first. The socket file is unlinked on
// shutdown by main.
func listenSocket(path string) (net.Listener, error) {
	_ = os.Remove(path)
	return net.Listen("unix", path)
}

// exitCode extracts the runner's exit status from cmd.Wait's error.
// nil → 0; *exec.ExitError → its ExitCode; anything else → 1.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 1
}

func fatalf(format string, args ...any) {
	fmt.Fprintln(os.Stderr, "daedalus-runner: "+fmt.Sprintf(format, args...))
	os.Exit(2)
}

type runnerConfig struct {
	adapter    string
	socket     string
	workdir    string
	scrollback int
	cols       int
	rows       int
	debug      bool
	resume     string
	prompt     string
}

func parseFlags() runnerConfig {
	cfg := runnerConfig{
		workdir:    "/workspace",
		scrollback: defaultScrollback,
		cols:       defaultCols,
		rows:       defaultRows,
	}
	flag.StringVar(&cfg.adapter, "adapter", "", "runner adapter to launch (e.g. claude, copilot)")
	flag.StringVar(&cfg.socket, "socket", "", "unix socket path to listen on")
	flag.StringVar(&cfg.workdir, "workdir", cfg.workdir, "working directory for the runner subprocess")
	flag.IntVar(&cfg.scrollback, "scrollback", cfg.scrollback, "scrollback buffer size in bytes")
	flag.IntVar(&cfg.cols, "cols", cfg.cols, "initial PTY width before a client negotiates size")
	flag.IntVar(&cfg.rows, "rows", cfg.rows, "initial PTY height before a client negotiates size")
	flag.BoolVar(&cfg.debug, "debug", false, "enable runner debug mode (if supported)")
	flag.StringVar(&cfg.resume, "resume", "", "session id to resume")
	flag.StringVar(&cfg.prompt, "prompt", "", "headless one-shot prompt; empty = interactive")
	flag.Parse()

	if cfg.adapter == "" {
		fatalf("--adapter is required (one of: %v)", runner.Names())
	}
	if cfg.socket == "" {
		fatalf("--socket is required")
	}
	return cfg
}
