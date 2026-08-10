// Copyright (C) 2026 Techdelight BV

package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each operation gets a round-trip test (apply, then parse the result and
// assert the intended change) and a prose-preservation test (assert the
// untouched regions are byte-identical). The prose tests lean on the repo's own
// ROADMAP.md / SPRINTS.md so they exercise the real hand-written formatting the
// writers must not disturb.

// fixtureRoadmap / fixtureSprints are stable, self-contained documents for the
// value-pinned writer tests (exact assigned numbers, specific prose). They do
// NOT read the repo's own ROADMAP.md / SPRINTS.md, because those change every
// time a milestone or sprint is opened or closed — in part by these very
// functions — which would break any test pinned to their current contents. The
// prose/consistency of the real docs is covered separately by the
// "…AgainstRealRoadmap" / "…RealDoc" tests, which assert invariants rather than
// exact values.
const fixtureRoadmap = `# Roadmap

## Milestones

### Milestone 1: Foundation (Done)

Prose about the foundation milestone.

### Milestone 2: Current Work (In Progress)

Prose about the current milestone.

- a bullet
- another bullet

## Phasing

A phasing diagram would live here.
`

const fixtureSprints = `# Sprints

## Current Sprint

### Sprint 5: Doing Things

Goal: do the things.

Milestone: 2

| # | Item | Status |
|---|------|--------|
| 1 | first thing | Done |
| 2 | second thing | In Progress |

Out of scope: nothing here.

## Sprint History

### Sprint 4: Older Work (v1.0.0)

Goal: the older work.

Milestone: 1
`

func readRepoDoc(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", name))
	if err != nil {
		t.Fatalf("reading repo %s: %v", name, err)
	}
	return string(data)
}

func indexOf(t *testing.T, s, sub string) int {
	t.Helper()
	i := strings.Index(s, sub)
	if i < 0 {
		t.Fatalf("substring %q not found", sub)
	}
	return i
}

func milestoneByNumber(ms []Milestone, n int) (Milestone, bool) {
	for _, m := range ms {
		if m.Number == n {
			return m, true
		}
	}
	return Milestone{}, false
}

func sprintByNumber(ss []Sprint, n int) (Sprint, bool) {
	for _, s := range ss {
		if s.Number == n {
			return s, true
		}
	}
	return Sprint{}, false
}

// --- SetMilestoneStatus ------------------------------------------------------

func TestSetMilestoneStatus_RoundTrip(t *testing.T) {
	roadmap := readRepoDoc(t, "ROADMAP.md")

	got, err := SetMilestoneStatus(roadmap, 7, StatusPaused)
	if err != nil {
		t.Fatalf("SetMilestoneStatus: %v", err)
	}
	m, ok := milestoneByNumber(ParseMilestones(got), 7)
	if !ok || m.Status != StatusPaused {
		t.Errorf("milestone 7 status = %q (found=%v), want %q", m.Status, ok, StatusPaused)
	}

	// Planned removes the parenthetical again, and the title survives intact.
	back, err := SetMilestoneStatus(got, 7, StatusPlanned)
	if err != nil {
		t.Fatalf("SetMilestoneStatus->Planned: %v", err)
	}
	m, _ = milestoneByNumber(ParseMilestones(back), 7)
	if m.Status != StatusPlanned {
		t.Errorf("milestone 7 status = %q, want %q", m.Status, StatusPlanned)
	}
	if !strings.Contains(m.Title, "Project-Management Tools") {
		t.Errorf("milestone 7 title corrupted: %q", m.Title)
	}
}

func TestSetMilestoneStatus_PreservesProse(t *testing.T) {
	roadmap := readRepoDoc(t, "ROADMAP.md")
	got, err := SetMilestoneStatus(roadmap, 7, StatusPaused)
	if err != nil {
		t.Fatalf("SetMilestoneStatus: %v", err)
	}

	// Everything up to milestone 7's heading, and everything from the next
	// heading (## Phasing) onward, must be byte-identical.
	head := roadmap[:indexOf(t, roadmap, "### Milestone 7:")]
	if !strings.HasPrefix(got, head) {
		t.Error("content before milestone 7 changed")
	}
	tail := roadmap[indexOf(t, roadmap, "## Phasing"):]
	if !strings.HasSuffix(got, tail) {
		t.Error("content from ## Phasing onward changed")
	}
}

func TestSetMilestoneStatus_KeepsUnrecognisedParentheticalTitle(t *testing.T) {
	in := "### Milestone 2: Rework (Phase 2)\n"
	got, err := SetMilestoneStatus(in, 2, StatusDone)
	if err != nil {
		t.Fatalf("SetMilestoneStatus: %v", err)
	}
	if got != "### Milestone 2: Rework (Phase 2) (Done)\n" {
		t.Errorf("got %q", got)
	}
	m, _ := milestoneByNumber(ParseMilestones(got), 2)
	if m.Title != "Rework (Phase 2)" || m.Status != StatusDone {
		t.Errorf("parsed title=%q status=%q", m.Title, m.Status)
	}
}

// Backlog #65: the writer's status list was missing `Planned` after the parser
// gained it, so "(Planned)" was not stripped and the new marker was appended —
// "… (V2) (Planned) (In Progress)". Table-driven over every status a heading can
// carry, because the bug was one absent alternative and a test pinning only the
// statuses that happened to work would have passed throughout.
func TestSetMilestoneStatus_ReplacesEveryStatusMarker(t *testing.T) {
	for _, from := range []Status{StatusDone, StatusInProgress, StatusPaused, StatusPlanned} {
		in := "### Milestone 15: Governance (V2) (" + string(from) + ")\n"
		got, err := SetMilestoneStatus(in, 15, StatusInProgress)
		if err != nil {
			t.Fatalf("SetMilestoneStatus from %q: %v", from, err)
		}
		if want := "### Milestone 15: Governance (V2) (In Progress)\n"; got != want {
			t.Errorf("from %q: got %q, want %q", from, got, want)
		}
		m, ok := milestoneByNumber(ParseMilestones(got), 15)
		if !ok || m.Title != "Governance (V2)" {
			t.Errorf("from %q: parsed title = %q, want the marker out of the title", from, m.Title)
		}
	}
}

// A heading already corrupted by #65 is repaired by the next status write rather
// than growing a third marker — otherwise the older marker stays welded into the
// title, since nothing else ever rewrites it.
func TestSetMilestoneStatus_RepairsAnAlreadyDoubledHeading(t *testing.T) {
	in := "### Milestone 15: Governance (V2) (Planned) (In Progress)\n"
	got, err := SetMilestoneStatus(in, 15, StatusDone)
	if err != nil {
		t.Fatalf("SetMilestoneStatus: %v", err)
	}
	if want := "### Milestone 15: Governance (V2) (Done)\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSetMilestoneStatus_NotFound(t *testing.T) {
	if _, err := SetMilestoneStatus(readRepoDoc(t, "ROADMAP.md"), 99, StatusDone); err == nil {
		t.Error("want error for a missing milestone")
	}
}

// --- AddMilestone ------------------------------------------------------------

func TestAddMilestone_RoundTrip(t *testing.T) {
	before := ParseMilestones(fixtureRoadmap)

	got, number, err := AddMilestone(fixtureRoadmap, "Frontier Features", "Some new frontier work.\n\n- bullet one\n- bullet two")
	if err != nil {
		t.Fatalf("AddMilestone: %v", err)
	}
	if number != 3 {
		t.Errorf("assigned number = %d, want 3", number)
	}
	after := ParseMilestones(got)
	if len(after) != len(before)+1 {
		t.Fatalf("milestone count = %d, want %d", len(after), len(before)+1)
	}
	m, ok := milestoneByNumber(after, 3)
	if !ok {
		t.Fatal("milestone 3 not parsed back")
	}
	if m.Title != "Frontier Features" || m.Status != StatusPlanned {
		t.Errorf("milestone 3 title=%q status=%q", m.Title, m.Status)
	}
	if !strings.Contains(m.Description, "- bullet two") {
		t.Errorf("description not preserved: %q", m.Description)
	}
}

func TestAddMilestone_PreservesProseAndInsertsBeforePhasing(t *testing.T) {
	roadmap := readRepoDoc(t, "ROADMAP.md")
	got, _, err := AddMilestone(roadmap, "Frontier", "desc")
	if err != nil {
		t.Fatalf("AddMilestone: %v", err)
	}

	phasing := indexOf(t, roadmap, "## Phasing")
	// Everything before the Phasing section is an unchanged prefix, and the
	// Phasing section onward is an unchanged suffix — the new block sits exactly
	// between them.
	if !strings.HasPrefix(got, roadmap[:phasing]) {
		t.Error("content before ## Phasing was disturbed")
	}
	if !strings.HasSuffix(got, roadmap[phasing:]) {
		t.Error("## Phasing section onward was disturbed")
	}
	if strings.Index(got, "### Milestone 8:") > strings.Index(got, "## Phasing") {
		t.Error("new milestone was not inserted before ## Phasing")
	}
}

// The documented fallback: with no ## Phasing section, append at the end.
func TestAddMilestone_AppendsAtEndWhenNoPhasing(t *testing.T) {
	in := "# Roadmap\n\n## Milestones\n\n### Milestone 1: First (Done)\n\nBody.\n"
	got, number, err := AddMilestone(in, "Second", "More.")
	if err != nil {
		t.Fatalf("AddMilestone: %v", err)
	}
	if number != 2 {
		t.Errorf("number = %d, want 2", number)
	}
	if !strings.HasPrefix(got, in) {
		t.Error("original content should be an unchanged prefix when appending at end")
	}
	m, ok := milestoneByNumber(ParseMilestones(got), 2)
	if !ok || m.Title != "Second" {
		t.Errorf("milestone 2 not parsed: found=%v title=%q", ok, m.Title)
	}
}

// --- RemoveMilestone ---------------------------------------------------------

func TestRemoveMilestone_RoundTrip(t *testing.T) {
	roadmap := readRepoDoc(t, "ROADMAP.md")
	got, err := RemoveMilestone(roadmap, 3)
	if err != nil {
		t.Fatalf("RemoveMilestone: %v", err)
	}
	if _, ok := milestoneByNumber(ParseMilestones(got), 3); ok {
		t.Error("milestone 3 still present after removal")
	}
	for _, n := range []int{1, 2, 4, 5, 6, 7} {
		if _, ok := milestoneByNumber(ParseMilestones(got), n); !ok {
			t.Errorf("milestone %d was lost", n)
		}
	}
}

func TestRemoveMilestone_PreservesProse(t *testing.T) {
	roadmap := readRepoDoc(t, "ROADMAP.md")
	got, err := RemoveMilestone(roadmap, 3)
	if err != nil {
		t.Fatalf("RemoveMilestone: %v", err)
	}
	// Exactly [Milestone 3 heading, Milestone 4 heading) is removed: the text
	// before M3 is an unchanged prefix, the text from M4 on an unchanged suffix.
	if !strings.HasPrefix(got, roadmap[:indexOf(t, roadmap, "### Milestone 3:")]) {
		t.Error("content before milestone 3 changed")
	}
	if !strings.HasSuffix(got, roadmap[indexOf(t, roadmap, "### Milestone 4:"):]) {
		t.Error("content from milestone 4 onward changed")
	}
}

func TestRemoveMilestone_NotFound(t *testing.T) {
	if _, err := RemoveMilestone(readRepoDoc(t, "ROADMAP.md"), 99); err == nil {
		t.Error("want error for a missing milestone")
	}
}

// --- SetSprintStatus ---------------------------------------------------------

func TestSetSprintStatus_RoundTripAndRemoval(t *testing.T) {
	sprints := readRepoDoc(t, "SPRINTS.md")

	got, err := SetSprintStatus(sprints, 45, StatusPaused)
	if err != nil {
		t.Fatalf("SetSprintStatus: %v", err)
	}
	s, ok := sprintByNumber(ParseSprints(got), 45)
	if !ok || s.Status != StatusPaused {
		t.Fatalf("sprint 45 status = %q (found=%v), want %q", s.Status, ok, StatusPaused)
	}
	if s.Milestone != 7 {
		t.Errorf("sprint 45 milestone link disturbed: %d", s.Milestone)
	}

	// Removing the status (Pending) restores the document byte-for-byte.
	back, err := SetSprintStatus(got, 45, StatusPending)
	if err != nil {
		t.Fatalf("SetSprintStatus->remove: %v", err)
	}
	if back != sprints {
		t.Error("add-then-remove Status did not restore the original document byte-for-byte")
	}
}

func TestSetSprintStatus_PreservesProse(t *testing.T) {
	got, err := SetSprintStatus(fixtureSprints, 5, StatusPaused)
	if err != nil {
		t.Fatalf("SetSprintStatus: %v", err)
	}
	// The Sprint History section (everything from its heading on) is untouched.
	tail := fixtureSprints[indexOf(t, fixtureSprints, "## Sprint History"):]
	if !strings.HasSuffix(got, tail) {
		t.Error("Sprint History section changed")
	}
	if !strings.Contains(got, "Out of scope: nothing here.") {
		t.Error("sprint 5 out-of-scope prose was lost")
	}
}

func TestSetSprintStatus_NotFound(t *testing.T) {
	if _, err := SetSprintStatus(readRepoDoc(t, "SPRINTS.md"), 999, StatusPaused); err == nil {
		t.Error("want error for a missing sprint")
	}
}

// --- AddSprint ---------------------------------------------------------------

const emptyCurrentSprints = `# Sprints

## Current Sprint

## Sprint History

### Sprint 44: Old (v0.41.0)

Milestone: 6

| # | Item | Status |
|---|------|--------|
| 1 | Did it | Done |
`

func TestAddSprint_RoundTripIntoEmptySection(t *testing.T) {
	got, number, err := AddSprint(emptyCurrentSprints, "Fresh Work", "", 7, []string{"first thing", "second thing"})
	if err != nil {
		t.Fatalf("AddSprint: %v", err)
	}
	if number != 45 {
		t.Errorf("assigned number = %d, want 45", number)
	}
	parsed := ParseSprints(got)
	s, ok := sprintByNumber(parsed, 45)
	if !ok {
		t.Fatal("sprint 45 not parsed back")
	}
	if !s.IsCurrent {
		t.Error("new sprint should be current")
	}
	if s.Milestone != 7 {
		t.Errorf("milestone = %d, want 7", s.Milestone)
	}
	if len(s.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(s.Items))
	}
	for _, it := range s.Items {
		if it.Status != StatusPending {
			t.Errorf("item %d status = %q, want pending", it.Number, it.Status)
		}
	}
	if s.Items[0].Description != "first thing" {
		t.Errorf("item 1 desc = %q", s.Items[0].Description)
	}
	// The history sprint is untouched.
	if !strings.HasSuffix(got, emptyCurrentSprints[indexOf(t, emptyCurrentSprints, "### Sprint 44:"):]) {
		t.Error("Sprint History entry changed")
	}
}

