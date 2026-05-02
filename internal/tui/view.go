// Copyright (C) 2026 Techdelight BV

package tui

import (
	"fmt"
	"strings"

	"github.com/techdelight/daedalus/core"
)

func (m tuiModel) View() string {
	var b strings.Builder

	fp := m.filteredProjects()

	b.WriteString("\n")
	title := "Daedalus [" + core.ReadVersion() + "]"
	if m.filterActive {
		title += " (active only)"
	}
	b.WriteString(titleStyle.Render(title))
	b.WriteString("\n\n")

	if m.err != nil {
		b.WriteString(statusMsgStyle.Render(fmt.Sprintf("Error: %v", m.err)))
		b.WriteString("\n\n")
	}

	if len(fp) == 0 && m.err == nil {
		if m.filterActive {
			b.WriteString(normalStyle.Render("  No running projects."))
		} else {
			b.WriteString(normalStyle.Render("  No registered projects."))
		}
		b.WriteString("\n\n")
	} else {
		b.WriteString(m.renderProjectTable(fp))
	}

	b.WriteString("\n")

	if m.statusMsg != "" {
		b.WriteString("  ")
		b.WriteString(statusMsgStyle.Render(m.statusMsg))
		b.WriteString("\n\n")
	}

	switch {
	case m.creating:
		b.WriteString(m.viewCreate())
	case m.confirming && m.cursor >= 0 && m.cursor < len(fp):
		b.WriteString(m.viewConfirm(fp))
	case m.renaming && m.cursor >= 0 && m.cursor < len(fp):
		b.WriteString(m.viewRename(fp))
	default:
		b.WriteString(helpStyle.Render("  [n]ew  [s]tart  [a]ttach  [x] kill  [del] remove  [f]ilter  [r]efresh  [F2] rename  [q]uit"))
	}
	b.WriteString("\n")

	return b.String()
}

func (m tuiModel) renderProjectTable(fp []projectRow) string {
	var b strings.Builder

	header := fmt.Sprintf("  %-20s %-12s %-10s %-8s %s", "PROJECT", "STATUS", "TARGET", "SESSIONS", "LAST USED")
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")
	b.WriteString(headerStyle.Render("  " + strings.Repeat("─", 70)))
	b.WriteString("\n")

	visRows := m.visibleRows()
	end := m.scrollOffset + visRows
	if end > len(fp) {
		end = len(fp)
	}
	showScrollbar := len(fp) > visRows
	trackHeight := visRows

	var thumbStart, thumbEnd int
	if showScrollbar && trackHeight > 0 {
		thumbSize := trackHeight * visRows / len(fp)
		if thumbSize < 1 {
			thumbSize = 1
		}
		thumbStart = trackHeight * m.scrollOffset / len(fp)
		thumbEnd = thumbStart + thumbSize
		if thumbEnd > trackHeight {
			thumbEnd = trackHeight
		}
	}

	for i := m.scrollOffset; i < end; i++ {
		p := fp[i]
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}

		var status string
		if p.running {
			status = runningStyle.Render("● running")
		} else {
			status = stoppedStyle.Render("○ stopped")
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
				row += " █"
			} else {
				row += " ░"
			}
		}

		b.WriteString(row)
		b.WriteString("\n")
	}

	return b.String()
}
