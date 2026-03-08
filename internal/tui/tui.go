// Copyright (C) 2026 Techdelight BV

package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	projects       []projectRow
	cursor         int
	err            error
	statusMsg      string
	registry       *registry.Registry
	docker         *docker.Docker
	executor       executor.Executor
	cfg            *core.Config
	pendingAttach  string
	renaming       bool     // whether rename mode is active
	renameInput    string   // text being typed for the new name
	termHeight     int      // from tea.WindowSizeMsg
	scrollOffset   int      // first visible project index
	creating       bool     // create mode active
	createStep     int      // 0=name, 1=directory browser
	createName     string   // project name input
	createDir      string   // current browsing directory
	createDirItems []string // subdirectories in createDir
	createDirIdx   int      // cursor within directory listing
	creatingDir    bool     // sub-mode: typing new dir name
	createNewDir   string   // new directory name input
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
		clampScroll(&m)
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

	case tea.WindowSizeMsg:
		m.termHeight = msg.Height
		return m, nil

	case tea.KeyMsg:
		// When in rename mode, forward all keys to the rename input
		if m.renaming {
			switch msg.Type {
			case tea.KeyEnter:
				name := strings.TrimSpace(m.renameInput)
				if name == "" {
					return m, nil
				}
				p := m.projects[m.cursor]
				if p.running {
					m.statusMsg = fmt.Sprintf("%s is running — stop it before renaming", p.name)
					m.renaming = false
					m.renameInput = ""
					return m, nil
				}
				m.renaming = false
				m.statusMsg = fmt.Sprintf("Renaming %s to %s...", p.name, name)
				return m, renameProject(m.registry, p.name, name)
			case tea.KeyEsc:
				m.renaming = false
				m.renameInput = ""
				return m, nil
			case tea.KeyBackspace:
				if len(m.renameInput) > 0 {
					m.renameInput = m.renameInput[:len(m.renameInput)-1]
				}
				return m, nil
			case tea.KeyRunes:
				m.renameInput += string(msg.Runes)
				return m, nil
			}
			return m, nil
		}

		// When in create mode, forward keys to create handlers
		if m.creating {
			return m.updateCreate(msg)
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "j", "down":
			if m.cursor < len(m.projects)-1 {
				m.cursor++
			}
			clampScroll(&m)

		case "k", "up":
			if m.cursor > 0 {
				m.cursor--
			}
			clampScroll(&m)

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

		case "delete":
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

		case "n":
			m.creating = true
			m.createStep = 0
			m.createName = ""
			m.statusMsg = ""
			return m, nil
		}

		// F2 enters rename mode
		if msg.Type == tea.KeyF2 {
			if len(m.projects) == 0 {
				return m, nil
			}
			m.renaming = true
			m.renameInput = ""
			return m, nil
		}
	}

	return m, nil
}

// chromeLines is the number of lines reserved for non-project UI elements:
// blank + title + blank + header + separator + blank + status/help + newline = 7
const chromeLines = 7

func (m tuiModel) visibleRows() int {
	if m.termHeight <= chromeLines {
		return 1
	}
	capacity := m.termHeight - chromeLines
	if capacity > len(m.projects) {
		return len(m.projects)
	}
	return capacity
}

func clampScroll(m *tuiModel) {
	vis := m.visibleRows()
	if m.cursor < m.scrollOffset {
		m.scrollOffset = m.cursor
	}
	if m.cursor >= m.scrollOffset+vis {
		m.scrollOffset = m.cursor - vis + 1
	}
}

