// Copyright (C) 2026 Techdelight BV

package core

import "testing"

func TestLookupAgent_Claude(t *testing.T) {
	o, ok := LookupAgent("claude", nil)
	if !ok {
		t.Fatal("LookupAgent(claude) ok = false, want true")
	}
	p := o.Profile
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
	if o.Overlay != nil {
		t.Error("Overlay should be nil for built-in agent")
	}
}

func TestLookupAgent_Copilot(t *testing.T) {
	o, ok := LookupAgent("copilot", nil)
	if !ok {
		t.Fatal("LookupAgent(copilot) ok = false, want true")
	}
	p := o.Profile
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
	if o.Overlay != nil {
		t.Error("Overlay should be nil for built-in agent")
	}
}

func TestLookupAgent_Unknown(t *testing.T) {
	o, ok := LookupAgent("unknown-agent", nil)
	if ok {
		t.Fatal("LookupAgent(unknown-agent) ok = true, want false")
	}
	if o.Profile.Name != "claude" {
		t.Errorf("Name = %q, want %q (should default to claude)", o.Profile.Name, "claude")
	}
}

func TestLookupAgent_UserConfig(t *testing.T) {
	userCfg := &AgentConfig{
		Name:      "reviewer",
		BaseAgent: "claude",
		ClaudeMd:  "You are a code reviewer.",
	}
	o, ok := LookupAgent("reviewer", userCfg)
	if !ok {
		t.Fatal("LookupAgent(reviewer) ok = false, want true")
	}
	if o.Profile.Name != "claude" {
		t.Errorf("Profile.Name = %q, want %q (base agent)", o.Profile.Name, "claude")
	}
	if o.Overlay == nil {
		t.Fatal("Overlay = nil, want non-nil")
	}
	if o.Overlay.Name != "reviewer" {
		t.Errorf("Overlay.Name = %q, want %q", o.Overlay.Name, "reviewer")
	}
}

func TestLookupAgent_UserConfig_CopilotBase(t *testing.T) {
	userCfg := &AgentConfig{
		Name:      "tester",
		BaseAgent: "copilot",
	}
	o, ok := LookupAgent("tester", userCfg)
	if !ok {
		t.Fatal("LookupAgent(tester) ok = false, want true")
	}
	if o.Profile.Name != "copilot" {
		t.Errorf("Profile.Name = %q, want %q (base agent)", o.Profile.Name, "copilot")
	}
	if o.Overlay == nil {
		t.Fatal("Overlay = nil, want non-nil")
	}
}

func TestLookupAgent_BuiltinWinsOverUserConfig(t *testing.T) {
	// Even if a user config is provided, built-in names take priority
	userCfg := &AgentConfig{Name: "claude", BaseAgent: "copilot"}
	o, ok := LookupAgent("claude", userCfg)
	if !ok {
		t.Fatal("LookupAgent(claude) ok = false, want true")
	}
	if o.Overlay != nil {
		t.Error("Overlay should be nil — built-in should win")
	}
	if o.Profile.Name != "claude" {
		t.Errorf("Profile.Name = %q, want %q", o.Profile.Name, "claude")
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

func TestValidAgentNames_WithUserDefined(t *testing.T) {
	names := ValidAgentNames("reviewer", "tester")
	if len(names) != 4 {
		t.Fatalf("len = %d, want 4", len(names))
	}
	found := make(map[string]bool)
	for _, n := range names {
		found[n] = true
	}
	for _, want := range []string{"claude", "copilot", "reviewer", "tester"} {
		if !found[want] {
			t.Errorf("missing %q in ValidAgentNames", want)
		}
	}
}

func TestLookupBuiltinAgent_Claude(t *testing.T) {
	p, ok := LookupBuiltinAgent("claude")
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if p.Name != "claude" {
		t.Errorf("Name = %q, want %q", p.Name, "claude")
	}
}

func TestLookupBuiltinAgent_Unknown(t *testing.T) {
	p, ok := LookupBuiltinAgent("reviewer")
	if ok {
		t.Fatal("ok = true, want false")
	}
	if p.Name != "claude" {
		t.Errorf("Name = %q, want %q (default)", p.Name, "claude")
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
