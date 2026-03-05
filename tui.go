// Copyright (C) 2026 Techdelight BV

package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/techdelight/daedalus/core"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// --- Styles ---

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("135")) // purple

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("252")) // light gray

	selectedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("255")) // white

	normalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")) // dim gray

	runningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("42")) // green

	stoppedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")) // dim gray

	statusMsgStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("220")) // yellow

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")) // dark gray
)

// --- Data types ---

type projectRow struct {
	name         string
	directory    string
	target       string
	lastUsed     string
	running      bool
	sessionCount int
}

// --- Messages ---

type tickMsg time.Time

type projectsLoadedMsg struct {
	projects  []projectRow
	err       error
	dockerErr error // non-fatal: Docker query failed but projects are still listed
}

type actionResultMsg struct {
	msg string
	err error
}

// requestAttachMsg signals the TUI to quit cleanly and then attach to a tmux session.
// This avoids calling syscall.Exec from within bubbletea, which would skip terminal cleanup.
type requestAttachMsg struct {
	sessionName string
}

// --- Model ---

type tuiModel struct {
	projects      []projectRow
	cursor        int
	err           error
	statusMsg     string
	registry      *Registry
	docker        *Docker
	executor      Executor
	cfg           *core.Config
	pendingAttach string // tmux session to attach to after TUI exits
}

// --- tea.Model interface ---

func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(
		loadProjects(m.registry, m.docker),
		doTick(),
	)
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tickMsg:
		return m, tea.Batch(
			loadProjects(m.registry, m.docker),
			doTick(),
		)

	case projectsLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.projects = msg.projects
		m.err = msg.dockerErr // nil clears previous error; non-nil shows Docker warning
		// Clamp cursor
		if m.cursor >= len(m.projects) {
			m.cursor = len(m.projects) - 1
		}
		if m.cursor < 0 {
			m.cursor = 0
		}
		return m, nil

	case actionResultMsg:
		if msg.err != nil {
			m.statusMsg = fmt.Sprintf("Error: %v", msg.err)
		} else {
			m.statusMsg = msg.msg
		}
		return m, loadProjects(m.registry, m.docker)

	case requestAttachMsg:
		m.pendingAttach = msg.sessionName
		return m, tea.Quit

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "j", "down":
			if m.cursor < len(m.projects)-1 {
				m.cursor++
			}

		case "k", "up":
			if m.cursor > 0 {
				m.cursor--
			}

		case "s":
			if len(m.projects) == 0 {
				return m, nil
			}
			p := m.projects[m.cursor]
			m.statusMsg = fmt.Sprintf("Starting %s...", p.name)
			return m, startProject(m.cfg, m.executor, m.registry, m.docker, p)

		case "a":
			if len(m.projects) == 0 {
				return m, nil
			}
			p := m.projects[m.cursor]
			if !p.running {
				m.statusMsg = fmt.Sprintf("%s is not running", p.name)
				return m, nil
			}
			// Attach replaces the process via syscall.Exec.
			// The TUI will end; the user re-runs `daedalus tui` to return.
			return m, attachToSession(m.executor, p.name)

		case "K":
			if len(m.projects) == 0 {
				return m, nil
			}
			p := m.projects[m.cursor]
			if !p.running {
				m.statusMsg = fmt.Sprintf("%s is not running", p.name)
				return m, nil
			}
			m.statusMsg = fmt.Sprintf("Stopping %s...", p.name)
			return m, killContainer(m.executor, p.name)

		case "r":
			m.statusMsg = "Refreshing..."
			return m, loadProjects(m.registry, m.docker)
		}
	}

	return m, nil
}

