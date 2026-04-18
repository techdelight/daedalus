// Copyright (C) 2026 Techdelight BV

package mcpclient

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/techdelight/daedalus/internal/progress"
)

func TestReadProgress_NoFile(t *testing.T) {
	// Arrange
	client := New()
	dir := t.TempDir()

	// Act
	data, err := client.ReadProgress(dir)

	// Assert
	if err != nil {
		t.Fatalf("ReadProgress() error = %v, want nil", err)
	}
	if data != (progress.Data{}) {
		t.Errorf("ReadProgress() = %+v, want zero-value Data", data)
	}
}

func TestReadProgress_WithFile(t *testing.T) {
	// Arrange
	client := New()
	dir := t.TempDir()
	want := progress.Data{
		ProgressPct:    65,
		Vision:         "Automate all the things",
		ProjectVersion: "1.2.0",
		Message:        "Sprint 5 underway",
	}
	writeProgressJSON(t, dir, want)

	// Act
	got, err := client.ReadProgress(dir)

	// Assert
	if err != nil {
		t.Fatalf("ReadProgress() error = %v", err)
	}
	if got != want {
		t.Errorf("ReadProgress() = %+v, want %+v", got, want)
	}
}

func TestReadRoadmap_NoFile(t *testing.T) {
	// Arrange
	client := New()
	dir := t.TempDir()

	// Act
	sprints, err := client.ReadRoadmap(dir)

	// Assert
	if err != nil {
		t.Fatalf("ReadRoadmap() error = %v, want nil", err)
	}
	if sprints != nil {
		t.Errorf("ReadRoadmap() = %+v, want nil", sprints)
	}
}

func TestReadRoadmap_WithFile(t *testing.T) {
	// Arrange
	client := New()
	dir := t.TempDir()
	roadmap := `# Roadmap

## Current Sprint

### Sprint 10: Polish (v2.0.0)

| # | Item | Status |
|---|------|--------|
| 1 | Fix bugs | Done |
| 2 | Add tests | In Progress |

## Future Sprints

### Sprint 11: Release

| # | Item | Status |
|---|------|--------|
| 1 | Deploy | |
`
	writeFile(t, filepath.Join(dir, "ROADMAP.md"), roadmap)

	// Act
	sprints, err := client.ReadRoadmap(dir)

	// Assert
	if err != nil {
		t.Fatalf("ReadRoadmap() error = %v", err)
	}
	if len(sprints) != 2 {
		t.Fatalf("ReadRoadmap() returned %d sprints, want 2", len(sprints))
	}
	if sprints[0].Number != 10 {
		t.Errorf("sprints[0].Number = %d, want 10", sprints[0].Number)
	}
	if !sprints[0].IsCurrent {
		t.Errorf("sprints[0].IsCurrent = false, want true")
	}
	if len(sprints[0].Items) != 2 {
		t.Errorf("sprints[0] has %d items, want 2", len(sprints[0].Items))
	}
	if sprints[1].IsCurrent {
		t.Errorf("sprints[1].IsCurrent = true, want false")
	}
}

func TestGetCurrentSprint(t *testing.T) {
	// Arrange
	client := New()
	dir := t.TempDir()
	roadmap := `# Roadmap

## Current Sprint

### Sprint 7: Dashboard (v1.5.0)

Goal: Build the dashboard

| # | Item | Status |
|---|------|--------|
| 1 | API endpoints | Done |
| 2 | Frontend | In Progress |
`
	writeFile(t, filepath.Join(dir, "ROADMAP.md"), roadmap)

	// Act
	sprint, err := client.GetCurrentSprint(dir)

	// Assert
	if err != nil {
		t.Fatalf("GetCurrentSprint() error = %v", err)
	}
	if sprint == nil {
		t.Fatal("GetCurrentSprint() = nil, want non-nil")
	}
	if sprint.Number != 7 {
		t.Errorf("sprint.Number = %d, want 7", sprint.Number)
	}
	if sprint.Title != "Dashboard" {
		t.Errorf("sprint.Title = %q, want %q", sprint.Title, "Dashboard")
	}
	if sprint.Version != "1.5.0" {
		t.Errorf("sprint.Version = %q, want %q", sprint.Version, "1.5.0")
	}
	if sprint.Goal != "Build the dashboard" {
		t.Errorf("sprint.Goal = %q, want %q", sprint.Goal, "Build the dashboard")
	}
}

func TestGetProjectStatus(t *testing.T) {
	// Arrange
	client := New()
	dir := t.TempDir()

	prog := progress.Data{
		ProgressPct:    80,
		Vision:         "Ship it",
		ProjectVersion: "3.0.0",
		Message:        "Almost there",
	}
	writeProgressJSON(t, dir, prog)

	roadmap := `# Roadmap

## Current Sprint

### Sprint 12: Final (v3.0.0)

| # | Item | Status |
|---|------|--------|
| 1 | Release | In Progress |
`
	writeFile(t, filepath.Join(dir, "ROADMAP.md"), roadmap)

	// Act
	status, err := client.GetProjectStatus("my-project", dir)

	// Assert
	if err != nil {
		t.Fatalf("GetProjectStatus() error = %v", err)
	}
	if status.Name != "my-project" {
		t.Errorf("Name = %q, want %q", status.Name, "my-project")
	}
	if status.ProgressPct != 80 {
		t.Errorf("ProgressPct = %d, want 80", status.ProgressPct)
	}
	if status.Vision != "Ship it" {
		t.Errorf("Vision = %q, want %q", status.Vision, "Ship it")
	}
	if status.ProjectVersion != "3.0.0" {
		t.Errorf("ProjectVersion = %q, want %q", status.ProjectVersion, "3.0.0")
	}
	if status.Message != "Almost there" {
		t.Errorf("Message = %q, want %q", status.Message, "Almost there")
	}
	if status.CurrentSprint == nil {
		t.Fatal("CurrentSprint = nil, want non-nil")
	}
	if status.CurrentSprint.Number != 12 {
		t.Errorf("CurrentSprint.Number = %d, want 12", status.CurrentSprint.Number)
	}
}

