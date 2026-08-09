// Copyright (C) 2026 Techdelight BV

package tui

import (
	"fmt"
	"time"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/control"
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
	projects []projectRow
	err      error
	listErr  error
}

type actionResultMsg struct {
	msg string
	err error
}

type requestAttachMsg struct {
	socketPath string
}

type tuiModel struct {
	projects       []projectRow
	cursor         int
	err            error
	statusMsg      string
	registry       *registry.Registry
	client         coordinatorClient
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

	// Control plane: the pending-approvals view (Sprint 59). `control` is nil
	// when the daedalus-control daemon is not running — the TUI never spawns it.
	control            control.TaskAPI
	approving          bool // whether the approvals view is open
	approvals          []approvalRow
	approvalCursor     int
	approvalsAvailable bool
	approvalsReason    string
	plane              planeLine
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
		loadProjects(m.client, m.registry),
		doTick(),
	)
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tickMsg:
		return m, tea.Batch(
			loadProjects(m.client, m.registry),
			doTick(),
		)

	case projectsLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.projects = msg.projects
		m.err = msg.listErr
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
		return m, loadProjects(m.client, m.registry)

	case requestAttachMsg:
		m.pendingAttach = msg.socketPath
		return m, tea.Quit

	case tea.WindowSizeMsg:
		m.termHeight = msg.Height
		return m, nil

	case approvalsLoadedMsg:
		m.approvals = msg.rows
		m.approvalsAvailable = msg.available
		m.approvalsReason = msg.reason
		m.plane = msg.plane
		if m.approvalCursor >= len(m.approvals) {
			m.approvalCursor = max(len(m.approvals)-1, 0)
		}
		return m, nil

	case approvalDecidedMsg:
		if msg.err != nil {
			m.statusMsg = "Approval failed: " + msg.err.Error()
		} else {
			m.statusMsg = msg.msg
		}
		return m, loadApprovals(m.control)

	case tea.KeyMsg:
		if m.approving {
			return m.updateApprovals(msg)
		}
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
		return m, startProject(m.client, m.cfg, m.registry, p)

	case "a":
		if len(fp) == 0 {
			return m, nil
		}
		p := fp[m.cursor]
		if !p.running {
			m.statusMsg = fmt.Sprintf("%s is not running", p.name)
			return m, nil
		}
		return m, attachToSession(m.client, p.name)

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
		return m, stopSession(m.client, p.name)

	case "r":
		m.statusMsg = "Refreshing..."
		return m, loadProjects(m.client, m.registry)

	case "n":
		m.creating = true
		m.createStep = 0
		m.createName = ""
		m.statusMsg = ""
		return m, nil

	case "A":
		// Capital A so it cannot be confused with [a]ttach, which is the muscle
		// memory this screen shares.
		m.approving = true
		m.approvalCursor = 0
		m.statusMsg = ""
		return m, loadApprovals(m.control)

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
