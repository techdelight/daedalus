// Copyright (C) 2026 Techdelight BV

package core

import (
	"path/filepath"
	"testing"
)

func TestConfig_Image(t *testing.T) {
	cfg := &Config{ImagePrefix: "techdelight/claude-runner", Target: "dev"}
	got := cfg.Image()
	want := "techdelight/claude-runner:dev"
	if got != want {
		t.Errorf("Image() = %q, want %q", got, want)
	}
}

func TestConfig_ContainerName(t *testing.T) {
	cfg := &Config{ProjectName: "my-app"}
	got := cfg.ContainerName()
	want := "claude-run-my-app"
	if got != want {
		t.Errorf("ContainerName() = %q, want %q", got, want)
	}
}

func TestConfig_TmuxSession(t *testing.T) {
	cfg := &Config{ProjectName: "my-app"}
	got := cfg.TmuxSession()
	want := "claude-my-app"
	if got != want {
		t.Errorf("TmuxSession() = %q, want %q", got, want)
	}
}

func TestConfig_CacheDir(t *testing.T) {
	cfg := &Config{ScriptDir: "/home/user", ProjectName: "my-app"}
	got := cfg.CacheDir()
	want := filepath.Join("/home/user", ".cache", "my-app")
	if got != want {
		t.Errorf("CacheDir() = %q, want %q", got, want)
	}
}

func TestConfig_UseTmux(t *testing.T) {
	tests := []struct {
		name   string
		cfg    Config
		expect bool
	}{
		{"default", Config{}, true},
		{"with prompt", Config{Prompt: "do stuff"}, false},
		{"no tmux flag", Config{NoTmux: true}, false},
		{"both", Config{Prompt: "x", NoTmux: true}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.cfg.UseTmux()
			if got != tc.expect {
				t.Errorf("UseTmux() = %v, want %v", got, tc.expect)
			}
		})
	}
}

func TestApplyRegistryEntry(t *testing.T) {
	cfg := &Config{Target: "dev"}
	entry := ProjectEntry{Directory: "/path/to/project", Target: "godot"}

	ApplyRegistryEntry(cfg, entry)

	if cfg.ProjectDir != "/path/to/project" {
		t.Errorf("ProjectDir = %q, want %q", cfg.ProjectDir, "/path/to/project")
	}
	if cfg.Target != "godot" {
		t.Errorf("Target = %q, want %q", cfg.Target, "godot")
	}
}

func TestApplyRegistryEntry_TargetOverride(t *testing.T) {
	cfg := &Config{Target: "dev", TargetOverride: true}
	entry := ProjectEntry{Directory: "/path/to/project", Target: "godot"}

	ApplyRegistryEntry(cfg, entry)

	if cfg.ProjectDir != "/path/to/project" {
		t.Errorf("ProjectDir = %q, want %q", cfg.ProjectDir, "/path/to/project")
	}
	if cfg.Target != "dev" {
		t.Errorf("Target = %q, want %q (override should win)", cfg.Target, "dev")
	}
}

func TestApplyRegistryEntry_DefaultFlags(t *testing.T) {
	cfg := &Config{Target: "dev"}
	entry := ProjectEntry{
		Directory:    "/path/to/project",
		Target:       "dev",
		DefaultFlags: map[string]string{"debug": "true", "dind": "true"},
	}

	ApplyRegistryEntry(cfg, entry)

	if !cfg.Debug {
		t.Error("Debug = false, want true (from default flags)")
	}
	if !cfg.DinD {
		t.Error("DinD = false, want true (from default flags)")
	}
}

func TestApplyRegistryEntry_CLIOverridesDefaults(t *testing.T) {
	// Simulate CLI already set Debug=true, default says dind=true
	cfg := &Config{Target: "dev", Debug: true}
	entry := ProjectEntry{
		Directory:    "/path/to/project",
		Target:       "dev",
		DefaultFlags: map[string]string{"debug": "false", "dind": "true"},
	}

	ApplyRegistryEntry(cfg, entry)

	// CLI Debug=true should win even though default says false
	if !cfg.Debug {
		t.Error("Debug = false, want true (CLI override should win)")
	}
	if !cfg.DinD {
		t.Error("DinD = false, want true (from default flags)")
	}
}

func TestApplyRegistryEntry_NilDefaultFlags(t *testing.T) {
	cfg := &Config{Target: "dev"}
	entry := ProjectEntry{
		Directory: "/path/to/project",
		Target:    "dev",
		// DefaultFlags is nil
	}

	ApplyRegistryEntry(cfg, entry)

	if cfg.Debug {
		t.Error("Debug = true, want false (nil defaults should not change anything)")
	}
	if cfg.DinD {
		t.Error("DinD = true, want false (nil defaults should not change anything)")
	}
}
