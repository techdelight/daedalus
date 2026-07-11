// Copyright (C) 2026 Techdelight BV

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/coordinator"
)

// manageCoordinator dispatches `daedalus coordinator <subcommand>`.
//
// Subcommands:
//
//	start   — spawn daedalus-coordinator detached in the background,
//	          write a pidfile so we can stop/status it later.
//	stop    — send SIGTERM to the running daemon and wait for it.
//	status  — print whether the daemon is running and list sessions.
//
// The daemon binary itself lives at <ScriptDir>/daedalus-coordinator,
// installed alongside the main `daedalus` binary by setup.sh.
func manageCoordinator(cfg *core.Config) error {
	if len(cfg.CoordinatorArgs) == 0 {
		return fmt.Errorf("coordinator: subcommand required (start|stop|status)")
	}
	sub := cfg.CoordinatorArgs[0]
	switch sub {
	case "start":
		return coordinatorStart(cfg)
	case "stop":
		return coordinatorStop(cfg)
	case "status":
		return coordinatorStatus(cfg)
	default:
		return fmt.Errorf("coordinator: unknown subcommand %q (want start|stop|status)", sub)
	}
}

// coordinatorPaths returns the well-known filesystem locations the
// three subcommands share. Kept in one place so a future change to
// the layout only edits here.
func coordinatorPaths(cfg *core.Config) (sockPath, pidPath, logPath, daemonBin string) {
	sockPath = coordinator.DefaultSocketPath(cfg.DataDir)
	pidPath = filepath.Join(cfg.DataDir, ".daedalus", "coordinator.pid")
	logPath = filepath.Join(cfg.DataDir, ".daedalus", "coordinator.log")
	daemonBin = filepath.Join(cfg.ScriptDir, "daedalus-coordinator")
	return
}

func coordinatorStart(cfg *core.Config) error {
	sockPath, pidPath, logPath, daemonBin := coordinatorPaths(cfg)

	if pid, alive := readPIDIfAlive(pidPath); alive {
		fmt.Printf("%s coordinator already running (PID %d, socket %s)\n", color.Green("OK:"), pid, sockPath)
		return nil
	}

	if _, err := os.Stat(daemonBin); err != nil {
		return fmt.Errorf("daedalus-coordinator binary not found at %s: %w\nHint: rerun install.sh (v0.39.0+ ships the binary)", daemonBin, err)
	}

	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return fmt.Errorf("create coordinator directory: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open coordinator log: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command(daemonBin,
		"--socket", sockPath,
		"--data-dir", cfg.DataDir,
		"--pid-file", pidPath,
	)
	// Setsid detaches from the CLI's process group so Ctrl-C on the
	// spawning shell doesn't propagate to the daemon.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn daedalus-coordinator: %w", err)
	}
	// Release the child so it isn't zombied when the CLI exits.
	if err := cmd.Process.Release(); err != nil {
		// Non-fatal: the daemon is still running, we just leaked the
		// handle. Note it and continue.
		fmt.Fprintf(os.Stderr, "%s release child: %v\n", color.Yellow("Warning:"), err)
	}

	// Wait for the socket to appear before reporting success. The
	// daemon's own startup path binds it before Serve loops, so this
	// races only if the binary crashes on startup — in which case the
	// deadline is the honest signal to point the user at the log.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(sockPath); err == nil {
			fmt.Printf("%s coordinator started (PID %d, socket %s)\n", color.Green("OK:"), cmd.Process.Pid, sockPath)
			fmt.Printf("       log: %s\n", logPath)
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("daemon did not open socket %s within 5s; check %s", sockPath, logPath)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func coordinatorStop(cfg *core.Config) error {
	_, pidPath, _, _ := coordinatorPaths(cfg)
	pid, alive := readPIDIfAlive(pidPath)
	if !alive {
		fmt.Printf("%s coordinator not running (no live pidfile at %s)\n", color.Yellow("Info:"), pidPath)
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("SIGTERM %d: %w", pid, err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		// signal(0) tests aliveness on Unix without delivering a signal.
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			fmt.Printf("%s coordinator stopped (was PID %d)\n", color.Green("OK:"), pid)
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("coordinator (PID %d) did not exit within 5s", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func coordinatorStatus(cfg *core.Config) error {
	sockPath, pidPath, _, _ := coordinatorPaths(cfg)
	pid, alive := readPIDIfAlive(pidPath)
	if !alive {
		fmt.Printf("%s coordinator not running\n", color.Yellow("Info:"))
		return nil
	}
	fmt.Printf("%s coordinator running (PID %d, socket %s)\n", color.Green("OK:"), pid, sockPath)

	client := coordinator.NewClient(sockPath)
	sessions, err := client.List()
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	if len(sessions) == 0 {
		fmt.Printf("       no active sessions\n")
		return nil
	}
	fmt.Printf("       %d active session(s):\n", len(sessions))
	for _, s := range sessions {
		fmt.Printf("         - %s (container %s, socket %s, started %s)\n",
			s.ProjectName, s.ContainerName, s.SocketPath, s.StartedAt.Format(time.RFC3339))
	}
	return nil
}

// readPIDIfAlive parses pidPath and returns (pid, alive). alive is true
// only when the process both parses and is verified live via signal(0).
// A missing or malformed pidfile counts as not alive; the caller
// decides what to do.
func readPIDIfAlive(pidPath string) (int, bool) {
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return pid, false
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return pid, false
	}
	return pid, true
}
