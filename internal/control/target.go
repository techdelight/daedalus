// Copyright (C) 2026 Techdelight BV

package control

import (
	"errors"
	"fmt"
	"log"
	"sort"
)

// The plane-owned integration target (Sprint 59, item 1).
//
// WHY THIS EXISTS. Until Sprint 59 the acceptance oracle was frozen at whatever
// the project checkout's HEAD happened to be, and staleness was measured against
// the same HEAD. Both are refs in the project repository — and a Job's worktree
// shares the parent repository's refs, not merely its objects. A worker that
// could reach the common git dir could therefore move the branch the oracle was
// read from. The Sprint-58 ancestry guard caught the naive form of that (pointing
// the branch at the Job's own commit) but could not catch a content-preserving
// rewrite (`cherry-pick`, `commit-tree`, `format-patch | am` reproduce the same
// weakened .daedalus/verify.json under a fresh sha with no ancestry link), and it
// did not cover `task create` at all — an attacker could simply move the ref and
// wait for the next Task to freeze its oracle there.
//
// The structural fix is to stop reading the oracle from a ref the worker can
// write. The plane keeps its OWN target commit per project, in the control
// database (host-side authoritative state, §5), and:
//
//   - a Task's base_sha and frozen acceptance policy both come from that target;
//   - staleness is measured against that target;
//   - the ONLY things that move it are a completed integration transaction
//     (compare-and-swap) and an explicit human resync.
//
// An agent may now rewrite every branch in the repository to no effect: none of
// them is the ref the plane grades against. The ancestry guard survives as
// defence in depth, not as the mechanism.
//
// TWO HONEST CAVEATS, stated rather than glossed:
//
//  1. ADOPTION IS TRUST-ON-FIRST-USE. A project with no target yet takes the
//     operator's current checkout HEAD, once, at the first Task — before any Job
//     for that project has run under the plane. The plane cannot invent a trusted
//     starting commit; it can only refuse to keep taking new ones.
//  2. THE RESYNC IS A CONSEQUENTIAL OPERATION. `daedalus task target --sync`
//     re-points the target at the checkout's HEAD, which does adopt whatever
//     policy that commit carries. It is deliberately manual, logged, and belongs
//     on the Sprint-60 list of operations an agent client must never be granted.

// targetRefName is the git ref the plane writes as a PROJECTION of its target,
// purely so the landed commit is visible and reachable (garbage-collection-safe)
// inside the repository.
//
// It is NOT authoritative and nothing reads a decision from it: the control
// database is the authority. A worker that overwrites this ref changes nothing
// the plane believes — which is exactly the property the DB-side target buys.
// It is also deliberately not a branch anyone checks out, so updating it can
// never disturb a developer's working tree.
const targetRefName = "refs/daedalus/target"

// repoIdentity resolves a project to (its directory, the canonical repository
// path the plane keys its target by). Two projects on one repository resolve to
// the same identity and therefore share one merge queue.
func (s *Service) repoIdentity(project string) (dir string, repoPath string, err error) {
	dir, err = s.projects.ProjectDir(project)
	if err != nil {
		return "", "", err
	}
	repoPath, err = CanonicalRepoPath(dir)
	if err != nil {
		return "", "", err
	}
	return dir, repoPath, nil
}

// TargetFor returns a project's plane-owned integration target, adopting the
// checkout's current HEAD if its repository has none yet (trust-on-first-use).
func (s *Service) TargetFor(project string) (Target, error) {
	dir, repoPath, err := s.repoIdentity(project)
	if err != nil {
		return Target{}, err
	}
	t, err := s.store.GetTarget(repoPath)
	switch {
	case err == nil:
		return t, nil
	case !errors.Is(err, ErrNotFound):
		// ONLY a genuine "no target yet" may fall through to adoption. Any other
		// failure — a locked database, a corrupt row, an I/O error — must surface,
		// because the fallback path adopts the worker-writable checkout HEAD, and
		// this is the single most security-relevant read in the package: it decides
		// which commit the acceptance oracle is read from. Treating an unreadable
		// target as "there isn't one" would let a read failure re-open the very
		// hole the plane-owned target closes.
		return Target{}, fmt.Errorf("reading the integration target for %s: %w", project, err)
	}
	head, err := ReadHeadSHA(dir)
	if err != nil {
		return Target{}, err
	}
	adopted, err := s.store.AdoptTarget(repoPath, head)
	if err != nil {
		return Target{}, err
	}
	s.writeTargetProjection(dir, adopted.SHA)
	return adopted, nil
}

