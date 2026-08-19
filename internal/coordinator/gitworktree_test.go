// Copyright (C) 2026 Techdelight BV

package coordinator

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/techdelight/daedalus/core"
)

// linkedWorktreeProject makes cfg.ProjectDir a REAL linked worktree of a real
// repository, which is what every control-plane Job launches against.
func linkedWorktreeProject(t *testing.T, cfg *core.Config) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := filepath.Join(cfg.DataDir, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", ".")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "init")
	run("worktree", "add", "-q", "-B", "job", cfg.ProjectDir, "HEAD")
	return repo
}

// TestStart_LinkedWorktree_MountsTheRepositoryReadOnly is the regression test for
// the defect a real Job reported: only the worktree's files were mounted, so
// /workspace/.git named a host path the container could not see and every git
// command was fatal. The agent concluded it could not work, wrote nothing, and
// the Job was rejected on the null-agent floor.
func TestStart_LinkedWorktree_MountsTheRepositoryReadOnly(t *testing.T) {
	cfg := configFor(t, "daedalus-job-J-12")
	repo := linkedWorktreeProject(t, cfg)

	args := capturedRunArgs(t, cfg)
	joined := strings.Join(args, " ")

	common := filepath.Join(repo, ".git")
	wantCommon := common + ":" + core.ContainerGitCommon + ":ro"
	if !strings.Contains(joined, wantCommon) {
		t.Errorf("missing the repository mount %q; args = %v", wantCommon, args)
	}
	wantPointer := cfg.WorktreeGitFilePath() + ":/workspace/.git:ro"
	if !strings.Contains(joined, wantPointer) {
		t.Errorf("missing the .git pointer mount %q; args = %v", wantPointer, args)
	}
	// Read-only is the posture, not an implementation detail: this is the
	// developer's real object store and refs, handed to a headless agent.
	if strings.Contains(joined, common+":"+core.ContainerGitCommon+" ") {
		t.Errorf("the repository was mounted WRITABLE; args = %v", args)
	}

	// The pointer file must exist, name the container path, and carry no host
	// path — a pointer holding the host path is the whole defect.
	raw, err := os.ReadFile(cfg.WorktreeGitFilePath())
	if err != nil {
		t.Fatalf("the pointer file was not written: %v", err)
	}
	got := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(got, "gitdir: "+core.ContainerGitCommon+"/worktrees/") {
		t.Errorf("pointer = %q, want it to name a path under %s", got, core.ContainerGitCommon)
	}
	if strings.Contains(got, repo) {
		t.Errorf("pointer = %q leaks the host path", got)
	}

	// And it is NOT inside the worktree — anything there is captured by the
	// plane's `git add -A` and shipped as part of the Job's artifact.
	if rel, err := filepath.Rel(cfg.ProjectDir, cfg.WorktreeGitFilePath()); err == nil &&
		!strings.HasPrefix(rel, "..") {
		t.Errorf("the pointer file is inside the worktree; the capture would commit it")
	}
}

// TestStart_OrdinaryCheckout_GetsNoGitMounts: a normal project's .git is a
// directory already inside the /workspace mount, so mounting anything extra
// would be both pointless and a second, shadowing copy of its repository.
func TestStart_OrdinaryCheckout_GetsNoGitMounts(t *testing.T) {
	cfg := configFor(t, "my-app")
	if err := os.MkdirAll(filepath.Join(cfg.ProjectDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	joined := strings.Join(capturedRunArgs(t, cfg), " ")
	if strings.Contains(joined, core.ContainerGitCommon) {
		t.Errorf("an ordinary checkout got a %s mount: %s", core.ContainerGitCommon, joined)
	}
	if strings.Contains(joined, ":/workspace/.git") {
		t.Errorf("an ordinary checkout had its .git shadowed: %s", joined)
	}
	if _, err := os.Stat(cfg.WorktreeGitFilePath()); err == nil {
		t.Error("a pointer file was written for a project that does not need one")
	}
}
