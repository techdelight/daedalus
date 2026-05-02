// Copyright (C) 2026 Techdelight BV

package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/techdelight/daedalus/core"

	tea "github.com/charmbracelet/bubbletea"
)

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
		b.WriteString(headerStyle.Render("  " + strings.Repeat("─", 40)))
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
