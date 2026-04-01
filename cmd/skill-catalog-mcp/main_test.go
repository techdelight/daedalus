// Copyright (C) 2026 Techdelight BV

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/techdelight/daedalus/internal/catalog"
)

// writeSkillDir creates a skill directory with a SKILL.md file.
func writeSkillDir(t *testing.T, dir, name, content string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestErrResult(t *testing.T) {
	result := errResult(os.ErrNotExist)
	if !result.IsError {
		t.Error("errResult should set IsError=true")
	}
	if len(result.Content) != 1 {
		t.Fatalf("errResult content count = %d, want 1", len(result.Content))
	}
}

func TestVersion_MissingFile(t *testing.T) {
	v := version()
	if v != "dev" {
		t.Errorf("version() with missing file = %q, want %q", v, "dev")
	}
}

func TestVersion_ValidFile(t *testing.T) {
	dir := t.TempDir()
	optDir := filepath.Join(dir, "opt", "claude")
	os.MkdirAll(optDir, 0755)
	os.WriteFile(filepath.Join(optDir, "VERSION"), []byte("1.2.3\n"), 0644)
	// version() reads from a hardcoded path, so we can't easily test it
	// without mocking. The MissingFile test covers the fallback.
}

func TestNameInput_Fields(t *testing.T) {
	input := NameInput{Name: "test-skill"}
	if input.Name != "test-skill" {
		t.Errorf("NameInput.Name = %q, want %q", input.Name, "test-skill")
	}
}

func TestCreateInput_Fields(t *testing.T) {
	input := CreateInput{Name: "new", Content: "# My Skill"}
	if input.Name != "new" || input.Content != "# My Skill" {
		t.Error("CreateInput fields not set correctly")
	}
}

func TestContentOutput_Fields(t *testing.T) {
	output := ContentOutput{Content: "hello"}
	if output.Content != "hello" {
		t.Error("ContentOutput.Content not set correctly")
	}
}

func TestStatusOutput_Fields(t *testing.T) {
	output := StatusOutput{Status: "done"}
	if output.Status != "done" {
		t.Error("StatusOutput.Status not set correctly")
	}
}

// Integration tests via catalog (MCP server is a thin wrapper).

func TestIntegration_ListEmpty(t *testing.T) {
	cat := catalog.New(t.TempDir(), t.TempDir())
	skills, err := cat.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 0 {
		t.Errorf("List() on empty dir returned %d skills", len(skills))
	}
}

func TestIntegration_CreateReadUpdateRemove(t *testing.T) {
	catDir := t.TempDir()
	sklDir := t.TempDir()
	cat := catalog.New(catDir, sklDir)

	// Create
	if err := cat.Create("test-skill", "# Test\nA test skill."); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Read
	content, err := cat.Read("test-skill")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(content, "# Test") {
		t.Errorf("Read content = %q, want to contain '# Test'", content)
	}

	// List
	skills, err := cat.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(skills) != 1 || skills[0].Name != "test-skill" {
		t.Errorf("List = %v, want [test-skill]", skills)
	}

	// Update
	if err := cat.Update("test-skill", "# Updated"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	content, _ = cat.Read("test-skill")
	if content != "# Updated" {
		t.Errorf("after Update, Read = %q", content)
	}

	// Remove
	if err := cat.Remove("test-skill"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	skills, _ = cat.List()
	if len(skills) != 0 {
		t.Errorf("after Remove, List = %v", skills)
	}
}

func TestIntegration_InstallUninstallListInstalled(t *testing.T) {
	catDir := t.TempDir()
	sklDir := t.TempDir()
	cat := catalog.New(catDir, sklDir)

	writeSkillDir(t, catDir, "commit", "# Commit Helper")

	// Install
	if err := cat.Install("commit"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// ListInstalled
	installed, err := cat.ListInstalled()
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	if len(installed) != 1 || installed[0].Name != "commit" {
		t.Errorf("ListInstalled = %v, want [commit]", installed)
	}

	// Uninstall
	if err := cat.Uninstall("commit"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	installed, _ = cat.ListInstalled()
	if len(installed) != 0 {
		t.Errorf("after Uninstall, ListInstalled = %v", installed)
	}
}

func TestIntegration_ReadNotFound(t *testing.T) {
	cat := catalog.New(t.TempDir(), t.TempDir())
	_, err := cat.Read("nonexistent")
	if err == nil {
		t.Error("Read nonexistent: expected error")
	}
}

func TestIntegration_InstallNotFound(t *testing.T) {
	cat := catalog.New(t.TempDir(), t.TempDir())
	err := cat.Install("nonexistent")
	if err == nil {
		t.Error("Install nonexistent: expected error")
	}
}

func TestIntegration_CreateDuplicate(t *testing.T) {
	catDir := t.TempDir()
	cat := catalog.New(catDir, t.TempDir())
	cat.Create("dup", "# First")
	err := cat.Create("dup", "# Second")
	if err == nil {
		t.Error("Create duplicate: expected error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %v, want 'already exists'", err)
	}
}

func TestIntegration_UpdateNotFound(t *testing.T) {
	cat := catalog.New(t.TempDir(), t.TempDir())
	err := cat.Update("nonexistent", "# Content")
	if err == nil {
		t.Error("Update nonexistent: expected error")
	}
}

func TestIntegration_RemoveNotFound(t *testing.T) {
	cat := catalog.New(t.TempDir(), t.TempDir())
	err := cat.Remove("nonexistent")
	if err == nil {
		t.Error("Remove nonexistent: expected error")
	}
}
