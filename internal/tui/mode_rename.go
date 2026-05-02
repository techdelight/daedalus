// Copyright (C) 2026 Techdelight BV

package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// updateRename handles keypresses while rename mode is active.
func (m tuiModel) updateRename(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	fp := m.filteredProjects()
	switch msg.Type {
	case tea.KeyEnter:
		name := strings.TrimSpace(m.renameInput)
		if name == "" {
			return m, nil
		}
		p := fp[m.cursor]
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

// viewRename renders the rename prompt.
func (m tuiModel) viewRename(fp []projectRow) string {
	prompt := fmt.Sprintf("  Rename %q to: %s", fp[m.cursor].name, m.renameInput)
	return statusMsgStyle.Render(prompt) + helpStyle.Render("  (enter to confirm, esc to cancel)")
}
