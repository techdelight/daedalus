// Copyright (C) 2026 Techdelight BV

package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Skill represents a skill in the catalog.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// skillFile is the fixed filename inside each skill directory.
const skillFile = "SKILL.md"

// Catalog provides operations on the shared skill catalog and per-project
// installed skills. All methods are safe for concurrent use from MCP handlers
// because each operation is a single filesystem call sequence.
//
// Skills are stored as directories: {catalogDir}/{name}/SKILL.md
type Catalog struct {
	catalogDir string
	skillsDir  string
}

// New creates a Catalog that reads skills from catalogDir and installs them
// into skillsDir. Both directories must be absolute paths.
func New(catalogDir, skillsDir string) *Catalog {
	return &Catalog{
		catalogDir: catalogDir,
		skillsDir:  skillsDir,
	}
}

// List returns all skills in the catalog directory.
func (c *Catalog) List() ([]Skill, error) {
	return listSkillsIn(c.catalogDir)
}

// ListInstalled returns all skills installed in the project's skills directory.
func (c *Catalog) ListInstalled() ([]Skill, error) {
	return listSkillsIn(c.skillsDir)
}

// Read returns the full content of a catalog skill.
func (c *Catalog) Read(name string) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	path := filepath.Join(c.catalogDir, name, skillFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading skill %q: %w", name, err)
	}
	return string(data), nil
}

// Install copies a skill from the catalog to the project's skills directory.
func (c *Catalog) Install(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	src := filepath.Join(c.catalogDir, name, skillFile)
	dstDir := filepath.Join(c.skillsDir, name)

	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("reading catalog skill %q: %w", name, err)
	}
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return fmt.Errorf("creating skill directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, skillFile), data, 0644); err != nil {
		return fmt.Errorf("installing skill %q: %w", name, err)
	}
	return nil
}

// Uninstall removes a skill from the project's skills directory.
func (c *Catalog) Uninstall(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	dir := filepath.Join(c.skillsDir, name)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("uninstalling skill %q: %w", name, err)
	}
	// RemoveAll returns nil for nonexistent paths; check existence explicitly.
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("uninstalling skill %q: directory still exists", name)
	}
	return nil
}

// Create saves a new skill to the catalog directory.
func (c *Catalog) Create(name, content string) error {
	if err := validateName(name); err != nil {
		return err
	}
	dir := filepath.Join(c.catalogDir, name)
	if _, err := os.Stat(filepath.Join(dir, skillFile)); err == nil {
		return fmt.Errorf("skill %q already exists", name)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating skill directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, skillFile), []byte(content), 0644); err != nil {
		return fmt.Errorf("creating skill %q: %w", name, err)
	}
	return nil
}

// Update overwrites an existing skill in the catalog directory.
func (c *Catalog) Update(name, content string) error {
	if err := validateName(name); err != nil {
		return err
	}
	path := filepath.Join(c.catalogDir, name, skillFile)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("skill %q does not exist", name)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("updating skill %q: %w", name, err)
	}
	return nil
}

// Remove deletes a skill from the catalog directory.
func (c *Catalog) Remove(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	dir := filepath.Join(c.catalogDir, name)
	if _, err := os.Stat(filepath.Join(dir, skillFile)); os.IsNotExist(err) {
		return fmt.Errorf("removing skill %q: skill does not exist", name)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("removing skill %q: %w", name, err)
	}
	return nil
}

// listSkillsIn reads all skill directories from a directory and returns them.
// A skill directory contains a SKILL.md file.
func listSkillsIn(dir string) ([]Skill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading directory: %w", err)
	}
	var skills []Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillPath := filepath.Join(dir, e.Name(), skillFile)
		if _, err := os.Stat(skillPath); err != nil {
			continue // not a skill directory
		}
		desc := readFirstLine(skillPath)
		skills = append(skills, Skill{Name: e.Name(), Description: desc})
	}
	return skills, nil
}

// readFirstLine returns the first non-empty line of a file, stripped of leading
// markdown comment markers. Returns empty string on any error.
func readFirstLine(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Strip leading markdown heading markers
		line = strings.TrimLeft(line, "# ")
		return line
	}
	return ""
}

// validateName checks that a skill name is safe for use as a directory name.
// It rejects empty names, path separators, and directory traversal attempts.
func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("skill name must not be empty")
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("skill name must not contain path separators")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("skill name must not be a directory reference")
	}
	if strings.HasPrefix(name, ".") {
		return fmt.Errorf("skill name must not start with a dot")
	}
	return nil
}
