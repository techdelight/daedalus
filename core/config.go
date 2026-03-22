// Copyright (C) 2026 Techdelight BV

package core

import (
	"path/filepath"
	"strings"
)

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
	Display         bool
	Force           bool
	NoColor         bool
	ImagePrefix     string
	Subcommand      string   // "list", "help", "build", "web", "remove", "rename", "config", "completion", or "" for normal mode
	RemoveTargets   []string // project names for "remove" subcommand
	ConfigTarget    string   // project name for "config" subcommand
	ConfigSet       []string // "key=value" pairs for --set
	ConfigUnset     []string // keys for --unset
	CompletionShell string   // shell name for "completion" subcommand
	RenameOldName   string   // old project name for "rename" subcommand
	RenameNewName   string   // new project name for "rename" subcommand
	Agent           string   // agent name: "claude" (default) or "copilot"
	SkillsArgs      []string // positional args for "skills" subcommand
	TargetOverride  bool     // true when --target was explicitly passed
	WebAddr         string   // host:port for web UI server
	WSL2Detected    bool     // true when WSL2 was auto-detected and host defaulted to 0.0.0.0
	LogFile         string   // path to log file for persistent logging
}

// Image returns the full Docker image tag.
// For non-claude agents, "claude-runner" in the prefix is replaced with
// "<agent>-runner" (e.g. "techdelight/copilot-runner:dev").
func (c *Config) Image() string {
	prefix := c.ImagePrefix
	agent := ResolveAgentName(c)
	if agent != "claude" {
		prefix = strings.Replace(prefix, "claude-runner", agent+"-runner", 1)
	}
	return prefix + ":" + c.Target
}

// BuildTarget returns the Dockerfile stage name for the current agent and
// target. Non-claude agents use prefixed stages (e.g. "copilot-dev").
func (c *Config) BuildTarget() string {
	agent := ResolveAgentName(c)
	if agent != "claude" {
		return agent + "-" + c.Target
	}
	return c.Target
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

// SkillsDir returns the path to the shared skill catalog directory.
func (c *Config) SkillsDir() string {
	return filepath.Join(c.DataDir, "skills")
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
	NormalizeAgentTarget(cfg)
}

// NormalizeAgentTarget detects agent-prefixed targets like "copilot-dev" and
// splits them into Agent="copilot" and Target="dev". Only applies when Agent
// is not already explicitly set.
func NormalizeAgentTarget(cfg *Config) {
	if cfg.Agent != "" {
		return
	}
	for _, name := range ValidAgentNames() {
		if name == "claude" {
			continue
		}
		prefix := name + "-"
		if strings.HasPrefix(cfg.Target, prefix) {
			cfg.Agent = name
			cfg.Target = strings.TrimPrefix(cfg.Target, prefix)
			return
		}
	}
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
		case "display":
			if !cfg.Display {
				cfg.Display = val == "true"
			}
		case "no-tmux":
			if !cfg.NoTmux {
				cfg.NoTmux = val == "true"
			}
		case "agent":
			if cfg.Agent == "" {
				cfg.Agent = val
			}
		}
	}
}
