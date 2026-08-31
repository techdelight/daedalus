// Copyright (C) 2026 Techdelight BV

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/completions"
	"github.com/techdelight/daedalus/internal/config"
	"github.com/techdelight/daedalus/internal/control"
	"github.com/techdelight/daedalus/internal/docker"
	"github.com/techdelight/daedalus/internal/executor"
	"github.com/techdelight/daedalus/internal/logging"
	"github.com/techdelight/daedalus/internal/registry"
	"github.com/techdelight/daedalus/internal/tui"
	"github.com/techdelight/daedalus/internal/web"
)

// Process exit codes. 2 is deliberately left unused (the conventional
// "usage error" slot) so it stays available.
const (
	// exitFailure is the catch-all: something went wrong.
	exitFailure = 1
	// exitRefused means the control plane REFUSED the request as a matter of
	// policy — over budget, attempts exhausted, concurrency exceeded (§6, "the
	// plane can reject"). It is distinct from exitFailure so a script driving
	// `daedalus task` can tell "the plane said no" from "something broke" without
	// parsing prose. The reason itself is on stderr and in `task events`.
	exitRefused = 3
)

func main() {
	color.Init()
	if err := run(os.Args[1:]); err != nil {
		logging.Error(err.Error())
		// The way out gets its own lines (#95 item 2). The plane already puts it in
		// the message, and on one line, run together with the explanation, it was
		// the least likely part to be read — which is precisely backwards for the
		// only part that says what to do next.
		msg, remedies := splitRemedies(err)
		if reason, refused := control.Rejected(err); refused {
			fmt.Fprintf(os.Stderr, "%s %s\n", color.Yellow("Refused:"), msg)
			logging.Info("refused by control-plane policy: " + string(reason))
		} else {
			fmt.Fprintf(os.Stderr, "%s %s\n", color.Red("Error:"), msg)
		}
		for _, line := range remedies {
			fmt.Fprintf(os.Stderr, "  %s %s\n", color.Cyan("→"), line)
		}
		os.Exit(exitCodeFor(err))
	}
}

// splitRemedies separates a refusal's explanation from the ways out it names, so
// the second can be printed one per line under the first.
//
// It splits on the plane's own sentence rather than rebuilding the list from the
// typed error, and the reason is the socket: a 409 state conflict arrives as a
// RemoteError whose remedies survive only when the daemon is new enough to send
// them, while the sentence is in the message either way. Formatting must not be
// the thing that decides whether an operator is told what to do next.
func splitRemedies(err error) (string, []string) {
	msg := err.Error()
	const marker = "From here you can: "
	i := strings.Index(msg, marker)
	if i < 0 {
		return msg, nil
	}
	list := strings.TrimSuffix(strings.TrimSpace(msg[i+len(marker):]), ".")
	var out []string
	for _, part := range strings.Split(list, "; ") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return strings.TrimSpace(msg[:i]), out
}

// exitCodeFor maps an error to a process exit code: a control-plane policy
// refusal gets its own code so callers can distinguish it from a failure.
func exitCodeFor(err error) int {
	if _, refused := control.Rejected(err); refused {
		return exitRefused
	}
	return exitFailure
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

	// Before anything can print: a warning emitted above this line would carry
	// ANSI codes the user explicitly asked not to see.
	if cfg.NoColor {
		color.Disable()
	}

	// Initialize file logging
	if err := logging.Init(cfg.LogFile, cfg.Debug); err != nil {
		fmt.Fprintf(os.Stderr, "%s could not initialize log file: %v\n", color.Yellow("Warning:"), err)
	}
	defer logging.Close()

	logging.Info("starting daedalus version " + core.Version)

	// Ensure the built-in Guild Master exists before any command that enumerates
	// or launches projects runs (list/tui/web/prune/remove and the normal launch
	// flow all fall through this single point). Skipped for the purely
	// informational, registry-free commands so `help`/`completion` stay fast and
	// side-effect-free — including a --help routed to a subcommand, where
	// Subcommand names the command whose usage to print but nothing will run.
	switch {
	case cfg.HelpRequested:
		// printing usage only
	case cfg.Subcommand == "help", cfg.Subcommand == "completion":
		// no registry work
	default:
		ensureGuildMaster(cfg)
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
	case "control":
		logging.Info("subcommand: control")
		return manageControl(cfg)
	case "docs":
		logging.Info("subcommand: docs")
		return manageDocs(cfg)
	case "init":
		logging.Info("subcommand: init")
		return runInit(cfg)
	case "version":
		logging.Info("subcommand: version")
		return manageVersions(cfg)
	case "task":
		logging.Info("subcommand: task")
		return manageTasks(cfg)
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
