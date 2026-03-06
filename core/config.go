// Copyright (C) 2026 Techdelight BV

package core

import "path/filepath"

// Config holds all parsed CLI configuration.
type Config struct {
	ProjectName     string
	ProjectDir      string
	ScriptDir       string
	DataDir         string   // base directory for registry + per-project caches
	Target          string
	Build           bool
	Resume          string
	Prompt          string
	NoTmux          bool
	Debug           bool
	DinD            bool
	Force           bool
	NoColor         bool
	ImagePrefix     string
	Subcommand      string   // "list", "help", "web", "remove", "config", "completion", or "" for normal mode
	RemoveTargets   []string // project names for "remove" subcommand
	ConfigTarget    string   // project name for "config" subcommand
	ConfigSet       []string // "key=value" pairs for --set
	ConfigUnset     []string // keys for --unset
	CompletionShell string   // shell name for "completion" subcommand
	TargetOverride  bool     // true when --target was explicitly passed
	WebAddr         string   // host:port for web UI server
}

// Image returns the full Docker image tag.
func (c *Config) Image() string {
	return c.ImagePrefix + ":" + c.Target
}

// ContainerName returns the Docker container name for this project.
func (c *Config) ContainerName() string {
	return "claude-run-" + c.ProjectName
}

// TmuxSession returns the tmux session name for this project.
func (c *Config) TmuxSession() string {
	return "claude-" + c.ProjectName
}

// CacheDir returns the per-project cache directory.
func (c *Config) CacheDir() string {
	return filepath.Join(c.DataDir, c.ProjectName)
}

// RegistryPath returns the path to the project registry file.
func (c *Config) RegistryPath() string {
	return filepath.Join(c.DataDir, "projects.json")
}

// UseTmux returns true if tmux should be used for this session.
func (c *Config) UseTmux() bool {
	if c.Prompt != "" || c.NoTmux {
		return false
	}
	return true
}

// ApplyRegistryEntry sets ProjectDir and Target from a registry entry,
// and applies per-project default flags.
// Target is only overwritten if the user did not pass --target explicitly.
func ApplyRegistryEntry(cfg *Config, entry ProjectEntry) {
	cfg.ProjectDir = entry.Directory
	if !cfg.TargetOverride {
		cfg.Target = entry.Target
	}
	applyDefaultFlags(cfg, entry.DefaultFlags)
}

// applyDefaultFlags applies per-project defaults to the config.
// CLI flags always win — defaults only enable flags that are at zero value.
func applyDefaultFlags(cfg *Config, flags map[string]string) {
	for key, val := range flags {
		switch key {
		case "debug":
			if !cfg.Debug {
				cfg.Debug = val == "true"
			}
		case "dind":
			if !cfg.DinD {
				cfg.DinD = val == "true"
			}
		case "no-tmux":
			if !cfg.NoTmux {
				cfg.NoTmux = val == "true"
			}
		}
	}
}
