// Copyright (C) 2026 Techdelight BV

package control

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// req is a compact admission request for the table tests.
func req(taskID, project string, projectRunning, globalRunning, taskConcurrency int) admissionRequest {
	return admissionRequest{
		taskID: taskID, project: project,
		projectRunning: projectRunning, globalRunning: globalRunning,
		taskConcurrency: taskConcurrency,
	}
}

func reasonOf(t *testing.T, err error) RejectionReason {
	t.Helper()
	reason, refused := Rejected(err)
	if !refused {
		t.Fatalf("err = %v, want a typed rejection", err)
	}
	return reason
}

// --- limits ---------------------------------------------------------------------

func TestScheduler_Limits(t *testing.T) {
	tests := []struct {
		name       string
		limits     SchedulerLimits
		request    admissionRequest
		wantReason RejectionReason // "" = admitted
	}{
		{
			name:    "under every limit",
			limits:  SchedulerLimits{Global: 4, PerProject: 2},
			request: req("T-1", "app", 1, 2, 0),
		},
		{
			name:       "per-project limit reached",
			limits:     SchedulerLimits{Global: 8, PerProject: 2},
			request:    req("T-1", "app", 2, 2, 0),
			wantReason: ReasonConcurrencyExceeded,
		},
		{
			name:       "global limit reached, project has room",
			limits:     SchedulerLimits{Global: 3, PerProject: 5},
			request:    req("T-1", "app", 1, 3, 0),
			wantReason: ReasonSchedulerSaturated,
		},
		{
			name:       "the TASK's own budget axis is tighter than the project limit",
			limits:     SchedulerLimits{Global: 8, PerProject: 5},
			request:    req("T-1", "app", 1, 1, 1),
			wantReason: ReasonConcurrencyExceeded,
		},
		{
			name:    "a zero limit is unbounded, not zero-capacity",
			limits:  SchedulerLimits{Global: 0, PerProject: 0},
			request: req("T-1", "app", 99, 99, 0),
		},
		{
			name:    "a zero task budget axis does not bind",
			limits:  SchedulerLimits{Global: 8, PerProject: 4},
			request: req("T-1", "app", 2, 2, 0),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewScheduler(tc.limits)
			err := s.admit(tc.request)
			if tc.wantReason == "" {
				if err != nil {
					t.Fatalf("admit = %v, want admitted", err)
				}
				return
			}
			if got := reasonOf(t, err); got != tc.wantReason {
				t.Errorf("reason = %q, want %q", got, tc.wantReason)
			}
		})
	}
}

// TestScheduler_TightestLimitWins: the per-Task budget axis and the operator's
// per-project limit both apply, and whichever is smaller binds. Until Sprint 61
// the budget axis could effectively never fire (Sprint 58 audit finding 11)
// because only one Job per project was possible at all.
func TestScheduler_TightestLimitWins(t *testing.T) {
	tests := []struct {
		name             string
		perProject, task int
		running          int
		wantBinding      int
		wantExceeded     bool
	}{
		{"task axis is tighter", 5, 2, 2, 2, true},
		{"project limit is tighter", 2, 5, 2, 2, true},
		{"both unbounded", 0, 0, 99, 0, false},
		{"only the task axis set", 0, 3, 3, 3, true},
		{"room under the tighter of the two", 5, 3, 2, 3, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			binding, exceeded := tightestLimit(tc.running, tc.perProject, tc.task)
			if binding != tc.wantBinding || exceeded != tc.wantExceeded {
				t.Errorf("tightestLimit(%d, %d, %d) = (%d, %v), want (%d, %v)",
					tc.running, tc.perProject, tc.task, binding, exceeded, tc.wantBinding, tc.wantExceeded)
			}
		})
	}
}

// --- saturation and release-and-admit -------------------------------------------

