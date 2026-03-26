// Copyright (C) 2026 Techdelight BV

package core

import (
	"strings"
	"testing"
)

func TestBuildClaudeArgs_NoFlags(t *testing.T) {
	cfg := &Config{}
	args := BuildClaudeArgs(cfg)
	if len(args) != 0 {
		t.Errorf("args = %v, want empty slice", args)
	}
}

func TestBuildClaudeArgs_WithDebug(t *testing.T) {
	cfg := &Config{Debug: true}
	args := BuildClaudeArgs(cfg)
	if len(args) != 1 || args[0] != "--debug" {
		t.Errorf("args = %v, want [--debug]", args)
	}
}

func TestBuildClaudeArgs_WithResume(t *testing.T) {
	cfg := &Config{Resume: "abc123"}
	args := BuildClaudeArgs(cfg)
	expected := []string{"--resume", "abc123"}
	if len(args) != len(expected) {
		t.Fatalf("len = %d, want %d", len(args), len(expected))
	}
	for i, a := range expected {
		if args[i] != a {
			t.Errorf("args[%d] = %q, want %q", i, args[i], a)
		}
	}
}

func TestBuildClaudeArgs_WithPrompt(t *testing.T) {
	cfg := &Config{Prompt: "fix bugs"}
	args := BuildClaudeArgs(cfg)
	expected := []string{"--print", "--verbose", "-p", "fix bugs"}
	if len(args) != len(expected) {
		t.Fatalf("len = %d, want %d", len(args), len(expected))
	}
	for i, a := range expected {
		if args[i] != a {
			t.Errorf("args[%d] = %q, want %q", i, args[i], a)
		}
	}
}

func TestBuildAgentArgs_Copilot_NoFlags(t *testing.T) {
	cfg := &Config{Agent: "copilot"}
	args := BuildAgentArgs(cfg)
	if len(args) != 0 {
		t.Errorf("args = %v, want empty slice", args)
	}
}

func TestBuildAgentArgs_Copilot_WithPrompt(t *testing.T) {
	cfg := &Config{Agent: "copilot", Prompt: "fix bugs"}
	args := BuildAgentArgs(cfg)
	// copilot has no prompt prefix, just -p
	expected := []string{"-p", "fix bugs"}
	if len(args) != len(expected) {
		t.Fatalf("len = %d, want %d; args = %v", len(args), len(expected), args)
	}
	for i, a := range expected {
		if args[i] != a {
			t.Errorf("args[%d] = %q, want %q", i, args[i], a)
		}
	}
}

func TestBuildAgentArgs_Copilot_DebugIgnored(t *testing.T) {
	cfg := &Config{Agent: "copilot", Debug: true}
	args := BuildAgentArgs(cfg)
	// copilot has no debug flag, so nothing emitted
	if len(args) != 0 {
		t.Errorf("args = %v, want empty (copilot has no debug flag)", args)
	}
}

func TestBuildAgentArgs_Copilot_WithResume(t *testing.T) {
	cfg := &Config{Agent: "copilot", Resume: "sess-42"}
	args := BuildAgentArgs(cfg)
	expected := []string{"--resume", "sess-42"}
	if len(args) != len(expected) {
		t.Fatalf("len = %d, want %d", len(args), len(expected))
	}
	for i, a := range expected {
		if args[i] != a {
			t.Errorf("args[%d] = %q, want %q", i, args[i], a)
		}
	}
}

func TestBuildAgentArgs_Claude_DefaultBehavior(t *testing.T) {
	// No Agent field set — should behave exactly like original BuildClaudeArgs
	cfg := &Config{Debug: true, Prompt: "fix bugs"}
	args := BuildAgentArgs(cfg)
	expected := []string{"--debug", "--print", "--verbose", "-p", "fix bugs"}
	if len(args) != len(expected) {
		t.Fatalf("len = %d, want %d; args = %v", len(args), len(expected), args)
	}
	for i, a := range expected {
		if args[i] != a {
			t.Errorf("args[%d] = %q, want %q", i, args[i], a)
		}
	}
}

func TestBuildTmuxCommand_IncludesAgentEnv(t *testing.T) {
	cfg := &Config{
		ProjectName: "test",
		ProjectDir:  "/tmp",
		Target:      "dev",
		Agent:       "copilot",
		ImagePrefix: "techdelight/claude-runner",
	}
	dockerCmd := []string{"docker", "compose", "run", "claude"}
	result := BuildTmuxCommand(cfg, dockerCmd)

	if !strings.Contains(result, "AGENT='copilot'") {
		t.Errorf("tmux command should include AGENT='copilot', got: %s", result)
	}
	if !strings.Contains(result, "IMAGE='techdelight/copilot-runner:dev'") {
		t.Errorf("tmux command should include copilot-runner IMAGE, got: %s", result)
	}
}

func TestBuildTmuxCommand_DefaultAgentEnv(t *testing.T) {
	cfg := &Config{
		ProjectName: "test",
		ProjectDir:  "/tmp",
		Target:      "dev",
	}
	dockerCmd := []string{"docker", "compose", "run", "claude"}
	result := BuildTmuxCommand(cfg, dockerCmd)

	if !strings.Contains(result, "AGENT='claude'") {
		t.Errorf("tmux command should include AGENT='claude' by default, got: %s", result)
	}
}

