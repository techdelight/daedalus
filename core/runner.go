// Copyright (C) 2026 Techdelight BV

package core

// RunnerProfile describes the CLI binary and flags for an AI runner.
type RunnerProfile struct {
	Name          string
	BinaryPath    string
	SkipPermsFlag string   // e.g. "--dangerously-skip-permissions"
	ConfigEnvVar  string   // e.g. "CLAUDE_CONFIG_DIR"
	ConfigDirPath string   // e.g. "/home/claude/.claude-config"
	DebugFlag     string   // empty = not supported
	ResumeFlag    string   // e.g. "--resume"
	PromptPrefix  []string // flags before the prompt (e.g. ["--print", "--verbose"])
	PromptFlag    string   // e.g. "-p"
}

var runnerProfiles = map[string]RunnerProfile{
	"claude": {
		Name:          "claude",
		BinaryPath:    "/opt/claude/bin/claude",
		SkipPermsFlag: "--dangerously-skip-permissions",
		ConfigEnvVar:  "CLAUDE_CONFIG_DIR",
		ConfigDirPath: "/home/claude/.claude-config",
		DebugFlag:     "--debug",
		ResumeFlag:    "--resume",
		PromptPrefix:  []string{"--print", "--verbose"},
		PromptFlag:    "-p",
	},
	"copilot": {
		Name:          "copilot",
		BinaryPath:    "/usr/local/bin/copilot",
		SkipPermsFlag: "--allow-all",
		ConfigEnvVar:  "COPILOT_HOME",
		ConfigDirPath: "/home/claude/.copilot",
		DebugFlag:     "",
		ResumeFlag:    "--resume",
		PromptPrefix:  nil,
		PromptFlag:    "-p",
	},
}

// LookupRunner returns the runner profile for the given name and whether
// the name was valid. Unknown names return the Claude profile and false.
// If userConfig is non-nil and name is not a built-in, it resolves the
// base runner from the user config and returns a PersonaOverlay.
func LookupRunner(name string, userConfig *PersonaConfig) (PersonaOverlay, bool) {
	if p, ok := runnerProfiles[name]; ok {
		return PersonaOverlay{Runner: p, Persona: nil}, true
	}
	if userConfig != nil && userConfig.Name == name {
		base, ok := runnerProfiles[userConfig.BaseRunner]
		if !ok {
			base = runnerProfiles["claude"]
		}
		return PersonaOverlay{Runner: base, Persona: userConfig}, ok || true
	}
	return PersonaOverlay{Runner: runnerProfiles["claude"]}, false
}

// LookupBuiltinRunner returns the built-in runner profile for the given name.
// Unknown names return the Claude profile and false.
func LookupBuiltinRunner(name string) (RunnerProfile, bool) {
	p, ok := runnerProfiles[name]
	if !ok {
		return runnerProfiles["claude"], false
	}
	return p, true
}

// ValidRunnerNames returns the list of supported built-in runner names.
func ValidRunnerNames() []string {
	return []string{"claude", "copilot"}
}

// ResolveRunnerName returns the effective runner name from the config,
// defaulting to "claude" when unset.
func ResolveRunnerName(cfg *Config) string {
	if cfg.Runner != "" {
		return cfg.Runner
	}
	return "claude"
}
