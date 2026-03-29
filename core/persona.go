// Copyright (C) 2026 Techdelight BV

package core

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

// PersonaConfig describes a user-defined persona that layers custom
// system prompts and tool permissions on top of a built-in runner.
type PersonaConfig struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	BaseRunner  string            `json:"baseRunner"`
	ClaudeMd    string            `json:"claudeMd"`
	Settings    json.RawMessage   `json:"settings,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
}

// PersonaOverlay combines a built-in RunnerProfile with an optional user-defined
// PersonaConfig overlay. When Persona is nil, the runner is a built-in.
type PersonaOverlay struct {
	Runner  RunnerProfile
	Persona *PersonaConfig // nil for built-in runners
}

// BuiltinRunnerNames returns the list of hardcoded built-in runner names.
func BuiltinRunnerNames() []string {
	return []string{"claude", "copilot"}
}

// IsBuiltinRunner reports whether name is a built-in runner.
func IsBuiltinRunner(name string) bool {
	for _, n := range BuiltinRunnerNames() {
		if n == name {
			return true
		}
	}
	return false
}

// ValidatePersonaName checks whether name is valid for a user-defined
// persona configuration. It reuses the project name rules and additionally
// rejects names that collide with built-in runners.
func ValidatePersonaName(name string) error {
	if err := ValidateProjectName(name); err != nil {
		return fmt.Errorf("invalid persona name: %w", err)
	}
	if IsBuiltinRunner(name) {
		return fmt.Errorf("persona name %q conflicts with built-in runner", name)
	}
	return nil
}

// PersonasDir returns the path to the user-defined persona configurations directory.
func (c *Config) PersonasDir() string {
	return filepath.Join(c.DataDir, "personas")
}
