// Copyright (C) 2026 Techdelight BV

package main

import (
	"strings"
	"testing"
)

func TestParseArgs_NoArgs_Help(t *testing.T) {
	cfg, err := parseArgs([]string{})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.Subcommand != "help" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "help")
	}
}

func TestParseArgs_HelpFlag(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		cfg, err := parseArgs([]string{flag})
		if err != nil {
			t.Fatalf("parseArgs(%s) failed: %v", flag, err)
		}
		if cfg.Subcommand != "help" {
			t.Errorf("parseArgs(%s): Subcommand = %q, want %q", flag, cfg.Subcommand, "help")
		}
	}
}

func TestParseArgs_HelpFlagWithOtherArgs(t *testing.T) {
	cfg, err := parseArgs([]string{"--build", "--help", "my-project"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.Subcommand != "help" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "help")
	}
}

func TestParseArgs_ListSubcommand(t *testing.T) {
	cfg, err := parseArgs([]string{"list"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.Subcommand != "list" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "list")
	}
}

func TestParseArgs_OneArg(t *testing.T) {
	cfg, err := parseArgs([]string{"my-project"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
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
	cfg, err := parseArgs([]string{"my-project", "/tmp"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
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
	cfg, err := parseArgs([]string{"--target", "godot", "my-project"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.Target != "godot" {
		t.Errorf("Target = %q, want %q", cfg.Target, "godot")
	}
	if !cfg.TargetOverride {
		t.Error("TargetOverride = false, want true")
	}
}

func TestParseArgs_DefaultTarget_NoOverride(t *testing.T) {
	cfg, err := parseArgs([]string{"my-project"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.Target != "dev" {
		t.Errorf("Target = %q, want %q", cfg.Target, "dev")
	}
	if cfg.TargetOverride {
		t.Error("TargetOverride = true, want false")
	}
}

func TestParseArgs_FlagsMixedWithPositional(t *testing.T) {
	cfg, err := parseArgs([]string{"--build", "my-project", "--no-tmux", "/tmp", "-p", "do stuff"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
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
	_, err := parseArgs([]string{"--target"})
	if err == nil {
		t.Fatal("expected error for --target without value")
	}
}

func TestParseArgs_ResumeFlag(t *testing.T) {
	cfg, err := parseArgs([]string{"--resume", "abc123", "my-project"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.Resume != "abc123" {
		t.Errorf("Resume = %q, want %q", cfg.Resume, "abc123")
	}
}

func TestParseArgs_PromptRequiresValue(t *testing.T) {
	_, err := parseArgs([]string{"-p"})
	if err == nil {
		t.Fatal("expected error for -p without value")
	}
}

func TestParseArgs_TooManyPositionalArgs(t *testing.T) {
	_, err := parseArgs([]string{"foo", "/tmp", "extra"})
	if err == nil {
		t.Fatal("expected error for >2 positional args")
	}
}

func TestParseArgs_TuiSubcommand(t *testing.T) {
	cfg, err := parseArgs([]string{"tui"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.Subcommand != "tui" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "tui")
	}
	if cfg.ClaudeConfigDir == "" {
		t.Error("ClaudeConfigDir is empty for tui subcommand, want non-empty")
	}
}

func TestParseArgs_ListSubcommand_HasClaudeConfigDir(t *testing.T) {
	cfg, err := parseArgs([]string{"list"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.ClaudeConfigDir == "" {
		t.Error("ClaudeConfigDir is empty for list subcommand, want non-empty")
	}
}

func TestParseArgs_MkdirAllError(t *testing.T) {
	// Point CLAUDE_CONFIG_DIR to an unwritable path
	t.Setenv("CLAUDE_CONFIG_DIR", "/dev/null/impossible")
	_, err := parseArgs([]string{"my-project"})
	if err == nil {
		t.Fatal("expected error for unwritable config dir")
	}
	if !strings.Contains(err.Error(), "creating claude config directory") {
		t.Errorf("error = %q, want mention of 'creating claude config directory'", err)
	}
}

func TestParseArgs_ThreePositionalWithFlags(t *testing.T) {
	_, err := parseArgs([]string{"--build", "foo", "bar", "baz"})
	if err == nil {
		t.Fatal("expected error for >2 positional args mixed with flags")
	}
}

func TestParseArgs_WebSubcommand(t *testing.T) {
	cfg, err := parseArgs([]string{"web"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.Subcommand != "web" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "web")
	}
	if cfg.WebAddr != "127.0.0.1:3000" {
		t.Errorf("WebAddr = %q, want %q", cfg.WebAddr, "127.0.0.1:3000")
	}
}

func TestParseArgs_WebWithPort(t *testing.T) {
	cfg, err := parseArgs([]string{"web", "--port", "8080"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.Subcommand != "web" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "web")
	}
	if cfg.WebAddr != "127.0.0.1:8080" {
		t.Errorf("WebAddr = %q, want %q", cfg.WebAddr, "127.0.0.1:8080")
	}
}

func TestParseArgs_WebWithHost(t *testing.T) {
	cfg, err := parseArgs([]string{"web", "--host", "0.0.0.0"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.Subcommand != "web" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "web")
	}
	if cfg.WebAddr != "0.0.0.0:3000" {
		t.Errorf("WebAddr = %q, want %q", cfg.WebAddr, "0.0.0.0:3000")
	}
}

func TestParseArgs_WebWithHostAndPort(t *testing.T) {
	cfg, err := parseArgs([]string{"web", "--host", "0.0.0.0", "--port", "9090"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.Subcommand != "web" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "web")
	}
	if cfg.WebAddr != "0.0.0.0:9090" {
		t.Errorf("WebAddr = %q, want %q", cfg.WebAddr, "0.0.0.0:9090")
	}
}

func TestParseArgs_WebPortRequiresValue(t *testing.T) {
	_, err := parseArgs([]string{"web", "--port"})
	if err == nil {
		t.Fatal("expected error for --port without value")
	}
}

func TestParseArgs_WebHostRequiresValue(t *testing.T) {
	_, err := parseArgs([]string{"web", "--host"})
	if err == nil {
		t.Fatal("expected error for --host without value")
	}
}

func TestParseArgs_DinDFlag(t *testing.T) {
	cfg, err := parseArgs([]string{"--dind", "my-project"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if !cfg.DinD {
		t.Error("DinD = false, want true")
	}
}

func TestParseArgs_PruneSubcommand(t *testing.T) {
	cfg, err := parseArgs([]string{"prune"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.Subcommand != "prune" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "prune")
	}
}

func TestParseArgs_DebugFlag(t *testing.T) {
	cfg, err := parseArgs([]string{"--debug", "my-project"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if !cfg.Debug {
		t.Error("Debug = false, want true")
	}
}

func TestParseArgs_ForceFlag(t *testing.T) {
	cfg, err := parseArgs([]string{"--force", "prune"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if !cfg.Force {
		t.Error("Force = false, want true")
	}
	if cfg.Subcommand != "prune" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "prune")
	}
}

func TestParseArgs_RemoveSubcommand(t *testing.T) {
	cfg, err := parseArgs([]string{"remove", "my-app"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.Subcommand != "remove" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "remove")
	}
	if len(cfg.RemoveTargets) != 1 || cfg.RemoveTargets[0] != "my-app" {
		t.Errorf("RemoveTargets = %v, want [my-app]", cfg.RemoveTargets)
	}
}

func TestParseArgs_RemoveMultiple(t *testing.T) {
	cfg, err := parseArgs([]string{"remove", "app1", "app2", "app3"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.Subcommand != "remove" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "remove")
	}
	if len(cfg.RemoveTargets) != 3 {
		t.Errorf("RemoveTargets count = %d, want 3", len(cfg.RemoveTargets))
	}
}

func TestParseArgs_RemoveNoArgs(t *testing.T) {
	cfg, err := parseArgs([]string{"remove"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.Subcommand != "remove" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "remove")
	}
	if len(cfg.RemoveTargets) != 0 {
		t.Errorf("RemoveTargets = %v, want empty", cfg.RemoveTargets)
	}
}

func TestParseArgs_RemoveWithForce(t *testing.T) {
	cfg, err := parseArgs([]string{"--force", "remove", "my-app"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
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
	_, err := parseArgs([]string{"web", "--port", "abc"})
	if err == nil {
		t.Fatal("expected error for --port abc")
	}
	if !strings.Contains(err.Error(), "valid port number") {
		t.Errorf("error = %q, want mention of 'valid port number'", err)
	}
}

func TestParseArgs_WebPortOutOfRange(t *testing.T) {
	_, err := parseArgs([]string{"web", "--port", "99999"})
	if err == nil {
		t.Fatal("expected error for --port 99999")
	}
	if !strings.Contains(err.Error(), "valid port number") {
		t.Errorf("error = %q, want mention of 'valid port number'", err)
	}
}

func TestParseArgs_WebPortValid(t *testing.T) {
	cfg, err := parseArgs([]string{"web", "--port", "8080"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.WebAddr != "127.0.0.1:8080" {
		t.Errorf("WebAddr = %q, want %q", cfg.WebAddr, "127.0.0.1:8080")
	}
}

func TestParseArgs_WebHostEmpty(t *testing.T) {
	_, err := parseArgs([]string{"web", "--host", "  "})
	if err == nil {
		t.Fatal("expected error for whitespace-only --host")
	}
	if !strings.Contains(err.Error(), "non-empty") {
		t.Errorf("error = %q, want mention of 'non-empty'", err)
	}
}

func TestParseArgs_NoColorFlag(t *testing.T) {
	cfg, err := parseArgs([]string{"--no-color", "my-project"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if !cfg.NoColor {
		t.Error("NoColor = false, want true")
	}
}

func TestParseArgs_ConfigSubcommand(t *testing.T) {
	cfg, err := parseArgs([]string{"config", "my-app"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.Subcommand != "config" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "config")
	}
	if cfg.ConfigTarget != "my-app" {
		t.Errorf("ConfigTarget = %q, want %q", cfg.ConfigTarget, "my-app")
	}
}

func TestParseArgs_ConfigNoProject(t *testing.T) {
	cfg, err := parseArgs([]string{"config"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.Subcommand != "config" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "config")
	}
	if cfg.ConfigTarget != "" {
		t.Errorf("ConfigTarget = %q, want empty", cfg.ConfigTarget)
	}
}

func TestParseArgs_ConfigWithSet(t *testing.T) {
	cfg, err := parseArgs([]string{"--set", "dind=true", "config", "my-app"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.Subcommand != "config" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "config")
	}
	if len(cfg.ConfigSet) != 1 || cfg.ConfigSet[0] != "dind=true" {
		t.Errorf("ConfigSet = %v, want [dind=true]", cfg.ConfigSet)
	}
}

func TestParseArgs_ConfigWithUnset(t *testing.T) {
	cfg, err := parseArgs([]string{"--unset", "dind", "config", "my-app"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.Subcommand != "config" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "config")
	}
	if len(cfg.ConfigUnset) != 1 || cfg.ConfigUnset[0] != "dind" {
		t.Errorf("ConfigUnset = %v, want [dind]", cfg.ConfigUnset)
	}
}

func TestParseArgs_SetRequiresEqualsSign(t *testing.T) {
	_, err := parseArgs([]string{"--set", "noequalssign", "config", "my-app"})
	if err == nil {
		t.Fatal("expected error for --set without =")
	}
	if !strings.Contains(err.Error(), "key=value") {
		t.Errorf("error = %q, want mention of 'key=value'", err)
	}
}

func TestParseArgs_CompletionSubcommand(t *testing.T) {
	cfg, err := parseArgs([]string{"completion", "bash"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.Subcommand != "completion" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "completion")
	}
	if cfg.CompletionShell != "bash" {
		t.Errorf("CompletionShell = %q, want %q", cfg.CompletionShell, "bash")
	}
}

func TestParseArgs_CompletionNoShell(t *testing.T) {
	cfg, err := parseArgs([]string{"completion"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.Subcommand != "completion" {
		t.Errorf("Subcommand = %q, want %q", cfg.Subcommand, "completion")
	}
	if cfg.CompletionShell != "" {
		t.Errorf("CompletionShell = %q, want empty", cfg.CompletionShell)
	}
}

func TestParseArgs_ClaudeConfigDirEnvOverride(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", tmp)
	cfg, err := parseArgs([]string{"my-project"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.ClaudeConfigDir != tmp {
		t.Errorf("ClaudeConfigDir = %q, want %q", cfg.ClaudeConfigDir, tmp)
	}
}

func TestParseArgs_DataDirFlag(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	cfg, err := parseArgs([]string{"--data-dir", tmp, "my-project"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.DataDir != tmp {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, tmp)
	}
}

func TestParseArgs_DataDirEnvVar(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DAEDALUS_DATA_DIR", tmp)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	cfg, err := parseArgs([]string{"my-project"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.DataDir != tmp {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, tmp)
	}
}

func TestParseArgs_DataDirFlagOverridesEnv(t *testing.T) {
	flagDir := t.TempDir()
	envDir := t.TempDir()
	t.Setenv("DAEDALUS_DATA_DIR", envDir)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	cfg, err := parseArgs([]string{"--data-dir", flagDir, "my-project"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cfg.DataDir != flagDir {
		t.Errorf("DataDir = %q, want flag value %q (flag should override env)", cfg.DataDir, flagDir)
	}
}

func TestParseArgs_DataDirDefaultFallback(t *testing.T) {
	t.Setenv("DAEDALUS_DATA_DIR", "")
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	cfg, err := parseArgs([]string{"my-project"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	want := filepath.Join(cfg.ScriptDir, ".cache")
	if cfg.DataDir != want {
		t.Errorf("DataDir = %q, want default %q", cfg.DataDir, want)
	}
}

func TestParseArgs_DataDirRequiresValue(t *testing.T) {
	_, err := parseArgs([]string{"--data-dir"})
	if err == nil {
		t.Fatal("expected error for --data-dir without value")
	}
}
