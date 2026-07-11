// Copyright (C) 2026 Techdelight BV

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/coordinator"
	"github.com/techdelight/daedalus/internal/docker"
	"github.com/techdelight/daedalus/internal/executor"
	"github.com/techdelight/daedalus/internal/logging"
	"github.com/techdelight/daedalus/internal/platform"
	"github.com/techdelight/daedalus/internal/registry"
	"github.com/techdelight/daedalus/internal/session"
)

// launchProject starts the project container, either in a tmux session or
// directly. It handles session tracking and DinD socket mounting.
//
// When DAEDALUS_USE_RUNNER=1 is set in the environment, the function
// short-circuits to launchProjectViaRunner, which delegates the container
// lifecycle to internal/coordinator and attaches through the runclient
// Unix-socket bridge instead of tmux.
func launchProject(cfg *core.Config, d *docker.Docker, reg *registry.Registry, sess *session.Session, useTmux bool) error {
	if os.Getenv("DAEDALUS_USE_RUNNER") == "1" {
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

// launchProjectViaRunner is the DAEDALUS_USE_RUNNER=1 path: ask the
// coordinator to spawn the project container with daedalus-runner as
// its entrypoint, then attach via the runclient socket bridge. tmux
// is not involved.
//
// The coordinator handles the lifecycle (compose env, `docker compose
// run --rm --detach`, socket-readiness wait); attachToRunner handles
// the host-terminal bridge to the returned socket.
func launchProjectViaRunner(cfg *core.Config, reg *registry.Registry) error {
	sessionID, sessionErr := reg.StartSession(cfg.ProjectName, cfg.Resume)
	if sessionErr != nil {
		fmt.Fprintf(os.Stderr, "%s session tracking: %v\n", color.Yellow("Warning:"), sessionErr)
	}

	// The coordinator's session map is process-scoped: this CLI invocation
	// owns exactly one runner session and exits when the user detaches.
	// No coord.Stop on the way out — the container survives detach so the
	// user can reattach (CLI or Web), and the in-memory map dies with us.
	coord := coordinator.New(coordinator.Options{
		Executor:    &executor.RealExecutor{},
		ComposeFile: filepath.Join(cfg.ScriptDir, "docker-compose.yml"),
	})
	sess, err := coord.Start(cfg)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "%s container started; attaching. Press Ctrl-D to detach.\n", color.Green("OK:"))

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
