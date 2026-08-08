// Copyright (C) 2026 Techdelight BV

package control

import (
	"errors"
	"strings"
	"testing"
)

// --- stale base (real temp repos) ---------------------------------------------

func TestIsStaleBase(t *testing.T) {
	repo := gitRepo(t)
	base, err := ReadHeadSHA(repo)
	if err != nil {
		t.Fatalf("ReadHeadSHA: %v", err)
	}

	stale, tip, err := IsStaleBase(repo, base)
	if err != nil {
		t.Fatalf("IsStaleBase: %v", err)
	}
	if stale {
		t.Errorf("base == tip should not be stale (tip %s)", tip)
	}

	// Someone else lands a commit on the project's target branch.
	moved := commitFile(t, repo, "other.txt", "someone else's work")
	stale, tip, err = IsStaleBase(repo, base)
	if err != nil {
		t.Fatalf("IsStaleBase after move: %v", err)
	}
	if !stale {
		t.Error("base should be stale once the project tip moves on")
	}
	if tip != moved {
		t.Errorf("tip = %s, want the new commit %s", tip, moved)
	}

	// An unreadable repo is an error, never a silent "not stale" — failing open
	// would quietly retire the whole check.
	if _, _, err := IsStaleBase(t.TempDir(), base); err == nil {
		t.Error("IsStaleBase on a non-repo should error, not report 'fresh'")
	}
}

func TestVerify_StaleBase_Rejected(t *testing.T) {
	repo := gitRepo(t)
	rv := &recordingVerifier{pass: true} // would pass — must never be consulted
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, rv)

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	// While the Job ran, the project's target tip moved on.
	newTip := commitFile(t, repo, "unrelated.txt", "landed meanwhile")

	res, err := svc.VerifyTask(task.ID)
	if err != nil {
		t.Fatalf("VerifyTask: %v", err)
	}
	if rv.called {
		t.Error("a stale-base candidate must be rejected BEFORE the verifier runs")
	}
	if res.Verified {
		t.Error("a stale-base candidate must not verify")
	}
	if res.Reason != ReasonStaleBase {
		t.Errorf("reason = %q, want %q", res.Reason, ReasonStaleBase)
	}
	if !strings.Contains(res.Detail, "rebase") {
		t.Errorf("the rejection should name the remedy, got %q", res.Detail)
	}
	got, _ := store.GetTask(task.ID)
	if got.State != StateRejected {
		t.Errorf("task state = %q, want rejected", got.State)
	}
	// The reason is on the transition event, machine-readable.
	events, _ := store.ListEventsFor("task", task.ID)
	last := events[len(events)-1]
	if last.Reason != ReasonStaleBase || last.Kind != EventRejection {
		t.Errorf("last event = %+v, want a rejection event carrying stale_base", last)
	}
	_ = newTip
}

// --- retry --------------------------------------------------------------------

// rejectedTask drives a task to `rejected` via a failing verifier and returns the
// service, store, and task.
func rejectedTask(t *testing.T, repo string, budget Budget) (*Service, *Store, Task) {
	t.Helper()
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, StubVerifyRunner{Pass: false})
	svc.SetBudgetSource(StaticBudget(budget))
	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "original objective"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if _, err := svc.VerifyTask(task.ID); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	got, _ := store.GetTask(task.ID)
	if got.State != StateRejected {
		t.Fatalf("precondition: task should be rejected, got %s", got.State)
	}
	return svc, store, got
}

func TestRetry_FreshJob_PreservesChain(t *testing.T) {
	repo := gitRepo(t)
	svc, store, task := rejectedTask(t, repo, Budget{MaxAttempts: 3, MaxReviewCycles: 3, Concurrency: 1})

	before, _ := store.ListJobsForTask(task.ID)
	if len(before) != 1 {
		t.Fatalf("precondition: want 1 job, got %d", len(before))
	}
	firstJob := before[0]

	res, err := svc.RetryTask(task.ID, RetryRequest{})
	if err != nil {
		t.Fatalf("RetryTask: %v", err)
	}
	if res.Attempt != 2 {
		t.Errorf("attempt = %d, want 2", res.Attempt)
	}
	if res.Attempts != 2 {
		t.Errorf("attempts used = %d, want 2", res.Attempts)
	}
	if res.Rebased {
		t.Error("a plain retry must not rebase")
	}

	after, _ := store.ListJobsForTask(task.ID)
	if len(after) != 2 {
		t.Fatalf("want 2 jobs after retry, got %d", len(after))
	}
	// The FIRST attempt is preserved verbatim — history is never overwritten.
	if after[0].ID != firstJob.ID || after[0].State != firstJob.State ||
		after[0].OutputSnapshot != firstJob.OutputSnapshot {
		t.Errorf("first attempt mutated by the retry: %+v → %+v", firstJob, after[0])
	}
	if after[1].ID == firstJob.ID {
		t.Error("retry must create a FRESH job, not reuse the old one")
	}
	// The new job produced its own candidate artifact.
	if res.Dispatch.Artifact == nil {
		t.Error("the retry attempt should have produced a candidate artifact")
	}
}

