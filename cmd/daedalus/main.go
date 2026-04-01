// Copyright (C) 2026 Techdelight BV

package main

import (
	"bufio"
	"encoding/json"
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
	"github.com/techdelight/daedalus/internal/foreman"
	"github.com/techdelight/daedalus/internal/logging"
	"github.com/techdelight/daedalus/internal/mcpclient"
	"github.com/techdelight/daedalus/internal/personas"
	"github.com/techdelight/daedalus/internal/platform"
	"github.com/techdelight/daedalus/internal/programme"
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
	case "personas":
		logging.Info("subcommand: personas")
		return managePersonas(cfg)
	case "runners":
		logging.Info("subcommand: runners")
		return manageRunners(cfg)
	case "programmes":
		logging.Info("subcommand: programmes")
		return manageProgrammes(cfg)
	case "foreman":
		logging.Info("subcommand: foreman")
		return manageForeman(cfg)
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

	// Collect unique (agent, target) build specs
	specs := collectBuildSpecs(cfg, entries)

	uid := strconv.Itoa(os.Getuid())
	fmt.Printf("Rebuilding %d image(s) for %d registered project(s)...\n\n", len(specs), len(entries))

	checksumPath := filepath.Join(cfg.DataDir, "build-checksum")

	for _, spec := range specs {
		if cfg.Debug {
			printBuildDebugInfo(cfg, spec.dockerTarget, spec.imageName)
		}
		if err := d.Build(spec.dockerTarget, spec.imageName, uid, cfg.ScriptDir); err != nil {
			return fmt.Errorf("building image %s: %w", spec.imageName, err)
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
			printBuildDebugInfo(cfg, cfg.BuildTarget(), image)
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
		if err := d.Build(cfg.BuildTarget(), image, uid, cfg.ScriptDir); err != nil {
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
	if err := d.Build(cfg.BuildTarget(), image, uid, cfg.ScriptDir); err != nil {
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

	claudeArgs := core.BuildRunnerArgs(cfg)
	composeEnv := map[string]string{
		"PROJECT_DIR": cfg.ProjectDir,
		"CACHE_DIR":   cfg.CacheDir(),
		"TARGET":      cfg.Target,
		"IMAGE":       cfg.Image(),
		"RUNNER":      core.ResolveRunnerName(cfg),
	}

	if cfg.DinD {
		fmt.Fprintln(os.Stderr, color.Yellow("WARNING:")+" --dind mounts the host Docker socket. This grants the container full access to host Docker.")
	}

	var containerLogPath string
	if cfg.ContainerLog {
		containerLogPath = cfg.ContainerLogPath()
		fmt.Printf("%s container log: %s\n", color.Dim("Log:"), containerLogPath)
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
	overlay, err := resolvePersonaOverlay(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, color.Yellow("Warning:")+" persona overlay: %v\n", err)
	}
	extraArgs := core.BuildExtraArgs(cfg, displayArgs, overlay)

	if useTmux {
		dockerCmd := d.ComposeRunCommand(cfg.ContainerName(), claudeArgs, extraArgs)
		tmuxCmd := core.BuildTmuxCommand(cfg, dockerCmd)

		sess.PrintAttachHint(os.Args[0])
		if err := sess.Create(); err != nil {
			return fmt.Errorf("creating tmux session: %w", err)
		}
		if containerLogPath != "" {
			if err := sess.PipePane(containerLogPath); err != nil {
				return fmt.Errorf("setting up container log pipe: %w", err)
			}
		}
		if err := sess.SendKeys(tmuxCmd); err != nil {
			return fmt.Errorf("sending command to tmux: %w", err)
		}
		return sess.Attach()
	}

	// Direct execution (no tmux)
	runErr := d.ComposeRun(cfg.ContainerName(), composeEnv, claudeArgs, extraArgs, containerLogPath)
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

// buildSpec describes a single Docker image build: the Dockerfile stage name
// and the resulting image tag.
type buildSpec struct {
	dockerTarget string // Dockerfile stage (e.g. "dev", "copilot-dev")
	imageName    string // full image tag (e.g. "techdelight/copilot-runner:dev")
}

// collectBuildSpecs returns the deduplicated, sorted list of images to build.
// If --target was explicitly set, only the current config's spec is returned.
// Otherwise, unique (agent, target) pairs are collected from all registered
// projects to produce the correct Dockerfile stage and image name per agent.
func collectBuildSpecs(cfg *core.Config, entries []core.ProjectInfo) []buildSpec {
	if cfg.TargetOverride {
		return []buildSpec{{
			dockerTarget: cfg.BuildTarget(),
			imageName:    cfg.Image(),
		}}
	}
	seen := make(map[string]bool)
	var specs []buildSpec
	for _, e := range entries {
		runner := e.Entry.DefaultFlags["runner"]
		if runner == "" {
			runner = e.Entry.DefaultFlags["agent"] // legacy fallback
		}
		tmpCfg := &core.Config{
			ImagePrefix: cfg.ImagePrefix,
			Target:      e.Entry.Target,
			Runner:      runner,
		}
		img := tmpCfg.Image()
		if !seen[img] {
			seen[img] = true
			specs = append(specs, buildSpec{
				dockerTarget: tmpCfg.BuildTarget(),
				imageName:    img,
			})
		}
	}
	sort.Slice(specs, func(i, j int) bool {
		return specs[i].imageName < specs[j].imageName
	})
	return specs
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
	fmt.Println("       daedalus runners [list | show <name>]")
	fmt.Println("       daedalus personas [list | show <name> | create <name> | remove <name>]")
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
	fmt.Println("  runners                       List or show built-in runner profiles (claude, copilot)")
	fmt.Println("  personas                      List, show, create, or remove named persona configurations")
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
	fmt.Println("  --runner <name>    AI runner: claude (default) or copilot")
	fmt.Println("  --persona <name>   Named persona configuration to use")
	fmt.Println("  --container-log    Log container output to <data-dir>/<project>/container.log")
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
	fmt.Println("  daedalus --runner copilot my-app          Use Copilot CLI instead of Claude")
	fmt.Println("  daedalus runners                        List built-in runners")
	fmt.Println("  daedalus personas create reviewer       Create a persona configuration")
	fmt.Println("  daedalus --persona reviewer my-app       Use a custom persona")
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

// resolvePersonaOverlay checks if a persona is selected and, if so, writes
// the overlay files to the cache directory and returns the paths for container
// volume mounts. Also sets cfg.Runner to the persona's BaseRunner so the
// correct binary and Docker image are used. Returns nil when no persona is set.
func resolvePersonaOverlay(cfg *core.Config) (*core.OverlayPaths, error) {
	if cfg.Persona == "" {
		return nil, nil
	}
	store := personas.New(cfg.PersonasDir())
	personaCfg, err := store.Read(cfg.Persona)
	if err != nil {
		return nil, fmt.Errorf("reading persona %q: %w", cfg.Persona, err)
	}
	// Set the runner to the persona's base so the correct binary and image are used.
	if cfg.Runner == "" {
		cfg.Runner = personaCfg.BaseRunner
	}

	overlayDir := filepath.Join(cfg.CacheDir(), "persona-overlay")
	if err := os.MkdirAll(overlayDir, 0755); err != nil {
		return nil, fmt.Errorf("creating overlay directory: %w", err)
	}

	var paths core.OverlayPaths
	if personaCfg.ClaudeMd != "" {
		p := filepath.Join(overlayDir, "CLAUDE.md")
		if err := os.WriteFile(p, []byte(personaCfg.ClaudeMd), 0644); err != nil {
			return nil, fmt.Errorf("writing CLAUDE.md overlay: %w", err)
		}
		paths.ClaudeMdPath = p
	}
	if len(personaCfg.Settings) > 0 {
		p := filepath.Join(overlayDir, "settings.json")
		if err := os.WriteFile(p, personaCfg.Settings, 0644); err != nil {
			return nil, fmt.Errorf("writing settings.json overlay: %w", err)
		}
		paths.SettingsPath = p
	}
	if len(personaCfg.Env) > 0 {
		paths.Env = personaCfg.Env
	}

	logging.Info("resolved persona overlay for " + cfg.Persona + " (base: " + personaCfg.BaseRunner + ")")
	return &paths, nil
}

// manageRunners handles the "runners" subcommand for listing and showing
// built-in runner profiles.
func manageRunners(cfg *core.Config) error {
	args := cfg.RunnersArgs

	if len(args) == 0 || args[0] == "list" {
		return listRunners()
	}

	switch args[0] {
	case "show":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus runners show <name>")
		}
		return showRunner(args[1])
	default:
		return fmt.Errorf("unknown runners command %q\n%s available: list, show", args[0], color.Cyan("Hint:"))
	}
}

// listRunners prints all built-in runner profiles.
func listRunners() error {
	nameW := 4
	for _, name := range core.BuiltinRunnerNames() {
		if len(name) > nameW {
			nameW = len(name)
		}
	}
	fmt.Printf("%-*s  %s\n", nameW, color.Bold("NAME"), color.Bold("BINARY"))
	fmt.Printf("%-*s  %s\n", nameW, strings.Repeat("-", nameW), "------")
	for _, name := range core.BuiltinRunnerNames() {
		profile, _ := core.LookupBuiltinRunner(name)
		fmt.Printf("%-*s  %s\n", nameW, name, profile.BinaryPath)
	}
	return nil
}

// showRunner prints the details of a built-in runner profile.
func showRunner(name string) error {
	if !core.IsBuiltinRunner(name) {
		return fmt.Errorf("unknown runner %q — valid runners: %s", name, strings.Join(core.ValidRunnerNames(), ", "))
	}
	profile, _ := core.LookupBuiltinRunner(name)
	fmt.Printf("%s %s\n", color.Bold("Name:"), profile.Name)
	fmt.Printf("%s %s\n", color.Bold("Binary:"), profile.BinaryPath)
	if profile.DebugFlag != "" {
		fmt.Printf("%s %s\n", color.Bold("Debug flag:"), profile.DebugFlag)
	}
	if profile.ResumeFlag != "" {
		fmt.Printf("%s %s\n", color.Bold("Resume flag:"), profile.ResumeFlag)
	}
	return nil
}

// managePersonas handles the "personas" subcommand for managing user-defined
// persona configurations.
func managePersonas(cfg *core.Config) error {
	store := personas.New(cfg.PersonasDir())
	args := cfg.PersonasArgs

	if len(args) == 0 || args[0] == "list" {
		return listPersonas(store)
	}

	switch args[0] {
	case "show":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus personas show <name>")
		}
		return showPersona(store, args[1])
	case "create":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus personas create <name>")
		}
		return createPersona(cfg, store, args[1])
	case "remove":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus personas remove <name>")
		}
		return removePersona(store, args[1])
	default:
		return fmt.Errorf("unknown personas command %q\n%s available: list, show, create, remove", args[0], color.Cyan("Hint:"))
	}
}

// listPersonas prints all user-defined persona configurations.
func listPersonas(store *personas.Store) error {
	configs, err := store.List()
	if err != nil {
		return fmt.Errorf("listing personas: %w", err)
	}

	if len(configs) == 0 {
		fmt.Println("No personas defined. Use 'daedalus personas create <name>' to create one.")
		return nil
	}

	nameW := 4
	baseW := 4
	for _, c := range configs {
		if len(c.Name) > nameW {
			nameW = len(c.Name)
		}
		if len(c.BaseRunner) > baseW {
			baseW = len(c.BaseRunner)
		}
	}

	fmt.Printf("%-*s  %-*s  %s\n", nameW, color.Bold("NAME"), baseW, color.Bold("BASE"), color.Bold("DESCRIPTION"))
	fmt.Printf("%-*s  %-*s  %s\n", nameW, strings.Repeat("-", nameW), baseW, strings.Repeat("-", baseW), "-----------")
	for _, c := range configs {
		fmt.Printf("%-*s  %-*s  %s\n", nameW, c.Name, baseW, c.BaseRunner, c.Description)
	}
	return nil
}

// showPersona prints the full JSON configuration for a named persona.
func showPersona(store *personas.Store, name string) error {
	if core.IsBuiltinRunner(name) {
		return fmt.Errorf("%q is a built-in runner, not a persona — use 'daedalus --runner %s' to select it", name, name)
	}
	cfg, err := store.Read(name)
	if err != nil {
		return fmt.Errorf("reading persona: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("formatting persona config: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

// createPersona interactively creates a new persona configuration.
func createPersona(cfg *core.Config, store *personas.Store, name string) error {
	if err := core.ValidatePersonaName(name); err != nil {
		return err
	}

	scanner := bufio.NewScanner(os.Stdin)

	// Prompt for base runner
	fmt.Printf("Base runner (claude, copilot) [claude]: ")
	baseRunner := "claude"
	if scanner.Scan() {
		if text := strings.TrimSpace(scanner.Text()); text != "" {
			baseRunner = text
		}
	}
	if !core.IsBuiltinRunner(baseRunner) {
		return fmt.Errorf("base runner must be a built-in runner (claude, copilot), got %q", baseRunner)
	}

	// Prompt for description
	fmt.Printf("Description: ")
	var description string
	if scanner.Scan() {
		description = strings.TrimSpace(scanner.Text())
	}

	// Prompt for CLAUDE.md content (inline or file path)
	fmt.Printf("CLAUDE.md content (enter text, or @filepath to read from file, or empty to skip):\n> ")
	var claudeMd string
	if scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(text, "@") {
			path := strings.TrimPrefix(text, "@")
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("reading CLAUDE.md file: %w", err)
			}
			claudeMd = string(data)
		} else {
			claudeMd = text
		}
	}

	personaCfg := core.PersonaConfig{
		Name:        name,
		Description: description,
		BaseRunner:  baseRunner,
		ClaudeMd:    claudeMd,
	}

	if err := os.MkdirAll(cfg.PersonasDir(), 0755); err != nil {
		return fmt.Errorf("creating personas directory: %w", err)
	}
	if err := store.Create(personaCfg); err != nil {
		return fmt.Errorf("creating persona: %w", err)
	}
	fmt.Printf("%s persona '%s' created (base: %s).\n", color.Green("OK:"), name, baseRunner)
	return nil
}

// removePersona deletes a user-defined persona configuration.
func removePersona(store *personas.Store, name string) error {
	if core.IsBuiltinRunner(name) {
		return fmt.Errorf("cannot remove built-in runner %q", name)
	}
	if err := store.Remove(name); err != nil {
		return fmt.Errorf("removing persona: %w", err)
	}
	fmt.Printf("%s persona '%s' removed.\n", color.Green("OK:"), name)
	return nil
}

// manageProgrammes handles the "programmes" subcommand for managing
// multi-project programme definitions.
func manageProgrammes(cfg *core.Config) error {
	store := programme.New(cfg.ProgrammesDir())
	args := cfg.ProgrammesArgs

	if len(args) == 0 || args[0] == "list" {
		return listProgrammes(store)
	}

	switch args[0] {
	case "show":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus programmes show <name>")
		}
		reg := registry.NewRegistry(cfg.RegistryPath())
		if err := reg.Init(); err != nil {
			return fmt.Errorf("initializing registry: %w", err)
		}
		client := mcpclient.New()
		return showProgramme(store, args[1], reg, client)
	case "create":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus programmes create <name>")
		}
		return createProgramme(store, args[1])
	case "add-project":
		if len(args) < 3 {
			return fmt.Errorf("usage: daedalus programmes add-project <programme> <project>")
		}
		return addProjectToProgramme(store, args[1], args[2])
	case "add-dep":
		if len(args) < 4 {
			return fmt.Errorf("usage: daedalus programmes add-dep <programme> <upstream> <downstream>")
		}
		return addDepToProgramme(store, args[1], args[2], args[3])
	case "remove":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus programmes remove <name>")
		}
		return removeProgramme(store, args[1])
	case "cascade":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus programmes cascade <programme> [--dry-run]")
		}
		progName := args[1]
		dryRun := len(args) >= 3 && args[2] == "--dry-run"
		return cascadeProgramme(store, progName, dryRun)
	default:
		return fmt.Errorf("unknown programmes command %q\n%s available: list, show, create, add-project, add-dep, remove, cascade", args[0], color.Cyan("Hint:"))
	}
}

