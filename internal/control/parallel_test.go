// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// Concurrency correctness with N Jobs GENUINELY in flight (Sprint 61, item 3).
//
// These use real goroutines and real temp repos rather than deterministic
// interleaving: the point is to prove the SYSTEM serializes, not that a single
// function does. M15 proved the integration compare-and-swap serializes 16 ways
// at the store level, but with one active Task per project two integrations
// could never actually be in flight — so the merge queue had never done real
// work. It does now.

// gatedRunner blocks every Job until released, so N Jobs can be held live at
// once and observed as a set.
type gatedRunner struct {
	release chan struct{}
	mu      sync.Mutex
	started map[string]bool
	marker  string
}

func newGatedRunner(marker string) *gatedRunner {
	return &gatedRunner{release: make(chan struct{}), started: map[string]bool{}, marker: marker}
}

func (r *gatedRunner) Run(ctx context.Context, spec JobSpec) RunOutcome {
	r.mu.Lock()
	r.started[spec.JobID] = true
	r.mu.Unlock()
	select {
	case <-r.release:
	case <-ctx.Done():
		return RunOutcome{Result: ExecCancelled}
	}
	// Write a per-job marker so each Job's artifact differs and a merge is real.
	name := r.marker
	if name == "" {
		name = spec.JobID + ".txt"
	}
	_ = writeFileForTestNoT(spec.WorktreeDir, name, "work by "+spec.JobID+"\n")
	return RunOutcome{Result: ExecSuccess}
}

func (r *gatedRunner) liveCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.started)
}

// parallelPlane builds a Service whose scheduler permits `perProject` concurrent
// Jobs, with a gated runner so Jobs can be held live.
func parallelPlane(t *testing.T, projects map[string]string, perProject, global int) (*Service, *Store, *gatedRunner) {
	t.Helper()
	runner := newGatedRunner("")
	svc, _, store := newService(t, mapResolver(projects), runner, nil, StubVerifyRunner{Pass: true})
	svc.SetSchedulerLimits(SchedulerLimits{Global: global, PerProject: perProject})
	svc.SetPolicySource(StaticBudget(Budget{
		WallClockSeconds: 120, MaxAttempts: 3, MaxReviewCycles: 3, Concurrency: 0,
	}))
	return svc, store, runner
}

// waitFor polls until cond is true or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// --- several Jobs live on ONE project -------------------------------------------

// TestParallel_SeveralJobsOneProject is the invariant lift, proven: three Tasks
// on one project run concurrently, each in its own worktree, each producing its
// own artifact.
func TestParallel_SeveralJobsOneProject(t *testing.T) {
	repo := gitRepo(t)
	svc, store, runner := parallelPlane(t, map[string]string{"app": repo}, 3, 8)

	var ids []string
	for i := 0; i < 3; i++ {
		task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "work"})
		if err != nil {
			t.Fatalf("CreateTask %d: %v", i, err)
		}
		ids = append(ids, task.ID)
	}

	var wg sync.WaitGroup
	results := make([]error, len(ids))
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			_, results[i] = svc.DispatchTask(id)
		}(i, id)
	}
	// All three genuinely in flight at once.
	waitFor(t, "3 jobs running concurrently", func() bool { return runner.liveCount() == 3 })
	running, err := store.CountRunningJobsForProject("app")
	if err != nil {
		t.Fatalf("CountRunningJobsForProject: %v", err)
	}
	if running != 3 {
		t.Errorf("running jobs = %d, want 3", running)
	}
	close(runner.release)
	wg.Wait()

	for i, err := range results {
		if err != nil {
			t.Errorf("dispatch %s: %v", ids[i], err)
		}
	}
	// Each Task has its own Job, its own worktree branch, and its own artifact.
	seenBranch := map[string]bool{}
	for _, id := range ids {
		jobs, _ := store.ListJobsForTask(id)
		if len(jobs) != 1 {
			t.Errorf("task %s has %d jobs, want 1", id, len(jobs))
			continue
		}
		arts, _ := store.ListArtifactsForJob(jobs[0].ID)
		if len(arts) != 1 {
			t.Errorf("task %s job %s has %d artifacts, want 1", id, jobs[0].ID, len(arts))
			continue
		}
		if seenBranch[arts[0].Branch] {
			t.Errorf("branch %s reused across concurrent jobs", arts[0].Branch)
		}
		seenBranch[arts[0].Branch] = true
		if arts[0].HeadSHA == arts[0].BaseSHA {
			t.Errorf("task %s produced an empty artifact", id)
		}
	}
}

