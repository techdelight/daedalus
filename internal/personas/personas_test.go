// Copyright (C) 2026 Techdelight BV

package personas

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/techdelight/daedalus/core"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "personas")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	return New(dir)
}

func TestCreate_And_Read(t *testing.T) {
	s := testStore(t)
	cfg := core.PersonaConfig{
		Name:        "reviewer",
		Description: "Code review specialist",
		BaseRunner:  "claude",
		ClaudeMd:    "You are a code reviewer.",
	}
	if err := s.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Read("reviewer")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Name != "reviewer" {
		t.Errorf("Name = %q, want %q", got.Name, "reviewer")
	}
	if got.BaseRunner != "claude" {
		t.Errorf("BaseRunner = %q, want %q", got.BaseRunner, "claude")
	}
	if got.ClaudeMd != "You are a code reviewer." {
		t.Errorf("ClaudeMd = %q, want %q", got.ClaudeMd, "You are a code reviewer.")
	}
}

func TestCreate_WritesMarkdownFile(t *testing.T) {
	s := testStore(t)
	cfg := core.PersonaConfig{
		Name:       "reviewer",
		BaseRunner: "claude",
		ClaudeMd:   "You are a code reviewer.",
	}
	if err := s.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify .md file exists with correct content
	mdPath := filepath.Join(s.dir, "reviewer.md")
	data, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("reading .md file: %v", err)
	}
	if string(data) != "You are a code reviewer." {
		t.Errorf("md content = %q, want %q", string(data), "You are a code reviewer.")
	}

	// Verify JSON does not contain claudeMd
	jsonData, err := os.ReadFile(filepath.Join(s.dir, "reviewer.json"))
	if err != nil {
		t.Fatalf("reading .json file: %v", err)
	}
	if strings.Contains(string(jsonData), "claudeMd") {
		t.Error("JSON should not contain claudeMd field")
	}
}

func TestCreate_NoMarkdownWhenEmpty(t *testing.T) {
	s := testStore(t)
	cfg := core.PersonaConfig{
		Name:       "simple",
		BaseRunner: "claude",
	}
	if err := s.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}

	mdPath := filepath.Join(s.dir, "simple.md")
	if _, err := os.Stat(mdPath); !os.IsNotExist(err) {
		t.Error(".md file should not exist when ClaudeMd is empty")
	}

	// Read should return empty ClaudeMd
	got, err := s.Read("simple")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.ClaudeMd != "" {
		t.Errorf("ClaudeMd = %q, want empty", got.ClaudeMd)
	}
}

func TestRemove_DeletesMarkdownFile(t *testing.T) {
	s := testStore(t)
	cfg := core.PersonaConfig{
		Name:       "reviewer",
		BaseRunner: "claude",
		ClaudeMd:   "Review prompt.",
	}
	if err := s.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Remove("reviewer"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	mdPath := filepath.Join(s.dir, "reviewer.md")
	if _, err := os.Stat(mdPath); !os.IsNotExist(err) {
		t.Error(".md file should be deleted on Remove")
	}
}

func TestUpdate_UpdatesMarkdownFile(t *testing.T) {
	s := testStore(t)
	cfg := core.PersonaConfig{Name: "reviewer", BaseRunner: "claude", ClaudeMd: "v1"}
	if err := s.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}
	cfg.ClaudeMd = "v2"
	if err := s.Update(cfg); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := s.Read("reviewer")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.ClaudeMd != "v2" {
		t.Errorf("ClaudeMd = %q, want %q", got.ClaudeMd, "v2")
	}
}

func TestCreate_DuplicateName(t *testing.T) {
	s := testStore(t)
	cfg := core.PersonaConfig{Name: "reviewer", BaseRunner: "claude"}
	if err := s.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Create(cfg); err == nil {
		t.Fatal("Create duplicate: want error, got nil")
	}
}

func TestCreate_RejectsBuiltinName(t *testing.T) {
	s := testStore(t)
	cfg := core.PersonaConfig{Name: "claude", BaseRunner: "claude"}
	if err := s.Create(cfg); err == nil {
		t.Fatal("Create(claude): want error, got nil")
	}
}

