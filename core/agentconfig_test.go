// Copyright (C) 2026 Techdelight BV

package core

import "testing"

func TestValidateAgentConfigName_Valid(t *testing.T) {
	names := []string{"reviewer", "code-review", "my.agent", "agent_v2"}
	for _, name := range names {
		if err := ValidateAgentConfigName(name); err != nil {
			t.Errorf("ValidateAgentConfigName(%q) = %v, want nil", name, err)
		}
	}
}

func TestValidateAgentConfigName_Empty(t *testing.T) {
	if err := ValidateAgentConfigName(""); err == nil {
		t.Error("ValidateAgentConfigName(\"\") = nil, want error")
	}
}

func TestValidateAgentConfigName_InvalidChars(t *testing.T) {
	names := []string{"-bad", ".hidden", "my@agent", "a/b"}
	for _, name := range names {
		if err := ValidateAgentConfigName(name); err == nil {
			t.Errorf("ValidateAgentConfigName(%q) = nil, want error", name)
		}
	}
}

func TestValidateAgentConfigName_RejectsBuiltinClaude(t *testing.T) {
	err := ValidateAgentConfigName("claude")
	if err == nil {
		t.Fatal("ValidateAgentConfigName(\"claude\") = nil, want error")
	}
	if got := err.Error(); got != `agent config name "claude" conflicts with built-in agent` {
		t.Errorf("error = %q, want built-in conflict message", got)
	}
}

func TestValidateAgentConfigName_RejectsBuiltinCopilot(t *testing.T) {
	err := ValidateAgentConfigName("copilot")
	if err == nil {
		t.Fatal("ValidateAgentConfigName(\"copilot\") = nil, want error")
	}
}

func TestIsBuiltinAgent(t *testing.T) {
	if !IsBuiltinAgent("claude") {
		t.Error("IsBuiltinAgent(\"claude\") = false, want true")
	}
	if !IsBuiltinAgent("copilot") {
		t.Error("IsBuiltinAgent(\"copilot\") = false, want true")
	}
	if IsBuiltinAgent("reviewer") {
		t.Error("IsBuiltinAgent(\"reviewer\") = true, want false")
	}
}

func TestAgentsDir(t *testing.T) {
	cfg := &Config{DataDir: "/data/daedalus"}
	got := cfg.AgentsDir()
	want := "/data/daedalus/agents"
	if got != want {
		t.Errorf("AgentsDir() = %q, want %q", got, want)
	}
}

func TestBuiltinAgentNames(t *testing.T) {
	names := BuiltinAgentNames()
	if len(names) != 2 {
		t.Fatalf("len = %d, want 2", len(names))
	}
	found := make(map[string]bool)
	for _, n := range names {
		found[n] = true
	}
	if !found["claude"] || !found["copilot"] {
		t.Errorf("BuiltinAgentNames() = %v, want [claude copilot]", names)
	}
}