func TestAddSprint_PreservesHistoryOnRealDoc(t *testing.T) {
	sprints := readRepoDoc(t, "SPRINTS.md")
	got, _, err := AddSprint(sprints, "Extra", "", 7, []string{"x"})
	if err != nil {
		t.Fatalf("AddSprint: %v", err)
	}
	tail := sprints[indexOf(t, sprints, "## Sprint History"):]
	if !strings.HasSuffix(got, tail) {
		t.Error("Sprint History section changed")
	}
}

func TestAddSprint_NoCurrentSection(t *testing.T) {
	if _, _, err := AddSprint("# Sprints\n\n## Sprint History\n", "X", "", 1, nil); err == nil {
		t.Error("want error when there is no ## Current Sprint section")
	}
}

// placeholderCurrentSprints is the shape SPRINTS.md takes between sprints: the
// last one has rolled to history and a human has written a note in the empty
// slot saying so.
const placeholderCurrentSprints = `# Sprints

## Current Sprint

_No active sprint, and no active milestone. Milestone 6 shipped in **v0.41.0**._

## Sprint History

### Sprint 44: Old (v0.41.0)

Milestone: 6

| # | Item | Status |
|---|------|--------|
| 1 | Did it | Done |
`

// Backlog #66: the placeholder is prose asserting there is no current sprint, so
// leaving it in place while inserting one above produced a section that both had
// a sprint and said it had none. It has to go.
func TestAddSprint_ReplacesTheBetweenSprintsPlaceholder(t *testing.T) {
	got, number, err := AddSprint(placeholderCurrentSprints, "Fresh Work", "get it done", 7, []string{"first thing"})
	if err != nil {
		t.Fatalf("AddSprint: %v", err)
	}
	if strings.Contains(got, "_No active sprint") {
		t.Errorf("placeholder prose survived alongside the new sprint:\n%s", got)
	}
	s, ok := sprintByNumber(ParseSprints(got), number)
	if !ok || !s.IsCurrent {
		t.Fatalf("sprint %d not current after add", number)
	}
	// Only the placeholder goes: the history below it is untouched.
	tail := placeholderCurrentSprints[indexOf(t, placeholderCurrentSprints, "## Sprint History"):]
	if !strings.HasSuffix(got, tail) {
		t.Errorf("Sprint History section changed:\n%s", got)
	}
}

