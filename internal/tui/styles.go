// Copyright (C) 2026 Techdelight BV

package tui

import "github.com/charmbracelet/lipgloss"

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