// listProgrammes prints all programme definitions in a table.
func listProgrammes(store *programme.Store) error {
	progs, err := store.List()
	if err != nil {
		return fmt.Errorf("listing programmes: %w", err)
	}

	if len(progs) == 0 {
		fmt.Println("No programmes defined. Use 'daedalus programmes create <name>' to create one.")
		return nil
	}

	nameW := 4
	for _, p := range progs {
		if len(p.Name) > nameW {
			nameW = len(p.Name)
		}
	}

	fmt.Printf("%-*s  %-10s  %s\n", nameW, color.Bold("NAME"), color.Bold("PROJECTS"), color.Bold("DEPS"))
	fmt.Printf("%-*s  %-10s  %s\n", nameW, strings.Repeat("-", nameW), "--------", "----")
	for _, p := range progs {
		fmt.Printf("%-*s  %-10d  %d\n", nameW, p.Name, len(p.Projects), len(p.Deps))
	}
	return nil
}

// showProgramme prints programme details with per-project progress aggregation.
func showProgramme(store *programme.Store, name string, reg *registry.Registry, client *mcpclient.Client) error {
	p, err := store.Read(name)
	if err != nil {
		return fmt.Errorf("reading programme: %w", err)
	}

	fmt.Printf("%s  %s\n", color.Bold("Programme:"), p.Name)
	if p.Description != "" {
		fmt.Printf("%s  %s\n", color.Bold("Description:"), p.Description)
	}
	fmt.Printf("%s  %d\n", color.Bold("Projects:"), len(p.Projects))
	fmt.Printf("%s  %d\n\n", color.Bold("Dependencies:"), len(p.Deps))

	if len(p.Deps) > 0 {
		fmt.Println(color.Bold("Dependency Graph:"))
		for _, d := range p.Deps {
			fmt.Printf("  %s → %s\n", d.Upstream, d.Downstream)
		}
		fmt.Println()
	}

	if len(p.Projects) > 0 && reg != nil && client != nil {
		fmt.Println(color.Bold("Project Status:"))
		fmt.Printf("  %-20s  %-8s  %-12s  %s\n", "NAME", "PROGRESS", "VERSION", "SPRINT")
		fmt.Printf("  %-20s  %-8s  %-12s  %s\n", "----", "--------", "-------", "------")
		for _, projName := range p.Projects {
			entry, found, _ := reg.GetProject(projName)
			if !found {
				fmt.Printf("  %-20s  %-8s  %-12s  %s\n", projName, "?", "?", "(not registered)")
				continue
			}
			status, _ := client.GetProjectStatus(projName, entry.Directory)
			pct := fmt.Sprintf("%d%%", status.ProgressPct)
			ver := status.ProjectVersion
			if ver == "" {
				ver = "—"
			}
			sprint := "—"
			if status.CurrentSprint != nil {
				sprint = fmt.Sprintf("Sprint %d", status.CurrentSprint.Number)
			}
			fmt.Printf("  %-20s  %-8s  %-12s  %s\n", projName, pct, ver, sprint)
		}
	}

	return nil
}

