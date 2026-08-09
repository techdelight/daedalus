// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// --- stale base (real temp repos) ---------------------------------------------

// TestStaleAgainstTarget: staleness is now measured against the PLANE-OWNED
// integration target, not the project checkout's HEAD. A developer's (or an
// agent's) commits to a branch no longer make a candidate stale, because they are
// not what the plane will integrate onto — only a completed integration moves the
// trunk.
func TestStaleAgainstTarget(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// The Task is based on the adopted target, so it starts fresh.
	stale, tip, err := svc.staleAgainstTarget(task)
	if err != nil {
		t.Fatalf("staleAgainstTarget: %v", err)
	}
	if stale {
		t.Errorf("a task based on the current target should not be stale (tip %s)", tip)
	}

	// A commit landing on the repo's BRANCH does not make it stale any more —
	// that is the Sprint-59 behaviour change and the point of the plane-owned ref.
	commitFile(t, repo, "developer.txt", "landed out of band")
	stale, _, err = svc.staleAgainstTarget(task)
	if err != nil {
		t.Fatalf("staleAgainstTarget after a branch commit: %v", err)
	}
	if stale {
		t.Error("a commit on the project branch must NOT make a candidate stale — the plane's trunk is its own target")
	}

	// Advancing the TARGET does, because that is a completed integration.
	landed := commitFile(t, repo, "landed.txt", "another task integrated")
	if _, err := store.AdvanceTarget(repoKey(t, repo), task.BaseSHA, landed, "test: simulate an integration"); err != nil {
		t.Fatalf("AdvanceTarget: %v", err)
	}
	stale, tip, err = svc.staleAgainstTarget(task)
	if err != nil {
		t.Fatalf("staleAgainstTarget after an integration: %v", err)
	}
	if !stale {
		t.Error("a task based on a superseded target should be stale")
	}
	if tip != landed {
		t.Errorf("tip = %s, want the new target %s", tip, landed)
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
	// While the Job ran, another integration landed and advanced the plane-owned
	// target. (A mere commit on the repo's branch would NOT do this — see
	// TestStaleAgainstTarget.)
	newTip := commitFile(t, repo, "unrelated.txt", "landed meanwhile")
	if _, err := store.AdvanceTarget(repoKey(t, repo), task.BaseSHA, newTip, "test: another integration landed"); err != nil {
		t.Fatalf("AdvanceTarget: %v", err)
	}

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

	// Another integration lands, advancing the PLANE-OWNED target onto a commit
	// carrying a different verify policy. (A bare branch commit would not move the
	// target — that is the Sprint-59 fix.)
	newTip := commitFile(t, repo, ".daedalus/verify.json", `{"checks":["b"],"acceptanceGlobs":["**/*_test.go"]}`)
	if _, err := store.AdvanceTarget(repoKey(t, repo), oldBase, newTip, "test: another integration landed"); err != nil {
		t.Fatalf("AdvanceTarget: %v", err)
	}

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
	landed := commitFile(t, repo, "meanwhile.txt", "landed")
	if _, err := store.AdvanceTarget(repoKey(t, repo), task.BaseSHA, landed, "test: another integration landed"); err != nil {
		t.Fatalf("AdvanceTarget: %v", err)
	}
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

// --- audit regressions (Sprint 58 follow-up) ---------------------------------

// TestVerify_StrandedVerifying_IsRecovered is the regression for "a Task wedges
// permanently in `verifying`". If a verification never reaches a verdict — a
// daemon crash, an aborted verifier — the Task must not be left in a state that
// only `cancel` can leave, and the review cycle it never spent must not be
// charged.
func TestVerify_StrandedVerifying_IsRecovered(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, fakeSessions{live: map[string]bool{"app": true}})
	svc.SetBudgetSource(StaticBudget(Budget{MaxAttempts: 3, MaxReviewCycles: 1, Concurrency: 1}))

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	res, err := svc.DispatchTask(task.ID)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	jobID := res.Job.ID

	// Strand it exactly as a crash between the transition and the verdict would:
	// job and task in `verifying`, nothing in flight.
	if _, err := store.TransitionJob(jobID, StateVerifying, false, "crash sim"); err != nil {
		t.Fatalf("stage verifying job: %v", err)
	}
	if _, err := store.TransitionTask(task.ID, StateVerifying, false, "crash sim"); err != nil {
		t.Fatalf("stage verifying task: %v", err)
	}
	// Precondition: stranded means every governance route refuses.
	if _, err := svc.VerifyTask(task.ID); err == nil {
		t.Error("precondition: a verifying task should not be verifiable")
	}
	if _, err := svc.RetryTask(task.ID, RetryRequest{}); err == nil {
		t.Error("precondition: a verifying task should not be retryable")
	}
	if _, err := svc.DispatchTask(task.ID); err == nil {
		t.Error("precondition: a verifying task should not be dispatchable")
	}

	// Reconcile is the repair — the same loop that exists for a crashed dispatch.
	rep, err := svc.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(rep.RecoveredVerifies) != 1 || rep.RecoveredVerifies[0] != jobID {
		t.Errorf("RecoveredVerifies = %v, want [%s]", rep.RecoveredVerifies, jobID)
	}
	gotTask, _ := store.GetTask(task.ID)
	if gotTask.State != StateCandidate {
		t.Errorf("task state = %q, want candidate", gotTask.State)
	}
	gotJob, _ := store.GetJob(jobID)
	if gotJob.State != StateCandidate {
		t.Errorf("job state = %q, want candidate", gotJob.State)
	}
	// The interrupted cycle was never spent, so the 1-cycle budget still allows a
	// verification. (Before the fix the append-only entry burned it permanently.)
	cycles, err := store.CountReviewCycles(task.ID)
	if err != nil {
		t.Fatalf("CountReviewCycles: %v", err)
	}
	if cycles != 0 {
		t.Errorf("review cycles = %d, want 0 — an interrupted verification costs nothing", cycles)
	}
	if _, err := svc.VerifyTask(task.ID); err != nil {
		t.Fatalf("verify after recovery: %v", err)
	}
	final, _ := store.GetTask(task.ID)
	if final.State != StateVerified {
		t.Errorf("task state = %q, want verified", final.State)
	}
	// A completed cycle IS charged.
	if cycles, _ := store.CountReviewCycles(task.ID); cycles != 1 {
		t.Errorf("review cycles after a real verification = %d, want 1", cycles)
	}
}

// TestVerify_NoVerifierConfigured_DoesNotStrand: discovering there is no verifier
// must happen before anything moves, or the check itself strands the task.
func TestVerify_NoVerifierConfigured_DoesNotStrand(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, nil /* no verifier */)
	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if _, err := svc.VerifyTask(task.ID); err == nil {
		t.Fatal("verify with no verifier should error")
	}
	got, _ := store.GetTask(task.ID)
	if got.State != StateCandidate {
		t.Errorf("task state = %q, want candidate — a misconfiguration must not strand it", got.State)
	}
	if cycles, _ := store.CountReviewCycles(task.ID); cycles != 0 {
		t.Errorf("review cycles = %d, want 0 — nothing was verified", cycles)
	}
}

