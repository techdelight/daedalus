// Copyright (C) 2026 Techdelight BV

package core

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
// TestParseMilestonesAgainstRealRoadmap asserts that the parser agrees with the
// repository's own ROADMAP.md — and asserts STRUCTURE, not content.
//
// It used to pin each milestone's status by hand ("Milestone 15 must be Done").
// That is restating the document rather than testing it: the assertions carried
// no information the document did not already carry, and they had to be edited
// every time a milestone opened or closed. They broke on two consecutive
// milestone openings in one working session, which is the shape of a test that
// costs more than it catches.
//
// What is worth asserting is what must be true of ANY valid roadmap, whatever
// its contents: that the parser sees every milestone the document declares, that
// numbering is contiguous from 1, that nothing is nameless, and that the arc has
// at most one milestone in progress. Those hold across every future close without
// anybody editing this file.
func TestParseMilestonesAgainstRealRoadmap(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "ROADMAP.md"))
	if err != nil {
		t.Fatalf("reading repo ROADMAP.md: %v", err)
	}
	doc := string(data)
	got := ParseMilestones(doc)

	// The parser must see exactly the milestones the document declares — derived
	// from the document rather than from a number written here, so adding M18
	// needs no edit.
	declared := regexp.MustCompile(`(?m)^### Milestone \d+:`).FindAllString(doc, -1)
	if len(declared) == 0 {
		t.Fatal("no milestone headings found in ROADMAP.md — the format changed under the parser")
	}
	if len(got) != len(declared) {
		t.Fatalf("parsed %d milestones, but the document declares %d", len(got), len(declared))
	}

	for i, m := range got {
		if m.Number != i+1 {
			t.Errorf("milestone at index %d has Number %d, want %d (numbering must be contiguous from 1)", i, m.Number, i+1)
		}
		if m.Title == "" {
			t.Errorf("milestone %d has an empty title", m.Number)
		}
		if m.Description == "" {
			t.Errorf("milestone %d has an empty description", m.Number)
		}
		// A title ending in a status-shaped parenthetical means the heading carries
		// TWO markers — `### Milestone 15: … (Planned) (In Progress)` — which the
		// parser is lenient enough to accept (it takes the last), so it is silent
		// document corruption rather than a hard failure. BACKLOG #65 records it
		// happening for real. Legitimate parentheticals like "(V3, demoted)" are
		// untouched, because only a KNOWN status word counts.
		if marker, doubled := trailingStatusMarker(m.Title); doubled {
			t.Errorf("milestone %d title %q ends with a second status marker (%s) — the heading carries two",
				m.Number, m.Title, marker)
		}
	}

	// At most one milestone is in progress: the arc nests the current sprint
	// inside the one in-progress milestone, so two would leave the timeline with
	// a choice of places to put it. Zero is a valid between-milestones state (one
	// milestone shipped, the next not yet chosen) — and then there is no current
	// sprint to place either.
	var inProgress []int
	for _, m := range got {
		if m.Status == StatusInProgress {
			inProgress = append(inProgress, m.Number)
		}
	}
	if len(inProgress) > 1 {
		t.Errorf("ROADMAP.md has %d in-progress milestones %v, want at most 1", len(inProgress), inProgress)
	}
}

// trailingStatusMarker reports whether a parsed title ends with a parenthetical
// that is itself a status word — the signature of a heading with two markers.
func trailingStatusMarker(title string) (string, bool) {
	for _, status := range []Status{StatusPlanned, StatusInProgress, StatusPaused, StatusDone} {
		if strings.HasSuffix(title, "("+string(status)+")") {
			return string(status), true
		}
	}
	return "", false
}