func TestScheduler_SaturateThenRelease(t *testing.T) {
	s := NewScheduler(SchedulerLimits{Global: 2, PerProject: 2})

	// Two admitted, filling the project.
	if err := s.admit(req("T-1", "app", 0, 0, 0)); err != nil {
		t.Fatalf("first admit: %v", err)
	}
	if err := s.admit(req("T-2", "app", 1, 1, 0)); err != nil {
		t.Fatalf("second admit: %v", err)
	}
	// The third is refused while they run.
	err := s.admit(req("T-3", "app", 2, 2, 0))
	if got := reasonOf(t, err); got != ReasonConcurrencyExceeded {
		t.Fatalf("third admit reason = %q, want %q", got, ReasonConcurrencyExceeded)
	}
	if waiting := s.Waiting(); len(waiting) != 1 || waiting[0] != "T-3" {
		t.Errorf("Waiting() = %v, want [T-3]", waiting)
	}
	// A Job finishes: the same request now succeeds, and the ticket is dropped.
	if err := s.admit(req("T-3", "app", 1, 1, 0)); err != nil {
		t.Fatalf("admit after release: %v", err)
	}
	if waiting := s.Waiting(); len(waiting) != 0 {
		t.Errorf("Waiting() = %v, want empty after admission", waiting)
	}
}

// --- fairness -------------------------------------------------------------------

// TestScheduler_FairnessNoStarvation is the anti-starvation rule. Without it a
// project dispatching in a tight loop starves the Task that asked first — and
// because a refusal is a typed rejection the caller retries, so the starved Task
// would retry forever while newer work sails past.
func TestScheduler_FairnessNoStarvation(t *testing.T) {
	s := NewScheduler(SchedulerLimits{Global: 4, PerProject: 1})

	// T-1 asks first and is refused: the project is busy.
	if got := reasonOf(t, s.admit(req("T-1", "app", 1, 1, 0))); got != ReasonConcurrencyExceeded {
		t.Fatalf("T-1 reason = %q", got)
	}
	// T-2 asks later and is also refused.
	if got := reasonOf(t, s.admit(req("T-2", "app", 1, 1, 0))); got != ReasonConcurrencyExceeded {
		t.Fatalf("T-2 reason = %q", got)
	}
	if waiting := s.Waiting(); len(waiting) != 2 || waiting[0] != "T-1" || waiting[1] != "T-2" {
		t.Fatalf("Waiting() = %v, want [T-1 T-2] oldest first", waiting)
	}

	// Capacity frees. The NEWER task must yield to the older one.
	err := s.admit(req("T-2", "app", 0, 0, 0))
	if got := reasonOf(t, err); got != ReasonQueuedBehind {
		t.Fatalf("T-2 with capacity available = %q, want %q (it must yield to T-1)", got, ReasonQueuedBehind)
	}
	var rej *RejectionError
	if errors.As(err, &rej) && !strings.Contains(rej.Message, "T-1") {
		t.Errorf("the refusal should name the task being yielded to: %q", rej.Message)
	}
	// The older one takes it.
	if err := s.admit(req("T-1", "app", 0, 0, 0)); err != nil {
		t.Fatalf("T-1 with capacity available: %v", err)
	}
	// And now T-2 is the oldest waiter, so the next free slot is its.
	if err := s.admit(req("T-2", "app", 0, 0, 0)); err != nil {
		t.Fatalf("T-2 after T-1 was admitted: %v", err)
	}
}

// TestScheduler_FairnessIsPerProject: a Task waiting on project A must not block
// a Task on project B — freeing A's slot does nothing for B, so making B wait
// would be starvation dressed up as fairness.
func TestScheduler_FairnessIsPerProject(t *testing.T) {
	s := NewScheduler(SchedulerLimits{Global: 8, PerProject: 1})

	// A-1 waits on project A.
	if reasonOf(t, s.admit(req("A-1", "alpha", 1, 1, 0))) != ReasonConcurrencyExceeded {
		t.Fatal("A-1 should have been refused")
	}
	// B-1 asks on project B, which has room. It must be admitted despite A-1
	// having waited longer.
	if err := s.admit(req("B-1", "beta", 0, 1, 0)); err != nil {
		t.Errorf("B-1 on a different project = %v, want admitted (A-1's queue is not B-1's)", err)
	}
}

