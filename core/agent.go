// Copyright (C) 2026 Techdelight BV

package core

// AgentProfile describes the CLI binary and flags for an AI agent.
type AgentProfile struct {
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

var agentProfiles = map[string]AgentProfile{
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

// LookupAgent returns the agent profile for the given name and whether
// the name was valid. Unknown names return the Claude profile and false.
// If userConfig is non-nil and name is not a built-in, it resolves the
// base agent from the user config and returns an AgentOverlay.
func LookupAgent(name string, userConfig *AgentConfig) (AgentOverlay, bool) {
	if p, ok := agentProfiles[name]; ok {
		return AgentOverlay{Profile: p, Overlay: nil}, true
	}
	if userConfig != nil && userConfig.Name == name {
		base, ok := agentProfiles[userConfig.BaseAgent]
		if !ok {
			base = agentProfiles["claude"]
		}
		return AgentOverlay{Profile: base, Overlay: userConfig}, ok || true
	}
	return AgentOverlay{Profile: agentProfiles["claude"]}, false
}

// LookupBuiltinAgent returns the built-in agent profile for the given name.
// Unknown names return the Claude profile and false.
func LookupBuiltinAgent(name string) (AgentProfile, bool) {
	p, ok := agentProfiles[name]
	if !ok {
		return agentProfiles["claude"], false
	}
	return p, true
}

// ValidAgentNames returns the list of supported agent names, including
// any user-defined names provided.
func ValidAgentNames(userDefined ...string) []string {
	names := []string{"claude", "copilot"}
	names = append(names, userDefined...)
	return names
}

// ResolveAgentName returns the effective agent name from the config,
// defaulting to "claude" when unset.
func ResolveAgentName(cfg *Config) string {
	if cfg.Agent != "" {
		return cfg.Agent
	}
	return "claude"
}
