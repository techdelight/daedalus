// Copyright (C) 2026 Techdelight BV

package control

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// fixedClock returns a deterministic, monotonically-advancing clock so
// timestamps in tests are stable and ordered without depending on wall time.
func fixedClock() Clock {
	base := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	var n int64
	return func() time.Time {
		t := base.Add(time.Duration(n) * time.Second)
		n++
		return t
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "control.db")
	s, err := Open(path, WithClock(fixedClock()))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCreateReadListTask(t *testing.T) {
	s := openTestStore(t)

	t1, err := s.CreateTask("proj-a", "do the thing", "acc@abc", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", StatePlanned)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if t1.ID != "T-1" {
		t.Errorf("first task id = %q, want T-1", t1.ID)
	}
	if t1.State != StatePlanned {
		t.Errorf("state = %q, want planned", t1.State)
	}

	got, err := s.GetTask("T-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Objective != "do the thing" || got.AcceptanceRef != "acc@abc" {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	// Cancel t1 so proj-a is free, then create a second task on a different
	// project to exercise id increment.
	if _, err := s.TransitionTask("T-1", StateCancelled, false, ""); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	t2, err := s.CreateTask("proj-b", "second", "", "cafebabecafebabecafebabecafebabecafebabe", StatePlanned)
	if err != nil {
		t.Fatalf("CreateTask 2: %v", err)
	}
	if t2.ID != "T-2" {
		t.Errorf("second task id = %q, want T-2", t2.ID)
	}

	all, err := s.ListTasks()
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListTasks len = %d, want 2", len(all))
	}
	if all[0].ID != "T-1" || all[1].ID != "T-2" {
		t.Errorf("list order = %s,%s want T-1,T-2", all[0].ID, all[1].ID)
	}
}

func TestGetTask_NotFound(t *testing.T) {
	s := openTestStore(t)
	_, err := s.GetTask("T-999")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetTask(missing) err = %v, want ErrNotFound", err)
	}
}

func TestOneActiveTaskPerProject(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.CreateTask("proj", "first", "", "sha1", StatePlanned); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := s.CreateTask("proj", "second", "", "sha1", StatePlanned)
	var active *ErrActiveTaskExists
	if !errors.As(err, &active) {
		t.Fatalf("second create err = %v, want ErrActiveTaskExists", err)
	}
	if active.ExistingID != "T-1" {
		t.Errorf("conflict points at %q, want T-1", active.ExistingID)
	}

	// Cancelling the first frees the project.
	if _, err := s.TransitionTask("T-1", StateCancelled, false, ""); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := s.CreateTask("proj", "third", "", "sha1", StatePlanned); err != nil {
		t.Errorf("create after cancel should succeed, got %v", err)
	}
}

func TestLegalTransitionLogsEvent(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.CreateTask("proj", "obj", "", "sha", StatePlanned); err != nil {
		t.Fatalf("create: %v", err)
	}
	// planned → queued → working (both legal, plane-driven).
	if _, err := s.TransitionTask("T-1", StateQueued, false, "dispatch"); err != nil {
		t.Fatalf("planned→queued: %v", err)
	}
	if _, err := s.TransitionTask("T-1", StateWorking, false, "start"); err != nil {
		t.Fatalf("queued→working: %v", err)
	}
	// worker: working → candidate.
	got, err := s.TransitionTask("T-1", StateCandidate, true, "i think im done")
	if err != nil {
		t.Fatalf("working→candidate (worker): %v", err)
	}
	if got.State != StateCandidate {
		t.Errorf("state = %q, want candidate", got.State)
	}

	events, err := s.ListEventsFor("task", "T-1")
	if err != nil {
		t.Fatalf("ListEventsFor: %v", err)
	}
	// created + 3 transitions = 4 events.
	if len(events) != 4 {
		t.Fatalf("event count = %d, want 4: %+v", len(events), events)
	}
	// Last event is the worker candidate transition.
	last := events[3]
	if last.From != StateWorking || last.To != StateCandidate || last.Actor != ActorWorker {
		t.Errorf("last event = %+v, want working→candidate by worker", last)
	}
	// Seq is strictly increasing (append-only ordering).
	for i := 1; i < len(events); i++ {
		if events[i].Seq <= events[i-1].Seq {
			t.Errorf("event seq not increasing at %d: %d <= %d", i, events[i].Seq, events[i-1].Seq)
		}
	}
}

