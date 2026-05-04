// Copyright (C) 2026 Techdelight BV

package tui

import (
	"fmt"
	"time"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/docker"
	"github.com/techdelight/daedalus/internal/executor"
	"github.com/techdelight/daedalus/internal/registry"
	"github.com/techdelight/daedalus/internal/session"

	tea "github.com/charmbracelet/bubbletea"
)

// setupCacheDir is a package-level reference to docker.SetupCacheDir,
// needed because the startProject parameter 'docker' shadows the package name.
var setupCacheDir = docker.SetupCacheDir

func doTick() tea.Cmd {
	return tea.Tick(5*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func loadProjects(reg *registry.Registry, docker *docker.Docker, containerPrefix string) tea.Cmd {
	return func() tea.Msg {
		entries, err := reg.GetProjectEntries()
		if err != nil {
			return projectsLoadedMsg{err: err}
		}

		var dockerErr error
		rows := make([]projectRow, 0, len(entries))
		for _, e := range entries {
			containerName := core.ContainerNameFor(containerPrefix, e.Name)
			running, err := docker.IsContainerRunning(containerName)
			if err != nil && dockerErr == nil {
				dockerErr = err
			}
			rows = append(rows, projectRow{
				name:         e.Name,
				directory:    e.Entry.Directory,
				target:       e.Entry.Target,
				lastUsed:     e.Entry.LastUsed,
				running:      running,
				sessionCount: len(e.Entry.Sessions),
			})
		}
		return projectsLoadedMsg{projects: rows, dockerErr: dockerErr}
	}
}

func startProject(cfg *core.Config, exec executor.Executor, reg *registry.Registry, docker *docker.Docker, p projectRow) tea.Cmd {
	return func() tea.Msg {
		projCfg := &core.Config{
			ProjectName: p.name,
			ScriptDir:   cfg.ScriptDir,
			DataDir:     cfg.DataDir,
			ImagePrefix: cfg.ImagePrefix,
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

		if err := setupCacheDir(projCfg); err != nil {
			return actionResultMsg{err: err}
		}

		image := projCfg.Image()
		if !docker.ImageExists(image) {
			return actionResultMsg{err: fmt.Errorf("image %s not found — run daedalus --build %s first", image, p.name)}
		}

		running, err := docker.IsContainerRunning(projCfg.ContainerName())
		if err != nil {
			return actionResultMsg{err: err}
		}
		if running {
			return actionResultMsg{msg: fmt.Sprintf("%s is already running", p.name)}
		}

		sess := session.NewSession(exec, projCfg.TmuxSession())
		if sess.Exists() {
			return actionResultMsg{msg: fmt.Sprintf("Session %s already exists — use [a]ttach", projCfg.TmuxSession())}
		}

		if err := sess.Create(); err != nil {
			return actionResultMsg{err: fmt.Errorf("creating tmux session: %w", err)}
		}

		tmuxCmd := docker.BuildSessionCommand(projCfg)

		if err := sess.SendKeys(tmuxCmd); err != nil {
			return actionResultMsg{err: fmt.Errorf("sending command to tmux: %w", err)}
		}

		if err := reg.TouchProject(p.name); err != nil {
			return actionResultMsg{err: fmt.Errorf("updating project timestamp: %w", err)}
		}

		return requestAttachMsg{sessionName: projCfg.TmuxSession()}
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

func killContainer(exec executor.Executor, name, containerPrefix string) tea.Cmd {
	return func() tea.Msg {
		containerName := core.ContainerNameFor(containerPrefix, name)
		_, err := exec.Output("docker", "stop", containerName)
		if err != nil {
			return actionResultMsg{err: fmt.Errorf("stopping %s: %w", containerName, err)}
		}
		return actionResultMsg{msg: fmt.Sprintf("Stopped %s", name)}
	}
}

func attachToSession(exec executor.Executor, name, tmuxPrefix string) tea.Cmd {
	return func() tea.Msg {
		sessionName := core.TmuxSessionFor(tmuxPrefix, name)
		sess := session.NewSession(exec, sessionName)
		if !sess.Exists() {
			return actionResultMsg{msg: fmt.Sprintf("No tmux session for %s", name)}
		}
		return requestAttachMsg{sessionName: sessionName}
	}
}