// TestReconcile_DoesNotTouchInflightWork: releasing s.mu across the long calls
// lets the reconcile ticker observe a Job that is perfectly healthy. It must skip
// it, not "repair" it.
func TestReconcile_DoesNotTouchInflightWork(t *testing.T) {
	repo := gitRepo(t)
	r := blockingRunner{release: make(chan struct{})}
	// No live session: without the in-flight guard reconcile would fail this job.
	svc, wt, store := newService(t, mapResolver{"app": repo}, r, fakeSessions{live: map[string]bool{}})

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	done := make(chan DispatchResult, 1)
	go func() {
		res, _ := svc.DispatchTask(task.ID)
		done <- res
	}()
	jobID := waitForRunningJob(t, store, task.ID)

	rep, err := svc.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rep.SkippedInflight != 1 {
		t.Errorf("SkippedInflight = %d, want 1", rep.SkippedInflight)
	}
	if len(rep.FailedVanished) != 0 {
		t.Errorf("reconcile failed a live job: %v", rep.FailedVanished)
	}
	if !wt.Exists(jobID) {
		t.Error("reconcile reclaimed a live job's worktree")
	}
	gotJob, _ := store.GetJob(jobID)
	if gotJob.State != StateWorking {
		t.Errorf("live job state = %q, want working", gotJob.State)
	}

	close(r.release)
	<-done
}