// TestScheduler_GlobalWaiterBlocksEveryProject: the global limit IS shared, so a
// Task waiting on it competes with every project rather than one.
func TestScheduler_GlobalWaiterBlocksEveryProject(t *testing.T) {
	s := NewScheduler(SchedulerLimits{Global: 1, PerProject: 4})

	// A-1 is refused on the global limit.
	if got := reasonOf(t, s.admit(req("A-1", "alpha", 0, 1, 0))); got != ReasonSchedulerSaturated {
		t.Fatalf("A-1 reason = %q, want %q", got, ReasonSchedulerSaturated)
	}
	// Capacity frees globally. A newer Task on ANOTHER project must yield.
	if got := reasonOf(t, s.admit(req("B-1", "beta", 0, 0, 0))); got != ReasonQueuedBehind {
		t.Errorf("B-1 = %q, want %q — a global waiter competes with every project", got, ReasonQueuedBehind)
	}
	if err := s.admit(req("A-1", "alpha", 0, 0, 0)); err != nil {
		t.Errorf("A-1 taking the freed global slot: %v", err)
	}
}

// TestScheduler_ForgetReleasesTheQueueHead: a Task that will never run must not
// block others forever by holding the oldest ticket.
func TestScheduler_ForgetReleasesTheQueueHead(t *testing.T) {
	s := NewScheduler(SchedulerLimits{Global: 4, PerProject: 1})
	if reasonOf(t, s.admit(req("T-1", "app", 1, 1, 0))) != ReasonConcurrencyExceeded {
		t.Fatal("T-1 should have been refused")
	}
	if reasonOf(t, s.admit(req("T-2", "app", 1, 1, 0))) != ReasonConcurrencyExceeded {
		t.Fatal("T-2 should have been refused")
	}
	// T-1 is cancelled.
	s.Forget("T-1")
	if waiting := s.Waiting(); len(waiting) != 1 || waiting[0] != "T-2" {
		t.Fatalf("Waiting() = %v, want [T-2]", waiting)
	}
	if err := s.admit(req("T-2", "app", 0, 0, 0)); err != nil {
		t.Errorf("T-2 after the head was forgotten = %v, want admitted", err)
	}
}

func TestScheduler_DefaultLimitsPreserveOldBehaviour(t *testing.T) {
	// Lifting the invariant changes what the plane CAN do; it must not silently
	// change what an existing installation DOES do.
	l := DefaultSchedulerLimits()
	if l.PerProject != 1 {
		t.Errorf("default PerProject = %d, want 1 — parallelism must be opt-in", l.PerProject)
	}
	if l.Global <= 0 {
		t.Errorf("default Global = %d, want a real bound", l.Global)
	}
}

// --- ticket liveness (audit F2) --------------------------------------------------

// TestScheduler_AbandonedTicketDoesNotBlockForever is the regression for the
// audit's blocking finding. Fairness without liveness is a deadlock: a Task
// refused for capacity kept its place while sitting in `planned`, and nothing
// woke it — dispatch is synchronous, so the queue only advanced if a human
// re-issued dispatch for that exact Task. One abandoned attempt bricked a
// project's parallelism.
//
// The invariant restored: free capacity must eventually become usable WITHOUT
// human intervention.
func TestScheduler_AbandonedTicketDoesNotBlockForever(t *testing.T) {
	s := NewScheduler(SchedulerLimits{Global: 4, PerProject: 1})
	s.SetTicketLiveness(time.Minute, 2, nil)

	// B asks while A is running, is refused, and then walks away.
	if reasonOf(t, s.admit(req("B", "app", 1, 1, 0))) != ReasonConcurrencyExceeded {
		t.Fatal("B should have been refused")
	}
	// A finishes: capacity is free, but B still holds the queue head.
	if got := reasonOf(t, s.admit(req("C", "app", 0, 0, 0))); got != ReasonQueuedBehind {
		t.Fatalf("C = %q, want %q while B's ticket is live", got, ReasonQueuedBehind)
	}
	// C keeps asking. Each attempt spends one of B's passovers; B never re-asks.
	var admitted bool
	for i := 0; i < 5; i++ {
		if err := s.admit(req("C", "app", 0, 0, 0)); err == nil {
			admitted = true
			break
		}
	}
	if !admitted {
		t.Fatal("C was never admitted — an abandoned ticket blocked free capacity indefinitely")
	}
	if waiting := s.Waiting(); len(waiting) != 0 {
		t.Errorf("Waiting() = %v, want empty (B lapsed, C admitted)", waiting)
	}
}

