// Copyright (C) 2026 Techdelight BV

package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/attach"
	"github.com/techdelight/daedalus/internal/color"
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