func TestRetry_WrongState_Refused(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)
	task, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"}) // planned
	if _, err := svc.RetryTask(task.ID, RetryRequest{}); err == nil {
		t.Error("retrying a planned (never-rejected) task should error")
	}
	if _, err := svc.RetryTask("T-404", RetryRequest{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("retry of an unknown task err = %v, want ErrNotFound", err)
	}
}

func TestRetry_ExhaustedAttempts_Refused(t *testing.T) {
	repo := gitRepo(t)
	// One attempt only: the first dispatch consumes it, so the retry is refused.
	svc, store, task := rejectedTask(t, repo, Budget{MaxAttempts: 1, MaxReviewCycles: 3, Concurrency: 1})

	_, err := svc.RetryTask(task.ID, RetryRequest{})
	var rej *RejectionError
	if !errors.As(err, &rej) {
		t.Fatalf("RetryTask err = %v, want *RejectionError", err)
	}
	if rej.Reason != ReasonAttemptsExhausted {
		t.Errorf("reason = %q, want %q", rej.Reason, ReasonAttemptsExhausted)
	}
	jobs, _ := store.ListJobsForTask(task.ID)
	if len(jobs) != 1 {
		t.Errorf("a refused retry must not create a Job: %d jobs", len(jobs))
	}
	// The refusal is recorded even though nothing moved.
	events, _ := store.ListEventsForTask(task.ID)
	if !hasEvent(events, EventBudget, ReasonAttemptsExhausted) {
		t.Error("the attempts-exhausted refusal should be in the event log")
	}
}

func TestRetry_Rebase_ReFreezesAtNewTip(t *testing.T) {
	repo := gitRepo(t)
	// A policy committed at the original base.
	commitFile(t, repo, ".daedalus/verify.json", `{"checks":["a"],"acceptanceGlobs":["**/*_test.go"]}`)

	svc, store, task := rejectedTask(t, repo, Budget{MaxAttempts: 3, MaxReviewCycles: 3, Concurrency: 1})
	oldBase, oldHash := task.BaseSHA, task.AcceptanceHash

	// The project tip moves on, carrying a different verify policy.
	newTip := commitFile(t, repo, ".daedalus/verify.json", `{"checks":["b"],"acceptanceGlobs":["**/*_test.go"]}`)

	res, err := svc.RetryTask(task.ID, RetryRequest{Rebase: true})
	if err != nil {
		t.Fatalf("RetryTask(rebase): %v", err)
	}
	if !res.Rebased {
		t.Error("expected the retry to report a rebase")
	}
	if res.BaseSHA != newTip {
		t.Errorf("retry base = %s, want the new tip %s", res.BaseSHA, newTip)
	}
	got, _ := store.GetTask(task.ID)
	if got.BaseSHA != newTip {
		t.Errorf("task base_sha = %s, want %s", got.BaseSHA, newTip)
	}
	if got.BaseSHA == oldBase {
		t.Error("rebase did not move the base")
	}
	// The acceptance oracle was re-frozen AT THE NEW BASE — a consequential act,
	// which is why --rebase is opt-in.
	wantPolicy, _ := ReadAcceptancePolicyAt(repo, newTip)
	if got.AcceptanceHash != wantPolicy.Hash() {
		t.Errorf("acceptance hash = %s, want the policy at the new base %s", got.AcceptanceHash, wantPolicy.Hash())
	}
	if got.AcceptanceHash == oldHash {
		t.Error("the acceptance hash should have changed with the new policy")
	}
	// The rebase is on the record.
	events, _ := store.ListEventsForTask(task.ID)
	if !hasNote(events, "rebase") {
		t.Error("the rebase should be recorded in the event log")
	}
}

