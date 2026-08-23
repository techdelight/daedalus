// Copyright (C) 2026 Techdelight BV

package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLookupRunner_Claude(t *testing.T) {
	o, ok := LookupRunner("claude", nil)
	if !ok {
		t.Fatal("LookupRunner(claude) ok = false, want true")
	}
	p := o.Runner
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
	if o.Persona != nil {
		t.Error("Persona should be nil for built-in runner")
	}
}

func TestLookupRunner_Copilot(t *testing.T) {
	o, ok := LookupRunner("copilot", nil)
	if !ok {
		t.Fatal("LookupRunner(copilot) ok = false, want true")
	}
	p := o.Runner
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
	if o.Persona != nil {
		t.Error("Persona should be nil for built-in runner")
	}
}

func TestLookupRunner_Unknown(t *testing.T) {
	o, ok := LookupRunner("unknown-runner", nil)
	if ok {
		t.Fatal("LookupRunner(unknown-runner) ok = true, want false")
	}
	if o.Runner.Name != "claude" {
		t.Errorf("Name = %q, want %q (should default to claude)", o.Runner.Name, "claude")
	}
}

func TestLookupRunner_UserConfig(t *testing.T) {
	userCfg := &PersonaConfig{
		Name:       "reviewer",
		BaseRunner: "claude",
		ClaudeMd:   "You are a code reviewer.",
	}
	o, ok := LookupRunner("reviewer", userCfg)
	if !ok {
		t.Fatal("LookupRunner(reviewer) ok = false, want true")
	}
	if o.Runner.Name != "claude" {
		t.Errorf("Runner.Name = %q, want %q (base runner)", o.Runner.Name, "claude")
	}
	if o.Persona == nil {
		t.Fatal("Persona = nil, want non-nil")
	}
	if o.Persona.Name != "reviewer" {
		t.Errorf("Persona.Name = %q, want %q", o.Persona.Name, "reviewer")
	}
}

func TestLookupRunner_UserConfig_CopilotBase(t *testing.T) {
	userCfg := &PersonaConfig{
		Name:       "tester",
		BaseRunner: "copilot",
	}
	o, ok := LookupRunner("tester", userCfg)
	if !ok {
		t.Fatal("LookupRunner(tester) ok = false, want true")
	}
	if o.Runner.Name != "copilot" {
		t.Errorf("Runner.Name = %q, want %q (base runner)", o.Runner.Name, "copilot")
	}
	if o.Persona == nil {
		t.Fatal("Persona = nil, want non-nil")
	}
}

func TestLookupRunner_BuiltinWinsOverUserConfig(t *testing.T) {
	// Even if a user config is provided, built-in names take priority
	userCfg := &PersonaConfig{Name: "claude", BaseRunner: "copilot"}
	o, ok := LookupRunner("claude", userCfg)
	if !ok {
		t.Fatal("LookupRunner(claude) ok = false, want true")
	}
	if o.Persona != nil {
		t.Error("Persona should be nil — built-in should win")
	}
	if o.Runner.Name != "claude" {
		t.Errorf("Runner.Name = %q, want %q", o.Runner.Name, "claude")
	}
}

func TestValidRunnerNames(t *testing.T) {
	names := ValidRunnerNames()
	if len(names) != 2 {
		t.Fatalf("len = %d, want 2", len(names))
	}
	found := make(map[string]bool)
	for _, n := range names {
		found[n] = true
	}
	if !found["claude"] {
		t.Error("missing 'claude' in ValidRunnerNames")
	}
	if !found["copilot"] {
		t.Error("missing 'copilot' in ValidRunnerNames")
	}
}

func TestLookupBuiltinRunner_Claude(t *testing.T) {
	p, ok := LookupBuiltinRunner("claude")
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if p.Name != "claude" {
		t.Errorf("Name = %q, want %q", p.Name, "claude")
	}
}

func TestLookupBuiltinRunner_Unknown(t *testing.T) {
	p, ok := LookupBuiltinRunner("reviewer")
	if ok {
		t.Fatal("ok = true, want false")
	}
	if p.Name != "claude" {
		t.Errorf("Name = %q, want %q (default)", p.Name, "claude")
	}
}

func TestResolveRunnerName_Default(t *testing.T) {
	cfg := &Config{}
	got := ResolveRunnerName(cfg)
	if got != "claude" {
		t.Errorf("ResolveRunnerName() = %q, want %q", got, "claude")
	}
}

