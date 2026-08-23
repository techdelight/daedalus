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
	HelpRequested   bool     // --help/-h was given; Subcommand names WHOSE help to print, so this stays a separate signal
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
	ControlArgs     []string // positional args for "control" subcommand (the plane daemon)
	// Container resource limits, per project (#81b). Empty means "whatever
	// docker-compose.yml defaults to" — the values are interpolated into the
	// compose file, so an unset one leaves the shipped default in force.
	//
	// They exist because they were hardcoded, and a project that genuinely needed
	// more had to edit a file the next upgrade replaces. Measured: a review of the
	// snowball project could not run its own Sprint-180 measurement because the
	// model needed ~8.4GB and the container capped at 4GiB.
	MemLimit       string   // docker mem_limit, e.g. "12g"
	CPUs           string   // docker cpus, e.g. "4.0"
	PidsLimit      string   // docker pids_limit, e.g. "1024"
	DocsArgs       []string // positional args for "docs" subcommand
	InitArgs       []string // positional args for "init" subcommand
	VersionArgs    []string // positional args for "version" subcommand
	TaskArgs       []string // positional args for "task" subcommand (control plane)
	TargetOverride bool     // true when --target was explicitly passed
	WebAddr        string   // host:port for web UI server
	WSL2Detected   bool     // true when WSL2 was auto-detected and host defaulted to 0.0.0.0
	LogFile        string   // path to log file for persistent logging
	ContainerLog   bool     // log container output to file
	Auth           bool     // enable token-based authentication for web UI
	AuthToken      string   // access token for web UI authentication
	AuthExpiry     int      // session cookie expiry in hours (default 24)
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

// ComposeProject is the docker-compose project name every invocation runs
// under. It is pinned rather than derived, and that is the whole point.
//
// Compose defaults the project name to the basename of the directory holding
// the compose file. setup.sh installs one full payload per version at
// $PREFIX/versions/<version>/docker-compose.yml, and a dev build's version is a
// timestamp (`dev_20260821134040`), so an UNPINNED project name changes with
// every install — and compose creates a fresh `<project>_default` network for
// each one and never removes it.
//
// Measured on the operator's host, 2026-08-21: 21 orphaned `dev_*_default`
// networks, which with the rest exhausted Docker's default address pools
// (172.16.0.0/12 in /16s plus 192.168.0.0/16 in /20s — roughly 31 bridge
// networks). Every project then failed to start with "all predefined address
// pools have been fully subnetted", and the coordinator retried forever. The
// error named a network and said nothing about daedalus, so it read as a Docker
// problem, or as a problem with whichever project was being opened at the time.
//
// Pinning costs nothing: the container name is passed explicitly with --name,
// so the project name governs only network and volume naming. What it buys is
// that upgrading cannot leak a network, because there is one project for every
// version that will ever be installed.
const ComposeProject = "daedalus"

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

// ControlDBPath returns the path to the host-side control-plane SQLite database
// (Sprint 54 / M13). It sits under the data root alongside the registry so all
// Daedalus state stays in one host-visible place, mirroring RegistryPath().
func (c *Config) ControlDBPath() string {
	return filepath.Join(c.DataDir, "control.db")
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

// GuildMasterDir returns the Daedalus-owned workspace directory for the
// built-in Guild Master project. It lives under the data root (alongside where
// cloned project workspaces are placed, DataDir/projects) so all Daedalus state
// stays in one host-visible place, and — crucially — is kept distinct from the
// Guild Master's per-project cache dir (CacheDir → DataDir/guild-master) so the
// workspace and the container-home mount never collide.
func (c *Config) GuildMasterDir() string {
	return filepath.Join(c.DataDir, "projects", GuildMasterName)
}

// WorktreeGitFilePath is where the container-side `.git` pointer is written for
// a project whose directory is a linked worktree (see gitworktree.go).
//
// It lives under the data root and NOT in the worktree, which is the whole point
// of it being a separate file: everything inside the worktree is part of the tree
// the control plane captures as the Job's artifact, so a pointer written there
// would be staged by `git add -A` and shipped as work the agent never did.
func (c *Config) WorktreeGitFilePath() string {
	return filepath.Join(c.DataDir, "gitfiles", c.ProjectName)
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

// ProjectFlagKeys are the per-project config keys `daedalus config <name> --set`
// understands. Anything else is stored and then silently ignored by
// applyDefaultFlags, which is the failure this list exists to make loud: a typo
// like `memlimit=12g` looks exactly like success until the container starts with
// the old limit.
//
// It must match the switch below, and a test derives the switch's cases from the
// source to check that it does — rather than trusting two lists to be edited
// together.
func ProjectFlagKeys() []string {
	return []string{"debug", "dind", "display", "runner", "persona", "agent",
		"mem-limit", "cpus", "pids-limit"}
}

// IsProjectFlagKey reports whether key is one applyDefaultFlags acts on.
func IsProjectFlagKey(key string) bool {
	for _, k := range ProjectFlagKeys() {
		if k == key {
			return true
		}
	}
	return false
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
		// Container resource limits (#81b). Carried as strings and passed to
		// compose verbatim: the units are docker's (`12g`, `2.0`, `512`), and
		// re-parsing them here would only add a second opinion about a syntax
		// docker already owns.
		case "mem-limit":
			if cfg.MemLimit == "" {
				cfg.MemLimit = val
			}
		case "cpus":
			if cfg.CPUs == "" {
				cfg.CPUs = val
			}
		case "pids-limit":
			if cfg.PidsLimit == "" {
				cfg.PidsLimit = val
			}
		case "agent":
			// Legacy fallback: map "agent" to Runner for backward compat
			if cfg.Runner == "" {
				cfg.Runner = val
			}
		}
	}
}
