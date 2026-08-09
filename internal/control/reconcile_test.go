// Copyright (C) 2026 Techdelight BV

package control

import (
	"testing"
	"time"
)

// Reconcile repairs (Sprint 62, item 1). Two defects the Sprint-61 audit found,
// both made materially worse by lifting the one-Job-per-project invariant.

// projectOnlySessions implements ONLY the legacy project-level observer, so the
// heuristic fallback is exercised — the deployment where per-Job liveness is
// unavailable.
type projectOnlySessions struct {
	live map[string]bool
	err  error
}

func (p projectOnlySessions) HasSession(project string) (bool, error) {
	if p.err != nil {
		return false, p.err
	}
	return p.live[project], nil
}

// stageWorkingJob puts a Task and Job into `working` as a crashed dispatch would
// have left them, with a worktree on disk.
func stageWorkingJob(t *testing.T, svc *Service, store *Store, wt *WorktreeManager, repo, project string, budget int) (Task, Job) {
	t.Helper()
	task, err := svc.CreateTask(CreateTaskRequest{Project: project, Objective: "work"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.TransitionTask(task.ID, StateQueued, false, ""); err != nil {
		t.Fatalf("→queued: %v", err)
	}
	if _, err := store.TransitionTask(task.ID, StateWorking, false, ""); err != nil {
		t.Fatalf("→working: %v", err)
	}
	job, err := store.CreateJob(task.ID, task.BaseSHA, "claude", budget, StateWorking)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if _, err := wt.Add(repo, task.ID, job.ID, task.BaseSHA); err != nil {
		t.Fatalf("worktree add: %v", err)
	}
	return task, job
}

// --- F1: a ghost Job must not eat a scheduler slot forever ----------------------

// TestReconcile_PerJobLivenessReapsOnlyTheDeadJob is the real fix. Project-level
// liveness could not name a Job, so a crashed Job among healthy siblings survived
// — holding a PerProject slot and its worktree — until the project could never
// dispatch again. Per-Job liveness answers the actual question.
func TestReconcile_PerJobLivenessReapsOnlyTheDeadJob(t *testing.T) {
	repo := gitRepo(t)
	sessions := fakeSessions{liveJobs: map[string]bool{}}
	svc, wt, store := newService(t, mapResolver{"app": repo}, StubRunner{}, sessions)
	svc.SetSchedulerLimits(SchedulerLimits{Global: 8, PerProject: 3})

	_, alive1 := stageWorkingJob(t, svc, store, wt, repo, "app", 0)
	deadTask, dead := stageWorkingJob(t, svc, store, wt, repo, "app", 0)
	_, alive2 := stageWorkingJob(t, svc, store, wt, repo, "app", 0)

	// Two siblings are genuinely alive; one crashed.
	sessions.liveJobs[alive1.ID] = true
	sessions.liveJobs[alive2.ID] = true

	rep, err := svc.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(rep.FailedVanished) != 1 || rep.FailedVanished[0] != dead.ID {
		t.Fatalf("FailedVanished = %v, want exactly [%s]", rep.FailedVanished, dead.ID)
	}
	// The dead one is reaped and gives its slot back…
	got, _ := store.GetJob(dead.ID)
	if got.State != StateFailed {
		t.Errorf("crashed job = %s, want failed", got.State)
	}
	if wt.Exists(dead.ID) {
		t.Error("the crashed job kept its worktree")
	}
	if gotTask, _ := store.GetTask(deadTask.ID); gotTask.State != StateFailed {
		t.Errorf("the crashed job's task = %s, want failed", gotTask.State)
	}
	// …and the healthy siblings are untouched.
	for _, job := range []Job{alive1, alive2} {
		gotJob, _ := store.GetJob(job.ID)
		if gotJob.State != StateWorking {
			t.Errorf("live sibling %s = %s, want working", job.ID, gotJob.State)
		}
		if !wt.Exists(job.ID) {
			t.Errorf("live sibling %s lost its worktree", job.ID)
		}
	}
	// The capacity is genuinely released: 3 running became 2.
	if running, _ := store.CountRunningJobsForProject("app"); running != 2 {
		t.Errorf("running = %d, want 2 — the ghost must stop consuming a slot", running)
	}
}

// TestReconcile_GhostJobsNoLongerExhaustTheProject is the denial-of-service the
// audit described: ghosts accumulate until PerProject is exhausted and the
// project can never dispatch again without a human cancelling each by hand.
func TestReconcile_GhostJobsNoLongerExhaustTheProject(t *testing.T) {
	repo := gitRepo(t)
	sessions := fakeSessions{liveJobs: map[string]bool{}}
	svc, wt, store := newService(t, mapResolver{"app": repo}, StubRunner{Result: ExecSuccess, WriteFile: true}, sessions)
	svc.SetSchedulerLimits(SchedulerLimits{Global: 8, PerProject: 2})

	// Two crashed Jobs fill the project's slots.
	for i := 0; i < 2; i++ {
		stageWorkingJob(t, svc, store, wt, repo, "app", 0)
	}
	fresh, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "new work"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(fresh.ID); err == nil {
		t.Fatal("precondition: the project should be saturated by the ghosts")
	}

	// Reconcile reaps them, and the project can dispatch again — no human needed.
	if _, err := svc.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if _, err := svc.DispatchTask(fresh.ID); err != nil {
		t.Fatalf("dispatch after reaping the ghosts: %v — the project is still wedged", err)
	}
}

// --- the heuristic, and its honest limits ---------------------------------------

// TestReconcile_HeuristicMissingWorktree: the near-certain signal. A `working`
// Job whose isolated checkout is gone cannot be producing anything.
func TestReconcile_HeuristicMissingWorktree(t *testing.T) {
	repo := gitRepo(t)
	// No per-Job observer: the heuristic is the only source.
	svc, wt, store := newService(t, mapResolver{"app": repo}, StubRunner{}, projectOnlySessions{live: map[string]bool{"app": true}})

	_, job := stageWorkingJob(t, svc, store, wt, repo, "app", 0)
	if err := wt.Remove(repo, job.ID); err != nil {
		t.Fatalf("removing the worktree: %v", err)
	}

	rep, err := svc.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(rep.FailedVanished) != 1 || rep.FailedVanished[0] != job.ID {
		t.Errorf("FailedVanished = %v, want [%s]", rep.FailedVanished, job.ID)
	}
	// Note what this proves: the PROJECT observer said "live", and the heuristic
	// overrode it — because that observer is answering about a different key.
	got, _ := store.GetJob(job.ID)
	if got.State != StateFailed {
		t.Errorf("job = %s, want failed", got.State)
	}
}

// TestReconcile_HeuristicOverdueBudget: the guess. A Job still `working` long
// past its own wall-clock budget has almost certainly lost the process that was
// meant to enforce that budget.
func TestReconcile_HeuristicOverdueBudget(t *testing.T) {
	repo := gitRepo(t)
	svc, wt, store := newService(t, mapResolver{"app": repo}, StubRunner{}, projectOnlySessions{})
	now := time.Now().UTC()
	svc.SetClock(func() time.Time { return now })

	_, job := stageWorkingJob(t, svc, store, wt, repo, "app", 60) // 60s budget

	// Well within budget + margin: no opinion, so the Job is left alone.
	rep, err := svc.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(rep.FailedVanished) != 0 {
		t.Fatalf("a Job inside its budget was failed: %v", rep.FailedVanished)
	}
	if rep.SkippedUnverified != 1 {
		t.Errorf("SkippedUnverified = %d, want 1 — an undecidable Job must be left alone", rep.SkippedUnverified)
	}

	// Far past budget × margin + grace: the heuristic calls it dead.
	now = now.Add(2*time.Hour + heuristicGrace)
	rep, err = svc.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(rep.FailedVanished) != 1 || rep.FailedVanished[0] != job.ID {
		t.Fatalf("FailedVanished = %v, want [%s]", rep.FailedVanished, job.ID)
	}
	if len(rep.HeuristicallyFailed) != 1 || rep.HeuristicallyFailed[0] != job.ID {
		t.Errorf("HeuristicallyFailed = %v — a guessed reaping must be reported AS a guess", rep.HeuristicallyFailed)
	}
}

// TestReconcile_HeuristicHasNoOpinionWithoutABudget: no budget and an intact
// worktree means nothing to measure, and the heuristic must say so rather than
// guess. "I don't know" is a valid answer; inventing one is not.
func TestReconcile_HeuristicHasNoOpinionWithoutABudget(t *testing.T) {
	repo := gitRepo(t)
	svc, wt, store := newService(t, mapResolver{"app": repo}, StubRunner{}, projectOnlySessions{})
	_, job := stageWorkingJob(t, svc, store, wt, repo, "app", 0) // no budget

	rep, err := svc.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(rep.FailedVanished) != 0 {
		t.Errorf("a Job the heuristic cannot judge was failed: %v", rep.FailedVanished)
	}
	if rep.SkippedUnverified != 1 {
		t.Errorf("SkippedUnverified = %d, want 1", rep.SkippedUnverified)
	}
	if got, _ := store.GetJob(job.ID); got.State != StateWorking {
		t.Errorf("job = %s, want working (untouched)", got.State)
	}
}

// TestReconcile_PerJobObserverBeatsTheHeuristic: when the real answer is
// available it wins, so a slow-but-alive Job is not reaped by a guess.
func TestReconcile_PerJobObserverBeatsTheHeuristic(t *testing.T) {
	repo := gitRepo(t)
	sessions := fakeSessions{liveJobs: map[string]bool{}}
	svc, wt, store := newService(t, mapResolver{"app": repo}, StubRunner{}, sessions)
	now := time.Now().UTC()
	svc.SetClock(func() time.Time { return now })

	_, job := stageWorkingJob(t, svc, store, wt, repo, "app", 60)
	sessions.liveJobs[job.ID] = true // genuinely alive, just slow

	now = now.Add(10 * time.Hour) // the heuristic would call this dead
	rep, err := svc.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(rep.FailedVanished) != 0 {
		t.Errorf("a Job the observer says is ALIVE was reaped by the heuristic: %v", rep.FailedVanished)
	}
	if got, _ := store.GetJob(job.ID); got.State != StateWorking {
		t.Errorf("job = %s, want working", got.State)
	}
}

// --- F4: a Task wedged with no Job at all ---------------------------------------

// TestReconcile_JoblessTaskIsRecovered: a crash between the `working` transition
// and CreateJob leaves a Task invisible to a Job-only census — not dispatchable,
// retryable or replannable, so only cancel escaped it.
func TestReconcile_JoblessTaskIsRecovered(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo}, StubRunner{Result: ExecSuccess, WriteFile: true}, fakeSessions{})

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// Exactly the crash window: the Task moved, the Job never existed.
	if _, err := store.TransitionTask(task.ID, StateQueued, false, ""); err != nil {
		t.Fatalf("→queued: %v", err)
	}
	if _, err := store.TransitionTask(task.ID, StateWorking, false, ""); err != nil {
		t.Fatalf("→working: %v", err)
	}
	if jobs, _ := store.ListJobsForTask(task.ID); len(jobs) != 0 {
		t.Fatalf("precondition: the task should have no jobs, got %d", len(jobs))
	}
	// Every route out is refused.
	if _, err := svc.DispatchTask(task.ID); err == nil {
		t.Error("precondition: a working task should not be dispatchable")
	}
	if _, err := svc.RetryTask(task.ID, RetryRequest{}); err == nil {
		t.Error("precondition: a working task should not be retryable")
	}

	rep, err := svc.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(rep.RecoveredJoblessTasks) != 1 || rep.RecoveredJoblessTasks[0] != task.ID {
		t.Fatalf("RecoveredJoblessTasks = %v, want [%s]", rep.RecoveredJoblessTasks, task.ID)
	}
	got, _ := store.GetTask(task.ID)
	if got.State != StateRejected {
		t.Errorf("recovered task = %s, want rejected (the state the retry ladder understands)", got.State)
	}
	// And it is usable again without a human cancelling it.
	if _, err := svc.RetryTask(task.ID, RetryRequest{}); err != nil {
		t.Errorf("retry after recovery: %v", err)
	}
}