func TestIllegalTransitionRejectedAndLogsNothing(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.CreateTask("proj", "obj", "", "sha", StatePlanned); err != nil {
		t.Fatalf("create: %v", err)
	}
	beforeAll, _ := s.ListEvents()

	// Illegal: planned → verified (skips the whole machine).
	_, err := s.TransitionTask("T-1", StateVerified, false, "cheat")
	if !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("planned→verified err = %v, want ErrIllegalTransition", err)
	}

	// Illegal for a worker even though legal for the plane: candidate → verified.
	// First drive to candidate legally.
	mustTransition(t, s, "T-1", StateQueued, false)
	mustTransition(t, s, "T-1", StateWorking, false)
	mustTransition(t, s, "T-1", StateCandidate, true)
	mustTransition(t, s, "T-1", StateVerifying, false)
	_, err = s.TransitionTask("T-1", StateVerified, true, "worker self-verify")
	if !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("worker verifying→verified err = %v, want ErrIllegalTransition", err)
	}

	// The task state must still be verifying (the illegal move changed nothing).
	got, _ := s.GetTask("T-1")
	if got.State != StateVerifying {
		t.Errorf("state after rejected worker move = %q, want verifying", got.State)
	}

	// No event was logged for either *rejected* attempt. Count legal events only.
	afterAll, _ := s.ListEvents()
	// beforeAll had 1 (created). Legal moves after: queued, working, candidate,
	// verifying = 4. Total legal = 5. The two illegal attempts logged nothing.
	if len(beforeAll) != 1 {
		t.Fatalf("precondition: created event count = %d, want 1", len(beforeAll))
	}
	if len(afterAll) != 5 {
		t.Errorf("event count = %d, want 5 (illegal attempts must not log)", len(afterAll))
	}
}

func TestStaleTransitionConflict(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.CreateTask("proj", "obj", "", "sha", StatePlanned); err != nil {
		t.Fatalf("create: %v", err)
	}
	mustTransition(t, s, "T-1", StateQueued, false)
	// Now the task is queued. A caller that believed it was still planned would
	// try planned→queued again; that's a no-op-on-wrong-state. We simulate the
	// atomic guard directly: transitioning queued→queued is illegal anyway, but
	// the optimistic guard also protects legal-shaped stale writes. Assert a
	// terminal task cannot be transitioned (conflict/illegal).
	mustTransition(t, s, "T-1", StateCancelled, false)
	_, err := s.TransitionTask("T-1", StateQueued, false, "")
	if err == nil {
		t.Error("transition from terminal cancelled should fail")
	}
}

func TestJobAndArtifactLifecycle(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.CreateTask("proj", "obj", "", "base-sha", StatePlanned); err != nil {
		t.Fatalf("create task: %v", err)
	}
	j, err := s.CreateJob("T-1", "base-sha", "claude", 3600, StateQueued)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if j.ID != "J-1" {
		t.Errorf("job id = %q, want J-1", j.ID)
	}

	// Record execution outcome + salvage snapshot.
	j, err = s.SetJobExecutionResult("J-1", ExecSuccess, "headsha123")
	if err != nil {
		t.Fatalf("SetJobExecutionResult: %v", err)
	}
	if j.ExecutionResult != ExecSuccess || j.OutputSnapshot != "headsha123" {
		t.Errorf("job result not persisted: %+v", j)
	}

	a, err := s.CreateArtifact("J-1", "base-sha", "headsha123", "daedalus/T-1/J-1")
	if err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if a.ID != "A-1" || a.Verify != VerifyPending || a.Review != ReviewPending {
		t.Errorf("artifact defaults wrong: %+v", a)
	}

	jobs, _ := s.ListJobsForTask("T-1")
	if len(jobs) != 1 {
		t.Errorf("ListJobsForTask len = %d, want 1", len(jobs))
	}
	arts, _ := s.ListArtifactsForJob("J-1")
	if len(arts) != 1 {
		t.Errorf("ListArtifactsForJob len = %d, want 1", len(arts))
	}

	// Creating a job for a missing task fails.
	if _, err := s.CreateJob("T-999", "x", "claude", 0, StateQueued); !errors.Is(err, ErrNotFound) {
		t.Errorf("CreateJob(missing task) err = %v, want ErrNotFound", err)
	}
}

func TestReopenPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	s1, err := Open(path, WithClock(fixedClock()))
	if err != nil {
		t.Fatalf("Open 1: %v", err)
	}
	if _, err := s1.CreateTask("proj", "persist me", "", "sha", StatePlanned); err != nil {
		t.Fatalf("create: %v", err)
	}
	s1.Close()

	// Reopen: migration is idempotent, data survives.
	s2, err := Open(path, WithClock(fixedClock()))
	if err != nil {
		t.Fatalf("Open 2: %v", err)
	}
	defer s2.Close()
	got, err := s2.GetTask("T-1")
	if err != nil {
		t.Fatalf("GetTask after reopen: %v", err)
	}
	if got.Objective != "persist me" {
		t.Errorf("objective = %q, want 'persist me'", got.Objective)
	}
	// Next id continues from the sqlite_sequence high-water mark.
	t2, err := s2.CreateTask("proj2", "next", "", "sha", StatePlanned)
	if err != nil {
		t.Fatalf("create after reopen: %v", err)
	}
	if t2.ID != "T-2" {
		t.Errorf("id after reopen = %q, want T-2", t2.ID)
	}
}

func mustTransition(t *testing.T, s *Store, id string, to State, byWorker bool) {
	t.Helper()
	if _, err := s.TransitionTask(id, to, byWorker, ""); err != nil {
		t.Fatalf("transition %s → %s (worker=%v): %v", id, to, byWorker, err)
	}
}
