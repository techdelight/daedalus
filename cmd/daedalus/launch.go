// Copyright (C) 2026 Techdelight BV

package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/coordinator"
	"github.com/techdelight/daedalus/internal/docker"
	"github.com/techdelight/daedalus/internal/logging"
	"github.com/techdelight/daedalus/internal/platform"
	"github.com/techdelight/daedalus/internal/registry"
	"github.com/techdelight/daedalus/internal/session"
)

// launchProject starts the project container, either in a tmux session or
// directly. It handles session tracking and DinD socket mounting.
//
// By default the function short-circuits to launchProjectViaRunner, which
// delegates the container lifecycle to internal/coordinator and attaches
// through the runclient Unix-socket bridge instead of tmux. Set
// DAEDALUS_USE_TMUX=1 to take the classic tmux path instead (core.UseRunner).
func launchProject(cfg *core.Config, d *docker.Docker, reg *registry.Registry, sess *session.Session, useTmux bool) error {
	if core.UseRunner() {
		return launchProjectViaRunner(cfg, reg)
	}

	sessionID, sessionErr := reg.StartSession(cfg.ProjectName, cfg.Resume)
	if sessionErr != nil {
		fmt.Fprintf(os.Stderr, color.Yellow("Warning:")+" failed to start session tracking: %v\n", sessionErr)
	}

	claudeArgs := core.BuildRunnerArgs(cfg)
	composeEnv := map[string]string{
		"PROJECT_DIR": cfg.ProjectDir,
		"CACHE_DIR":   cfg.CacheDir(),
		"TARGET":      cfg.Target,
		"IMAGE":       cfg.Image(),
		"RUNNER":      core.ResolveRunnerName(cfg),
	}

	if cfg.DinD {
		fmt.Fprintln(os.Stderr, color.Yellow("WARNING:")+" --dind mounts the host Docker socket. This grants the container full access to host Docker.")
	}

	var containerLogPath string
	if cfg.ContainerLog {
		containerLogPath = cfg.ContainerLogPath()
		fmt.Printf("%s container log: %s\n", color.Dim("Log:"), containerLogPath)
	}

	var displayArgs []string
	if cfg.Display {
		var displayWarnings []string
		displayArgs, displayWarnings = platform.DisplayArgs(
			os.Getenv("DISPLAY"),
			os.Getenv("WAYLAND_DISPLAY"),
			os.Getenv("XDG_RUNTIME_DIR"),
		)
		for _, w := range displayWarnings {
			fmt.Fprintln(os.Stderr, color.Yellow("Warning:")+" "+w)
		}
	}
	overlay, err := resolvePersonaOverlay(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, color.Yellow("Warning:")+" persona overlay: %v\n", err)
	}
	extraArgs := core.BuildExtraArgs(cfg, displayArgs, overlay)

	if useTmux {
		dockerCmd := d.ComposeRunCommand(cfg.ContainerName(), claudeArgs, extraArgs)
		tmuxCmd := core.BuildTmuxCommand(cfg, dockerCmd)

		sess.PrintAttachHint(os.Args[0])
		if err := sess.Create(); err != nil {
			return fmt.Errorf("creating tmux session: %w", err)
		}
		if containerLogPath != "" {
			if err := sess.PipePane(containerLogPath); err != nil {
				return fmt.Errorf("setting up container log pipe: %w", err)
			}
		}
		if err := sess.SendKeys(tmuxCmd); err != nil {
			return fmt.Errorf("sending command to tmux: %w", err)
		}
		return sess.Attach()
	}

	// Direct execution (no tmux)
	runErr := d.ComposeRun(cfg.ContainerName(), composeEnv, claudeArgs, extraArgs, containerLogPath)
	if sessionErr == nil {
		if err := reg.EndSession(cfg.ProjectName, sessionID); err != nil {
			fmt.Fprintf(os.Stderr, color.Yellow("Warning:")+" failed to end session tracking: %v\n", err)
		}
	}
	if runErr != nil {
		logging.Error(runErr.Error())
	} else {
		logging.Info("done")
	}
	return runErr
}

// launchProjectViaRunner is the runner (default) path: ask the
// coordinator daemon to spawn the project container with daedalus-runner
// as its entrypoint, then attach via the runclient socket bridge.
// tmux is not involved.
//
// The daemon owns the container lifecycle across CLI invocations, so
// a second `daedalus <project>` invocation sees the existing session
// via the daemon's Get. Auto-spawn (ssh-agent style) means the user
// doesn't need to have started the daemon by hand.
func launchProjectViaRunner(cfg *core.Config, reg *registry.Registry) error {
	sessionID, sessionErr := reg.StartSession(cfg.ProjectName, cfg.Resume)
	if sessionErr != nil {
		fmt.Fprintf(os.Stderr, "%s session tracking: %v\n", color.Yellow("Warning:"), sessionErr)
	}

	client, err := ensureCoordinatorClient(cfg)
	if err != nil {
		return fmt.Errorf("coordinator: %w", err)
	}

	sess, err := client.Start(cfg)
	switch {
	case errors.Is(err, coordinator.ErrAlreadyRunning):
		// A session for this project is already live — a second shell, or
		// a re-attach after Ctrl-D. daedalus-runner fans its PTY out to
		// every connected client, so attach to the existing session
		// rather than failing (ssh-agent-style start-or-attach).
		sess, err = client.Get(cfg.ProjectName)
		if err != nil {
			return fmt.Errorf("attach to existing session: %w", err)
		}
		fmt.Fprintf(os.Stderr, "%s attaching to existing session. Press Ctrl-D to detach.\n", color.Green("OK:"))
	case err != nil:
		return err
	default:
		fmt.Fprintf(os.Stderr, "%s container started; attaching. Press Ctrl-D to detach.\n", color.Green("OK:"))
	}

	code, attachErr := attachToRunner(sess.SocketPath)

	if sessionErr == nil {
		if err := reg.EndSession(cfg.ProjectName, sessionID); err != nil {
			fmt.Fprintf(os.Stderr, "%s end session: %v\n", color.Yellow("Warning:"), err)
		}
	}

	if attachErr != nil {
		return attachErr
	}
	if code > 0 {
		return fmt.Errorf("runner exit code %d", code)
	}
	return nil
}
