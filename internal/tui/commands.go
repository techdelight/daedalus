// Copyright (C) 2026 Techdelight BV

package tui

import (
	"errors"
	"fmt"
	"time"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/coordinator"
	"github.com/techdelight/daedalus/internal/registry"

	tea "github.com/charmbracelet/bubbletea"
)

// coordinatorClient is the slice of the coordinator daemon client the TUI
// needs: discover sessions, start-or-attach, and stop. *coordinator.Client
// satisfies it; tests supply a fake. Defined at the consumer so the TUI's
// commands stay unit-testable without a live daemon.
type coordinatorClient interface {
	List() ([]coordinator.Session, error)
	Start(cfg *core.Config) (*coordinator.Session, error)
	Get(name string) (*coordinator.Session, error)
	Stop(name string) error
}

func doTick() tea.Cmd {
	return tea.Tick(5*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// loadProjects lists every registered project and marks which currently
// have a live runner session, per the coordinator daemon (client.List).
// Running state is the coordinator's to know now — the TUI no longer
// probes Docker directly.
func loadProjects(client coordinatorClient, reg *registry.Registry) tea.Cmd {
	return func() tea.Msg {
		entries, err := reg.GetProjectEntries()
		if err != nil {
			return projectsLoadedMsg{err: err}
		}

		running := map[string]bool{}
		sessions, listErr := client.List()
		for _, s := range sessions {
			running[s.ProjectName] = true
		}

		rows := make([]projectRow, 0, len(entries))
		for _, e := range entries {
			rows = append(rows, projectRow{
				name:         e.Name,
				directory:    e.Entry.Directory,
				target:       e.Entry.Target,
				lastUsed:     e.Entry.LastUsed,
				running:      running[e.Name],
				sessionCount: len(e.Entry.Sessions),
			})
		}
		return projectsLoadedMsg{projects: rows, listErr: listErr}
	}
}

// startProject asks the coordinator to spawn the project's runner session
// (or hand back the existing one), then requests an attach to its socket.
// Start-or-attach mirrors the CLI runner path: daedalus-runner fans its
// PTY out to every client, so a running container is an attach target.
func startProject(client coordinatorClient, cfg *core.Config, reg *registry.Registry, p projectRow) tea.Cmd {
	return func() tea.Msg {
		projCfg := &core.Config{
			ProjectName:     p.name,
			ScriptDir:       cfg.ScriptDir,
			DataDir:         cfg.DataDir,
			ImagePrefix:     cfg.ImagePrefix,
			ContainerPrefix: cfg.ContainerPrefix,
		}

		entry, found, err := reg.GetProject(p.name)
		if err != nil {
			return actionResultMsg{err: err}
		}
		if found {
			core.ApplyRegistryEntry(projCfg, entry)
		} else {
			projCfg.ProjectDir = p.directory
			projCfg.Target = p.target
		}

		sess, err := client.Start(projCfg)
		if errors.Is(err, coordinator.ErrAlreadyRunning) {
			sess, err = client.Get(p.name)
		}
		if err != nil {
			return actionResultMsg{err: err}
		}

		if err := reg.TouchProject(p.name); err != nil {
			return actionResultMsg{err: fmt.Errorf("updating project timestamp: %w", err)}
		}
		return requestAttachMsg{socketPath: sess.SocketPath}
	}
}

// attachToSession attaches to an already-running project's runner socket.
func attachToSession(client coordinatorClient, name string) tea.Cmd {
	return func() tea.Msg {
		sess, err := client.Get(name)
		if err != nil {
			if errors.Is(err, coordinator.ErrNotFound) {
				return actionResultMsg{msg: fmt.Sprintf("No running session for %s", name)}
			}
			return actionResultMsg{err: err}
		}
		return requestAttachMsg{socketPath: sess.SocketPath}
	}
}

// stopSession asks the coordinator to stop the project's runner session
// (and tear down its container).
func stopSession(client coordinatorClient, name string) tea.Cmd {
	return func() tea.Msg {
		if err := client.Stop(name); err != nil {
			return actionResultMsg{err: fmt.Errorf("stopping %s: %w", name, err)}
		}
		return actionResultMsg{msg: fmt.Sprintf("Stopped %s", name)}
	}
}

func renameProject(reg *registry.Registry, oldName, newName string) tea.Cmd {
	return func() tea.Msg {
		if err := core.ValidateProjectName(newName); err != nil {
			return actionResultMsg{err: err}
		}
		if err := reg.RenameProject(oldName, newName); err != nil {
			return actionResultMsg{err: fmt.Errorf("renaming %s: %w", oldName, err)}
		}
		return actionResultMsg{msg: fmt.Sprintf("Renamed %s to %s", oldName, newName)}
	}
}

func addProject(reg *registry.Registry, name, directory string) tea.Cmd {
	return func() tea.Msg {
		if err := reg.AddProject(name, directory, "dev"); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Created project %s", name)}
	}
}

func removeProject(reg *registry.Registry, name string) tea.Cmd {
	return func() tea.Msg {
		if err := reg.RemoveProject(name); err != nil {
			return actionResultMsg{err: fmt.Errorf("removing %s: %w", name, err)}
		}
		return actionResultMsg{msg: fmt.Sprintf("Removed %s", name)}
	}
}