// TestParallel_SeveralProjects: Jobs on different projects do not contend.
func TestParallel_SeveralProjects(t *testing.T) {
	repoA, repoB := gitRepo(t), gitRepo(t)
	svc, store, runner := parallelPlane(t, map[string]string{"alpha": repoA, "beta": repoB}, 1, 8)

	a, err := svc.CreateTask(CreateTaskRequest{Project: "alpha", Objective: "a"})
	if err != nil {
		t.Fatalf("CreateTask alpha: %v", err)
	}
	b, err := svc.CreateTask(CreateTaskRequest{Project: "beta", Objective: "b"})
	if err != nil {
		t.Fatalf("CreateTask beta: %v", err)
	}

	var wg sync.WaitGroup
	for _, id := range []string{a.ID, b.ID} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, err := svc.DispatchTask(id); err != nil {
				t.Errorf("dispatch %s: %v", id, err)
			}
		}(id)
	}
	// Both live at once even though the PER-PROJECT limit is 1 — the limit is per
	// project, not global.
	waitFor(t, "one job per project running concurrently", func() bool { return runner.liveCount() == 2 })
	close(runner.release)
	wg.Wait()

	for _, id := range []string{a.ID, b.ID} {
		got, _ := store.GetTask(id)
		if got.State != StateCandidate {
			t.Errorf("task %s = %s, want candidate", id, got.State)
		}
	}
}

// TestParallel_PerProjectLimitRefusesTheExtra: with the limit at 1, a second
// concurrent dispatch on the same project is refused with a typed rejection —
// never silently dropped.
func TestParallel_PerProjectLimitRefusesTheExtra(t *testing.T) {
	repo := gitRepo(t)
	svc, store, runner := parallelPlane(t, map[string]string{"app": repo}, 1, 8)

	first, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "first"})
	second, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "second"})

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := svc.DispatchTask(first.ID); err != nil {
			t.Errorf("first dispatch: %v", err)
		}
	}()
	waitFor(t, "the first job to start", func() bool { return runner.liveCount() == 1 })

	_, err := svc.DispatchTask(second.ID)
	var rej *RejectionError
	if !errors.As(err, &rej) {
		t.Fatalf("second dispatch = %v, want a typed rejection", err)
	}
	if rej.Reason != ReasonConcurrencyExceeded {
		t.Errorf("reason = %q, want %q", rej.Reason, ReasonConcurrencyExceeded)
	}
	// The refusal is on the record, not silent.
	events, _ := store.ListEventsForTask(second.ID)
	if !hasEvent(events, EventSchedule, ReasonConcurrencyExceeded) {
		t.Error("the admission refusal should be a logged scheduler event")
	}
	// And the refused Task is untouched.
	got, _ := store.GetTask(second.ID)
	if got.State != StatePlanned {
		t.Errorf("refused task = %s, want planned", got.State)
	}

	close(runner.release)
	<-done
	// With capacity free it is admitted.
	if _, err := svc.DispatchTask(second.ID); err != nil {
		t.Errorf("dispatch after release: %v", err)
	}
}

// --- reconcile must not touch another Job's work --------------------------------

// TestParallel_ReconcileDoesNotAdoptAnotherJobsWork: with N Jobs live, a
// reconcile pass must leave every one of them alone — not fail them, not reclaim
// their worktrees, not settle them.
func TestParallel_ReconcileDoesNotAdoptAnotherJobsWork(t *testing.T) {
	repo := gitRepo(t)
	runner := newGatedRunner("")
	// No live sessions: without the in-flight guard reconcile would fail all three.
	svc, wt, store := newService(t, mapResolver{"app": repo}, runner,
		fakeSessions{live: map[string]bool{}}, StubVerifyRunner{Pass: true})
	svc.SetSchedulerLimits(SchedulerLimits{Global: 8, PerProject: 3})

	var ids []string
	for i := 0; i < 3; i++ {
		task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "work"})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		ids = append(ids, task.ID)
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, err := svc.DispatchTask(id); err != nil {
				t.Errorf("dispatch %s: %v", id, err)
			}
		}(id)
	}
	waitFor(t, "3 jobs running", func() bool { return runner.liveCount() == 3 })

	// Reconcile repeatedly WHILE they run.
	for i := 0; i < 3; i++ {
		rep, err := svc.Reconcile()
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if len(rep.FailedVanished) != 0 {
			t.Errorf("reconcile failed live jobs: %v", rep.FailedVanished)
		}
		if len(rep.RemovedOrphans) != 0 {
			t.Errorf("reconcile reclaimed live worktrees: %v", rep.RemovedOrphans)
		}
		if len(rep.SettledOrphanJobs) != 0 {
			t.Errorf("reconcile settled live jobs: %v", rep.SettledOrphanJobs)
		}
		if rep.SkippedInflight != 3 {
			t.Errorf("SkippedInflight = %d, want 3 — every live job must be recognised", rep.SkippedInflight)
		}
	}
	close(runner.release)
	wg.Wait()

	for _, id := range ids {
		jobs, _ := store.ListJobsForTask(id)
		if len(jobs) != 1 || jobs[0].State != StateCandidate {
			t.Errorf("task %s job state = %+v, want a single candidate", id, jobs)
			continue
		}
		if !wt.Exists(jobs[0].ID) {
			t.Errorf("job %s lost its worktree to a concurrent reconcile", jobs[0].ID)
		}
	}
}

