// Copyright (C) 2026 Techdelight BV

package completions

import (
	"os"
	"strings"
	"testing"

	"github.com/techdelight/daedalus/core"
)

func TestBashCompletion_ContainsSubcommands(t *testing.T) {
	if !strings.Contains(bashCompletion, "skills runners personas") {
		t.Error("bash completion missing skills/runners/personas subcommands")
	}
	if !strings.Contains(bashCompletion, "list prune remove rename config tui web completion skills runners personas") {
		t.Error("bash completion missing subcommands")
	}
	if !strings.Contains(bashCompletion, "complete -F _daedalus daedalus") {
		t.Error("bash completion missing complete command")
	}
}

func TestZshCompletion_ContainsFlags(t *testing.T) {
	for _, flag := range []string{"--build", "--target", "--no-tmux", "--debug", "--dind", "--no-color"} {
		if !strings.Contains(zshCompletion, flag) {
			t.Errorf("zsh completion missing flag %q", flag)
		}
	}
	if !strings.Contains(zshCompletion, "#compdef daedalus") {
		t.Error("zsh completion missing #compdef header")
	}
}

func TestFishCompletion_ContainsFlags(t *testing.T) {
	for _, flag := range []string{"build", "target", "no-tmux", "debug", "dind", "no-color"} {
		if !strings.Contains(fishCompletion, flag) {
			t.Errorf("fish completion missing flag %q", flag)
		}
	}
	if !strings.Contains(fishCompletion, "complete -c daedalus") {
		t.Error("fish completion missing complete command")
	}
}

func TestGenerate_Bash(t *testing.T) {
	cfg := &core.Config{CompletionShell: "bash"}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := Generate(cfg)

	w.Close()
	var buf [8192]byte
	n, _ := r.Read(buf[:])
	os.Stdout = old

	if err != nil {
		t.Fatalf("Generate(bash) failed: %v", err)
	}

	output := string(buf[:n])
	if !strings.Contains(output, "_daedalus") {
		t.Error("expected bash completion function in output")
	}
}

func TestGenerate_Zsh(t *testing.T) {
	cfg := &core.Config{CompletionShell: "zsh"}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := Generate(cfg)

	w.Close()
	var buf [8192]byte
	n, _ := r.Read(buf[:])
	os.Stdout = old

	if err != nil {
		t.Fatalf("Generate(zsh) failed: %v", err)
	}

	output := string(buf[:n])
	if !strings.Contains(output, "#compdef") {
		t.Error("expected zsh compdef header in output")
	}
}

func TestGenerate_Fish(t *testing.T) {
	cfg := &core.Config{CompletionShell: "fish"}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := Generate(cfg)

	w.Close()
	var buf [8192]byte
	n, _ := r.Read(buf[:])
	os.Stdout = old

	if err != nil {
		t.Fatalf("Generate(fish) failed: %v", err)
	}

	output := string(buf[:n])
	if !strings.Contains(output, "complete -c daedalus") {
		t.Error("expected fish completion commands in output")
	}
}

func TestGenerate_InvalidShell(t *testing.T) {
	cfg := &core.Config{CompletionShell: "powershell"}
	err := Generate(cfg)
	if err == nil {
		t.Fatal("expected error for unsupported shell, got nil")
	}
	if !strings.Contains(err.Error(), "usage") {
		t.Errorf("error = %q, want usage hint", err)
	}
}

func TestGenerate_EmptyShell(t *testing.T) {
	cfg := &core.Config{CompletionShell: ""}
	err := Generate(cfg)
	if err == nil {
		t.Fatal("expected error for empty shell, got nil")
	}
}

func TestBashCompletion_RunnersSubcommand(t *testing.T) {
	if !strings.Contains(bashCompletion, "runners)") {
		t.Error("bash completion missing runners case")
	}
	if !strings.Contains(bashCompletion, `"list show"`) {
		t.Error("bash completion missing runners subcommands")
	}
}

func TestZshCompletion_RunnersSubcommand(t *testing.T) {
	if !strings.Contains(zshCompletion, "'runners:List or show built-in runner profiles'") {
		t.Error("zsh completion missing runners subcommand description")
	}
	if !strings.Contains(zshCompletion, "runners)") {
		t.Error("zsh completion missing runners case")
	}
}

func TestFishCompletion_RunnersSubcommand(t *testing.T) {
	if !strings.Contains(fishCompletion, "'runners'") {
		t.Error("fish completion missing runners subcommand")
	}
	if !strings.Contains(fishCompletion, "__fish_seen_subcommand_from runners") {
		t.Error("fish completion missing runners subcommand completions")
	}
}

func TestBashCompletion_PersonasSubcommand(t *testing.T) {
	if !strings.Contains(bashCompletion, "personas)") {
		t.Error("bash completion missing personas case")
	}
	if !strings.Contains(bashCompletion, "list show create remove") {
		t.Error("bash completion missing personas subcommands")
	}
}

func TestZshCompletion_PersonasSubcommand(t *testing.T) {
	if !strings.Contains(zshCompletion, "'personas:Manage named persona configurations'") {
		t.Error("zsh completion missing personas subcommand description")
	}
	if !strings.Contains(zshCompletion, "personas)") {
		t.Error("zsh completion missing personas case")
	}
}

func TestFishCompletion_PersonasSubcommand(t *testing.T) {
	if !strings.Contains(fishCompletion, "'personas'") {
		t.Error("fish completion missing personas subcommand")
	}
	if !strings.Contains(fishCompletion, "__fish_seen_subcommand_from personas") {
		t.Error("fish completion missing personas subcommand completions")
	}
}

func TestBashCompletion_RunnerFlag(t *testing.T) {
	if !strings.Contains(bashCompletion, "--runner") {
		t.Error("bash completion missing --runner flag")
	}
}

func TestBashCompletion_PersonaFlag(t *testing.T) {
	if !strings.Contains(bashCompletion, "--persona") {
		t.Error("bash completion missing --persona flag")
	}
}

func TestZshCompletion_RunnerFlag(t *testing.T) {
	if !strings.Contains(zshCompletion, "--runner") {
		t.Error("zsh completion missing --runner flag")
	}
}

func TestZshCompletion_PersonaFlag(t *testing.T) {
	if !strings.Contains(zshCompletion, "--persona") {
		t.Error("zsh completion missing --persona flag")
	}
}

func TestFishCompletion_RunnerFlag(t *testing.T) {
	if !strings.Contains(fishCompletion, "--runner") {
		t.Error("fish completion missing --runner flag")
	}
}

func TestFishCompletion_PersonaFlag(t *testing.T) {
	if !strings.Contains(fishCompletion, "--persona") {
		t.Error("fish completion missing --persona flag")
	}
}

func TestBashCompletion_SkillsSubcommand(t *testing.T) {
	if !strings.Contains(bashCompletion, "skills)") {
		t.Error("bash completion missing skills case")
	}
	if !strings.Contains(bashCompletion, "add remove show") {
		t.Error("bash completion missing skills subcommands")
	}
}

func TestZshCompletion_SkillsSubcommand(t *testing.T) {
	if !strings.Contains(zshCompletion, "'skills:Manage shared skill catalog'") {
		t.Error("zsh completion missing skills subcommand description")
	}
}

func TestFishCompletion_SkillsSubcommand(t *testing.T) {
	if !strings.Contains(fishCompletion, "'skills'") {
		t.Error("fish completion missing skills subcommand")
	}
	if !strings.Contains(fishCompletion, "__fish_seen_subcommand_from skills") {
		t.Error("fish completion missing skills subcommand completions")
	}
}
