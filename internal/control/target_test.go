// Copyright (C) 2026 Techdelight BV

package control

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The Sprint-58 audit found two ways to launder a weakened acceptance oracle past
// the plane, and confirmed that no ancestry check could close either. These tests
// are the evidence that the plane-owned target closes them structurally.
//
// The attacker's goal throughout: get a Task graded against a `.daedalus/
// verify.json` that the worker wrote — `{"checks":["true"],
// "acceptanceGlobs":["nothing/matches/ever/**"]}` — instead of the strict policy
// the project actually declares.

const strictPolicy = `{"checks":["go test ./..."],"acceptanceGlobs":["**/*_test.go","testdata/**",".daedalus/verify.json"]}`
const weakenedPolicy = `{"checks":["true"],"acceptanceGlobs":["nothing/matches/ever/**"]}`

// TestAttack_CherryPickLaundering is the attack the audit confirmed bypasses the
// ancestry guard: the worker reproduces its weakened policy under a NEW sha with
// no ancestry link to any Job commit (cherry-pick / commit-tree / format-patch|am
// all do this), then points the project's branch at it. Ancestry sees an
// unrelated commit and allows everything.
//
// Under the plane-owned target it does not matter what the branch says: the
// oracle is read from a commit only the plane can move.
func TestAttack_CherryPickLaundering(t *testing.T) {
	repo := gitRepo(t)
	commitFile(t, repo, ".daedalus/verify.json", strictPolicy)

	svc, _, store := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)

	// The plane adopts the honest tip as its target.
	first, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "honest work"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	honestHash := first.AcceptanceHash
	target, err := svc.TargetFor("app")
	if err != nil {
		t.Fatalf("TargetFor: %v", err)
	}

	// --- the attack -----------------------------------------------------------
	// A worker builds the weakened policy on a side branch, then LAUNDERS it: the
	// content is replayed onto the honest trunk as a brand-new commit whose
	// ancestry contains no Job commit at all.
	branch := trim(mustGit(t, repo, "rev-parse", "--abbrev-ref", "HEAD"))
	mustGit(t, repo, "checkout", "-q", "-b", "worker-side", target.SHA)
	poisoned := commitFile(t, repo, ".daedalus/verify.json", weakenedPolicy)
	mustGit(t, repo, "checkout", "-q", branch)
	// cherry-pick: same tree, new sha, no ancestry link to `poisoned`.
	mustGit(t, repo, "cherry-pick", poisoned)
	laundered := trim(mustGit(t, repo, "rev-parse", "HEAD"))
	if laundered == poisoned {
		t.Fatal("precondition: the cherry-pick should produce a NEW sha")
	}
	// Confirm the laundering really does defeat ancestry, so this test keeps
	// proving something if the guard is ever leaned on again.
	selfAuthored, _, err := IsSelfAuthoredTip(repo, laundered, []string{poisoned})
	if err != nil {
		t.Fatalf("IsSelfAuthoredTip: %v", err)
	}
	if selfAuthored {
		t.Fatal("precondition: a cherry-picked commit must NOT be ancestry-linked (the attack assumes it is not)")
	}
	// And the poisoned content really is on the branch now.
	onBranch, err := ReadAcceptancePolicyAt(repo, laundered)
	if err != nil {
		t.Fatalf("ReadAcceptancePolicyAt: %v", err)
	}
	if onBranch.Hash() == honestHash {
		t.Fatal("precondition: the laundered commit should carry a different policy")
	}

	// --- the defence ----------------------------------------------------------
	// The plane's target never moved, so nothing downstream of it changed.
	after, err := svc.TargetFor("app")
	if err != nil {
		t.Fatalf("TargetFor after the attack: %v", err)
	}
	if after.SHA != target.SHA {
		t.Errorf("the target moved to %s — a worker must not be able to advance it", after.SHA)
	}
	// The existing Task's frozen oracle is untouched.
	reread, _ := store.GetTask(first.ID)
	if reread.AcceptanceHash != honestHash {
		t.Errorf("frozen acceptance hash changed: %s → %s", honestHash, reread.AcceptanceHash)
	}
}

