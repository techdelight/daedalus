// Copyright (C) 2026 Techdelight BV

package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseMilestonesReadsNumberTitleAndStatus(t *testing.T) {
	md := `# Roadmap

## Milestones

### Milestone 1: Autonomous Container Runtime (Done)

Single-command project launch.

### Milestone 4: Layered Architecture (In Progress)

Introduce daedalus-runner.

### Milestone 5: Self-Sustaining Operations

- Shared Docker volumes
- Homebrew distribution
`
	got := ParseMilestones(md)
	if len(got) != 3 {
		t.Fatalf("parsed %d milestones, want 3: %+v", len(got), got)
	}

	want := []struct {
		number int
		title  string
		status Status
	}{
		{1, "Autonomous Container Runtime", StatusDone},
		{4, "Layered Architecture", StatusInProgress},
		{5, "Self-Sustaining Operations", StatusPlanned},
	}
	for i, w := range want {
		if got[i].Number != w.number {
			t.Errorf("milestone %d: Number = %d, want %d", i, got[i].Number, w.number)
		}
		if got[i].Title != w.title {
			t.Errorf("milestone %d: Title = %q, want %q", i, got[i].Title, w.title)
		}
		if got[i].Status != w.status {
			t.Errorf("milestone %d: Status = %q, want %q", i, got[i].Status, w.status)
		}
	}
}

// A heading with no parenthetical is a commitment not yet started, and must
// read as Planned rather than as an empty string the caller has to interpret.
func TestParseMilestonesDefaultsToPlanned(t *testing.T) {
	got := ParseMilestones("### Milestone 9: Something Later\n")
	if len(got) != 1 {
		t.Fatalf("parsed %d milestones, want 1", len(got))
	}
	if got[0].Status != StatusPlanned {
		t.Errorf("Status = %q, want %q", got[0].Status, StatusPlanned)
	}
	if got[0].Status == StatusPending {
		t.Error("planned milestone must not collapse to the sprint-side StatusPending")
	}
}

// The status group is pinned to (Done|In Progress) precisely so a title that
// ends in an unrelated parenthetical survives intact. A permissive group would
// parse the title as "Rework" with status "Phase 2".
func TestParseMilestonesKeepsUnrecognisedParentheticalInTitle(t *testing.T) {
	got := ParseMilestones("### Milestone 2: Rework (Phase 2)\n")
	if len(got) != 1 {
		t.Fatalf("parsed %d milestones, want 1", len(got))
	}
	if got[0].Title != "Rework (Phase 2)" {
		t.Errorf("Title = %q, want %q", got[0].Title, "Rework (Phase 2)")
	}
	if got[0].Status != StatusPlanned {
		t.Errorf("Status = %q, want %q", got[0].Status, StatusPlanned)
	}
}

// ...and a real status still parses when the title carries one too.
func TestParseMilestonesStatusWinsAfterParentheticalTitle(t *testing.T) {
	got := ParseMilestones("### Milestone 2: Rework (Phase 2) (Done)\n")
	if len(got) != 1 {
		t.Fatalf("parsed %d milestones, want 1", len(got))
	}
	if got[0].Title != "Rework (Phase 2)" {
		t.Errorf("Title = %q, want %q", got[0].Title, "Rework (Phase 2)")
	}
	if got[0].Status != StatusDone {
		t.Errorf("Status = %q, want %q", got[0].Status, StatusDone)
	}
}

func TestParseMilestonesCapturesDescription(t *testing.T) {
	md := `### Milestone 1: First (Done)

Single-command project launch with Docker isolation.

### Milestone 2: Second
`
	got := ParseMilestones(md)
	if len(got) != 2 {
		t.Fatalf("parsed %d milestones, want 2", len(got))
	}
	if got[0].Description != "Single-command project launch with Docker isolation." {
		t.Errorf("Description = %q", got[0].Description)
	}
	if got[1].Description != "" {
		t.Errorf("milestone with no body: Description = %q, want empty", got[1].Description)
	}
}

// A bullet-list body (Milestone 5's shape in the real ROADMAP.md) must survive
// as lines, not be flattened into one run-on string.
func TestParseMilestonesPreservesBulletBody(t *testing.T) {
	md := "### Milestone 5: Ops\n\n- Shared volumes\n- Homebrew\n"
	got := ParseMilestones(md)
	if len(got) != 1 {
		t.Fatalf("parsed %d milestones, want 1", len(got))
	}
	if got[0].Description != "- Shared volumes\n- Homebrew" {
		t.Errorf("Description = %q", got[0].Description)
	}
}

// The last milestone must not absorb the rest of the document. ROADMAP.md ends
// with "## Phasing" and "## Current Focus" after the milestone list.
func TestParseMilestonesStopsDescriptionAtNextHeading(t *testing.T) {
	md := `### Milestone 5: Ops

Shared volumes.

## Phasing

M1 ──► M2

## Current Focus

Milestone 4, mid-flight.
`
	got := ParseMilestones(md)
	if len(got) != 1 {
		t.Fatalf("parsed %d milestones, want 1", len(got))
	}
	if got[0].Description != "Shared volumes." {
		t.Errorf("Description = %q, want it to stop at the next heading", got[0].Description)
	}
}

func TestParseMilestonesIgnoresNonMilestoneContent(t *testing.T) {
	md := `# Roadmap

## End Goal

Daedalus orchestrates agents.

### Sprint 41: Trust-Prompt (v0.40.0)

Goal: not a milestone.
`
	if got := ParseMilestones(md); len(got) != 0 {
		t.Errorf("parsed %d milestones from milestone-free markdown, want 0: %+v", len(got), got)
	}
}

func TestParseMilestonesEmptyInput(t *testing.T) {
	if got := ParseMilestones(""); len(got) != 0 {
		t.Errorf("ParseMilestones(\"\") = %+v, want none", got)
	}
}

// The parser is meant to fit the ROADMAP.md this repo already ships, unchanged
// — the whole premise of the structured-docs plan is that the convention is
// already there. If this fails, either the parser or the document drifted.
func TestParseMilestonesAgainstRealRoadmap(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "ROADMAP.md"))
	if err != nil {
		t.Fatalf("reading repo ROADMAP.md: %v", err)
	}

	got := ParseMilestones(string(data))
	if len(got) != 6 {
		t.Fatalf("parsed %d milestones from the repo ROADMAP.md, want 6: %+v", len(got), got)
	}

	for i, m := range got {
		if m.Number != i+1 {
			t.Errorf("milestone at index %d has Number %d, want %d", i, m.Number, i+1)
		}
		if m.Title == "" {
			t.Errorf("milestone %d has an empty title", m.Number)
		}
		if m.Description == "" {
			t.Errorf("milestone %d has an empty description", m.Number)
		}
	}

	// Exactly one milestone is in progress: the arc nests the current sprint
	// inside it, so a document with two (or none) would leave the timeline
	// with nowhere to put it.
	var inProgress int
	for _, m := range got {
		if m.Status == StatusInProgress {
			inProgress++
		}
	}
	if inProgress != 1 {
		t.Errorf("repo ROADMAP.md has %d in-progress milestones, want exactly 1", inProgress)
	}

	if got[3].Status != StatusDone {
		t.Errorf("Milestone 4 Status = %q, want %q", got[3].Status, StatusDone)
	}
	if got[4].Status != StatusDone {
		t.Errorf("Milestone 5 Status = %q, want %q", got[4].Status, StatusDone)
	}
	if got[5].Status != StatusInProgress {
		t.Errorf("Milestone 6 Status = %q, want %q", got[5].Status, StatusInProgress)
	}
}
