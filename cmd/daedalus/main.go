// Copyright (C) 2026 Techdelight BV

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/completions"
	"github.com/techdelight/daedalus/internal/config"
	"github.com/techdelight/daedalus/internal/docker"
	"github.com/techdelight/daedalus/internal/executor"
	"github.com/techdelight/daedalus/internal/logging"
	"github.com/techdelight/daedalus/internal/registry"
	"github.com/techdelight/daedalus/internal/tui"
	"github.com/techdelight/daedalus/internal/web"
)

func main() {
	color.Init()
	if err := run(os.Args[1:]); err != nil {
		logging.Error(err.Error())
		fmt.Fprintf(os.Stderr, "%s %v\n", color.Red("Error:"), err)
		os.Exit(1)
	}
}

// run is the top-level dispatcher. Subcommand handlers live in topic
// files within this package: build.go, launch.go, resolve.go, clone.go,
// config_cmd.go, list.go, persona.go, runners.go, programmes.go,
// skills.go, usage.go.
func run(args []string) error {
	cfg, err := config.ParseArgs(args)
	if err != nil {
		return err
	}

	// Initialize file logging
	if err := logging.Init(cfg.LogFile, cfg.Debug); err != nil {
		fmt.Fprintf(os.Stderr, "%s could not initialize log file: %v\n", color.Yellow("Warning:"), err)
	}
	defer logging.Close()

	logging.Info("starting daedalus version " + core.Version)

	if cfg.NoColor {
		color.Disable()
	}

	switch cfg.Subcommand {
	case "help":
		printUsage()
		return nil
	case "build":
		logging.Info("subcommand: build")
		return buildAllProjects(cfg)
	case "list":
		logging.Info("subcommand: list")
		return listProjects(cfg)
	case "tui":
		logging.Info("subcommand: tui")
		printBanner(cfg.ScriptDir)
		return tui.Run(cfg)
	case "web":
		logging.Info("subcommand: web")
		printBanner(cfg.ScriptDir)
		return web.Run(cfg)
	case "prune":
		logging.Info("subcommand: prune")
		return pruneProjects(cfg)
	case "remove":
		logging.Info("subcommand: remove")
		return removeProjects(cfg)
	case "rename":
		logging.Info("subcommand: rename")
		return renameProject(cfg)
	case "config":
		logging.Info("subcommand: config")
		return showOrEditConfig(cfg)
	case "completion":
		logging.Info("subcommand: completion")
		return completions.Generate(cfg)
	case "skills":
		logging.Info("subcommand: skills")
		return manageSkills(cfg)
	case "personas":
		logging.Info("subcommand: personas")
		return managePersonas(cfg)
	case "runners":
		logging.Info("subcommand: runners")
		return manageRunners(cfg)
	case "programmes":
		logging.Info("subcommand: programmes")
		return manageProgrammes(cfg)
	case "coordinator":
		logging.Info("subcommand: coordinator")
		return manageCoordinator(cfg)
	case "docs":
		logging.Info("subcommand: docs")
		return manageDocs(cfg)
	case "init":
		logging.Info("subcommand: init")
		return runInit(cfg)
	case "version":
		logging.Info("subcommand: version")
		return manageVersions(cfg)
	}

	// --- Normal project flow ---
	logging.Info("project: " + cfg.ProjectName)
	logging.Debug("config: project-dir=" + cfg.ProjectDir + " target=" + cfg.Target + " data-dir=" + cfg.DataDir + " log-file=" + cfg.LogFile)

	exec := &executor.RealExecutor{}

	reg := registry.NewRegistry(cfg.RegistryPath())
	if err := reg.Init(); err != nil {
		return fmt.Errorf("initializing registry: %w", err)
	}

	if err := resolveProject(cfg, reg); err != nil {
		return err
	}

	// Validate project directory exists
	info, err := os.Stat(cfg.ProjectDir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("project directory '%s' does not exist\n%s check the path or re-register with: daedalus <name> <correct-path>", cfg.ProjectDir, color.Cyan("Hint:"))
	}

	if err := docker.SetupCacheDir(cfg); err != nil {
		return err
	}

	if err := docker.SetupProjectDirs(cfg); err != nil {
		return err
	}

	if err := initSkillsCatalog(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "%s could not initialize skill catalog: %v\n", color.Yellow("Warning:"), err)
	}

	d := docker.NewDocker(exec, filepath.Join(cfg.ScriptDir, "docker-compose.yml"))

	if err := ensureImageBuilt(cfg, d); err != nil {
		return err
	}

	return launchProject(cfg, reg)
}