func (m tuiModel) View() string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(titleStyle.Render("Daedalus [" + core.ReadVersion() + "]"))
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

		visRows := m.visibleRows()
		end := m.scrollOffset + visRows
		if end > len(m.projects) {
			end = len(m.projects)
		}
		showScrollbar := len(m.projects) > visRows
		trackHeight := visRows

		// Compute scrollbar thumb position and size
		var thumbStart, thumbEnd int
		if showScrollbar && trackHeight > 0 {
			thumbSize := trackHeight * visRows / len(m.projects)
			if thumbSize < 1 {
				thumbSize = 1
			}
			thumbStart = trackHeight * m.scrollOffset / len(m.projects)
			thumbEnd = thumbStart + thumbSize
			if thumbEnd > trackHeight {
				thumbEnd = trackHeight
			}
		}

		for i := m.scrollOffset; i < end; i++ {
			p := m.projects[i]
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

			if showScrollbar {
				trackIdx := i - m.scrollOffset
				if trackIdx >= thumbStart && trackIdx < thumbEnd {
					row += " \u2588"
				} else {
					row += " \u2591"
				}
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

	if m.creating {
		b.WriteString(m.viewCreate())
	} else if m.renaming && m.cursor >= 0 && m.cursor < len(m.projects) {
		prompt := fmt.Sprintf("  Rename %q to: %s", m.projects[m.cursor].name, m.renameInput)
		b.WriteString(statusMsgStyle.Render(prompt))
		b.WriteString(helpStyle.Render("  (enter to confirm, esc to cancel)"))
	} else {
		b.WriteString(helpStyle.Render("  [n]ew  [s]tart  [a]ttach  [del]ete  [r]efresh  [F2] rename  [q]uit"))
	}
	b.WriteString("\n")

	return b.String()
}

// --- Create mode ---

func (m tuiModel) updateCreate(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.createStep == 0 {
		return m.updateCreateName(msg)
	}
	if m.creatingDir {
		return m.updateCreateNewDir(msg)
	}
	return m.updateCreateBrowser(msg)
}

func (m tuiModel) updateCreateName(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		name := strings.TrimSpace(m.createName)
		if name == "" {
			return m, nil
		}
		if err := core.ValidateProjectName(name); err != nil {
			m.statusMsg = fmt.Sprintf("Error: %v", err)
			m.creating = false
			m.createName = ""
			return m, nil
		}
		exists, err := m.registry.HasProject(name)
		if err != nil {
			m.statusMsg = fmt.Sprintf("Error: %v", err)
			m.creating = false
			m.createName = ""
			return m, nil
		}
		if exists {
			m.statusMsg = fmt.Sprintf("Error: project %q already exists", name)
			m.creating = false
			m.createName = ""
			return m, nil
		}
		m.createName = name
		m.createStep = 1
		home, err := os.UserHomeDir()
		if err != nil {
			home = "/"
		}
		m.createDir = home
		m.createDirItems = listDirs(home)
		m.createDirIdx = 0
		return m, nil
	case tea.KeyEsc:
		m.creating = false
		m.createName = ""
		return m, nil
	case tea.KeyBackspace:
		if len(m.createName) > 0 {
			m.createName = m.createName[:len(m.createName)-1]
		}
		return m, nil
	case tea.KeyRunes:
		m.createName += string(msg.Runes)
		return m, nil
	}
	return m, nil
}

func (m tuiModel) updateCreateBrowser(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if m.createDirIdx < len(m.createDirItems)-1 {
			m.createDirIdx++
		}
		return m, nil
	case "k", "up":
		if m.createDirIdx > 0 {
			m.createDirIdx--
		}
		return m, nil
	case "s":
		m.creating = false
		m.statusMsg = fmt.Sprintf("Creating project %s...", m.createName)
		return m, addProject(m.registry, m.createName, m.createDir)
	case "c":
		m.creatingDir = true
		m.createNewDir = ""
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEnter:
		if len(m.createDirItems) == 0 {
			return m, nil
		}
		selected := m.createDirItems[m.createDirIdx]
		if selected == ".." {
			m.createDir = filepath.Dir(m.createDir)
		} else {
			m.createDir = filepath.Join(m.createDir, selected)
		}
		m.createDirItems = listDirs(m.createDir)
		m.createDirIdx = 0
		return m, nil
	case tea.KeyBackspace:
		m.createDir = filepath.Dir(m.createDir)
		m.createDirItems = listDirs(m.createDir)
		m.createDirIdx = 0
		return m, nil
	case tea.KeyEsc:
		m.creating = false
		m.createName = ""
		return m, nil
	}
	return m, nil
}

