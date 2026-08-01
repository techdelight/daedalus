// Copyright (C) 2026 Techdelight BV

package core

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

// RunnerVolumeArgs returns the `-v` bind mounts every runner container needs.
// Keeping this in one place is what lets both launch surfaces (the CLI and the
// web coordinator handler) mount the same set — the source of the Backlog #55
// gap, where the coordinator built its `docker compose run` args without these
// mounts.
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
