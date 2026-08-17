// Copyright (C) 2026 Techdelight BV

package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/config"
	"github.com/techdelight/daedalus/internal/docker"
	"github.com/techdelight/daedalus/internal/executor"
	"github.com/techdelight/daedalus/internal/registry"
)

// listProjects prints a formatted table of all registered projects.
func listProjects(cfg *core.Config) error {
	reg := registry.NewRegistry(cfg.RegistryPath())
	if err := reg.Init(); err != nil {
		return fmt.Errorf("initializing registry: %w", err)
	}

	entries, err := reg.GetProjectEntries()
	if err != nil {
		return fmt.Errorf("reading projects: %w", err)
	}

	if len(entries) == 0 {
		fmt.Println("No registered projects.")
		return nil
	}

	// Calculate column widths
	nameW, dirW, targetW := 7, 9, 6 // minimum header widths
	for _, e := range entries {
		if len(e.Name) > nameW {
			nameW = len(e.Name)
		}
		if len(e.Entry.Directory) > dirW {
			dirW = len(e.Entry.Directory)
		}
		if len(e.Entry.Target) > targetW {
			targetW = len(e.Entry.Target)
		}
	}

	// Print header
	fmt.Printf("%-*s  %-*s  %-*s  %-8s  %s\n", nameW, color.Bold("PROJECT"), dirW, color.Bold("DIRECTORY"), targetW, color.Bold("TARGET"), color.Bold("SESSIONS"), color.Bold("LAST USED"))
	fmt.Printf("%-*s  %-*s  %-*s  %-8s  %s\n", nameW, strings.Repeat("-", nameW), dirW, strings.Repeat("-", dirW), targetW, strings.Repeat("-", targetW), "--------", "---------")

	// Print rows
	for _, e := range entries {
		tag := ""
		if core.IsGuildMaster(e.Name) {
			tag = "  " + color.Cyan("[built-in manager]")
		}
		fmt.Printf("%-*s  %-*s  %-*s  %-8d  %s%s\n", nameW, e.Name, dirW, e.Entry.Directory, targetW, e.Entry.Target, len(e.Entry.Sessions), e.Entry.LastUsed, tag)
	}
	return nil
}

// pruneProjects removes registry entries whose project directories no longer exist.
func pruneProjects(cfg *core.Config) error {
	reg := registry.NewRegistry(cfg.RegistryPath())
	if err := reg.Init(); err != nil {
		return fmt.Errorf("initializing registry: %w", err)
	}

	entries, err := reg.GetProjectEntries()
	if err != nil {
		return fmt.Errorf("reading projects: %w", err)
	}

	var stale []string
	for _, e := range entries {
		// The Guild Master owns its workspace and is never pruned, even if its
		// directory is somehow missing — it will be re-scaffolded on next ensure.
		if core.IsGuildMaster(e.Name) {
			continue
		}
		info, err := os.Stat(e.Entry.Directory)
		if err != nil || !info.IsDir() {
			stale = append(stale, e.Name)
		}
	}

	if len(stale) == 0 {
		fmt.Println("No stale projects found.")
		return nil
	}

	fmt.Printf("Found %d stale project(s):\n", len(stale))
	for _, name := range stale {
		fmt.Printf("  - %s\n", name)
	}

	proceed, err := confirmDestructive(cfg, "\nRemove these entries? [Y/n]: ")
	if err != nil {
		return err
	}
	if !proceed {
		return nil
	}

	removed, err := reg.RemoveProjects(stale)
	if err != nil {
		return fmt.Errorf("removing stale projects: %w", err)
	}
	for _, name := range removed {
		fmt.Printf("%s '%s'.\n", color.Green("Removed"), name)
	}
	return nil
}

