// Copyright (C) 2026 Techdelight BV

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/techdelight/daedalus/core"
)

func TestReportFindings_Gate(t *testing.T) {
	err := core.Finding{Severity: core.SeverityError, Doc: "ROADMAP.md", Message: "x"}
	warn := core.Finding{Severity: core.SeverityWarning, Doc: "SPRINTS.md", Message: "y"}

	tests := []struct {
		name     string
		findings []core.Finding
		ci       bool
		wantFail bool
	}{
		{"clean", nil, false, false},
		{"clean under ci", nil, true, false},
		{"error fails", []core.Finding{err}, false, true},
		{"error fails under ci", []core.Finding{err}, true, true},
		{"warning passes without ci", []core.Finding{warn}, false, false},
		{"warning fails under ci", []core.Finding{warn}, true, true},
		{"error dominates", []core.Finding{warn, err}, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotErr := reportFindings(tc.findings, tc.ci)
			if (gotErr != nil) != tc.wantFail {
				t.Errorf("reportFindings(ci=%v) error = %v, want fail=%v", tc.ci, gotErr, tc.wantFail)
			}
		})
	}
}

func writeDoc(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func TestLintDocs_CleanDir(t *testing.T) {
	dir := t.TempDir()
	writeDoc(t, dir, "ROADMAP.md", "### Milestone 1: Foundations (In Progress)\n")
	writeDoc(t, dir, "SPRINTS.md", `## Current Sprint

### Sprint 7: Build (v1.0.0)

Milestone: 1

| # | Item | Status |
|---|------|--------|
| 1 | A | Done |
`)
	if err := lintDocs([]string{dir}); err != nil {
		t.Errorf("lintDocs() on clean dir = %v, want nil", err)
	}
}

func TestLintDocs_DroppedHeadingFails(t *testing.T) {
	dir := t.TempDir()
	// Wrong level: this milestone is silently dropped by the parser.
	writeDoc(t, dir, "ROADMAP.md", "## Milestone 1: Foundations (In Progress)\n")
	if err := lintDocs([]string{dir}); err == nil {
		t.Error("lintDocs() on a dropped heading = nil, want failure")
	}
}

func TestLintDocs_NoDocsIsNotAFailure(t *testing.T) {
	dir := t.TempDir()
	if err := lintDocs([]string{dir}); err != nil {
		t.Errorf("lintDocs() on empty dir = %v, want nil (nothing to lint)", err)
	}
}

func TestLintDocs_UnknownFlag(t *testing.T) {
	if err := lintDocs([]string{"--nope"}); err == nil {
		t.Error("lintDocs(--nope) = nil, want an error")
	}
}

func TestManageDocs_UnknownCommand(t *testing.T) {
	cfg := &core.Config{DocsArgs: []string{"frobnicate"}}
	if err := manageDocs(cfg); err == nil {
		t.Error("manageDocs(frobnicate) = nil, want an error")
	}
}