// TestAttack_WaitForTheNextTask is the sharper half of the audit's finding: the
// Sprint-58 guard was bolted onto `retry --rebase`, but `CreateTask` froze
// whatever HEAD returned with no check at all — so the attacker never needed
// --rebase. It could poison the branch and simply wait for the next Task.
func TestAttack_WaitForTheNextTask(t *testing.T) {
	repo := gitRepo(t)
	commitFile(t, repo, ".daedalus/verify.json", strictPolicy)

	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)

	first, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "first"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	honestHash := first.AcceptanceHash
	if _, err := svc.CancelTask(first.ID); err != nil { // free the project
		t.Fatalf("CancelTask: %v", err)
	}

	// Poison the branch and wait. No --rebase, no ancestry link needed.
	poisoned := commitFile(t, repo, ".daedalus/verify.json", weakenedPolicy)
	if head, _ := ReadHeadSHA(repo); head != poisoned {
		t.Fatalf("precondition: HEAD should be the poisoned commit")
	}

	// The next Task must NOT pick it up.
	second, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "the next task"})
	if err != nil {
		t.Fatalf("CreateTask 2: %v", err)
	}
	if second.AcceptanceHash != honestHash {
		t.Errorf("the next Task froze a poisoned oracle: %s (want the honest %s)", second.AcceptanceHash, honestHash)
	}
	if second.BaseSHA == poisoned {
		t.Error("the next Task was based on the poisoned commit — base must come from the plane-owned target")
	}
	weak, _ := ReadAcceptancePolicyAt(repo, poisoned)
	if second.AcceptanceHash == weak.Hash() {
		t.Error("the next Task adopted the weakened policy")
	}
}

// TestAttack_WorkerRewritesEveryRef: the general statement. A worker with full
// write access to the repository's refs — which a linked worktree genuinely has —
// changes nothing the plane grades against.
func TestAttack_WorkerRewritesEveryRef(t *testing.T) {
	repo := gitRepo(t)
	commitFile(t, repo, ".daedalus/verify.json", strictPolicy)
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	honest := task.AcceptanceHash
	target, _ := svc.TargetFor("app")

	// Rewrite everything a worker could reach: the checked-out branch, a fresh
	// branch, and even the plane's own PROJECTION ref (which is explicitly not
	// authoritative — this asserts that claim rather than assuming it).
	branch := trim(mustGit(t, repo, "rev-parse", "--abbrev-ref", "HEAD"))
	mustGit(t, repo, "checkout", "-q", "-b", "evil", target.SHA)
	poisoned := commitFile(t, repo, ".daedalus/verify.json", weakenedPolicy)
	mustGit(t, repo, "checkout", "-q", branch)
	mustGit(t, repo, "update-ref", "refs/heads/"+branch, poisoned)
	mustGit(t, repo, "update-ref", targetRefName, poisoned)

	after, err := svc.TargetFor("app")
	if err != nil {
		t.Fatalf("TargetFor: %v", err)
	}
	if after.SHA != target.SHA {
		t.Errorf("target moved to %s after ref rewrites — the DB row must be the authority", after.SHA)
	}
	// A new Task still freezes the honest policy.
	if _, err := svc.CancelTask(task.ID); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	next, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "next"})
	if err != nil {
		t.Fatalf("CreateTask 2: %v", err)
	}
	if next.AcceptanceHash != honest {
		t.Errorf("a rewritten projection ref changed the frozen oracle: %s", next.AcceptanceHash)
	}
}

// --- adoption + CAS semantics --------------------------------------------------

func TestTarget_AdoptionIsOnce(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)

	first, err := svc.TargetFor("app")
	if err != nil {
		t.Fatalf("TargetFor: %v", err)
	}
	head, _ := ReadHeadSHA(repo)
	if first.SHA != head {
		t.Errorf("adopted %s, want the checkout HEAD %s", first.SHA, head)
	}
	// HEAD moves; a second resolution must NOT re-adopt.
	commitFile(t, repo, "later.txt", "later")
	second, err := svc.TargetFor("app")
	if err != nil {
		t.Fatalf("TargetFor 2: %v", err)
	}
	if second.SHA != first.SHA {
		t.Errorf("target re-adopted (%s → %s) — adoption must be trust-on-FIRST-use only", first.SHA, second.SHA)
	}
	// The projection ref exists and points at the target.
	if got := trim(mustGit(t, repo, "rev-parse", targetRefName)); got != first.SHA {
		t.Errorf("%s = %s, want %s", targetRefName, got, first.SHA)
	}
	// And the adoption is on the record.
	events, _ := store.ListEventsFor("repository", repoKey(t, repo))
	if len(events) == 0 || !strings.Contains(events[0].Note, "trust-on-first-use") {
		t.Errorf("adoption should be logged as trust-on-first-use, got %+v", events)
	}
}

