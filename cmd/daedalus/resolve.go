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
	"github.com/techdelight/daedalus/internal/registry"
)

// resolveProject determines the project name, directory, and target from the
// registry and CLI arguments. It modifies cfg in place.
func resolveProject(cfg *core.Config, reg *registry.Registry) error {
	if cfg.ProjectDir != "" {
		return resolveTwoArgs(cfg, reg)
	}
	return resolveOneArg(cfg, reg)
}

// resolveTwoArgs handles the case where both project name and directory are provided.
func resolveTwoArgs(cfg *core.Config, reg *registry.Registry) error {
	entry, nameFound, err := reg.GetProject(cfg.ProjectName)
	if err != nil {
		return fmt.Errorf("checking project: %w", err)
	}

	dirName, _, dirFound, err := reg.FindProjectByDir(cfg.ProjectDir)
	if err != nil {
		return fmt.Errorf("checking directory: %w", err)
	}

	switch {
	case nameFound && dirFound && dirName == cfg.ProjectName:
		// Both match the same project — open it
		core.ApplyRegistryEntry(cfg, entry)
		if err := reg.TouchProject(cfg.ProjectName); err != nil {
			return fmt.Errorf("updating project timestamp: %w", err)
		}
	case nameFound && entry.Directory != cfg.ProjectDir:
		return fmt.Errorf("project '%s' is already registered with directory '%s' (given: '%s')",
			cfg.ProjectName, entry.Directory, cfg.ProjectDir)
	case dirFound && dirName != cfg.ProjectName:
		return fmt.Errorf("directory '%s' is already used by project '%s' (given name: '%s')",
			cfg.ProjectDir, dirName, cfg.ProjectName)
	default:
		// Neither name nor dir registered — new project
		if err := handleNewProject(cfg, reg); err != nil {
			return err
		}
	}
	return nil
}

// resolveOneArg handles the case where only the project name is provided.
func resolveOneArg(cfg *core.Config, reg *registry.Registry) error {
	// Check if the "name" is actually a GitHub URL
	if repoURL, repoName, ok := parseGitHubURL(cfg.ProjectName); ok {
		cfg.ProjectName = repoName
		// Check if already registered
		entry, found, err := reg.GetProject(repoName)
		if err != nil {
			return fmt.Errorf("checking project: %w", err)
		}
		if found {
			core.ApplyRegistryEntry(cfg, entry)
			if err := reg.TouchProject(repoName); err != nil {
				return fmt.Errorf("updating project timestamp: %w", err)
			}
			return nil
		}
		// Clone into projects directory
		projectsRoot := filepath.Join(cfg.DataDir, "projects")
		cloneDir := filepath.Join(projectsRoot, repoName)
		if err := cloneGitRepo(repoURL, cloneDir); err != nil {
			return err
		}
		cfg.ProjectDir = cloneDir
		return handleNewProject(cfg, reg)
	}

	entry, found, err := reg.GetProject(cfg.ProjectName)
	if err != nil {
		return fmt.Errorf("checking project: %w", err)
	}

	if found {
		core.ApplyRegistryEntry(cfg, entry)
		if err := reg.TouchProject(cfg.ProjectName); err != nil {
			return fmt.Errorf("updating project timestamp: %w", err)
		}
		return nil
	}

	// Name not in registry — use cwd as project directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cannot determine working directory: %w", err)
	}
	cfg.ProjectDir = cwd

	// Check if cwd is used by another project
	dirName, _, dirFound, err := reg.FindProjectByDir(cwd)
	if err != nil {
		return fmt.Errorf("checking directory: %w", err)
	}

	if dirFound {
		return handleDirConflict(cfg, reg, dirName)
	}

	return handleNewProject(cfg, reg)
}

