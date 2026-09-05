// Copyright (C) 2026 Techdelight BV

package control

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ADOPTING LANDED WORK.
//
// The property under test is not "git can fast-forward" — advanceCheckoutBranch
// is already covered by the --into-branch tests in integrate_test.go, and this
// code calls it rather than reimplementing it. What is tested here is the part
// that is new: that the offer is made ONCE PER PROJECT however many tasks landed
// into it, that a project with nothing to adopt says so instead of offering an
// action, and that every answer — including the refusals and including success —
// comes back with the plane's own sentence attached.

// landedProject drives `n` tasks through to `integrated` on one project, WITHOUT
// --into-branch. That last part is the whole fixture: it leaves the repository in
// the state the plane's default produces — a target ahead of a checkout that has
// not moved — which is the state an adoption exists for.
func landedProject(t *testing.T, repo string, n int) (*Service, *Store, []string) {
	t.Helper()
	// No MarkerName: the stub then writes a file per JOB, so each task's diff is
	// genuinely its own and the second landing is not an empty commit.
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, StubVerifyRunner{Pass: true})

	var ids []string
	for i := 0; i < n; i++ {
		ids = append(ids, landOne(t, svc, "app", fmt.Sprintf("landed work %d", i+1)))
	}
	return svc, store, ids
}

