// Copyright (C) 2026 Techdelight BV

package core

import (
	"errors"
	"testing"
)

func asInvariant(t *testing.T, err error) *InvariantError {
	t.Helper()
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	var ie *InvariantError
	if !errors.As(err, &ie) {
		t.Fatalf("error %v is not an *InvariantError", err)
	}
	return ie
}

// The repo's own documents are internally consistent, so a validation of them
// as-is must pass — the baseline the guards are measured against.
func TestValidateWrite_RealDocsClean(t *testing.T) {
	if err := ValidateWrite(readRepoDoc(t, "ROADMAP.md"), readRepoDoc(t, "SPRINTS.md")); err != nil {
		t.Errorf("ValidateWrite on the repo docs = %v, want nil", err)
	}
}

// Marking a second milestone In Progress is the classic invariant break: the
// arc has one slot for the current focus. ValidateDocs only warns; a write must
// refuse.
func TestValidateWrite_RejectsSecondInProgress(t *testing.T) {
	roadmap := readRepoDoc(t, "ROADMAP.md") // milestone 7 is already In Progress
	twoInProgress, err := SetMilestoneStatus(roadmap, 5, StatusInProgress)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	ie := asInvariant(t, ValidateWrite(twoInProgress, readRepoDoc(t, "SPRINTS.md")))
	if ie == nil {
		t.Fatal("expected a rejection")
	}
}

// An error-severity ValidateDocs finding — here a current sprint linked to a
// milestone that does not exist — is likewise a refusal, not a warning.
func TestValidateWrite_RejectsContradiction(t *testing.T) {
	roadmap := "## Milestones\n\n### Milestone 1: Only One (In Progress)\n\nBody.\n"
	sprints := "## Current Sprint\n\n### Sprint 1: Work\n\nMilestone: 9\n\n| # | Item | Status |\n|---|------|--------|\n| 1 | Do it | Done |\n"
	asInvariant(t, ValidateWrite(roadmap, sprints))
}

// A milestone with no In Progress and one Paused milestone is still a clean
// write — Paused does not count against the one-In-Progress rule, and a "no
// milestone in progress" is only a ValidateDocs warning, not a write refusal.
func TestValidateWrite_PausedMilestoneAllowed(t *testing.T) {
	roadmap := "## Milestones\n\n### Milestone 1: Alpha (In Progress)\n\nA.\n\n### Milestone 2: Beta (Paused)\n\nB.\n"
	if err := ValidateWrite(roadmap, ""); err != nil {
		t.Errorf("ValidateWrite = %v, want nil for one In Progress + one Paused", err)
	}
}

// --- FinishSprint ------------------------------------------------------------

const finishFixture = `# Sprints

## Current Sprint

### Sprint 45: Work In Flight

Milestone: 7

| # | Item | Status |
|---|------|--------|
| 1 | Done thing | Done |
| 2 | Not yet | In Progress |

## Sprint History

### Sprint 44: Old (v0.41.0)

Milestone: 6

| # | Item | Status |
|---|------|--------|
| 1 | Did it | Done |
`

func TestFinishSprint_RefusesUnfinished(t *testing.T) {
	_, err := FinishSprint(finishFixture, 45, "0.42.0", false)
	ie := asInvariant(t, err)
	if ie == nil {
		t.Fatal("expected a rejection")
	}
}

func TestFinishSprint_ForceOverride(t *testing.T) {
	got, err := FinishSprint(finishFixture, 45, "0.42.0", true)
	if err != nil {
		t.Fatalf("FinishSprint(force): %v", err)
	}
	s, ok := sprintByNumber(ParseSprints(got), 45)
	if !ok || s.Version != "0.42.0" || s.IsCurrent {
		t.Errorf("forced roll wrong: found=%v version=%q current=%v", ok, s.Version, s.IsCurrent)
	}
}

func TestFinishSprint_AllDoneSucceeds(t *testing.T) {
	allDone := "# Sprints\n\n## Current Sprint\n\n### Sprint 45: Done Work\n\nMilestone: 7\n\n| # | Item | Status |\n|---|------|--------|\n| 1 | A | Done |\n| 2 | B | Done |\n\n## Sprint History\n\n### Sprint 44: Old (v0.41.0)\n\nMilestone: 6\n"
	got, err := FinishSprint(allDone, 45, "0.42.0", false)
	if err != nil {
		t.Fatalf("FinishSprint on an all-done sprint: %v", err)
	}
	s, _ := sprintByNumber(ParseSprints(got), 45)
	if s.Version != "0.42.0" {
		t.Errorf("version = %q, want 0.42.0", s.Version)
	}
}

// --- FinishMilestone ---------------------------------------------------------

func TestFinishMilestone_RefusesWithOpenSprint(t *testing.T) {
	// Real docs: sprint 45 is current and links to milestone 7.
	_, err := FinishMilestone(readRepoDoc(t, "ROADMAP.md"), readRepoDoc(t, "SPRINTS.md"), 7)
	ie := asInvariant(t, err)
	if ie == nil {
		t.Fatal("expected a rejection")
	}
}

func TestFinishMilestone_SucceedsWithNoOpenSprint(t *testing.T) {
	roadmap := readRepoDoc(t, "ROADMAP.md")
	// Roll the current sprint into history so milestone 7 has no open sprint.
	rolled, err := RollSprintToHistory(readRepoDoc(t, "SPRINTS.md"), 45, "0.42.0")
	if err != nil {
		t.Fatalf("setup roll: %v", err)
	}
	got, err := FinishMilestone(roadmap, rolled, 7)
	if err != nil {
		t.Fatalf("FinishMilestone: %v", err)
	}
	m, _ := milestoneByNumber(ParseMilestones(got), 7)
	if m.Status != StatusDone {
		t.Errorf("milestone 7 status = %q, want %q", m.Status, StatusDone)
	}
}
