// Copyright (C) 2026 Techdelight BV

// Package tui implements the terminal UI for daedalus. The package is split
// into topic files: model.go (state + Update + browse-mode keys), view.go
// (rendering), styles.go (lipgloss styles), commands.go (tea.Cmd I/O
// factories), and one mode_*.go per modal state (mode_create, mode_rename,
// mode_confirm). This file owns the entry point only.
package tui

import (
	"fmt"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/attach"
	"github.com/techdelight/daedalus/internal/coordinator"
	"github.com/techdelight/daedalus/internal/registry"

	tea "github.com/charmbracelet/bubbletea"
)

// handleTUIResult inspects the final model after the TUI exits. It returns
// the runner socket path to attach to, or "" if the user quit normally.
func handleTUIResult(finalModel tea.Model) string {
	fm, ok := finalModel.(tuiModel)
	if !ok || fm.pendingAttach == "" {
		return ""
	}
	return fm.pendingAttach
}

func Run(cfg *core.Config) error {
	reg := registry.NewRegistry(cfg.RegistryPath())
	if err := reg.Init(); err != nil {
		return fmt.Errorf("initializing registry: %w", err)
	}

	// The TUI drives the runner path: it discovers, starts, and attaches
	// to sessions through the coordinator daemon (auto-spawned ssh-agent
	// style), never tmux. Same seam the CLI and Web already use.
	client, err := coordinator.EnsureRunning(coordinator.DefaultLayout(cfg.DataDir, cfg.ScriptDir))
	if err != nil {
		return fmt.Errorf("coordinator: %w", err)
	}

	var nextStatus string
	for {
		m := tuiModel{
			registry:  reg,
			client:    client,
			cfg:       cfg,
			statusMsg: nextStatus,
		}
		nextStatus = ""

		p := tea.NewProgram(m, tea.WithAltScreen())
		finalModel, err := p.Run()
		if err != nil {
			return err
		}

		socketPath := handleTUIResult(finalModel)
		if socketPath == "" {
			return nil // normal quit — exit to shell
		}

		// Blocks until the user detaches (Ctrl-D) or the runner exits,
		// then loops back to a fresh project list.
		if _, err := attach.ToRunner(socketPath); err != nil {
			nextStatus = fmt.Sprintf("Attach failed: %v", err)
		}
	}
}