func TestRetry_RebaseThenVerify_ClearsStaleBase(t *testing.T) {
	repo := gitRepo(t)
	rv := &recordingVerifier{pass: true}
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, rv)

	task, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	commitFile(t, repo, "meanwhile.txt", "landed")
	res, err := svc.VerifyTask(task.ID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Reason != ReasonStaleBase {
		t.Fatalf("precondition: want a stale_base rejection, got %q", res.Reason)
	}

	// The documented remedy: rebase + re-verify.
	if _, err := svc.RetryTask(task.ID, RetryRequest{Rebase: true}); err != nil {
		t.Fatalf("RetryTask(rebase): %v", err)
	}
	res2, err := svc.VerifyTask(task.ID)
	if err != nil {
		t.Fatalf("Verify after rebase: %v", err)
	}
	if !res2.Verified {
		t.Fatalf("after a rebase the candidate should verify, got reason %q (%s)", res2.Reason, res2.Detail)
	}
	got, _ := store.GetTask(task.ID)
	if got.State != StateVerified {
		t.Errorf("task state = %q, want verified", got.State)
	}
}

// --- replan -------------------------------------------------------------------

func TestReplan_RevisesObjectiveAndPreservesAttempts(t *testing.T) {
	repo := gitRepo(t)
	svc, store, task := rejectedTask(t, repo, Budget{MaxAttempts: 3, MaxReviewCycles: 3, Concurrency: 1})

	got, err := svc.ReplanTask(task.ID, ReplanRequest{Objective: "revised objective"})
	if err != nil {
		t.Fatalf("ReplanTask: %v", err)
	}
	if got.State != StatePlanned {
		t.Errorf("state = %q, want planned", got.State)
	}
	if got.Objective != "revised objective" {
		t.Errorf("objective = %q, want the revision", got.Objective)
	}
	// Objective and state moved together (no window where planned carries the
	// stale objective).
	reread, _ := store.GetTask(task.ID)
	if reread.Objective != "revised objective" || reread.State != StatePlanned {
		t.Errorf("stored task = %+v, want the revised objective in planned", reread)
	}
	// The attempt counter is NOT reset: a replan cannot buy more attempts.
	jobs, _ := store.ListJobsForTask(task.ID)
	if len(jobs) != 1 {
		t.Errorf("job chain = %d jobs, want the 1 preserved attempt", len(jobs))
	}
	// The old objective is preserved in the log next to the new one.
	events, _ := store.ListEventsForTask(task.ID)
	if !hasNote(events, "original objective") || !hasNote(events, "revised objective") {
		t.Error("the replan event should record both the old and the new objective")
	}
	// It is dispatchable again from planned.
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("dispatch after replan: %v", err)
	}
}

func TestReplan_Guards(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)
	task, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})

	if _, err := svc.ReplanTask(task.ID, ReplanRequest{Objective: "y"}); err == nil {
		t.Error("replanning a planned (never-rejected) task should error")
	}
	if _, err := svc.ReplanTask(task.ID, ReplanRequest{}); err == nil {
		t.Error("replan without an objective should error")
	}
}

func TestReplan_ExhaustedAttempts_Refused(t *testing.T) {
	repo := gitRepo(t)
	svc, store, task := rejectedTask(t, repo, Budget{MaxAttempts: 1, MaxReviewCycles: 3, Concurrency: 1})

	_, err := svc.ReplanTask(task.ID, ReplanRequest{Objective: "revised"})
	var rej *RejectionError
	if !errors.As(err, &rej) || rej.Reason != ReasonAttemptsExhausted {
		t.Fatalf("ReplanTask err = %v, want an attempts_exhausted rejection", err)
	}
	// Refused, so the objective and state are untouched.
	got, _ := store.GetTask(task.ID)
	if got.Objective != "original objective" || got.State != StateRejected {
		t.Errorf("a refused replan changed the task: %+v", got)
	}
}

// --- helpers ------------------------------------------------------------------

func hasEvent(events []Event, kind string, reason RejectionReason) bool {
	for _, e := range events {
		if e.Kind == kind && e.Reason == reason {
			return true
		}
	}
	return false
}

func hasNote(events []Event, substr string) bool {
	for _, e := range events {
		if strings.Contains(e.Note, substr) {
			return true
		}
	}
	return false
}