// handleNewProject prompts the user or auto-registers a new project.
func handleNewProject(cfg *core.Config, reg *registry.Registry) error {
	register := func() error {
		if err := reg.AddProject(cfg.ProjectName, cfg.ProjectDir, cfg.Target); err != nil {
			return err
		}
		// Capture non-default flags as per-project defaults
		if flags := collectDefaultFlags(cfg); len(flags) > 0 {
			if err := reg.SetDefaultFlags(cfg.ProjectName, flags); err != nil {
				fmt.Fprintf(os.Stderr, color.Yellow("Warning:")+" failed to save default flags: %v\n", err)
			}
		}
		return nil
	}

	if config.IsHeadless(cfg) {
		fmt.Printf("%s new project '%s'.\n", color.Green("Auto-registering"), cfg.ProjectName)
		return register()
	}

	scanner := bufio.NewScanner(os.Stdin)
	fmt.Printf("\nProject '%s' is not registered.\n", cfg.ProjectName)
	fmt.Printf("  Directory: %s\n", cfg.ProjectDir)
	fmt.Printf("  Target:    %s\n\n", cfg.Target)
	fmt.Printf("Create new project '%s'? [Y/n]: ", cfg.ProjectName)
	if !scanner.Scan() {
		return fmt.Errorf("aborted")
	}
	reply := strings.TrimSpace(strings.ToLower(scanner.Text()))

	switch reply {
	case "n", "no":
		return fmt.Errorf("aborted")
	default:
		fmt.Printf("%s new project '%s'.\n", color.Green("Registering"), cfg.ProjectName)
		if err := register(); err != nil {
			return err
		}
		promptDisplayForwarding(cfg, reg, scanner)
		return nil
	}
}

// promptDisplayForwarding asks the user whether to enable display forwarding
// for a newly registered project. Default is no.
func promptDisplayForwarding(cfg *core.Config, reg *registry.Registry, scanner *bufio.Scanner) {
	fmt.Printf("\nEnable display forwarding (X11/Wayland) for '%s'? [y/N]: ", cfg.ProjectName)
	if !scanner.Scan() {
		return
	}
	reply := strings.TrimSpace(strings.ToLower(scanner.Text()))
	if reply != "y" && reply != "yes" {
		return
	}
	cfg.Display = true
	if err := reg.UpdateDefaultFlags(cfg.ProjectName, map[string]string{"display": "true"}, nil); err != nil {
		fmt.Fprintf(os.Stderr, color.Yellow("Warning:")+" failed to save display setting: %v\n", err)
		return
	}
	fmt.Println(color.Green("Display forwarding enabled."))
}

// collectDefaultFlags returns a map of non-default flag values from the config.
func collectDefaultFlags(cfg *core.Config) map[string]string {
	flags := make(map[string]string)
	if cfg.DinD {
		flags["dind"] = "true"
	}
	if cfg.Display {
		flags["display"] = "true"
	}
	if cfg.Debug {
		flags["debug"] = "true"
	}
	if cfg.NoTmux {
		flags["no-tmux"] = "true"
	}
	if cfg.Runner != "" {
		flags["runner"] = cfg.Runner
	}
	if cfg.Persona != "" {
		flags["persona"] = cfg.Persona
	}
	if len(flags) == 0 {
		return nil
	}
	return flags
}

// handleDirConflict handles the case where the current directory is already
// used by a different project. In interactive mode it offers to open the
// existing project; in headless mode it returns an error.
func handleDirConflict(cfg *core.Config, reg *registry.Registry, existingName string) error {
	if config.IsHeadless(cfg) {
		return fmt.Errorf("directory '%s' is already used by project '%s' (given name: '%s')",
			cfg.ProjectDir, existingName, cfg.ProjectName)
	}

	scanner := bufio.NewScanner(os.Stdin)
	fmt.Printf("\nDirectory '%s' is already used by project '%s'.\n", cfg.ProjectDir, existingName)
	fmt.Printf("Open project '%s' instead? [Y/n]: ", existingName)
	if !scanner.Scan() {
		return fmt.Errorf("aborted")
	}
	reply := strings.TrimSpace(strings.ToLower(scanner.Text()))

	switch reply {
	case "n", "no":
		return fmt.Errorf("aborted")
	default:
		cfg.ProjectName = existingName
		entry, _, err := reg.GetProject(existingName)
		if err != nil {
			return fmt.Errorf("reading project: %w", err)
		}
		core.ApplyRegistryEntry(cfg, entry)
		if err := reg.TouchProject(existingName); err != nil {
			return fmt.Errorf("updating project timestamp: %w", err)
		}
		fmt.Printf("Using project '%s'.\n", existingName)
		return nil
	}
}
