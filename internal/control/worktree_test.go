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

// AND IT MUST STILL WORK WHEN THE PROJECT GITIGNORES `.daedalus`.
//
// The fix above shipped as `git add -A -- :(exclude).daedalus/activity.json`,
// and that shape is refused outright by git when the named path sits inside a
// directory .gitignore ignores:
//
//	The following paths are ignored by one of your .gitignore files:
//	.daedalus
//
// exit 1, nothing staged, Capture returns ("", err) — so a Job in such a
// project could not be captured AT ALL. Its work was done and then discarded.
// Ignoring `.daedalus` is the sensible thing to do with it and is what daedalus
// does in its own repository, so the fix for #94 broke exactly the projects it
// was written for, and the test for #94 did not notice because its fixture had
// no .gitignore.
//
// The one thing this asserts that the test above cannot: that Capture SUCCEEDS.
func TestCapture_WorksWhenTheProjectIgnoresTheDaedalusDirectory(t *testing.T) {
	repo := gitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".daedalus\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", ".gitignore")
	git(t, repo, "commit", "-m", "ignore the plane's scratch")

	m := &WorktreeManager{root: t.TempDir()}
	wt, err := m.Add(repo, "T-1", "J-1", "HEAD")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	defer runGit(repo, "worktree", "remove", "--force", wt)

	if err := os.MkdirAll(filepath.Join(wt, ".daedalus"), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, body := range map[string]string{
		"feature.go":              "package main\n",
		".daedalus/activity.json": `{"state":"busy"}`,
	} {
		if err := os.WriteFile(filepath.Join(wt, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	head, err := m.Capture(wt)
	if err != nil {
		t.Fatalf("Capture failed in a project that ignores .daedalus — the agent's work is "+
			"discarded and the Job produces an artifact naming no commit: %v", err)
	}
	if head == "" {
		t.Fatal("Capture returned no commit")
	}
	files, err := runGit(repo, "show", "--name-only", "--format=", head)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(files, "feature.go") {
		t.Errorf("the agent's work must be captured:\n%s", files)
	}
	if strings.Contains(files, "activity.json") {
		t.Errorf("the harness's own liveness file reached the artifact:\n%s", files)
	}
}

// A scratch file ALREADY TRACKED from an earlier Job keeps whatever it was.
//
// The rule the exclusion was chosen for and which the unstage has to preserve:
// leave what is tracked exactly as it is and simply stop adding more. Deleting
// it would stage a removal the agent never made.
func TestCapture_LeavesAnAlreadyTrackedScratchFileAlone(t *testing.T) {
	repo := gitRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, ".daedalus"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".daedalus", "activity.json"),
		[]byte(`{"state":"OLD"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", ".daedalus/activity.json")
	git(t, repo, "commit", "-m", "a previous job committed it")

	m := &WorktreeManager{root: t.TempDir()}
	wt, err := m.Add(repo, "T-1", "J-1", "HEAD")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	defer runGit(repo, "worktree", "remove", "--force", wt)

	if err := os.WriteFile(filepath.Join(wt, ".daedalus", "activity.json"),
		[]byte(`{"state":"NEW"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "feature.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	head, err := m.Capture(wt)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	kept, err := runGit(repo, "show", head+":.daedalus/activity.json")
	if err != nil {
		t.Fatalf("the tracked file was removed from the artifact: %v", err)
	}
	if !strings.Contains(kept, "OLD") {
		t.Errorf("the artifact carries the harness's churn: %q", strings.TrimSpace(kept))
	}
}

// A JOB THAT CHANGED NOTHING STILL CAPTURES.
//
// The harness writes .daedalus/activity.json on every tool use, and Capture
// unstages it on purpose — so a Job that edited no files leaves a tree that
// `git status` calls dirty and an index that is empty. Deciding to commit from
// the STATUS then ran `git commit` with nothing staged, git exited 1, and
// Capture returned ("", err): the same empty artifact that started this whole
// chain, reached by a different door.
//
// A Job that changes nothing is ordinary — it answers a question, it refuses,
// it finds the work already done — and its snapshot is simply the base.
func TestCapture_AJobThatChangedNothingSnapshotsTheBase(t *testing.T) {
	repo := gitRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, ".daedalus"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".daedalus", "activity.json"),
		[]byte(`{"state":"idle"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", ".daedalus/activity.json")
	git(t, repo, "commit", "-m", "tracked scratch")
	base, err := runGit(repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	base = strings.TrimSpace(base)

	m := &WorktreeManager{root: t.TempDir()}
	wt, err := m.Add(repo, "T-1", "J-1", "HEAD")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	defer runGit(repo, "worktree", "remove", "--force", wt)

	// The agent edits nothing. Only the harness moves, as it does on every run.
	if err := os.WriteFile(filepath.Join(wt, ".daedalus", "activity.json"),
		[]byte(`{"state":"busy","detail":"tool_use"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	head, err := m.Capture(wt)
	if err != nil {
		t.Fatalf("Capture failed for a Job that simply changed nothing: %v", err)
	}
	if head != base {
		t.Errorf("snapshot = %s, want the base %s — nothing was changed, so nothing should be "+
			"committed", shortSHA(head), shortSHA(base))
	}
}
