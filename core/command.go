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

// BuildControlSendKeys builds a tmux control-mode `send-keys` command that
// types text into the target pane. Newlines (\n, \r, \r\n) become Enter
// keystrokes so the result is a single line — required because tmux control
// mode is line-delimited and an embedded newline would split the command.
// All non-newline content is sent via `send-keys -l` (literal) so tmux key
// names like "Enter" or "BSpace" embedded in user text are typed verbatim.
func BuildControlSendKeys(target, text string) string {
	norm := strings.ReplaceAll(text, "\r\n", "\n")
	norm = strings.ReplaceAll(norm, "\r", "\n")
	parts := strings.Split(norm, "\n")

	var args []string
	for i, p := range parts {
		if i > 0 {
			args = append(args, "Enter")
		}
		if p != "" {
			args = append(args, "-l", ShellQuote(p))
		}
	}
	if len(args) == 0 {
		args = append(args, "-l", ShellQuote(""))
	}
	return fmt.Sprintf("send-keys -t %s %s", target, strings.Join(args, " "))
}

// BuildRunnerArgs constructs runner CLI arguments from config, using the
// runner profile to determine which flags to emit.
func BuildRunnerArgs(cfg *Config) []string {
	overlay, _ := LookupRunner(ResolveRunnerName(cfg), nil)
	profile := overlay.Runner
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
// Deprecated: use BuildRunnerArgs instead.
func BuildClaudeArgs(cfg *Config) []string {
	return BuildRunnerArgs(cfg)
}

// OverlayPaths holds host paths to files that should be mounted into the
// container for a user-defined persona overlay. The caller is responsible for
// writing the files before calling BuildExtraArgs.
type OverlayPaths struct {
	ClaudeMdPath string            // host path to CLAUDE.md (mounted read-only)
	SettingsPath string            // host path to settings.json (mounted read-only)
	Env          map[string]string // extra environment variables
}

// RunnerVolumeArgs returns the `-v` bind mounts every runner container needs,
// independent of launch path (coordinator daemon or legacy tmux). Keeping this
// in one place is what lets the coordinator path mount the same volumes the
// legacy path always did — the source of the Backlog #55 gap, where the
// coordinator built its `docker compose run` args without these mounts.
//
// It mounts:
//   - the shared skill catalog at /opt/skills
//   - the project's .daedalus progress dir at /workspace/.daedalus
//   - the shared Claude version store (#37) and Maven repo (#21) at their
//     subpaths under the container home (nested mounts, so they are not masked
//     by the ${CACHE_DIR}:/home/claude home mount)
//   - the per-project persistent tools prefix (#27) at /opt/tools
//
// The host directories must exist before the container starts (Docker would
// otherwise create them root-owned); callers create them via their setup step.
func RunnerVolumeArgs(cfg *Config) []string {
	return []string{
		"-v", cfg.SkillsDir() + ":/opt/skills",
		"-v", cfg.ProjectDir + "/.daedalus:/workspace/.daedalus",
		"-v", cfg.SharedClaudeVersionsDir() + ":/home/claude/.local/share/claude/versions",
		"-v", cfg.SharedMavenDir() + ":/home/claude/.m2",
		"-v", cfg.ProjectToolsDir() + ":/opt/tools",
	}
}

// RunnerVolumeHostDirs returns the host-side directories backing
// RunnerVolumeArgs. Callers create these before starting the container so
// Docker does not materialise them root-owned. Pure (no I/O) so it can live
// in core; the mkdir happens at the edge.
func RunnerVolumeHostDirs(cfg *Config) []string {
	return []string{
		cfg.SkillsDir(),
		cfg.ProjectDir + "/.daedalus",
		cfg.SharedClaudeVersionsDir(),
		cfg.SharedMavenDir(),
		cfg.ProjectToolsDir(),
	}
}

// BuildExtraArgs returns extra docker compose run flags derived from the config.
// displayArgs should come from platform.DisplayArgs when cfg.Display is true.
// overlay may be nil when no persona overlay is active.
func BuildExtraArgs(cfg *Config, displayArgs []string, overlay *OverlayPaths) []string {
	args := RunnerVolumeArgs(cfg)

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
		"RUNNER":       ResolveRunnerName(cfg),
	})

	quoted := make([]string, len(dockerCmd))
	for i, arg := range dockerCmd {
		quoted[i] = ShellQuote(arg)
	}
	return "clear && " + exports + " && " + strings.Join(quoted, " ") + "; exit"
}
