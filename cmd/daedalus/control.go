// Copyright (C) 2026 Techdelight BV

package main

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/control"
)

// manageControl dispatches `daedalus control <subcommand>` — the explicit
// lifecycle for the control-plane daemon.
//
// WHY THIS EXISTS, given the daemon already starts itself. Every `daedalus task`
// and `daedalus programmes` command calls control.EnsureRunning, so in normal use
// nobody has to think about the daemon at all, and that is the right default. It
// leaves two things with no command at all, and both are things an operator
// genuinely needs:
//
//   - STOPPING it. The only documented answer was `kill $(cat
//     <data-dir>/.daedalus/control.pid)`, which asks somebody to know a path and
//     to reach for kill(1) against a daemon they never started by hand.
//   - RESTARTING it after an upgrade. A running daemon serves the routes it was
//     BUILT with, so installing a new version and calling a new operation gets a
//     404 from a daemon that is working perfectly — it is simply the old one.
//     That has cost real diagnosis time here more than once.
//
// It is modelled on `daedalus coordinator` deliberately: same subcommands, same
// pidfile-liveness rule, same output shape. Two daemons with two different
// lifecycles to learn would be one more thing to remember for no gain.
func manageControl(cfg *core.Config) error {
	if len(cfg.ControlArgs) == 0 {
		return fmt.Errorf("control: subcommand required (start|stop|restart|status)")
	}
	switch sub := cfg.ControlArgs[0]; sub {
	case "start":
		return controlStart(cfg)
	case "stop":
		return controlStop(cfg)
	case "restart":
		return controlRestart(cfg)
	case "status":
		return controlStatus(cfg)
	default:
		return fmt.Errorf("control: unknown subcommand %q (want start|stop|restart|status)", sub)
	}
}

// controlLayout projects the shared bootstrap layout. Layout changes belong in
// control.DefaultLayout; this only reads it, so the CLI can never disagree with
// the daemon about where its own socket is.
func controlLayout(cfg *core.Config) control.BootstrapOptions {
	return control.DefaultLayout(cfg.DataDir, cfg.ScriptDir)
}

func controlStart(cfg *core.Config) error {
	opts := controlLayout(cfg)
	if pid, alive := readPIDIfAlive(opts.PIDPath); alive {
		fmt.Printf("%s the control plane is already running (PID %d)\n", color.Green("OK:"), pid)
		printControlPaths(cfg, opts)
		return nil
	}
	if _, err := control.EnsureRunning(opts); err != nil {
		return err
	}
	// EnsureRunning waits for the socket, so the pidfile is fresh here.
	pid, _ := readPIDIfAlive(opts.PIDPath)
	fmt.Printf("%s the control plane is running (PID %d)\n", color.Green("OK:"), pid)
	printControlPaths(cfg, opts)
	return nil
}

func controlStop(cfg *core.Config) error {
	opts := controlLayout(cfg)
	stopped, err := stopControlDaemon(opts)
	if err != nil {
		return err
	}
	if !stopped {
		fmt.Printf("%s the control plane is not running (no live pidfile at %s)\n",
			color.Yellow("Info:"), opts.PIDPath)
		return nil
	}
	// Said because it is the question this command raises: nothing was lost.
	// The plane's state is in control.db, and a stopped daemon is a daemon not
	// listening — it is not a cancelled Task or an abandoned Job.
	fmt.Printf("%s nothing was lost — the plane's state is in control.db, and the next "+
		"`daedalus task` command starts it again.\n", color.Cyan("Note:"))
	return nil
}

// controlRestart is the reason many people will reach for this command.
//
// A daemon serves the routes it was compiled with, so after an upgrade the old
// process keeps answering with the old surface: a new operation returns 404 from
// a daemon that is behaving perfectly. Restarting is the fix, and it should not
// require knowing that.
func controlRestart(cfg *core.Config) error {
	opts := controlLayout(cfg)
	if stopped, err := stopControlDaemon(opts); err != nil {
		return err
	} else if !stopped {
		fmt.Printf("%s it was not running; starting it.\n", color.Yellow("Info:"))
	}
	return controlStart(cfg)
}

