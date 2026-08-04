// Copyright (C) 2026 Techdelight BV

package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLintHeadings_WellFormed(t *testing.T) {
	content := `# Roadmap

## Milestones

### Milestone 1: Foundations (Done)

Groundwork.

### Milestone 2: Rework (In Progress)

### Milestone 3: Scale
`
	if got := LintHeadings("ROADMAP.md", content); len(got) != 0 {
		t.Errorf("LintHeadings() = %v, want none", got)
	}
}

func TestLintHeadings_SectionHeadingsAreNotEntries(t *testing.T) {
	// "## Milestones" and "## Sprint History" name the concept, not an
	// instance — they carry no number and must not be flagged.
	content := `## Milestones

## Sprint History

## Current Sprint
`
	if got := LintHeadings("ROADMAP.md", content); len(got) != 0 {
		t.Errorf("LintHeadings() = %v, want none", got)
	}
}

func TestLintHeadings_WrongLevel(t *testing.T) {
	content := "## Milestone 4: Layered Architecture (In Progress)\n"
	got := LintHeadings("ROADMAP.md", content)
	if len(got) != 1 {
		t.Fatalf("LintHeadings() returned %d findings, want 1: %v", len(got), got)
	}
	if got[0].Severity != SeverityError {
		t.Errorf("severity = %q, want %q", got[0].Severity, SeverityError)
	}
	if !strings.Contains(got[0].Message, "line 1") || !strings.Contains(got[0].Message, "milestone") {
		t.Errorf("message = %q, want it to name the line and kind", got[0].Message)
	}
}

func TestLintHeadings_MissingColon(t *testing.T) {
	content := "# Roadmap\n\n### Milestone 4 Layered Architecture\n"
	got := LintHeadings("ROADMAP.md", content)
	if len(got) != 1 {
		t.Fatalf("LintHeadings() returned %d findings, want 1: %v", len(got), got)
	}
	if !strings.Contains(got[0].Message, "line 3") {
		t.Errorf("message = %q, want it to point at line 3", got[0].Message)
	}
}

func TestLintHeadings_BadSprintHeading(t *testing.T) {
	content := "## Sprint History\n\n### Sprint 40 Coordinator\n"
	got := LintHeadings("SPRINTS.md", content)
	if len(got) != 1 {
		t.Fatalf("LintHeadings() returned %d findings, want 1: %v", len(got), got)
	}
	if !strings.Contains(got[0].Message, "sprint") {
		t.Errorf("message = %q, want it to name the sprint kind", got[0].Message)
	}
}

func TestLintHeadings_FencedBlockIsSkipped(t *testing.T) {
	// A phasing diagram inside a fence draws milestone-shaped text that is
	// prose about the arc, not an entry in it — even a fenced line that would
	// otherwise parse-fail must be left alone.
	content := "## Phasing\n\n```\n### Milestone 9 broken heading\nM4 (In Progress) --> M5\n```\n\n### Milestone 4: Real (In Progress)\n"
	if got := LintHeadings("ROADMAP.md", content); len(got) != 0 {
		t.Errorf("LintHeadings() = %v, want none (fenced content is skipped)", got)
	}
}

func TestLintHeadings_MultipleFindings(t *testing.T) {
	content := "### Milestone 1 no colon\n### Milestone 2: Fine (Done)\n#### Sprint 3: Wrong level (v1.0.0)\n"
	got := LintHeadings("ROADMAP.md", content)
	if len(got) != 2 {
		t.Fatalf("LintHeadings() returned %d findings, want 2: %v", len(got), got)
	}
}

// The linter must pass this repo's own documents clean, so a future drift in
// Daedalus's own ROADMAP.md or SPRINTS.md fails here rather than silently
// dropping an entry from the arc.
func TestLintHeadings_RepoDocsAreClean(t *testing.T) {
	for _, doc := range []string{"ROADMAP.md", "SPRINTS.md"} {
		data, err := os.ReadFile(filepath.Join("..", doc))
		if err != nil {
			t.Fatalf("reading repo %s: %v", doc, err)
		}
		if got := LintHeadings(doc, string(data)); len(got) != 0 {
			t.Errorf("repo %s has malformed headings: %v", doc, got)
		}
	}
}
