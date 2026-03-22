// Copyright (C) 2026 Techdelight BV

package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/catalog"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/completions"
	"github.com/techdelight/daedalus/internal/config"
	"github.com/techdelight/daedalus/internal/docker"
	"github.com/techdelight/daedalus/internal/executor"
	"github.com/techdelight/daedalus/internal/logging"
	"github.com/techdelight/daedalus/internal/platform"
	"github.com/techdelight/daedalus/internal/registry"
	"github.com/techdelight/daedalus/internal/session"
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

	if err := initSkillsCatalog(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "%s could not initialize skill catalog: %v\n", color.Yellow("Warning:"), err)
	}

	// --- tmux session management ---
	useTmux := cfg.UseTmux()

	if useTmux && !session.TmuxAvailable(exec) {
		fmt.Fprintln(os.Stderr, color.Yellow("Warning:")+" tmux not found. Running without session management.")
		fmt.Fprintln(os.Stderr, color.Cyan("Hint:")+" install tmux for detach/reattach support: apt install tmux")
		useTmux = false
	}

	sess := session.NewSession(exec, cfg.TmuxSession())

	if useTmux && sess.Exists() {
		fmt.Printf("Attaching to existing session '%s'...\n", cfg.TmuxSession())
		fmt.Println("  " + color.Dim("(Detach with Ctrl-B d)"))
		return sess.Attach()
	}

	// --- Container duplicate detection ---
	d := docker.NewDocker(exec, filepath.Join(cfg.ScriptDir, "docker-compose.yml"))

	running, err := d.IsContainerRunning(cfg.ContainerName())
	if err != nil {
		return err
	}
	if running {
		return fmt.Errorf("project '%s' is already running (container: %s)\n%s attach with 'daedalus %s' or stop with 'docker stop %s'",
			cfg.ProjectName, cfg.ContainerName(), color.Cyan("Hint:"), cfg.ProjectName, cfg.ContainerName())
	}

	if err := ensureImageBuilt(cfg, d); err != nil {
		return err
	}

	return launchProject(cfg, d, reg, sess, useTmux)
}

// buildAllProjects rebuilds Docker images for all registered projects.
// When --target is explicitly provided, only that target is rebuilt.
// Otherwise, each unique target from the registry is rebuilt.
func buildAllProjects(cfg *core.Config) error {
	exec := &executor.RealExecutor{}
	d := docker.NewDocker(exec, filepath.Join(cfg.ScriptDir, "docker-compose.yml"))

	reg := registry.NewRegistry(cfg.RegistryPath())
	if err := reg.Init(); err != nil {
		return fmt.Errorf("initializing registry: %w", err)
	}

	entries, err := reg.GetProjectEntries()
	if err != nil {
		return fmt.Errorf("reading projects: %w", err)
	}

	if len(entries) == 0 {
		return fmt.Errorf("no registered projects\n%s register a project first with: daedalus <name> <path>", color.Cyan("Hint:"))
	}

	// Collect unique targets to build
	targets := collectBuildTargets(cfg, entries)

	uid := strconv.Itoa(os.Getuid())
	fmt.Printf("Rebuilding %d image(s) for %d registered project(s)...\n\n", len(targets), len(entries))

	checksumPath := filepath.Join(cfg.DataDir, "build-checksum")

	for _, target := range targets {
		image := cfg.ImagePrefix + ":" + target
		if cfg.Debug {
			printBuildDebugInfo(cfg, target, image)
		}
		if err := d.Build(target, image, uid, cfg.ScriptDir); err != nil {
			return fmt.Errorf("building image %s: %w", image, err)
		}
		fmt.Println()
	}

	if err := updateBuildChecksum(cfg.ScriptDir, checksumPath); err != nil {
		fmt.Fprintf(os.Stderr, "%s could not update build checksum: %v\n", color.Yellow("Warning:"), err)
	}

	fmt.Printf("%s all images rebuilt.\n", color.Green("Done:"))
	return nil
}