// TestCancel_IsNotBlockedByARunningJob is the regression for "s.mu is held across
// runner.Run". A cancel arriving while a Job runs must be answered promptly, not
// after the whole wall-clock budget.
func TestCancel_IsNotBlockedByARunningJob(t *testing.T) {
	repo := gitRepo(t)
	r := blockingRunner{release: make(chan struct{})}
	svc, _, store := newService(t, mapResolver{"app": repo}, r, nil)

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_, _ = svc.DispatchTask(task.ID)
		close(done)
	}()
	waitForRunningJob(t, store, task.ID)

	// The runner is still blocked. Cancel must return anyway.
	cancelled := make(chan error, 1)
	go func() {
		_, err := svc.CancelTask(task.ID)
		cancelled <- err
	}()
	select {
	case err := <-cancelled:
		if err != nil {
			t.Fatalf("CancelTask: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CancelTask blocked behind the running job — the lock is held across runner.Run")
	}
	got, _ := store.GetTask(task.ID)
	if got.State != StateCancelled {
		t.Errorf("task state = %q, want cancelled", got.State)
	}

	// Let the run finish: the dispatch must not fight the cancellation.
	close(r.release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("dispatch never returned after the runner was released")
	}
	final, _ := store.GetTask(task.ID)
	if final.State != StateCancelled {
		t.Errorf("task state = %q after the run finished, want cancelled to stick", final.State)
	}
}