func TestBuildTmuxCommand_QuotesDockerArgs(t *testing.T) {
	cfg := &Config{
		ProjectName: "my-app",
		ProjectDir:  "/path/with spaces/project",
		ScriptDir:   "/home/user",
		Target:      "dev",
	}
	dockerCmd := []string{"docker", "compose", "-f", "/path/with spaces/compose.yml", "run", "--rm", "claude"}
	result := BuildTmuxCommand(cfg, dockerCmd)

	// Command should start with clear to suppress docker command echo
	if !strings.HasPrefix(result, "clear && ") {
		t.Errorf("command should start with 'clear && ', got: %s", result)
	}
	// Each docker arg should be individually shell-quoted
	if !strings.Contains(result, "'/path/with spaces/compose.yml'") {
		t.Errorf("docker args not quoted, got: %s", result)
	}
	// Env exports should also be quoted
	if !strings.Contains(result, "'/path/with spaces/project'") {
		t.Errorf("env exports not quoted, got: %s", result)
	}
}

func TestBuildExtraArgs_AlwaysMountsSkills(t *testing.T) {
	cfg := &Config{DataDir: "/data/daedalus"}
	args := BuildExtraArgs(cfg, nil, nil)
	if len(args) < 2 {
		t.Fatalf("args = %v, want at least 2 elements for skills mount", args)
	}
	if args[0] != "-v" {
		t.Errorf("args[0] = %q, want %q", args[0], "-v")
	}
	want := "/data/daedalus/skills:/opt/skills"
	if args[1] != want {
		t.Errorf("args[1] = %q, want %q", args[1], want)
	}
}

func TestBuildExtraArgs_WithDinD(t *testing.T) {
	cfg := &Config{DataDir: "/data", DinD: true}
	args := BuildExtraArgs(cfg, nil, nil)
	// Should have skills mount (2 args) + DinD mount (2 args)
	if len(args) != 4 {
		t.Fatalf("args = %v, want 4 elements", args)
	}
	if args[2] != "-v" || args[3] != "/var/run/docker.sock:/var/run/docker.sock" {
		t.Errorf("DinD mount not found, got: %v", args[2:])
	}
}

func TestBuildExtraArgs_WithOverlay_ClaudeMd(t *testing.T) {
	cfg := &Config{DataDir: "/data"}
	overlay := &OverlayPaths{ClaudeMdPath: "/tmp/overlay/CLAUDE.md"}
	args := BuildExtraArgs(cfg, nil, overlay)
	// skills mount (2) + CLAUDE.md mount (2)
	if len(args) != 4 {
		t.Fatalf("args = %v, want 4 elements", args)
	}
	if args[2] != "-v" {
		t.Errorf("args[2] = %q, want %q", args[2], "-v")
	}
	want := "/tmp/overlay/CLAUDE.md:/workspace/.claude/CLAUDE.md:ro"
	if args[3] != want {
		t.Errorf("args[3] = %q, want %q", args[3], want)
	}
}

func TestBuildExtraArgs_WithOverlay_Settings(t *testing.T) {
	cfg := &Config{DataDir: "/data"}
	overlay := &OverlayPaths{SettingsPath: "/tmp/overlay/settings.json"}
	args := BuildExtraArgs(cfg, nil, overlay)
	// skills mount (2) + settings mount (2)
	if len(args) != 4 {
		t.Fatalf("args = %v, want 4 elements", args)
	}
	want := "/tmp/overlay/settings.json:/workspace/.claude/settings.json:ro"
	if args[3] != want {
		t.Errorf("args[3] = %q, want %q", args[3], want)
	}
}

func TestBuildExtraArgs_WithOverlay_Env(t *testing.T) {
	cfg := &Config{DataDir: "/data"}
	overlay := &OverlayPaths{Env: map[string]string{"FOO": "bar"}}
	args := BuildExtraArgs(cfg, nil, overlay)
	// skills mount (2) + env (2)
	if len(args) != 4 {
		t.Fatalf("args = %v, want 4 elements", args)
	}
	if args[2] != "-e" {
		t.Errorf("args[2] = %q, want %q", args[2], "-e")
	}
	if args[3] != "FOO=bar" {
		t.Errorf("args[3] = %q, want %q", args[3], "FOO=bar")
	}
}

func TestBuildExtraArgs_WithOverlay_Full(t *testing.T) {
	cfg := &Config{DataDir: "/data"}
	overlay := &OverlayPaths{
		ClaudeMdPath: "/tmp/CLAUDE.md",
		SettingsPath: "/tmp/settings.json",
		Env:          map[string]string{"KEY": "val"},
	}
	args := BuildExtraArgs(cfg, nil, overlay)
	// skills (2) + claudemd (2) + settings (2) + env (2) = 8
	if len(args) != 8 {
		t.Fatalf("args = %v, want 8 elements", args)
	}
}

func TestBuildExtraArgs_NilOverlay(t *testing.T) {
	cfg := &Config{DataDir: "/data"}
	args := BuildExtraArgs(cfg, nil, nil)
	// Only skills mount
	if len(args) != 2 {
		t.Fatalf("args = %v, want 2 elements", args)
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "'simple'"},
		{"with spaces", "'with spaces'"},
		{"it's", "'it'\\''s'"},
		{"", "''"},
	}
	for _, tc := range tests {
		got := ShellQuote(tc.input)
		if got != tc.expected {
			t.Errorf("ShellQuote(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}
