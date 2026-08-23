// Copyright (C) 2026 Techdelight BV

package control

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCapture_ExcludesTheHarnessesOwnScratch (RV-18, on another project).
//
// daedalus's hooks write .daedalus/activity.json into /workspace on every tool
// use, and for a Job /workspace IS the worktree that becomes the artifact — so
// `git add -A` committed the plane's own liveness state as the agent's work, in
// every project it has ever run a Job in. A reviewer found it in a change whose
// stated constraint was "documentation and planning only".
//
// daedalus itself never noticed: its own .gitignore excludes `.daedalus/*` for
// an unrelated reason, which is the worst way to be immune to your own bug.
func TestCapture_ExcludesTheHarnessesOwnScratch(t *testing.T) {
	repo := gitRepo(t)
	m := &WorktreeManager{root: t.TempDir()}
	wt, err := m.Add(repo, "T-1", "J-1", "HEAD")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	defer runGit(repo, "worktree", "remove", "--force", wt)

	// The agent's work, the plane's scratch, and the project's own policy — which
	// lives in the same directory and MUST be committed.
	if err := os.MkdirAll(filepath.Join(wt, ".daedalus"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, body string) {
		if err := os.WriteFile(filepath.Join(wt, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("feature.go", "package main\n")
	write(".daedalus/activity.json", `{"state":"idle","detail":"stop"}`)
	write(".daedalus/verify.json", `{"checks":["go build ./..."]}`)

	head, err := m.Capture(wt)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	files, err := runGit(repo, "show", "--name-only", "--format=", head)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(files, "feature.go") {
		t.Errorf("the agent's work must be captured:\n%s", files)
	}
	// The project's own acceptance policy lives in the same directory and is NOT
	// scratch — excluding `.daedalus/` wholesale would drop it.
	if !strings.Contains(files, ".daedalus/verify.json") {
		t.Errorf("verify.json is project content and must be captured:\n%s", files)
	}
	if strings.Contains(files, "activity.json") {
		t.Errorf("the harness's own liveness file reached the artifact:\n%s", files)
	}
}
