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
func LookupAgent(name string) (AgentProfile, bool) {
	p, ok := agentProfiles[name]
	if !ok {
		return agentProfiles["claude"], false
	}
	return p, true
}

// ValidAgentNames returns the list of supported agent names.
func ValidAgentNames() []string {
	return []string{"claude", "copilot"}
}

// ResolveAgentName returns the effective agent name from the config,
// defaulting to "claude" when unset.
func ResolveAgentName(cfg *Config) string {
	if cfg.Agent != "" {
		return cfg.Agent
	}
	return "claude"
}
