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
	DataDir         string // base directory for registry + per-project caches
	Target          string
	Build           bool
	Resume          string
	Prompt          string
	Debug           bool
	DinD            bool
	Display         bool
	Force           bool
	NoColor         bool
	ImagePrefix     string
	ContainerPrefix string   // docker container name prefix; default DefaultContainerPrefix
	Subcommand      string   // "list", "help", "build", "web", "remove", "rename", "config", "completion", or "" for normal mode
	RemoveTargets   []string // project names for "remove" subcommand
	ConfigTarget    string   // project name for "config" subcommand
	ConfigSet       []string // "key=value" pairs for --set
	ConfigUnset     []string // keys for --unset
	CompletionShell string   // shell name for "completion" subcommand
	RenameOldName   string   // old project name for "rename" subcommand
	RenameNewName   string   // new project name for "rename" subcommand
	Runner          string   // runner name: "claude" (default), "copilot"
	Persona         string   // persona name: user-defined persona configuration
	SkillsArgs      []string // positional args for "skills" subcommand
	PersonasArgs    []string // positional args for "personas" subcommand
	RunnersArgs     []string // positional args for "runners" subcommand
	ProgrammesArgs  []string // positional args for "programmes" subcommand
	CoordinatorArgs []string // positional args for "coordinator" subcommand
	DocsArgs        []string // positional args for "docs" subcommand
	InitArgs        []string // positional args for "init" subcommand
	VersionArgs     []string // positional args for "version" subcommand
	TargetOverride  bool     // true when --target was explicitly passed
	WebAddr         string   // host:port for web UI server
	WSL2Detected    bool     // true when WSL2 was auto-detected and host defaulted to 0.0.0.0
	LogFile         string   // path to log file for persistent logging
	ContainerLog    bool     // log container output to file
	Auth            bool     // enable token-based authentication for web UI
	AuthToken       string   // access token for web UI authentication
	AuthExpiry      int      // session cookie expiry in hours (default 24)
}

// ValidTargets returns the list of valid build target names.
func ValidTargets() []string {
	return []string{"dev", "godot", "base", "utils"}
}

// IsValidTarget returns true if name is a recognised build target.
func IsValidTarget(name string) bool {
	for _, t := range ValidTargets() {
		if t == name {
			return true
		}
	}
	return false
}

// Image returns the full Docker image tag.
// For non-claude runners, "claude-runner" in the prefix is replaced with
// "<runner>-runner" (e.g. "techdelight/copilot-runner:dev").
func (c *Config) Image() string {
	prefix := c.ImagePrefix
	runner := ResolveRunnerName(c)
	if runner != "claude" {
		prefix = strings.Replace(prefix, "claude-runner", runner+"-runner", 1)
	}
	return prefix + ":" + c.Target
}

// BuildTarget returns the Dockerfile stage name for the current runner and
// target. Non-claude runners use prefixed stages (e.g. "copilot-dev").
func (c *Config) BuildTarget() string {
	runner := ResolveRunnerName(c)
	if runner != "claude" {
		return runner + "-" + c.Target
	}
	return c.Target
}

// DefaultContainerPrefix is the prefix prepended to a project name to form
// the docker container name when no override is configured. Parallel test
// installs override this via config.json `container-prefix` so containers
// don't collide with a production install.
const DefaultContainerPrefix = "claude-run-"

// ContainerName returns the Docker container name for this project.
func (c *Config) ContainerName() string {
	return ContainerNameFor(c.ContainerPrefix, c.ProjectName)
}

// ContainerNameFor builds a container name from a prefix and project name,
// substituting DefaultContainerPrefix when prefix is empty. Used by
// callers that have a project name in hand but not a full Config (e.g.
// internal/web and internal/tui handlers).
func ContainerNameFor(prefix, projectName string) string {
	if prefix == "" {
		prefix = DefaultContainerPrefix
	}
	return prefix + projectName
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

// SharedDir returns the root for caches shared across all projects
// (Milestone 5). Kept under DataDir alongside the registry and per-project
// caches so all Daedalus state stays in one host-visible place.
func (c *Config) SharedDir() string {
	return filepath.Join(c.DataDir, "shared")
}

// SharedClaudeVersionsDir returns the host dir backing the shared Claude CLI
// version store (Backlog #37). Bind-mounted at the versions subpath under the
// container home so every project reuses one download instead of N copies.
func (c *Config) SharedClaudeVersionsDir() string {
	return filepath.Join(c.SharedDir(), "claude-versions")
}

// SharedMavenDir returns the host dir backing the shared Maven local
// repository (Backlog #21), bind-mounted at /home/claude/.m2 so artifacts are
// shared across projects — the same way a normal dev machine has one ~/.m2.
func (c *Config) SharedMavenDir() string {
	return filepath.Join(c.SharedDir(), "m2")
}

// ProjectToolsDir returns the per-project persistent tools prefix (Backlog
// #27), bind-mounted at /opt/tools. Deliberately kept OUTSIDE the per-project
// cache dir (CacheDir → /home/claude) so it is not double-mounted under the
// home mount.
func (c *Config) ProjectToolsDir() string {
	return filepath.Join(c.DataDir, "tools", c.ProjectName)
}

// ProgrammesDir returns the path to the programmes directory.
func (c *Config) ProgrammesDir() string {
	return filepath.Join(c.DataDir, "programmes")
}

// ContainerLogPath returns the path to the container log file.
func (c *Config) ContainerLogPath() string {
	return filepath.Join(c.DataDir, c.ProjectName, "container.log")
}

// RunnerSocketPath returns the host-side path of the daedalus-runner Unix
// socket for this project. The path is bind-mounted into the container at
// /home/claude/.daedalus/runner.sock; both sides agree because the parent
// is just CacheDir() with a stable suffix. Used by the CLI runner-attach
// path and the Web runner-mode handler so they stay in sync if the layout
// ever moves.
func (c *Config) RunnerSocketPath() string {
	return filepath.Join(c.CacheDir(), ".daedalus", "runner.sock")
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
	NormalizeRunnerTarget(cfg)
}

// NormalizeRunnerTarget detects runner-prefixed targets like "copilot-dev" and
// splits them into Runner="copilot" and Target="dev". Only applies when Runner
// is not already explicitly set.
func NormalizeRunnerTarget(cfg *Config) {
	if cfg.Runner != "" {
		return
	}
	for _, name := range ValidRunnerNames() {
		if name == "claude" {
			continue
		}
		prefix := name + "-"
		if strings.HasPrefix(cfg.Target, prefix) {
			cfg.Runner = name
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
		case "runner":
			if cfg.Runner == "" {
				cfg.Runner = val
			}
		case "persona":
			if cfg.Persona == "" {
				cfg.Persona = val
			}
		case "agent":
			// Legacy fallback: map "agent" to Runner for backward compat
			if cfg.Runner == "" {
				cfg.Runner = val
			}
		}
	}
}