// TestDispatch_SecondOperationOnOneTaskIsRefused: with the lock released across
// the run, the in-flight set is what keeps two operations off one Task.
func TestDispatch_SecondOperationOnOneTaskIsRefused(t *testing.T) {
	repo := gitRepo(t)
	r := blockingRunner{release: make(chan struct{})}
	svc, _, store := newService(t, mapResolver{"app": repo}, r, nil)

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	first := make(chan struct{})
	go func() {
		_, _ = svc.DispatchTask(task.ID)
		close(first)
	}()
	// Release the runner and let the dispatch finish before the temp dirs go away,
	// or its post-run capture races t.TempDir cleanup.
	t.Cleanup(func() {
		close(r.release)
		<-first
	})
	waitForRunningJob(t, store, task.ID)

	// A second dispatch of the same task is refused, and it does not block.
	refused := make(chan error, 1)
	go func() {
		_, err := svc.DispatchTask(task.ID)
		refused <- err
	}()
	select {
	case err := <-refused:
		if err == nil {
			t.Fatal("a second concurrent dispatch should be refused")
		}
		// It is refused by the STATE guard (the task is already `working`), which
		// sits in front of the in-flight guard and shadows it for same-shaped
		// operations. That is the right order — the state answer is the more
		// informative one — so the in-flight set is defence in depth here, and its
		// own refusal behaviour is pinned directly in TestBeginOp_RefusesAndReleases.
		// What matters at this level is that the answer comes back at all instead of
		// blocking behind an hour-long lock.
		if !errors.Is(err, ErrWrongState) {
			t.Errorf("refusal = %v, want a state-precondition error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the second dispatch blocked instead of being refused")
	}
	jobs, _ := store.ListJobsForTask(task.ID)
	if len(jobs) != 1 {
		t.Errorf("job count = %d, want 1 — the refused dispatch must not create a Job", len(jobs))
	}
}

// waitForRunningJob blocks until the task has a job in `working`, so a test can
// act while a dispatch is genuinely mid-run.
func waitForRunningJob(t *testing.T, store *Store, taskID string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		jobs, _ := store.ListJobsForTask(taskID)
		for _, j := range jobs {
			if j.State == StateWorking {
				return j.ID
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no job reached `working` in time")
	return ""
}

// TestRetry_RebaseOntoSelfAuthoredTip_Refused is the regression for the audit's
// self-completing attack: a linked worktree shares the parent repo's refs, so a
// Job can move the target branch onto its own commit. `--rebase` would then
// re-freeze the acceptance oracle at a policy the worker wrote.
func TestRetry_RebaseOntoSelfAuthoredTip_Refused(t *testing.T) {
	repo := gitRepo(t)
	// A strict policy at the base.
	commitFile(t, repo, ".daedalus/verify.json",
		`{"checks":["go test ./..."],"acceptanceGlobs":["**/*_test.go"]}`)

	svc, store, task := rejectedTask(t, repo, Budget{MaxAttempts: 5, MaxReviewCycles: 5, Concurrency: 1})
	frozen := task.AcceptanceHash

	// The Job's own commit — exactly what a worktree-side `git update-ref` would
	// point the target branch at.
	jobs, _ := store.ListJobsForTask(task.ID)
	if len(jobs) == 0 || jobs[0].OutputSnapshot == "" {
		t.Fatalf("precondition: need a job commit, got %+v", jobs)
	}
	jobCommit := jobs[0].OutputSnapshot

	// Stage the self-authored tip. Note what this now takes: moving the repo's
	// BRANCH is no longer enough (the plane does not read it), so the only way to
	// reach this guard is for the plane-owned target itself to end up on a job
	// commit — e.g. an operator resyncing onto a commit a Job had planted. That is
	// precisely why the guard is now defence in depth rather than the mechanism.
	branch := trim(mustGit(t, repo, "rev-parse", "--abbrev-ref", "HEAD"))
	mustGit(t, repo, "update-ref", "refs/heads/"+branch, jobCommit)
	if _, err := store.SetTarget(repoKey(t, repo), jobCommit, EventMeta{Kind: EventGovernance}, "test: operator resync onto a job commit"); err != nil {
		t.Fatalf("SetTarget: %v", err)
	}
	target, err := svc.Target("app")
	if err != nil {
		t.Fatalf("TargetFor: %v", err)
	}
	if target.SHA != jobCommit {
		t.Fatalf("precondition: target = %s, want the job commit %s", target.SHA, jobCommit)
	}

	_, err = svc.RetryTask(task.ID, RetryRequest{Rebase: true})
	var rej *RejectionError
	if !errors.As(err, &rej) {
		t.Fatalf("rebase onto a self-authored tip = %v, want *RejectionError", err)
	}
	if rej.Reason != ReasonUnsafeRebase {
		t.Errorf("reason = %q, want %q", rej.Reason, ReasonUnsafeRebase)
	}
	// The oracle was NOT re-frozen, and the base was not moved.
	got, _ := store.GetTask(task.ID)
	if got.AcceptanceHash != frozen {
		t.Errorf("acceptance hash changed despite the refusal: %s → %s", frozen, got.AcceptanceHash)
	}
	if got.BaseSHA == jobCommit {
		t.Error("the task was rebased onto the job's own commit")
	}
	// A plain retry is still available — the refusal is scoped to the rebase.
	if _, err := svc.RetryTask(task.ID, RetryRequest{}); err != nil {
		t.Errorf("a non-rebase retry should still work: %v", err)
	}
}

// TestIsSelfAuthoredTip covers the primitive directly, including the safe case.
func TestIsSelfAuthoredTip(t *testing.T) {
	repo := gitRepo(t)
	base, _ := ReadHeadSHA(repo)
	jobCommit := commitFile(t, repo, "job-work.txt", "written by the job")
	// Reset the branch so jobCommit is not the tip any more.
	mustGit(t, repo, "reset", "--hard", base)
	developerCommit := commitFile(t, repo, "human-work.txt", "written by a human")

	tests := []struct {
		name string
		tip  string
		want bool
	}{
		{"the job's own commit is self-authored", jobCommit, true},
		{"a base the job built on is not", base, false},
		{"an unrelated developer commit is not", developerCommit, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, offender, err := IsSelfAuthoredTip(repo, tc.tip, []string{jobCommit})
			if err != nil {
				t.Fatalf("IsSelfAuthoredTip: %v", err)
			}
			if got != tc.want {
				t.Errorf("IsSelfAuthoredTip(%s) = %v (offender %s), want %v", shortSHA(tc.tip), got, offender, tc.want)
			}
		})
	}
}

func TestIsAncestor(t *testing.T) {
	repo := gitRepo(t)
	base, _ := ReadHeadSHA(repo)
	child := commitFile(t, repo, "later.txt", "later")

	for _, tc := range []struct {
		name                 string
		ancestor, descendant string
		want                 bool
	}{
		{"base is an ancestor of child", base, child, true},
		{"child is not an ancestor of base", child, base, false},
		{"a commit is its own ancestor", child, child, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := IsAncestor(repo, tc.ancestor, tc.descendant)
			if err != nil {
				t.Fatalf("IsAncestor: %v", err)
			}
			if got != tc.want {
				t.Errorf("IsAncestor = %v, want %v", got, tc.want)
			}
		})
	}
}

// mustGit runs a git command in dir and returns its output, failing the test on
// error. Used where a test needs to manipulate refs directly.
func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runGit(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return out
}

// panickingVerifier blows up mid-verification — the worst case for the
// stranded-`verifying` window.
type panickingVerifier struct{}

func (panickingVerifier) Verify(context.Context, VerifySpec) VerifyOutcome {
	panic("verifier exploded")
}

// TestVerify_PanickingVerifier_LeavesNoStrandedTask: net/http recovers a handler
// panic, so the daemon survives a verifier blowing up — the Task must survive it
// too, rather than being wedged in `verifying` with a review cycle spent.
func TestVerify_PanickingVerifier_LeavesNoStrandedTask(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, panickingVerifier{})

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected the verifier panic to propagate")
			}
		}()
		_, _ = svc.VerifyTask(task.ID)
	}()

	// The deferred recovery must have put it back.
	got, _ := store.GetTask(task.ID)
	if got.State != StateCandidate {
		t.Errorf("task state = %q, want candidate — a panicking verifier must not strand it", got.State)
	}
	if cycles, _ := store.CountReviewCycles(task.ID); cycles != 0 {
		t.Errorf("review cycles = %d, want 0 — the verification never completed", cycles)
	}
	// The in-flight claim must have been released too. Probe with VerifyTask
	// specifically: it is the operation that takes a claim, and `candidate`'s only
	// forward move. (Probing with CancelTask would prove nothing — cancel never
	// calls beginOp, so it succeeds even on a task bricked by a leaked claim.)
	if op, leaked := svc.inflightFor(task.ID); leaked {
		t.Errorf("the verifier panic leaked an in-flight claim %+v — the task is permanently un-verifiable", op)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		// A second verify panics again (same verifier); what matters is that it
		// RUNS rather than being refused with operation_in_flight.
		defer func() { _ = recover() }()
		if _, err := svc.VerifyTask(task.ID); err != nil {
			if reason, refused := Rejected(err); refused && reason == ReasonOperationInFlight {
				t.Errorf("verify after a panic was refused as %q — the claim leaked", reason)
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the service lock was left inconsistent by the verifier panic")
	}
}

// TestPrepareDispatch_PanicDoesNotDeadlockThePlane: the locked phase of a
// dispatch shells out to git, reads files and touches the DB. A panic in there
// must not leave s.mu held — net/http recovers the handler panic, so a deadlocked
// plane would stay "up" and silently stop answering, the worst failure mode.
func TestPrepareDispatch_PanicDoesNotDeadlockThePlane(t *testing.T) {
	repo := gitRepo(t)
	resolver := &panicResolver{dirs: mapResolver{"app": repo}}
	svc, _, _ := newService(t, resolver, StubRunner{Result: ExecSuccess, WriteFile: true}, nil)

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	resolver.panicNow = true
	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected the resolver panic to propagate")
			}
		}()
		_, _ = svc.DispatchTask(task.ID)
	}()
	resolver.panicNow = false

	// The plane must still answer. Probe with operations that take s.mu.
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := svc.ListTasks(); err != nil {
			t.Errorf("ListTasks after a panic: %v", err)
		}
		if _, err := svc.CancelTask(task.ID); err != nil {
			t.Errorf("CancelTask after a panic: %v", err)
		}
		if _, err := svc.Reconcile(); err != nil {
			t.Errorf("Reconcile after a panic: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the control plane deadlocked: s.mu was still held after a panic in the locked dispatch phase")
	}
	// And the claim did not leak either.
	if op, leaked := svc.inflightFor(task.ID); leaked {
		t.Errorf("panic in the locked phase leaked an in-flight claim: %+v", op)
	}
}

