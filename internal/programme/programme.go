// Copyright (C) 2026 Techdelight BV

package programme

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/techdelight/daedalus/core"
)

// Store manages programme definitions on the filesystem.
type Store struct {
	Dir string
}

// New creates a Store for the given directory.
func New(dir string) *Store {
	return &Store{Dir: dir}
}

// List returns all programmes sorted by name.
func (s *Store) List() ([]core.Programme, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("listing programmes: %w", err)
	}
	var progs []core.Programme
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		p, err := s.Read(name)
		if err != nil {
			continue
		}
		progs = append(progs, p)
	}
	sort.Slice(progs, func(i, j int) bool { return progs[i].Name < progs[j].Name })
	return progs, nil
}

// Read loads a programme by name.
func (s *Store) Read(name string) (core.Programme, error) {
	path := filepath.Join(s.Dir, name+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		return core.Programme{}, fmt.Errorf("reading programme %q: %w", name, err)
	}
	var p core.Programme
	if err := json.Unmarshal(b, &p); err != nil {
		return core.Programme{}, fmt.Errorf("parsing programme %q: %w", name, err)
	}
	return p, nil
}

// Create stores a new programme. Returns error if it already exists.
func (s *Store) Create(p core.Programme) error {
	if err := core.ValidateProgrammeName(p.Name); err != nil {
		return err
	}
	path := filepath.Join(s.Dir, p.Name+".json")
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("programme %q already exists", p.Name)
	}
	return s.write(p)
}

// Update overwrites an existing programme. Returns error if it doesn't exist.
func (s *Store) Update(p core.Programme) error {
	path := filepath.Join(s.Dir, p.Name+".json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("programme %q not found", p.Name)
	}
	return s.write(p)
}

// Remove deletes a programme by name.
func (s *Store) Remove(name string) error {
	path := filepath.Join(s.Dir, name+".json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("programme %q not found", name)
	}
	return os.Remove(path)
}

// AddProject adds a project to a programme. Returns error if already present.
func (s *Store) AddProject(programmeName, projectName string) error {
	p, err := s.Read(programmeName)
	if err != nil {
		return err
	}
	for _, existing := range p.Projects {
		if existing == projectName {
			return fmt.Errorf("project %q already in programme %q", projectName, programmeName)
		}
	}
	p.Projects = append(p.Projects, projectName)
	return s.write(p)
}

// AddDep adds a dependency edge. Returns error if it creates a cycle.
func (s *Store) AddDep(programmeName, upstream, downstream string) error {
	p, err := s.Read(programmeName)
	if err != nil {
		return err
	}
	// Check both projects are members
	hasUp, hasDown := false, false
	for _, proj := range p.Projects {
		if proj == upstream {
			hasUp = true
		}
		if proj == downstream {
			hasDown = true
		}
	}
	if !hasUp {
		return fmt.Errorf("project %q is not in programme %q", upstream, programmeName)
	}
	if !hasDown {
		return fmt.Errorf("project %q is not in programme %q", downstream, programmeName)
	}
	// Check for duplicate edge
	for _, e := range p.Deps {
		if e.Upstream == upstream && e.Downstream == downstream {
			return fmt.Errorf("dependency %s → %s already exists", upstream, downstream)
		}
	}
	// Add edge and check for cycles
	newEdge := core.DependencyEdge{Upstream: upstream, Downstream: downstream}
	newDeps := append(p.Deps, newEdge)
	g := core.NewDependencyGraph(p.Projects, newDeps)
	if g.DetectCycles() {
		return fmt.Errorf("adding %s → %s would create a cycle", upstream, downstream)
	}
	p.Deps = newDeps
	return s.write(p)
}

func (s *Store) write(p core.Programme) error {
	if err := os.MkdirAll(s.Dir, 0755); err != nil {
		return fmt.Errorf("creating programmes directory: %w", err)
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling programme: %w", err)
	}
	b = append(b, '\n')
	return os.WriteFile(filepath.Join(s.Dir, p.Name+".json"), b, 0644)
}
