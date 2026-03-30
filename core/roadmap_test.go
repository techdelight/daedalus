// Copyright (C) 2026 Techdelight BV

package core

import "testing"

func TestParseRoadmap_CurrentSprint(t *testing.T) {
	// Arrange
	input := `## Current Sprint

### Sprint 10: Web Dashboard (v2.0.0)

Goal: Build a real-time web dashboard for project monitoring.

| # | Item | Status |
|---|------|--------|
| 1 | REST API endpoints | Done |
| 2 | WebSocket integration | In Progress |
| 3 | Dashboard styling | |
`

	// Act
	sprints := ParseRoadmap(input)

	// Assert
	if len(sprints) != 1 {
		t.Fatalf("got %d sprints, want 1", len(sprints))
	}

	s := sprints[0]
	if s.Number != 10 {
		t.Errorf("Number = %d, want 10", s.Number)
	}
	if s.Title != "Web Dashboard" {
		t.Errorf("Title = %q, want %q", s.Title, "Web Dashboard")
	}
	if s.Version != "2.0.0" {
		t.Errorf("Version = %q, want %q", s.Version, "2.0.0")
	}
	if s.Goal != "Build a real-time web dashboard for project monitoring." {
		t.Errorf("Goal = %q, want %q", s.Goal, "Build a real-time web dashboard for project monitoring.")
	}
	if !s.IsCurrent {
		t.Error("IsCurrent = false, want true")
	}
	if len(s.Items) != 3 {
		t.Fatalf("got %d items, want 3", len(s.Items))
	}
	if s.Items[0].Number != 1 {
		t.Errorf("Items[0].Number = %d, want 1", s.Items[0].Number)
	}
	if s.Items[0].Description != "REST API endpoints" {
		t.Errorf("Items[0].Description = %q, want %q", s.Items[0].Description, "REST API endpoints")
	}
	if s.Items[0].Status != StatusDone {
		t.Errorf("Items[0].Status = %q, want %q", s.Items[0].Status, StatusDone)
	}
}

func TestParseRoadmap_MultipleSprints(t *testing.T) {
	// Arrange
	input := `## Current Sprint

### Sprint 5: Polish (v1.5.0)

Goal: Final polish pass.

| # | Item | Status |
|---|------|--------|
| 1 | Fix bugs | Done |

### Sprint 4: Features (v1.4.0)

Goal: Add new features.

| # | Item | Status |
|---|------|--------|
| 1 | Add feature A | Done |

## Sprint History

### Sprint 3: Foundation (v1.3.0)

Goal: Build foundation.

| # | Item | Status |
|---|------|--------|
| 1 | Setup project | Done |
`

	// Act
	sprints := ParseRoadmap(input)

	// Assert
	if len(sprints) != 3 {
		t.Fatalf("got %d sprints, want 3", len(sprints))
	}

	// Sprints under Current Sprint are marked current.
	if !sprints[0].IsCurrent {
		t.Error("sprints[0].IsCurrent = false, want true")
	}
	if sprints[0].Number != 5 {
		t.Errorf("sprints[0].Number = %d, want 5", sprints[0].Number)
	}

	if !sprints[1].IsCurrent {
		t.Error("sprints[1].IsCurrent = false, want true")
	}
	if sprints[1].Number != 4 {
		t.Errorf("sprints[1].Number = %d, want 4", sprints[1].Number)
	}

	// Sprint under Sprint History is not current.
	if sprints[2].IsCurrent {
		t.Error("sprints[2].IsCurrent = true, want false")
	}
	if sprints[2].Number != 3 {
		t.Errorf("sprints[2].Number = %d, want 3", sprints[2].Number)
	}
}

func TestParseRoadmap_ItemStatuses(t *testing.T) {
	// Arrange
	input := `## Current Sprint

### Sprint 1: Test (v0.1.0)

| # | Item | Status |
|---|------|--------|
| 1 | Completed task | Done |
| 2 | Pending task | |
| 3 | Active task | In Progress |
`

	// Act
	sprints := ParseRoadmap(input)

	// Assert
	if len(sprints) != 1 {
		t.Fatalf("got %d sprints, want 1", len(sprints))
	}
	items := sprints[0].Items
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	if items[0].Status != StatusDone {
		t.Errorf("items[0].Status = %q, want %q", items[0].Status, StatusDone)
	}
	if items[1].Status != StatusPending {
		t.Errorf("items[1].Status = %q, want %q", items[1].Status, StatusPending)
	}
	if items[2].Status != StatusInProgress {
		t.Errorf("items[2].Status = %q, want %q", items[2].Status, StatusInProgress)
	}
}

