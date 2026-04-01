// Copyright (C) 2026 Techdelight BV

package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupDirs(t *testing.T) (catalogDir, skillsDir string) {
	t.Helper()
	catalogDir = filepath.Join(t.TempDir(), "skills")
	skillsDir = filepath.Join(t.TempDir(), "skills")
	if err := os.MkdirAll(catalogDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	return catalogDir, skillsDir
}

// writeSkill creates a skill directory with a SKILL.md file.
func writeSkill(t *testing.T, dir, name, content string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, skillFile), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestList_Empty(t *testing.T) {
	catDir, sklDir := setupDirs(t)
	c := New(catDir, sklDir)

	skills, err := c.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("List() = %v, want empty", skills)
	}
}

func TestList_WithSkills(t *testing.T) {
	catDir, sklDir := setupDirs(t)
	writeSkill(t, catDir, "commit", "# Commit Helper\nHelps with commits.")
	writeSkill(t, catDir, "review", "# Code Review\nReviews code.")
	c := New(catDir, sklDir)

	skills, err := c.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("List() returned %d skills, want 2", len(skills))
	}

	names := map[string]bool{}
	for _, s := range skills {
		names[s.Name] = true
	}
	if !names["commit"] || !names["review"] {
		t.Errorf("List() names = %v, want commit and review", names)
	}
}

func TestList_IgnoresNonSkillDirs(t *testing.T) {
	catDir, sklDir := setupDirs(t)
	writeSkill(t, catDir, "valid", "# Valid skill")
	// Create a directory without SKILL.md — should be ignored
	os.MkdirAll(filepath.Join(catDir, "empty-dir"), 0755)
	// Create a plain file — should be ignored
	os.WriteFile(filepath.Join(catDir, "notes.txt"), []byte("not a skill"), 0644)
	c := New(catDir, sklDir)

	skills, err := c.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(skills) != 1 {
		t.Errorf("List() returned %d skills, want 1", len(skills))
	}
}

func TestList_MissingDir(t *testing.T) {
	c := New("/nonexistent/catalog", "/nonexistent/skills")
	skills, err := c.List()
	if err != nil {
		t.Fatalf("List() error = %v, want nil for missing dir", err)
	}
	if len(skills) != 0 {
		t.Errorf("List() = %v, want empty", skills)
	}
}

func TestRead(t *testing.T) {
	catDir, sklDir := setupDirs(t)
	writeSkill(t, catDir, "commit", "# Commit\nDo a commit.")
	c := New(catDir, sklDir)

	content, err := c.Read("commit")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if content != "# Commit\nDo a commit." {
		t.Errorf("Read() = %q, want %q", content, "# Commit\nDo a commit.")
	}
}

func TestRead_NotFound(t *testing.T) {
	catDir, sklDir := setupDirs(t)
	c := New(catDir, sklDir)

	_, err := c.Read("nonexistent")
	if err == nil {
		t.Error("Read() expected error for missing skill")
	}
}

func TestInstall(t *testing.T) {
	catDir, sklDir := setupDirs(t)
	writeSkill(t, catDir, "commit", "# Commit\nDo a commit.")
	c := New(catDir, sklDir)

	if err := c.Install("commit"); err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(sklDir, "commit", skillFile))
	if err != nil {
		t.Fatalf("installed file not found: %v", err)
	}
	if string(data) != "# Commit\nDo a commit." {
		t.Errorf("installed content = %q, want original", string(data))
	}
}

func TestInstall_CreatesSkillsDir(t *testing.T) {
	catDir, _ := setupDirs(t)
	sklDir := filepath.Join(t.TempDir(), "new", "skills")
	writeSkill(t, catDir, "commit", "# Commit")
	c := New(catDir, sklDir)

	if err := c.Install("commit"); err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(sklDir, "commit", skillFile)); err != nil {
		t.Errorf("installed file not found: %v", err)
	}
}

func TestInstall_NotInCatalog(t *testing.T) {
	catDir, sklDir := setupDirs(t)
	c := New(catDir, sklDir)

	err := c.Install("nonexistent")
	if err == nil {
		t.Error("Install() expected error for missing catalog skill")
	}
}

