// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sequenceVerifier returns a different verdict on each call, which is the shape
// re-verification exists for: the artifact is fixed, the HARNESS changed. A
// verifier with one fixed answer could not tell a genuine re-grading apart from
// a cached one.
type sequenceVerifier struct {
	verdicts []VerifyOutcome
	specs    []VerifySpec
}

func (s *sequenceVerifier) Verify(_ context.Context, spec VerifySpec) VerifyOutcome {
	s.specs = append(s.specs, spec)
	if len(s.specs) <= len(s.verdicts) {
		return s.verdicts[len(s.specs)-1]
	}
	return s.verdicts[len(s.verdicts)-1]
}

func (s *sequenceVerifier) calls() int { return len(s.specs) }

// TestReverify_ReplayGradesTheSameArtifactWithoutANewJob is the milestone's
// central claim: a wrong verdict is recoverable without re-running the work.
//
// It asserts the two halves separately, because either alone would be
// misleading. That the second verdict differs proves a real grading happened;
// that the Job count and the graded commit are unchanged proves it happened
// against the SAME artifact rather than a freshly produced one.
func TestReverify_ReplayGradesTheSameArtifactWithoutANewJob(t *testing.T) {
	sv := &sequenceVerifier{verdicts: []VerifyOutcome{
		{Passed: false, Detail: "check never ran — verifier bypassed the entrypoint"},
		{Passed: true, Detail: "harness fixed; check actually executed"},
	}}
	svc, store, task := dispatchToCandidate(t, "AGENT_RAN.txt", sv)

	if _, err := svc.VerifyTask(task.ID, VerifyRequest{}); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	before, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.State != StateRejected {
		t.Fatalf("precondition: want rejected after a failing verify, got %s", before.State)
	}
	jobsBefore, err := store.CountJobsForTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}

	res, err := svc.ReverifyTask(task.ID, ReverifyRequest{})
	if err != nil {
		t.Fatalf("ReverifyTask: %v", err)
	}

	if !res.Verify.Verified {
		t.Errorf("re-verification should have reached the second verdict, got %+v", res.Verify)
	}
	if got, _ := store.GetTask(task.ID); got.State != StateVerified {
		t.Errorf("task state = %s, want verified", got.State)
	}
	if res.PreviousReason != ReasonVerifyFailed {
		t.Errorf("PreviousReason = %q, want %q — the verdict being set aside must be reported",
			res.PreviousReason, ReasonVerifyFailed)
	}

	// No new Job: this is the whole point of the feature.
	jobsAfter, err := store.CountJobsForTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if jobsAfter != jobsBefore {
		t.Errorf("job count %d → %d: re-verification must not dispatch a new Job", jobsBefore, jobsAfter)
	}

	// The SAME commit was graded both times.
	if sv.calls() != 2 {
		t.Fatalf("verifier called %d times, want 2", sv.calls())
	}
	if sv.specs[0].HeadSHA != sv.specs[1].HeadSHA {
		t.Errorf("graded head %s then %s — re-verification must grade the same artifact",
			sv.specs[0].HeadSHA, sv.specs[1].HeadSHA)
	}
	if sv.specs[0].JobID != sv.specs[1].JobID {
		t.Errorf("graded job %s then %s — re-verification must reuse the Job", sv.specs[0].JobID, sv.specs[1].JobID)
	}
}

