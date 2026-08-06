// Copyright (C) 2026 Techdelight BV

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/progress"
)

// setup creates a server backed by a temp directory and returns a connected
// client session plus the directory.
func setup(t *testing.T) (*mcp.ClientSession, string) {
	t.Helper()
	dir := t.TempDir()
	server := newServer(dir)

	ct, st := mcp.NewInMemoryTransports()
	_, err := server.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, dir
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return res
}

// resultText returns the text payload of a successful tool call, failing if the
// call reported an error.
func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res.IsError {
		t.Fatalf("unexpected tool error: %v", res.Content)
	}
	return res.Content[0].(*mcp.TextContent).Text
}

func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// seed writes a ROADMAP.md and SPRINTS.md into dir.
func seed(t *testing.T, dir, roadmap, sprints string) {
	t.Helper()
	writeFileT(t, filepath.Join(dir, "ROADMAP.md"), roadmap)
	writeFileT(t, filepath.Join(dir, "SPRINTS.md"), sprints)
}

// roadmapFixture: milestone 1 In Progress, milestone 2 Planned.
const roadmapFixture = `# Roadmap

## Milestones

### Milestone 1: First (In Progress)

Body one — hand-written prose that must survive edits.

### Milestone 2: Second

Planned body.

## Phasing

M1 -> M2
`

// sprintsFixture: a current sprint 44 (with an unfinished item) linked to
// milestone 1, plus one history sprint.
const sprintsFixture = `# Sprints

## Current Sprint

### Sprint 44: Current Work

Goal: do the thing.

Milestone: 1

| # | Item | Status |
|---|------|--------|
| 1 | a | Done |
| 2 | b | In Progress |

## Sprint History

### Sprint 43: Old (v0.1.0)

Milestone: 1

| # | Item | Status |
|---|------|--------|
| 1 | x | Done |
`

// sprintsEmptyCurrent: an empty current-sprint slot, for add_sprint.
const sprintsEmptyCurrent = `# Sprints

## Current Sprint

## Sprint History

### Sprint 43: Old (v0.1.0)

Milestone: 1

| # | Item | Status |
|---|------|--------|
| 1 | x | Done |
`

func TestNewServer_RegistersAllTools(t *testing.T) {
	cs, _ := setup(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		// read side
		"get_progress":          false,
		"get_roadmap":           false,
		"get_current_sprint":    false,
		"get_sprints":           false,
		"get_backlog":           false,
		"get_strategic_roadmap": false,
		// lifecycle (write) side
		"add_milestone":    false,
		"remove_milestone": false,
		"start_milestone":  false,
		"finish_milestone": false,
		"pause_milestone":  false,
		"add_sprint":       false,
		"remove_sprint":    false,
		"move_sprint":      false,
		"start_sprint":     false,
		"finish_sprint":    false,
		"pause_sprint":     false,
	}
	for _, tool := range res.Tools {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("tool %q not registered", name)
		}
	}
	// The retired self-report tools must be gone (Backlog #52).
	for _, tool := range res.Tools {
		switch tool.Name {
		case "report_progress", "set_vision", "set_version":
			t.Errorf("retired tool %q is still registered", tool.Name)
		}
	}
}

// --- read side ---

func TestGetProgress_Empty(t *testing.T) {
	cs, _ := setup(t)
	res := callTool(t, cs, "get_progress", map[string]any{})
	var d progress.Data
	if err := json.Unmarshal([]byte(resultText(t, res)), &d); err != nil {
		t.Fatal(err)
	}
	if d != (progress.Data{}) {
		t.Errorf("expected zero-value data, got %+v", d)
	}
}

func TestGetProgress_DerivedFromFiles(t *testing.T) {
	cs, dir := setup(t)
	writeFileT(t, filepath.Join(dir, "VERSION"), "1.2.3\n")
	writeFileT(t, filepath.Join(dir, "VISION.md"), "Automate everything\n")
	seed(t, dir, roadmapFixture, sprintsFixture) // current sprint: 1 of 2 done

	res := callTool(t, cs, "get_progress", map[string]any{})
	var d progress.Data
	if err := json.Unmarshal([]byte(resultText(t, res)), &d); err != nil {
		t.Fatal(err)
	}
	if d.ProjectVersion != "1.2.3" {
		t.Errorf("ProjectVersion = %q, want 1.2.3", d.ProjectVersion)
	}
	if d.Vision != "Automate everything" {
		t.Errorf("Vision = %q, want %q", d.Vision, "Automate everything")
	}
	if d.ProgressPct != 50 {
		t.Errorf("ProgressPct = %d, want 50", d.ProgressPct)
	}
}

func TestGetRoadmap_NoFile(t *testing.T) {
	cs, _ := setup(t)
	res := callTool(t, cs, "get_roadmap", map[string]any{})
	var out RoadmapOutput
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Sprints) != 0 {
		t.Errorf("expected 0 sprints, got %d", len(out.Sprints))
	}
}

