// Copyright (C) 2026 Techdelight BV

package tui

import (
	"fmt"
	"time"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/docker"
	"github.com/techdelight/daedalus/internal/executor"
	"github.com/techdelight/daedalus/internal/registry"

	tea "github.com/charmbracelet/bubbletea"
)

type projectRow struct {
	name         string
	directory    string
	target       string
	lastUsed     string
	running      bool
	sessionCount int
}

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
	confirming     bool     // whether delete-confirm mode is active
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
	filterActive   bool     // show only running projects
}

func (m tuiModel) filteredProjects() []projectRow {
	if !m.filterActive {
		return m.projects
	}
	var fp []projectRow
	for _, p := range m.projects {
		if p.running {
			fp = append(fp, p)
		}
	}
	return fp
}

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
		fp := m.filteredProjects()
		if m.cursor >= len(fp) {
			m.cursor = len(fp) - 1
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
		if m.confirming {
			return m.updateConfirm(msg)
		}
		if m.renaming {
			return m.updateRename(msg)
		}
		if m.creating {
			return m.updateCreate(msg)
		}
		return m.updateBrowse(msg)
	}

	return m, nil
}

// updateBrowse handles keypresses in the default project-list mode.
func (m tuiModel) updateBrowse(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	fp := m.filteredProjects()

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "j", "down":
		if m.cursor < len(fp)-1 {
			m.cursor++
		}
		clampScroll(&m)

	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
		clampScroll(&m)

	case "s":
		if len(fp) == 0 {
			return m, nil
		}
		p := fp[m.cursor]
		m.statusMsg = fmt.Sprintf("Starting %s...", p.name)
		return m, startProject(m.cfg, m.executor, m.registry, m.docker, p)

	case "a":
		if len(fp) == 0 {
			return m, nil
		}
		p := fp[m.cursor]
		if !p.running {
			m.statusMsg = fmt.Sprintf("%s is not running", p.name)
			return m, nil
		}
		return m, attachToSession(m.executor, p.name)

	case "x":
		if len(fp) == 0 {
			return m, nil
		}
		p := fp[m.cursor]
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

	case "f":
		m.filterActive = !m.filterActive
		fp = m.filteredProjects()
		if m.cursor >= len(fp) {
			m.cursor = max(len(fp)-1, 0)
		}
		m.scrollOffset = 0
		clampScroll(&m)
	}

	if msg.Type == tea.KeyF2 {
		if len(fp) == 0 {
			return m, nil
		}
		m.renaming = true
		m.renameInput = ""
		return m, nil
	}

	if msg.Type == tea.KeyDelete {
		if len(fp) == 0 {
			return m, nil
		}
		p := fp[m.cursor]
		if p.running {
			m.statusMsg = fmt.Sprintf("%s is running — stop it before removing", p.name)
			return m, nil
		}
		m.confirming = true
		m.statusMsg = ""
		return m, nil
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
	fp := m.filteredProjects()
	capacity := m.termHeight - chromeLines
	if capacity > len(fp) {
		return len(fp)
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
