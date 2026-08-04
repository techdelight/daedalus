// Copyright (C) 2026 Techdelight BV

package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/techdelight/daedalus/core"

	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/websocket"
)

// branchFallbackInterval is how often an attached session re-reads HEAD when
// no filesystem watch is available — see watchBranch.
const branchFallbackInterval = 5 * time.Second

// branchMsg is pushed to the browser whenever the project's branch changes.
type branchMsg struct {
	Type   string `json:"type"`
	Branch string `json:"branch"`
}

// watchBranch pushes the project's git branch over the session WebSocket:
// once on attach, then again only when it changes.
//
// The branch is host state, not session state, so the browser holds no timer
// and issues no requests — it renders what it is told. An agent checking out
// a branch mid-session is the normal case, so a value read once at attach
// would go stale and quietly mislead.
//
// Changes arrive from a filesystem watch, so a checkout reaches the header
// immediately instead of up to an interval later. Where no watch can be
// established — the project is not a repo yet, inotify is exhausted, the
// filesystem does not support it — it degrades to polling at
// fallbackInterval rather than leaving the header frozen. That fallback also
// covers `git init` part-way through a session: the poll picks up the repo
// that did not exist to be watched at attach.
//
// Returns when ctx is cancelled (the session detaching) or the socket fails.
func watchBranch(ctx context.Context, conn *safeConn, projectDir string, fallbackInterval time.Duration) {
	last := gitBranch(projectDir)
	if err := sendBranch(conn, last); err != nil {
		return
	}

	// pushIfChanged reports whether the watcher should keep running: false
	// once the socket is gone.
	pushIfChanged := func() bool {
		current := gitBranch(projectDir)
		if current == last {
			return true
		}
		last = current
		return sendBranch(conn, current) == nil
	}

	changed, stop, err := watchGitHEAD(projectDir)
	if err != nil {
		pollBranch(ctx, pushIfChanged, fallbackInterval)
		return
	}
	defer stop()

	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-changed:
			if !ok {
				return
			}
			if !pushIfChanged() {
				return
			}
		}
	}
}

// watchGitHEAD reports HEAD changes for the repo at projectDir on the
// returned channel, and hands back a stop function.
//
// The watch is on the git *directory*, never on HEAD itself. Git does not
// edit HEAD in place: it writes HEAD.lock and renames it over the top. A
// watch on the file tracks the old inode and goes deaf after the first
// checkout — precisely the event this exists to catch. Watching the
// directory and filtering by name survives the rename.
//
// For a linked worktree or submodule the directory is the one .git points
// at, not <project>/.git, which is a file there (see resolveGitDir).
func watchGitHEAD(projectDir string) (<-chan struct{}, func(), error) {
	gitDir, ok := resolveGitDir(projectDir)
	if !ok {
		return nil, nil, fmt.Errorf("no git directory for %q", projectDir)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("branch watch: fsnotify unavailable for %q, falling back to polling: %v", projectDir, err)
		return nil, nil, err
	}
	if err := watcher.Add(gitDir); err != nil {
		log.Printf("branch watch: cannot watch %q, falling back to polling: %v", gitDir, err)
		watcher.Close()
		return nil, nil, err
	}

	// Buffered by one and sent to non-blockingly: a single checkout fires
	// several events (HEAD.lock created, renamed over HEAD), and they only
	// need to collapse into "re-read HEAD once".
	changed := make(chan struct{}, 1)
	go func() {
		defer close(changed)
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if filepath.Base(event.Name) != "HEAD" {
					continue
				}
				select {
				case changed <- struct{}{}:
				default:
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("branch watch error for %q: %v", projectDir, err)
			}
		}
	}()

	return changed, func() { watcher.Close() }, nil
}

// pollBranch is the fallback when no filesystem watch is available.
func pollBranch(ctx context.Context, pushIfChanged func() bool, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !pushIfChanged() {
				return
			}
		}
	}
}

// sendBranch writes a branch message. An empty branch is sent as such — it
// is how the client learns to hide the pill after a checkout to a detached
// HEAD, so it must not be suppressed.
func sendBranch(conn *safeConn, branch string) error {
	data, err := json.Marshal(branchMsg{Type: "branch", Branch: branch})
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, data)
}

// gitBranch resolves the current branch of the repo at projectDir, or ""
// when there isn't one — not a repo, detached HEAD, or an unreadable ref.
// Every failure is absence, never an error: the branch is a decoration in
// the header and must not be able to break a session.
//
// HEAD is read straight off disk rather than shelling out to git: no
// subprocess, and no dependency on git being installed on the host.
func gitBranch(projectDir string) string {
	gitDir, ok := resolveGitDir(projectDir)
	if !ok {
		return ""
	}
	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	return core.ParseGitHEAD(string(head))
}

// resolveGitDir finds the git directory for projectDir.
//
// Usually that is plain <dir>/.git. But in a linked worktree or a submodule,
// .git is a *file* holding "gitdir: <path>" pointing at the real directory —
// so a naive <dir>/.git/HEAD read reports "no branch" for exactly the
// worktree layouts an agent is most likely to be working in. The pointer may
// be relative, in which case it resolves against the project directory.
func resolveGitDir(projectDir string) (string, bool) {
	gitPath := filepath.Join(projectDir, ".git")

	info, err := os.Stat(gitPath)
	if err != nil {
		return "", false
	}
	if info.IsDir() {
		return gitPath, true
	}

	data, err := os.ReadFile(gitPath)
	if err != nil {
		return "", false
	}
	target, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return "", false
	}
	gitDir := strings.TrimSpace(target)
	if gitDir == "" {
		return "", false
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(projectDir, gitDir)
	}
	return gitDir, true
}
