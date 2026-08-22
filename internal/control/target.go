// Copyright (C) 2026 Techdelight BV

package control

import (
	"crypto/sha256"
	"encoding/hex"
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

// QueueIDFor returns the stable, opaque identifier for a repository's merge
// queue: a truncated SHA-256 of its canonical path.
//
// WHY AN OPAQUE ID. The canonical path is an absolute HOST filesystem path, and
// two things were leaking it. `GET /targets` listed every queue with its path —
// so an agent working on one project would learn the on-disk layout of every
// other repository, including the Guild Master's own, which is exactly the line
// M12 drew when it made cross-project access read-only and mount-mediated. Worse,
// AdoptTarget/AdvanceTarget/SetTarget wrote those paths into the APPEND-ONLY
// event log as entity ids: once an agent client can read that log, the paths are
// historical and unerasable, and fixing the projection later would not retract
// what was already recorded. Hence doing it now, while the log is short.
//
// An agent keeps everything it legitimately needs — whether two projects share a
// queue is visible, because the id is stable and comparable — and learns nothing
// about host layout. Humans still see the path (targetView renders it for them),
// because for a person the path is the useful part.
//
// 16 hex characters is 64 bits: collision-proof enough for the number of
// repositories one host can hold, and short enough to read in a log line.
func QueueIDFor(canonicalPath string) string {
	sum := sha256.Sum256([]byte(canonicalPath))
	return "q-" + hex.EncodeToString(sum[:])[:16]
}

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

// Reading the target and adopting one are TWO OPERATIONS, deliberately
// (CONTRIBUTING.md § Command-Query Separation).
//
// They used to be one function, `TargetFor`, named and typed as a query — it
// returned a Target — that on first call also wrote a database row and a git ref.
// A command wearing a query's name is always a liability, and here it was the
// worst possible one: this is the single most security-relevant read in the
// package, because it decides which commit the acceptance oracle is frozen at.
// Every caller that merely wanted to *know* the target was, on a bad day, one
// missing row away from *creating* one from the worker-writable checkout HEAD —
// on a retry, on a re-verification, in the middle of landing code.
//
// So: Target reads and never writes; ensureTarget adopts and is called from
// exactly one place. Trust-on-first-use is unchanged as a behaviour. What changed
// is that it can now only happen where it belongs.

// Target returns a project's plane-owned integration target.
//
// PURE QUERY. It adopts nothing, writes nothing, and returns an ErrNotFound-
// wrapped error when the project's repository has no target yet. A caller that
// gets that answer should think hard before doing anything other than failing:
// outside `CreateTask`, "there is no target" means something is wrong, not that
// one should be invented.
func (s *Service) Target(project string) (Target, error) {
	_, repoPath, err := s.repoIdentity(project)
	if err != nil {
		return Target{}, err
	}
	t, err := s.store.GetTarget(repoPath)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// Wrapped so errors.Is still answers true. The distinction between "no
			// target yet" and "the read failed" is load-bearing for ensureTarget, and
			// a caller that cannot tell them apart cannot make it.
			return Target{}, fmt.Errorf("%w: no integration target has been adopted for %s", ErrNotFound, project)
		}
		return Target{}, fmt.Errorf("reading the integration target for %s: %w", project, err)
	}
	return t, nil
}

// ensureTarget adopts the checkout's current HEAD as a project's integration
// target if its repository has none (trust-on-first-use).
//
// COMMAND: it returns no data, only whether it succeeded. Idempotent — a
// repository that already has a target is left exactly as it was, and
// Store.AdoptTarget makes that true even against a concurrent first Task, because
// its insert-or-read is a single transaction.
//
// UNEXPORTED, AND THAT IS THE POINT. The only production caller is createTask,
// which is the one moment a project legitimately has no target: before any Job for
// it has ever run under the plane. Keeping it off the Service's exported surface
// means no daemon route, no CLI verb and no MCP tool can be wired to it by
// somebody who has not read this comment — which is the whole difference between
// "adoption happens once, at the beginning" and "adoption happens whenever a read
// misses".
func (s *Service) ensureTarget(project string) error {
	dir, repoPath, err := s.repoIdentity(project)
	if err != nil {
		return err
	}
	switch _, err := s.store.GetTarget(repoPath); {
	case err == nil:
		return nil // already adopted; never silently re-adopted
	case !errors.Is(err, ErrNotFound):
		// ONLY a genuine "no target yet" may fall through to adoption. Any other
		// failure — a locked database, a corrupt row, an I/O error — must surface,
		// because the fallback path below adopts the worker-writable checkout HEAD.
		// Treating an unreadable target as "there isn't one" would let a read failure
		// re-open the very hole the plane-owned target closes.
		return fmt.Errorf("reading the integration target for %s: %w", project, err)
	}
	head, err := ReadHeadSHA(dir)
	if err != nil {
		return err
	}
	adopted, err := s.store.AdoptTarget(repoPath, head)
	if err != nil {
		return err
	}
	s.writeTargetProjection(dir, adopted.SHA)
	return nil
}

