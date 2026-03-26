// Copyright (C) 2026 Techdelight BV

package agents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/techdelight/daedalus/core"
)

// Store provides CRUD operations for user-defined agent configurations.
// Each agent config is stored as a JSON file in the store directory.
type Store struct {
	dir string
}

// New creates a Store that manages agent configs in dir.
func New(dir string) *Store {
	return &Store{dir: dir}
}

// List returns all agent configurations in the store directory.
func (s *Store) List() ([]core.AgentConfig, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading agents directory: %w", err)
	}
	var configs []core.AgentConfig
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

// Read returns the agent configuration with the given name.
func (s *Store) Read(name string) (core.AgentConfig, error) {
	path := filepath.Join(s.dir, name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return core.AgentConfig{}, fmt.Errorf("reading agent config %q: %w", name, err)
	}
	var cfg core.AgentConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return core.AgentConfig{}, fmt.Errorf("parsing agent config %q: %w", name, err)
	}
	return cfg, nil
}

// Create saves a new agent configuration. Returns an error if the name
// already exists or collides with a built-in agent.
func (s *Store) Create(cfg core.AgentConfig) error {
	if err := core.ValidateAgentConfigName(cfg.Name); err != nil {
		return err
	}
	path := filepath.Join(s.dir, cfg.Name+".json")
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("agent config %q already exists", cfg.Name)
	}
	return s.write(path, cfg)
}

// Update overwrites an existing agent configuration.
func (s *Store) Update(cfg core.AgentConfig) error {
	if err := core.ValidateAgentConfigName(cfg.Name); err != nil {
		return err
	}
	path := filepath.Join(s.dir, cfg.Name+".json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("agent config %q does not exist", cfg.Name)
	}
	return s.write(path, cfg)
}

// Remove deletes an agent configuration.
func (s *Store) Remove(name string) error {
	path := filepath.Join(s.dir, name+".json")
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("removing agent config %q: %w", name, err)
	}
	return nil
}

// write marshals cfg to JSON and writes it to path, creating the directory
// if needed.
func (s *Store) write(path string, cfg core.AgentConfig) error {
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return fmt.Errorf("creating agents directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling agent config: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing agent config %q: %w", cfg.Name, err)
	}
	return nil
}