func (m tuiModel) updateCreateNewDir(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		name := strings.TrimSpace(m.createNewDir)
		if name == "" {
			return m, nil
		}
		newPath := filepath.Join(m.createDir, name)
		if err := os.MkdirAll(newPath, 0755); err != nil {
			m.statusMsg = fmt.Sprintf("Error: %v", err)
		}
		m.creatingDir = false
		m.createNewDir = ""
		m.createDirItems = listDirs(m.createDir)
		return m, nil
	case tea.KeyEsc:
		m.creatingDir = false
		m.createNewDir = ""
		return m, nil
	case tea.KeyBackspace:
		if len(m.createNewDir) > 0 {
			m.createNewDir = m.createNewDir[:len(m.createNewDir)-1]
		}
		return m, nil
	case tea.KeyRunes:
		m.createNewDir += string(msg.Runes)
		return m, nil
	}
	return m, nil
}

func (m tuiModel) viewCreate() string {
	var b strings.Builder
	if m.createStep == 0 {
		b.WriteString(statusMsgStyle.Render(fmt.Sprintf("  New project name: %s", m.createName)))
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("  (enter to continue, esc to cancel)"))
	} else {
		b.WriteString(statusMsgStyle.Render(fmt.Sprintf("  New project: %s", m.createName)))
		b.WriteString("\n")
		b.WriteString(statusMsgStyle.Render(fmt.Sprintf("  Select directory: %s", m.createDir)))
		b.WriteString("\n")
		b.WriteString(headerStyle.Render("  " + strings.Repeat("\u2500", 40)))
		b.WriteString("\n")

		for i, item := range m.createDirItems {
			cursor := "    "
			if i == m.createDirIdx {
				cursor = "  > "
			}
			if i == m.createDirIdx {
				b.WriteString(selectedStyle.Render(cursor + item))
			} else {
				b.WriteString(normalStyle.Render(cursor + item))
			}
			b.WriteString("\n")
		}

		if m.creatingDir {
			b.WriteString(statusMsgStyle.Render(fmt.Sprintf("  New directory name: %s", m.createNewDir)))
			b.WriteString("\n")
			b.WriteString(helpStyle.Render("  (enter to create, esc to cancel)"))
		} else {
			b.WriteString(helpStyle.Render("  (enter=open  s=select  c=create dir  backspace=up  esc=cancel)"))
		}
	}
	return b.String()
}

func listDirs(path string) []string {
	dirs := []string{".."}
	entries, err := os.ReadDir(path)
	if err != nil {
		return dirs
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return append(dirs, names...)
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
			ProjectName: p.name,
			ProjectDir:  p.directory,
			ScriptDir:   cfg.ScriptDir,
			DataDir:     cfg.DataDir,
			Target:      p.target,
			ImagePrefix: cfg.ImagePrefix,
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

func killContainer(exec executor.Executor, name string) tea.Cmd {
	return func() tea.Msg {
		containerName := "claude-run-" + name
		_, err := exec.Output("docker", "stop", containerName)
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

// handleTUIResult inspects the final model after the TUI exits.
// It returns the session name to attach to, or "" if the user quit normally.
func handleTUIResult(finalModel tea.Model) string {
	fm, ok := finalModel.(tuiModel)
	if !ok || fm.pendingAttach == "" {
		return ""
	}
	return fm.pendingAttach
}

func Run(cfg *core.Config) error {
	core.PrintBanner(cfg.ScriptDir)
	exec := &executor.RealExecutor{}
	reg := registry.NewRegistry(cfg.RegistryPath())
	if err := reg.Init(); err != nil {
		return fmt.Errorf("initializing registry: %w", err)
	}
	d := docker.NewDocker(exec, filepath.Join(cfg.ScriptDir, "docker-compose.yml"))

	for {
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

		sessionName := handleTUIResult(finalModel)
		if sessionName == "" {
			return nil // normal quit — exit to shell
		}

		sess := session.NewSession(exec, sessionName)
		sess.AttachWait() // blocks until detach/exit, then loops back to TUI
	}
}
