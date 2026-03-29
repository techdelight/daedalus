// Copyright (C) 2026 Techdelight BV

package personas

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/techdelight/daedalus/core"
)

// Store provides CRUD operations for user-defined persona configurations.
// Each persona config is stored as a JSON file in the store directory.
type Store struct {
	dir string
}

// New creates a Store that manages persona configs in dir.
// It also performs a one-time migration from the legacy "agents" directory
// if "agents" exists and "personas" does not.
func New(dir string) *Store {
	migrateAgentsDir(dir)
	return &Store{dir: dir}
}

// migrateAgentsDir renames the legacy <data-dir>/agents/ directory to
// <data-dir>/personas/ for backward compatibility. Only runs when the
// old directory exists and the new one does not.
func migrateAgentsDir(personasDir string) {
	agentsDir := strings.TrimSuffix(personasDir, "personas") + "agents"
	if _, err := os.Stat(agentsDir); os.IsNotExist(err) {
		return
	}
	if _, err := os.Stat(personasDir); err == nil {
		return // personas dir already exists, skip
	}
	// Best effort rename
	os.Rename(agentsDir, personasDir)
}

// List returns all persona configurations in the store directory.
func (s *Store) List() ([]core.PersonaConfig, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading personas directory: %w", err)
	}
	var configs []core.PersonaConfig
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		cfg, err := s.Read(name)
		if err != nil {
			continue // skip unreadable files
		}
		configs = append(configs, cfg)
	}
	return configs, nil
}

// Read returns the persona configuration with the given name.
// The ClaudeMd field is loaded from a companion <name>.md file if it exists.
func (s *Store) Read(name string) (core.PersonaConfig, error) {
	path := filepath.Join(s.dir, name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return core.PersonaConfig{}, fmt.Errorf("reading persona %q: %w", name, err)
	}
	var cfg core.PersonaConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return core.PersonaConfig{}, fmt.Errorf("parsing persona %q: %w", name, err)
	}
	// Load companion markdown file
	mdPath := filepath.Join(s.dir, name+".md")
	if md, err := os.ReadFile(mdPath); err == nil {
		cfg.ClaudeMd = string(md)
	}
	return cfg, nil
}

// Create saves a new persona configuration. Returns an error if the name
// already exists or collides with a built-in runner.
func (s *Store) Create(cfg core.PersonaConfig) error {
	if err := core.ValidatePersonaName(cfg.Name); err != nil {
		return err
	}
	path := filepath.Join(s.dir, cfg.Name+".json")
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("persona %q already exists", cfg.Name)
	}
	return s.write(path, cfg)
}

// Update overwrites an existing persona configuration.
func (s *Store) Update(cfg core.PersonaConfig) error {
	if err := core.ValidatePersonaName(cfg.Name); err != nil {
		return err
	}
	path := filepath.Join(s.dir, cfg.Name+".json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("persona %q does not exist", cfg.Name)
	}
	return s.write(path, cfg)
}

// Remove deletes a persona configuration and its companion markdown file.
func (s *Store) Remove(name string) error {
	path := filepath.Join(s.dir, name+".json")
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("removing persona %q: %w", name, err)
	}
	// Best-effort removal of companion markdown file
	os.Remove(filepath.Join(s.dir, name+".md"))
	return nil
}

// write marshals cfg to JSON and writes it to path, creating the directory
// if needed. The ClaudeMd content is written to a companion <name>.md file.
func (s *Store) write(path string, cfg core.PersonaConfig) error {
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return fmt.Errorf("creating personas directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling persona config: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing persona %q: %w", cfg.Name, err)
	}
	// Write companion markdown file
	mdPath := strings.TrimSuffix(path, ".json") + ".md"
	if cfg.ClaudeMd != "" {
		if err := os.WriteFile(mdPath, []byte(cfg.ClaudeMd), 0644); err != nil {
			return fmt.Errorf("writing persona markdown %q: %w", cfg.Name, err)
		}
	} else {
		// Remove stale markdown file if content is empty
		os.Remove(mdPath)
	}
	return nil
}
