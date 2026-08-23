// Copyright (C) 2026 Techdelight BV

package control

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// WorktreeManager owns the isolated per-Job Git worktrees (docs/guild-master-plan.md
// §5). Each Job runs in a dedicated worktree checked out clean at the Task's
// base_sha on branch daedalus/<task>/<job>, under the Daedalus data dir — never
// the developer's live checkout. Isolation is an *artifact-provenance* property:
// the captured commit contains only the Job's changes.
//
// Side-effects are idempotent and deterministically named (path + branch derived
// from the job id), so a reconcile re-run is a no-op — the dual-write-safe
// property §6 relies on.
type WorktreeManager struct {
	root string // <DataDir>/control/worktrees
}

// NewWorktreeManager roots the manager at <dataDir>/control/worktrees.
func NewWorktreeManager(dataDir string) *WorktreeManager {
	return &WorktreeManager{root: filepath.Join(dataDir, "control", "worktrees")}
}

// Root returns the directory holding all job worktrees.
func (m *WorktreeManager) Root() string { return m.root }

// Path is the deterministic worktree directory for a job.
func (m *WorktreeManager) Path(jobID string) string {
	return filepath.Join(m.root, jobID)
}

// BranchName is the deterministic branch a job's worktree checks out.
func BranchName(taskID, jobID string) string {
	return fmt.Sprintf("daedalus/%s/%s", taskID, jobID)
}

// Add creates the worktree for a job: a clean checkout at baseSHA on a fresh
// branch daedalus/<task>/<job>. Idempotent-ish: if the deterministic path
// already exists it is treated as already-present (reconcile adoption) and the
// existing path is returned without error.
func (m *WorktreeManager) Add(repoDir, taskID, jobID, baseSHA string) (string, error) {
	path := m.Path(jobID)
	if _, err := os.Stat(path); err == nil {
		return path, nil // already materialised — deterministic name makes this safe
	}
	if err := os.MkdirAll(m.root, 0o755); err != nil {
		return "", fmt.Errorf("creating worktree root: %w", err)
	}
	branch := BranchName(taskID, jobID)
	// -B recreates the branch if a prior aborted attempt left it dangling, so a
	// retry with the same job id is deterministic rather than failing on
	// "branch already exists".
	if out, err := runGit(repoDir, "worktree", "add", "-B", branch, path, baseSHA); err != nil {
		return "", fmt.Errorf("git worktree add %s @ %s: %w\n%s", path, baseSHA, err, out)
	}
	return path, nil
}

// Capture stages and commits everything in the worktree (agents do not reliably
// commit, so the wrapper auto-commits at job end) and returns the resulting
// HEAD sha — the Job's output_snapshot, captured even on failure as a salvage
// snapshot (§5). If the tree is unchanged, HEAD stays at base_sha.
func (m *WorktreeManager) Capture(worktreePath string) (string, error) {
	if _, err := os.Stat(worktreePath); err != nil {
		return "", fmt.Errorf("worktree %s missing: %w", worktreePath, err)
	}
	// EXCLUDE THE HARNESS'S OWN SCRATCH FILES.
	//
	// daedalus's hooks write /workspace/.daedalus/activity.json on every tool use,
	// every stop and every prompt (settings.json), and for a Job /workspace IS the
	// worktree that becomes the artifact. So `git add -A` committed the plane's own
	// liveness state as part of the agent's work — in every project daedalus has
	// ever run a Job in.
	//
	// Found by a REVIEWER on another project's change (RV-18): "a harness state
	// file is committed as part of a change whose stated constraint is
	// documentation and planning only… it will churn on every job — producing
	// conflicts between exactly the parallel branches noted above."
	//
	// daedalus itself never noticed because its own .gitignore excludes
	// `.daedalus/*` for an unrelated reason, which is the worst way to be immune to
	// your own bug.
	//
	// Excluded by PATHSPEC rather than deleted: if a previous Job already committed
	// the file, removing it would stage a deletion the agent did not make. This
	// leaves whatever is tracked exactly as it is and simply stops adding more.
	// `.daedalus/verify.json` is deliberately NOT excluded — that is the project's
	// own acceptance policy and belongs in its history.
	addArgs := append([]string{"add", "-A", "--"}, harnessExcludes()...)
	if out, err := runGit(worktreePath, addArgs...); err != nil {
		return "", fmt.Errorf("git add: %w\n%s", err, out)
	}
	// Commit only if there is something staged; a no-op commit errors, which we
	// treat as "nothing to snapshot" and fall through to read the base HEAD.
	if !worktreeClean(worktreePath) {
		if out, err := runGit(worktreePath,
			"-c", "user.email=daedalus@localhost", "-c", "user.name=Daedalus",
			"commit", "-m", "daedalus: job snapshot"); err != nil {
			return "", fmt.Errorf("git commit: %w\n%s", err, out)
		}
	}
	head, err := runGit(worktreePath, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w\n%s", err, head)
	}
	return strings.TrimSpace(head), nil
}

// Remove tears down a job's worktree. repoDir may be empty (e.g. an orphan whose
// DB row is gone); in that case the checkout directory is removed directly. The
// harnessScratch are the paths daedalus itself writes into a project checkout.
// They are the plane's state, not the agent's work, and must never reach an
// artifact. Named individually rather than excluding `.daedalus/` wholesale,
// because `.daedalus/verify.json` in that same directory is project content the
// verifier reads from the commit.
var harnessScratch = []string{".daedalus/activity.json"}

// harnessExcludes renders harnessScratch as git pathspecs.
func harnessExcludes() []string {
	out := make([]string, 0, len(harnessScratch))
	for _, p := range harnessScratch {
		out = append(out, ":(exclude)"+p)
	}
	return out
}

// branch is intentionally preserved so a successful Job's Artifact commit
// survives after its worktree is reclaimed.
func (m *WorktreeManager) Remove(repoDir, jobID string) error {
	path := m.Path(jobID)
	if repoDir != "" {
		_, _ = runGit(repoDir, "worktree", "remove", "--force", path)
	}
	if _, err := os.Stat(path); err == nil {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("removing worktree dir %s: %w", path, err)
		}
	}
	if repoDir != "" {
		_, _ = runGit(repoDir, "worktree", "prune")
	}
	return nil
}

// Exists reports whether a job's worktree directory is present.
func (m *WorktreeManager) Exists(jobID string) bool {
	_, err := os.Stat(m.Path(jobID))
	return err == nil
}

// List returns the job ids that currently have a worktree directory. Used by
// reconcile to find orphans (worktrees with no live, non-terminal DB job).
func (m *WorktreeManager) List() ([]string, error) {
	entries, err := os.ReadDir(m.root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// worktreeClean reports whether the worktree has no staged/unstaged changes.
func worktreeClean(path string) bool {
	out, err := runGit(path, "status", "--porcelain")
	if err != nil {
		return false // be safe: attempt a commit rather than skip a real change
	}
	return strings.TrimSpace(out) == ""
}

// runGit runs a git command in dir and returns combined output. Shelling to the
// host git is intentional here — worktrees are host-side tooling, not a
// container operation (Sprint 55 does not touch Docker for this).
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// Deterministic identity so auto-commits never fail on a machine with no
	// configured git user, and so tests are reproducible.
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Daedalus", "GIT_AUTHOR_EMAIL=daedalus@localhost",
		"GIT_COMMITTER_NAME=Daedalus", "GIT_COMMITTER_EMAIL=daedalus@localhost",
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