func TestGetSprints_FromSprintsFile(t *testing.T) {
	cs, dir := setup(t)
	writeFileT(t, filepath.Join(dir, "SPRINTS.md"), sprintsFixture)
	res := callTool(t, cs, "get_sprints", map[string]any{})
	var out RoadmapOutput
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Sprints) != 2 {
		t.Fatalf("got %d sprints, want 2", len(out.Sprints))
	}
	if out.Sprints[0].Number != 44 {
		t.Errorf("Number = %d, want 44", out.Sprints[0].Number)
	}
}

func TestGetBacklog_NoFile(t *testing.T) {
	cs, _ := setup(t)
	res := callTool(t, cs, "get_backlog", map[string]any{})
	var out BacklogOutput
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 0 {
		t.Errorf("got %d items, want 0", len(out.Items))
	}
}

func TestGetStrategicRoadmap_WithFile(t *testing.T) {
	cs, dir := setup(t)
	writeFileT(t, filepath.Join(dir, "ROADMAP.md"), roadmapFixture)
	res := callTool(t, cs, "get_strategic_roadmap", map[string]any{})
	var out StrategicRoadmapOutput
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	if out.Content != roadmapFixture {
		t.Errorf("content mismatch")
	}
}

// --- lifecycle: milestones ---

func TestAddMilestone(t *testing.T) {
	cs, dir := setup(t)
	seed(t, dir, roadmapFixture, sprintsFixture)

	res := callTool(t, cs, "add_milestone", map[string]any{
		"title":       "Third",
		"description": "New milestone body.",
	})
	var out MilestonesOutput
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	m, ok := findMilestone(out.Milestones, 3)
	if !ok || m.Title != "Third" || m.Status != core.StatusPlanned {
		t.Errorf("milestone 3 = %+v (found=%v), want Third/Planned", m, ok)
	}
	// The write hit disk and preserved milestone 1's prose.
	roadmap, _ := os.ReadFile(filepath.Join(dir, "ROADMAP.md"))
	if !strings.Contains(string(roadmap), "hand-written prose that must survive edits") {
		t.Error("milestone 1 prose was lost on disk")
	}
}

func TestStartMilestone_RejectsSecondInProgress(t *testing.T) {
	cs, dir := setup(t)
	seed(t, dir, roadmapFixture, sprintsFixture) // milestone 1 already In Progress

	res := callTool(t, cs, "start_milestone", map[string]any{"number": 2})
	if !res.IsError {
		t.Fatal("starting a second milestone should be rejected")
	}
	// Nothing was written: milestone 2 is still Planned on disk.
	roadmap, _ := os.ReadFile(filepath.Join(dir, "ROADMAP.md"))
	m, _ := findMilestone(core.ParseMilestones(string(roadmap)), 2)
	if m.Status != core.StatusPlanned {
		t.Errorf("milestone 2 status = %q, want unchanged Planned", m.Status)
	}
}

func TestPauseMilestone(t *testing.T) {
	cs, dir := setup(t)
	seed(t, dir, roadmapFixture, sprintsFixture)

	res := callTool(t, cs, "pause_milestone", map[string]any{"number": 1})
	var out MilestonesOutput
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	m, _ := findMilestone(out.Milestones, 1)
	if m.Status != core.StatusPaused {
		t.Errorf("milestone 1 status = %q, want %q", m.Status, core.StatusPaused)
	}
}

func TestFinishMilestone_RejectsOpenSprint(t *testing.T) {
	cs, dir := setup(t)
	seed(t, dir, roadmapFixture, sprintsFixture) // sprint 44 current, links milestone 1

	res := callTool(t, cs, "finish_milestone", map[string]any{"number": 1})
	if !res.IsError {
		t.Fatal("finishing a milestone with an open current sprint should be rejected")
	}
}

func TestFinishMilestone_AfterRollingSprint(t *testing.T) {
	cs, dir := setup(t)
	seed(t, dir, roadmapFixture, sprintsFixture)

	// Roll the open sprint (force, it has an unfinished item), then finish M1.
	if res := callTool(t, cs, "finish_sprint", map[string]any{"number": 44, "version": "0.2.0", "force": true}); res.IsError {
		t.Fatalf("finish_sprint force: %v", res.Content)
	}
	res := callTool(t, cs, "finish_milestone", map[string]any{"number": 1})
	var out MilestonesOutput
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	m, _ := findMilestone(out.Milestones, 1)
	if m.Status != core.StatusDone {
		t.Errorf("milestone 1 status = %q, want %q", m.Status, core.StatusDone)
	}
}

func TestRemoveMilestone(t *testing.T) {
	cs, dir := setup(t)
	seed(t, dir, roadmapFixture, sprintsFixture)

	res := callTool(t, cs, "remove_milestone", map[string]any{"number": 2})
	var out MilestonesOutput
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := findMilestone(out.Milestones, 2); ok {
		t.Error("milestone 2 still present after removal")
	}
}

// --- lifecycle: sprints ---