func TestResolveRunnerName_Copilot(t *testing.T) {
	cfg := &Config{Runner: "copilot"}
	got := ResolveRunnerName(cfg)
	if got != "copilot" {
		t.Errorf("ResolveRunnerName() = %q, want %q", got, "copilot")
	}
}

func TestResolveRunnerName_Claude(t *testing.T) {
	cfg := &Config{Runner: "claude"}
	got := ResolveRunnerName(cfg)
	if got != "claude" {
		t.Errorf("ResolveRunnerName() = %q, want %q", got, "claude")
	}
}

func TestClaudeHookConfig(t *testing.T) {
	p, _ := LookupBuiltinRunner("claude")
	hooks := p.ActivityHooks.Hooks

	// Claude should have 6 activity hooks
	expectedHooks := []string{
		"PreToolUse", "PostToolUse", "SubagentStart",
		"Stop", "Notification", "UserPromptSubmit",
	}
	for _, name := range expectedHooks {
		if _, ok := hooks[name]; !ok {
			t.Errorf("missing hook %q in Claude HookConfig", name)
		}
	}
	if len(hooks) != len(expectedHooks) {
		t.Errorf("got %d hooks, want %d", len(hooks), len(expectedHooks))
	}

	// Stop hook must write idle state
	if cmd, ok := hooks["Stop"]; ok {
		if !contains(cmd, `"state":"idle"`) {
			t.Errorf("Stop hook should write idle state, got: %s", cmd)
		}
	}

	// PreToolUse hook must write busy state
	if cmd, ok := hooks["PreToolUse"]; ok {
		if !contains(cmd, `"state":"busy"`) {
			t.Errorf("PreToolUse hook should write busy state, got: %s", cmd)
		}
	}
}

func TestCopilotHookConfig_Empty(t *testing.T) {
	p, _ := LookupBuiltinRunner("copilot")
	if len(p.ActivityHooks.Hooks) != 0 {
		t.Errorf("copilot should have no activity hooks, got %d", len(p.ActivityHooks.Hooks))
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestResourceLimitEnv_OnlySpeaksWhenAsked (#81b).
//
// The safety property is the silence: an unset limit must emit NOTHING, so
// docker-compose.yml's own default stays in force and a project that has
// configured nothing behaves exactly as it did before these existed. Emitting
// an empty value would interpolate `mem_limit:` to nothing and break the file
// for every project at once.
func TestResourceLimitEnv_OnlySpeaksWhenAsked(t *testing.T) {
	if env := ResourceLimitEnv(&Config{}); len(env) != 0 {
		t.Errorf("an unconfigured project must emit nothing, got %v", env)
	}
	if env := ResourceLimitEnv(nil); len(env) != 0 {
		t.Errorf("a nil config must emit nothing, got %v", env)
	}
	// Whitespace is not a value: "   " would interpolate to an empty limit.
	if env := ResourceLimitEnv(&Config{MemLimit: "   "}); len(env) != 0 {
		t.Errorf("whitespace must not count as a limit, got %v", env)
	}

	env := ResourceLimitEnv(&Config{MemLimit: "12g", CPUs: "4.0", PidsLimit: "1024"})
	want := []string{"DAEDALUS_MEM_LIMIT=12g", "DAEDALUS_CPUS=4.0", "DAEDALUS_PIDS_LIMIT=1024"}
	if len(env) != len(want) {
		t.Fatalf("env = %v, want %v", env, want)
	}
	for i := range want {
		if env[i] != want[i] {
			t.Errorf("env[%d] = %q, want %q", i, env[i], want[i])
		}
	}

	// One set, two unset: only the one travels.
	if env := ResourceLimitEnv(&Config{MemLimit: "16g"}); len(env) != 1 || env[0] != "DAEDALUS_MEM_LIMIT=16g" {
		t.Errorf("a single limit must travel alone, got %v", env)
	}
}

// The compose file must actually interpolate what we set, with today's values as
// the fallback — derived from the file rather than restated, so the two cannot
// drift into a limit nobody can change or a default nobody expects.
func TestComposeInterpolatesResourceLimits(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "docker-compose.yml"))
	if err != nil {
		t.Skipf("docker-compose.yml not readable from here: %v", err)
	}
	compose := string(data)
	for _, want := range []string{
		"${DAEDALUS_MEM_LIMIT:-4g}",
		"${DAEDALUS_CPUS:-2.0}",
		"${DAEDALUS_PIDS_LIMIT:-512}",
	} {
		if !strings.Contains(compose, want) {
			t.Errorf("docker-compose.yml does not interpolate %s — the per-project limit "+
				"would be set and silently ignored", want)
		}
	}
}