func TestParseRoadmap_Empty(t *testing.T) {
	// Arrange
	input := ""

	// Act
	sprints := ParseRoadmap(input)

	// Assert
	if sprints != nil {
		t.Errorf("got %v, want nil", sprints)
	}
}

func TestParseRoadmap_RealFormat(t *testing.T) {
	// Arrange — realistic excerpt modeled on the actual ROADMAP.md Sprint 23 section.
	input := "## Current Sprint\n" +
		"\n" +
		"### Sprint 23: Project Management View in Web UI (v0.18.0)\n" +
		"\n" +
		"Goal: per-project dashboard showing vision, version, time spent, and progress percentage — the foundation for the Foreman agent's reporting layer. Implements backlog item 13.\n" +
		"\n" +
		"| # | Item | Status |\n" +
		"|---|------|--------|\n" +
		"| 1 | `core/project.go` — add `ProgressPct`, `Vision`, `ProjectVersion` fields to `ProjectEntry` with tests | Done |\n" +
		"| 2 | `internal/registry/` — v2-to-v3 migration (new fields default to zero values) with migration test | Done |\n" +
		"| 3 | `internal/registry/` — `UpdateProjectProgress(name, pct, vision, version)` method with tests | Done |\n" +
		"| 4 | `internal/web/` — `GET /api/projects/{name}/dashboard` endpoint returning progress data with tests | Done |\n" +
		"| 5 | `internal/web/static/` — project detail panel (click project row to see vision, version, total session time, progress bar) | Done |\n" +
		"| 6 | Documentation — update ARCHITECTURE.md, CHANGELOG.md, VERSION, README.md | Done |\n" +
		"\n" +
		"## Sprint History\n" +
		"\n" +
		"### Sprint 22: Runner/Persona Polish & Skill Fix (v0.17.0)\n" +
		"\n" +
		"Goal: clean up the runner/persona split — add `daedalus runners` subcommand, separate `personas list` from runners, store persona details in companion `.md` files, fix skill installation path, and harden validation and test coverage.\n" +
		"\n" +
		"| # | Item | Status |\n" +
		"|---|------|--------|\n" +
		"| 1 | `daedalus runners` subcommand — list and show built-in runner profiles with shell completions | Done |\n" +
		"| 2 | `personas list` shows only user-defined personas, `personas show` rejects built-in names | Done |\n" +
		"| 3 | Persona `.md` companion file — store CLAUDE.md content alongside `.json` config | Done |\n"

	// Act
	sprints := ParseRoadmap(input)

	// Assert
	if len(sprints) != 2 {
		t.Fatalf("got %d sprints, want 2", len(sprints))
	}

	// Sprint 23 — current.
	s23 := sprints[0]
	if s23.Number != 23 {
		t.Errorf("s23.Number = %d, want 23", s23.Number)
	}
	if s23.Title != "Project Management View in Web UI" {
		t.Errorf("s23.Title = %q, want %q", s23.Title, "Project Management View in Web UI")
	}
	if s23.Version != "0.18.0" {
		t.Errorf("s23.Version = %q, want %q", s23.Version, "0.18.0")
	}
	if !s23.IsCurrent {
		t.Error("s23.IsCurrent = false, want true")
	}
	expectedGoal := "per-project dashboard showing vision, version, time spent, and progress percentage — the foundation for the Foreman agent's reporting layer. Implements backlog item 13."
	if s23.Goal != expectedGoal {
		t.Errorf("s23.Goal = %q, want %q", s23.Goal, expectedGoal)
	}
	if len(s23.Items) != 6 {
		t.Fatalf("s23 got %d items, want 6", len(s23.Items))
	}
	// Verify first item has backtick content preserved.
	if s23.Items[0].Number != 1 {
		t.Errorf("s23.Items[0].Number = %d, want 1", s23.Items[0].Number)
	}
	wantDesc := "`core/project.go` — add `ProgressPct`, `Vision`, `ProjectVersion` fields to `ProjectEntry` with tests"
	if s23.Items[0].Description != wantDesc {
		t.Errorf("s23.Items[0].Description = %q, want %q", s23.Items[0].Description, wantDesc)
	}
	if s23.Items[0].Status != StatusDone {
		t.Errorf("s23.Items[0].Status = %q, want %q", s23.Items[0].Status, StatusDone)
	}

	// Sprint 22 — historical.
	s22 := sprints[1]
	if s22.Number != 22 {
		t.Errorf("s22.Number = %d, want 22", s22.Number)
	}
	if s22.Version != "0.17.0" {
		t.Errorf("s22.Version = %q, want %q", s22.Version, "0.17.0")
	}
	if s22.IsCurrent {
		t.Error("s22.IsCurrent = true, want false")
	}
	if len(s22.Items) != 3 {
		t.Fatalf("s22 got %d items, want 3", len(s22.Items))
	}
}
