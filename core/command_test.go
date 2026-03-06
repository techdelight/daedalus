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

func TestBuildTmuxCommand_QuotesDockerArgs(t *testing.T) {
	cfg := &Config{
		ProjectName: "my-app",
		ProjectDir:  "/path/with spaces/project",
		ScriptDir:   "/home/user",
		Target:      "dev",
	}
	dockerCmd := []string{"docker", "compose", "-f", "/path/with spaces/compose.yml", "run", "--rm", "claude"}
	result := BuildTmuxCommand(cfg, dockerCmd)

	// Each docker arg should be individually shell-quoted
	if !strings.Contains(result, "'/path/with spaces/compose.yml'") {
		t.Errorf("docker args not quoted, got: %s", result)
	}
	// Env exports should also be quoted
	if !strings.Contains(result, "'/path/with spaces/project'") {
		t.Errorf("env exports not quoted, got: %s", result)
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