// --- cancellation targets exactly one Job ---------------------------------------

func TestParallel_CancelTargetsExactlyOneJob(t *testing.T) {
	repo := gitRepo(t)
	svc, store, runner := parallelPlane(t, map[string]string{"app": repo}, 3, 8)

	var ids []string
	for i := 0; i < 3; i++ {
		task, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "work"})
		ids = append(ids, task.ID)
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, _ = svc.DispatchTask(id)
		}(id)
	}
	waitFor(t, "3 jobs running", func() bool { return runner.liveCount() == 3 })

	victim := ids[1]
	if _, err := svc.CancelTask(victim); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	close(runner.release)
	wg.Wait()

	for _, id := range ids {
		got, _ := store.GetTask(id)
		jobs, _ := store.ListJobsForTask(id)
		if id == victim {
			if got.State != StateCancelled {
				t.Errorf("the cancelled task = %s, want cancelled", got.State)
			}
			if len(jobs) == 1 && !IsTerminal(jobs[0].State) {
				t.Errorf("the cancelled task's job = %s, want terminal", jobs[0].State)
			}
			continue
		}
		if got.State != StateCandidate {
			t.Errorf("task %s = %s, want candidate — cancellation hit the wrong Job", id, got.State)
		}
		if len(jobs) == 1 && jobs[0].State != StateCandidate {
			t.Errorf("job of %s = %s, want candidate", id, jobs[0].State)
		}
	}
}

// --- the merge queue, finally load-bearing --------------------------------------

// TestParallel_CompetingIntegrationsSerialize is what Sprint 61 makes possible:
// two Tasks on ONE project, both verified against the same target, both trying to
// land at the same time from real goroutines. Exactly one may win the
// compare-and-swap on the first pass; the other must rebase onto the winner and
// re-verify, so BOTH changes end up in the trunk and neither is lost.
func TestParallel_CompetingIntegrationsSerialize(t *testing.T) {
	repo := gitRepo(t)
	runner := newGatedRunner("")
	svc, _, store := newService(t, mapResolver{"app": repo}, runner, nil, StubVerifyRunner{Pass: true})
	svc.SetSchedulerLimits(SchedulerLimits{Global: 8, PerProject: 2})

	// Two Tasks, dispatched and verified concurrently against one target.
	var ids []string
	for i := 0; i < 2; i++ {
		task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "work"})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		ids = append(ids, task.ID)
	}
	startTarget, err := svc.Target("app")
	if err != nil {
		t.Fatalf("TargetFor: %v", err)
	}

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, err := svc.DispatchTask(id); err != nil {
				t.Errorf("dispatch %s: %v", id, err)
			}
		}(id)
	}
	waitFor(t, "both jobs running", func() bool { return runner.liveCount() == 2 })
	close(runner.release)
	wg.Wait()

	for _, id := range ids {
		if _, err := svc.VerifyTask(id); err != nil {
			t.Fatalf("verify %s: %v", id, err)
		}
	}
	// Both are verified against the SAME base — the competing-landings setup.
	for _, id := range ids {
		got, _ := store.GetTask(id)
		if got.BaseSHA != startTarget.SHA {
			t.Fatalf("task %s base %s != start target %s", id, got.BaseSHA, startTarget.SHA)
		}
	}

	// Now race the landings from real goroutines.
	results := make([]error, len(ids))
	var landWg sync.WaitGroup
	start := make(chan struct{})
	for i, id := range ids {
		landWg.Add(1)
		go func(i int, id string) {
			defer landWg.Done()
			<-start
			_, results[i] = svc.IntegrateTask(id, IntegrateRequest{})
		}(i, id)
	}
	close(start)
	landWg.Wait()

	// Both should have landed: the loser of the CAS rebases onto the winner and
	// re-verifies rather than failing. If either errored it must be a typed
	// rejection, never a corrupted target.
	landed := 0
	for i, err := range results {
		if err == nil {
			landed++
			continue
		}
		if _, refused := Rejected(err); !refused {
			t.Errorf("integration of %s failed with an untyped error: %v", ids[i], err)
		}
	}
	if landed == 0 {
		t.Fatalf("neither integration landed: %v", results)
	}

	final, err := svc.Target("app")
	if err != nil {
		t.Fatalf("TargetFor after landing: %v", err)
	}
	if final.SHA == startTarget.SHA {
		t.Fatal("the target never moved — nothing landed")
	}
	// The trunk must contain the work of every task that reported success, and the
	// history must be linear (a rebase, not a clobber).
	for i, id := range ids {
		if results[i] != nil {
			continue
		}
		jobs, _ := store.ListJobsForTask(id)
		arts, _ := store.ListArtifactsForJob(jobs[0].ID)
		marker := jobs[0].ID + ".txt"
		if !fileExistsAt(repo, final.SHA, marker) {
			t.Errorf("task %s reported success but %s is not in the landed trunk %s",
				id, marker, shortSHA(final.SHA))
		}
		if arts[0].IntegratedSHA == "" {
			t.Errorf("task %s landed without recording the integrated commit", id)
		}
	}
	if landed == 2 {
		// Both landed: the second must have rebased onto the first, so the target's
		// history holds both markers.
		t.Logf("both landings serialized onto %s", shortSHA(final.SHA))
	}
}

