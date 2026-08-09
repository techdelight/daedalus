// Copyright (C) 2026 Techdelight BV

package control

import (
	"errors"
	"strings"
	"testing"
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