// createProgramme creates a new empty programme.
func createProgramme(store *programme.Store, name string) error {
	p := core.Programme{
		Name:     name,
		Projects: []string{},
	}
	if err := store.Create(p); err != nil {
		return fmt.Errorf("creating programme: %w", err)
	}
	fmt.Printf("%s programme '%s' created.\n", color.Green("OK:"), name)
	return nil
}

// addProjectToProgramme adds a project to a programme.
func addProjectToProgramme(store *programme.Store, programmeName, projectName string) error {
	if err := store.AddProject(programmeName, projectName); err != nil {
		return fmt.Errorf("adding project: %w", err)
	}
	fmt.Printf("%s project '%s' added to programme '%s'.\n", color.Green("OK:"), projectName, programmeName)
	return nil
}

// addDepToProgramme adds a dependency edge to a programme.
func addDepToProgramme(store *programme.Store, programmeName, upstream, downstream string) error {
	if err := store.AddDep(programmeName, upstream, downstream); err != nil {
		return fmt.Errorf("adding dependency: %w", err)
	}
	fmt.Printf("%s dependency %s → %s added to programme '%s'.\n", color.Green("OK:"), upstream, downstream, programmeName)
	return nil
}

// removeProgramme deletes a programme by name.
func removeProgramme(store *programme.Store, name string) error {
	if err := store.Remove(name); err != nil {
		return fmt.Errorf("removing programme: %w", err)
	}
	fmt.Printf("%s programme '%s' removed.\n", color.Green("OK:"), name)
	return nil
}

