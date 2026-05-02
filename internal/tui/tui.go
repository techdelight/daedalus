// Copyright (C) 2026 Techdelight BV

// Package tui implements the terminal UI for daedalus. The package is split
// into topic files: model.go (state + Update + browse-mode keys), view.go
// (rendering), styles.go (lipgloss styles), commands.go (tea.Cmd I/O
// factories), and one mode_*.go per modal state (mode_create, mode_rename,
// mode_confirm). This file owns the entry point only.
package tui

import (
	"fmt"
	"path/filepath"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/docker"
	"github.com/techdelight/daedalus/internal/executor"
	"github.com/techdelight/daedalus/internal/registry"
	"github.com/techdelight/daedalus/internal/session"

	tea "github.com/charmbracelet/bubbletea"
)

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