func TestUninstall(t *testing.T) {
	catDir, sklDir := setupDirs(t)
	writeSkill(t, sklDir, "commit", "# Commit")
	c := New(catDir, sklDir)

	if err := c.Uninstall("commit"); err != nil {
		t.Fatalf("Uninstall() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(sklDir, "commit")); !os.IsNotExist(err) {
		t.Error("Uninstall() did not remove the skill directory")
	}
}

func TestUninstall_NotInstalled(t *testing.T) {
	catDir, sklDir := setupDirs(t)
	c := New(catDir, sklDir)

	// RemoveAll on a nonexistent path succeeds silently — no error expected.
	if err := c.Uninstall("nonexistent"); err != nil {
		t.Errorf("Uninstall() error = %v, want nil for missing skill", err)
	}
}

func TestCreate(t *testing.T) {
	catDir, sklDir := setupDirs(t)
	c := New(catDir, sklDir)

	if err := c.Create("new-skill", "# New Skill\nDoes something."); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(catDir, "new-skill", skillFile))
	if err != nil {
		t.Fatalf("created file not found: %v", err)
	}
	if string(data) != "# New Skill\nDoes something." {
		t.Errorf("created content = %q", string(data))
	}
}

func TestCreate_AlreadyExists(t *testing.T) {
	catDir, sklDir := setupDirs(t)
	writeSkill(t, catDir, "existing", "# Existing")
	c := New(catDir, sklDir)

	err := c.Create("existing", "# New content")
	if err == nil {
		t.Error("Create() expected error for existing skill")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("Create() error = %v, want 'already exists'", err)
	}
}

func TestUpdate(t *testing.T) {
	catDir, sklDir := setupDirs(t)
	writeSkill(t, catDir, "commit", "# Old content")
	c := New(catDir, sklDir)

	if err := c.Update("commit", "# New content"); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(catDir, "commit", skillFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# New content" {
		t.Errorf("updated content = %q", string(data))
	}
}

func TestUpdate_NotFound(t *testing.T) {
	catDir, sklDir := setupDirs(t)
	c := New(catDir, sklDir)

	err := c.Update("nonexistent", "# Content")
	if err == nil {
		t.Error("Update() expected error for missing skill")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("Update() error = %v, want 'does not exist'", err)
	}
}

func TestRemove(t *testing.T) {
	catDir, sklDir := setupDirs(t)
	writeSkill(t, catDir, "commit", "# Commit")
	c := New(catDir, sklDir)

	if err := c.Remove("commit"); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(catDir, "commit")); !os.IsNotExist(err) {
		t.Error("Remove() did not delete the skill directory")
	}
}

func TestRemove_NotFound(t *testing.T) {
	catDir, sklDir := setupDirs(t)
	c := New(catDir, sklDir)

	err := c.Remove("nonexistent")
	if err == nil {
		t.Error("Remove() expected error for missing skill")
	}
}

func TestListInstalled(t *testing.T) {
	catDir, sklDir := setupDirs(t)
	writeSkill(t, sklDir, "commit", "# Commit Helper")
	writeSkill(t, sklDir, "review", "# Code Review")
	c := New(catDir, sklDir)

	skills, err := c.ListInstalled()
	if err != nil {
		t.Fatalf("ListInstalled() error = %v", err)
	}
	if len(skills) != 2 {
		t.Errorf("ListInstalled() returned %d skills, want 2", len(skills))
	}
}

func TestValidateName_Invalid(t *testing.T) {
	tests := []struct {
		name    string
		wantErr string
	}{
		{"", "must not be empty"},
		{"../escape", "path separators"},
		{"sub/dir", "path separators"},
		{"back\\slash", "path separators"},
		{".", "directory reference"},
		{"..", "directory reference"},
		{".hidden", "must not start with a dot"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateName(tc.name)
			if err == nil {
				t.Fatal("validateName() expected error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("validateName(%q) = %v, want error containing %q", tc.name, err, tc.wantErr)
			}
		})
	}
}

func TestValidateName_Valid(t *testing.T) {
	names := []string{"commit", "git-workflow", "test_runner", "my-skill-2"}
	for _, name := range names {
		if err := validateName(name); err != nil {
			t.Errorf("validateName(%q) = %v, want nil", name, err)
		}
	}
}

func TestReadFirstLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")

	tests := []struct {
		content string
		want    string
	}{
		{"# My Skill\nDescription here.", "My Skill"},
		{"## Second Level\nContent.", "Second Level"},
		{"\n\n# After blanks\n", "After blanks"},
		{"No heading\nJust text.", "No heading"},
		{"", ""},
	}
	for _, tc := range tests {
		os.WriteFile(path, []byte(tc.content), 0644)
		got := readFirstLine(path)
		if got != tc.want {
			t.Errorf("readFirstLine(%q) = %q, want %q", tc.content, got, tc.want)
		}
	}
}
