// Copyright (C) 2026 Techdelight BV

package coordinator

// Bootstrap: ssh-agent-style auto-spawn of the coordinator daemon.
//
// EnsureRunning is the entry point every UI process uses when it
// needs a Client. If the daemon is already listening on the socket
// (per pidfile + live-dial check), a Client is returned directly.
// Otherwise the daemon binary is spawned detached (Setsid), its
// stdout/stderr redirected to LogPath, and EnsureRunning blocks until
// the socket appears or ReadyWait elapses.
//
// The pidfile + socket layout mirrors what cmd/daedalus-coordinator
// writes when passed `--pid-file` and `--socket`. Callers assemble
// the paths themselves (via DefaultSocketPath and their own DataDir
// conventions) so we don't hard-code layout twice.

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DefaultLayout returns the standard on-disk layout for the daemon
// beneath the given DataDir + ScriptDir. All callers that hand off
// to EnsureRunning should assemble their BootstrapOptions through
// this helper so the CLI, Web, and TUI agree on where the socket,
// pidfile, log, and binary live.
func DefaultLayout(dataDir, scriptDir string) BootstrapOptions {
	return BootstrapOptions{
		SocketPath: DefaultSocketPath(dataDir),
		PIDPath:    filepath.Join(dataDir, ".daedalus", "coordinator.pid"),
		LogPath:    filepath.Join(dataDir, ".daedalus", "coordinator.log"),
		DaemonBin:  filepath.Join(scriptDir, "daedalus-coordinator"),
		DataDir:    dataDir,
	}
}

// DefaultSessionsFile returns the standard sessions.json path for the
// daemon under DataDir. Kept separate from BootstrapOptions because
// this is a daemon-side concern (only cmd/daedalus-coordinator uses
// it); the bootstrap layout is client-side.
func DefaultSessionsFile(dataDir string) string {
	return filepath.Join(dataDir, ".daedalus", "sessions.json")
}

// BootstrapOptions tells EnsureRunning how to find the daemon and how
// to auto-spawn it if missing.
type BootstrapOptions struct {
	// SocketPath is where the daemon listens; also where NewClient
	// will dial when EnsureRunning returns a fresh Client.
	SocketPath string

	// PIDPath is the pidfile the daemon writes on startup and unlinks
	// on graceful shutdown. Used as a fast liveness probe before we
	// try to dial the socket.
	PIDPath string

	// LogPath receives the daemon's stdout+stderr. The parent dir is
	// created if missing. If empty, output is discarded.
	LogPath string

	// DaemonBin is the full path to the daedalus-coordinator binary.
	// If a spawn is needed and this is empty (or the file is missing)
	// EnsureRunning returns an actionable error.
	DaemonBin string

	// DataDir is passed through as --data-dir to the spawned daemon
	// so its own default resolution matches ours.
	DataDir string

	// ReadyWait bounds how long EnsureRunning blocks waiting for the
	// spawned daemon's socket to appear. Zero uses 5s.
	ReadyWait time.Duration
}

// EnsureRunning returns a Client, spawning the daemon if it isn't
// already running. Safe to call from multiple UI processes: a stale
// pidfile from a crashed prior daemon is treated as "not running"
// and a fresh spawn takes over.
func EnsureRunning(opts BootstrapOptions) (*Client, error) {
	if isDaemonRunning(opts.SocketPath, opts.PIDPath) {
		return NewClient(opts.SocketPath), nil
	}

	if opts.DaemonBin == "" {
		return nil, fmt.Errorf("coordinator: daemon binary path is required")
	}
	if _, err := os.Stat(opts.DaemonBin); err != nil {
		return nil, fmt.Errorf("coordinator: daedalus-coordinator not found at %s: %w", opts.DaemonBin, err)
	}

	if err := spawnDaemon(opts); err != nil {
		return nil, err
	}

	wait := opts.ReadyWait
	if wait == 0 {
		wait = 5 * time.Second
	}
	if err := waitForSocket(opts.SocketPath, wait, 50*time.Millisecond); err != nil {
		return nil, fmt.Errorf("coordinator: daemon did not become ready: %w", err)
	}
	return NewClient(opts.SocketPath), nil
}

// isDaemonRunning returns true only when both the pidfile names a
// live process AND the socket accepts a connection. Either alone can
// lie: pidfile can be stale, socket file can outlive a crashed process.
func isDaemonRunning(sockPath, pidPath string) bool {
	if !pidfileAlive(pidPath) {
		return false
	}
	conn, err := net.DialTimeout("unix", sockPath, 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// pidfileAlive parses pidPath and probes the process with signal(0).
// Duplicates readPIDIfAlive from cmd/daedalus/coordinator.go on
// purpose — the CLI subcommand and internal/coordinator can't share
// helpers without a package split, and the routine is 12 lines.
func pidfileAlive(pidPath string) bool {
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// spawnDaemon forks the daemon binary in a new session (Setsid) so
// the caller's process group can exit independently. stdout+stderr
// go to LogPath; the daemon is Release()'d so we don't zombie it.
func spawnDaemon(opts BootstrapOptions) error {
	var logFile *os.File
	if opts.LogPath != "" {
		if err := os.MkdirAll(filepath.Dir(opts.LogPath), 0o755); err != nil {
			return fmt.Errorf("coordinator: create log dir: %w", err)
		}
		f, err := os.OpenFile(opts.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return fmt.Errorf("coordinator: open daemon log: %w", err)
		}
		logFile = f
	}

	args := []string{
		"--socket", opts.SocketPath,
		"--pid-file", opts.PIDPath,
	}
	if opts.DataDir != "" {
		args = append(args, "--data-dir", opts.DataDir)
	}
	cmd := exec.Command(opts.DaemonBin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	if err := cmd.Start(); err != nil {
		if logFile != nil {
			_ = logFile.Close()
		}
		return fmt.Errorf("coordinator: spawn daemon: %w", err)
	}
	// Close our copy of the log fd; the child inherits its own copy.
	if logFile != nil {
		_ = logFile.Close()
	}
	// Detach so we don't have to Wait for it.
	if err := cmd.Process.Release(); err != nil {
		// Non-fatal: the daemon is running, we just leaked the handle.
		// EnsureRunning still returns a valid Client.
		return nil
	}
	return nil
}
