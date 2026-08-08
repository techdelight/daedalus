// Copyright (C) 2026 Techdelight BV

package control

// ssh-agent-style auto-spawn of the daedalus-control daemon, modelled on
// internal/coordinator/bootstrap.go. EnsureRunning is what the `daedalus task`
// CLI calls to obtain a Client: if the daemon is already listening it returns a
// Client directly; otherwise it spawns the daemon detached and waits for the
// socket. A stale pidfile (crashed prior daemon) counts as "not running".

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

// BootstrapOptions locates the daemon and describes how to spawn it if absent.
type BootstrapOptions struct {
	SocketPath string        // where the daemon listens / the client dials
	PIDPath    string        // pidfile written by the daemon; fast liveness probe
	LogPath    string        // daemon stdout+stderr; discarded if empty
	DaemonBin  string        // full path to the daedalus-control binary
	DataDir    string        // passed through as --data-dir
	ReadyWait  time.Duration // bound on waiting for the spawned socket; 0 → 5s
}

// DefaultLayout returns the standard on-disk layout beneath dataDir/scriptDir,
// so CLI/Web/TUI all agree on where the socket, pidfile, log, and binary live.
func DefaultLayout(dataDir, scriptDir string) BootstrapOptions {
	return BootstrapOptions{
		SocketPath: DefaultSocketPath(dataDir),
		PIDPath:    filepath.Join(dataDir, ".daedalus", "control.pid"),
		LogPath:    filepath.Join(dataDir, ".daedalus", "control.log"),
		DaemonBin:  filepath.Join(scriptDir, "daedalus-control"),
		DataDir:    dataDir,
	}
}

// EnsureRunning returns a Client, spawning the daemon if it is not already up.
func EnsureRunning(opts BootstrapOptions) (*Client, error) {
	if isDaemonRunning(opts.SocketPath, opts.PIDPath) {
		return NewClient(opts.SocketPath), nil
	}
	if opts.DaemonBin == "" {
		return nil, fmt.Errorf("control: daemon binary path is required")
	}
	if _, err := os.Stat(opts.DaemonBin); err != nil {
		return nil, fmt.Errorf("control: daedalus-control not found at %s: %w", opts.DaemonBin, err)
	}
	if err := spawnDaemon(opts); err != nil {
		return nil, err
	}
	wait := opts.ReadyWait
	if wait == 0 {
		wait = 5 * time.Second
	}
	if err := waitForSocketFile(opts.SocketPath, wait, 50*time.Millisecond); err != nil {
		return nil, fmt.Errorf("control: daemon did not become ready: %w", err)
	}
	return NewClient(opts.SocketPath), nil
}

// isDaemonRunning is true only when the pidfile names a live process AND the
// socket accepts a connection — either alone can lie.
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

// spawnDaemon forks the daemon in a new session (Setsid) with stdout+stderr to
// LogPath, then releases it so the caller need not Wait.
func spawnDaemon(opts BootstrapOptions) error {
	var logFile *os.File
	if opts.LogPath != "" {
		if err := os.MkdirAll(filepath.Dir(opts.LogPath), 0o755); err != nil {
			return fmt.Errorf("control: create log dir: %w", err)
		}
		f, err := os.OpenFile(opts.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return fmt.Errorf("control: open daemon log: %w", err)
		}
		logFile = f
	}
	args := []string{"--socket", opts.SocketPath, "--pid-file", opts.PIDPath}
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
		return fmt.Errorf("control: spawn daemon: %w", err)
	}
	if logFile != nil {
		_ = logFile.Close()
	}
	_ = cmd.Process.Release()
	return nil
}

// waitForSocketFile polls for the socket to appear on disk.
func waitForSocketFile(path string, timeout, poll time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("control socket %s did not appear within %s", path, timeout)
		}
		time.Sleep(poll)
	}
}