// TestReconcile_JoblessRecoveryIsIdempotentAndNarrow: it must not touch a Task
// that legitimately has a Job, nor act twice.
func TestReconcile_JoblessRecoveryIsIdempotentAndNarrow(t *testing.T) {
	repo := gitRepo(t)
	sessions := fakeSessions{liveJobs: map[string]bool{}}
	svc, wt, store := newService(t, mapResolver{"app": repo}, StubRunner{}, sessions)

	// A healthy working Task WITH a job, kept alive by the observer.
	_, job := stageWorkingJob(t, svc, store, wt, repo, "app", 0)
	sessions.liveJobs[job.ID] = true

	rep, err := svc.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(rep.RecoveredJoblessTasks) != 0 {
		t.Errorf("a Task WITH a job was 'recovered': %v", rep.RecoveredJoblessTasks)
	}
	// A second pass over the same healthy state changes nothing.
	rep2, _ := svc.Reconcile()
	if len(rep2.RecoveredJoblessTasks) != 0 || len(rep2.FailedVanished) != 0 {
		t.Errorf("second pass was not a no-op: %+v", rep2)
	}
}

// TestJobObserver_HandlesUnusableObservers pins the type-assertion guard. The
// naive form (`assert; ok && sessions != nil`) checks the wrong thing in the
// wrong order: a nil interface already fails the assertion, and the real hazard —
// a NON-nil interface holding a nil pointer whose method set satisfies the
// interface — asserts successfully and then panics on the first dereference.
func TestJobObserver_HandlesUnusableObservers(t *testing.T) {
	tests := []struct {
		name     string
		sessions SessionObserver
		wantOK   bool
	}{
		{"nil interface", nil, false},
		{"observer without per-Job support", projectOnlySessions{}, false},
		{"a usable per-Job observer", fakeSessions{}, true},
		{"a usable per-Job observer behind a pointer", &pointerSessions{}, true},
		// The one that matters: non-nil interface, nil pointer inside.
		{"typed nil pointer", (*pointerSessions)(nil), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			observer, ok := jobObserver(tc.sessions)
			if ok != tc.wantOK {
				t.Fatalf("jobObserver ok = %v, want %v", ok, tc.wantOK)
			}
			if ok {
				// It must be callable, not merely non-nil.
				if _, err := observer.HasSessionForJob("J-1"); err != nil {
					t.Errorf("HasSessionForJob: %v", err)
				}
			}
		})
	}
}