// ensureImageBuilt builds the Docker image if needed (explicit --build, missing
// image, or changed runtime files). It also updates the build checksum afterward.
func ensureImageBuilt(cfg *core.Config, d *docker.Docker) error {
	image := cfg.Image()
	checksumPath := filepath.Join(cfg.DataDir, "build-checksum")

	if cfg.Build {
		logging.Info("building image: " + image)
		if cfg.Debug {
			printBuildDebugInfo(cfg, cfg.Target, image)
		}
		if err := buildImage(cfg, d, image); err != nil {
			return err
		}
	} else if !d.ImageExists(image) {
		logging.Info("building image: " + image + " (missing)")
		fmt.Printf(color.Yellow("Warning:")+" image %s missing, building...\n", image)
		if err := buildImage(cfg, d, image); err != nil {
			return err
		}
	} else if docker.NeedsRebuild(cfg.ScriptDir, checksumPath) {
		logging.Info("runtime files changed, rebuilding image: " + image)
		fmt.Printf("%s runtime files changed, rebuilding image %s...\n", color.Yellow("Notice:"), image)
		uid := strconv.Itoa(os.Getuid())
		if err := d.Build(cfg.Target, image, uid, cfg.ScriptDir); err != nil {
			logging.Error("auto-rebuild failed: " + err.Error())
			return fmt.Errorf("auto-rebuilding image: %w", err)
		}
	} else {
		return nil
	}

	if err := updateBuildChecksum(cfg.ScriptDir, checksumPath); err != nil {
		fmt.Fprintf(os.Stderr, "%s could not update build checksum: %v\n", color.Yellow("Warning:"), err)
	}
	return nil
}

// buildImage builds the Docker image for the configured target. Used by
// ensureImageBuilt for explicit builds and missing-image builds.
func buildImage(cfg *core.Config, d *docker.Docker, image string) error {
	uid := strconv.Itoa(os.Getuid())
	if err := d.Build(cfg.Target, image, uid, cfg.ScriptDir); err != nil {
		logging.Error("build failed: " + err.Error())
		return fmt.Errorf("building image: %w\n%s check Docker is running and try: daedalus --build %s", err, color.Cyan("Hint:"), cfg.ProjectName)
	}
	return nil
}

// launchProject starts the project container, either in a tmux session or
// directly. It handles session tracking and DinD socket mounting.
func launchProject(cfg *core.Config, d *docker.Docker, reg *registry.Registry, sess *session.Session, useTmux bool) error {
	sessionID, sessionErr := reg.StartSession(cfg.ProjectName, cfg.Resume)
	if sessionErr != nil {
		fmt.Fprintf(os.Stderr, color.Yellow("Warning:")+" failed to start session tracking: %v\n", sessionErr)
	}

	claudeArgs := core.BuildAgentArgs(cfg)
	composeEnv := map[string]string{
		"PROJECT_DIR": cfg.ProjectDir,
		"CACHE_DIR":   cfg.CacheDir(),
		"TARGET":      cfg.Target,
		"AGENT":       core.ResolveAgentName(cfg),
	}

	if cfg.DinD {
		fmt.Fprintln(os.Stderr, color.Yellow("WARNING:")+" --dind mounts the host Docker socket. This grants the container full access to host Docker.")
	}

	var displayArgs []string
	if cfg.Display {
		var displayWarnings []string
		displayArgs, displayWarnings = platform.DisplayArgs(
			os.Getenv("DISPLAY"),
			os.Getenv("WAYLAND_DISPLAY"),
			os.Getenv("XDG_RUNTIME_DIR"),
		)
		for _, w := range displayWarnings {
			fmt.Fprintln(os.Stderr, color.Yellow("Warning:")+" "+w)
		}
	}
	extraArgs := core.BuildExtraArgs(cfg, displayArgs)

	if useTmux {
		dockerCmd := d.ComposeRunCommand(cfg.ContainerName(), claudeArgs, extraArgs)
		tmuxCmd := core.BuildTmuxCommand(cfg, dockerCmd)

		sess.PrintAttachHint(os.Args[0])
		if err := sess.Create(); err != nil {
			return fmt.Errorf("creating tmux session: %w", err)
		}
		if err := sess.SendKeys(tmuxCmd); err != nil {
			return fmt.Errorf("sending command to tmux: %w", err)
		}
		return sess.Attach()
	}

	// Direct execution (no tmux)
	runErr := d.ComposeRun(cfg.ContainerName(), composeEnv, claudeArgs, extraArgs)
	if sessionErr == nil {
		if err := reg.EndSession(cfg.ProjectName, sessionID); err != nil {
			fmt.Fprintf(os.Stderr, color.Yellow("Warning:")+" failed to end session tracking: %v\n", err)
		}
	}
	if runErr != nil {
		logging.Error(runErr.Error())
	} else {
		logging.Info("done")
	}
	return runErr
}