func TestCreate_RejectsInvalidName(t *testing.T) {
	s := testStore(t)
	cfg := core.PersonaConfig{Name: "-bad", BaseRunner: "claude"}
	if err := s.Create(cfg); err == nil {
		t.Fatal("Create(-bad): want error, got nil")
	}
}

func TestUpdate(t *testing.T) {
	s := testStore(t)
	cfg := core.PersonaConfig{Name: "reviewer", BaseRunner: "claude", Description: "v1"}
	if err := s.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}
	cfg.Description = "v2"
	if err := s.Update(cfg); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := s.Read("reviewer")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Description != "v2" {
		t.Errorf("Description = %q, want %q", got.Description, "v2")
	}
}

func TestUpdate_NotFound(t *testing.T) {
	s := testStore(t)
	cfg := core.PersonaConfig{Name: "nonexistent", BaseRunner: "claude"}
	if err := s.Update(cfg); err == nil {
		t.Fatal("Update nonexistent: want error, got nil")
	}
}

func TestRemove(t *testing.T) {
	s := testStore(t)
	cfg := core.PersonaConfig{Name: "reviewer", BaseRunner: "claude"}
	if err := s.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Remove("reviewer"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := s.Read("reviewer"); err == nil {
		t.Fatal("Read after Remove: want error, got nil")
	}
}

func TestRemove_NotFound(t *testing.T) {
	s := testStore(t)
	if err := s.Remove("nonexistent"); err == nil {
		t.Fatal("Remove nonexistent: want error, got nil")
	}
}

func TestList_Empty(t *testing.T) {
	s := testStore(t)
	configs, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(configs) != 0 {
		t.Errorf("len = %d, want 0", len(configs))
	}
}

func TestList_WithConfigs(t *testing.T) {
	s := testStore(t)
	for _, name := range []string{"alpha", "beta"} {
		cfg := core.PersonaConfig{Name: name, BaseRunner: "claude"}
		if err := s.Create(cfg); err != nil {
			t.Fatalf("Create(%s): %v", name, err)
		}
	}
	configs, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(configs) != 2 {
		t.Fatalf("len = %d, want 2", len(configs))
	}
}

func TestList_MissingDir(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "nonexistent"))
	configs, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if configs != nil {
		t.Errorf("configs = %v, want nil", configs)
	}
}

func TestRead_NotFound(t *testing.T) {
	s := testStore(t)
	_, err := s.Read("nonexistent")
	if err == nil {
		t.Fatal("Read nonexistent: want error, got nil")
	}
}

func TestCreate_WithEnvAndSettings(t *testing.T) {
	s := testStore(t)
	cfg := core.PersonaConfig{
		Name:       "custom",
		BaseRunner: "copilot",
		Env:        map[string]string{"FOO": "bar"},
		Settings:   []byte(`{"permissions":{"allow":["Read"]}}`),
	}
	if err := s.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Read("custom")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Env["FOO"] != "bar" {
		t.Errorf("Env[FOO] = %q, want %q", got.Env["FOO"], "bar")
	}
	if got.Settings == nil {
		t.Error("Settings = nil, want non-nil")
	}
}

func TestMigrateAgentsDir(t *testing.T) {
	base := t.TempDir()
	agentsDir := filepath.Join(base, "agents")
	personasDir := filepath.Join(base, "personas")

	// Create old agents dir with a file
	os.MkdirAll(agentsDir, 0755)
	os.WriteFile(filepath.Join(agentsDir, "reviewer.json"),
		[]byte(`{"name":"reviewer","baseRunner":"claude"}`), 0644)

	// New() should migrate
	store := New(personasDir)

	// The old dir should be gone, new dir should exist
	if _, err := os.Stat(agentsDir); !os.IsNotExist(err) {
		t.Error("agents directory should have been renamed")
	}
	if _, err := os.Stat(personasDir); err != nil {
		t.Fatalf("personas directory should exist after migration: %v", err)
	}

	// Should be able to read the migrated config
	cfg, err := store.Read("reviewer")
	if err != nil {
		t.Fatalf("Read migrated config: %v", err)
	}
	if cfg.Name != "reviewer" {
		t.Errorf("Name = %q, want %q", cfg.Name, "reviewer")
	}
}