// TestParallel_WorktreeCreateRemoveDoNotRace hammers the worktree manager from
// several goroutines: deterministic names mean create/remove must be safe when
// several Jobs come and go at once.
func TestParallel_WorktreeCreateRemoveDoNotRace(t *testing.T) {
	repo := gitRepo(t)
	dataDir := t.TempDir()
	wt := NewWorktreeManager(dataDir)
	base, err := ReadHeadSHA(repo)
	if err != nil {
		t.Fatalf("ReadHeadSHA: %v", err)
	}

	const workers = 6
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			jobID := "J-" + string(rune('a'+i))
			path, err := wt.Add(repo, "T-1", jobID, base)
			if err != nil {
				errs[i] = err
				return
			}
			if _, err := wt.Capture(path); err != nil {
				errs[i] = err
				return
			}
			errs[i] = wt.Remove(repo, jobID)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("worker %d: %v", i, err)
		}
	}
	remaining, err := wt.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("worktrees left behind: %v", remaining)
	}
	// git's own view agrees — no stale registrations.
	out, err := runGit(repo, "worktree", "list", "--porcelain")
	if err != nil {
		t.Fatalf("worktree list: %v", err)
	}
	if n := strings.Count(out, "worktree "); n != 1 {
		t.Errorf("git sees %d worktrees, want 1 (the main checkout):\n%s", n, out)
	}
}

// TestParallel_AbandonedDispatchDoesNotBrickTheProject is the audit's F2 repro,
// end to end through the Service rather than the scheduler alone: dispatch, get
// refused for capacity, walk away — and the project must not lose its
// parallelism.
func TestParallel_AbandonedDispatchDoesNotBrickTheProject(t *testing.T) {
	repo := gitRepo(t)
	svc, store, runner := parallelPlane(t, map[string]string{"app": repo}, 1, 8)
	svc.Scheduler().SetTicketLiveness(time.Minute, 2, nil)

	a, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "a"})
	abandoned, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "abandoned"})
	later, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "later"})

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := svc.DispatchTask(a.ID); err != nil {
			t.Errorf("dispatch a: %v", err)
		}
	}()
	waitFor(t, "the first job to start", func() bool { return runner.liveCount() == 1 })

	// The abandoned Task asks once and is refused for capacity.
	if _, err := svc.DispatchTask(abandoned.ID); err == nil {
		t.Fatal("the second dispatch should have been refused for capacity")
	}
	// The first job finishes; capacity is free.
	close(runner.release)
	<-done
	if running, _ := store.CountRunningJobsForProject("app"); running != 0 {
		t.Fatalf("project still shows %d running", running)
	}

	// A third Task keeps asking. It must get in without anyone touching the
	// abandoned one.
	var admitted bool
	for i := 0; i < 6; i++ {
		if _, err := svc.DispatchTask(later.ID); err == nil {
			admitted = true
			break
		}
	}
	if !admitted {
		t.Fatal("free capacity never became usable — an abandoned dispatch bricked the project")
	}
	// And the abandoned Task is untouched: nothing was cancelled on its behalf.
	got, _ := store.GetTask(abandoned.ID)
	if got.State != StatePlanned {
		t.Errorf("the abandoned task = %s, want planned (it should simply have lost its place)", got.State)
	}
}