// cascadeProgramme evaluates cascade propagation for a programme and prints the results.
func cascadeProgramme(store *programme.Store, name string, dryRun bool) error {
	p, err := store.Read(name)
	if err != nil {
		return fmt.Errorf("reading programme: %w", err)
	}

	if len(p.Deps) == 0 {
		fmt.Println("No dependencies defined — nothing to cascade.")
		return nil
	}

	if dryRun {
		fmt.Printf("%s Cascade dry-run for programme %q\n\n", color.Yellow("DRY RUN:"), name)
	}

	// Evaluate cascade for each project (simulate all completing)
	seen := make(map[string]bool)
	for _, proj := range p.Projects {
		result := foreman.EvaluateCascade(p, proj, dryRun)
		for _, event := range result.Events {
			key := event.Upstream + "\u2192" + event.Downstream
			if seen[key] {
				continue
			}
			seen[key] = true
			actionColor := color.Green
			if event.Action == "skip" {
				actionColor = color.Dim
			} else if event.Action == "notify" {
				actionColor = color.Yellow
			}
			fmt.Printf("  %s  %s \u2192 %s  [%s]\n", actionColor(event.Action), event.Upstream, event.Downstream, string(event.Strategy))
			fmt.Printf("         %s\n", event.Message)
		}
	}

	if dryRun {
		fmt.Printf("\n%s No changes made.\n", color.Yellow("DRY RUN:"))
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
		skillName := strings.TrimSuffix(name, ".md")
		skillDir := filepath.Join(dir, skillName)
		if err := os.MkdirAll(skillDir, 0755); err != nil {
			return fmt.Errorf("creating skill directory %s: %w", skillName, err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), data, 0644); err != nil {
			return fmt.Errorf("writing starter skill %s: %w", skillName, err)
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

// manageForeman handles the "foreman" subcommand.
func manageForeman(cfg *core.Config) error {
	args := cfg.ForemanArgs
	if len(args) == 0 {
		return fmt.Errorf("usage: daedalus foreman <start|stop|status>\n%s available: start, stop, status", color.Cyan("Hint:"))
	}
	switch args[0] {
	case "status":
		return foremanStatus(cfg)
	case "start":
		return foremanStart(cfg)
	case "stop":
		fmt.Println("Foreman stop requires a running web server. Use the Web UI or API.")
		return nil
	default:
		return fmt.Errorf("unknown foreman command %q\n%s available: start, stop, status", args[0], color.Cyan("Hint:"))
	}
}

func foremanStatus(cfg *core.Config) error {
	fmt.Println("Foreman status is available via the Web UI at /api/foreman/status")
	fmt.Println("The Foreman runs as a background process inside 'daedalus web'.")
	return nil
}

func foremanStart(cfg *core.Config) error {
	fmt.Println("The Foreman runs inside 'daedalus web'. Start the web server to use the Foreman:")
	fmt.Println("  daedalus web")
	fmt.Println()
	fmt.Println("Then manage the Foreman via the Web UI or API:")
	fmt.Println("  POST /api/foreman/start    — start the Foreman for a programme")
	fmt.Println("  POST /api/foreman/stop     — stop the Foreman")
	fmt.Println("  GET  /api/foreman/status   — current state and plan")
	return nil
}
