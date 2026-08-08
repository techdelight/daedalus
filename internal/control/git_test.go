// Copyright (C) 2026 Techdelight BV

package control

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initGitRepo creates a temp git repo with one commit and returns its dir and
// the HEAD sha. Skips the test if git is unavailable.
func initGitRepo(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")

	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	sha := string(out)
	return dir, sha[:len(sha)-1] // strip trailing newline
}

func TestReadHeadSHA_RealRepo(t *testing.T) {
	dir, want := initGitRepo(t)
	got, err := ReadHeadSHA(dir)
	if err != nil {
		t.Fatalf("ReadHeadSHA: %v", err)
	}
	if got != want {
		t.Errorf("ReadHeadSHA = %q, want %q", got, want)
	}
}

func TestReadHeadSHA_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadHeadSHA(dir)
	var notGit *ErrNotGitRepo
	if !errors.As(err, &notGit) {
		t.Errorf("ReadHeadSHA(non-git) err = %v, want *ErrNotGitRepo", err)
	}
}

func TestReadHeadSHA_UnbornBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	// No commit yet: HEAD points at an unborn branch.
	_, err := ReadHeadSHA(dir)
	if err == nil {
		t.Error("ReadHeadSHA(unborn) = nil, want error")
	}
	// It must NOT be reported as "not a git repo" — the repo exists.
	var notGit *ErrNotGitRepo
	if errors.As(err, &notGit) {
		t.Error("unborn branch wrongly reported as not-a-git-repo")
	}
}

func TestReadHeadSHA_DetachedHead(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sha := "0123456789abcdef0123456789abcdef01234567"
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte(sha+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadHeadSHA(dir)
	if err != nil {
		t.Fatalf("ReadHeadSHA(detached): %v", err)
	}
	if got != sha {
		t.Errorf("ReadHeadSHA = %q, want %q", got, sha)
	}
}

func TestReadHeadSHA_PackedRef(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sha := "89abcdef0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// No loose ref; only packed-refs holds it.
	packed := "# pack-refs with: peeled fully-peeled sorted\n" + sha + " refs/heads/main\n"
	if err := os.WriteFile(filepath.Join(gitDir, "packed-refs"), []byte(packed), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadHeadSHA(dir)
	if err != nil {
		t.Fatalf("ReadHeadSHA(packed): %v", err)
	}
	if got != sha {
		t.Errorf("ReadHeadSHA = %q, want %q", got, sha)
	}
}
