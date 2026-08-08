// Copyright (C) 2026 Techdelight BV

package control

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotGitRepo is returned when a project directory has no .git — orchestration
// is Git-native (docs/guild-master-plan.md §5), so a non-Git project cannot be a
// control-plane task target.
type ErrNotGitRepo struct{ Dir string }

func (e *ErrNotGitRepo) Error() string {
	return fmt.Sprintf("project directory %q is not a Git repository (no .git); "+
		"the control plane is Git-native — initialise it with `git init` and commit", e.Dir)
}

// ReadHeadSHA resolves the current HEAD commit of the Git repository at dir,
// reading the plumbing directly rather than shelling out (Sprint 54 does not
// touch Docker/containers and must stay dependency-free). It handles:
//   - a normal .git directory,
//   - a .git *file* pointing elsewhere via "gitdir: <path>" (worktrees/submodules),
//   - a symbolic HEAD ("ref: refs/heads/<b>") resolved via loose or packed refs,
//   - a detached HEAD (a raw 40/64-hex object id).
//
// It returns *ErrNotGitRepo if there is no .git at all, and a plain error for an
// unborn branch (a repo with no commits yet) — a task needs a real base_sha.
func ReadHeadSHA(dir string) (string, error) {
	gitDir, err := resolveGitDir(dir)
	if err != nil {
		return "", err
	}

	headBytes, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "", fmt.Errorf("reading HEAD: %w", err)
	}
	head := strings.TrimSpace(string(headBytes))

	// Detached HEAD: the file holds the object id directly.
	if !strings.HasPrefix(head, "ref:") {
		if isHexSHA(head) {
			return head, nil
		}
		return "", fmt.Errorf("unrecognised HEAD contents %q in %s", head, gitDir)
	}

	ref := strings.TrimSpace(strings.TrimPrefix(head, "ref:"))

	// Loose ref: .git/<ref> (e.g. .git/refs/heads/main).
	if sha, ok := readLooseRef(gitDir, ref); ok {
		return sha, nil
	}
	// Packed ref fallback.
	if sha, ok := readPackedRef(gitDir, ref); ok {
		return sha, nil
	}
	return "", fmt.Errorf("repository at %q has no commit on %s yet (unborn branch); "+
		"make an initial commit before creating a task", dir, ref)
}

// TargetTipSHA returns the commit an artifact would ultimately land on: the
// project checkout's current HEAD.
//
// V1 assumption, stated plainly: the *target branch is whatever the developer's
// project checkout has checked out*. The control plane has no separate notion of
// a target ref yet (that arrives with the M15 integration transaction), and a
// Job's own worktree is a detached side branch under the data dir, so it never
// moves this tip.
func TargetTipSHA(repoDir string) (string, error) { return ReadHeadSHA(repoDir) }

// IsStaleBase reports whether baseSHA is no longer the project's target tip —
// §6's "artifact built from a stale base → REJECTED, must rebase + re-verify".
// A candidate verified against a base the project has since moved past proves
// something about a tree nobody will ever integrate, so the plane refuses to
// call it verified.
//
// Returns (stale, currentTip, err). A repo whose tip cannot be read is an error,
// never a silent "not stale": failing open here would quietly retire the check.
func IsStaleBase(repoDir, baseSHA string) (bool, string, error) {
	tip, err := TargetTipSHA(repoDir)
	if err != nil {
		return false, "", err
	}
	return baseSHA != "" && tip != baseSHA, tip, nil
}

// resolveGitDir returns the real git directory for a project dir, following a
// ".git file" gitdir pointer if present. Returns *ErrNotGitRepo when absent.
func resolveGitDir(dir string) (string, error) {
	dotGit := filepath.Join(dir, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return "", &ErrNotGitRepo{Dir: dir}
	}
	if info.IsDir() {
		return dotGit, nil
	}
	// .git is a file: "gitdir: <path>" (worktree/submodule linkage).
	data, err := os.ReadFile(dotGit)
	if err != nil {
		return "", &ErrNotGitRepo{Dir: dir}
	}
	line := strings.TrimSpace(string(data))
	const prefix = "gitdir:"
	if !strings.HasPrefix(line, prefix) {
		return "", &ErrNotGitRepo{Dir: dir}
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(dir, gitDir)
	}
	return gitDir, nil
}

// readLooseRef reads .git/<ref>, returning the trimmed sha and whether it existed.
func readLooseRef(gitDir, ref string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(gitDir, filepath.FromSlash(ref)))
	if err != nil {
		return "", false
	}
	sha := strings.TrimSpace(string(data))
	if isHexSHA(sha) {
		return sha, true
	}
	return "", false
}

// readPackedRef scans .git/packed-refs for ref, returning its sha if present.
func readPackedRef(gitDir, ref string) (string, bool) {
	f, err := os.Open(filepath.Join(gitDir, "packed-refs"))
	if err != nil {
		return "", false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "^") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == ref && isHexSHA(fields[0]) {
			return fields[0], true
		}
	}
	return "", false
}

// isHexSHA reports whether s looks like a git object id (SHA-1 = 40 hex, SHA-256
// = 64 hex). Kept lenient on length to tolerate future object formats.
func isHexSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
