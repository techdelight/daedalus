// Copyright (C) 2026 Techdelight BV

package core

import "testing"

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }

func TestApplyAppConfig_DataDir_Applied(t *testing.T) {
	cfg := &Config{ImagePrefix: "techdelight/claude-runner"}
	ApplyAppConfig(cfg, AppConfig{DataDir: strPtr("/mnt/data")})
	if cfg.DataDir != "/mnt/data" {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, "/mnt/data")
	}
}

func TestApplyAppConfig_DataDir_CLIWins(t *testing.T) {
	cfg := &Config{DataDir: "/from-cli", ImagePrefix: "techdelight/claude-runner"}
	ApplyAppConfig(cfg, AppConfig{DataDir: strPtr("/from-config")})
	if cfg.DataDir != "/from-cli" {
		t.Errorf("DataDir = %q, want %q (CLI should win)", cfg.DataDir, "/from-cli")
	}
}

func TestApplyAppConfig_Debug_Applied(t *testing.T) {
	cfg := &Config{ImagePrefix: "techdelight/claude-runner"}
	ApplyAppConfig(cfg, AppConfig{Debug: boolPtr(true)})
	if !cfg.Debug {
		t.Error("Debug = false, want true")
	}
}

func TestApplyAppConfig_Debug_CLIWins(t *testing.T) {
	cfg := &Config{Debug: true, ImagePrefix: "techdelight/claude-runner"}
	ApplyAppConfig(cfg, AppConfig{Debug: boolPtr(false)})
	if !cfg.Debug {
		t.Error("Debug = false, want true (CLI should win)")
	}
}

func TestApplyAppConfig_NoTmux_Applied(t *testing.T) {
	cfg := &Config{ImagePrefix: "techdelight/claude-runner"}
	ApplyAppConfig(cfg, AppConfig{NoTmux: boolPtr(true)})
	if !cfg.NoTmux {
		t.Error("NoTmux = false, want true")
	}
}

func TestApplyAppConfig_NoTmux_CLIWins(t *testing.T) {
	cfg := &Config{NoTmux: true, ImagePrefix: "techdelight/claude-runner"}
	ApplyAppConfig(cfg, AppConfig{NoTmux: boolPtr(false)})
	if !cfg.NoTmux {
		t.Error("NoTmux = false, want true (CLI should win)")
	}
}

func TestApplyAppConfig_ImagePrefix_Applied(t *testing.T) {
	cfg := &Config{ImagePrefix: "techdelight/claude-runner"}
	ApplyAppConfig(cfg, AppConfig{ImagePrefix: strPtr("custom/runner")})
	if cfg.ImagePrefix != "custom/runner" {
		t.Errorf("ImagePrefix = %q, want %q", cfg.ImagePrefix, "custom/runner")
	}
}

func TestApplyAppConfig_ImagePrefix_CLIWins(t *testing.T) {
	cfg := &Config{ImagePrefix: "user/override"}
	ApplyAppConfig(cfg, AppConfig{ImagePrefix: strPtr("custom/runner")})
	if cfg.ImagePrefix != "user/override" {
		t.Errorf("ImagePrefix = %q, want %q (CLI should win)", cfg.ImagePrefix, "user/override")
	}
}

func TestApplyAppConfig_NilPointers_NoChange(t *testing.T) {
	cfg := &Config{ImagePrefix: "techdelight/claude-runner"}
	ApplyAppConfig(cfg, AppConfig{})
	if cfg.DataDir != "" {
		t.Errorf("DataDir = %q, want empty", cfg.DataDir)
	}
	if cfg.Debug {
		t.Error("Debug = true, want false")
	}
	if cfg.NoTmux {
		t.Error("NoTmux = true, want false")
	}
	if cfg.ImagePrefix != "techdelight/claude-runner" {
		t.Errorf("ImagePrefix = %q, want default", cfg.ImagePrefix)
	}
}

func TestApplyAppConfig_AllFields(t *testing.T) {
	cfg := &Config{ImagePrefix: "techdelight/claude-runner"}
	ApplyAppConfig(cfg, AppConfig{
		DataDir:     strPtr("/data"),
		Debug:       boolPtr(true),
		NoTmux:      boolPtr(true),
		ImagePrefix: strPtr("my/image"),
	})
	if cfg.DataDir != "/data" {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, "/data")
	}
	if !cfg.Debug {
		t.Error("Debug = false, want true")
	}
	if !cfg.NoTmux {
		t.Error("NoTmux = false, want true")
	}
	if cfg.ImagePrefix != "my/image" {
		t.Errorf("ImagePrefix = %q, want %q", cfg.ImagePrefix, "my/image")
	}
}
