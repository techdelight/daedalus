// Copyright (C) 2026 Techdelight BV

package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Skill represents a skill file in the catalog.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Catalog provides operations on the shared skill catalog and per-user
// installed skills. All methods are safe for concurrent use from MCP handlers
// because each operation is a single filesystem call sequence.
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

// ListInstalled returns all skills installed in the user's skills directory.
func (c *Catalog) ListInstalled() ([]Skill, error) {
	return listSkillsIn(c.skillsDir)
}

// Read returns the full content of a catalog skill.
func (c *Catalog) Read(name string) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	path := filepath.Join(c.catalogDir, name+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading skill %q: %w", name, err)
	}
	return string(data), nil
}

// Install copies a skill from the catalog to the user's skills directory.
func (c *Catalog) Install(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	src := filepath.Join(c.catalogDir, name+".md")
	dst := filepath.Join(c.skillsDir, name+".md")

	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("reading catalog skill %q: %w", name, err)
	}
	if err := os.MkdirAll(c.skillsDir, 0755); err != nil {
		return fmt.Errorf("creating skills directory: %w", err)
	}
	if err := os.WriteFile(dst, data, 0644); err != nil {
		return fmt.Errorf("installing skill %q: %w", name, err)
	}
	return nil
}

// Uninstall removes a skill from the user's skills directory.
func (c *Catalog) Uninstall(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	path := filepath.Join(c.skillsDir, name+".md")
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("uninstalling skill %q: %w", name, err)
	}
	return nil
}

// Create saves a new skill to the catalog directory.
func (c *Catalog) Create(name, content string) error {
	if err := validateName(name); err != nil {
		return err
	}
	path := filepath.Join(c.catalogDir, name+".md")
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("skill %q already exists", name)
	}
	if err := os.MkdirAll(c.catalogDir, 0755); err != nil {
		return fmt.Errorf("creating catalog directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("creating skill %q: %w", name, err)
	}
	return nil
}

// Update overwrites an existing skill in the catalog directory.
func (c *Catalog) Update(name, content string) error {
	if err := validateName(name); err != nil {
		return err
	}
	path := filepath.Join(c.catalogDir, name+".md")
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
	path := filepath.Join(c.catalogDir, name+".md")
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("removing skill %q: %w", name, err)
	}
	return nil
}

// listSkillsIn reads all .md files from a directory and returns them as skills.
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
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		desc := readFirstLine(filepath.Join(dir, e.Name()))
		skills = append(skills, Skill{Name: name, Description: desc})
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

// validateName checks that a skill name is safe for use as a filename.
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
