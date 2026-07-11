// Copyright (C) 2026 Techdelight BV

package main

import (
	"fmt"
	"os"
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

// coordinatorPaths unpacks the shared coordinator.DefaultLayout into
// the four fields the subcommands print in status messages. Layout
// changes belong in coordinator.DefaultLayout — this helper only
// projects them.
func coordinatorPaths(cfg *core.Config) (sockPath, pidPath, logPath, daemonBin string) {
	opts := coordinator.DefaultLayout(cfg.DataDir, cfg.ScriptDir)
	return opts.SocketPath, opts.PIDPath, opts.LogPath, opts.DaemonBin
}

func coordinatorStart(cfg *core.Config) error {
	sockPath, pidPath, logPath, daemonBin := coordinatorPaths(cfg)

	if pid, alive := readPIDIfAlive(pidPath); alive {
		fmt.Printf("%s coordinator already running (PID %d, socket %s)\n", color.Green("OK:"), pid, sockPath)
		return nil
	}

	if _, err := coordinator.EnsureRunning(bootstrapOpts(cfg)); err != nil {
		return err
	}

	// EnsureRunning has already waited for the socket, so the pidfile
	// is fresh by the time we get here.
	pid, _ := readPIDIfAlive(pidPath)
	fmt.Printf("%s coordinator started (PID %d, socket %s)\n", color.Green("OK:"), pid, sockPath)
	fmt.Printf("       log: %s\n", logPath)
	_ = daemonBin // referenced only through bootstrapOpts now
	return nil
}

// ensureCoordinatorClient is the ssh-agent-style entry point for
// callers that need a Client (launch flow, potentially the Web
// runner-mode handler). Spawns the daemon if it isn't already up.
func ensureCoordinatorClient(cfg *core.Config) (*coordinator.Client, error) {
	return coordinator.EnsureRunning(bootstrapOpts(cfg))
}

// bootstrapOpts is a thin passthrough to coordinator.DefaultLayout —
// kept because the CLI has three call sites and this reads better than
// inlining the layout construction each time.
func bootstrapOpts(cfg *core.Config) coordinator.BootstrapOptions {
	return coordinator.DefaultLayout(cfg.DataDir, cfg.ScriptDir)
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