// panicResolver is a ProjectResolver that panics on demand, standing in for any
// of the DB / git / filesystem work inside the locked dispatch phase blowing up.
type panicResolver struct {
	dirs     mapResolver
	panicNow bool
}

func (p *panicResolver) ProjectDir(name string) (string, error) {
	if p.panicNow {
		panic("resolver exploded")
	}
	return p.dirs.ProjectDir(name)
}

// --- the in-flight guard itself ----------------------------------------------

// TestBeginOp_RefusesAndReleases pins the in-flight set directly. It is the whole
// mechanism that replaced the over-held mutex, so its refusal behaviour needs
// covering on its own, not only through the reconcile-skip path.
func TestWithClaim_RefusesReleasesAndSurvivesPanics(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)
	first := inflightOp{kind: "dispatch", jobID: "J-1", project: "app"}

	t.Run("a second claim on the same task is refused, not queued", func(t *testing.T) {
		svc.mu.Lock()
		defer svc.mu.Unlock()
		err := svc.withClaim("T-1", first, func() error {
			// Inside the scope the task is claimed…
			if !svc.jobIsInflight("T-1", "J-1") {
				t.Error("jobIsInflight(T-1, J-1) = false inside the claim")
			}
			if svc.jobIsInflight("T-1", "J-9") {
				t.Error("jobIsInflight matched a job that is not the claimed one")
			}
			// …so a second claim is refused with a typed reason naming the holder.
			inner := svc.withClaim("T-1", inflightOp{kind: "verify", project: "app"}, func() error {
				t.Error("the body of a refused claim must not run")
				return nil
			})
			var rej *RejectionError
			if !errors.As(inner, &rej) {
				t.Fatalf("second claim err = %v, want *RejectionError", inner)
			}
			if rej.Reason != ReasonOperationInFlight {
				t.Errorf("reason = %q, want %q", rej.Reason, ReasonOperationInFlight)
			}
			if !strings.Contains(rej.Message, "dispatch") || !strings.Contains(rej.Message, "J-1") {
				t.Errorf("message %q should name the operation already in flight", rej.Message)
			}
			// A DIFFERENT task is unaffected — claims are per task, not global.
			if err := svc.withClaim("T-2", inflightOp{kind: "dispatch", project: "app"}, func() error { return nil }); err != nil {
				t.Errorf("claim on a different task: %v", err)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("withClaim: %v", err)
		}
		if _, leaked := svc.claimJobID("T-1"); leaked {
			t.Error("the claim outlived its scope")
		}
	})

	t.Run("an error inside the scope still releases", func(t *testing.T) {
		svc.mu.Lock()
		defer svc.mu.Unlock()
		want := errors.New("body failed")
		if got := svc.withClaim("T-3", first, func() error { return want }); !errors.Is(got, want) {
			t.Errorf("withClaim err = %v, want %v", got, want)
		}
		if _, leaked := svc.claimJobID("T-3"); leaked {
			t.Error("an error path leaked the claim")
		}
	})

	t.Run("a PANIC inside the scope still releases", func(t *testing.T) {
		svc.mu.Lock()
		defer svc.mu.Unlock()
		func() {
			defer func() {
				if recover() == nil {
					t.Error("expected the panic to propagate")
				}
			}()
			_ = svc.withClaim("T-4", first, func() error { panic("boom") })
		}()
		if _, leaked := svc.claimJobID("T-4"); leaked {
			t.Error("a panic leaked the claim — this is the whole point of the helper")
		}
	})

	t.Run("unlockedDuring re-takes the lock even on a panic", func(t *testing.T) {
		svc.mu.Lock()
		func() {
			defer func() { _ = recover() }()
			svc.unlockedDuring(func() { panic("boom") })
		}()
		// If the lock were not re-taken this Unlock would panic on an unlocked mutex.
		svc.mu.Unlock()
	})
}

// TestClaimReleaseIsUnrepresentable is the class-level guard the Sprint-58 audit
// asked for. The same bug — a claim or a lock released by a bare statement that a
// panic unwinds past — appeared three times in one sprint, and five hand-written
// correct `defer` sites were still one bare statement away from a sixth bug.
//
// So the release lives in exactly ONE place. This test fails the moment a second
// appears, which turns "somebody must remember" into "the suite will say so".
func TestClaimReleaseIsUnrepresentable(t *testing.T) {
	// Needles assembled at runtime so this file does not match itself.
	release := "delete(" + "s.inflight"
	unlock := "s.mu." + "Unlock()"
	// The claim witness closes the deliberate-act gap the type system cannot: Go
	// has no unconstructible-within-its-package type, so constructing the witness
	// anywhere but claim.go is the one remaining way to take a claim outside the
	// scope helper.
	witness := "claimWitness" + "{"

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package dir: %v", err)
	}
	scanned := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		scanned++
		body := string(data)
		if name == "claim.go" {
			continue // the one place both are allowed to be written
		}
		if strings.Contains(body, release) {
			t.Errorf("%s releases an in-flight claim directly; use Service.withClaim so a panic cannot skip it", name)
		}
		if strings.Contains(body, witness) {
			t.Errorf("%s constructs a claim witness; only Service.withClaim may, or the claim can be taken without its scoped release", name)
		}
		for _, line := range strings.Split(body, "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.Contains(trimmed, unlock) || strings.HasPrefix(trimmed, "//") {
				continue // prose about the rule is not a violation of it
			}
			// `defer s.mu.Unlock()` is the safe form; a bare call is not.
			if !strings.HasPrefix(trimmed, "defer ") {
				t.Errorf("%s: bare %s (%q) — use Service.unlockedDuring or defer, so a panic cannot leave the plane deadlocked",
					name, unlock, trimmed)
			}
		}
	}
	if scanned == 0 {
		t.Fatal("scanned no source files — the guard is not actually running")
	}
}

