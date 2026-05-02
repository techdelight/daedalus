// Copyright (C) 2026 Techdelight BV

package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/registry"
)

// showOrEditConfig displays or modifies per-project default flags.
func showOrEditConfig(cfg *core.Config) error {
	if cfg.ConfigTarget == "" {
		return fmt.Errorf("usage: daedalus config <project-name> [--set key=value] [--unset key]")
	}

	reg := registry.NewRegistry(cfg.RegistryPath())
	if err := reg.Init(); err != nil {
		return fmt.Errorf("initializing registry: %w", err)
	}

	entry, found, err := reg.GetProject(cfg.ConfigTarget)
	if err != nil {
		return fmt.Errorf("reading project: %w", err)
	}
	if !found {
		return fmt.Errorf("project '%s' not found in registry\n%s run 'daedalus list' to see registered projects", cfg.ConfigTarget, color.Cyan("Hint:"))
	}

	// Apply --set and --unset if provided
	if len(cfg.ConfigSet) > 0 || len(cfg.ConfigUnset) > 0 {
		setMap := make(map[string]string)
		for _, kv := range cfg.ConfigSet {
			parts := strings.SplitN(kv, "=", 2)
			// Handle target= specially — update the project target directly
			if parts[0] == "target" {
				if !core.IsValidTarget(parts[1]) {
					return fmt.Errorf("invalid target %q — valid targets: %s",
						parts[1], strings.Join(core.ValidTargets(), ", "))
				}
				if err := reg.UpdateProjectTarget(cfg.ConfigTarget, parts[1]); err != nil {
					return fmt.Errorf("updating target: %w", err)
				}
				fmt.Printf("%s target changed to '%s' for '%s'.\n", color.Green("OK:"), parts[1], cfg.ConfigTarget)
				continue
			}
			setMap[parts[0]] = parts[1]
		}
		if len(setMap) > 0 || len(cfg.ConfigUnset) > 0 {
			if err := reg.UpdateDefaultFlags(cfg.ConfigTarget, setMap, cfg.ConfigUnset); err != nil {
				return fmt.Errorf("updating config: %w", err)
			}
		}
		// Re-read to show updated state
		entry, _, err = reg.GetProject(cfg.ConfigTarget)
		if err != nil {
			return fmt.Errorf("reading project: %w", err)
		}
		fmt.Printf("%s updated config for '%s'.\n", color.Green("OK:"), cfg.ConfigTarget)
	}

	// Display project config
	fmt.Printf("%s %s\n", color.Bold("Project:"), cfg.ConfigTarget)
	fmt.Printf("%s %s\n", color.Bold("Directory:"), entry.Directory)
	fmt.Printf("%s %s\n", color.Bold("Target:"), entry.Target)
	fmt.Printf("%s %d\n", color.Bold("Sessions:"), len(entry.Sessions))

	if len(entry.DefaultFlags) > 0 {
		fmt.Printf("\n%s\n", color.Bold("Default Flags:"))
		// Sort keys for deterministic output
		keys := make([]string, 0, len(entry.DefaultFlags))
		for k := range entry.DefaultFlags {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("  %s = %s\n", k, entry.DefaultFlags[k])
		}
	} else {
		fmt.Printf("\n%s\n", color.Dim("No default flags configured."))
	}

	return nil
}
