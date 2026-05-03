// Copyright (C) 2026 Techdelight BV

package runner

import (
	"reflect"
	"strings"
	"testing"
)

func TestClaudeAdapter_Defaults(t *testing.T) {
	bin, args, env := ClaudeAdapter{}.Command(LaunchOptions{})

	if bin != "/opt/claude/bin/claude" {
		t.Errorf("binary = %q, want %q", bin, "/opt/claude/bin/claude")
	}
	if !reflect.DeepEqual(args, []string{"--dangerously-skip-permissions"}) {
		t.Errorf("args = %v, want [--dangerously-skip-permissions]", args)
	}
	if !reflect.DeepEqual(env, []string{"CLAUDE_CONFIG_DIR=/home/claude/.claude-config"}) {
		t.Errorf("env = %v", env)
	}
}

func TestClaudeAdapter_AllOptions(t *testing.T) {
	_, args, _ := ClaudeAdapter{}.Command(LaunchOptions{
		Debug:  true,
		Resume: "abc123",
		Prompt: "fix the bug",
	})

	want := []string{
		"--dangerously-skip-permissions",
		"--debug",
		"--resume", "abc123",
		"--print", "--verbose", "-p", "fix the bug",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("args = %v\n want %v", args, want)
	}
}

func TestClaudeAdapter_PromptWithoutDebug(t *testing.T) {
	_, args, _ := ClaudeAdapter{}.Command(LaunchOptions{Prompt: "hello"})

	if containsArg(args, "--debug") {
		t.Errorf("args should not contain --debug: %v", args)
	}
	if !containsArg(args, "-p") {
		t.Errorf("args should contain -p: %v", args)
	}
	if !endsWith(args, []string{"-p", "hello"}) {
		t.Errorf("args should end with -p hello: %v", args)
	}
}

func TestCopilotAdapter_Defaults(t *testing.T) {
	bin, args, env := CopilotAdapter{}.Command(LaunchOptions{})

	if bin != "/usr/local/bin/copilot" {
		t.Errorf("binary = %q, want %q", bin, "/usr/local/bin/copilot")
	}
	if !reflect.DeepEqual(args, []string{"--allow-all"}) {
		t.Errorf("args = %v, want [--allow-all]", args)
	}
	if !reflect.DeepEqual(env, []string{"COPILOT_HOME=/home/claude/.copilot"}) {
		t.Errorf("env = %v", env)
	}
}

func TestCopilotAdapter_DebugIgnored(t *testing.T) {
	// Copilot has no --debug today; passing Debug=true must not emit
	// a flag the runner doesn't understand.
	_, args, _ := CopilotAdapter{}.Command(LaunchOptions{Debug: true})

	if containsArg(args, "--debug") {
		t.Errorf("copilot adapter must not emit --debug: %v", args)
	}
}

func TestCopilotAdapter_ResumeAndPrompt(t *testing.T) {
	_, args, _ := CopilotAdapter{}.Command(LaunchOptions{
		Resume: "session-7",
		Prompt: "summarise diff",
	})

	want := []string{"--allow-all", "--resume", "session-7", "-p", "summarise diff"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("args = %v\n want %v", args, want)
	}
}

func TestLookup_KnownAdapters(t *testing.T) {
	for _, name := range Names() {
		a, err := Lookup(name)
		if err != nil {
			t.Errorf("Lookup(%q): %v", name, err)
			continue
		}
		if a.Name() != name {
			t.Errorf("Lookup(%q).Name() = %q", name, a.Name())
		}
	}
}

func TestLookup_Unknown(t *testing.T) {
	a, err := Lookup("not-a-runner")
	if err == nil {
		t.Fatal("Lookup of unknown name returned nil error")
	}
	if a != nil {
		t.Errorf("Lookup of unknown name returned non-nil adapter: %v", a)
	}
	// Error should hint at known adapters so misconfig is debuggable.
	if !strings.Contains(err.Error(), "claude") || !strings.Contains(err.Error(), "copilot") {
		t.Errorf("error message should list known adapters: %v", err)
	}
}

// containsArg reports whether args contains s as a discrete element.
func containsArg(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}

// endsWith reports whether args ends with the suffix sequence.
func endsWith(args, suffix []string) bool {
	if len(args) < len(suffix) {
		return false
	}
	return reflect.DeepEqual(args[len(args)-len(suffix):], suffix)
}
