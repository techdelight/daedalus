// Copyright (C) 2026 Techdelight BV

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/docker"
	"github.com/techdelight/daedalus/internal/logging"
	"github.com/techdelight/daedalus/internal/platform"
	"github.com/techdelight/daedalus/internal/registry"
	"github.com/techdelight/daedalus/internal/session"
)

// launchProject starts the project container, either in a tmux session or
// directly. It handles session tracking and DinD socket mounting.
//
// When DAEDALUS_USE_RUNNER=1 is set in the environment, the function
// short-circuits to the runner-detached path: docker compose run is
// launched with --detach and an entrypoint override that boots
// daedalus-runner inside the container; the host then attaches via
// the runclient Unix-socket bridge instead of tmux. This is the
// migration scaffolding for the new architecture.
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

// launchProjectViaRunner is the DAEDALUS_USE_RUNNER=1 path: spawn the
// project container in detached mode with daedalus-runner as its
// entrypoint, then attach via the runclient socket bridge. tmux is
// not involved.
//
// Lifecycle:
//   - sets DAEDALUS_RUNNER and DAEDALUS_SOCKET in the compose env so
//     entrypoint.sh dispatches to /usr/local/bin/daedalus-runner;
//   - launches with `docker compose run --rm --detach`, leaving the
//     container running as long as daedalus-runner is alive;
//   - attaches to the bind-mounted Unix socket and drives stdio /
//     resize / Ctrl-D detach against it;
//   - exit code from the runner becomes the CLI's exit code.
func launchProjectViaRunner(cfg *core.Config, reg *registry.Registry) error {
	sessionID, sessionErr := reg.StartSession(cfg.ProjectName, cfg.Resume)
	if sessionErr != nil {
		fmt.Fprintf(os.Stderr, "%s session tracking: %v\n", color.Yellow("Warning:"), sessionErr)
	}

	// Socket lives under the per-project cache dir, which is bind-
	// mounted at /home/claude inside the container. Same path on both
	// sides modulo the bind-mount prefix.
	sockHostDir := filepath.Join(cfg.CacheDir(), ".daedalus")
	if err := os.MkdirAll(sockHostDir, 0o755); err != nil {
		return fmt.Errorf("creating socket directory: %w", err)
	}
	sockHostPath := filepath.Join(sockHostDir, "runner.sock")
	_ = os.Remove(sockHostPath) // stale socket from a previous run blocks bind

	composeEnv := []string{
		"PROJECT_DIR=" + cfg.ProjectDir,
		"CACHE_DIR=" + cfg.CacheDir(),
		"TARGET=" + cfg.Target,
		"IMAGE=" + cfg.Image(),
		"RUNNER=" + core.ResolveRunnerName(cfg),
		"DAEDALUS_RUNNER=1",
		"DAEDALUS_SOCKET=/home/claude/.daedalus/runner.sock",
	}
	if cfg.Debug {
		composeEnv = append(composeEnv, "DAEDALUS_DEBUG=1")
	}
	if cfg.Resume != "" {
		composeEnv = append(composeEnv, "DAEDALUS_RESUME="+cfg.Resume)
	}
	if cfg.Prompt != "" {
		composeEnv = append(composeEnv, "DAEDALUS_PROMPT="+cfg.Prompt)
	}

	composeFile := filepath.Join(cfg.ScriptDir, "docker-compose.yml")
	cmd := exec.Command(
		"docker", "compose", "-f", composeFile,
		"run", "--rm", "--detach", "--name", cfg.ContainerName(),
		"claude",
	)
	cmd.Env = append(os.Environ(), composeEnv...)
	cmd.Stdout = os.Stderr // container ID lands on stderr; keep stdout clean
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose run --detach: %w", err)
	}

	fmt.Fprintf(os.Stderr, "%s container started; attaching. Press Ctrl-D to detach.\n", color.Green("OK:"))

	code, attachErr := attachToRunner(sockHostPath)

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
