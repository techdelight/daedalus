// Copyright (C) 2026 Techdelight BV

package core

import "testing"

// A milestone heading may carry "(Paused)" as its status parenthetical, parsed
// into StatusPaused alongside Done / In Progress.
func TestParseMilestonesRecognisesPaused(t *testing.T) {
	got := ParseMilestones("### Milestone 8: Later Thing (Paused)\n")
	if len(got) != 1 {
		t.Fatalf("parsed %d milestones, want 1", len(got))
	}
	if got[0].Title != "Later Thing" {
		t.Errorf("Title = %q, want %q", got[0].Title, "Later Thing")
	}
	if got[0].Status != StatusPaused {
		t.Errorf("Status = %q, want %q", got[0].Status, StatusPaused)
	}
}

// The sprint-side marker is a "Status: <s>" line in the header block, parsed
// like the Goal: and Milestone: lines around it.
func TestParseSprintsReadsStatusLine(t *testing.T) {
	md := `## Current Sprint

### Sprint 45: Parked Work

Goal: something.

Milestone: 7
Status: Paused

| # | Item | Status |
|---|------|--------|
| 1 | Do it | In Progress |
`
	got := ParseSprints(md)
	if len(got) != 1 {
		t.Fatalf("parsed %d sprints, want 1", len(got))
	}
	if got[0].Status != StatusPaused {
		t.Errorf("Status = %q, want %q", got[0].Status, StatusPaused)
	}
	if got[0].Milestone != 7 {
		t.Errorf("Milestone = %d, want 7 — the Status: line must not disturb the link", got[0].Milestone)
	}
	if got[0].Goal != "something." {
		t.Errorf("Goal = %q, want it still parsed", got[0].Goal)
	}
	if len(got[0].Items) != 1 {
		t.Fatalf("parsed %d items, want 1", len(got[0].Items))
	}
}

// A sprint with no Status: line leaves Status empty, so its phase is derived as
// before. The "| # | Item | Status |" table header must not be mistaken for the
// marker.
func TestParseSprintsNoStatusLineIsEmpty(t *testing.T) {
	md := `### Sprint 44: Ordinary (v0.41.0)

Goal: ship it.

| # | Item | Status |
|---|------|--------|
| 1 | Do it | Done |
`
	got := ParseSprints(md)
	if len(got) != 1 {
		t.Fatalf("parsed %d sprints, want 1", len(got))
	}
	if got[0].Status != "" {
		t.Errorf("Status = %q, want empty", got[0].Status)
	}
}

// PhaseOf reports PhasePaused for a parked sprint, and does so ahead of the
// Version and item-state logic — a shipped-looking or fully-done sprint that is
// marked Paused still reads as Paused.
func TestPhaseOfPaused(t *testing.T) {
	if p := PhaseOf(Sprint{Status: StatusPaused, Items: items(StatusInProgress, StatusPending)}); p != PhasePaused {
		t.Errorf("PhaseOf(paused, building) = %q, want %q", p, PhasePaused)
	}
	if p := PhaseOf(Sprint{Status: StatusPaused, Version: "v0.42.0", Items: items(StatusDone)}); p != PhasePaused {
		t.Errorf("PhaseOf(paused, versioned) = %q, want %q — Paused must win over Shipped", p, PhasePaused)
	}
	if p := PhaseOf(Sprint{Status: StatusPaused}); p != PhasePaused {
		t.Errorf("PhaseOf(paused, empty) = %q, want %q", p, PhasePaused)
	}
}

// A Paused milestone is not In Progress, so it neither counts toward the
// exactly-one-in-progress rule nor produces a finding of its own.
func TestValidateDocsPausedMilestoneIsNotInProgress(t *testing.T) {
	milestones := []Milestone{
		{Number: 7, Status: StatusInProgress},
		{Number: 8, Status: StatusPaused},
	}
	got := ValidateDocs(milestones, nil)
	if hasFinding(got, SeverityWarning, "one current focus") {
		t.Errorf("a Paused milestone must not count as a second In Progress; got %v", got)
	}
	if len(got) != 0 {
		t.Errorf("ValidateDocs = %v, want no findings for one In Progress + one Paused", got)
	}
}

// A parked current sprint linked to a paused milestone is coherent, not drift:
// the "current sprint on a not-in-progress milestone" warning is suppressed
// when the sprint itself is Paused.
func TestValidateDocsPausedCurrentSprintIsQuiet(t *testing.T) {
	milestones := []Milestone{
		{Number: 7, Status: StatusInProgress},
		{Number: 8, Status: StatusPaused},
	}
	sprints := []Sprint{{Number: 46, Milestone: 8, IsCurrent: true, Status: StatusPaused}}

	got := ValidateDocs(milestones, sprints)
	if hasFinding(got, SeverityWarning, "sprint 46", "milestone 8") {
		t.Errorf("a paused current sprint on a paused milestone must be quiet; got %v", got)
	}
}

// The exemption is narrow: an *unpaused* current sprint on a paused milestone
// still warns — that is real work against a milestone the roadmap parked.
func TestValidateDocsActiveSprintOnPausedMilestoneWarns(t *testing.T) {
	milestones := []Milestone{{Number: 8, Status: StatusPaused}}
	sprints := []Sprint{{Number: 46, Milestone: 8, IsCurrent: true}}

	got := ValidateDocs(milestones, sprints)
	if !hasFinding(got, SeverityWarning, "sprint 46", "milestone 8") {
		t.Errorf("an active current sprint on a paused milestone should warn; got %v", got)
	}
}
