// Copyright (C) 2026 Techdelight BV

package core

import "testing"

func TestRequiredDocsHasEightEntries(t *testing.T) {
	docs := RequiredDocs()
	if len(docs) != 8 {
		t.Fatalf("RequiredDocs() returned %d docs, want 8", len(docs))
	}
}

func TestRequiredDocsFieldsPopulated(t *testing.T) {
	for _, d := range RequiredDocs() {
		if d.Name == "" {
			t.Errorf("doc with filename %q has empty Name", d.Filename)
		}
		if d.Filename == "" {
			t.Errorf("doc %q has empty Filename", d.Name)
		}
		if d.Description == "" {
			t.Errorf("doc %q has empty Description", d.Name)
		}
	}
}

func TestRequiredDocsFilenamesUnique(t *testing.T) {
	seen := make(map[string]bool)
	for _, d := range RequiredDocs() {
		if seen[d.Filename] {
			t.Errorf("duplicate filename %q", d.Filename)
		}
		seen[d.Filename] = true
	}
}

// The frontend keys the Vision panel off VISION.md, so its presence in the
// set is load-bearing rather than incidental.
func TestRequiredDocsContainsExpectedFilenames(t *testing.T) {
	want := []string{
		"README.md", "VISION.md", "ARCHITECTURE.md", "ROADMAP.md",
		"BACKLOG.md", "SPRINTS.md", "CHANGELOG.md", "CONTRIBUTING.md",
	}
	docs := RequiredDocs()
	if len(docs) != len(want) {
		t.Fatalf("got %d docs, want %d", len(docs), len(want))
	}
	for i, w := range want {
		if docs[i].Filename != w {
			t.Errorf("docs[%d].Filename = %q, want %q", i, docs[i].Filename, w)
		}
	}
}

// Callers must not be able to mutate the canonical set.
func TestRequiredDocsReturnsFreshSlice(t *testing.T) {
	first := RequiredDocs()
	first[0].Name = "mutated"

	if second := RequiredDocs(); second[0].Name == "mutated" {
		t.Error("mutating the returned slice affected a later call")
	}
}
