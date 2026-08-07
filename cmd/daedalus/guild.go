// Copyright (C) 2026 Techdelight BV

package main

import (
	"fmt"
	"os"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/logging"
	"github.com/techdelight/daedalus/internal/registry"
)

// ensureGuildMaster makes sure the built-in Guild Master project is present
// before any command enumerates or launches projects. It is deliberately
// best-effort: a failure to create/scaffold/register it is logged and warned
// but never aborts the CLI, so a read-only data dir (or a transient I/O error)
// can't stop the user from running Daedalus.
func ensureGuildMaster(cfg *core.Config) {
	reg := registry.NewRegistry(cfg.RegistryPath())
	if err := reg.Init(); err != nil {
		logging.Error("ensure guild master: init registry: " + err.Error())
		fmt.Fprintf(os.Stderr, "%s could not ensure Guild Master: %v\n", color.Yellow("Warning:"), err)
		return
	}
	if err := reg.EnsureGuildMaster(cfg.GuildMasterDir()); err != nil {
		logging.Error("ensure guild master: " + err.Error())
		fmt.Fprintf(os.Stderr, "%s could not ensure Guild Master: %v\n", color.Yellow("Warning:"), err)
	}
}