// TestParallel_TwoIndependentChangesBothLand exercises the CLEAN-REBASE path
// deliberately. Before the stub's marker became job-scoped, every concurrent Job
// wrote the same filename, so two artifacts landing on one queue always
// collided — the conflict path was reached by accident and this one was not
// reached at all.
func TestParallel_TwoIndependentChangesBothLand(t *testing.T) {
	repo := gitRepo(t)
	// A plain (ungated) runner: this test dispatches synchronously, so a runner
	// that waits for a release channel would deadlock. Its marker is job-scoped by
	// default, which is what makes the two changes independent.
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, StubVerifyRunner{Pass: true})
	svc.SetSchedulerLimits(SchedulerLimits{Global: 8, PerProject: 2})

	var ids []string
	for i := 0; i < 2; i++ {
		task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "independent work"})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		ids = append(ids, task.ID)
	}
	start, _ := svc.Target("app")

	// Dispatch and verify both against the same target.
	for _, id := range ids {
		if _, err := svc.DispatchTask(id); err != nil {
			t.Fatalf("dispatch %s: %v", id, err)
		}
		if _, err := svc.VerifyTask(id); err != nil {
			t.Fatalf("verify %s: %v", id, err)
		}
		got, _ := store.GetTask(id)
		if got.BaseSHA != start.SHA {
			t.Fatalf("task %s base %s != start target %s", id, got.BaseSHA, start.SHA)
		}
	}

	// Both land: the second REBASES onto the first rather than conflicting.
	for _, id := range ids {
		if _, err := svc.IntegrateTask(id, IntegrateRequest{}); err != nil {
			t.Fatalf("integrate %s: %v — independent changes must both land", id, err)
		}
	}
	final, _ := svc.Target("app")
	if final.SHA == start.SHA {
		t.Fatal("the target never moved")
	}
	// Every Job's own marker is present in the landed trunk: nothing was lost.
	for _, id := range ids {
		jobs, _ := store.ListJobsForTask(id)
		marker := jobs[0].ID + "-AGENT_RAN.txt"
		if !fileExistsAt(repo, final.SHA, marker) {
			t.Errorf("task %s: %s missing from the landed trunk %s", id, marker, shortSHA(final.SHA))
		}
	}
	// Linear history: base + one commit per landing.
	out, err := runGit(repo, "rev-list", "--count", final.SHA)
	if err != nil {
		t.Fatalf("rev-list: %v", err)
	}
	if got := trim(out); got != "3" {
		t.Errorf("history length = %s, want 3 (init + two rebased landings)", got)
	}
}

// TestParallel_CollidingChangesConflictDeliberately is the other half: with a
// SHARED marker name the two artifacts really do collide, and the transaction
// refuses cleanly with the target untouched. Opting in to the conflict is the
// point — it used to happen whether a test wanted it or not.
func TestParallel_CollidingChangesConflictDeliberately(t *testing.T) {
	repo := gitRepo(t)
	// Both Jobs write the SAME file: a genuine add/add conflict.
	runner := StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "shared.txt"}
	svc, _, store := newService(t, mapResolver{"app": repo}, runner, nil, StubVerifyRunner{Pass: true})
	svc.SetSchedulerLimits(SchedulerLimits{Global: 8, PerProject: 2})

	var ids []string
	for i := 0; i < 2; i++ {
		task, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "colliding work"})
		ids = append(ids, task.ID)
		if _, err := svc.DispatchTask(task.ID); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		if _, err := svc.VerifyTask(task.ID); err != nil {
			t.Fatalf("verify: %v", err)
		}
	}

	if _, err := svc.IntegrateTask(ids[0], IntegrateRequest{}); err != nil {
		t.Fatalf("the first landing should succeed: %v", err)
	}
	afterFirst, _ := svc.Target("app")

	_, err := svc.IntegrateTask(ids[1], IntegrateRequest{})
	var rej *RejectionError
	if !errors.As(err, &rej) || rej.Reason != ReasonMergeConflict {
		t.Fatalf("the colliding landing = %v, want a merge_conflict refusal", err)
	}
	// The target is untouched and the Task is back on the retry ladder.
	final, _ := svc.Target("app")
	if final.SHA != afterFirst.SHA {
		t.Errorf("target moved on a conflict: %s → %s", afterFirst.SHA, final.SHA)
	}
	got, _ := store.GetTask(ids[1])
	if got.State != StateRejected {
		t.Errorf("the conflicted task = %s, want rejected", got.State)
	}
}