// renameProject renames a registered project.
func renameProject(cfg *core.Config) error {
	if cfg.RenameOldName == "" || cfg.RenameNewName == "" {
		return fmt.Errorf("usage: daedalus rename <old-name> <new-name>")
	}

	if err := core.ValidateProjectName(cfg.RenameNewName); err != nil {
		return err
	}

	reg := registry.NewRegistry(cfg.RegistryPath())
	if err := reg.Init(); err != nil {
		return fmt.Errorf("initializing registry: %w", err)
	}

	_, found, err := reg.GetProject(cfg.RenameOldName)
	if err != nil {
		return fmt.Errorf("checking project: %w", err)
	}
	if !found {
		return fmt.Errorf("project '%s' not found in registry\n%s run 'daedalus list' to see registered projects", cfg.RenameOldName, color.Cyan("Hint:"))
	}

	// Refuse rename if the project is running
	exec := &executor.RealExecutor{}
	d := docker.NewDocker(exec, filepath.Join(cfg.ScriptDir, "docker-compose.yml"))
	containerName := "claude-run-" + cfg.RenameOldName
	running, err := d.IsContainerRunning(containerName)
	if err != nil {
		return fmt.Errorf("checking container status: %w", err)
	}
	if running {
		return fmt.Errorf("project '%s' is running — stop it before renaming", cfg.RenameOldName)
	}

	if err := reg.RenameProject(cfg.RenameOldName, cfg.RenameNewName); err != nil {
		return fmt.Errorf("renaming project: %w", err)
	}

	fmt.Printf("%s '%s' to '%s'.\n", color.Green("Renamed"), cfg.RenameOldName, cfg.RenameNewName)
	return nil
}

// confirmDestructive decides whether a destructive registry edit may proceed,
// and is the single place that answers the question for `prune` and `remove`.
//
// --force WINS OUTRIGHT and never touches stdin. That ordering is the whole fix:
// --force used to be consulted only in the headless branch, which made it
// useless in exactly the case it exists for. The control plane runs
// `daedalus remove daedalus-job-<id> --force` from the daemon, whose children
// get stdin on /dev/null — and /dev/null IS a character device, so IsHeadless
// reported "interactive", the prompt was printed to a log file, Scan() hit EOF
// and the removal aborted. Every Job therefore stranded a `daedalus-job-*`
// registry entry, which is the exact churn that deferred remove exists to
// prevent. A flag that says "no human is present" must not be overridden by a
// heuristic guess about whether one is.
//
// Returns (proceed, err): (false, nil) is a considered decline that already
// printed its reason; an error is an abort the caller surfaces.
func confirmDestructive(cfg *core.Config, prompt string) (bool, error) {
	if cfg.Force {
		return true, nil
	}
	if config.IsHeadless(cfg) {
		fmt.Println("Run with --force to remove in non-interactive mode.")
		return false, nil
	}
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print(prompt)
	if !scanner.Scan() {
		return false, fmt.Errorf("aborted")
	}
	switch strings.TrimSpace(strings.ToLower(scanner.Text())) {
	case "n", "no":
		return false, fmt.Errorf("aborted")
	}
	return true, nil
}

// removeProjects removes named projects from the registry.
func removeProjects(cfg *core.Config) error {
	if len(cfg.RemoveTargets) == 0 {
		return fmt.Errorf("usage: daedalus remove <name> [name...]")
	}

	reg := registry.NewRegistry(cfg.RegistryPath())
	if err := reg.Init(); err != nil {
		return fmt.Errorf("initializing registry: %w", err)
	}

	// Separate out the protected Guild Master: surface it as protected and drop
	// it from the working set so the rest of the batch still proceeds.
	var targets []string
	for _, name := range cfg.RemoveTargets {
		if core.IsGuildMaster(name) {
			fmt.Printf("%s cannot remove the built-in Guild Master — skipping.\n", color.Yellow("Protected:"))
			continue
		}
		targets = append(targets, name)
	}
	if len(targets) == 0 {
		return nil
	}

	// Validate all targets exist before prompting
	for _, name := range targets {
		has, err := reg.HasProject(name)
		if err != nil {
			return fmt.Errorf("checking project '%s': %w", name, err)
		}
		if !has {
			return fmt.Errorf("project '%s' not found in registry\n%s run 'daedalus list' to see registered projects", name, color.Cyan("Hint:"))
		}
	}

	// Confirm removal
	prompt := fmt.Sprintf("Remove %d projects? [Y/n]: ", len(targets))
	if len(targets) == 1 {
		prompt = fmt.Sprintf("Remove project '%s'? [Y/n]: ", targets[0])
	}
	proceed, err := confirmDestructive(cfg, prompt)
	if err != nil {
		return err
	}
	if !proceed {
		return nil
	}

	removed, err := reg.RemoveProjects(targets)
	if err != nil {
		return fmt.Errorf("removing projects: %w", err)
	}
	for _, name := range removed {
		fmt.Printf("%s '%s'.\n", color.Green("Removed"), name)
	}
	return nil
}
