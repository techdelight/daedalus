// Copyright (C) 2026 Techdelight BV

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/techdelight/daedalus/internal/platform"
)

func TestParseArgs_NoArgs_Help(t *testing.T) {
	cfg, err := ParseArgs([]string{})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "help" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "help")
	}
}

func TestParseArgs_HelpFlag(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		cfg, err := ParseArgs([]string{flag})
		if err != nil {
			t.Fatalf("ParseArgs(%s) failed: %v", flag, err)
		}
		if cfg.Subcommand != "help" {
			t.Errorf("ParseArgs(%s): Subcommand = %q, want %q", flag, cfg.Subcommand, "help")
		}
	}
}

func TestParseArgs_HelpFlagWithOtherArgs(t *testing.T) {
	cfg, err := ParseArgs([]string{"--build", "--help", "my-project"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "help" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "help")
	}
}

func TestParseArgs_ListSubcommand(t *testing.T) {
	cfg, err := ParseArgs([]string{"list"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "list" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "list")
	}
}

func TestParseArgs_OneArg(t *testing.T) {
	cfg, err := ParseArgs([]string{"my-project"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "" {
		t.Errorf("Subcommand = %q, want empty", cfg.Subcommand)
	}
	if cfg.ProjectName != "my-project" {
		t.Errorf("ProjectName = %q, want %q", cfg.ProjectName, "my-project")
	}
	if cfg.ProjectDir != "" {
		t.Errorf("ProjectDir = %q, want empty", cfg.ProjectDir)
	}
}

func TestParseArgs_TwoArgs(t *testing.T) {
	cfg, err := ParseArgs([]string{"my-project", "/tmp"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "" {
		t.Errorf("Subcommand = %q, want empty", cfg.Subcommand)
	}
	if cfg.ProjectName != "my-project" {
		t.Errorf("ProjectName = %q, want %q", cfg.ProjectName, "my-project")
	}
	if cfg.ProjectDir == "" {
		t.Error("ProjectDir is empty, want non-empty")
	}
}

func TestParseArgs_TargetOverride(t *testing.T) {
	cfg, err := ParseArgs([]string{"--target", "godot", "my-project"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Target != "godot" {
		t.Errorf("Target = %q, want %q", cfg.Target, "godot")
	}
	if !cfg.TargetOverride {
		t.Error("TargetOverride = false, want true")
	}
}

func TestParseArgs_DefaultTarget_NoOverride(t *testing.T) {
	cfg, err := ParseArgs([]string{"my-project"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Target != "dev" {
		t.Errorf("Target = %q, want %q", cfg.Target, "dev")
	}
	if cfg.TargetOverride {
		t.Error("TargetOverride = true, want false")
	}
}

func TestParseArgs_FlagsMixedWithPositional(t *testing.T) {
	cfg, err := ParseArgs([]string{"--build", "my-project", "--no-tmux", "/tmp", "-p", "do stuff"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.ProjectName != "my-project" {
		t.Errorf("ProjectName = %q, want %q", cfg.ProjectName, "my-project")
	}
	if cfg.ProjectDir == "" {
		t.Error("ProjectDir is empty, want non-empty")
	}
	if !cfg.Build {
		t.Error("Build = false, want true")
	}
	if !cfg.NoTmux {
		t.Error("NoTmux = false, want true")
	}
	if cfg.Prompt != "do stuff" {
		t.Errorf("Prompt = %q, want %q", cfg.Prompt, "do stuff")
	}
}

func TestParseArgs_TargetRequiresValue(t *testing.T) {
	_, err := ParseArgs([]string{"--target"})
	if err == nil {
		t.Fatal("expected error for --target without value")
	}
}

func TestParseArgs_ResumeFlag(t *testing.T) {
	cfg, err := ParseArgs([]string{"--resume", "abc123", "my-project"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Resume != "abc123" {
		t.Errorf("Resume = %q, want %q", cfg.Resume, "abc123")
	}
}

func TestParseArgs_PromptRequiresValue(t *testing.T) {
	_, err := ParseArgs([]string{"-p"})
	if err == nil {
		t.Fatal("expected error for -p without value")
	}
}

func TestParseArgs_TooManyPositionalArgs(t *testing.T) {
	_, err := ParseArgs([]string{"foo", "/tmp", "extra"})
	if err == nil {
		t.Fatal("expected error for >2 positional args")
	}
}

func TestParseArgs_TuiSubcommand(t *testing.T) {
	cfg, err := ParseArgs([]string{"tui"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "tui" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "tui")
	}
}

func TestParseArgs_ThreePositionalWithFlags(t *testing.T) {
	_, err := ParseArgs([]string{"--build", "foo", "bar", "baz"})
	if err == nil {
		t.Fatal("expected error for >2 positional args mixed with flags")
	}
}

func TestParseArgs_WebSubcommand(t *testing.T) {
	cfg, err := ParseArgs([]string{"web"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "web" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "web")
	}
	if platform.IsWSL2() {
		if cfg.WebAddr != "0.0.0.0:3000" {
			t.Errorf("WebAddr = %q, want %q (WSL2 detected)", cfg.WebAddr, "0.0.0.0:3000")
		}
		if !cfg.WSL2Detected {
			t.Error("WSL2Detected = false, want true on WSL2")
		}
	} else {
		if cfg.WebAddr != "127.0.0.1:3000" {
			t.Errorf("WebAddr = %q, want %q", cfg.WebAddr, "127.0.0.1:3000")
		}
		if cfg.WSL2Detected {
			t.Error("WSL2Detected = true, want false on non-WSL2")
		}
	}
}

func TestParseArgs_WebWithPort(t *testing.T) {
	cfg, err := ParseArgs([]string{"web", "--port", "8080"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "web" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "web")
	}
	wantHost := "127.0.0.1"
	if platform.IsWSL2() {
		wantHost = "0.0.0.0"
	}
	want := wantHost + ":8080"
	if cfg.WebAddr != want {
		t.Errorf("WebAddr = %q, want %q", cfg.WebAddr, want)
	}
}

func TestParseArgs_WebWithHost(t *testing.T) {
	cfg, err := ParseArgs([]string{"web", "--host", "0.0.0.0"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "web" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "web")
	}
	if cfg.WebAddr != "0.0.0.0:3000" {
		t.Errorf("WebAddr = %q, want %q", cfg.WebAddr, "0.0.0.0:3000")
	}
}

func TestParseArgs_WebWithHostAndPort(t *testing.T) {
	cfg, err := ParseArgs([]string{"web", "--host", "0.0.0.0", "--port", "9090"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "web" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "web")
	}
	if cfg.WebAddr != "0.0.0.0:9090" {
		t.Errorf("WebAddr = %q, want %q", cfg.WebAddr, "0.0.0.0:9090")
	}
}

func TestParseArgs_WebPortRequiresValue(t *testing.T) {
	_, err := ParseArgs([]string{"web", "--port"})
	if err == nil {
		t.Fatal("expected error for --port without value")
	}
}

func TestParseArgs_WebHostRequiresValue(t *testing.T) {
	_, err := ParseArgs([]string{"web", "--host"})
	if err == nil {
		t.Fatal("expected error for --host without value")
	}
}

func TestParseArgs_DinDFlag(t *testing.T) {
	cfg, err := ParseArgs([]string{"--dind", "my-project"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if !cfg.DinD {
		t.Error("DinD = false, want true")
	}
}

func TestParseArgs_PruneSubcommand(t *testing.T) {
	cfg, err := ParseArgs([]string{"prune"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "prune" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "prune")
	}
}

func TestParseArgs_DebugFlag(t *testing.T) {
	cfg, err := ParseArgs([]string{"--debug", "my-project"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if !cfg.Debug {
		t.Error("Debug = false, want true")
	}
}

func TestParseArgs_ForceFlag(t *testing.T) {
	cfg, err := ParseArgs([]string{"--force", "prune"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if !cfg.Force {
		t.Error("Force = false, want true")
	}
	if cfg.Subcommand != "prune" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "prune")
	}
}

func TestParseArgs_RemoveSubcommand(t *testing.T) {
	cfg, err := ParseArgs([]string{"remove", "my-app"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "remove" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "remove")
	}
	if len(cfg.RemoveTargets) != 1 || cfg.RemoveTargets[0] != "my-app" {
		t.Errorf("RemoveTargets = %v, want [my-app]", cfg.RemoveTargets)
	}
}

func TestParseArgs_RemoveMultiple(t *testing.T) {
	cfg, err := ParseArgs([]string{"remove", "app1", "app2", "app3"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "remove" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "remove")
	}
	if len(cfg.RemoveTargets) != 3 {
		t.Errorf("RemoveTargets count = %d, want 3", len(cfg.RemoveTargets))
	}
}

func TestParseArgs_RemoveNoArgs(t *testing.T) {
	cfg, err := ParseArgs([]string{"remove"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "remove" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "remove")
	}
	if len(cfg.RemoveTargets) != 0 {
		t.Errorf("RemoveTargets = %v, want empty", cfg.RemoveTargets)
	}
}

func TestParseArgs_RemoveWithForce(t *testing.T) {
	cfg, err := ParseArgs([]string{"--force", "remove", "my-app"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "remove" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "remove")
	}
	if !cfg.Force {
		t.Error("Force = false, want true")
	}
	if len(cfg.RemoveTargets) != 1 || cfg.RemoveTargets[0] != "my-app" {
		t.Errorf("RemoveTargets = %v, want [my-app]", cfg.RemoveTargets)
	}
}

func TestParseArgs_WebPortInvalid(t *testing.T) {
	_, err := ParseArgs([]string{"web", "--port", "abc"})
	if err == nil {
		t.Fatal("expected error for --port abc")
	}
	if !strings.Contains(err.Error(), "valid port number") {
		t.Errorf("error = %q, want mention of 'valid port number'", err)
	}
}

func TestParseArgs_WebPortOutOfRange(t *testing.T) {
	_, err := ParseArgs([]string{"web", "--port", "99999"})
	if err == nil {
		t.Fatal("expected error for --port 99999")
	}
	if !strings.Contains(err.Error(), "valid port number") {
		t.Errorf("error = %q, want mention of 'valid port number'", err)
	}
}

func TestParseArgs_WebPortValid(t *testing.T) {
	cfg, err := ParseArgs([]string{"web", "--port", "8080"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	wantHost := "127.0.0.1"
	if platform.IsWSL2() {
		wantHost = "0.0.0.0"
	}
	want := wantHost + ":8080"
	if cfg.WebAddr != want {
		t.Errorf("WebAddr = %q, want %q", cfg.WebAddr, want)
	}
}

func TestParseArgs_WebHostEmpty(t *testing.T) {
	_, err := ParseArgs([]string{"web", "--host", "  "})
	if err == nil {
		t.Fatal("expected error for whitespace-only --host")
	}
	if !strings.Contains(err.Error(), "non-empty") {
		t.Errorf("error = %q, want mention of 'non-empty'", err)
	}
}

func TestParseArgs_WebHostOverride_PreventsWSL2(t *testing.T) {
	cfg, err := ParseArgs([]string{"web", "--host", "127.0.0.1"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.WSL2Detected {
		t.Error("WSL2Detected = true, want false when --host is explicitly set")
	}
	if cfg.WebAddr != "127.0.0.1:3000" {
		t.Errorf("WebAddr = %q, want %q", cfg.WebAddr, "127.0.0.1:3000")
	}
}

func TestParseArgs_NoColorFlag(t *testing.T) {
	cfg, err := ParseArgs([]string{"--no-color", "my-project"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if !cfg.NoColor {
		t.Error("NoColor = false, want true")
	}
}

func TestParseArgs_ConfigSubcommand(t *testing.T) {
	cfg, err := ParseArgs([]string{"config", "my-app"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "config" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "config")
	}
	if cfg.ConfigTarget != "my-app" {
		t.Errorf("ConfigTarget = %q, want %q", cfg.ConfigTarget, "my-app")
	}
}

func TestParseArgs_ConfigNoProject(t *testing.T) {
	cfg, err := ParseArgs([]string{"config"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "config" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "config")
	}
	if cfg.ConfigTarget != "" {
		t.Errorf("ConfigTarget = %q, want empty", cfg.ConfigTarget)
	}
}

func TestParseArgs_ConfigWithSet(t *testing.T) {
	cfg, err := ParseArgs([]string{"--set", "dind=true", "config", "my-app"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "config" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "config")
	}
	if len(cfg.ConfigSet) != 1 || cfg.ConfigSet[0] != "dind=true" {
		t.Errorf("ConfigSet = %v, want [dind=true]", cfg.ConfigSet)
	}
}

func TestParseArgs_ConfigWithUnset(t *testing.T) {
	cfg, err := ParseArgs([]string{"--unset", "dind", "config", "my-app"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "config" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "config")
	}
	if len(cfg.ConfigUnset) != 1 || cfg.ConfigUnset[0] != "dind" {
		t.Errorf("ConfigUnset = %v, want [dind]", cfg.ConfigUnset)
	}
}

func TestParseArgs_SetRequiresEqualsSign(t *testing.T) {
	_, err := ParseArgs([]string{"--set", "noequalssign", "config", "my-app"})
	if err == nil {
		t.Fatal("expected error for --set without =")
	}
	if !strings.Contains(err.Error(), "key=value") {
		t.Errorf("error = %q, want mention of 'key=value'", err)
	}
}

func TestParseArgs_CompletionSubcommand(t *testing.T) {
	cfg, err := ParseArgs([]string{"completion", "bash"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "completion" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "completion")
	}
	if cfg.CompletionShell != "bash" {
		t.Errorf("CompletionShell = %q, want %q", cfg.CompletionShell, "bash")
	}
}

func TestParseArgs_CompletionNoShell(t *testing.T) {
	cfg, err := ParseArgs([]string{"completion"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "completion" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "completion")
	}
	if cfg.CompletionShell != "" {
		t.Errorf("CompletionShell = %q, want empty", cfg.CompletionShell)
	}
}

func TestParseArgs_RenameSubcommand(t *testing.T) {
	cfg, err := ParseArgs([]string{"rename", "old-app", "new-app"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "rename" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "rename")
	}
	if cfg.RenameOldName != "old-app" {
		t.Errorf("RenameOldName = %q, want %q", cfg.RenameOldName, "old-app")
	}
	if cfg.RenameNewName != "new-app" {
		t.Errorf("RenameNewName = %q, want %q", cfg.RenameNewName, "new-app")
	}
}

func TestParseArgs_RenameOneArg(t *testing.T) {
	cfg, err := ParseArgs([]string{"rename", "old-app"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "rename" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "rename")
	}
	if cfg.RenameOldName != "old-app" {
		t.Errorf("RenameOldName = %q, want %q", cfg.RenameOldName, "old-app")
	}
	if cfg.RenameNewName != "" {
		t.Errorf("RenameNewName = %q, want empty", cfg.RenameNewName)
	}
}

func TestParseArgs_RenameNoArgs(t *testing.T) {
	cfg, err := ParseArgs([]string{"rename"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "rename" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "rename")
	}
	if cfg.RenameOldName != "" {
		t.Errorf("RenameOldName = %q, want empty", cfg.RenameOldName)
	}
	if cfg.RenameNewName != "" {
		t.Errorf("RenameNewName = %q, want empty", cfg.RenameNewName)
	}
}

func TestParseArgs_DisplayFlag(t *testing.T) {
	cfg, err := ParseArgs([]string{"--display", "my-project"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if !cfg.Display {
		t.Error("Display = false, want true")
	}
	if cfg.ProjectName != "my-project" {
		t.Errorf("ProjectName = %q, want %q", cfg.ProjectName, "my-project")
	}
}

func TestParseArgs_DataDirEnvVar(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DAEDALUS_DATA_DIR", tmp)
	cfg, err := ParseArgs([]string{"my-project"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.DataDir != tmp {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, tmp)
	}
}

func TestParseArgs_DataDirDefaultFallback(t *testing.T) {
	t.Setenv("DAEDALUS_DATA_DIR", "")
	cfg, err := ParseArgs([]string{"my-project"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	want := filepath.Join(cfg.ScriptDir, ".cache")
	if cfg.DataDir != want {
		t.Errorf("DataDir = %q, want default %q", cfg.DataDir, want)
	}
}

func TestParseArgs_BuildNoArgs_BuildSubcommand(t *testing.T) {
	cfg, err := ParseArgs([]string{"--build"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "build" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "build")
	}
	if !cfg.Build {
		t.Error("Build = false, want true")
	}
}

func TestParseArgs_BuildWithTargetNoArgs(t *testing.T) {
	cfg, err := ParseArgs([]string{"--build", "--target", "godot"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "build" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "build")
	}
	if !cfg.Build {
		t.Error("Build = false, want true")
	}
	if cfg.Target != "godot" {
		t.Errorf("Target = %q, want %q", cfg.Target, "godot")
	}
}

func TestParseArgs_BuildWithProjectName_NormalFlow(t *testing.T) {
	cfg, err := ParseArgs([]string{"--build", "my-project"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "" {
		t.Errorf("Subcommand = %q, want empty (normal project flow)", cfg.Subcommand)
	}
	if !cfg.Build {
		t.Error("Build = false, want true")
	}
	if cfg.ProjectName != "my-project" {
		t.Errorf("ProjectName = %q, want %q", cfg.ProjectName, "my-project")
	}
}

func TestParseArgs_BuildNoArgs_ScriptDirResolved(t *testing.T) {
	cfg, err := ParseArgs([]string{"--build"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.ScriptDir == "" {
		t.Error("ScriptDir is empty, want non-empty (should be resolved)")
	}
}

func TestParseArgs_BuildNoArgs_DataDirResolved(t *testing.T) {
	cfg, err := ParseArgs([]string{"--build"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.DataDir == "" {
		t.Error("DataDir is empty, want non-empty (should be resolved)")
	}
}

func TestParseArgs_AgentFlag_Claude(t *testing.T) {
	cfg, err := ParseArgs([]string{"--agent", "claude", "my-project"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Agent != "claude" {
		t.Errorf("Agent = %q, want %q", cfg.Agent, "claude")
	}
}

func TestParseArgs_AgentFlag_Copilot(t *testing.T) {
	cfg, err := ParseArgs([]string{"--agent", "copilot", "my-project"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Agent != "copilot" {
		t.Errorf("Agent = %q, want %q", cfg.Agent, "copilot")
	}
}

func TestParseArgs_AgentFlag_Invalid(t *testing.T) {
	_, err := ParseArgs([]string{"--agent", "gpt", "my-project"})
	if err == nil {
		t.Fatal("expected error for --agent gpt")
	}
	if !strings.Contains(err.Error(), "unknown agent") {
		t.Errorf("error = %q, want mention of 'unknown agent'", err)
	}
}

func TestParseArgs_AgentFlag_MissingValue(t *testing.T) {
	_, err := ParseArgs([]string{"--agent"})
	if err == nil {
		t.Fatal("expected error for --agent without value")
	}
	if !strings.Contains(err.Error(), "requires an agent name") {
		t.Errorf("error = %q, want mention of 'requires an agent name'", err)
	}
}

func TestParseArgs_AgentFlag_Default(t *testing.T) {
	cfg, err := ParseArgs([]string{"my-project"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Agent != "" {
		t.Errorf("Agent = %q, want empty (default)", cfg.Agent)
	}
}

func TestParseArgs_AgentsSubcommand_NoArgs(t *testing.T) {
	cfg, err := ParseArgs([]string{"agents"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "agents" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "agents")
	}
	if len(cfg.AgentsArgs) != 0 {
		t.Errorf("AgentsArgs = %v, want empty", cfg.AgentsArgs)
	}
}

func TestParseArgs_AgentsSubcommand_List(t *testing.T) {
	cfg, err := ParseArgs([]string{"agents", "list"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "agents" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "agents")
	}
	if len(cfg.AgentsArgs) != 1 || cfg.AgentsArgs[0] != "list" {
		t.Errorf("AgentsArgs = %v, want [list]", cfg.AgentsArgs)
	}
}

func TestParseArgs_AgentsSubcommand_Create(t *testing.T) {
	cfg, err := ParseArgs([]string{"agents", "create", "reviewer"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "agents" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "agents")
	}
	if len(cfg.AgentsArgs) != 2 || cfg.AgentsArgs[0] != "create" || cfg.AgentsArgs[1] != "reviewer" {
		t.Errorf("AgentsArgs = %v, want [create reviewer]", cfg.AgentsArgs)
	}
}

func TestParseArgs_AgentsSubcommand_Show(t *testing.T) {
	cfg, err := ParseArgs([]string{"agents", "show", "reviewer"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "agents" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "agents")
	}
	if len(cfg.AgentsArgs) != 2 || cfg.AgentsArgs[0] != "show" || cfg.AgentsArgs[1] != "reviewer" {
		t.Errorf("AgentsArgs = %v, want [show reviewer]", cfg.AgentsArgs)
	}
}

func TestParseArgs_AgentsSubcommand_Remove(t *testing.T) {
	cfg, err := ParseArgs([]string{"agents", "remove", "reviewer"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Subcommand != "agents" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "agents")
	}
	if len(cfg.AgentsArgs) != 2 || cfg.AgentsArgs[0] != "remove" || cfg.AgentsArgs[1] != "reviewer" {
		t.Errorf("AgentsArgs = %v, want [remove reviewer]", cfg.AgentsArgs)
	}
}

func TestParseArgs_AgentFlag_UserDefined(t *testing.T) {
	// Create a temp agents dir with a user-defined agent
	tmp := t.TempDir()
	t.Setenv("DAEDALUS_DATA_DIR", tmp)
	agentsDir := filepath.Join(tmp, "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "reviewer.json"),
		[]byte(`{"name":"reviewer","baseAgent":"claude"}`), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseArgs([]string{"--agent", "reviewer", "my-project"})
	if err != nil {
		t.Fatalf("ParseArgs failed: %v", err)
	}
	if cfg.Agent != "reviewer" {
		t.Errorf("Agent = %q, want %q", cfg.Agent, "reviewer")
	}
}

func TestParseArgs_AgentFlag_InvalidUserDefined(t *testing.T) {
	// No agents dir — user-defined name should fail
	tmp := t.TempDir()
	t.Setenv("DAEDALUS_DATA_DIR", tmp)
	_, err := ParseArgs([]string{"--agent", "nonexistent", "my-project"})
	if err == nil {
		t.Fatal("expected error for --agent nonexistent")
	}
	if !strings.Contains(err.Error(), "unknown agent") {
		t.Errorf("error = %q, want mention of 'unknown agent'", err)
	}
}

func TestValidateAgentName_BuiltIn(t *testing.T) {
	tmp := t.TempDir()
	if err := validateAgentName("claude", tmp); err != nil {
		t.Errorf("validateAgentName(claude) = %v, want nil", err)
	}
	if err := validateAgentName("copilot", tmp); err != nil {
		t.Errorf("validateAgentName(copilot) = %v, want nil", err)
	}
}

func TestValidateAgentName_UserDefined(t *testing.T) {
	tmp := t.TempDir()
	agentsDir := filepath.Join(tmp, "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "tester.json"),
		[]byte(`{"name":"tester","baseAgent":"claude"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateAgentName("tester", agentsDir); err != nil {
		t.Errorf("validateAgentName(tester) = %v, want nil", err)
	}
}

func TestValidateAgentName_Unknown(t *testing.T) {
	tmp := t.TempDir()
	err := validateAgentName("nonexistent", tmp)
	if err == nil {
		t.Fatal("validateAgentName(nonexistent) = nil, want error")
	}
	if !strings.Contains(err.Error(), "unknown agent") {
		t.Errorf("error = %q, want mention of 'unknown agent'", err)
	}
	// Error should list valid agents
	if !strings.Contains(err.Error(), "claude") {
		t.Errorf("error = %q, want to list 'claude'", err)
	}
}