// TestJobLive_TypedNilObserverFallsBackWithoutPanicking is the same hazard end to
// end: an unusable observer must degrade to the heuristic, not crash reconcile.
func TestJobLive_TypedNilObserverFallsBackWithoutPanicking(t *testing.T) {
	repo := gitRepo(t)
	svc, wt, store := newService(t, mapResolver{"app": repo}, StubRunner{}, (*pointerSessions)(nil))

	_, job := stageWorkingJob(t, svc, store, wt, repo, "app", 0)
	// The worktree is gone, so the heuristic — which must be reached — condemns it.
	if err := wt.Remove(repo, job.ID); err != nil {
		t.Fatalf("removing the worktree: %v", err)
	}

	rep, err := svc.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(rep.FailedVanished) != 1 || rep.FailedVanished[0] != job.ID {
		t.Fatalf("FailedVanished = %v, want [%s] — the heuristic should have been reached", rep.FailedVanished, job.ID)
	}
	if len(rep.HeuristicallyFailed) != 1 {
		t.Errorf("HeuristicallyFailed = %v, want the fallback to be reported as a guess", rep.HeuristicallyFailed)
	}
}

// pointerSessions is a per-Job observer with POINTER receivers, so a typed nil
// satisfies the interface and panics if actually called.
type pointerSessions struct{ live map[string]bool }

func (p *pointerSessions) HasSession(project string) (bool, error) { return p.live[project], nil }

func (p *pointerSessions) HasSessionForJob(jobID string) (bool, error) { return p.live[jobID], nil }
