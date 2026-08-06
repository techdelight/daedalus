// Copyright (C) 2026 Techdelight BV

package core

import (
	"os"
	"path/filepath"
	"testing"
)

// TestScaffoldDocs_ConformsToContract is the load-bearing test: a freshly
// scaffolded project must be clean under the exact gate `daedalus docs lint`
// runs — the strict-format heading checks plus the cross-file validation — with
// zero findings of ANY severity (the --ci bar). If a template drifts out of
// contract, this fails here rather than on a real bootstrap.
func TestScaffoldDocs_ConformsToContract(t *testing.T) {
	dir := t.TempDir()

	created, skipped, err := ScaffoldDocs(dir, false)
	if err != nil {
		t.Fatalf("ScaffoldDocs() error = %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("ScaffoldDocs() into an empty dir skipped %v, want none", skipped)
	}
	if len(created) != len(RequiredDocs()) {
		t.Errorf("ScaffoldDocs() created %d docs, want %d", len(created), len(RequiredDocs()))
	}

	roadmap, err := os.ReadFile(filepath.Join(dir, "ROADMAP.md"))
	if err != nil {
		t.Fatalf("reading scaffolded ROADMAP.md: %v", err)
	}
	sprints, err := os.ReadFile(filepath.Join(dir, "SPRINTS.md"))
	if err != nil {
		t.Fatalf("reading scaffolded SPRINTS.md: %v", err)
	}

	if got := LintHeadings("ROADMAP.md", string(roadmap)); len(got) != 0 {
		t.Errorf("LintHeadings(ROADMAP.md) = %v, want none", got)
	}
	if got := LintHeadings("SPRINTS.md", string(sprints)); len(got) != 0 {
		t.Errorf("LintHeadings(SPRINTS.md) = %v, want none", got)
	}

	milestones := ParseMilestones(string(roadmap))
	parsedSprints := ParseSprints(string(sprints))

	if got := ValidateDocs(milestones, parsedSprints); len(got) != 0 {
		t.Errorf("ValidateDocs() = %v, want none", got)
	}

	// Spot-check the structure the contract hinges on: one In-Progress milestone,
	// a current sprint linked to it.
	if len(milestones) != 1 {
		t.Fatalf("scaffolded ROADMAP.md parsed %d milestones, want 1", len(milestones))
	}
	if milestones[0].Status != StatusInProgress {
		t.Errorf("scaffolded milestone status = %q, want %q", milestones[0].Status, StatusInProgress)
	}
	if milestones[0].Description == "" {
		t.Error("scaffolded milestone has an empty description, want non-empty")
	}
	if len(parsedSprints) != 1 {
		t.Fatalf("scaffolded SPRINTS.md parsed %d sprints, want 1", len(parsedSprints))
	}
	if !parsedSprints[0].IsCurrent {
		t.Error("scaffolded sprint is not marked current")
	}
	if parsedSprints[0].Milestone != 1 {
		t.Errorf("scaffolded sprint links to milestone %d, want 1", parsedSprints[0].Milestone)
	}
	for _, item := range parsedSprints[0].Items {
		if item.Status != StatusPending {
			t.Errorf("scaffolded sprint item %d has status %q, want pending (empty)", item.Number, item.Status)
		}
	}
}

// TestScaffoldDocs_WritesAllRequired confirms every RequiredDocs() file lands on
// disk — the scaffolder and the canonical set do not drift.
func TestScaffoldDocs_WritesAllRequired(t *testing.T) {
	dir := t.TempDir()

	if _, _, err := ScaffoldDocs(dir, false); err != nil {
		t.Fatalf("ScaffoldDocs() error = %v", err)
	}

	for _, doc := range RequiredDocs() {
		if _, err := os.Stat(filepath.Join(dir, doc.Filename)); err != nil {
			t.Errorf("expected %s on disk: %v", doc.Filename, err)
		}
	}
}

// TestScaffoldDocs_SkipsExisting proves an existing file is never overwritten
// without force, and is reported as skipped rather than created.
func TestScaffoldDocs_SkipsExisting(t *testing.T) {
	dir := t.TempDir()
	const sentinel = "DO NOT OVERWRITE\n"
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(sentinel), 0o644); err != nil {
		t.Fatalf("seeding README.md: %v", err)
	}

	created, skipped, err := ScaffoldDocs(dir, false)
	if err != nil {
		t.Fatalf("ScaffoldDocs() error = %v", err)
	}

	if !containsDoc(skipped, "README.md") {
		t.Errorf("skipped = %v, want it to contain README.md", skipped)
	}
	if containsDoc(created, "README.md") {
		t.Errorf("created = %v, should not contain the pre-existing README.md", created)
	}
	if len(created) != len(RequiredDocs())-1 {
		t.Errorf("created %d docs, want %d", len(created), len(RequiredDocs())-1)
	}

	got, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}
	if string(got) != sentinel {
		t.Errorf("README.md was overwritten: got %q, want %q", string(got), sentinel)
	}
}

// TestScaffoldDocs_ForceOverwrites proves force rewrites an existing file from
// the template and counts it as created.
func TestScaffoldDocs_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("stale\n"), 0o644); err != nil {
		t.Fatalf("seeding README.md: %v", err)
	}

	created, skipped, err := ScaffoldDocs(dir, true)
	if err != nil {
		t.Fatalf("ScaffoldDocs(force) error = %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("ScaffoldDocs(force) skipped %v, want none", skipped)
	}
	if !containsDoc(created, "README.md") {
		t.Errorf("created = %v, want it to contain README.md under force", created)
	}

	got, err := os.ReadFile(readme)
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}
	if string(got) == "stale\n" {
		t.Error("README.md was not overwritten under force")
	}
}

// TestScaffoldDocs_CreatesDir confirms a missing target directory is created.
func TestScaffoldDocs_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "project")

	if _, _, err := ScaffoldDocs(dir, false); err != nil {
		t.Fatalf("ScaffoldDocs() into a missing dir error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ROADMAP.md")); err != nil {
		t.Errorf("expected ROADMAP.md in a freshly created dir: %v", err)
	}
}

// TestScaffoldDocs_EveryRequiredHasTemplate guards the single-source-of-truth
// invariant directly: no RequiredDocs() entry lacks a template.
func TestScaffoldDocs_EveryRequiredHasTemplate(t *testing.T) {
	for _, doc := range RequiredDocs() {
		if _, ok := docTemplates[doc.Filename]; !ok {
			t.Errorf("required document %q has no scaffold template", doc.Filename)
		}
	}
}

func containsDoc(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
