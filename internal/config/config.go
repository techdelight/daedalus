// Copyright (C) 2026 Techdelight BV

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/personas"
	"github.com/techdelight/daedalus/internal/platform"
)

// IsHeadless returns true if running without interactive input.
func IsHeadless(cfg *core.Config) bool {
	if cfg.Prompt != "" {
		return true
	}
	fi, err := os.Stdin.Stat()
	if err != nil {
		return true
	}
	return fi.Mode()&os.ModeCharDevice == 0
}

// ParseArgs parses CLI arguments into a Config.
// Flags can appear in any position; 0/1/2 positional args are accepted.
//
// Subcommand detection:
//   - --help / -h or 0 positional args → Subcommand = "help"
//   - "list" as first positional → Subcommand = "list"
//   - 1 positional arg → ProjectName set, ProjectDir left empty (resolved later)
//   - 2 positional args → ProjectName and ProjectDir set
func ParseArgs(args []string) (*core.Config, error) {
	cfg := &core.Config{
		ImagePrefix: "techdelight/claude-runner",
		Target:      "dev",
	}

	var positional []string
	webHost := "127.0.0.1"
	webPort := "3000"
	hostOverride := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--help", "-h":
			cfg.Subcommand = "help"
			return cfg, nil
		case "--build":
			cfg.Build = true
		case "--target":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--target requires a stage name (dev, godot, base, utils)")
			}
			i++
			cfg.Target = args[i]
			cfg.TargetOverride = true
		case "--resume":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--resume requires a session id")
			}
			i++
			cfg.Resume = args[i]
		case "-p":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("-p requires a prompt string")
			}
			i++
			cfg.Prompt = args[i]
		case "--no-tmux":
			cfg.NoTmux = true
		case "--debug":
			cfg.Debug = true
		case "--dind":
			cfg.DinD = true
		case "--display":
			cfg.Display = true
		case "--force":
			cfg.Force = true
		case "--no-color":
			cfg.NoColor = true
		case "--container-log":
			cfg.ContainerLog = true
		case "--set":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--set requires a key=value pair")
			}
			i++
			if !strings.Contains(args[i], "=") {
				return nil, fmt.Errorf("--set requires format key=value, got %q", args[i])
			}
			cfg.ConfigSet = append(cfg.ConfigSet, args[i])
		case "--unset":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--unset requires a key name")
			}
			i++
			cfg.ConfigUnset = append(cfg.ConfigUnset, args[i])
		case "--port":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--port requires a port number")
			}
			i++
			webPort = args[i]
			port, err := strconv.Atoi(webPort)
			if err != nil || port < 1 || port > 65535 {
				return nil, fmt.Errorf("--port requires a valid port number (1-65535), got %q", webPort)
			}
		case "--host":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--host requires an address")
			}
			i++
			webHost = args[i]
			if strings.TrimSpace(webHost) == "" {
				return nil, fmt.Errorf("--host requires a non-empty address")
			}
			hostOverride = true
		case "--runner":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--runner requires a runner name")
			}
			i++
			cfg.Runner = args[i]
		case "--persona":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--persona requires a persona name")
			}
			i++
			cfg.Persona = args[i]
		case "--agent":
			// Legacy: --agent maps to --runner for backward compat
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--agent requires a name")
			}
			i++
			cfg.Runner = args[i]
		default:
			positional = append(positional, args[i])
		}
	}

	// Resolve script directory (follow symlinks so runtime files are found
	// when the binary is invoked via a symlink, e.g. ~/.local/bin/daedalus)
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cannot determine executable path: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve executable symlink: %w", err)
	}
	cfg.ScriptDir, err = filepath.Abs(filepath.Dir(exe))
	if err != nil {
		return nil, fmt.Errorf("cannot resolve script directory: %w", err)
	}

	// Resolve data directory: env var > config file > default
	if cfg.DataDir == "" {
		cfg.DataDir = os.Getenv("DAEDALUS_DATA_DIR")
	}
	appCfg, err := LoadAppConfig(cfg.ScriptDir)
	if err != nil {
		return nil, err
	}
	core.ApplyAppConfig(cfg, appCfg)
	if cfg.DataDir == "" {
		cfg.DataDir = filepath.Join(cfg.ScriptDir, ".cache")
	}
	if cfg.LogFile == "" {
		cfg.LogFile = filepath.Join(cfg.DataDir, "daedalus.log")
	}

	// Validate --runner against built-in names
	if cfg.Runner != "" {
		if err := validateRunnerName(cfg.Runner, cfg.PersonasDir()); err != nil {
			return nil, err
		}
	}

	// Handle "remove" subcommand — before the positional switch to allow
	// arbitrary number of target names (e.g., daedalus remove a b c).
	if len(positional) > 0 && positional[0] == "remove" {
		cfg.Subcommand = "remove"
		cfg.RemoveTargets = positional[1:]
		return cfg, nil
	}

	// Handle "rename" subcommand (e.g., daedalus rename old-name new-name).
	if len(positional) > 0 && positional[0] == "rename" {
		cfg.Subcommand = "rename"
		if len(positional) > 1 {
			cfg.RenameOldName = positional[1]
		}
		if len(positional) > 2 {
			cfg.RenameNewName = positional[2]
		}
		return cfg, nil
	}

	// Handle "config" subcommand
	if len(positional) > 0 && positional[0] == "config" {
		cfg.Subcommand = "config"
		if len(positional) > 1 {
			cfg.ConfigTarget = positional[1]
		}
		return cfg, nil
	}

	// Handle "completion" subcommand
	if len(positional) > 0 && positional[0] == "completion" {
		cfg.Subcommand = "completion"
		if len(positional) > 1 {
			cfg.CompletionShell = positional[1]
		}
		return cfg, nil
	}

	// Handle "skills" subcommand (e.g., daedalus skills add file.md)
	if len(positional) > 0 && positional[0] == "skills" {
		cfg.Subcommand = "skills"
		cfg.SkillsArgs = positional[1:]
		return cfg, nil
	}

	// Handle "personas" subcommand (e.g., daedalus personas list)
	if len(positional) > 0 && positional[0] == "personas" {
		cfg.Subcommand = "personas"
		cfg.PersonasArgs = positional[1:]
		return cfg, nil
	}

	// Handle positional args
	switch len(positional) {
	case 0:
		if cfg.Build {
			cfg.Subcommand = "build"
			return cfg, nil
		}
		cfg.Subcommand = "help"
		return cfg, nil
	case 1:
		if positional[0] == "list" || positional[0] == "tui" || positional[0] == "web" || positional[0] == "prune" {
			cfg.Subcommand = positional[0]
			if positional[0] == "web" {
				if !hostOverride && platform.IsWSL2() {
					webHost = "0.0.0.0"
					cfg.WSL2Detected = true
				}
				cfg.WebAddr = webHost + ":" + webPort
			}
			return cfg, nil
		}
		cfg.ProjectName = positional[0]
		// ProjectDir left empty — resolved later via registry or cwd
	case 2:
		cfg.ProjectName = positional[0]
		cfg.ProjectDir, err = filepath.Abs(positional[1])
		if err != nil {
			return nil, fmt.Errorf("cannot resolve project directory: %w", err)
		}
	default:
		return nil, fmt.Errorf("too many arguments (expected at most 2, got %d)\n%s run 'daedalus --help' for usage", len(positional), color.Cyan("Hint:"))
	}

	core.NormalizeRunnerTarget(cfg)
	return cfg, nil
}

// validateRunnerName checks whether name is a valid runner — either built-in
// or a user-defined persona in the personas directory.
func validateRunnerName(name, personasDir string) error {
	if core.IsBuiltinRunner(name) {
		return nil
	}
	store := personas.New(personasDir)
	if _, err := store.Read(name); err == nil {
		return nil
	}
	// List user-defined names for error message
	var userNames []string
	configs, _ := store.List()
	for _, c := range configs {
		userNames = append(userNames, c.Name)
	}
	validNames := core.ValidRunnerNames()
	validNames = append(validNames, userNames...)
	return fmt.Errorf("unknown runner %q — valid runners: %s", name, strings.Join(validNames, ", "))
}
