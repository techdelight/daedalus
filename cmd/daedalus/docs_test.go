// Copyright (C) 2026 Techdelight BV

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/control"
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

func TestScaffoldDocs_CleanThenLints(t *testing.T) {
	dir := t.TempDir()
	if err := scaffoldDocs([]string{dir}, false); err != nil {
		t.Fatalf("scaffoldDocs() = %v, want nil", err)
	}
	// Freshly scaffolded output must pass the --ci gate (warnings fail too).
	if err := lintDocs([]string{"--ci", dir}); err != nil {
		t.Errorf("lintDocs(--ci) on scaffolded dir = %v, want nil", err)
	}
}

func TestScaffoldDocs_SkipThenForce(t *testing.T) {
	dir := t.TempDir()
	writeDoc(t, dir, "README.md", "sentinel\n")

	// Without force the existing README is preserved.
	if err := scaffoldDocs([]string{dir}, false); err != nil {
		t.Fatalf("scaffoldDocs() = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}
	if string(got) != "sentinel\n" {
		t.Errorf("README.md overwritten without force: %q", string(got))
	}

	// With force it is rewritten from the template.
	if err := scaffoldDocs([]string{dir}, true); err != nil {
		t.Fatalf("scaffoldDocs(force) = %v", err)
	}
	got, err = os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}
	if string(got) == "sentinel\n" {
		t.Error("README.md not overwritten under force")
	}
}

func TestScaffoldDocs_UnknownFlag(t *testing.T) {
	if err := scaffoldDocs([]string{"--nope"}, false); err == nil {
		t.Error("scaffoldDocs(--nope) = nil, want an error")
	}
}

func TestManageDocs_UnknownCommand(t *testing.T) {
	cfg := &core.Config{DocsArgs: []string{"frobnicate"}}
	if err := manageDocs(cfg); err == nil {
		t.Error("manageDocs(frobnicate) = nil, want an error")
	}
}

// TestDefaultAcceptancePolicyDoesNotFailOnWarnings runs the built-in acceptance
// policy's own docs-lint check through the REAL gate function, against a
// warnings-only finding set, and asserts it passes.
//
// This is the unconditional half of the guard whose other half lives in
// internal/control. That one derives its premise from this repository's current
// documents, which makes it concrete but also makes it stand down whenever the
// roadmap happens to be warning-free — precisely the state a maintainer is in
// after opening a milestone, and no time to lose a guard. This one depends on
// nothing transient: it takes the shipped policy, parses its flags exactly as
// lintDocs does, and asks reportFindings — the function that actually decides
// pass or fail — what verdict a warning would get.
//
// The defect it pins: `daedalus docs lint --ci` was the default check, `--ci`
// makes a warning fatal, and a roadmap between milestones (a supported state the
// linter exits 0 on) emits exactly one warning and no errors. Every Task in every
// project that had declared no verify.json was rejected by it, whatever it did.
func TestDefaultAcceptancePolicyDoesNotFailOnWarnings(t *testing.T) {
	warn := core.Finding{Severity: core.SeverityWarning, Doc: "ROADMAP.md", Message: "advisory"}

	var checked int
	for _, check := range control.DefaultAcceptancePolicy().Checks {
		fields := strings.Fields(check)
		if len(fields) < 3 || fields[0] != "daedalus" || fields[1] != "docs" || fields[2] != "lint" {
			continue
		}
		checked++
		// Parse the flags the way lintDocs does, rather than looking for a literal
		// "--ci": a future spelling that means the same thing must fail this test too.
		ci := false
		for _, a := range fields[3:] {
			if a == "--ci" || a == "--strict" {
				ci = true
			}
		}
		if err := reportFindings([]core.Finding{warn}, ci); err != nil {
			t.Errorf("the built-in acceptance policy check %q fails on an advisory warning (%v); "+
				"as the oracle for every project that declares no .daedalus/verify.json, it must gate "+
				"on what is broken, not on what is merely remarked upon", check, err)
		}
	}
	if checked == 0 {
		t.Fatal("the default policy no longer runs `daedalus docs lint` — this test pins the wrong check")
	}
}
