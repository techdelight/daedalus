// Copyright (C) 2026 Techdelight BV

package core

import "testing"

func TestLookupAgent_Claude(t *testing.T) {
	p, ok := LookupAgent("claude")
	if !ok {
		t.Fatal("LookupAgent(claude) ok = false, want true")
	}
	if p.Name != "claude" {
		t.Errorf("Name = %q, want %q", p.Name, "claude")
	}
	if p.BinaryPath != "/opt/claude/bin/claude" {
		t.Errorf("BinaryPath = %q, want %q", p.BinaryPath, "/opt/claude/bin/claude")
	}
	if p.SkipPermsFlag != "--dangerously-skip-permissions" {
		t.Errorf("SkipPermsFlag = %q, want %q", p.SkipPermsFlag, "--dangerously-skip-permissions")
	}
	if p.DebugFlag != "--debug" {
		t.Errorf("DebugFlag = %q, want %q", p.DebugFlag, "--debug")
	}
	if len(p.PromptPrefix) != 2 || p.PromptPrefix[0] != "--print" || p.PromptPrefix[1] != "--verbose" {
		t.Errorf("PromptPrefix = %v, want [--print --verbose]", p.PromptPrefix)
	}
}

func TestLookupAgent_Copilot(t *testing.T) {
	p, ok := LookupAgent("copilot")
	if !ok {
		t.Fatal("LookupAgent(copilot) ok = false, want true")
	}
	if p.Name != "copilot" {
		t.Errorf("Name = %q, want %q", p.Name, "copilot")
	}
	if p.BinaryPath != "/usr/local/bin/copilot" {
		t.Errorf("BinaryPath = %q, want %q", p.BinaryPath, "/usr/local/bin/copilot")
	}
	if p.SkipPermsFlag != "--allow-all" {
		t.Errorf("SkipPermsFlag = %q, want %q", p.SkipPermsFlag, "--allow-all")
	}
	if p.DebugFlag != "" {
		t.Errorf("DebugFlag = %q, want empty", p.DebugFlag)
	}
	if p.PromptPrefix != nil {
		t.Errorf("PromptPrefix = %v, want nil", p.PromptPrefix)
	}
}

func TestLookupAgent_Unknown(t *testing.T) {
	p, ok := LookupAgent("unknown-agent")
	if ok {
		t.Fatal("LookupAgent(unknown-agent) ok = true, want false")
	}
	if p.Name != "claude" {
		t.Errorf("Name = %q, want %q (should default to claude)", p.Name, "claude")
	}
}

func TestValidAgentNames(t *testing.T) {
	names := ValidAgentNames()
	if len(names) != 2 {
		t.Fatalf("len = %d, want 2", len(names))
	}
	found := make(map[string]bool)
	for _, n := range names {
		found[n] = true
	}
	if !found["claude"] {
		t.Error("missing 'claude' in ValidAgentNames")
	}
	if !found["copilot"] {
		t.Error("missing 'copilot' in ValidAgentNames")
	}
}

func TestResolveAgentName_Default(t *testing.T) {
	cfg := &Config{}
	got := ResolveAgentName(cfg)
	if got != "claude" {
		t.Errorf("ResolveAgentName() = %q, want %q", got, "claude")
	}
}

func TestResolveAgentName_Copilot(t *testing.T) {
	cfg := &Config{Agent: "copilot"}
	got := ResolveAgentName(cfg)
	if got != "copilot" {
		t.Errorf("ResolveAgentName() = %q, want %q", got, "copilot")
	}
}

func TestResolveAgentName_Claude(t *testing.T) {
	cfg := &Config{Agent: "claude"}
	got := ResolveAgentName(cfg)
	if got != "claude" {
		t.Errorf("ResolveAgentName() = %q, want %q", got, "claude")
	}
}