// The counterpart guard: prose sitting beside a REAL sprint is an author's note,
// not a placeholder, and must survive. Without this the fix above would be a
// licence to delete arbitrary text from the section.
func TestAddSprint_KeepsProseWhenASprintIsAlreadyCurrent(t *testing.T) {
	const withSprint = `# Sprints

## Current Sprint

_A note the author wants kept._

### Sprint 44: Live

Milestone: 6

| # | Item | Status |
|---|------|--------|
| 1 | Doing it |  |

## Sprint History
`
	got, _, err := AddSprint(withSprint, "Second", "", 6, []string{"x"})
	if err != nil {
		t.Fatalf("AddSprint: %v", err)
	}
	if !strings.Contains(got, "_A note the author wants kept._") {
		t.Errorf("author's note was deleted:\n%s", got)
	}
	if !strings.Contains(got, "### Sprint 44: Live") {
		t.Errorf("existing sprint was deleted:\n%s", got)
	}
}

func TestAddSprint_WritesTheGoalLine(t *testing.T) {
	got, number, err := AddSprint(emptyCurrentSprints, "Fresh Work", "prove the writer emits a goal", 7, nil)
	if err != nil {
		t.Fatalf("AddSprint: %v", err)
	}
	if !strings.Contains(got, "Goal: prove the writer emits a goal") {
		t.Errorf("Goal: line missing:\n%s", got)
	}
	s, ok := sprintByNumber(ParseSprints(got), number)
	if !ok {
		t.Fatalf("sprint %d not parsed back", number)
	}
	if s.Goal != "prove the writer emits a goal" {
		t.Errorf("parsed goal = %q", s.Goal)
	}
	// The goal must not displace the milestone link.
	if s.Milestone != 7 {
		t.Errorf("milestone = %d, want 7", s.Milestone)
	}
}