// updateBuildChecksum computes and stores the checksum of build-relevant files.
func updateBuildChecksum(scriptDir, checksumPath string) error {
	content, err := docker.ReadBuildFilesContent(scriptDir)
	if err != nil {
		return fmt.Errorf("reading build files: %w", err)
	}
	checksum := core.ComputeBuildChecksum(content)
	if err := docker.WriteChecksum(checksumPath, checksum); err != nil {
		return fmt.Errorf("writing checksum: %w", err)
	}
	return nil
}

// printBuildDebugInfo prints diagnostic information before a Docker build when
// both --debug and --build are set. It prints resolved paths, target, image,
// and all environment variables sorted alphabetically.
func printBuildDebugInfo(cfg *core.Config, target, image string) {
	fmt.Println(color.Dim("--- Build Debug Info ---"))
	fmt.Printf("  Dockerfile:       %s\n", filepath.Join(cfg.ScriptDir, "Dockerfile"))
	fmt.Printf("  Compose file:     %s\n", filepath.Join(cfg.ScriptDir, "docker-compose.yml"))
	fmt.Printf("  Target:           %s\n", target)
	fmt.Printf("  Image:            %s\n", image)
	fmt.Println()
	fmt.Println(color.Dim("  Environment variables:"))
	envVars := os.Environ()
	sort.Strings(envVars)
	for _, env := range envVars {
		fmt.Printf("    %s\n", env)
	}
	fmt.Println(color.Dim("--- End Build Debug Info ---"))
	fmt.Println()
}