// SyncTarget re-points a project's target at its checkout's current HEAD — the
// human resync for commits that landed outside the plane.
//
// It is never automatic. An automatic follow would hand the acceptance oracle
// back to whoever can write the repository's refs, undoing the whole point of a
// plane-owned target; a person asking for it by name is the difference.
func (s *Service) SyncTarget(project string) (Target, error) { return s.syncTarget(Human(), project) }

// syncTarget is SyncTarget with an explicit caller identity.
func (s *Service) syncTarget(caller Caller, project string) (Target, error) {
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
	t, err := s.store.SetTarget(repoPath, head, governanceMetaFor(caller), fmt.Sprintf(
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
	// QueueID is the opaque, stable identity of the queue — always present, safe
	// to show any caller.
	QueueID string `json:"queueId"`
	// RepoPath is the absolute host path. Present for HUMAN callers only; an
	// agent receives it empty (see QueueIDFor).
	RepoPath  string   `json:"repoPath,omitempty"`
	SHA       string   `json:"sha"`
	UpdatedAt string   `json:"updatedAt"`
	Projects  []string `json:"projects"`
}

// TargetLag reports how far a project's checkout has moved past the plane-owned
// integration target (#89).
//
// WHY THIS EXISTS. The target is what every Job is dispatched against, and it
// moves only when work integrates or when somebody runs `task target --sync`.
// Nothing made it follow a branch, and nothing said when it had stopped
// following one — so a repository can be worked on for days while the plane
// keeps handing agents a tree from before any of it.
//
// Measured 2026-08-22: seven commits were pushed to `development` in a day, the
// target was never synced, and T-17 was dispatched against a base from four days
// earlier — a tree in which plane-owned programmes did not exist. The agent's
// work was coherent for the tree it was given and obsolete for the repository.
//
// **`stale_base` cannot catch this and never could.** It compares a candidate's
// base against the target tip, and the target tip WAS its base. The plane was
// perfectly consistent with itself and four days behind the world. The check is
// not wrong; it answers a different question, and nothing was asking this one.
//
// So this REPORTS and refuses nothing. A branch deliberately ahead of the target
// is a normal way to work — the operator is the one who knows whether the gap is
// intended, and the only failure worth fixing is that they could not see it.
type TargetLag struct {
	Project   string `json:"project"`
	TargetSHA string `json:"targetSha"`
	HeadSHA   string `json:"headSha"`
	// Behind is how many commits the checkout's HEAD is ahead of the target.
	Behind int `json:"behind,omitempty"`
	// Diverged is true when the target is not an ancestor of HEAD at all — a
	// rebase or a reset moved the branch out from under it. Reported separately
	// because "behind by 7" and "no longer on this history" need different
	// answers, and a commit count alone would render the second as the first.
	Diverged bool `json:"diverged,omitempty"`
	// Unknown carries why the comparison could not be made, when it could not.
	// Silence would read as "up to date", which is the one answer this must never
	// give wrongly.
	Unknown string `json:"unknown,omitempty"`
}

// Stale reports whether this lag is worth telling somebody about.
func (l TargetLag) Stale() bool { return l.Behind > 0 || l.Diverged }

// Summary is the one-line rendering shared by every surface, so the CLI, the
// warning at create time and the daemon's status cannot describe the same gap
// differently.
func (l TargetLag) Summary() string {
	switch {
	case l.Unknown != "":
		return "target gap unknown: " + l.Unknown
	case l.Diverged:
		return fmt.Sprintf("the target %s is not on this checkout's history — a rebase or reset moved the branch",
			shortSHA(l.TargetSHA))
	case l.Behind == 1:
		return fmt.Sprintf("the target is 1 commit behind this checkout (%s → %s)",
			shortSHA(l.TargetSHA), shortSHA(l.HeadSHA))
	case l.Behind > 1:
		return fmt.Sprintf("the target is %d commits behind this checkout (%s → %s)",
			l.Behind, shortSHA(l.TargetSHA), shortSHA(l.HeadSHA))
	}
	return "the target is the checkout's HEAD"
}

// TargetLagFor compares a project's integration target against its checkout.
func (s *Service) TargetLagFor(project string) (TargetLag, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.targetLagLocked(project)
}

// targetLagLocked is TargetLagFor with s.mu already held.
//
// Every failure to compare answers Unknown rather than an error: this is a
// report attached to other work, and a project whose repository has been moved
// or whose target has never been set must not fail the operation it is decorating.
func (s *Service) targetLagLocked(project string) (TargetLag, error) {
	lag := TargetLag{Project: project}
	dir, repoPath, err := s.repoIdentity(project)
	if err != nil {
		lag.Unknown = err.Error()
		return lag, nil
	}
	t, err := s.store.GetTarget(repoPath)
	if err != nil {
		// No target yet is not a gap: the first integration or sync sets it, and
		// there is nothing for the checkout to be ahead of.
		if errors.Is(err, ErrNotFound) {
			return lag, nil
		}
		lag.Unknown = err.Error()
		return lag, nil
	}
	lag.TargetSHA = t.SHA
	head, err := ReadHeadSHA(dir)
	if err != nil {
		lag.Unknown = err.Error()
		return lag, nil
	}
	lag.HeadSHA = head
	if head == t.SHA {
		return lag, nil
	}
	// Ancestry first: a target that is not on this history has no meaningful
	// commit count, and rev-list would happily return one anyway.
	onHistory, err := IsAncestor(dir, t.SHA, head)
	if err != nil {
		lag.Unknown = err.Error()
		return lag, nil
	}
	if !onHistory {
		lag.Diverged = true
		return lag, nil
	}
	n, err := CountCommitsBetween(dir, t.SHA, head)
	if err != nil {
		lag.Unknown = err.Error()
		return lag, nil
	}
	lag.Behind = n
	return lag, nil
}

// TargetLags reports the gap for every project the plane knows a target for,
// newest-first by size, and only the ones worth mentioning.
func (s *Service) TargetLags() ([]TargetLag, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	targets, err := s.store.ListTargets()
	if err != nil {
		return nil, err
	}
	byRepo := s.projectsByRepo()
	var out []TargetLag
	for _, t := range targets {
		projects := byRepo[t.RepoPath]
		sort.Strings(projects)
		if len(projects) == 0 {
			continue // a target whose projects are all deregistered
		}
		// One comparison per REPOSITORY, reported under the first project sharing
		// it: the target is keyed by repo, so the gap is a fact about the repo and
		// repeating it per project would triple-count one problem.
		lag, err := s.targetLagLocked(projects[0])
		if err != nil {
			return nil, err
		}
		if lag.Stale() || lag.Unknown != "" {
			out = append(out, lag)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Behind > out[j].Behind })
	return out, nil
}

// ProjectTargets lists every integration target with the projects that share it
// (the CLI's `task target` view), for a HUMAN caller.
func (s *Service) ProjectTargets() ([]TargetView, error) { return s.projectTargets(Human()) }

// projectTargets renders the target list for a caller class. The repository path
// is included ONLY for a human: see QueueIDFor for why an agent must not receive
// host filesystem layout.
func (s *Service) projectTargets(caller Caller) ([]TargetView, error) {
	targets, err := s.store.ListTargets()
	if err != nil {
		return nil, err
	}
	byRepo := s.projectsByRepo()
	out := make([]TargetView, 0, len(targets))
	for _, t := range targets {
		projects := byRepo[t.RepoPath]
		sort.Strings(projects)
		view := TargetView{
			QueueID: t.QueueID, SHA: t.SHA, UpdatedAt: t.UpdatedAt, Projects: projects,
		}
		if !caller.IsAgent() {
			view.RepoPath = t.RepoPath
		}
		out = append(out, view)
	}
	return out, nil
}

// projectsByRepo maps each REGISTERED project onto the repository whose queue it
// shares.
//
// It enumerates the registry rather than the tasks table. Deriving it from tasks
// (as this first did) makes a shared queue look unshared until every sharer
// happens to have a task — which is precisely when an operator most needs to know
// that two projects will serialize against each other. Projects the resolver
// cannot answer for are skipped: a stale registry entry must not fail the view.
func (s *Service) projectsByRepo() map[string][]string {
	byRepo := map[string][]string{}
	seen := map[string]bool{}
	add := func(project string) {
		if project == "" || seen[project] {
			return
		}
		seen[project] = true
		if _, repoPath, err := s.repoIdentity(project); err == nil {
			byRepo[repoPath] = append(byRepo[repoPath], project)
		}
	}
	if lister, ok := s.projects.(ProjectLister); ok {
		if names, err := lister.ProjectNames(); err == nil {
			for _, n := range names {
				add(n)
			}
		}
	}
	// Tasks are a fallback, not the source: a resolver that cannot enumerate
	// still yields the projects the plane has actually worked with.
	if tasks, err := s.store.ListTasks(); err == nil {
		for _, t := range tasks {
			add(t.Project)
		}
	}
	return byRepo
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
	// A pure read. If the Task exists, CreateTask already adopted a target, so a
	// miss here is a genuine fault — and adopting one at this point would be worse
	// than failing: the verdict "is this candidate stale?" would then be computed
	// against the checkout HEAD, which is exactly the worker-writable value the
	// plane-owned target exists to stop grading against.
	target, err := s.Target(task.Project)
	if err != nil {
		return false, "", err
	}
	return task.BaseSHA != "" && task.BaseSHA != target.SHA, target.SHA, nil
}
