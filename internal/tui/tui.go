// Copyright (C) 2026 Techdelight BV

package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/docker"
	"github.com/techdelight/daedalus/internal/executor"
	"github.com/techdelight/daedalus/internal/registry"
	"github.com/techdelight/daedalus/internal/session"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// setupCacheDir is a package-level reference to docker.SetupCacheDir,
// needed because the startProject parameter 'docker' shadows the package name.
var setupCacheDir = docker.SetupCacheDir

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
	dockerErr error
}

type actionResultMsg struct {
	msg string
	err error
}

type requestAttachMsg struct {
	sessionName string
}

// --- Model ---

type tuiModel struct {
	projects      []projectRow
	cursor        int
	err           error
	statusMsg     string
	registry      *registry.Registry
	docker        *docker.Docker
	executor      executor.Executor
	cfg           *core.Config
	pendingAttach string
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
		m.err = msg.dockerErr
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
		header := fmt.Sprintf("  %-20s %-12s %-10s %-8s %s", "PROJECT", "STATUS", "TARGET", "SESSIONS", "LAST USED")
		b.WriteString(headerStyle.Render(header))
		b.WriteString("\n")
		b.WriteString(headerStyle.Render("  " + strings.Repeat("\u2500", 70)))
		b.WriteString("\n")

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

	if m.statusMsg != "" {
		b.WriteString("  ")
		b.WriteString(statusMsgStyle.Render(m.statusMsg))
		b.WriteString("\n\n")
	}

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

func loadProjects(reg *registry.Registry, docker *docker.Docker) tea.Cmd {
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

func startProject(cfg *core.Config, exec executor.Executor, reg *registry.Registry, docker *docker.Docker, p projectRow) tea.Cmd {
	return func() tea.Msg {
		projCfg := &core.Config{
			ProjectName:     p.name,
			ProjectDir:      p.directory,
			ScriptDir:       cfg.ScriptDir,
			DataDir:         cfg.DataDir,
			Target:          p.target,
			ImagePrefix:     cfg.ImagePrefix,
			ClaudeConfigDir: cfg.ClaudeConfigDir,
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

		claudeArgs := core.BuildClaudeArgs(projCfg)
		dockerCmd := docker.ComposeRunCommand(projCfg.ContainerName(), claudeArgs, nil)
		tmuxCmd := core.BuildTmuxCommand(projCfg, dockerCmd)

		if err := sess.SendKeys(tmuxCmd); err != nil {
			return actionResultMsg{err: fmt.Errorf("sending command to tmux: %w", err)}
		}

		if err := reg.TouchProject(p.name); err != nil {
			return actionResultMsg{err: fmt.Errorf("updating project timestamp: %w", err)}
		}

		return requestAttachMsg{sessionName: projCfg.TmuxSession()}
	}
}

func killContainer(exec executor.Executor, name string) tea.Cmd {
	return func() tea.Msg {
		containerName := "claude-run-" + name
		err := exec.Run("docker", "stop", containerName)
		if err != nil {
			return actionResultMsg{err: fmt.Errorf("stopping %s: %w", containerName, err)}
		}
		return actionResultMsg{msg: fmt.Sprintf("Stopped %s", name)}
	}
}

func attachToSession(exec executor.Executor, name string) tea.Cmd {
	return func() tea.Msg {
		sessionName := "claude-" + name
		sess := session.NewSession(exec, sessionName)
		if !sess.Exists() {
			return actionResultMsg{msg: fmt.Sprintf("No tmux session for %s", name)}
		}
		return requestAttachMsg{sessionName: sessionName}
	}
}

// --- Entry point ---

func Run(cfg *core.Config) error {
	exec := &executor.RealExecutor{}
	reg := registry.NewRegistry(cfg.RegistryPath())
	if err := reg.Init(); err != nil {
		return fmt.Errorf("initializing registry: %w", err)
	}
	d := docker.NewDocker(exec, filepath.Join(cfg.ScriptDir, "docker-compose.yml"))

	m := tuiModel{
		registry: reg,
		docker:   d,
		executor: exec,
		cfg:      cfg,
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		return err
	}

	if fm, ok := finalModel.(tuiModel); ok && fm.pendingAttach != "" {
		sess := session.NewSession(exec, fm.pendingAttach)
		return sess.Attach()
	}
	return nil
}