// collectBuildTargets returns the deduplicated, sorted list of targets to build.
// If --target was explicitly set, only that target is returned.
// Otherwise, unique targets are collected from all registered projects.
func collectBuildTargets(cfg *core.Config, entries []core.ProjectInfo) []string {
	if cfg.TargetOverride {
		return []string{cfg.Target}
	}
	seen := make(map[string]bool)
	for _, e := range entries {
		seen[e.Entry.Target] = true
	}
	targets := make([]string, 0, len(seen))
	for t := range seen {
		targets = append(targets, t)
	}
	sort.Strings(targets)
	return targets
}

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
	if cfg.Agent != "" {
		flags["agent"] = cfg.Agent
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
			setMap[parts[0]] = parts[1]
		}
		if err := reg.UpdateDefaultFlags(cfg.ConfigTarget, setMap, cfg.ConfigUnset); err != nil {
			return fmt.Errorf("updating config: %w", err)
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

// printUsage prints the CLI usage message.
func printUsage() {
	fmt.Printf("%s daedalus [flags] <project-name> [project-dir]\n", color.Bold("Usage:"))
	fmt.Println("       daedalus --build")
	fmt.Println("       daedalus list")
	fmt.Println("       daedalus prune")
	fmt.Println("       daedalus remove <name> [name...]")
	fmt.Println("       daedalus rename <old-name> <new-name>")
	fmt.Println("       daedalus config <project-name> [--set key=value] [--unset key]")
	fmt.Println("       daedalus tui")
	fmt.Println("       daedalus web [--port PORT] [--host HOST]")
	fmt.Println("       daedalus skills [add <file> | remove <name> | show <name>]")
	fmt.Println("       daedalus completion <bash|zsh|fish>")
	fmt.Println("       daedalus --help")
	fmt.Println()
	fmt.Println(color.Bold("Commands:"))
	fmt.Println("  <project-name>                Open a registered project (uses stored directory)")
	fmt.Println("  <project-name> <project-dir>  Register and open a new project")
	fmt.Println("  --build                       Rebuild images for all registered projects")
	fmt.Println("  list                          List all registered projects")
	fmt.Println("  prune                         Remove registry entries with missing directories")
	fmt.Println("  remove <name> [name...]       Remove named projects from the registry")
	fmt.Println("  rename <old> <new>            Rename a registered project")
	fmt.Println("  config <name>                 View or edit per-project default flags")
	fmt.Println("  tui                           Interactive dashboard for managing projects")
	fmt.Println("  web                           Web UI dashboard (default: localhost:3000, auto-detects WSL2)")
	fmt.Println("  skills                        List, add, remove, or show skills in the shared catalog")
	fmt.Println("  completion <shell>            Print shell completion script (bash, zsh, fish)")
	fmt.Println()
	fmt.Println(color.Bold("Flags:"))
	fmt.Println("  --build            Force rebuild the Docker image (standalone: rebuild all)")
	fmt.Println("  --target <stage>   Build target: dev (default), godot, base, utils")
	fmt.Println("  --resume <id>      Resume a previous Claude session")
	fmt.Println("  -p <prompt>        Run a headless single-prompt task")
	fmt.Println("  --no-tmux          Run without tmux session wrapping")
	fmt.Println("  --debug            Enable Claude Code debug mode")
	fmt.Println("  --dind             Mount Docker socket (WARNING: grants host Docker access)")
	fmt.Println("  --display          Forward host display (X11/Wayland) into the container")
	fmt.Println("  --force            Force deletion in non-interactive mode (e.g. prune)")
	fmt.Println("  --agent <name>     AI agent: claude (default) or copilot")
	fmt.Println("  --no-color         Disable colored output (also honors NO_COLOR env var)")
	fmt.Println("  --port <port>      Port for web UI (default: 3000)")
	fmt.Println("  --host <host>      Host for web UI (default: 127.0.0.1, 0.0.0.0 on WSL2)")
	fmt.Println("  --help, -h         Show this help message")
	fmt.Println()
	fmt.Println(color.Bold("Examples:"))
	fmt.Println("  daedalus my-app                         Open existing project from registry")
	fmt.Println("  daedalus my-app /path/to/project        Register and open a new project")
	fmt.Println("  daedalus my-app -p \"Fix linting errors\" Run a headless task")
	fmt.Println("  daedalus --build                        Rebuild images for all projects")
	fmt.Println("  daedalus --build --target godot          Rebuild only the godot target image")
	fmt.Println("  daedalus --build --target godot my-game /path/to/game")
	fmt.Println("  daedalus list                           Show all registered projects")
	fmt.Println("  daedalus web --port 8080                Start web UI on port 8080")
	fmt.Println("  daedalus rename my-app my-new-app        Rename a project")
	fmt.Println("  daedalus config my-app --set dind=true  Set per-project default")
	fmt.Println("  daedalus --agent copilot my-app          Use Copilot CLI instead of Claude")
	fmt.Println("  daedalus completion bash                Print bash completion script")
}

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
		fmt.Printf("%-*s  %-*s  %-*s  %-8d  %s\n", nameW, e.Name, dirW, e.Entry.Directory, targetW, e.Entry.Target, len(e.Entry.Sessions), e.Entry.LastUsed)
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

	if !config.IsHeadless(cfg) {
		scanner := bufio.NewScanner(os.Stdin)
		fmt.Print("\nRemove these entries? [Y/n]: ")
		if !scanner.Scan() {
			return fmt.Errorf("aborted")
		}
		reply := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if reply == "n" || reply == "no" {
			return fmt.Errorf("aborted")
		}
	} else if !cfg.Force {
		fmt.Println("Run with --force to remove in non-interactive mode.")
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

// removeProjects removes named projects from the registry.
func removeProjects(cfg *core.Config) error {
	if len(cfg.RemoveTargets) == 0 {
		return fmt.Errorf("usage: daedalus remove <name> [name...]")
	}

	reg := registry.NewRegistry(cfg.RegistryPath())
	if err := reg.Init(); err != nil {
		return fmt.Errorf("initializing registry: %w", err)
	}

	// Validate all targets exist before prompting
	for _, name := range cfg.RemoveTargets {
		has, err := reg.HasProject(name)
		if err != nil {
			return fmt.Errorf("checking project '%s': %w", name, err)
		}
		if !has {
			return fmt.Errorf("project '%s' not found in registry\n%s run 'daedalus list' to see registered projects", name, color.Cyan("Hint:"))
		}
	}

	// Confirm removal
	if !config.IsHeadless(cfg) {
		scanner := bufio.NewScanner(os.Stdin)
		if len(cfg.RemoveTargets) == 1 {
			fmt.Printf("Remove project '%s'? [Y/n]: ", cfg.RemoveTargets[0])
		} else {
			fmt.Printf("Remove %d projects? [Y/n]: ", len(cfg.RemoveTargets))
		}
		if !scanner.Scan() {
			return fmt.Errorf("aborted")
		}
		reply := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if reply == "n" || reply == "no" {
			return fmt.Errorf("aborted")
		}
	} else if !cfg.Force {
		fmt.Println("Run with --force to remove in non-interactive mode.")
		return nil
	}

	removed, err := reg.RemoveProjects(cfg.RemoveTargets)
	if err != nil {
		return fmt.Errorf("removing projects: %w", err)
	}
	for _, name := range removed {
		fmt.Printf("%s '%s'.\n", color.Green("Removed"), name)
	}
	return nil
}

// initSkillsCatalog ensures the shared skill catalog directory exists.
// On first run (directory absent), it seeds it with starter skills.
func initSkillsCatalog(cfg *core.Config) error {
	dir := cfg.SkillsDir()
	if _, err := os.Stat(dir); err == nil {
		return nil // already exists
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating skills directory: %w", err)
	}
	starters, err := core.StarterSkills()
	if err != nil {
		return fmt.Errorf("reading starter skills: %w", err)
	}
	for name, data := range starters {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
			return fmt.Errorf("writing starter skill %s: %w", name, err)
		}
	}
	logging.Info("initialized skill catalog with " + strconv.Itoa(len(starters)) + " starter skills")
	return nil
}

// manageSkills handles the "skills" subcommand for host-side catalog management.
func manageSkills(cfg *core.Config) error {
	if err := initSkillsCatalog(cfg); err != nil {
		return err
	}

	cat := catalog.New(cfg.SkillsDir(), "")
	args := cfg.SkillsArgs

	if len(args) == 0 {
		return listSkills(cat)
	}

	switch args[0] {
	case "add":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus skills add <file.md>")
		}
		return addSkill(cat, args[1])
	case "remove":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus skills remove <name>")
		}
		return removeSkill(cat, args[1])
	case "show":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus skills show <name>")
		}
		return showSkill(cat, args[1])
	default:
		return fmt.Errorf("unknown skills command %q\n%s available: add, remove, show (or no args to list)", args[0], color.Cyan("Hint:"))
	}
}