func TestTarget_SyncIsExplicit(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)

	adopted, _ := svc.TargetFor("app")
	moved := commitFile(t, repo, "human.txt", "a developer landed this by hand")

	// Not automatic…
	still, _ := svc.TargetFor("app")
	if still.SHA != adopted.SHA {
		t.Fatal("the target followed HEAD without being asked")
	}
	// …but available on request.
	synced, err := svc.SyncTarget("app")
	if err != nil {
		t.Fatalf("SyncTarget: %v", err)
	}
	if synced.SHA != moved {
		t.Errorf("synced target = %s, want %s", synced.SHA, moved)
	}
	if got := trim(mustGit(t, repo, "rev-parse", targetRefName)); got != moved {
		t.Errorf("projection ref = %s, want %s", got, moved)
	}
	// Idempotent.
	again, err := svc.SyncTarget("app")
	if err != nil || again.SHA != moved {
		t.Errorf("SyncTarget again = (%v, %v), want a no-op", again.SHA, err)
	}
}

// TestAdvanceTarget_CompareAndSwap is a GENUINE concurrent race: N goroutines all
// try to advance the same target from the same commit. Exactly one may win — that
// is the whole safety property of the merge queue.
func TestAdvanceTarget_CompareAndSwap(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.AdoptTarget("/repos/proj", "aaaa"); err != nil {
		t.Fatalf("AdoptTarget: %v", err)
	}

	const racers = 8
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		winners []string
		losers  int
	)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // release them all at once
			to := "bbbb" + string(rune('a'+i))
			_, err := s.AdvanceTarget("/repos/proj", "aaaa", to, "racer")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				winners = append(winners, to)
			case errors.Is(err, ErrConflict):
				losers++
			default:
				t.Errorf("unexpected error from racer %d: %v", i, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if len(winners) != 1 {
		t.Fatalf("%d goroutines won the compare-and-swap, want exactly 1: %v", len(winners), winners)
	}
	if losers != racers-1 {
		t.Errorf("losers = %d, want %d", losers, racers-1)
	}
	got, err := s.GetTarget("/repos/proj")
	if err != nil {
		t.Fatalf("GetTarget: %v", err)
	}
	if got.SHA != winners[0] {
		t.Errorf("target = %s, want the winner's %s", got.SHA, winners[0])
	}
}

func TestAdvanceTarget_StaleFromLoses(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.AdoptTarget("/repos/proj", "aaaa"); err != nil {
		t.Fatalf("AdoptTarget: %v", err)
	}
	if _, err := s.AdvanceTarget("/repos/proj", "aaaa", "bbbb", "first"); err != nil {
		t.Fatalf("first advance: %v", err)
	}
	// A second integration that started from the old target must lose.
	_, err := s.AdvanceTarget("/repos/proj", "aaaa", "cccc", "stale")
	if !errors.Is(err, ErrConflict) {
		t.Errorf("stale advance err = %v, want ErrConflict", err)
	}
	got, _ := s.GetTarget("/repos/proj")
	if got.SHA != "bbbb" {
		t.Errorf("target = %s, want bbbb (the stale advance must not write)", got.SHA)
	}
}

func TestAdoptTarget_ConcurrentFirstTasksAgree(t *testing.T) {
	s := openTestStore(t)
	const racers = 8
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		seen = map[string]int{}
	)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			// Each racer offers a DIFFERENT commit, as two concurrent first Tasks
			// reading a moving HEAD might.
			tgt, err := s.AdoptTarget("/repos/proj", "sha"+string(rune('a'+i)))
			if err != nil {
				t.Errorf("AdoptTarget: %v", err)
				return
			}
			mu.Lock()
			seen[tgt.SHA]++
			mu.Unlock()
		}(i)
	}
	close(start)
	wg.Wait()

	if len(seen) != 1 {
		t.Errorf("concurrent adoption produced %d different targets, want 1: %v", len(seen), seen)
	}
}

// repoKey resolves a repository's canonical path — the key the plane stores its
// integration target under.
func repoKey(t *testing.T, repoDir string) string {
	t.Helper()
	key, err := CanonicalRepoPath(repoDir)
	if err != nil {
		t.Fatalf("CanonicalRepoPath(%s): %v", repoDir, err)
	}
	return key
}