// TestRefuseSelfAuthoredRebase_FailsClosedOnCheckError: when the ancestry check
// cannot be *performed* (a pruned or garbage-collected job commit), the plane must
// refuse the rebase rather than assume it is safe. Re-freezing the acceptance
// oracle is the consequential act, so "don't know" has to mean "no".
func TestRefuseSelfAuthoredRebase_FailsClosedOnCheckError(t *testing.T) {
	repo := gitRepo(t)
	svc, store, task := rejectedTask(t, repo, Budget{MaxAttempts: 5, MaxReviewCycles: 5, Concurrency: 1})
	frozen := task.AcceptanceHash

	// Point the job at a commit that does not exist, so `git merge-base
	// --is-ancestor` fails outright rather than answering yes or no.
	jobs, _ := store.ListJobsForTask(task.ID)
	if len(jobs) == 0 {
		t.Fatal("precondition: expected a job")
	}
	if _, err := store.SetJobExecutionResult(jobs[0].ID, ExecSuccess,
		"0123456789abcdef0123456789abcdef01234567"); err != nil {
		t.Fatalf("planting a bogus job commit: %v", err)
	}
	// Advance the plane-owned target so a rebase is actually attempted.
	moved := commitFile(t, repo, "moved.txt", "the target moved")
	if _, err := store.AdvanceTarget(repoKey(t, repo), task.BaseSHA, moved, "test: another integration landed"); err != nil {
		t.Fatalf("AdvanceTarget: %v", err)
	}

	_, err := svc.RetryTask(task.ID, RetryRequest{Rebase: true})
	var rej *RejectionError
	if !errors.As(err, &rej) {
		t.Fatalf("rebase with an uncheckable ancestry = %v, want *RejectionError", err)
	}
	if rej.Reason != ReasonUnsafeRebase {
		t.Errorf("reason = %q, want %q", rej.Reason, ReasonUnsafeRebase)
	}
	if !strings.Contains(rej.Message, "cannot confirm") {
		t.Errorf("message %q should say the check could not be performed", rej.Message)
	}
	// Nothing was re-frozen.
	got, _ := store.GetTask(task.ID)
	if got.AcceptanceHash != frozen {
		t.Errorf("acceptance hash changed despite the refusal: %s → %s", frozen, got.AcceptanceHash)
	}
}

