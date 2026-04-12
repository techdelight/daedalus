// Copyright (C) 2026 Techdelight BV

package hooks

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/techdelight/daedalus/core"
)

func TestGenerateSettings_ClaudeHooks(t *testing.T) {
	// Arrange — use the actual Claude runner profile hooks
	profile, _ := core.LookupBuiltinRunner("claude")
	cfg := profile.ActivityHooks

	// Act
	data, err := GenerateSettings(cfg, "/workspace/.daedalus/activity.json")
	if err != nil {
		t.Fatalf("GenerateSettings: %v", err)
	}

	// Assert — valid JSON
	var parsed settingsJSON
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, string(data))
	}

	// Assert — all 6 hooks present
	expectedHooks := []string{
		"PreToolUse", "PostToolUse", "SubagentStart",
		"Stop", "Notification", "UserPromptSubmit",
	}
	for _, name := range expectedHooks {
		entries, ok := parsed.Hooks[name]
		if !ok {
			t.Errorf("missing hook %q in output", name)
			continue
		}
		if len(entries) != 1 || len(entries[0].Hooks) != 1 {
			t.Errorf("hook %q: unexpected structure", name)
			continue
		}
		if entries[0].Hooks[0].Type != "command" {
			t.Errorf("hook %q: type = %q, want %q", name, entries[0].Hooks[0].Type, "command")
		}
	}
}

func TestGenerateSettings_ActivityFilePlaceholder(t *testing.T) {
	// Arrange
	cfg := core.HookConfig{
		Hooks: map[string]string{
			"Stop": `echo idle > {{.ActivityFile}}`,
		},
	}

	// Act
	data, err := GenerateSettings(cfg, "/workspace/.daedalus/activity.json")
	if err != nil {
		t.Fatalf("GenerateSettings: %v", err)
	}

	// Assert — placeholder replaced
	output := string(data)
	if strings.Contains(output, "{{.ActivityFile}}") {
		t.Error("placeholder was not replaced in output")
	}
	if !strings.Contains(output, "/workspace/.daedalus/activity.json") {
		t.Error("activity file path not found in output")
	}
}

func TestGenerateSettings_StopHookWritesIdle(t *testing.T) {
	// Arrange
	profile, _ := core.LookupBuiltinRunner("claude")
	cfg := profile.ActivityHooks

	// Act
	data, err := GenerateSettings(cfg, "/workspace/.daedalus/activity.json")
	if err != nil {
		t.Fatalf("GenerateSettings: %v", err)
	}

	// Assert — Stop hook command contains idle state
	var parsed settingsJSON
	json.Unmarshal(data, &parsed)
	stopCmd := parsed.Hooks["Stop"][0].Hooks[0].Command
	if !strings.Contains(stopCmd, `"state":"idle"`) {
		t.Errorf("Stop hook should write idle state, got: %s", stopCmd)
	}
}

func TestGenerateSettings_EmptyConfig(t *testing.T) {
	// Arrange
	cfg := core.HookConfig{}

	// Act
	data, err := GenerateSettings(cfg, "/workspace/.daedalus/activity.json")
	if err != nil {
		t.Fatalf("GenerateSettings: %v", err)
	}

	// Assert
	if string(data) != "{}\n" {
		t.Errorf("empty config should produce {}, got: %s", string(data))
	}
}
