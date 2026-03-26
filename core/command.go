// Copyright (C) 2026 Techdelight BV

package core

import (
	"fmt"
	"strings"
)

// BuildEnvExports builds a shell string that exports environment variables,
// suitable for tmux send-keys.
func BuildEnvExports(vars map[string]string) string {
	parts := []string{}
	for k, v := range vars {
		parts = append(parts, fmt.Sprintf("export %s=%s", k, ShellQuote(v)))
	}
	return strings.Join(parts, " && ")
}

// ShellQuote wraps a string in single quotes for safe shell embedding.
func ShellQuote(s string) string {
	// Replace each ' with '\'' (end quote, escaped quote, start quote)
	escaped := strings.ReplaceAll(s, "'", "'\\''")
	return "'" + escaped + "'"
}

// BuildAgentArgs constructs agent CLI arguments from config, using the
// agent profile to determine which flags to emit.
func BuildAgentArgs(cfg *Config) []string {
	overlay, _ := LookupAgent(ResolveAgentName(cfg), nil)
	profile := overlay.Profile
	var args []string
	if cfg.Debug && profile.DebugFlag != "" {
		args = append(args, profile.DebugFlag)
	}
	if cfg.Resume != "" {
		args = append(args, profile.ResumeFlag, cfg.Resume)
	}
	if cfg.Prompt != "" {
		args = append(args, profile.PromptPrefix...)
		args = append(args, profile.PromptFlag, cfg.Prompt)
	}
	return args
}

// BuildClaudeArgs constructs the Claude CLI arguments from config.
// Deprecated: use BuildAgentArgs instead.
func BuildClaudeArgs(cfg *Config) []string {
	return BuildAgentArgs(cfg)
}

// OverlayPaths holds host paths to files that should be mounted into the
// container for a user-defined agent overlay. The caller is responsible for
// writing the files before calling BuildExtraArgs.
type OverlayPaths struct {
	ClaudeMdPath  string            // host path to CLAUDE.md (mounted read-only)
	SettingsPath  string            // host path to settings.json (mounted read-only)
	Env           map[string]string // extra environment variables
}

// BuildExtraArgs returns extra docker compose run flags derived from the config.
// displayArgs should come from platform.DisplayArgs when cfg.Display is true.
// overlay may be nil when no agent overlay is active.
func BuildExtraArgs(cfg *Config, displayArgs []string, overlay *OverlayPaths) []string {
	var args []string

	// Always mount the shared skill catalog
	args = append(args, "-v", cfg.SkillsDir()+":/opt/skills")

	if cfg.DinD {
		args = append(args, "-v", "/var/run/docker.sock:/var/run/docker.sock")
	}
	if cfg.Display {
		args = append(args, displayArgs...)
	}

	if overlay != nil {
		if overlay.ClaudeMdPath != "" {
			args = append(args, "-v", overlay.ClaudeMdPath+":/workspace/.claude/CLAUDE.md:ro")
		}
		if overlay.SettingsPath != "" {
			args = append(args, "-v", overlay.SettingsPath+":/workspace/.claude/settings.json:ro")
		}
		for k, v := range overlay.Env {
			args = append(args, "-e", k+"="+v)
		}
	}
	return args
}

// BuildTmuxCommand constructs the full command string for tmux send-keys.
// It sets env vars and runs docker compose.
func BuildTmuxCommand(cfg *Config, dockerCmd []string) string {
	exports := BuildEnvExports(map[string]string{
		"PROJECT_NAME": cfg.ProjectName,
		"PROJECT_DIR":  cfg.ProjectDir,
		"CACHE_DIR":    cfg.CacheDir(),
		"TARGET":       cfg.Target,
		"IMAGE":        cfg.Image(),
		"AGENT":        ResolveAgentName(cfg),
	})

	quoted := make([]string, len(dockerCmd))
	for i, arg := range dockerCmd {
		quoted[i] = ShellQuote(arg)
	}
	return "clear && " + exports + " && " + strings.Join(quoted, " ") + "; exit"
}
