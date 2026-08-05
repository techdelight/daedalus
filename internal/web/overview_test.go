// Copyright (C) 2026 Techdelight BV

package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/techdelight/daedalus/core"
)

const testRoadmap = `# Roadmap

## Milestones

### Milestone 1: Container Runtime (Done)

Single-command launch.

### Milestone 2: Layered Architecture (In Progress)

Runner and coordinator.

### Milestone 3: Self-Sustaining Ops

- Homebrew distribution
`

const testSprints = `# Sprints

## Current Sprint

### Sprint 41: Trust-Prompt (v0.40.0)

Goal: close the trust-prompt gap.

| # | Item | Status |
|---|------|--------|
| 1 | Pre-seed trust | Done |
| 2 | Repaint on attach | In Progress |

## Sprint History

### Sprint 40: Coordinator (v0.39.0)

Goal: promote the coordinator.
`

const testBacklog = `# Backlog

| # | Item |
|---|------|
| 52 | Derive project state from files |
| 53 | Per-project doc opt-out |
`

// newProject registers a project backed by a temp dir and returns the dir.
func newProject(t *testing.T, ws *WebServer, name string) string {
	t.Helper()
	dir := t.TempDir()
	if err := ws.registry.AddProject(name, dir, "dev"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func getOverview(t *testing.T, ws *WebServer, project string) (*httptest.ResponseRecorder, overviewJSON) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/overview", ws.handleOverview)
	req := httptest.NewRequest("GET", "/api/projects/"+project+"/overview", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var resp overviewJSON
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return rec, resp
}

func getMilestones(t *testing.T, ws *WebServer, project string) (*httptest.ResponseRecorder, milestonesJSON) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/milestones", ws.handleMilestones)
	req := httptest.NewRequest("GET", "/api/projects/"+project+"/milestones", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var resp milestonesJSON
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return rec, resp
}

// The point of the aggregate: one request paints the whole journey.
func TestHandleOverview_AllSections(t *testing.T) {
	ws, _ := setupWebTest(t)
	dir := newProject(t, ws, "app")
	writeFile(t, dir, "VISION.md", "Orchestrate agents.\n")
	writeFile(t, dir, "ROADMAP.md", testRoadmap)
	writeFile(t, dir, "SPRINTS.md", testSprints)
	writeFile(t, dir, "BACKLOG.md", testBacklog)

	rec, resp := getOverview(t, ws, "app")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	if resp.Vision != "Orchestrate agents.\n" {
		t.Errorf("Vision = %q", resp.Vision)
	}
	if len(resp.Milestones) != 3 {
		t.Fatalf("got %d milestones, want 3", len(resp.Milestones))
	}
	if resp.Milestones[1].Status != core.StatusInProgress {
		t.Errorf("milestone 2 Status = %q, want %q", resp.Milestones[1].Status, core.StatusInProgress)
	}
	if resp.Milestones[2].Status != core.StatusPlanned {
		t.Errorf("milestone 3 Status = %q, want %q", resp.Milestones[2].Status, core.StatusPlanned)
	}
	if resp.CurrentSprint == nil {
		t.Fatal("CurrentSprint = nil, want Sprint 41")
	}
	if resp.CurrentSprint.Number != 41 {
		t.Errorf("CurrentSprint.Number = %d, want 41", resp.CurrentSprint.Number)
	}
	if len(resp.CurrentSprint.Items) != 2 {
		t.Errorf("CurrentSprint has %d items, want 2", len(resp.CurrentSprint.Items))
	}
	if len(resp.Backlog) != 2 {
		t.Errorf("got %d backlog items, want 2", len(resp.Backlog))
	}
}

// A project with none of the documents is a normal, early-life project. The
// dashboard must render its emptiness, so every section is empty and the call
// still succeeds — this is the deliberate divergence from handleVision's 404.
func TestHandleOverview_NoDocumentsIsEmptyNotError(t *testing.T) {
	ws, _ := setupWebTest(t)
	newProject(t, ws, "bare")

	rec, resp := getOverview(t, ws, "bare")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if resp.Vision != "" {
		t.Errorf("Vision = %q, want empty", resp.Vision)
	}
	if len(resp.Milestones) != 0 {
		t.Errorf("Milestones = %+v, want empty", resp.Milestones)
	}
	if resp.CurrentSprint != nil {
		t.Errorf("CurrentSprint = %+v, want nil", resp.CurrentSprint)
	}
	if len(resp.Backlog) != 0 {
		t.Errorf("Backlog = %+v, want empty", resp.Backlog)
	}
}

// Empty slices must serialise as [] rather than null: the frontend maps over
// them directly, and null would throw where an empty section should render.
func TestHandleOverview_EmptySectionsSerialiseAsArrays(t *testing.T) {
	ws, _ := setupWebTest(t)
	newProject(t, ws, "bare")

	rec, _ := getOverview(t, ws, "bare")
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"milestones", "backlog"} {
		if got := string(raw[key]); got != "[]" {
			t.Errorf("%s = %s, want []", key, got)
		}
	}
	// ...but absent-sprint really is null: it is one value, not a list.
	if got := string(raw["currentSprint"]); got != "null" {
		t.Errorf("currentSprint = %s, want null", got)
	}
}

// A `touch`ed VISION.md is a placeholder, not a vision. docPresent refuses to
// count one toward the badge, so the overview must not serve one either or the
// dashboard would show an empty Purpose while the badge calls it missing.
func TestHandleOverview_EmptyVisionFileReadsAsAbsent(t *testing.T) {
	ws, _ := setupWebTest(t)
	dir := newProject(t, ws, "placeholder")
	writeFile(t, dir, "VISION.md", "")

	_, resp := getOverview(t, ws, "placeholder")
	if resp.Vision != "" {
		t.Errorf("Vision = %q, want empty for a placeholder file", resp.Vision)
	}
}

// Projects predating the doc split keep sprints in ROADMAP.md; readSprints
// falls back to it, and the overview must inherit that fallback rather than
// showing no sprint. The same file's milestones still parse.
func TestHandleOverview_LegacyRoadmapSprintFallback(t *testing.T) {
	ws, _ := setupWebTest(t)
	dir := newProject(t, ws, "legacy")
	writeFile(t, dir, "ROADMAP.md", testRoadmap+testSprints)

	_, resp := getOverview(t, ws, "legacy")
	if resp.CurrentSprint == nil {
		t.Fatal("CurrentSprint = nil, want Sprint 41 via the ROADMAP.md fallback")
	}
	if resp.CurrentSprint.Number != 41 {
		t.Errorf("CurrentSprint.Number = %d, want 41", resp.CurrentSprint.Number)
	}
	if len(resp.Milestones) != 3 {
		t.Errorf("got %d milestones, want 3 from the same file", len(resp.Milestones))
	}
}

// Only the sprint under "## Current Sprint" is current; history is not.
func TestHandleOverview_CurrentSprintIsNotHistory(t *testing.T) {
	ws, _ := setupWebTest(t)
	dir := newProject(t, ws, "app")
	writeFile(t, dir, "SPRINTS.md", testSprints)

	_, resp := getOverview(t, ws, "app")
	if resp.CurrentSprint == nil {
		t.Fatal("CurrentSprint = nil")
	}
	if resp.CurrentSprint.Number == 40 {
		t.Error("CurrentSprint picked Sprint 40 out of Sprint History")
	}
}

func TestHandleOverview_UnknownProject404s(t *testing.T) {
	ws, _ := setupWebTest(t)
	rec, _ := getOverview(t, ws, "nope")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHandleMilestones_ParsesRoadmap(t *testing.T) {
	ws, _ := setupWebTest(t)
	dir := newProject(t, ws, "app")
	writeFile(t, dir, "ROADMAP.md", testRoadmap)

	rec, resp := getMilestones(t, ws, "app")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if len(resp.Milestones) != 3 {
		t.Fatalf("got %d milestones, want 3", len(resp.Milestones))
	}
	if resp.Milestones[0].Title != "Container Runtime" {
		t.Errorf("Title = %q, want %q", resp.Milestones[0].Title, "Container Runtime")
	}
	if resp.Milestones[0].Status != core.StatusDone {
		t.Errorf("Status = %q, want %q", resp.Milestones[0].Status, core.StatusDone)
	}
}

func TestHandleMilestones_NoRoadmapIsEmptyArray(t *testing.T) {
	ws, _ := setupWebTest(t)
	newProject(t, ws, "bare")

	rec, resp := getMilestones(t, ws, "bare")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(resp.Milestones) != 0 {
		t.Errorf("Milestones = %+v, want empty", resp.Milestones)
	}
	if got := rec.Body.String(); got != "{\"milestones\":[]}\n" {
		t.Errorf("body = %q, want an empty array not null", got)
	}
}

func TestHandleMilestones_UnknownProject404s(t *testing.T) {
	ws, _ := setupWebTest(t)
	rec, _ := getMilestones(t, ws, "nope")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func getMilestoneSprints(t *testing.T, ws *WebServer, project string) (*httptest.ResponseRecorder, milestoneSprintsJSON) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/milestone-sprints", ws.handleMilestoneSprints)
	req := httptest.NewRequest("GET", "/api/projects/"+project+"/milestone-sprints", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var resp milestoneSprintsJSON
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return rec, resp
}

func TestHandleMilestoneSprints_FiltersToActiveAndPhases(t *testing.T) {
	ws, _ := setupWebTest(t)
	dir := newProject(t, ws, "app")
	writeFile(t, dir, "ROADMAP.md", `# Roadmap

### Milestone 1: Foundation (Done)

Done work.

### Milestone 2: Current Work (In Progress)

The active one.
`)
	writeFile(t, dir, "SPRINTS.md", `# Sprints

## Current Sprint

### Sprint 10: Building It

Milestone: 2

| # | Item | Status |
|---|------|--------|
| 1 | a | Done |
| 2 | b | In Progress |

## Sprint History

### Sprint 9: Shipped It (v1.0.0)

Milestone: 2

| # | Item | Status |
|---|------|--------|
| 1 | a | Done |

### Sprint 8: Other Milestone (v0.9.0)

Milestone: 1

| # | Item | Status |
|---|------|--------|
| 1 | a | Done |
`)

	rec, resp := getMilestoneSprints(t, ws, "app")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if resp.ActiveMilestone == nil || resp.ActiveMilestone.Number != 2 {
		t.Fatalf("ActiveMilestone = %+v, want milestone 2", resp.ActiveMilestone)
	}
	// Sprint 8 (milestone 1) excluded; 10 and 9 kept, newest first.
	if len(resp.Sprints) != 2 {
		t.Fatalf("got %d sprints, want 2: %+v", len(resp.Sprints), resp.Sprints)
	}
	if resp.Sprints[0].Number != 10 || resp.Sprints[0].Phase != core.PhaseBuilding {
		t.Errorf("sprint[0] = #%d %s, want #10 Building", resp.Sprints[0].Number, resp.Sprints[0].Phase)
	}
	if resp.Sprints[0].Done != 1 || resp.Sprints[0].Total != 2 {
		t.Errorf("sprint[0] progress = %d/%d, want 1/2", resp.Sprints[0].Done, resp.Sprints[0].Total)
	}
	if resp.Sprints[1].Number != 9 || resp.Sprints[1].Phase != core.PhaseShipped || resp.Sprints[1].Version != "1.0.0" {
		t.Errorf("sprint[1] = #%d %s %q, want #9 Shipped 1.0.0", resp.Sprints[1].Number, resp.Sprints[1].Phase, resp.Sprints[1].Version)
	}
}

func TestHandleMilestoneSprints_NoActiveMilestone(t *testing.T) {
	ws, _ := setupWebTest(t)
	dir := newProject(t, ws, "app")
	writeFile(t, dir, "ROADMAP.md", `# Roadmap

### Milestone 1: Done Thing (Done)

All done.
`)
	rec, resp := getMilestoneSprints(t, ws, "app")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if resp.ActiveMilestone != nil {
		t.Errorf("ActiveMilestone = %+v, want nil", resp.ActiveMilestone)
	}
	if len(resp.Sprints) != 0 {
		t.Errorf("Sprints = %+v, want empty", resp.Sprints)
	}
}