// TestScheduler_AbandonedGlobalTicketDoesNotStallEveryProject is the worse
// variant: a `scheduler_saturated` ticket is filed against the shared global
// limit, so an abandoned one stalls the ENTIRE plane, across projects.
func TestScheduler_AbandonedGlobalTicketDoesNotStallEveryProject(t *testing.T) {
	s := NewScheduler(SchedulerLimits{Global: 1, PerProject: 4})
	s.SetTicketLiveness(time.Minute, 2, nil)

	// A-1 is refused on the GLOBAL limit and abandoned.
	if got := reasonOf(t, s.admit(req("A-1", "alpha", 0, 1, 0))); got != ReasonSchedulerSaturated {
		t.Fatalf("A-1 = %q, want %q", got, ReasonSchedulerSaturated)
	}
	// The global slot frees. A Task on a DIFFERENT project must not be stalled
	// forever by a waiter that stopped asking.
	var admitted bool
	for i := 0; i < 6; i++ {
		if err := s.admit(req("B-1", "beta", 0, 0, 0)); err == nil {
			admitted = true
			break
		}
	}
	if !admitted {
		t.Fatal("B-1 was never admitted — an abandoned GLOBAL ticket stalled another project indefinitely")
	}
}

// TestScheduler_TicketTTLExpires covers the quiet-queue case: nobody is pushing,
// so there are no passovers to spend, and only the wall clock heals it.
func TestScheduler_TicketTTLExpires(t *testing.T) {
	now := time.Now()
	s := NewScheduler(SchedulerLimits{Global: 4, PerProject: 1})
	s.SetTicketLiveness(30*time.Second, 100, func() time.Time { return now })

	if reasonOf(t, s.admit(req("B", "app", 1, 1, 0))) != ReasonConcurrencyExceeded {
		t.Fatal("B should have been refused")
	}
	if waiting := s.Waiting(); len(waiting) != 1 {
		t.Fatalf("Waiting() = %v, want [B]", waiting)
	}
	// Still inside the lease: B keeps its place even with capacity free.
	now = now.Add(20 * time.Second)
	if got := reasonOf(t, s.admit(req("C", "app", 0, 0, 0))); got != ReasonQueuedBehind {
		t.Errorf("C inside B's lease = %q, want %q", got, ReasonQueuedBehind)
	}
	// Past the lease: B has lapsed and C takes the capacity.
	now = now.Add(30 * time.Second)
	if err := s.admit(req("C", "app", 0, 0, 0)); err != nil {
		t.Errorf("C after B's lease expired = %v, want admitted", err)
	}
}

// TestScheduler_ReAskingRenewsWithoutLosingPlace: a waiter that keeps asking
// keeps its place. Renewal must not send it to the back of the queue, or a busy
// project would starve the very Task the fairness rule exists to protect.
func TestScheduler_ReAskingRenewsWithoutLosingPlace(t *testing.T) {
	now := time.Now()
	s := NewScheduler(SchedulerLimits{Global: 4, PerProject: 1})
	s.SetTicketLiveness(30*time.Second, 2, func() time.Time { return now })

	// B asks first, then C.
	if reasonOf(t, s.admit(req("B", "app", 1, 1, 0))) != ReasonConcurrencyExceeded {
		t.Fatal("B should have been refused")
	}
	if reasonOf(t, s.admit(req("C", "app", 1, 1, 0))) != ReasonConcurrencyExceeded {
		t.Fatal("C should have been refused")
	}
	// B keeps asking across a span longer than the TTL, renewing each time.
	for i := 0; i < 4; i++ {
		now = now.Add(20 * time.Second)
		if reasonOf(t, s.admit(req("B", "app", 1, 1, 0))) != ReasonConcurrencyExceeded {
			t.Fatalf("B renewal %d should still be refused for capacity", i)
		}
	}
	// B is still ahead of C.
	if waiting := s.Waiting(); len(waiting) == 0 || waiting[0] != "B" {
		t.Fatalf("Waiting() = %v, want B still at the head", waiting)
	}
	// Capacity frees: B takes it, not C.
	if got := reasonOf(t, s.admit(req("C", "app", 0, 0, 0))); got != ReasonQueuedBehind {
		t.Errorf("C = %q, want to yield to the renewing B", got)
	}
	if err := s.admit(req("B", "app", 0, 0, 0)); err != nil {
		t.Errorf("B taking the capacity it queued for: %v", err)
	}
}

