// Copyright (C) 2026 Techdelight BV

package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/attach"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/control"
	"github.com/techdelight/daedalus/internal/coordinator"
	"github.com/techdelight/daedalus/internal/registry"
)

// launchProject asks the coordinator daemon to spawn the project container
// with daedalus-runner as its entrypoint, then attaches via the runclient
// socket bridge.
//
// The daemon owns the container lifecycle across CLI invocations, so a second
// `daedalus <project>` invocation sees the existing session via the daemon's
// Get. Auto-spawn (ssh-agent style) means the user doesn't need to have started
// the daemon by hand. Because daedalus-runner fans its PTY out to every
// connected client, a running container is an attach target, not a duplicate
// error.
func launchProject(cfg *core.Config, reg *registry.Registry) error {
	sessionID, sessionErr := reg.StartSession(cfg.ProjectName, cfg.Resume)
	if sessionErr != nil {
		fmt.Fprintf(os.Stderr, "%s session tracking: %v\n", color.Yellow("Warning:"), sessionErr)
	}

	// The Guild Master is the one project whose container may hold a control-plane
	// client, and the socket it connects through has to EXIST before the container
	// starts — a bind-mount source is resolved at `docker run`, not later. So the
	// plane is started here, on the human's launch command, rather than by the
	// coordinator: the coordinator is a daemon that manages containers, and giving
	// it the job of spawning a second daemon would put that decision somewhere the
	// operator cannot see it. Non-fatal by design — a Guild Master that cannot
	// reach the plane is still the read-only overseer it was in M12.
	if core.IsGuildMaster(cfg.ProjectName) {
		ensureControlPlane(cfg)
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

	code, attachErr := attach.ToRunner(sess.SocketPath)

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

// ensureControlPlane starts daedalus-control if it is not already listening, so
// the restricted agent socket exists for the Guild Master's launch to mount.
//
// Failure is reported and swallowed. The alternative — refusing to launch the
// Guild Master because a *second* subsystem is unavailable — would trade a
// working read-only overseer for no overseer at all, and the operator would be
// stuck with a project they cannot open. What they get instead is the warning
// plus a Guild Master with no guild-control tools, which is a state the
// entrypoint's socket gate already handles correctly.
func ensureControlPlane(cfg *core.Config) {
	if _, err := control.EnsureRunning(control.DefaultLayout(cfg.DataDir, cfg.ScriptDir)); err != nil {
		fmt.Fprintf(os.Stderr,
			"%s control plane not started (%v)\n         the Guild Master will launch as a READ-ONLY overseer, without the guild-control tools\n",
			color.Yellow("Warning:"), err)
	}
}