// --- RemoveSprint ------------------------------------------------------------

func TestRemoveSprint_RoundTrip(t *testing.T) {
	sprints := readRepoDoc(t, "SPRINTS.md")
	got, err := RemoveSprint(sprints, 44)
	if err != nil {
		t.Fatalf("RemoveSprint: %v", err)
	}
	if _, ok := sprintByNumber(ParseSprints(got), 44); ok {
		t.Error("sprint 44 still present")
	}
	if _, ok := sprintByNumber(ParseSprints(got), 43); !ok {
		t.Error("sprint 43 was lost")
	}
	// [Sprint 44 heading, Sprint 43 heading) removed exactly.
	if !strings.HasPrefix(got, sprints[:indexOf(t, sprints, "### Sprint 44:")]) {
		t.Error("content before sprint 44 changed")
	}
	if !strings.HasSuffix(got, sprints[indexOf(t, sprints, "### Sprint 43:"):]) {
		t.Error("content from sprint 43 onward changed")
	}
}

func TestRemoveSprint_NotFound(t *testing.T) {
	if _, err := RemoveSprint(readRepoDoc(t, "SPRINTS.md"), 999); err == nil {
		t.Error("want error for a missing sprint")
	}
}

// --- MoveSprint --------------------------------------------------------------

func TestMoveSprint_RoundTrip(t *testing.T) {
	got, err := MoveSprint(fixtureSprints, 5, 1)
	if err != nil {
		t.Fatalf("MoveSprint: %v", err)
	}
	s, _ := sprintByNumber(ParseSprints(got), 5)
	if s.Milestone != 1 {
		t.Errorf("milestone = %d, want 1", s.Milestone)
	}
	// Only the Milestone: line changed; Sprint History is untouched.
	if !strings.HasSuffix(got, fixtureSprints[indexOf(t, fixtureSprints, "## Sprint History"):]) {
		t.Error("Sprint History changed")
	}
}

