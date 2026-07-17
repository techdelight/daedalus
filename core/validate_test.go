// Copyright (C) 2026 Techdelight BV

package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hasFinding reports whether any finding is of sev and mentions each substring.
func hasFinding(findings []Finding, sev Severity, substrings ...string) bool {
	for _, f := range findings {
		if f.Severity != sev {
			continue
		}
		all := true
		for _, s := range substrings {
			if !strings.Contains(f.Message, s) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// The finding this validator was built for. "In progress" parses fine and then
// compares unequal to StatusInProgress everywhere, so the item silently stops
// counting as in progress rather than failing loudly.
func TestValidateDocs_UnrecognisedItemStatus(t *testing.T) {
	sprints := []Sprint{{
		Number: 41,
		Items: []SprintItem{
			{Number: 1, Status: StatusDone},
			{Number: 2, Status: SprintStatus("In progress")},
		},
	}}

	got := ValidateDocs(nil, sprints)
	if !hasFinding(got, SeverityWarning, "sprint 41 item 2", "In progress") {
		t.Errorf("no warning for a miscased status; got %v", got)
	}
}

func TestValidateDocs_RecognisedStatusesAreQuiet(t *testing.T) {
	sprints := []Sprint{{
		Number: 41,
		Items: []SprintItem{
			{Number: 1, Status: StatusDone},
			{Number: 2, Status: StatusInProgress},
			{Number: 3, Status: StatusPending},
		},
	}}

	if got := ValidateDocs(nil, sprints); len(got) != 0 {
		t.Errorf("ValidateDocs = %v, want no findings", got)
	}
}

// The cross-file check no single-file parser can make: ParseSprints happily
// records a link to a milestone ROADMAP.md has never defined.
func TestValidateDocs_LinkToMissingMilestone(t *testing.T) {
	milestones := []Milestone{{Number: 1, Status: StatusInProgress}}
	sprints := []Sprint{{Number: 41, Milestone: 9, IsCurrent: true}}

	got := ValidateDocs(milestones, sprints)
	if !hasFinding(got, SeverityError, "sprint 41", "milestone 9") {
		t.Errorf("no error for a link to an undefined milestone; got %v", got)
	}
}

// Work happening against a milestone the roadmap does not show as under way
// means one of the two documents was not updated.
func TestValidateDocs_CurrentSprintOnNonCurrentMilestone(t *testing.T) {
	milestones := []Milestone{
		{Number: 3, Status: StatusDone},
		{Number: 4, Status: StatusInProgress},
	}
	sprints := []Sprint{{Number: 41, Milestone: 3, IsCurrent: true}}

	got := ValidateDocs(milestones, sprints)
	if !hasFinding(got, SeverityWarning, "sprint 41", "milestone 3") {
		t.Errorf("no warning for a current sprint on a Done milestone; got %v", got)
	}
}

func TestValidateDocs_CurrentSprintWithNoLink(t *testing.T) {
	milestones := []Milestone{{Number: 4, Status: StatusInProgress}}
	sprints := []Sprint{{Number: 41, IsCurrent: true}}

	got := ValidateDocs(milestones, sprints)
	if !hasFinding(got, SeverityWarning, "sprint 41", "Milestone: N") {
		t.Errorf("no warning for an unlinked current sprint; got %v", got)
	}
}

// History predates the convention; retro-fitting links to closed sprints is
// busywork, so only the current sprint's missing link is worth saying.
func TestValidateDocs_UnlinkedHistoryIsQuiet(t *testing.T) {
	milestones := []Milestone{{Number: 4, Status: StatusInProgress}}
	sprints := []Sprint{
		{Number: 41, Milestone: 4, IsCurrent: true},
		{Number: 40},
		{Number: 39},
	}

	if got := ValidateDocs(milestones, sprints); len(got) != 0 {
		t.Errorf("ValidateDocs = %v, want silence about unlinked history", got)
	}
}

func TestValidateDocs_DuplicateMilestone(t *testing.T) {
	milestones := []Milestone{
		{Number: 4, Title: "First", Status: StatusInProgress},
		{Number: 4, Title: "Second", Status: StatusPlanned},
	}

	got := ValidateDocs(milestones, nil)
	if !hasFinding(got, SeverityError, "milestone 4", "more than once") {
		t.Errorf("no error for a duplicate milestone; got %v", got)
	}
}

func TestValidateDocs_DuplicateSprint(t *testing.T) {
	sprints := []Sprint{{Number: 41}, {Number: 41}}

	got := ValidateDocs(nil, sprints)
	if !hasFinding(got, SeverityError, "sprint 41", "more than once") {
		t.Errorf("no error for a duplicate sprint; got %v", got)
	}
}

func TestValidateDocs_TwoCurrentSprints(t *testing.T) {
	sprints := []Sprint{
		{Number: 41, IsCurrent: true},
		{Number: 40, IsCurrent: true},
	}

	got := ValidateDocs(nil, sprints)
	if !hasFinding(got, SeverityError, "only one sprint can be current") {
		t.Errorf("no error for two current sprints; got %v", got)
	}
}

func TestValidateDocs_NoMilestoneInProgress(t *testing.T) {
	milestones := []Milestone{
		{Number: 1, Status: StatusDone},
		{Number: 2, Status: StatusPlanned},
	}

	got := ValidateDocs(milestones, nil)
	if !hasFinding(got, SeverityWarning, "no milestone is marked") {
		t.Errorf("no warning when nothing is in progress; got %v", got)
	}
}

func TestValidateDocs_SeveralMilestonesInProgress(t *testing.T) {
	milestones := []Milestone{
		{Number: 3, Status: StatusInProgress},
		{Number: 4, Status: StatusInProgress},
	}

	got := ValidateDocs(milestones, nil)
	if !hasFinding(got, SeverityWarning, "one current focus") {
		t.Errorf("no warning when several milestones are in progress; got %v", got)
	}
}

// A project that has written no documents yet is early, not broken. The docs
// badge already reports what is missing; repeating it here would bury the
// findings that matter.
func TestValidateDocs_AbsenceIsNotAFinding(t *testing.T) {
	if got := ValidateDocs(nil, nil); len(got) != 0 {
		t.Errorf("ValidateDocs(nil, nil) = %v, want no findings", got)
	}
	if got := ValidateDocs(nil, nil); got == nil {
		t.Error("ValidateDocs returned nil, want an empty slice so callers can range freely")
	}
}

// Without a ROADMAP.md there is nothing to link against, so a sprint naming a
// milestone is unverifiable rather than wrong.
func TestValidateDocs_NoMilestonesSkipsLinkChecks(t *testing.T) {
	sprints := []Sprint{{Number: 41, Milestone: 9, IsCurrent: true}}

	if got := ValidateDocs(nil, sprints); len(got) != 0 {
		t.Errorf("ValidateDocs = %v, want link checks skipped with no roadmap", got)
	}
}

// The validator's whole purpose is to hold this repo's own documents to the
// convention they define. If this fails, either a document drifted or the
// validator gained an opinion the documents never agreed to.
func TestValidateDocs_RealRepoDocsAreClean(t *testing.T) {
	roadmap, err := os.ReadFile(filepath.Join("..", "ROADMAP.md"))
	if err != nil {
		t.Fatalf("reading repo ROADMAP.md: %v", err)
	}
	sprints, err := os.ReadFile(filepath.Join("..", "SPRINTS.md"))
	if err != nil {
		t.Fatalf("reading repo SPRINTS.md: %v", err)
	}

	got := ValidateDocs(ParseMilestones(string(roadmap)), ParseSprints(string(sprints)))
	for _, f := range got {
		t.Errorf("repo documents are not clean: %s", f)
	}
}
