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

// TestReverify_RefusesUnappealableRejections pins the trust boundary. The
// integrity gate exists to refuse a self-grading diff; a re-verification that
// could set it aside would let the same diff through on the second ask, which
// would make the gate advisory rather than structural.
func TestReverify_RefusesUnappealableRejections(t *testing.T) {
	sv := &sequenceVerifier{verdicts: []VerifyOutcome{{Passed: true}}}
	svc, store, task := dispatchToCandidate(t, "sneaky_test.go", sv)

	res, err := svc.VerifyTask(task.ID, VerifyRequest{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !res.GateTouched {
		t.Fatalf("precondition: expected the integrity gate to trip")
	}

	_, err = svc.ReverifyTask(task.ID, ReverifyRequest{})
	if err == nil {
		t.Fatal("re-verifying an integrity-gate rejection must be refused")
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
	if sv.calls() != 0 {
		t.Errorf("the verifier ran %d time(s) — a refusal must never reach it", sv.calls())
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
