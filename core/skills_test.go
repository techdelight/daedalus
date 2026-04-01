// Copyright (C) 2026 Techdelight BV

package core

import (
	"strings"
	"testing"
)

func TestStarterSkills_ReturnsNonEmptyMap(t *testing.T) {
	// Act
	skills, err := StarterSkills()

	// Assert
	if err != nil {
		t.Fatalf("StarterSkills() returned error: %v", err)
	}
	if len(skills) == 0 {
		t.Fatal("StarterSkills() returned empty map, want at least one entry")
	}
}

func TestStarterSkills_KeysEndInMd(t *testing.T) {
	// Arrange
	skills, err := StarterSkills()
	if err != nil {
		t.Fatalf("StarterSkills() returned error: %v", err)
	}

	// Act / Assert
	for name := range skills {
		if !strings.HasSuffix(name, ".md") {
			t.Errorf("skill key %q does not end in .md", name)
		}
	}
}

func TestStarterSkills_ValuesAreNonEmpty(t *testing.T) {
	// Arrange
	skills, err := StarterSkills()
	if err != nil {
		t.Fatalf("StarterSkills() returned error: %v", err)
	}

	// Act / Assert
	for name, content := range skills {
		if len(content) == 0 {
			t.Errorf("skill %q has empty content", name)
		}
	}
}

func TestStarterSkills_KeysHaveNoDirectoryPrefix(t *testing.T) {
	// Arrange
	skills, err := StarterSkills()
	if err != nil {
		t.Fatalf("StarterSkills() returned error: %v", err)
	}

	// Act / Assert
	for name := range skills {
		if strings.Contains(name, "/") {
			t.Errorf("skill key %q contains directory separator, want bare filename", name)
		}
	}
}
