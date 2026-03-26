// Copyright (C) 2026 Techdelight BV

package agents

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/techdelight/daedalus/core"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "agents")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	return New(dir)
}

func TestCreate_And_Read(t *testing.T) {
	s := testStore(t)
	cfg := core.AgentConfig{
		Name:        "reviewer",
		Description: "Code review specialist",
		BaseAgent:   "claude",
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
	if got.BaseAgent != "claude" {
		t.Errorf("BaseAgent = %q, want %q", got.BaseAgent, "claude")
	}
	if got.ClaudeMd != "You are a code reviewer." {
		t.Errorf("ClaudeMd = %q, want %q", got.ClaudeMd, "You are a code reviewer.")
	}
}

func TestCreate_DuplicateName(t *testing.T) {
	s := testStore(t)
	cfg := core.AgentConfig{Name: "reviewer", BaseAgent: "claude"}
	if err := s.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Create(cfg); err == nil {
		t.Fatal("Create duplicate: want error, got nil")
	}
}

func TestCreate_RejectsBuiltinName(t *testing.T) {
	s := testStore(t)
	cfg := core.AgentConfig{Name: "claude", BaseAgent: "claude"}
	if err := s.Create(cfg); err == nil {
		t.Fatal("Create(claude): want error, got nil")
	}
}

func TestCreate_RejectsInvalidName(t *testing.T) {
	s := testStore(t)
	cfg := core.AgentConfig{Name: "-bad", BaseAgent: "claude"}
	if err := s.Create(cfg); err == nil {
		t.Fatal("Create(-bad): want error, got nil")
	}
}

func TestUpdate(t *testing.T) {
	s := testStore(t)
	cfg := core.AgentConfig{Name: "reviewer", BaseAgent: "claude", Description: "v1"}
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
	cfg := core.AgentConfig{Name: "nonexistent", BaseAgent: "claude"}
	if err := s.Update(cfg); err == nil {
		t.Fatal("Update nonexistent: want error, got nil")
	}
}

func TestRemove(t *testing.T) {
	s := testStore(t)
	cfg := core.AgentConfig{Name: "reviewer", BaseAgent: "claude"}
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
		cfg := core.AgentConfig{Name: name, BaseAgent: "claude"}
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
	cfg := core.AgentConfig{
		Name:      "custom",
		BaseAgent: "copilot",
		Env:       map[string]string{"FOO": "bar"},
		Settings:  []byte(`{"permissions":{"allow":["Read"]}}`),
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