func TestAddSprint(t *testing.T) {
	cs, dir := setup(t)
	seed(t, dir, roadmapFixture, sprintsEmptyCurrent)

	res := callTool(t, cs, "add_sprint", map[string]any{
		"title":     "Fresh Work",
		"milestone": 1,
		"items":     []any{"first", "second"},
	})
	var out SprintsOutput
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	s, ok := findSprint(out.Sprints, 44) // max was 43
	if !ok {
		t.Fatal("new sprint 44 not returned")
	}
	if !s.IsCurrent || s.Milestone != 1 || len(s.Items) != 2 {
		t.Errorf("new sprint = %+v, want current/milestone 1/2 items", s)
	}
	for _, it := range s.Items {
		if it.Status != core.StatusPending {
			t.Errorf("item %d status = %q, want pending", it.Number, it.Status)
		}
	}
}

func TestAddSprint_RejectsSecondCurrent(t *testing.T) {
	cs, dir := setup(t)
	seed(t, dir, roadmapFixture, sprintsFixture) // already has a current sprint

	res := callTool(t, cs, "add_sprint", map[string]any{"title": "X", "milestone": 1})
	if !res.IsError {
		t.Fatal("adding a second current sprint should be rejected")
	}
}

func TestPauseAndResumeSprint(t *testing.T) {
	cs, dir := setup(t)
	seed(t, dir, roadmapFixture, sprintsFixture)

	res := callTool(t, cs, "pause_sprint", map[string]any{"number": 44})
	var out SprintsOutput
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	s, _ := findSprint(out.Sprints, 44)
	if s.Status != core.StatusPaused {
		t.Fatalf("sprint 44 status = %q, want Paused", s.Status)
	}

	res = callTool(t, cs, "start_sprint", map[string]any{"number": 44})
	var resumed SprintsOutput
	if err := json.Unmarshal([]byte(resultText(t, res)), &resumed); err != nil {
		t.Fatal(err)
	}
	s, _ = findSprint(resumed.Sprints, 44)
	if s.Status != "" {
		t.Errorf("sprint 44 status = %q after resume, want empty", s.Status)
	}
}

func TestMoveSprint(t *testing.T) {
	cs, dir := setup(t)
	seed(t, dir, roadmapFixture, sprintsFixture)

	res := callTool(t, cs, "move_sprint", map[string]any{"number": 44, "to_milestone": 2})
	var out SprintsOutput
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	s, _ := findSprint(out.Sprints, 44)
	if s.Milestone != 2 {
		t.Errorf("sprint 44 milestone = %d, want 2", s.Milestone)
	}
}

func TestFinishSprint_RejectsUnfinishedThenForce(t *testing.T) {
	cs, dir := setup(t)
	seed(t, dir, roadmapFixture, sprintsFixture) // sprint 44 has an unfinished item

	if res := callTool(t, cs, "finish_sprint", map[string]any{"number": 44, "version": "0.2.0"}); !res.IsError {
		t.Fatal("finishing a sprint with unfinished items should be rejected without force")
	}
	// Force it through.
	res := callTool(t, cs, "finish_sprint", map[string]any{"number": 44, "version": "0.2.0", "force": true})
	var out SprintsOutput
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	s, ok := findSprint(out.Sprints, 44)
	if !ok || s.Version != "0.2.0" || s.IsCurrent {
		t.Errorf("forced finish wrong: found=%v version=%q current=%v", ok, s.Version, s.IsCurrent)
	}
}

func TestRemoveSprint(t *testing.T) {
	cs, dir := setup(t)
	seed(t, dir, roadmapFixture, sprintsFixture)

	res := callTool(t, cs, "remove_sprint", map[string]any{"number": 43})
	var out SprintsOutput
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := findSprint(out.Sprints, 43); ok {
		t.Error("sprint 43 still present after removal")
	}
}

func TestLifecycleTool_MissingTarget(t *testing.T) {
	cs, dir := setup(t)
	seed(t, dir, roadmapFixture, sprintsFixture)
	if res := callTool(t, cs, "remove_milestone", map[string]any{"number": 99}); !res.IsError {
		t.Error("removing a missing milestone should error")
	}
	if res := callTool(t, cs, "pause_sprint", map[string]any{"number": 99}); !res.IsError {
		t.Error("pausing a missing sprint should error")
	}
}

// --- misc ---

func TestVersion_DefaultDev(t *testing.T) {
	if v := version(); v != "dev" {
		t.Logf("version() = %q (VERSION file exists)", v)
	}
}

func TestErrResult(t *testing.T) {
	res := errResult(os.ErrNotExist)
	if !res.IsError {
		t.Error("expected IsError=true")
	}
	if text := res.Content[0].(*mcp.TextContent).Text; text != "file does not exist" {
		t.Errorf("got %q, want %q", text, "file does not exist")
	}
}

// --- helpers ---

func findMilestone(ms []core.Milestone, n int) (core.Milestone, bool) {
	for _, m := range ms {
		if m.Number == n {
			return m, true
		}
	}
	return core.Milestone{}, false
}

func findSprint(ss []core.Sprint, n int) (core.Sprint, bool) {
	for _, s := range ss {
		if s.Number == n {
			return s, true
		}
	}
	return core.Sprint{}, false
}
