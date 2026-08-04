// Copyright (C) 2026 Techdelight BV

package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// initGitDir writes a minimal .git directory whose HEAD holds head.
func initGitDir(t *testing.T, projectDir, head string) {
	t.Helper()
	gitDir := filepath.Join(projectDir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeHEAD(t, projectDir, head)
}

func writeHEAD(t *testing.T, projectDir, head string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(projectDir, ".git", "HEAD"), []byte(head), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestGitBranch_OnBranch(t *testing.T) {
	dir := t.TempDir()
	initGitDir(t, dir, "ref: refs/heads/development\n")

	if got := gitBranch(dir); got != "development" {
		t.Errorf("gitBranch = %q, want %q", got, "development")
	}
}

// Not a git repo is absence, not an error.
func TestGitBranch_NotARepo(t *testing.T) {
	if got := gitBranch(t.TempDir()); got != "" {
		t.Errorf("gitBranch = %q, want empty", got)
	}
}

func TestGitBranch_DetachedHead(t *testing.T) {
	dir := t.TempDir()
	initGitDir(t, dir, "9fceb02d0ae598e95dc970b74767f19372d61af8\n")

	if got := gitBranch(dir); got != "" {
		t.Errorf("gitBranch = %q, want empty for detached HEAD", got)
	}
}

// A linked worktree's .git is a file pointing at the real git dir. Without
// following it, every worktree reports "no branch".
func TestGitBranch_WorktreeGitFile(t *testing.T) {
	dir := t.TempDir()
	realGitDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(realGitDir, "HEAD"),
		[]byte("ref: refs/heads/feat/runner-default-flip\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git"),
		[]byte("gitdir: "+realGitDir+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if got := gitBranch(dir); got != "feat/runner-default-flip" {
		t.Errorf("gitBranch = %q, want %q", got, "feat/runner-default-flip")
	}
}

// The gitdir pointer may be relative to the project directory.
func TestGitBranch_WorktreeRelativeGitdir(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "wt")
	realGitDir := filepath.Join(base, "realgit")
	for _, d := range []string{dir, realGitDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(realGitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: ../realgit\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if got := gitBranch(dir); got != "main" {
		t.Errorf("gitBranch = %q, want %q", got, "main")
	}
}

// A .git file that isn't a gitdir pointer must not blow up.
func TestGitBranch_GarbageGitFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("nonsense\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if got := gitBranch(dir); got != "" {
		t.Errorf("gitBranch = %q, want empty", got)
	}
}

// A .git dir with no HEAD (mid-clone, say) reads as absent.
func TestGitBranch_MissingHead(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	if got := gitBranch(dir); got != "" {
		t.Errorf("gitBranch = %q, want empty", got)
	}
}

// startBranchServer serves a WebSocket that runs watchBranch over projectDir
// for the lifetime of the connection.
func startBranchServer(t *testing.T, projectDir string, interval time.Duration) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer raw.Close()
		conn := newSafeConn(raw)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go watchBranch(ctx, conn, projectDir, interval)

		// Hold the connection open until the client goes away, so the
		// watcher's lifetime tracks the session as it does in the handlers.
		for {
			if _, _, err := raw.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func readBranchMsg(t *testing.T, c *websocket.Conn) branchMsg {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m branchMsg
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
	if m.Type != "branch" {
		t.Fatalf("Type = %q, want %q", m.Type, "branch")
	}
	return m
}

func dialBranchWS(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// The header must populate on attach without the client asking.
func TestWatchBranch_PushesInitialBranchOnAttach(t *testing.T) {
	dir := t.TempDir()
	initGitDir(t, dir, "ref: refs/heads/development\n")

	c := dialBranchWS(t, startBranchServer(t, dir, time.Hour))

	if m := readBranchMsg(t, c); m.Branch != "development" {
		t.Errorf("Branch = %q, want %q", m.Branch, "development")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=", "GIT_CONFIG_SYSTEM=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// The load-bearing test for the fsnotify design. Git never rewrites HEAD in
// place — it writes HEAD.lock and renames it over the top — so a watch on the
// HEAD file itself would go deaf at exactly this moment. Driving a real
// checkout (rather than an os.WriteFile standing in for one) is the only way
// to prove the directory watch survives git's actual behaviour.
func TestWatchBranch_RealGitCheckoutIsPushed(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-qm", "initial")

	// A long fallback interval: if this passes, it passed via the watch.
	c := dialBranchWS(t, startBranchServer(t, dir, time.Hour))

	if m := readBranchMsg(t, c); m.Branch != "main" {
		t.Fatalf("initial Branch = %q, want %q", m.Branch, "main")
	}

	runGit(t, dir, "checkout", "-q", "-b", "feat/sidebar")

	if m := readBranchMsg(t, c); m.Branch != "feat/sidebar" {
		t.Errorf("Branch after real checkout = %q, want %q", m.Branch, "feat/sidebar")
	}
}

// A real detach, again through git rather than a hand-written HEAD.
func TestWatchBranch_RealGitDetachIsPushed(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-qm", "initial")

	c := dialBranchWS(t, startBranchServer(t, dir, time.Hour))

	if m := readBranchMsg(t, c); m.Branch != "main" {
		t.Fatalf("initial Branch = %q, want %q", m.Branch, "main")
	}

	runGit(t, dir, "checkout", "-q", "--detach", "HEAD")

	if m := readBranchMsg(t, c); m.Branch != "" {
		t.Errorf("Branch after detach = %q, want empty", m.Branch)
	}
}

// A project that is not a repo cannot be watched, so the watcher degrades to
// polling — which also means `git init` mid-session is picked up rather than
// leaving the header blank until reattach.
func TestWatchBranch_FallbackPollingPicksUpNewRepo(t *testing.T) {
	dir := t.TempDir()

	c := dialBranchWS(t, startBranchServer(t, dir, 20*time.Millisecond))

	if m := readBranchMsg(t, c); m.Branch != "" {
		t.Fatalf("initial Branch = %q, want empty for a non-repo", m.Branch)
	}

	initGitDir(t, dir, "ref: refs/heads/main\n")

	if m := readBranchMsg(t, c); m.Branch != "main" {
		t.Errorf("Branch after git init = %q, want %q", m.Branch, "main")
	}
}

// The point of the whole exercise: a checkout during the session reaches the
// browser unprompted.
func TestWatchBranch_PushesOnChange(t *testing.T) {
	dir := t.TempDir()
	initGitDir(t, dir, "ref: refs/heads/development\n")

	c := dialBranchWS(t, startBranchServer(t, dir, 20*time.Millisecond))

	if m := readBranchMsg(t, c); m.Branch != "development" {
		t.Fatalf("initial Branch = %q, want %q", m.Branch, "development")
	}

	writeHEAD(t, dir, "ref: refs/heads/feat/sidebar\n")

	if m := readBranchMsg(t, c); m.Branch != "feat/sidebar" {
		t.Errorf("Branch after checkout = %q, want %q", m.Branch, "feat/sidebar")
	}
}

// Checking out a detached HEAD pushes an empty branch so the client knows to
// hide the pill. Suppressing it would strand a stale name in the header.
func TestWatchBranch_PushesEmptyOnDetach(t *testing.T) {
	dir := t.TempDir()
	initGitDir(t, dir, "ref: refs/heads/development\n")

	c := dialBranchWS(t, startBranchServer(t, dir, 20*time.Millisecond))

	if m := readBranchMsg(t, c); m.Branch != "development" {
		t.Fatalf("initial Branch = %q, want %q", m.Branch, "development")
	}

	writeHEAD(t, dir, "9fceb02d0ae598e95dc970b74767f19372d61af8\n")

	if m := readBranchMsg(t, c); m.Branch != "" {
		t.Errorf("Branch after detach = %q, want empty", m.Branch)
	}
}

// Only changes are pushed: an unchanged branch must not stream messages at
// the poll interval.
func TestWatchBranch_SilentWhileUnchanged(t *testing.T) {
	dir := t.TempDir()
	initGitDir(t, dir, "ref: refs/heads/development\n")

	c := dialBranchWS(t, startBranchServer(t, dir, 10*time.Millisecond))

	if m := readBranchMsg(t, c); m.Branch != "development" {
		t.Fatalf("initial Branch = %q, want %q", m.Branch, "development")
	}

	// Several poll intervals pass with no checkout: nothing more should come.
	c.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	_, data, err := c.ReadMessage()
	if err == nil {
		t.Errorf("unexpected extra message while branch unchanged: %s", data)
	}
}

// A project that is not a repo still gets one message, so the client can hide
// the pill rather than wait forever.
func TestWatchBranch_NonRepoPushesEmpty(t *testing.T) {
	c := dialBranchWS(t, startBranchServer(t, t.TempDir(), time.Hour))

	if m := readBranchMsg(t, c); m.Branch != "" {
		t.Errorf("Branch = %q, want empty", m.Branch)
	}
}