// landOne drives one task on one project all the way to `integrated`, WITHOUT
// --into-branch, and returns its id.
func landOne(t *testing.T, svc *Service, project, objective string) string {
	t.Helper()
	task, err := svc.CreateTask(CreateTaskRequest{Project: project, Objective: objective})
	if err != nil {
		t.Fatalf("CreateTask(%s): %v", objective, err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch %s: %v", task.ID, err)
	}
	if _, err := svc.VerifyTask(task.ID, VerifyRequest{}); err != nil {
		t.Fatalf("Verify %s: %v", task.ID, err)
	}
	if _, err := svc.ApproveTask(task.ID, ""); err != nil {
		t.Fatalf("Approve %s: %v", task.ID, err)
	}
	if _, err := svc.IntegrateTask(task.ID, IntegrateRequest{}); err != nil {
		t.Fatalf("Integrate %s: %v", task.ID, err)
	}
	return task.ID
}

// onlyAdoption asserts there is exactly one and returns it — the shape most of
// these tests want, and the assertion that the per-project rule holds.
func onlyAdoption(t *testing.T, svc *Service) Adoption {
	t.Helper()
	list, err := svc.Adoptions()
	if err != nil {
		t.Fatalf("Adoptions: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("Adoptions() returned %d entries, want 1: %+v", len(list), list)
	}
	return list[0]
}

// TestAdopt_MovesABranchBehindItsTarget is the happy path: work landed, the
// branch did not move (by design), and one action takes it.
func TestAdopt_MovesABranchBehindItsTarget(t *testing.T) {
	repo := gitRepo(t)
	before := trim(mustGit(t, repo, "rev-parse", "HEAD"))
	svc, _, ids := landedProject(t, repo, 1)

	target, err := svc.Target("app")
	if err != nil {
		t.Fatalf("Target: %v", err)
	}
	if target.SHA == before {
		t.Fatal("precondition: the landing should have advanced the target")
	}
	if now := trim(mustGit(t, repo, "rev-parse", "HEAD")); now != before {
		t.Fatal("precondition: a landing must not move a branch on its own")
	}

	// The read says what would move, to what, and by how much — the three things
	// the Landed column has to name.
	a := onlyAdoption(t, svc)
	if a.Adopted || !a.Adoptable {
		t.Fatalf("adoption = %+v; a checkout behind its target has work to adopt", a)
	}
	if a.Branch == "" || a.TargetSHA != target.SHA || a.Behind != 1 {
		t.Errorf("adoption = %+v; want the branch named, target %s and behind 1",
			a, shortSHA(target.SHA))
	}
	if !strings.Contains(a.Note, a.Branch) || !strings.Contains(a.Note, "behind") {
		t.Errorf("Note = %q; it should name the branch and say how far behind it is", a.Note)
	}
	if len(a.Waiting) != 1 || a.Waiting[0] != ids[0] {
		t.Errorf("Waiting = %v, want the one task the branch is missing (%s)", a.Waiting, ids[0])
	}

	res, err := svc.AdoptLanded("app")
	if err != nil {
		t.Fatalf("AdoptLanded: %v", err)
	}
	if !res.Adopted {
		t.Errorf("result = %+v, want adopted", res)
	}
	if head := trim(mustGit(t, repo, "rev-parse", "HEAD")); head != target.SHA {
		t.Errorf("HEAD = %s, want the landed target %s", shortSHA(head), shortSHA(target.SHA))
	}
	// THE NOTE ON THE SUCCESS PATH TOO. advanceCheckoutBranch fills it on every
	// path precisely because "nothing appeared to happen" is the complaint this
	// area exists to answer; dropping it here would reintroduce the silence one
	// layer up.
	if res.Note == "" || !strings.Contains(res.Note, res.Branch) {
		t.Errorf("Note = %q; a successful adoption must still say which branch moved and where to", res.Note)
	}
	// And the offer is withdrawn, because there is nothing left to do.
	if after := onlyAdoption(t, svc); !after.Adopted || after.Adoptable || after.Pending {
		t.Errorf("after adopting: %+v; the row should say there is nothing to adopt", after)
	}
}

// TestAdopt_RefusesADirtyTreeAndLeavesItUntouched — the guard that matters most,
// reached through the new operation rather than through integrate. It holds here
// because this code calls advanceCheckoutBranch rather than doing the move
// itself; a reimplementation is exactly how it would stop holding.
func TestAdopt_RefusesADirtyTreeAndLeavesItUntouched(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := landedProject(t, repo, 1)

	// An edit in progress, of the kind anyone might have open.
	if err := os.WriteFile(filepath.Join(repo, "seed.txt"), []byte("work in progress"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := trim(mustGit(t, repo, "rev-parse", "HEAD"))

	res, err := svc.AdoptLanded("app")
	var rej *RejectionError
	if !errors.As(err, &rej) {
		t.Fatalf("AdoptLanded over a dirty tree = %v (%+v), want a typed refusal", err, res)
	}
	if rej.Reason != ReasonBranchNotAdvanced {
		t.Errorf("reason = %q, want %q", rej.Reason, ReasonBranchNotAdvanced)
	}
	// The refusal carries advanceCheckoutBranch's own words: what is in the way,
	// and what to do about it. A bare status would leave the operator guessing at
	// which of the three refusals they had hit.
	if !strings.Contains(rej.Message, "uncommitted changes") ||
		!strings.Contains(rej.Message, targetRefName) {
		t.Errorf("message = %q; it should say the tree is dirty and name the ref to merge once it is not",
			rej.Message)
	}
	if after := trim(mustGit(t, repo, "rev-parse", "HEAD")); after != before {
		t.Errorf("HEAD moved despite the dirty tree: %s → %s", shortSHA(before), shortSHA(after))
	}
	body, err := os.ReadFile(filepath.Join(repo, "seed.txt"))
	if err != nil || string(body) != "work in progress" {
		t.Errorf("the uncommitted edit was not preserved: %q (%v)", body, err)
	}
	// The landed work is untouched and still reachable: a refused adoption says
	// nothing about the landing.
	target, _ := svc.Target("app")
	if trim(mustGit(t, repo, "rev-parse", targetRefName)) != target.SHA {
		t.Error("the landed commit should still be reachable through the projection ref")
	}
}

// TestAdopt_AlreadyAtTheTargetIsSuccessNotAnError.
//
// Two people press Adopt, or one presses it twice, or the project was landed
// with --into-branch in the first place. The branch is exactly where it should
// be, and calling that a failure would tell somebody whose checkout is right
// that it is wrong — the same mistake RV-8 fixed one layer down.
func TestAdopt_AlreadyAtTheTargetIsSuccessNotAnError(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := landedProject(t, repo, 1)
	if _, err := svc.AdoptLanded("app"); err != nil {
		t.Fatalf("first AdoptLanded: %v", err)
	}
	head := trim(mustGit(t, repo, "rev-parse", "HEAD"))

	res, err := svc.AdoptLanded("app")
	if err != nil {
		t.Fatalf("adopting a branch that is already there = %v, want success", err)
	}
	if !res.Adopted {
		t.Errorf("result = %+v; a branch that HAS the landed commit is adopted", res)
	}
	if !strings.Contains(res.Note, "already") {
		t.Errorf("Note = %q; it should say the branch was already at the landed commit", res.Note)
	}
	if strings.Contains(res.Note, "merge --ff-only") {
		t.Errorf("Note = %q; a branch that already has the work must not be handed a remedy that does nothing",
			res.Note)
	}
	if after := trim(mustGit(t, repo, "rev-parse", "HEAD")); after != head {
		t.Errorf("HEAD moved on a no-op adoption: %s → %s", shortSHA(head), shortSHA(after))
	}
}

// TestAdoptions_OneRowPerProjectNotPerLandedTask is the shape of the whole
// feature. Three tasks land into one project; the branch is one fast-forward
// behind, not three, so there is one thing to do and one row to do it from.
func TestAdoptions_OneRowPerProjectNotPerLandedTask(t *testing.T) {
	repo := gitRepo(t)
	svc, store, ids := landedProject(t, repo, 3)

	landed := 0
	tasks, _ := store.ListTasks()
	for _, task := range tasks {
		if task.State == StateIntegrated {
			landed++
		}
	}
	if landed != 3 {
		t.Fatalf("precondition: %d tasks landed, want 3", landed)
	}

	// One entry. The assertion is onlyAdoption's, and it is the point of the test.
	a := onlyAdoption(t, svc)
	if a.Project != "app" {
		t.Errorf("project = %q, want app", a.Project)
	}
	if len(a.Waiting) != len(ids) {
		t.Errorf("Waiting = %v, want all three tasks named on the one row (%v)", a.Waiting, ids)
	}
	if a.Behind != 3 {
		t.Errorf("behind = %d, want 3 — the gap is measured in commits, and three landed",
			a.Behind)
	}

	// And ONE action clears all three. Not three actions, and not one that leaves
	// the other two behind.
	if _, err := svc.AdoptLanded("app"); err != nil {
		t.Fatalf("AdoptLanded: %v", err)
	}
	target, _ := svc.Target("app")
	if head := trim(mustGit(t, repo, "rev-parse", "HEAD")); head != target.SHA {
		t.Errorf("HEAD = %s after one adoption, want %s — all three landings at once",
			shortSHA(head), shortSHA(target.SHA))
	}
	if after := onlyAdoption(t, svc); after.Pending {
		t.Errorf("after one adoption: %+v; nothing should be left to adopt", after)
	}
}

// TestAdoptions_NothingLandedIsNotARow: a project the plane has never landed
// anything for has nothing to adopt, and a row saying so would be a row about
// nothing.
func TestAdoptions_NothingLandedIsNotARow(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil)
	if _, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "not landed"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	list, err := svc.Adoptions()
	if err != nil {
		t.Fatalf("Adoptions: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("Adoptions() = %+v, want none — nothing has landed for this project", list)
	}
}

// TestAdoptions_ADivergedBranchIsSaidRatherThanOffered. A branch carrying commits
// the target does not have cannot be wound forward, and the row says which and
// how to resolve it rather than presenting a button whose only answer is no.
func TestAdoptions_ADivergedBranchIsSaidRatherThanOffered(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := landedProject(t, repo, 1)

	// A commit on the checkout's branch that the landed target does not contain.
	if err := os.WriteFile(filepath.Join(repo, "local-only.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "add", "-A")
	mustGit(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-m", "local work")
	before := trim(mustGit(t, repo, "rev-parse", "HEAD"))

	a := onlyAdoption(t, svc)
	if !a.Diverged || a.Adoptable || a.Adopted {
		t.Fatalf("adoption = %+v; a diverged branch is neither up to date nor fast-forwardable", a)
	}
	if !strings.Contains(a.Note, "diverged") || !strings.Contains(a.Note, targetRefName) {
		t.Errorf("Note = %q; it should say the branch diverged and name the ref to merge", a.Note)
	}
	// And if it is asked for anyway — the state can change between a poll and a
	// click — it refuses and touches nothing.
	if _, err := svc.AdoptLanded("app"); err == nil {
		t.Error("adopting a diverged branch should be refused")
	}
	if after := trim(mustGit(t, repo, "rev-parse", "HEAD")); after != before {
		t.Errorf("HEAD moved despite divergence: %s → %s", shortSHA(before), shortSHA(after))
	}
}

// TestAdoptions_ABranchAheadOfTheTargetHasNothingToAdopt.
//
// The ordinary state of a checkout somebody is working in: it has every landed
// commit, and some of its own on top. There is nothing to take, so the row says
// ADOPTED and offers no action — a fast-forward here is one git would refuse,
// which is the dead end the Landed column exists to remove.
//
// The case needs its own test because "ahead" and "behind" are the same fact to
// anything that only asks whether HEAD equals the target. Telling them apart is
// one extra ancestry question, and getting it wrong puts a button on the one row
// whose answer can only be no.
func TestAdoptions_ABranchAheadOfTheTargetHasNothingToAdopt(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := landedProject(t, repo, 1)
	if _, err := svc.AdoptLanded("app"); err != nil {
		t.Fatalf("AdoptLanded: %v", err)
	}
	target, err := svc.Target("app")
	if err != nil {
		t.Fatalf("Target: %v", err)
	}

	// Work of their own, on top of what landed.
	if err := os.WriteFile(filepath.Join(repo, "mine.txt"), []byte("in progress"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "add", "-A")
	mustGit(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-m", "my own work")
	head := trim(mustGit(t, repo, "rev-parse", "HEAD"))
	if head == target.SHA {
		t.Fatal("precondition: the local commit should have carried the branch past the target")
	}

	a := onlyAdoption(t, svc)
	if !a.Adopted || a.Adoptable || a.Pending {
		t.Fatalf("adoption = %+v; a branch AHEAD of the target already has the landed work", a)
	}
	// NOT DIVERGED, which is the distinction the whole test is about: a diverged
	// branch is missing landed commits and this one is missing none.
	if a.Diverged {
		t.Errorf("adoption = %+v; a branch that CONTAINS the target has not diverged from it", a)
	}
	if a.Behind != 0 || len(a.Waiting) != 0 {
		t.Errorf("adoption = %+v; a branch that contains the target is behind by nothing and waiting on nothing", a)
	}
	if !strings.Contains(a.Note, "already has") || !strings.Contains(a.Note, "on top") {
		t.Errorf("Note = %q; it should say the branch has the landed commit AND carries work of its own on top",
			a.Note)
	}

	// AND THE ACTION AGREES WITH THE ROW.
	//
	// The row is a snapshot, so the click can arrive after it and `daedalus task
	// adopt <project>` has no row in front of it at all — it takes a project name
	// and runs. So the write is asked the same question and must give the same
	// answer: adopted, nothing moved, no remedy offered.
	//
	// This is what the two disagreed about. advanceCheckoutBranch asked only
	// whether HEAD was an ancestor of the target; ahead is not, so it refused with
	// "has diverged from the landed commit" and handed `git merge` — telling an
	// operator whose branch has everything that it has fallen behind, and offering
	// a remedy that would do nothing. The read has always been right about this
	// case; the write is what changed.
	res, err := svc.AdoptLanded("app")
	if err != nil {
		t.Fatalf("adopting a branch that is AHEAD of the target = %v; it has the landed work, which is success", err)
	}
	if !res.Adopted {
		t.Errorf("result = %+v; a branch that CONTAINS the landed commit is adopted", res)
	}
	if strings.Contains(res.Note, "diverged") {
		t.Errorf("Note = %q; a branch that contains every landed commit has not diverged from them", res.Note)
	}
	if strings.Contains(res.Note, "git merge") {
		t.Errorf("Note = %q; a branch with nothing missing must not be handed a merge that would do nothing",
			res.Note)
	}
	if after := trim(mustGit(t, repo, "rev-parse", "HEAD")); after != head {
		t.Errorf("HEAD moved on a branch that already had the work: %s → %s",
			shortSHA(head), shortSHA(after))
	}
}

// TestAdoptions_ADetachedHeadIsSaidRatherThanOffered. Two shapes, because a
// detached HEAD can be behind the landed commit or sitting on it, and the row has
// a different thing to say about each.
//
// Neither offers an action. A fast-forward needs a branch to move, and there
// isn't one — so the row says that, in words, rather than presenting a plate
// whose only possible answer is "there is no branch". Same rule as the diverged
// row above, reached from a different direction.
func TestAdoptions_ADetachedHeadIsSaidRatherThanOffered(t *testing.T) {
	repo := gitRepo(t)
	svc, _, ids := landedProject(t, repo, 1)
	target, err := svc.Target("app")
	if err != nil {
		t.Fatalf("Target: %v", err)
	}

	// --- BEHIND the landed commit, and with no branch to advance ---------------
	mustGit(t, repo, "checkout", "--detach", "HEAD")
	before := trim(mustGit(t, repo, "rev-parse", "HEAD"))

	a := onlyAdoption(t, svc)
	if a.Branch != "" {
		t.Errorf("Branch = %q, want empty — there is no branch on a detached HEAD", a.Branch)
	}
	if a.Adoptable || a.Adopted {
		t.Fatalf("adoption = %+v; a detached HEAD is neither up to date nor fast-forwardable", a)
	}
	// PENDING, though. There IS landed work this checkout does not have, and the
	// row exists to say so; only the ACTION is missing.
	if !a.Pending || a.Behind != 1 || len(a.Waiting) != 1 || a.Waiting[0] != ids[0] {
		t.Errorf("adoption = %+v; the gap is still real and still worth naming (want behind 1, waiting %s)",
			a, ids[0])
	}
	if !strings.Contains(a.Note, "detached HEAD") || !strings.Contains(a.Note, targetRefName) {
		t.Errorf("Note = %q; it should say the HEAD is detached and name the ref to merge", a.Note)
	}

	// Asked for anyway — a row is a snapshot and a click can arrive after it — the
	// refusal comes from advanceCheckoutBranch and nothing is touched.
	if _, err := svc.AdoptLanded("app"); err == nil {
		t.Error("adopting into a detached HEAD should be refused")
	}
	if after := trim(mustGit(t, repo, "rev-parse", "HEAD")); after != before {
		t.Errorf("HEAD moved despite there being no branch: %s → %s", shortSHA(before), shortSHA(after))
	}

	// --- ON the landed commit, still detached ---------------------------------
	//
	// Somebody checked the landed work out to look at it. They HAVE it, so the row
	// says so — and says it about "the checkout", because naming a branch that does
	// not exist is the wrong sentence in the other direction.
	mustGit(t, repo, "checkout", "--detach", target.SHA)

	at := onlyAdoption(t, svc)
	if !at.Adopted || at.Pending || at.Adoptable {
		t.Fatalf("adoption = %+v; a detached HEAD sitting on the landed commit has the work", at)
	}
	if !strings.Contains(at.Note, "detached HEAD") || !strings.Contains(at.Note, "already at") {
		t.Errorf("Note = %q; it should name the checkout rather than a branch, and say it is already there",
			at.Note)
	}
	if strings.Contains(at.Note, "behind") {
		t.Errorf("Note = %q; a checkout that is AT the landed commit is not behind it", at.Note)
	}
}

// TestAdopt_AnAgentMayOnlyPropose pins the tier. Moving a branch in somebody's
// working checkout is the one plane operation whose effect is felt outside the
// plane, which is exactly what a poisoned project document would reach for.
func TestAdopt_AnAgentMayOnlyPropose(t *testing.T) {
	repo := gitRepo(t)
	svc, store, _ := landedProject(t, repo, 1)
	before := trim(mustGit(t, repo, "rev-parse", "HEAD"))

	agent := svc.WithCaller(Agent())
	_, err := agent.AdoptLanded("app")
	var rej *RejectionError
	if !errors.As(err, &rej) || rej.Reason != ReasonProposalRecorded {
		t.Fatalf("agent AdoptLanded = %v, want a recorded proposal", err)
	}
	if after := trim(mustGit(t, repo, "rev-parse", "HEAD")); after != before {
		t.Fatalf("an agent moved a human's branch: %s → %s", shortSHA(before), shortSHA(after))
	}
	// Reading it is allowed — an agent that can see the gap can say why the human
	// it reports to cannot find the work it landed.
	if _, err := agent.Adoptions(); err != nil {
		t.Errorf("agent Adoptions() = %v, want a permitted read", err)
	}

	// A human confirms, and only then does the branch move — through the same
	// guarded operation, not a privileged back door.
	proposals, _ := store.ListProposals(ProposalPending)
	if len(proposals) != 1 {
		t.Fatalf("proposals = %d, want 1", len(proposals))
	}
	if _, err := svc.WithCaller(Human()).ResolveProposal(proposals[0].ID, true, "go on"); err != nil {
		t.Fatalf("human confirm: %v", err)
	}
	target, _ := svc.Target("app")
	if head := trim(mustGit(t, repo, "rev-parse", "HEAD")); head != target.SHA {
		t.Errorf("HEAD = %s after the confirmation, want the landed target %s",
			shortSHA(head), shortSHA(target.SHA))
	}
}

// TestAdopt_IsRecordedEitherWay. Moving a branch in a checkout the plane does not
// own is worth a line in the log, and so is declining to.
func TestAdopt_IsRecordedEitherWay(t *testing.T) {
	repo := gitRepo(t)
	svc, store, _ := landedProject(t, repo, 1)

	if _, err := svc.AdoptLanded("app"); err != nil {
		t.Fatalf("AdoptLanded: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "seed.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AdoptLanded("app"); err == nil {
		t.Fatal("precondition: the dirty tree should have been refused")
	}

	events, err := store.ListEventsFor("project", "app")
	if err != nil {
		t.Fatalf("ListEventsFor: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("recorded %d adoption events, want 2 (one move, one refusal): %+v", len(events), events)
	}
	if !strings.Contains(events[1].Note, "uncommitted changes") {
		t.Errorf("the refusal was recorded as %q; it should say why nothing moved", events[1].Note)
	}
}

// TestAdoptions_WaitingIsWhatTheBRANCHIsMissing, not everything the project has
// ever landed.
//
// Three tasks land and are adopted; a fourth lands after. The branch is now one
// commit behind holding one task's work, and a row that still named all four
// would be telling an operator that three landings they have already taken are
// waiting for them — which is exactly the shape a project with a long history
// takes: fifty landed, two waiting.
func TestAdoptions_WaitingIsWhatTheBranchIsMissing(t *testing.T) {
	repo := gitRepo(t)
	svc, _, first := landedProject(t, repo, 3)
	if _, err := svc.AdoptLanded("app"); err != nil {
		t.Fatalf("AdoptLanded: %v", err)
	}
	if a := onlyAdoption(t, svc); len(a.Waiting) != 0 {
		t.Errorf("Waiting = %v on an up-to-date branch; nothing is waiting", a.Waiting)
	}

	fourth := landOne(t, svc, "app", "landed after the adoption")

	a := onlyAdoption(t, svc)
	if a.Behind != 1 {
		t.Fatalf("behind = %d, want 1 — one landing since the branch was adopted", a.Behind)
	}
	if len(a.Waiting) != 1 || a.Waiting[0] != fourth {
		t.Errorf("Waiting = %v, want only %s — the three already adopted are not waiting for anybody (%v)",
			a.Waiting, fourth, first)
	}
}

// TestAdoptions_TwoProjectsOnOneRepositoryAreOneRow.
//
// Registering a repository twice is allowed (CanonicalRepoPath says why), and
// the two names then share one target, one branch and one fast-forward. Two rows
// would offer the same move twice with nothing to choose between them.
func TestAdoptions_TwoProjectsOnOneRepositoryAreOneRow(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo, "docs": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, StubVerifyRunner{Pass: true})
	fromApp := landOne(t, svc, "app", "work through app")
	fromDocs := landOne(t, svc, "docs", "work through docs")

	list, err := svc.Adoptions()
	if err != nil {
		t.Fatalf("Adoptions: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("Adoptions() = %d rows, want 1 — one repository, one branch, one move: %+v",
			len(list), list)
	}
	a := list[0]
	// Filed under the first name alphabetically, and NAMING THE OTHER: an
	// operator looking for what `docs` landed must be able to find it here.
	if a.Project != "app" || len(a.Projects) != 2 || a.Projects[1] != "docs" {
		t.Errorf("row = %+v; it should be filed under one name and name both", a)
	}
	if len(a.Waiting) != 2 {
		t.Errorf("Waiting = %v, want both landings (%s, %s) — they are in one branch's gap",
			a.Waiting, fromApp, fromDocs)
	}
	// And the one move takes both, whichever name it is asked for under.
	if _, err := svc.AdoptLanded("docs"); err != nil {
		t.Fatalf("AdoptLanded(docs): %v", err)
	}
	target, _ := svc.Target("app")
	if head := trim(mustGit(t, repo, "rev-parse", "HEAD")); head != target.SHA {
		t.Errorf("HEAD = %s, want the landed target %s", shortSHA(head), shortSHA(target.SHA))
	}
	if after := onlyAdoption(t, svc); after.Pending {
		t.Errorf("after adopting: %+v; the shared checkout has everything", after)
	}
}

// TestAdoptions_TwoWorktreesOfOneRepositoryAreTwoRows.
//
// The other half of the rule above, and the one that bites. Two projects
// registered on separate git WORKTREES of one repository share a target — it is
// keyed by the repository, deliberately — and have a branch and a HEAD each.
// Grouped by repository alone they would share a row, and its Adopt would move
// whichever checkout the row's name resolved to: a branch the row never
// described, while the other project's lag stayed invisible.
func TestAdoptions_TwoWorktreesOfOneRepositoryAreTwoRows(t *testing.T) {
	main := gitRepo(t)
	// A linked worktree on its own branch — the shape a person keeps a second
	// checkout of one repository in.
	linked := filepath.Join(t.TempDir(), "second")
	mustGit(t, main, "worktree", "add", "-b", "second", linked)

	svc, _, _ := newService(t, mapResolver{"app": main, "side": linked},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, StubVerifyRunner{Pass: true})
	landOne(t, svc, "app", "work in the main checkout")
	landOne(t, svc, "side", "work in the linked worktree")

	list, err := svc.Adoptions()
	if err != nil {
		t.Fatalf("Adoptions: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("Adoptions() = %d rows, want 2 — two working trees, two branches, two moves: %+v",
			len(list), list)
	}
	rows := map[string]Adoption{}
	for _, a := range list {
		if len(a.Projects) > 1 {
			t.Errorf("row %+v pools two worktrees; each row must describe the branch its own action moves", a)
		}
		rows[a.Project] = a
	}
	// EACH ROW NAMES ITS OWN BRANCH. That is the whole property: the row is what
	// the operator reads before pressing the button that moves it.
	if rows["side"].Branch != "second" {
		t.Errorf("the row for `side` names branch %q, want the linked worktree's own branch `second`",
			rows["side"].Branch)
	}
	if rows["app"].Branch == "second" {
		t.Errorf("the row for `app` names the OTHER worktree's branch: %+v", rows["app"])
	}

	// And adopting one moves that one. The other is left exactly where it was,
	// which is the failure the shared row produced.
	sideHeadBefore := trim(mustGit(t, linked, "rev-parse", "HEAD"))
	if _, err := svc.AdoptLanded("app"); err != nil {
		t.Fatalf("AdoptLanded(app): %v", err)
	}
	target, _ := svc.Target("app")
	if head := trim(mustGit(t, main, "rev-parse", "HEAD")); head != target.SHA {
		t.Errorf("the main checkout is at %s, want the landed target %s", shortSHA(head), shortSHA(target.SHA))
	}
	if head := trim(mustGit(t, linked, "rev-parse", "HEAD")); head != sideHeadBefore {
		t.Errorf("adopting `app` moved `side`'s branch too: %s → %s",
			shortSHA(sideHeadBefore), shortSHA(head))
	}
	// The row for the untouched worktree still says it has work waiting, rather
	// than having been cleared by somebody else's adoption.
	after, err := svc.Adoptions()
	if err != nil {
		t.Fatalf("Adoptions: %v", err)
	}
	for _, a := range after {
		if a.Project == "side" && !a.Pending {
			t.Errorf("`side` reads as up to date after only `app` was adopted: %+v", a)
		}
	}
}

// TestAdoptions_AnUnreadableCheckoutNamesTheProjectAndNotThePath.
//
// /adoptions is a read an agent may make, granted on the grounds that it carries
// a project name, a branch and two commit ids and no host layout. git's own
// messages quote the directory they ran in, so repeating one in the row would
// hand an agent the checkout's path by way of a fault — and a fault is exactly
// when nobody is looking.
func TestAdoptions_AnUnreadableCheckoutNamesTheProjectAndNotThePath(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := landedProject(t, repo, 1)
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}

	a := onlyAdoption(t, svc)
	if a.Unknown == "" {
		t.Fatalf("a checkout that is gone was reported as %+v; silence here reads as nothing to adopt", a)
	}
	if !strings.Contains(a.Unknown, "app") {
		t.Errorf("Unknown = %q; it should name the project the operator has to go and look at", a.Unknown)
	}
	// The path itself, and the leading directory it sits in — either would be a
	// map of the host.
	for _, field := range []string{a.Unknown, a.Note} {
		if strings.Contains(field, repo) || strings.Contains(field, filepath.Dir(repo)) {
			t.Errorf("the row carries the checkout's host path: %q", field)
		}
	}
}

// TestAdopt_IsRecordedOnTheTasksItCarried.
//
// The project row is the record of the operation; this is the record where
// anybody will look. `daedalus task events <id>` reads a Task's own log, and
// without this line the last thing that log says about a landed Task is that it
// landed on a ref nobody checks out.
func TestAdopt_IsRecordedOnTheTasksItCarried(t *testing.T) {
	repo := gitRepo(t)
	svc, store, ids := landedProject(t, repo, 2)

	if _, err := svc.AdoptLanded("app"); err != nil {
		t.Fatalf("AdoptLanded: %v", err)
	}
	for _, id := range ids {
		events, err := store.ListEventsForTask(id)
		if err != nil {
			t.Fatalf("ListEventsForTask(%s): %v", id, err)
		}
		last := events[len(events)-1]
		if !strings.Contains(last.Note, "adopt") || !strings.Contains(last.Note, "fast-forwarded") {
			t.Errorf("the last thing %s's log says is %q; it should say its work was adopted into a branch",
				id, last.Note)
		}
	}

	// A SECOND adoption carries nothing — the branch already has both — so it
	// writes nothing on the Tasks. The log records what happened to them, not
	// every time somebody pressed the button.
	before := countTaskEvents(t, store, ids[0])
	if _, err := svc.AdoptLanded("app"); err != nil {
		t.Fatalf("second AdoptLanded: %v", err)
	}
	if after := countTaskEvents(t, store, ids[0]); after != before {
		t.Errorf("%s picked up %d more events from an adoption that carried nothing",
			ids[0], after-before)
	}
}

func countTaskEvents(t *testing.T, store *Store, id string) int {
	t.Helper()
	events, err := store.ListEventsFor("task", id)
	if err != nil {
		t.Fatalf("ListEventsFor(task, %s): %v", id, err)
	}
	return len(events)
}