// repoKeyNoT is repoKey for doubles that have no *testing.T to fail on.
func repoKeyNoT(repoDir string) string {
	key, err := CanonicalRepoPath(repoDir)
	if err != nil {
		return repoDir
	}
	return key
}

// --- one repository, one merge queue (audit F2) --------------------------------

// TestTarget_TwoProjectsOneRepoShareAQueue is the regression for the audit's F2:
// the target used to be keyed by PROJECT name, so two registry projects pointing
// at one repository got independent target rows — two merge queues on one trunk,
// each rebasing onto its own notion of it and swapping a row the other never
// read. The queue's whole safety property silently did not apply between them.
func TestTarget_TwoProjectsOneRepoShareAQueue(t *testing.T) {
	repo := gitRepo(t)
	// Two registry entries, one checkout — a clone registered twice, or a project
	// registered on a subdirectory of another.
	svc, _, store := newService(t, mapResolver{"app": repo, "app-clone": repo}, StubRunner{}, nil)

	a, err := svc.TargetFor("app")
	if err != nil {
		t.Fatalf("TargetFor(app): %v", err)
	}
	b, err := svc.TargetFor("app-clone")
	if err != nil {
		t.Fatalf("TargetFor(app-clone): %v", err)
	}
	if a.RepoPath != b.RepoPath {
		t.Fatalf("two projects on one repo resolved to %q and %q — they must share a key", a.RepoPath, b.RepoPath)
	}
	if a.SHA != b.SHA {
		t.Errorf("shared repo has two targets: %s vs %s", a.SHA, b.SHA)
	}
	// Exactly ONE row exists, not one per project.
	all, _ := store.ListTargets()
	if len(all) != 1 {
		t.Fatalf("targets = %d, want 1 for one repository: %+v", len(all), all)
	}

	// An integration landing under one project moves the trunk the OTHER sees —
	// which is what makes the compare-and-swap protect them from each other.
	landed := commitFileOn(t, repo, a.SHA, "landed.txt", "integrated under app")
	if _, err := store.AdvanceTarget(a.RepoPath, a.SHA, landed, "test: app integrated"); err != nil {
		t.Fatalf("AdvanceTarget: %v", err)
	}
	after, err := svc.TargetFor("app-clone")
	if err != nil {
		t.Fatalf("TargetFor(app-clone) after: %v", err)
	}
	if after.SHA != landed {
		t.Errorf("app-clone's target = %s, want %s — a landing under one project must move the shared trunk",
			after.SHA, landed)
	}
	// And a stale compare-and-swap from the other project's starting point loses.
	if _, err := store.AdvanceTarget(b.RepoPath, b.SHA, "cafebabe", "test: app-clone integrating from a stale start"); !errors.Is(err, ErrConflict) {
		t.Errorf("stale CAS from the sibling project = %v, want ErrConflict", err)
	}

	// The operator-facing view says the queue is shared, rather than leaving it
	// to be discovered.
	if _, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"}); err != nil {
		t.Fatalf("CreateTask(app): %v", err)
	}
	if _, err := svc.CreateTask(CreateTaskRequest{Project: "app-clone", Objective: "y"}); err != nil {
		t.Fatalf("CreateTask(app-clone): %v", err)
	}
	views, err := svc.ProjectTargets()
	if err != nil {
		t.Fatalf("ProjectTargets: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("views = %d, want 1", len(views))
	}
	if len(views[0].Projects) != 2 {
		t.Errorf("view projects = %v, want both projects listed as sharing the queue", views[0].Projects)
	}
}

// TestCanonicalRepoPath maps a subdirectory and a symlink onto the same identity,
// so "two projects, one repository" cannot be dodged by registering a subdir.
func TestCanonicalRepoPath(t *testing.T) {
	repo := gitRepo(t)
	root, err := CanonicalRepoPath(repo)
	if err != nil {
		t.Fatalf("CanonicalRepoPath: %v", err)
	}

	sub := filepath.Join(repo, "nested", "deeper")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := CanonicalRepoPath(sub)
	if err != nil {
		t.Fatalf("CanonicalRepoPath(subdir): %v", err)
	}
	if got != root {
		t.Errorf("subdirectory resolved to %q, want the repo root %q", got, root)
	}

	// A non-repository still yields a stable absolute identity rather than an error.
	plain := t.TempDir()
	if got, err := CanonicalRepoPath(plain); err != nil || got == "" {
		t.Errorf("CanonicalRepoPath(non-repo) = (%q, %v), want a resolved path", got, err)
	}
}