// TestScheduler_PassoversResetOnRenewal: a Task that is still asking must not be
// aged out by its own competitors.
func TestScheduler_PassoversResetOnRenewal(t *testing.T) {
	s := NewScheduler(SchedulerLimits{Global: 4, PerProject: 1})
	s.SetTicketLiveness(time.Minute, 2, nil)

	if reasonOf(t, s.admit(req("B", "app", 1, 1, 0))) != ReasonConcurrencyExceeded {
		t.Fatal("B should have been refused")
	}
	for i := 0; i < 6; i++ {
		// C pushes (spending B's passovers), but B re-asks each round.
		_ = s.admit(req("C", "app", 0, 0, 0))
		if reasonOf(t, s.admit(req("B", "app", 1, 1, 0))) != ReasonConcurrencyExceeded {
			t.Fatalf("round %d: B should still be refused for capacity", i)
		}
	}
	if waiting := s.Waiting(); len(waiting) == 0 || waiting[0] != "B" {
		t.Errorf("Waiting() = %v, want B still queued — a live waiter must not be aged out", waiting)
	}
}

// --- the concurrency axis refuses an over-ask (audit F3) -------------------------

// TestCreateTask_ConcurrencyOverAskIsRefused: since the budget's concurrency
// default became unset (= unbounded), the generic ceiling check could no longer
// refuse a concurrency ask — a request for 1000 was stored and echoed back by
// `task status` as though it were the limit. Every other axis refuses an over-ask
// out loud; this one must too, rather than storing a fiction.
func TestCreateTask_ConcurrencyOverAskIsRefused(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)
	svc.SetSchedulerLimits(SchedulerLimits{Global: 8, PerProject: 2})

	_, err := svc.CreateTask(CreateTaskRequest{
		Project: "app", Objective: "greedy", Budget: &Budget{Concurrency: 1000},
	})
	var rej *RejectionError
	if !errors.As(err, &rej) {
		t.Fatalf("over-ask on concurrency = %v, want a typed refusal", err)
	}
	if rej.Reason != ReasonOverBudget {
		t.Errorf("reason = %q, want %q", rej.Reason, ReasonOverBudget)
	}
	if !strings.Contains(rej.Message, "concurrency") {
		t.Errorf("the refusal should name the axis: %q", rej.Message)
	}
	// Nothing was stored, so `task status` cannot report a limit that isn't one.
	if tasks, _ := store.ListTasks(); len(tasks) != 0 {
		t.Errorf("an over-ask created %d task(s), want none", len(tasks))
	}

	// Asking for exactly the limit, or less, is fine and is what gets stored.
	task, err := svc.CreateTask(CreateTaskRequest{
		Project: "app", Objective: "modest", Budget: &Budget{Concurrency: 1},
	})
	if err != nil {
		t.Fatalf("a narrowing ask should be allowed: %v", err)
	}
	if task.Budget.Concurrency != 1 {
		t.Errorf("stored concurrency = %d, want the requested 1", task.Budget.Concurrency)
	}
	// And with no per-project limit configured, there is nothing to exceed.
	svc.SetSchedulerLimits(SchedulerLimits{Global: 8, PerProject: 0})
	if _, err := svc.CreateTask(CreateTaskRequest{
		Project: "app", Objective: "unbounded project", Budget: &Budget{Concurrency: 50},
	}); err != nil {
		t.Errorf("with no per-project limit, any ask is within bounds: %v", err)
	}
}
