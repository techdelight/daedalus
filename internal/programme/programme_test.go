// Copyright (C) 2026 Techdelight BV

package programme

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/techdelight/daedalus/core"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "programmes")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	return New(dir)
}

func TestCreate_And_Read(t *testing.T) {
	// Arrange
	s := testStore(t)
	prog := core.Programme{
		Name:        "platform",
		Description: "Platform services",
		Projects:    []string{"api", "web"},
	}

	// Act
	if err := s.Create(prog); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Read("platform")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	// Assert
	if got.Name != "platform" {
		t.Errorf("Name = %q, want %q", got.Name, "platform")
	}
	if got.Description != "Platform services" {
		t.Errorf("Description = %q, want %q", got.Description, "Platform services")
	}
	if len(got.Projects) != 2 {
		t.Errorf("len(Projects) = %d, want 2", len(got.Projects))
	}
}

func TestCreate_DuplicateName(t *testing.T) {
	// Arrange
	s := testStore(t)
	prog := core.Programme{Name: "platform", Projects: []string{}}
	if err := s.Create(prog); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Act
	err := s.Create(prog)

	// Assert
	if err == nil {
		t.Fatal("Create duplicate: want error, got nil")
	}
}

func TestList_Empty(t *testing.T) {
	// Arrange
	s := testStore(t)

	// Act
	progs, err := s.List()

	// Assert
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if progs != nil {
		t.Errorf("progs = %v, want nil", progs)
	}
}

func TestList_Multiple(t *testing.T) {
	// Arrange
	s := testStore(t)
	for _, name := range []string{"charlie", "alpha", "bravo"} {
		prog := core.Programme{Name: name, Projects: []string{}}
		if err := s.Create(prog); err != nil {
			t.Fatalf("Create(%s): %v", name, err)
		}
	}

	// Act
	progs, err := s.List()

	// Assert
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(progs) != 3 {
		t.Fatalf("len = %d, want 3", len(progs))
	}
	if progs[0].Name != "alpha" {
		t.Errorf("progs[0].Name = %q, want %q", progs[0].Name, "alpha")
	}
	if progs[1].Name != "bravo" {
		t.Errorf("progs[1].Name = %q, want %q", progs[1].Name, "bravo")
	}
	if progs[2].Name != "charlie" {
		t.Errorf("progs[2].Name = %q, want %q", progs[2].Name, "charlie")
	}
}

func TestRemove(t *testing.T) {
	// Arrange
	s := testStore(t)
	prog := core.Programme{Name: "platform", Projects: []string{}}
	if err := s.Create(prog); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Act
	if err := s.Remove("platform"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	// Assert
	if _, err := s.Read("platform"); err == nil {
		t.Fatal("Read after Remove: want error, got nil")
	}
}

func TestRemove_NotFound(t *testing.T) {
	// Arrange
	s := testStore(t)

	// Act
	err := s.Remove("nonexistent")

	// Assert
	if err == nil {
		t.Fatal("Remove nonexistent: want error, got nil")
	}
}

func TestAddProject(t *testing.T) {
	// Arrange
	s := testStore(t)
	prog := core.Programme{Name: "platform", Projects: []string{"api"}}
	if err := s.Create(prog); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Act
	if err := s.AddProject("platform", "web"); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	// Assert
	got, err := s.Read("platform")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got.Projects) != 2 {
		t.Fatalf("len(Projects) = %d, want 2", len(got.Projects))
	}
	if got.Projects[1] != "web" {
		t.Errorf("Projects[1] = %q, want %q", got.Projects[1], "web")
	}
}

func TestAddProject_Duplicate(t *testing.T) {
	// Arrange
	s := testStore(t)
	prog := core.Programme{Name: "platform", Projects: []string{"api"}}
	if err := s.Create(prog); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Act
	err := s.AddProject("platform", "api")

	// Assert
	if err == nil {
		t.Fatal("AddProject duplicate: want error, got nil")
	}
}

func TestAddDep(t *testing.T) {
	// Arrange
	s := testStore(t)
	prog := core.Programme{Name: "platform", Projects: []string{"api", "web"}}
	if err := s.Create(prog); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Act
	err := s.AddDep("platform", "api", "web")

	// Assert
	if err != nil {
		t.Fatalf("AddDep: %v", err)
	}
	got, err := s.Read("platform")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got.Deps) != 1 {
		t.Fatalf("len(Deps) = %d, want 1", len(got.Deps))
	}
	if got.Deps[0].Upstream != "api" || got.Deps[0].Downstream != "web" {
		t.Errorf("Dep = %v, want api → web", got.Deps[0])
	}
}

func TestAddDep_CycleDetection(t *testing.T) {
	// Arrange
	s := testStore(t)
	prog := core.Programme{Name: "platform", Projects: []string{"a", "b"}}
	if err := s.Create(prog); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.AddDep("platform", "a", "b"); err != nil {
		t.Fatalf("AddDep(a→b): %v", err)
	}

	// Act
	err := s.AddDep("platform", "b", "a")

	// Assert
	if err == nil {
		t.Fatal("AddDep(b→a) creating cycle: want error, got nil")
	}
}

func TestAddDep_ProjectNotInProgramme(t *testing.T) {
	// Arrange
	s := testStore(t)
	prog := core.Programme{Name: "platform", Projects: []string{"api"}}
	if err := s.Create(prog); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Act
	err := s.AddDep("platform", "api", "missing")

	// Assert
	if err == nil {
		t.Fatal("AddDep with missing project: want error, got nil")
	}
}
