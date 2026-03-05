// Copyright (C) 2026 Techdelight BV

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/techdelight/daedalus/core"
)

// isHeadless returns true if running without interactive input.
func isHeadless(cfg *core.Config) bool {
	if cfg.Prompt != "" {
		return true
	}
	fi, err := os.Stdin.Stat()
	if err != nil {
		return true
	}
	return fi.Mode()&os.ModeCharDevice == 0
}

// parseArgs parses CLI arguments into a Config.
// Flags can appear in any position; 0/1/2 positional args are accepted.
//
// Subcommand detection:
//   - --help / -h or 0 positional args → Subcommand = "help"
//   - "list" as first positional → Subcommand = "list"
//   - 1 positional arg → ProjectName set, ProjectDir left empty (resolved later)
//   - 2 positional args → ProjectName and ProjectDir set
func parseArgs(args []string) (*core.Config, error) {
	cfg := &core.Config{
		ImagePrefix: "techdelight/claude-runner",
		Target:      "dev",
	}

	var positional []string
	webHost := "127.0.0.1"
	webPort := "3000"

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
		case "--force":
			cfg.Force = true
		case "--no-color":
			cfg.NoColor = true
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
		default:
			positional = append(positional, args[i])
		}
	}

	// Resolve script directory
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cannot determine executable path: %w", err)
	}
	cfg.ScriptDir, err = filepath.Abs(filepath.Dir(exe))
	if err != nil {
		return nil, fmt.Errorf("cannot resolve script directory: %w", err)
	}

	// Resolve data directory: env var > config file > default
	if cfg.DataDir == "" {
		cfg.DataDir = os.Getenv("DAEDALUS_DATA_DIR")
	}
	appCfg, err := loadAppConfig(cfg.ScriptDir)
	if err != nil {
		return nil, err
	}
	core.ApplyAppConfig(cfg, appCfg)
	if cfg.DataDir == "" {
		cfg.DataDir = filepath.Join(cfg.ScriptDir, ".cache")
	}

	// Resolve claude config dir (needed by all modes including tui)
	cfg.ClaudeConfigDir = os.Getenv("CLAUDE_CONFIG_DIR")
	if cfg.ClaudeConfigDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cannot determine home directory: %w", err)
		}
		cfg.ClaudeConfigDir = filepath.Join(home, ".claude")
	}

	// Handle "remove" subcommand — before the positional switch to allow
	// arbitrary number of target names (e.g., daedalus remove a b c).
	if len(positional) > 0 && positional[0] == "remove" {
		cfg.Subcommand = "remove"
		cfg.RemoveTargets = positional[1:]
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

	// Handle positional args
	switch len(positional) {
	case 0:
		cfg.Subcommand = "help"
		return cfg, nil
	case 1:
		if positional[0] == "list" || positional[0] == "tui" || positional[0] == "web" || positional[0] == "prune" {
			cfg.Subcommand = positional[0]
			if positional[0] == "web" {
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
		return nil, fmt.Errorf("too many arguments (expected at most 2, got %d)\n%s run 'daedalus --help' for usage", len(positional), colorCyan("Hint:"))
	}

	if err := os.MkdirAll(cfg.ClaudeConfigDir, 0755); err != nil {
		return nil, fmt.Errorf("creating claude config directory: %w", err)
	}

	return cfg, nil
}
