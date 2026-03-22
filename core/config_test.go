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
	cfg := &Config{DataDir: "/data/daedalus", ProjectName: "my-app"}
	got := cfg.CacheDir()
	want := filepath.Join("/data/daedalus", "my-app")
	if got != want {
		t.Errorf("CacheDir() = %q, want %q", got, want)
	}
}

func TestConfig_RegistryPath(t *testing.T) {
	cfg := &Config{DataDir: "/data/daedalus"}
	got := cfg.RegistryPath()
	want := filepath.Join("/data/daedalus", "projects.json")
	if got != want {
		t.Errorf("RegistryPath() = %q, want %q", got, want)
	}
}

func TestConfig_SkillsDir(t *testing.T) {
	cfg := &Config{DataDir: "/data/daedalus"}
	got := cfg.SkillsDir()
	want := filepath.Join("/data/daedalus", "skills")
	if got != want {
		t.Errorf("SkillsDir() = %q, want %q", got, want)
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

func TestApplyRegistryEntry_DisplayFlag(t *testing.T) {
	tests := []struct {
		name       string
		cliDisplay bool
		flagVal    string
		want       bool
	}{
		{"default flag enables display", false, "true", true},
		{"CLI flag wins over default", true, "false", true},
		{"default flag false keeps disabled", false, "false", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			cfg := &Config{Display: tt.cliDisplay}
			entry := ProjectEntry{
				Directory:    "/tmp/test",
				Target:       "dev",
				DefaultFlags: map[string]string{"display": tt.flagVal},
			}

			// Act
			ApplyRegistryEntry(cfg, entry)

			// Assert
			if cfg.Display != tt.want {
				t.Errorf("Display = %v, want %v", cfg.Display, tt.want)
			}
		})
	}
}

func TestApplyRegistryEntry_AgentDefaultFlag(t *testing.T) {
	cfg := &Config{Target: "dev"}
	entry := ProjectEntry{
		Directory:    "/tmp/test",
		Target:       "dev",
		DefaultFlags: map[string]string{"agent": "copilot"},
	}

	ApplyRegistryEntry(cfg, entry)

	if cfg.Agent != "copilot" {
		t.Errorf("Agent = %q, want %q", cfg.Agent, "copilot")
	}
}

func TestApplyRegistryEntry_AgentCLIOverridesDefault(t *testing.T) {
	cfg := &Config{Target: "dev", Agent: "claude"}
	entry := ProjectEntry{
		Directory:    "/tmp/test",
		Target:       "dev",
		DefaultFlags: map[string]string{"agent": "copilot"},
	}

	ApplyRegistryEntry(cfg, entry)

	if cfg.Agent != "claude" {
		t.Errorf("Agent = %q, want %q (CLI should win)", cfg.Agent, "claude")
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
