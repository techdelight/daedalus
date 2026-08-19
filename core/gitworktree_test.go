// Copyright (C) 2026 Techdelight BV

package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// makeLinkedWorktree builds a real repository with a real linked worktree and
// returns (repoDir, worktreeDir).
//
// It shells out to git rather than hand-writing the pointer files on purpose:
// the whole subject of this file is the layout git actually produces, and a
// fixture that wrote its own would prove agreement with itself.
func makeLinkedWorktree(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	wt := filepath.Join(root, "wt")

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run(repo, "init", "-q", ".")
	run(repo, "config", "user.email", "t@example.com")
	run(repo, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(repo, "add", "-A")
	run(repo, "commit", "-qm", "init")
	run(repo, "worktree", "add", "-q", "-B", "job", wt, "HEAD")
	return repo, wt
}

// TestReadLinkedWorktree_ResolvesBothDirectories is the fix's foundation: the
// pointer file names the admin dir, and `commondir` inside it names the shared
// repository — relatively, which is what lets the common dir be mounted anywhere.
func TestReadLinkedWorktree_ResolvesBothDirectories(t *testing.T) {
	repo, wt := makeLinkedWorktree(t)

	got, ok := ReadLinkedWorktree(wt)
	if !ok {
		t.Fatal("ReadLinkedWorktree said a real linked worktree was not one")
	}
	wantCommon, _ := filepath.EvalSymlinks(filepath.Join(repo, ".git"))
	gotCommon, _ := filepath.EvalSymlinks(got.CommonDir)
	if gotCommon != wantCommon {
		t.Errorf("CommonDir = %s, want %s", gotCommon, wantCommon)
	}
	if filepath.Base(filepath.Dir(got.AdminDir)) != "worktrees" {
		t.Errorf("AdminDir = %s, want …/.git/worktrees/<name>", got.AdminDir)
	}

	// The container-side pointer must name the RELOCATED admin dir, never the
	// host's — a pointer carrying the host path is the whole defect.
	if !strings.HasPrefix(got.ContainerAdminDir(), ContainerGitCommon+"/worktrees/") {
		t.Errorf("ContainerAdminDir = %s, want it under %s", got.ContainerAdminDir(), ContainerGitCommon)
	}
	if strings.Contains(got.Pointer(), repo) {
		t.Errorf("the container's .git leaks the host path: %q", got.Pointer())
	}
	if !strings.HasPrefix(got.Pointer(), "gitdir: ") || !strings.HasSuffix(got.Pointer(), "\n") {
		t.Errorf("Pointer() = %q, want a `gitdir: <path>` line", got.Pointer())
	}
}

// TestReadLinkedWorktree_OrdinaryCheckoutIsNotOne: false must mean "add no
// mounts". A normal project's `.git` is a directory and is already inside the
// /workspace mount, so mounting anything extra for it would be wrong.
func TestReadLinkedWorktree_OrdinaryCheckoutIsNotOne(t *testing.T) {
	repo, _ := makeLinkedWorktree(t)
	if _, ok := ReadLinkedWorktree(repo); ok {
		t.Error("an ordinary checkout was read as a linked worktree")
	}
	if _, ok := ReadLinkedWorktree(t.TempDir()); ok {
		t.Error("a directory with no repository at all was read as a linked worktree")
	}
}

// TestReadLinkedWorktree_RefusesAPointerItCannotResolve: a `.git` file naming a
// directory that is not a worktree admin dir must not produce mounts. Fail
// closed — a bogus mount would be a bind mount of an arbitrary path into the
// container.
func TestReadLinkedWorktree_RefusesAPointerItCannotResolve(t *testing.T) {
	dir := t.TempDir()
	for _, content := range []string{
		"gitdir: " + filepath.Join(dir, "nowhere") + "\n", // resolves to nothing
		"gitdir:\n",              // empty target
		"ref: refs/heads/main\n", // not a gitdir pointer at all
		"",                       // empty file
	} {
		if err := os.WriteFile(filepath.Join(dir, ".git"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, ok := ReadLinkedWorktree(dir); ok {
			t.Errorf("%q was accepted as a linked worktree", content)
		}
	}
}

// TestWorktreeGitMounts_AreReadOnlyAndShadowThePointer pins the security
// posture, not just the shape. The mount is of the developer's real object store
// and refs; read-only is what makes it safe to hand to a headless agent working
// on an objective the plane treats as untrusted.
func TestWorktreeGitMounts_AreReadOnlyAndShadowThePointer(t *testing.T) {
	_, wt := makeLinkedWorktree(t)
	w, ok := ReadLinkedWorktree(wt)
	if !ok {
		t.Fatal("fixture is not a linked worktree")
	}
	args := WorktreeGitMounts(w, "/data/gitfiles/proj")

	var mounts []string
	for i := 0; i+1 < len(args); i += 2 {
		if args[i] != "-v" {
			t.Fatalf("arg %d = %q, want -v", i, args[i])
		}
		mounts = append(mounts, args[i+1])
	}
	if len(mounts) != 2 {
		t.Fatalf("%d mounts, want 2: %v", len(mounts), mounts)
	}
	common := w.CommonDir + ":" + ContainerGitCommon + ":ro"
	if mounts[0] != common {
		t.Errorf("mounts[0] = %q, want %q", mounts[0], common)
	}
	if mounts[1] != "/data/gitfiles/proj:/workspace/.git:ro" {
		t.Errorf("mounts[1] = %q, want the pointer shadowing /workspace/.git read-only", mounts[1])
	}
	// Every mount of the repository must be read-only. An agent that could write
	// here could move refs, add objects, and push — into the developer's own
	// checkout's repository.
	for _, m := range mounts {
		if !strings.HasSuffix(m, ":ro") {
			t.Errorf("mount %q is writable; the repository must be read-only", m)
		}
	}
	// And the admin dir is NOT mounted separately. It was writable in an earlier
	// draft, on the theory that the index needed refreshing; it does not, and a
	// writable admin dir would let the agent move its worktree's HEAD to a branch
	// other than the one the plane records on the artifact.
	for _, m := range mounts {
		if strings.HasPrefix(m, w.AdminDir+":") {
			t.Errorf("the admin dir is mounted on its own (%q); read-only common covers it", m)
		}
	}
}

// TestWorktreeGitFilePath_IsOutsideTheWorktree is a correctness requirement, not
// tidiness: everything inside the worktree is part of the tree the control plane
// captures as the Job's artifact, so a pointer file written there would be staged
// by `git add -A` and shipped as work the agent never did.
func TestWorktreeGitFilePath_IsOutsideTheWorktree(t *testing.T) {
	_, wt := makeLinkedWorktree(t)
	cfg := &Config{DataDir: t.TempDir(), ProjectName: "daedalus-job-J-12", ProjectDir: wt}
	pointer := cfg.WorktreeGitFilePath()

	rel, err := filepath.Rel(wt, pointer)
	if err == nil && !strings.HasPrefix(rel, "..") {
		t.Errorf("the pointer file %s is inside the worktree %s — the capture would commit it", pointer, wt)
	}
	if !strings.HasPrefix(pointer, cfg.DataDir) {
		t.Errorf("pointer = %s, want it under the data dir %s", pointer, cfg.DataDir)
	}
}