// listSkills prints all skills in the catalog.
func listSkills(cat *catalog.Catalog) error {
	skills, err := cat.List()
	if err != nil {
		return fmt.Errorf("listing skills: %w", err)
	}
	if len(skills) == 0 {
		fmt.Println("No skills in catalog.")
		return nil
	}
	nameW := 4
	for _, s := range skills {
		if len(s.Name) > nameW {
			nameW = len(s.Name)
		}
	}
	fmt.Printf("%-*s  %s\n", nameW, color.Bold("NAME"), color.Bold("DESCRIPTION"))
	fmt.Printf("%-*s  %s\n", nameW, strings.Repeat("-", nameW), "-----------")
	for _, s := range skills {
		fmt.Printf("%-*s  %s\n", nameW, s.Name, s.Description)
	}
	return nil
}

// addSkill copies a local .md file into the catalog.
func addSkill(cat *catalog.Catalog, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}
	name := strings.TrimSuffix(filepath.Base(path), ".md")
	if err := cat.Create(name, string(data)); err != nil {
		return fmt.Errorf("adding skill: %w", err)
	}
	fmt.Printf("%s skill '%s' added to catalog.\n", color.Green("OK:"), name)
	return nil
}

// removeSkill deletes a skill from the catalog.
func removeSkill(cat *catalog.Catalog, name string) error {
	name = strings.TrimSuffix(name, ".md")
	if err := cat.Remove(name); err != nil {
		return fmt.Errorf("removing skill: %w", err)
	}
	fmt.Printf("%s skill '%s' removed from catalog.\n", color.Green("OK:"), name)
	return nil
}

// showSkill prints a skill's content.
func showSkill(cat *catalog.Catalog, name string) error {
	name = strings.TrimSuffix(name, ".md")
	content, err := cat.Read(name)
	if err != nil {
		return fmt.Errorf("reading skill: %w", err)
	}
	fmt.Print(content)
	return nil
}
