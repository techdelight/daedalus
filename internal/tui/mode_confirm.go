// Copyright (C) 2026 Techdelight BV

package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// updateConfirm handles keypresses while the delete-confirmation modal is active.
func (m tuiModel) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	fp := m.filteredProjects()
	switch msg.String() {
	case "y", "enter":
		p := fp[m.cursor]
		m.confirming = false
		m.statusMsg = fmt.Sprintf("Removing %s...", p.name)
		return m, removeProject(m.registry, p.name)
	case "n", "esc":
		m.confirming = false
		m.statusMsg = ""
		return m, nil
	}
	return m, nil
}

// viewConfirm renders the delete-confirmation prompt.
func (m tuiModel) viewConfirm(fp []projectRow) string {
	prompt := fmt.Sprintf("  Remove %q? ", fp[m.cursor].name)
	return statusMsgStyle.Render(prompt) + helpStyle.Render("  (y to confirm, esc to cancel)")
}