// SyncTarget re-points a project's target at its checkout's current HEAD — the
// human resync for commits that landed outside the plane.
//
// It is never automatic. An automatic follow would hand the acceptance oracle
// back to whoever can write the repository's refs, undoing the whole point of a
// plane-owned target; a person asking for it by name is the difference.
func (s *Service) SyncTarget(project string) (Target, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir, repoPath, err := s.repoIdentity(project)
	if err != nil {
		return Target{}, err
	}
	head, err := ReadHeadSHA(dir)
	if err != nil {
		return Target{}, err
	}
	previous := "(none)"
	if t, err := s.store.GetTarget(repoPath); err == nil {
		if t.SHA == head {
			return t, nil // already there; no event, no noise
		}
		previous = t.SHA
	} else if !errors.Is(err, ErrNotFound) {
		return Target{}, fmt.Errorf("reading the integration target for %s: %w", project, err)
	}
	t, err := s.store.SetTarget(repoPath, head, s.governanceMeta(), fmt.Sprintf(
		"target resynced by hand for %s: %s → %s (adopts the acceptance policy at the new commit)",
		project, shortSHA(previous), shortSHA(head)))
	if err != nil {
		return Target{}, err
	}
	s.writeTargetProjection(dir, t.SHA)
	return t, nil
}

// TargetView is a target as an operator sees it: which repository queue it is,
// which registry projects share that queue, and where the trunk currently is.
//
// SharedWith exists because sharing is the thing an operator most needs to know:
// two projects on one repository serialize against each other, and that is
// surprising unless it is shown.
type TargetView struct {
	RepoPath  string   `json:"repoPath"`
	SHA       string   `json:"sha"`
	UpdatedAt string   `json:"updatedAt"`
	Projects  []string `json:"projects"`
}

// ProjectTargets lists every integration target with the projects that share it
// (the CLI's `task target` view).
func (s *Service) ProjectTargets() ([]TargetView, error) {
	targets, err := s.store.ListTargets()
	if err != nil {
		return nil, err
	}
	// Map each known project onto its repository, so a shared queue is visible
	// rather than implied. Projects the resolver cannot answer for are skipped:
	// a stale registry entry must not fail the whole view.
	byRepo := map[string][]string{}
	if tasks, err := s.store.ListTasks(); err == nil {
		seen := map[string]bool{}
		for _, t := range tasks {
			if seen[t.Project] {
				continue
			}
			seen[t.Project] = true
			if _, repoPath, err := s.repoIdentity(t.Project); err == nil {
				byRepo[repoPath] = append(byRepo[repoPath], t.Project)
			}
		}
	}
	out := make([]TargetView, 0, len(targets))
	for _, t := range targets {
		projects := byRepo[t.RepoPath]
		sort.Strings(projects)
		out = append(out, TargetView{
			RepoPath: t.RepoPath, SHA: t.SHA, UpdatedAt: t.UpdatedAt, Projects: projects,
		})
	}
	return out, nil
}

// writeTargetProjection best-effort updates the repository's projection ref.
// A failure is logged and ignored: the database row is the authority, and the
// plane must not fail an operation because a convenience ref could not be
// written.
func (s *Service) writeTargetProjection(repoDir, sha string) {
	if repoDir == "" || sha == "" {
		return
	}
	if out, err := runGit(repoDir, "update-ref", targetRefName, sha); err != nil {
		log.Printf("control: updating %s in %s: %v\n%s", targetRefName, repoDir, err, out)
	}
}

// staleAgainstTarget reports whether a Task's base_sha is behind the project's
// plane-owned target — i.e. an integration landed while this Task was in flight,
// so its artifact was built against a trunk that has moved.
//
// This deliberately no longer consults the project checkout's HEAD. Under the
// plane-owned target a developer's (or an agent's) commits to a branch do not
// make a candidate stale, because they are not what the plane will integrate
// onto. Only a completed integration moves the trunk.
func (s *Service) staleAgainstTarget(task Task) (bool, string, error) {
	target, err := s.TargetFor(task.Project)
	if err != nil {
		return false, "", err
	}
	return task.BaseSHA != "" && task.BaseSHA != target.SHA, target.SHA, nil
}
