// Copyright (C) 2026 Techdelight BV

package control

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
// reading the plumbing directly rather than shelling out (the one exception is
// refSearchDirs' fallback for an unusual worktree layout). It handles:
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

	// Branch refs live in the COMMON dir, not the per-worktree dir. In a linked
	// worktree, HEAD and the per-worktree refs sit in .git/worktrees/<id>/ while
	// refs/heads/* stays in the parent .git — so both must be searched, or a
	// worktree checkout is misreported as an unborn branch.
	for _, d := range refSearchDirs(gitDir) {
		if sha, ok := readLooseRef(d, ref); ok {
			return sha, nil
		}
		if sha, ok := readPackedRef(d, ref); ok {
			return sha, nil
		}
	}
	return "", fmt.Errorf("repository at %q has no commit on %s yet (unborn branch); "+
		"make an initial commit before creating a task", dir, ref)
}

// refSearchDirs returns the directories that may hold a ref for gitDir: the dir
// itself, then the common dir when gitDir belongs to a linked worktree.
//
// The common dir is found from the `commondir` file git writes inside
// .git/worktrees/<id> (a path, usually relative). That keeps the lookup a pure
// file read, matching the rest of this file; `git rev-parse --git-common-dir` is
// the fallback for a layout where that file is missing.
func refSearchDirs(gitDir string) []string {
	dirs := []string{gitDir}
	data, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err == nil {
		common := strings.TrimSpace(string(data))
		if common != "" {
			if !filepath.IsAbs(common) {
				common = filepath.Join(gitDir, common)
			}
			return append(dirs, filepath.Clean(common))
		}
	}
	// Not a linked worktree (no commondir file), or it is unreadable: ask git,
	// but only when it can tell us something new.
	out, gitErr := runGit(gitDir, "rev-parse", "--git-common-dir")
	if gitErr != nil {
		return dirs
	}
	common := strings.TrimSpace(out)
	if common == "" || common == "." {
		return dirs
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitDir, common)
	}
	if common = filepath.Clean(common); common != filepath.Clean(gitDir) {
		dirs = append(dirs, common)
	}
	return dirs
}

// NOTE — there is deliberately no exported "current tip" helper here any more.
// Sprint 58 had TargetTipSHA/IsStaleBase, which read the project checkout's HEAD;
// that is a ref a Job's worktree can write, and reading the acceptance oracle
// from it was the hole the audit found. The tip the plane integrates onto now
// lives in the control database (see target.go), so anything needing "the
// current target" must ask Service.TargetFor and not git. ReadHeadSHA survives
// only for trust-on-first-use adoption and for reading a worktree's own HEAD.

// IsAncestor reports whether `ancestor` is reachable from `descendant` (i.e. it
// is contained in that commit's history). A commit is its own ancestor. Shells
// out to `git merge-base --is-ancestor`, whose exit status 1 means "no" rather
// than a failure — anything else is a real error.
func IsAncestor(repoDir, ancestor, descendant string) (bool, error) {
	if ancestor == "" || descendant == "" {
		return false, nil
	}
	cmd := exec.Command("git", "merge-base", "--is-ancestor", ancestor, descendant)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, wrapGit("git merge-base --is-ancestor", string(out), err)
}

// IsSelfAuthoredTip reports whether `tip` is reachable from any of the given
// Job commits — that is, whether the project's target tip contains work the Jobs
// themselves authored.
//
// This is the guard on `retry --rebase`. Rebasing re-freezes the acceptance
// oracle at the new tip, so if a Job can move that tip onto its own commit it can
// hand the plane an oracle it wrote (§6: "the acceptance oracle must live outside
// the agent's write scope"). Detecting the condition costs one `merge-base` per
// attempt and does not depend on *how* the tip came to be moved.
//
// KNOWN LIMITS — this is ancestry, not authorship, and it is not a complete
// defence (see docs/control-plane.md, "What the rebase guard does NOT cover"):
//   - a content-preserving rewrite (cherry-pick, commit-tree, format-patch|am)
//     reproduces the same weakened policy under a NEW sha with no ancestry link,
//     and is structurally indistinguishable from an honest commit;
//   - CreateTask freezes whatever ReadHeadSHA returns with NO ancestry check at
//     all, so an attacker that can move the target ref need not use --rebase — it
//     can simply wait for the next Task on that project.
//
// The real fix is a plane-owned target ref the agent cannot write, which arrives
// with the integration transaction (Sprint 59). Do not extend this check into a
// larger one and mistake that for closing the hole.
//
// The direction matters and is easy to get backwards: the question is whether a
// job commit is CONTAINED IN the tip's history — IsAncestor(jobCommit, tip) —
// not the reverse. Asking it the other way round would flag the perfectly
// ordinary case of a tip the Job was built on top of.
//
// Returns (selfAuthored, theOffendingJobCommit, err).
func IsSelfAuthoredTip(repoDir, tip string, jobCommits []string) (bool, string, error) {
	for _, commit := range jobCommits {
		if commit == "" {
			continue
		}
		containedInTip, err := IsAncestor(repoDir, commit, tip)
		if err != nil {
			return false, "", err
		}
		if containedInTip {
			return true, commit, nil
		}
	}
	return false, "", nil
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
