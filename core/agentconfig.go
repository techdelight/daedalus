// Copyright (C) 2026 Techdelight BV

package core

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

// AgentConfig describes a user-defined agent persona that layers custom
// system prompts and tool permissions on top of a built-in base agent.
type AgentConfig struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	BaseAgent   string            `json:"baseAgent"`
	ClaudeMd    string            `json:"claudeMd"`
	Settings    json.RawMessage   `json:"settings,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
}

// AgentOverlay combines a built-in AgentProfile with an optional user-defined
// AgentConfig overlay. When Overlay is nil, the agent is a built-in.
type AgentOverlay struct {
	Profile AgentProfile
	Overlay *AgentConfig // nil for built-in agents
}

// BuiltinAgentNames returns the list of hardcoded built-in agent names.
func BuiltinAgentNames() []string {
	return []string{"claude", "copilot"}
}

// IsBuiltinAgent reports whether name is a built-in agent.
func IsBuiltinAgent(name string) bool {
	for _, n := range BuiltinAgentNames() {
		if n == name {
			return true
		}
	}
	return false
}

// ValidateAgentConfigName checks whether name is valid for a user-defined
// agent configuration. It reuses the project name rules and additionally
// rejects names that collide with built-in agents.
func ValidateAgentConfigName(name string) error {
	if err := ValidateProjectName(name); err != nil {
		return fmt.Errorf("invalid agent config name: %w", err)
	}
	if IsBuiltinAgent(name) {
		return fmt.Errorf("agent config name %q conflicts with built-in agent", name)
	}
	return nil
}

// AgentsDir returns the path to the user-defined agent configurations directory.
func (c *Config) AgentsDir() string {
	return filepath.Join(c.DataDir, "agents")
}