// TestReconcile_SettlesAGhostJob is the regression for the audit's F6: a Job whose
// bookkeeping failed after its Task settled stays non-terminal forever, so
// ListActiveJobs returns it on every pass and the other reconcile checks skip it
// (its state is neither `working` nor `verifying`). A ghost in the census.
func TestReconcile_SettlesAGhostJob(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"},
		fakeSessions{live: map[string]bool{"app": true}}, StubVerifyRunner{Pass: true})

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	res, err := svc.DispatchTask(task.ID)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if _, err := svc.VerifyTask(task.ID); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if _, err := svc.IntegrateTask(task.ID); err != nil {
		t.Fatalf("Integrate: %v", err)
	}
	// Strand the Job as a failed driveJob would: Task integrated, Job left behind.
	if _, err := store.db.Exec(`UPDATE jobs SET state = ? WHERE id = ?`,
		string(StateApprovalRequired), res.Job.ID); err != nil {
		t.Fatalf("stranding the job: %v", err)
	}
	if active, _ := store.ListActiveJobs(); len(active) != 1 {
		t.Fatalf("precondition: expected 1 ghost job, got %d", len(active))
	}

	rep, err := svc.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(rep.SettledOrphanJobs) != 1 || rep.SettledOrphanJobs[0] != res.Job.ID {
		t.Errorf("SettledOrphanJobs = %v, want [%s]", rep.SettledOrphanJobs, res.Job.ID)
	}
	got, _ := store.GetJob(res.Job.ID)
	if !IsTerminal(got.State) {
		t.Errorf("job state = %q, still not terminal", got.State)
	}
	// The census is clean, and a second pass is a no-op.
	if active, _ := store.ListActiveJobs(); len(active) != 0 {
		t.Errorf("ListActiveJobs = %d, want 0", len(active))
	}
	rep2, _ := svc.Reconcile()
	if len(rep2.SettledOrphanJobs) != 0 {
		t.Errorf("second pass settled %v, want nothing (idempotent)", rep2.SettledOrphanJobs)
	}
	// A live Job under a NON-terminal task is untouched by this rule.
	if _, err := svc.CancelTask(task.ID); err != nil && !errors.Is(err, ErrIllegalTransition) {
		t.Logf("cancel of an integrated task: %v (expected)", err)
	}
}