// stopControlDaemon sends SIGTERM and waits for the process to go. It reports
// whether there was anything to stop, so "already stopped" is a normal answer
// rather than an error — restart depends on that distinction.
func stopControlDaemon(opts control.BootstrapOptions) (bool, error) {
	pid, alive := readPIDIfAlive(opts.PIDPath)
	if !alive {
		return false, nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, fmt.Errorf("find process %d: %w", pid, err)
	}
	// SIGTERM, never SIGKILL. The daemon closes its sockets and its database on
	// the way out; killing it outright would leave a socket file behind that the
	// next start has to reason about, and this command exists to spare an
	// operator exactly that kind of knowledge.
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return false, fmt.Errorf("SIGTERM %d: %w", pid, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			fmt.Printf("%s the control plane stopped (was PID %d)\n", color.Green("OK:"), pid)
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, fmt.Errorf("the control plane (PID %d) did not exit within 5s; "+
				"it may be mid-transaction — try again, and use kill -9 %d only as a last resort", pid, pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func controlStatus(cfg *core.Config) error {
	opts := controlLayout(cfg)
	pid, alive := readPIDIfAlive(opts.PIDPath)
	if !alive {
		fmt.Printf("%s the control plane is not running\n", color.Yellow("Info:"))
		fmt.Printf("       start it with `daedalus control start`, or just run a " +
			"`daedalus task` command — they start it on demand.\n")
		return nil
	}
	fmt.Printf("%s the control plane is running (PID %d)\n", color.Green("OK:"), pid)
	printControlPaths(cfg, opts)

	if stale, since := daemonPredatesItsBinary(opts); stale {
		// The upgrade case, named rather than left to be diagnosed. A daemon that
		// predates its binary answers with the surface it was built with, and the
		// symptom is a new command 404ing against a daemon that is working.
		fmt.Printf("%s the running daemon started before %s was installed (%s ago).\n",
			color.Yellow("Stale:"), opts.DaemonBin, since.Truncate(time.Second))
		fmt.Printf("       It serves the routes it was BUILT with, so a command added since " +
			"then will 404. Restart it: `daedalus control restart`.\n")
	}

	// What the plane is actually doing. A pidfile says a process exists; this says
	// whether anything is happening, which is the question status is usually asked
	// in order to answer.
	client := control.NewClient(opts.SocketPath)
	st, err := client.PlaneStatus()
	if err != nil {
		fmt.Printf("%s the daemon is alive but did not answer: %v\n", color.Yellow("Warning:"), err)
		return nil
	}
	limit := "unbounded"
	if st.Limits.Global > 0 {
		limit = fmt.Sprintf("%d", st.Limits.Global)
	}
	fmt.Printf("       jobs:      %d running of %s", st.GlobalRunning, limit)
	if n := len(st.Waiting); n > 0 {
		fmt.Printf(", %d queued for a slot", n)
	}
	fmt.Println()
	if approvals, err := client.PendingApprovals(); err == nil && len(approvals) > 0 {
		fmt.Printf("       awaiting you: %d task(s) — `daedalus task approvals`\n", len(approvals))
	}
	if props, err := client.ListProposals(control.ProposalPending); err == nil && len(props) > 0 {
		fmt.Printf("       proposed:  %d awaiting your word — `daedalus task proposals list`\n", len(props))
	}
	return nil
}

// printControlPaths prints where the plane lives. Both sockets, because which
// one a caller reaches decides what it is allowed to do — the agent socket is
// the Guild Master's, and its absence is the usual reason a Guild Master starts
// with no control tools.
func printControlPaths(cfg *core.Config, opts control.BootstrapOptions) {
	fmt.Printf("       socket:    %s\n", opts.SocketPath)
	fmt.Printf("       agent:     %s\n", control.AgentSocketPath(cfg.DataDir))
	fmt.Printf("       log:       %s\n", opts.LogPath)
}

// daemonPredatesItsBinary reports whether the installed daemon binary is newer
// than the running daemon.
//
// The pidfile is written when the daemon starts, so its mtime is the process's
// start time closely enough for this purpose; the binary's mtime is when the
// version was installed. Binary newer than pidfile means the process running now
// is not the code on disk. It is a heuristic and is reported as one — the daemon
// serves no version of its own to ask, which would be the exact answer.
func daemonPredatesItsBinary(opts control.BootstrapOptions) (bool, time.Duration) {
	pidInfo, err := os.Stat(opts.PIDPath)
	if err != nil {
		return false, 0
	}
	binInfo, err := os.Stat(opts.DaemonBin)
	if err != nil {
		return false, 0
	}
	if !binInfo.ModTime().After(pidInfo.ModTime()) {
		return false, 0
	}
	return true, binInfo.ModTime().Sub(pidInfo.ModTime())
}