func TestMoveSprint_AddsMissingLine(t *testing.T) {
	in := "## Current Sprint\n\n### Sprint 41: Trust-Prompt\n\nGoal: close the gap.\n\n| # | Item | Status |\n|---|------|--------|\n| 1 | Do it | Done |\n"
	got, err := MoveSprint(in, 41, 4)
	if err != nil {
		t.Fatalf("MoveSprint: %v", err)
	}
	s, _ := sprintByNumber(ParseSprints(got), 41)
	if s.Milestone != 4 {
		t.Errorf("milestone = %d, want 4", s.Milestone)
	}
	if s.Goal != "close the gap." {
		t.Errorf("goal disturbed: %q", s.Goal)
	}
	// The new line lands right after the Goal: line.
	if !strings.Contains(got, "Goal: close the gap.\nMilestone: 4\n") {
		t.Errorf("Milestone: line not placed after Goal:\n%s", got)
	}
}

func TestMoveSprint_NotFound(t *testing.T) {
	if _, err := MoveSprint(readRepoDoc(t, "SPRINTS.md"), 999, 1); err == nil {
		t.Error("want error for a missing sprint")
	}
}

// --- RollSprintToHistory -----------------------------------------------------

func TestRollSprintToHistory_RoundTrip(t *testing.T) {
	// Fixture-backed, not repo-backed: the current sprint is Sprint 5, with
	// Sprint 4 already in history. Reading the live SPRINTS.md made this test
	// break whenever the board rolled forward (it hard-coded the then-current
	// sprint number); the fixture pins the shape it actually exercises.
	got, err := RollSprintToHistory(fixtureSprints, 5, "2.0.0")
	if err != nil {
		t.Fatalf("RollSprintToHistory: %v", err)
	}
	parsed := ParseSprints(got)
	s, ok := sprintByNumber(parsed, 5)
	if !ok {
		t.Fatal("sprint 5 not parsed back")
	}
	if s.Version != "2.0.0" {
		t.Errorf("version = %q, want 2.0.0", s.Version)
	}
	if s.IsCurrent {
		t.Error("rolled sprint should no longer be current")
	}
	// The current slot is now empty.
	for _, s := range parsed {
		if s.IsCurrent {
			t.Errorf("sprint %d is still current after the roll", s.Number)
		}
	}
	// 5 lands at the top of history, immediately above 4, which is untouched.
	if strings.Index(got, "### Sprint 5:") > strings.Index(got, "### Sprint 4:") {
		t.Error("rolled sprint 5 is not above sprint 4 in history")
	}
	if !strings.HasSuffix(got, fixtureSprints[indexOf(t, fixtureSprints, "### Sprint 4:"):]) {
		t.Error("history from sprint 4 onward changed")
	}
}

func TestRollSprintToHistory_ToleratesVPrefix(t *testing.T) {
	sprints := readRepoDoc(t, "SPRINTS.md")
	got, err := RollSprintToHistory(sprints, 45, "v0.42.0")
	if err != nil {
		t.Fatalf("RollSprintToHistory: %v", err)
	}
	s, _ := sprintByNumber(ParseSprints(got), 45)
	if s.Version != "0.42.0" {
		t.Errorf("version = %q, want 0.42.0 (a leading v must not double up)", s.Version)
	}
}

func TestRollSprintToHistory_NotFound(t *testing.T) {
	if _, err := RollSprintToHistory(readRepoDoc(t, "SPRINTS.md"), 999, "1.0.0"); err == nil {
		t.Error("want error for a missing sprint")
	}
}
