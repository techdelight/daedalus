// Copyright (C) 2026 Techdelight BV

package progress

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

// With none of VERSION, VISION.md or SPRINTS.md present, every derived field is
// its zero value — a bare project is a valid, empty state, not an error.
func TestRead_NoFiles(t *testing.T) {
	data, err := Read(t.TempDir())
	if err != nil {
		t.Fatalf("Read() error = %v, want nil", err)
	}
	if data != (Data{}) {
		t.Errorf("Read() = %+v, want zero-value Data", data)
	}
}

// Version and vision are the trimmed contents of their files.
func TestRead_DerivesVersionAndVision(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "VERSION", "1.4.2\n")
	writeFile(t, dir, "VISION.md", "  Automate everything.\n")

	data, err := Read(dir)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if data.ProjectVersion != "1.4.2" {
		t.Errorf("ProjectVersion = %q, want %q", data.ProjectVersion, "1.4.2")
	}
	if data.Vision != "Automate everything." {
		t.Errorf("Vision = %q, want %q", data.Vision, "Automate everything.")
	}
}

// Progress is the current sprint's Done/total ratio as a percentage.
func TestRead_DerivesProgressFromCurrentSprint(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "SPRINTS.md", `## Current Sprint

### Sprint 5: Work

| # | Item | Status |
|---|------|--------|
| 1 | a | Done |
| 2 | b | Done |
| 3 | c | Done |
| 4 | d | In Progress |

## Sprint History

### Sprint 4: Old (v0.1.0)

| # | Item | Status |
|---|------|--------|
| 1 | x | Done |
`)
	data, err := Read(dir)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if data.ProgressPct != 75 {
		t.Errorf("ProgressPct = %d, want 75 (3 of 4 done)", data.ProgressPct)
	}
}

// No current sprint (or an itemless one) means nothing to measure — 0, not an
// invented number.
func TestRead_ProgressZeroWithoutCurrentSprint(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "SPRINTS.md", `## Sprint History

### Sprint 4: Old (v0.1.0)

| # | Item | Status |
|---|------|--------|
| 1 | x | Done |
`)
	data, err := Read(dir)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if data.ProgressPct != 0 {
		t.Errorf("ProgressPct = %d, want 0 (no current sprint)", data.ProgressPct)
	}
}

// A legacy project keeping its sprints in ROADMAP.md still derives progress.
func TestRead_ProgressFallsBackToRoadmap(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ROADMAP.md", `## Current Sprint

### Sprint 1: Foundation

| # | Item | Status |
|---|------|--------|
| 1 | a | Done |
| 2 | b | |
`)
	data, err := Read(dir)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if data.ProgressPct != 50 {
		t.Errorf("ProgressPct = %d, want 50 (1 of 2 done, ROADMAP fallback)", data.ProgressPct)
	}
}