func (m tuiModel) View() string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(titleStyle.Render("Claude Runner"))
	b.WriteString("\n\n")

	if m.err != nil {
		b.WriteString(statusMsgStyle.Render(fmt.Sprintf("Error: %v", m.err)))
		b.WriteString("\n\n")
	}

	if len(m.projects) == 0 && m.err == nil {
		b.WriteString(normalStyle.Render("  No registered projects."))
		b.WriteString("\n\n")
	} else {
		// Header
		header := fmt.Sprintf("  %-20s %-12s %-10s %-8s %s", "PROJECT", "STATUS", "TARGET", "SESSIONS", "LAST USED")
		b.WriteString(headerStyle.Render(header))
		b.WriteString("\n")
		b.WriteString(headerStyle.Render("  " + strings.Repeat("\u2500", 70)))
		b.WriteString("\n")

		// Rows
		for i, p := range m.projects {
			cursor := "  "
			if i == m.cursor {
				cursor = "> "
			}

			var status string
			if p.running {
				status = runningStyle.Render("\u25cf running")
			} else {
				status = stoppedStyle.Render("\u25cb stopped")
			}

			lastUsed := core.RelativeTime(p.lastUsed)

			name := p.name
			if len(name) > 18 {
				name = name[:18] + ".."
			}

			row := fmt.Sprintf("%-20s %-21s %-10s %-8d %s", name, status, p.target, p.sessionCount, lastUsed)

			if i == m.cursor {
				row = selectedStyle.Render(cursor + row)
			} else {
				row = normalStyle.Render(cursor + row)
			}

			b.WriteString(row)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")

	// Status message
	if m.statusMsg != "" {
		b.WriteString("  ")
		b.WriteString(statusMsgStyle.Render(m.statusMsg))
		b.WriteString("\n\n")
	}

	// Help bar
	b.WriteString(helpStyle.Render("  [s]tart  [a]ttach  [K]ill  [r]efresh  [q]uit"))
	b.WriteString("\n")

	return b.String()
}

// --- Commands ---

func doTick() tea.Cmd {
	return tea.Tick(5*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func loadProjects(reg *Registry, docker *Docker) tea.Cmd {
	return func() tea.Msg {
		entries, err := reg.GetProjectEntries()
		if err != nil {
			return projectsLoadedMsg{err: err}
		}

		var dockerErr error
		rows := make([]projectRow, 0, len(entries))
		for _, e := range entries {
			containerName := "claude-run-" + e.Name
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

func startProject(cfg *core.Config, exec Executor, reg *Registry, docker *Docker, p projectRow) tea.Cmd {
	return func() tea.Msg {
		// Build a config for this project
		projCfg := &core.Config{
			ProjectName:     p.name,
			ProjectDir:      p.directory,
			ScriptDir:       cfg.ScriptDir,
			DataDir:         cfg.DataDir,
			Target:          p.target,
			ImagePrefix:     cfg.ImagePrefix,
			ClaudeConfigDir: cfg.ClaudeConfigDir,
		}

		// Setup cache dir
		if err := SetupCacheDir(projCfg); err != nil {
			return actionResultMsg{err: err}
		}

		// Ensure image exists
		image := projCfg.Image()
		if !docker.ImageExists(image) {
			return actionResultMsg{err: fmt.Errorf("image %s not found — run daedalus --build %s first", image, p.name)}
		}

		// Check if already running
		running, err := docker.IsContainerRunning(projCfg.ContainerName())
		if err != nil {
			return actionResultMsg{err: err}
		}
		if running {
			return actionResultMsg{msg: fmt.Sprintf("%s is already running", p.name)}
		}

		// Create detached tmux session and launch container inside it
		session := NewSession(exec, projCfg.TmuxSession())
		if session.Exists() {
			return actionResultMsg{msg: fmt.Sprintf("Session %s already exists — use [a]ttach", projCfg.TmuxSession())}
		}

		if err := session.Create(); err != nil {
			return actionResultMsg{err: fmt.Errorf("creating tmux session: %w", err)}
		}

		claudeArgs := core.BuildClaudeArgs(projCfg)
		dockerCmd := docker.ComposeRunCommand(projCfg.ContainerName(), claudeArgs, nil)
		tmuxCmd := core.BuildTmuxCommand(projCfg, dockerCmd)

		if err := session.SendKeys(tmuxCmd); err != nil {
			return actionResultMsg{err: fmt.Errorf("sending command to tmux: %w", err)}
		}

		if err := reg.TouchProject(p.name); err != nil {
			return actionResultMsg{err: fmt.Errorf("updating project timestamp: %w", err)}
		}

		// Auto-attach after starting, just like the non-TUI flow.
		return requestAttachMsg{sessionName: projCfg.TmuxSession()}
	}
}

func killContainer(exec Executor, name string) tea.Cmd {
	return func() tea.Msg {
		containerName := "claude-run-" + name
		err := exec.Run("docker", "stop", containerName)
		if err != nil {
			return actionResultMsg{err: fmt.Errorf("stopping %s: %w", containerName, err)}
		}
		return actionResultMsg{msg: fmt.Sprintf("Stopped %s", name)}
	}
}

func attachToSession(exec Executor, name string) tea.Cmd {
	return func() tea.Msg {
		sessionName := "claude-" + name
		session := NewSession(exec, sessionName)
		if !session.Exists() {
			return actionResultMsg{msg: fmt.Sprintf("No tmux session for %s", name)}
		}
		// Signal the TUI to quit cleanly, then attach after terminal cleanup.
		return requestAttachMsg{sessionName: sessionName}
	}
}

// --- Entry point ---

func runTUI(cfg *core.Config) error {
	exec := &RealExecutor{}
	reg := NewRegistry(cfg.RegistryPath())
	if err := reg.Init(); err != nil {
		return fmt.Errorf("initializing registry: %w", err)
	}
	docker := NewDocker(exec, filepath.Join(cfg.ScriptDir, "docker-compose.yml"))

	m := tuiModel{
		registry: reg,
		docker:   docker,
		executor: exec,
		cfg:      cfg,
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		return err
	}

	// If the TUI requested a tmux attach, do it now that the terminal is restored.
	if fm, ok := finalModel.(tuiModel); ok && fm.pendingAttach != "" {
		session := NewSession(exec, fm.pendingAttach)
		return session.Attach()
	}
	return nil
}