func TestReadSprints_FromSprintsFile(t *testing.T) {
	client := New()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SPRINTS.md"), `## Current Sprint

### Sprint 5: Test (v1.0.0)

| # | Item | Status |
|---|------|--------|
| 1 | Task A | Done |
`)
	sprints, err := client.ReadSprints(dir)
	if err != nil {
		t.Fatalf("ReadSprints() error = %v", err)
	}
	if len(sprints) != 1 {
		t.Fatalf("got %d sprints, want 1", len(sprints))
	}
	if sprints[0].Number != 5 {
		t.Errorf("Number = %d, want 5", sprints[0].Number)
	}
}

func TestReadSprints_FallsBackToRoadmap(t *testing.T) {
	client := New()
	dir := t.TempDir()
	// No SPRINTS.md, only ROADMAP.md
	writeFile(t, filepath.Join(dir, "ROADMAP.md"), `## Current Sprint

### Sprint 3: Legacy (v0.5.0)

| # | Item | Status |
|---|------|--------|
| 1 | Old task | Done |
`)
	sprints, err := client.ReadSprints(dir)
	if err != nil {
		t.Fatalf("ReadSprints() error = %v", err)
	}
	if len(sprints) != 1 {
		t.Fatalf("got %d sprints, want 1", len(sprints))
	}
	if sprints[0].Number != 3 {
		t.Errorf("Number = %d, want 3", sprints[0].Number)
	}
}

func TestReadSprints_PrefersSprintsOverRoadmap(t *testing.T) {
	client := New()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SPRINTS.md"), `## Current Sprint

### Sprint 10: New (v2.0.0)

| # | Item | Status |
|---|------|--------|
| 1 | New task | Done |
`)
	writeFile(t, filepath.Join(dir, "ROADMAP.md"), `## Current Sprint

### Sprint 1: Old (v0.1.0)

| # | Item | Status |
|---|------|--------|
| 1 | Old task | Done |
`)
	sprints, err := client.ReadSprints(dir)
	if err != nil {
		t.Fatalf("ReadSprints() error = %v", err)
	}
	if len(sprints) != 1 {
		t.Fatalf("got %d sprints, want 1", len(sprints))
	}
	if sprints[0].Number != 10 {
		t.Errorf("Number = %d, want 10 (should prefer SPRINTS.md)", sprints[0].Number)
	}
}

func TestReadSprints_NoFiles(t *testing.T) {
	client := New()
	dir := t.TempDir()
	sprints, err := client.ReadSprints(dir)
	if err != nil {
		t.Fatalf("ReadSprints() error = %v", err)
	}
	if sprints != nil {
		t.Errorf("got %v, want nil", sprints)
	}
}

func TestReadBacklog_WithFile(t *testing.T) {
	client := New()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "BACKLOG.md"), `# Backlog

| # | Item |
|---|------|
| 1 | First task |
| 2 | Second task |
`)
	items, err := client.ReadBacklog(dir)
	if err != nil {
		t.Fatalf("ReadBacklog() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].Description != "First task" {
		t.Errorf("items[0].Description = %q", items[0].Description)
	}
}

func TestReadBacklog_NoFile(t *testing.T) {
	client := New()
	dir := t.TempDir()
	items, err := client.ReadBacklog(dir)
	if err != nil {
		t.Fatalf("ReadBacklog() error = %v", err)
	}
	if items != nil {
		t.Errorf("got %v, want nil", items)
	}
}

func TestReadStrategicRoadmap_WithFile(t *testing.T) {
	client := New()
	dir := t.TempDir()
	content := "# Roadmap\n\n## Milestone 1\n\nShip the MVP.\n"
	writeFile(t, filepath.Join(dir, "ROADMAP.md"), content)
	got, err := client.ReadStrategicRoadmap(dir)
	if err != nil {
		t.Fatalf("ReadStrategicRoadmap() error = %v", err)
	}
	if got != content {
		t.Errorf("got %q, want %q", got, content)
	}
}

func TestReadStrategicRoadmap_NoFile(t *testing.T) {
	client := New()
	dir := t.TempDir()
	got, err := client.ReadStrategicRoadmap(dir)
	if err != nil {
		t.Fatalf("ReadStrategicRoadmap() error = %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// writeProgressJSON writes a progress.json file in the .daedalus/ subdirectory.
func writeProgressJSON(t *testing.T, dir string, d progress.Data) {
	t.Helper()
	daedalusDir := filepath.Join(dir, ".daedalus")
	if err := os.MkdirAll(daedalusDir, 0755); err != nil {
		t.Fatalf("creating .daedalus dir: %v", err)
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		t.Fatalf("marshaling progress data: %v", err)
	}
	if err := os.WriteFile(filepath.Join(daedalusDir, "progress.json"), b, 0644); err != nil {
		t.Fatalf("writing progress.json: %v", err)
	}
}

// writeFile writes content to a file, creating parent directories as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("creating directory %q: %v", dir, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writing file %q: %v", path, err)
	}
}
