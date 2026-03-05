// Copyright (C) 2026 Techdelight BV

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAppConfig_FileNotFound(t *testing.T) {
	cfg, err := loadAppConfig(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DataDir != nil || cfg.Debug != nil || cfg.NoTmux != nil || cfg.ImagePrefix != nil {
		t.Error("expected all nil fields for missing file")
	}
}

func TestLoadAppConfig_ValidJSON(t *testing.T) {
	dir := t.TempDir()
	data := `{"data-dir": "/mnt/data", "debug": true, "no-tmux": false, "image-prefix": "custom/runner"}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadAppConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DataDir == nil || *cfg.DataDir != "/mnt/data" {
		t.Errorf("DataDir = %v, want /mnt/data", cfg.DataDir)
	}
	if cfg.Debug == nil || *cfg.Debug != true {
		t.Errorf("Debug = %v, want true", cfg.Debug)
	}
	if cfg.NoTmux == nil || *cfg.NoTmux != false {
		t.Errorf("NoTmux = %v, want false", cfg.NoTmux)
	}
	if cfg.ImagePrefix == nil || *cfg.ImagePrefix != "custom/runner" {
		t.Errorf("ImagePrefix = %v, want custom/runner", cfg.ImagePrefix)
	}
}

func TestLoadAppConfig_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{bad json"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := loadAppConfig(dir)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "parsing config file") {
		t.Errorf("error = %q, want mention of 'parsing config file'", err)
	}
}

func TestLoadAppConfig_EmptyObject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadAppConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DataDir != nil || cfg.Debug != nil || cfg.NoTmux != nil || cfg.ImagePrefix != nil {
		t.Error("expected all nil fields for empty object")
	}
}

func TestLoadAppConfig_UnknownKeysIgnored(t *testing.T) {
	dir := t.TempDir()
	data := `{"data-dir": "/data", "unknown-key": "value", "another": 42}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadAppConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DataDir == nil || *cfg.DataDir != "/data" {
		t.Errorf("DataDir = %v, want /data", cfg.DataDir)
	}
}