// TestReverify_RefusesUnappealableRejections pins the trust boundary.
//
// An integrity rejection now means the frozen oracle could not be established,
// so nothing was graded. Re-grading would produce the same nothing, and letting
// it through on a second ask is exactly what would make the boundary advisory
// rather than structural — the same argument as when this reason meant "the diff
// touched an acceptance file", which it no longer does.
func TestReverify_RefusesUnappealableRejections(t *testing.T) {
	sv := &sequenceVerifier{verdicts: []VerifyOutcome{
		{Passed: false, OracleUnrestorable: true, Detail: "could not restore the frozen acceptance files"},
		{Passed: true},
	}}
	svc, store, task := dispatchToCandidate(t, "AGENT_RAN.txt", sv)

	res, err := svc.VerifyTask(task.ID, VerifyRequest{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !res.GateTouched {
		t.Fatalf("precondition: expected an unrestorable oracle to be reported as one")
	}

	_, err = svc.ReverifyTask(task.ID, ReverifyRequest{})
	if err == nil {
		t.Fatal("re-verifying an integrity rejection must be refused")
	}
	var rej *RejectionError
	if !errors.As(err, &rej) {
		t.Fatalf("want a typed rejection, got %T: %v", err, err)
	}
	if rej.Reason != ReasonUnappealable {
		t.Errorf("reason = %q, want %q", rej.Reason, ReasonUnappealable)
	}
	if got, _ := store.GetTask(task.ID); got.State != StateRejected {
		t.Errorf("a refused re-verification must leave the task rejected, got %s", got.State)
	}
	// One call — the verify that produced the verdict. The refusal must not reach
	// the verifier a second time.
	if sv.calls() != 1 {
		t.Errorf("the verifier ran %d time(s); the refusal must never reach it again", sv.calls())
	}
}

// TestReverify_RefusesWhenTheArtifactIsGone: rejection removes a Job's worktree
// but never its branch, so the commit normally survives. If someone deleted it
// there is nothing to re-grade, and saying "failed" would be a false verdict
// about work that may have been fine — in an append-only log.
func TestReverify_RefusesWhenTheArtifactIsGone(t *testing.T) {
	sv := &sequenceVerifier{verdicts: []VerifyOutcome{{Passed: false, Detail: "nope"}, {Passed: true}}}
	repo := gitRepo(t)
	runner := StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "AGENT_RAN.txt"}
	svc, _, store := newService(t, mapResolver{"app": repo}, runner, nil, sv)
	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "do work"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifyTask(task.ID, VerifyRequest{}); err != nil {
		t.Fatal(err)
	}
	job, ok, err := svc.jobInState(task.ID, StateRejected)
	if err != nil || !ok {
		t.Fatalf("precondition: want a rejected job, ok=%v err=%v", ok, err)
	}

	// Destroy the artifact the way a human would: delete the branch that keeps it
	// reachable, then expire the reflog so nothing else holds it.
	git(t, repo, "branch", "-D", BranchName(task.ID, job.ID))
	git(t, repo, "reflog", "expire", "--expire=now", "--all")
	git(t, repo, "gc", "--prune=now")

	_, err = svc.ReverifyTask(task.ID, ReverifyRequest{})
	if err == nil {
		t.Fatal("re-verifying an unreachable artifact must be refused")
	}
	var rej *RejectionError
	if !errors.As(err, &rej) {
		t.Fatalf("want a typed rejection, got %T: %v", err, err)
	}
	if rej.Reason != ReasonArtifactGone {
		t.Errorf("reason = %q, want %q", rej.Reason, ReasonArtifactGone)
	}
	if got, _ := store.GetTask(task.ID); got.State != StateRejected {
		t.Errorf("a refused re-verification must leave the task rejected, got %s", got.State)
	}
}

// TestReverify_RefusesFromANonRejectedState — re-verification is a correction to
// a verdict, so there must BE one.
func TestReverify_RefusesFromANonRejectedState(t *testing.T) {
	sv := &sequenceVerifier{verdicts: []VerifyOutcome{{Passed: true}}}
	svc, _, task := dispatchToCandidate(t, "AGENT_RAN.txt", sv)

	// Still `candidate`: never verified, so nothing has been decided.
	if _, err := svc.ReverifyTask(task.ID, ReverifyRequest{}); !errors.Is(err, ErrWrongState) {
		t.Errorf("re-verify from candidate: err = %v, want ErrWrongState", err)
	}
}

// TestReverify_BudgetAccounting is the asymmetry argued for in
// Store.CountReviewCycles, asserted in both directions.
//
// A replay is not charged: the previous verdict came from a harness that never
// judged the artifact, and a defect in our own grading must not consume the
// operator's budget. An amended re-grade IS charged: the oracle changed, and a
// real grading happened against a real policy.
func TestReverify_BudgetAccounting(t *testing.T) {
	t.Run("a replay does not consume a review cycle", func(t *testing.T) {
		sv := &sequenceVerifier{verdicts: []VerifyOutcome{{Passed: false}, {Passed: true}}}
		svc, store, task := dispatchToCandidate(t, "AGENT_RAN.txt", sv)

		if _, err := svc.VerifyTask(task.ID, VerifyRequest{}); err != nil {
			t.Fatal(err)
		}
		spent, err := store.CountReviewCycles(task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if spent != 1 {
			t.Fatalf("precondition: one verify should spend one cycle, got %d", spent)
		}

		if _, err := svc.ReverifyTask(task.ID, ReverifyRequest{}); err != nil {
			t.Fatalf("ReverifyTask: %v", err)
		}
		after, err := store.CountReviewCycles(task.ID)
		if err != nil {
			t.Fatal(err)
		}
		// Two verifications have now run, but only one judged the artifact.
		if after != 1 {
			t.Errorf("review cycles = %d after a replay, want 1 — a harness fault must not "+
				"spend the operator's budget", after)
		}
	})

	t.Run("attempts are never consumed", func(t *testing.T) {
		sv := &sequenceVerifier{verdicts: []VerifyOutcome{{Passed: false}, {Passed: true}}}
		svc, store, task := dispatchToCandidate(t, "AGENT_RAN.txt", sv)
		if _, err := svc.VerifyTask(task.ID, VerifyRequest{}); err != nil {
			t.Fatal(err)
		}
		before, _ := store.CountJobsForTask(task.ID)
		if _, err := svc.ReverifyTask(task.ID, ReverifyRequest{}); err != nil {
			t.Fatal(err)
		}
		after, _ := store.CountJobsForTask(task.ID)
		if before != after {
			t.Errorf("attempts %d → %d: re-verification creates no Job, so it can spend no attempt",
				before, after)
		}
	})
}

// TestReverify_AmendedRefreezesThePolicyAndRecordsTheLineage covers the case
// that motivated the milestone: the ORACLE was wrong, so correcting it moves the
// project's target, which makes the artifact's base stale. A replay cannot help
// there; the artifact has to be re-pinned to the corrected policy.
//
// The lineage assertion is the point of the mode. A verdict produced under a
// policy amended after the artifact existed is weaker than one produced under
// the policy the artifact faced, and the event log is the only place that
// difference survives.
func TestReverify_AmendedRefreezesThePolicyAndRecordsTheLineage(t *testing.T) {
	sv := &sequenceVerifier{verdicts: []VerifyOutcome{{Passed: false, Detail: "bad oracle"}, {Passed: true}}}
	repo := gitRepo(t)
	runner := StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "AGENT_RAN.txt"}
	svc, _, store := newService(t, mapResolver{"app": repo}, runner, nil, sv)
	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "do work"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifyTask(task.ID, VerifyRequest{}); err != nil {
		t.Fatal(err)
	}
	rejected, _ := store.GetTask(task.ID)
	oldHash, oldBase := rejected.AcceptanceHash, rejected.BaseSHA

	// Fix the oracle: commit a policy the artifact never faced, and move the
	// plane-owned target onto it. A benign filename, so the integrity gate has
	// nothing to catch when the artifact is diffed against the new base.
	if err := os.MkdirAll(filepath.Join(repo, ".daedalus"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".daedalus", "verify.json"),
		[]byte(`{"checks":["true"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-m", "fix the acceptance policy")
	if _, err := svc.SyncTarget("app"); err != nil {
		t.Fatalf("SyncTarget: %v", err)
	}

	res, err := svc.ReverifyTask(task.ID, ReverifyRequest{Amended: true})
	if err != nil {
		t.Fatalf("ReverifyTask(amended): %v", err)
	}
	if !res.Rebased {
		t.Error("an amended re-verify against a moved target must rebase")
	}
	if !res.Verify.Verified {
		t.Errorf("expected the corrected policy to verify, got %+v", res.Verify)
	}

	after, _ := store.GetTask(task.ID)
	if after.BaseSHA == oldBase {
		t.Error("base_sha should have moved to the corrected target")
	}
	if after.AcceptanceHash == oldHash {
		t.Error("the acceptance policy should have been re-frozen at the new base")
	}

	// The record: both the amendment and the hash lineage must be findable.
	events, err := store.ListEventsForTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sawReverify, sawLineage bool
	for _, e := range events {
		if e.Kind == EventReverify {
			sawReverify = true
		}
		if strings.Contains(e.Note, shortHash(oldHash)) && strings.Contains(e.Note, shortHash(after.AcceptanceHash)) {
			sawLineage = true
		}
	}
	if !sawReverify {
		t.Error("no reverify event was logged — the re-grading must be answerable later")
	}
	if !sawLineage {
		t.Errorf("no event records the policy lineage %s → %s; a verdict under an amended "+
			"oracle must stay visibly weaker", shortHash(oldHash), shortHash(after.AcceptanceHash))
	}
}

// TestReverify_AmendedConsumesAReviewCycle is the other half of the accounting
// asymmetry: the oracle changed, so a real grading happened and it is charged.
func TestReverify_AmendedConsumesAReviewCycle(t *testing.T) {
	sv := &sequenceVerifier{verdicts: []VerifyOutcome{{Passed: false}, {Passed: true}}}
	repo := gitRepo(t)
	runner := StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "AGENT_RAN.txt"}
	svc, _, store := newService(t, mapResolver{"app": repo}, runner, nil, sv)
	task, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "do work"})
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifyTask(task.ID, VerifyRequest{}); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(repo, "policy-note.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-m", "move the target")
	if _, err := svc.SyncTarget("app"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReverifyTask(task.ID, ReverifyRequest{Amended: true}); err != nil {
		t.Fatalf("ReverifyTask(amended): %v", err)
	}

	spent, err := store.CountReviewCycles(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if spent != 2 {
		t.Errorf("review cycles = %d, want 2 — an amended re-grade runs a real oracle and is charged", spent)
	}
}

// TestReplan_CanRebaseInOneStep is #84: replan corrected the instruction and left
// the base alone; retry moved the base and carried the old instruction; and
// neither could be chained, because both refuse from any state but `rejected`.
//
// A Task asked the wrong question against a tree that has since moved on
// therefore had no door at all — the advice was to abandon it and create a new
// one, which works and quietly discards the history, the reviews, and every
// recorded reason the first attempt was wrong.
func TestReplan_CanRebaseInOneStep(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil,
		StubVerifyRunner{Pass: false, Detail: "the oracle said no"})

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "the wrong question"})
	if err != nil {
		t.Fatal(err)
	}
	originalBase, originalHash := task.BaseSHA, task.AcceptanceHash
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifyTask(task.ID, VerifyRequest{}); err != nil {
		t.Fatal(err)
	}

	// The project moves on, and the plane's target with it — which is what makes
	// the Task's base stale. It also gains an acceptance policy, so the re-freeze
	// below has something to re-freeze TO: with no `.daedalus/verify.json` at
	// either commit the default policy applies to both and an unchanged hash is
	// the correct answer, which would make the assertion prove nothing.
	writeFileForTest(t, repo, "moved-on.txt", "the world changed")
	if err := os.MkdirAll(filepath.Join(repo, ".daedalus"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileForTest(t, repo, ".daedalus/verify.json", `{"checks":["true"]}`)
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-m", "the world changed")
	if _, err := svc.SyncTarget("app"); err != nil {
		t.Fatalf("SyncTarget: %v", err)
	}

	// One command: new instruction AND a new base.
	replanned, err := svc.ReplanTask(task.ID, ReplanRequest{
		Objective: "the right question", Rebase: true,
	})
	if err != nil {
		t.Fatalf("ReplanTask --rebase: %v", err)
	}
	if replanned.Objective != "the right question" {
		t.Errorf("objective = %q, want it replaced", replanned.Objective)
	}
	if replanned.BaseSHA == originalBase {
		t.Error("base_sha is unchanged; the rebase half did nothing")
	}
	if replanned.State != StatePlanned {
		t.Errorf("state = %q, want planned", replanned.State)
	}
	// Re-pinning re-freezes the acceptance oracle at the new base — the property
	// that makes this a governance act rather than a wording change, and the
	// reason it reuses retry's helper rather than reimplementing it.
	if replanned.AcceptanceHash == originalHash {
		t.Error("the acceptance policy was not re-frozen at the new base")
	}
	// And the lineage is on the record, because a policy amended after an attempt
	// existed is a fact somebody will need later.
	events, _ := store.ListEventsForTask(task.ID)
	found := false
	for _, e := range events {
		if strings.Contains(e.Note, "re-pinned to") || strings.Contains(e.Note, "acceptance policy re-frozen") {
			found = true
		}
	}
	if !found {
		t.Error("the rebase is not visible in the event log")
	}
}

// Without --rebase, replan leaves the base exactly where it was. The default has
// to stay unchanged: adopting a newer oracle is opt-in by name on every path
// that can do it.
func TestReplan_WithoutRebaseLeavesTheBaseAlone(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil,
		StubVerifyRunner{Pass: false})

	task, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	base := task.BaseSHA
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifyTask(task.ID, VerifyRequest{}); err != nil {
		t.Fatal(err)
	}
	writeFileForTest(t, repo, "moved-on.txt", "changed")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-m", "changed")
	if _, err := svc.SyncTarget("app"); err != nil {
		t.Fatal(err)
	}

	replanned, err := svc.ReplanTask(task.ID, ReplanRequest{Objective: "y"})
	if err != nil {
		t.Fatalf("ReplanTask: %v", err)
	}
	if replanned.BaseSHA != base {
		t.Errorf("base moved to %s without --rebase; re-pinning must stay opt-in", replanned.BaseSHA)
	}
}

// TestReplan_WorksOnAnUngradedCandidate.
//
// Until the candidate → planned edge existed, an operator who realised the
// INSTRUCTION was wrong while the work sat ungraded had to run a verification
// they did not care about — purely to reach the `rejected` state the ladder
// opens from. Spending a review cycle to earn permission to say "I asked for the
// wrong thing" is a toll, not a safeguard.
func TestReplan_WorksOnAnUngradedCandidate(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil,
		StubVerifyRunner{Pass: true})

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "push it somewhere"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetTask(task.ID)
	if got.State != StateCandidate {
		t.Fatalf("state = %q, want candidate", got.State)
	}
	cyclesBefore, _ := store.CountReviewCycles(task.ID)

	replanned, err := svc.ReplanTask(task.ID, ReplanRequest{Objective: "prepare it on a branch"})
	if err != nil {
		t.Fatalf("replanning an ungraded candidate = %v, want it allowed", err)
	}
	if replanned.State != StatePlanned || replanned.Objective != "prepare it on a branch" {
		t.Errorf("task = %+v, want planned with the new objective", replanned)
	}
	// No verification was run to earn the right to do it.
	if after, _ := store.CountReviewCycles(task.ID); after != cyclesBefore {
		t.Errorf("review cycles went %d → %d; replanning must not cost a grading",
			cyclesBefore, after)
	}

	// The ungraded attempt is SET ASIDE rather than left live: a Job in
	// `candidate` under a `planned` Task is an attempt nothing will ever move,
	// and candidateJob would keep offering it to a verify about a different
	// objective.
	jobs, _ := store.ListJobsForTask(task.ID)
	if len(jobs) != 1 {
		t.Fatalf("%d jobs, want 1", len(jobs))
	}
	if jobs[0].State != StateRejected {
		t.Errorf("job state = %q, want it set aside as rejected", jobs[0].State)
	}
	if _, ok, _ := svc.candidateJob(task.ID); ok {
		t.Error("a candidate job survived the replan; a later verify would grade the wrong objective")
	}

	// And the new objective can actually run.
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Errorf("dispatch after replanning a candidate: %v", err)
	}
}

// A VACUOUS PASS HAS TO BE REACHABLE, and today the route is through `reject`.
//
// Reverify exists because "a verdict can be wrong for reasons that say nothing
// about the work" — but it accepts only `rejected`, so it can correct a wrong
// NO and not a wrong YES. A task verified by the built-in default policy (the
// project committed no .daedalus/verify.json, so `daedalus docs lint` graded its
// documents) sits at `verified`, one command from being integrated, and cannot
// be re-graded against the real policy once one exists.
//
// The escape is a human rejection first. This pins that it works end to end,
// because the operator who needs it is holding a task they cannot otherwise
// touch — and because `approval_rejected` being appealable is what makes it
// possible, which is a property of a table two files away.
func TestReverify_AVerifiedTaskIsReachableThroughReject(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil, StubVerifyRunner{Pass: true})

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if _, err := svc.VerifyTask(task.ID, VerifyRequest{}); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if cur, _ := store.GetTask(task.ID); cur.State != StateVerified {
		t.Fatalf("task state = %q, want verified — the fixture needs the stuck state", cur.State)
	}

	// The state the operator is actually in.
	if _, err := svc.ReverifyTask(task.ID, ReverifyRequest{Amended: true}); err == nil {
		t.Fatal("reverify accepted a verified task; this test is pinning the opposite")
	}

	// The way out: say no at the gate, then re-grade.
	if _, err := svc.RejectApproval(task.ID, "graded by the default policy, not the project's"); err != nil {
		t.Fatalf("reject from verified: %v", err)
	}
	if cur, _ := store.GetTask(task.ID); cur.State != StateRejected {
		t.Fatalf("task state = %q, want rejected", cur.State)
	}
	res, err := svc.ReverifyTask(task.ID, ReverifyRequest{Amended: true})
	if err != nil {
		t.Fatalf("reverify after a human rejection: %v — the route out of a vacuous pass is closed", err)
	}
	if !res.Verify.Verified {
		t.Errorf("the re-grade did not verify: %+v", res.Verify)
	}
	// What keeps this route open: the reason a human rejection leaves behind must
	// never be unappealable. A gate rejection is a statement about the DECISION,
	// not a finding about the artifact — unlike the integrity gate and the
	// null-agent floor, which are the two things reverify must not let anybody
	// appeal. If either of these stops holding, the only way out of a vacuous
	// pass closes silently.
	if unappealableReasons[ReasonApprovalRejected] {
		t.Error("approval_rejected became unappealable — the only route out of a vacuous pass is now closed")
	}
	if unappealableReasons[res.PreviousReason] {
		t.Errorf("the verdict this set aside (%q) is unappealable, so the route is closed", res.PreviousReason)
	}
}

// EVERY OPERATION THAT ADOPTS A NEWER ORACLE REPORTS ADOPTING NO ORACLE.
//
// `retry --rebase`, `replan --rebase` and `reverify --amended` all re-freeze the
// acceptance policy at the project's target, and all three go through
// rebaseTaskToTip. Re-freezing onto a commit with no .daedalus/verify.json
// adopts the built-in default, which grades DOCUMENTS — and looks exactly like
// re-freezing onto a real policy. Derived in the one shared helper so the three
// cannot drift; asserted through two of them so the plumbing is real.
func TestRebase_ReportsWhenTheNewBaseCarriesNoPolicy(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil, StubVerifyRunner{Pass: true})

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	// Move the target on, with no policy at the new tip — the shape of a project
	// that has simply never committed one.
	if err := os.WriteFile(filepath.Join(repo, "moved.txt"), []byte("on\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "moved.txt")
	git(t, repo, "commit", "-m", "the target moves")
	if _, err := svc.SyncTarget("app"); err != nil {
		t.Fatalf("SyncTarget: %v", err)
	}
	toRejected(t, svc, store, task.ID)

	res, err := svc.ReverifyTask(task.ID, ReverifyRequest{Amended: true})
	if err != nil {
		t.Fatalf("Reverify: %v", err)
	}
	if !res.Rebased {
		t.Fatal("the fixture did not rebase; nothing is being tested")
	}
	if !res.DefaultPolicy {
		t.Error("reverify --amended re-froze onto a base with no verify.json and said nothing — " +
			"the verdict is about documents and the operator cannot tell")
	}
	// The record carries it too, for whoever reads this later.
	events, _ := store.ListEventsForTask(task.ID)
	var noted bool
	for _, e := range events {
		if strings.Contains(e.Note, "BUILT-IN DEFAULT") {
			noted = true
		}
	}
	if !noted {
		t.Error("nothing in the event log says the oracle adopted was the built-in default")
	}

	// And a base that DOES carry one reports nothing — a warning that always
	// fires is a warning nobody reads.
	if err := os.MkdirAll(filepath.Join(repo, ".daedalus"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".daedalus", "verify.json"),
		[]byte(`{"checks":["true"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", ".daedalus/verify.json")
	git(t, repo, "commit", "-m", "declare the policy")
	if _, err := svc.SyncTarget("app"); err != nil {
		t.Fatalf("SyncTarget: %v", err)
	}
	toRejected(t, svc, store, task.ID)
	res2, err := svc.ReverifyTask(task.ID, ReverifyRequest{Amended: true})
	if err != nil {
		t.Fatalf("Reverify: %v", err)
	}
	if res2.DefaultPolicy {
		t.Error("a base that carries a policy was reported as carrying none")
	}
}

// toRejected drives a task to `rejected`, whatever it is now — the only state
// reverify accepts. Written as a helper because a test about POLICY should not
// be a test about the ladder.
func toRejected(t *testing.T, svc *Service, store *Store, id string) {
	t.Helper()
	for i := 0; i < 3; i++ {
		cur, err := store.GetTask(id)
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		switch cur.State {
		case StateRejected:
			return
		case StateVerified, StateApprovalRequired:
			if _, err := svc.RejectApproval(id, "re-grade it"); err != nil {
				t.Fatalf("Reject from %s: %v", cur.State, err)
			}
		case StateCandidate:
			if _, err := svc.VerifyTask(id, VerifyRequest{}); err != nil {
				t.Fatalf("Verify: %v", err)
			}
		default:
			t.Fatalf("cannot drive %s to rejected", cur.State)
		}
	}
	t.Fatalf("task %s never reached rejected", id)
}
