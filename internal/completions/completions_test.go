// Copyright (C) 2026 Techdelight BV

package completions

import (
	"os"
	"strings"
	"testing"

	"github.com/techdelight/daedalus/core"
)

func TestBashCompletion_ContainsSubcommands(t *testing.T) {
	if !strings.Contains(bashCompletion, "list prune remove rename config tui web completion") {
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
