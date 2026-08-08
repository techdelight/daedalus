// Copyright (C) 2026 Techdelight BV

package control

import "testing"

// TestCanTransition_Exhaustive walks every (from, to) pair across all states and
// asserts the plane-level legality table exactly. This pins the state machine so
// an accidental edit to legalTransitions is caught.
func TestCanTransition_Exhaustive(t *testing.T) {
	// The complete set of legal plane transitions, spelled out independently of
	// the implementation map so the test is a real cross-check.
	legal := map[State][]State{
		StatePlanned:          {StateQueued, StateCancelled, StateExpired, StateFailed},
		StateQueued:           {StateWorking, StateCancelled, StateExpired, StateFailed},
		StateWorking:          {StateCandidate, StateInputRequired, StateCancelled, StateExpired, StateFailed},
		StateInputRequired:    {StateWorking, StateCancelled, StateExpired, StateFailed},
		StateCandidate:        {StateVerifying, StateRejected, StateCancelled, StateExpired, StateFailed},
		StateVerifying:        {StateVerified, StateRejected, StateCancelled, StateExpired, StateFailed},
		StateVerified:         {StateApprovalRequired, StateCancelled, StateExpired},
		StateRejected:         {StateQueued, StatePlanned, StateCancelled, StateExpired},
		StateApprovalRequired: {StateApproved, StateRejected, StateCancelled, StateExpired},
		StateApproved:         {StateIntegrated, StateCancelled},
		// terminal states: no outgoing edges
		StateIntegrated: {},
		StateFailed:     {},
		StateCancelled:  {},
		StateExpired:    {},
	}

	legalSet := func(from State) map[State]bool {
		m := map[State]bool{}
		for _, to := range legal[from] {
			m[to] = true
		}
		return m
	}

	all := AllStates()
	for _, from := range all {
		want := legalSet(from)
		for _, to := range all {
			got := CanTransition(from, to)
			if got != want[to] {
				t.Errorf("CanTransition(%s → %s) = %v, want %v", from, to, got, want[to])
			}
		}
	}
}

// TestTerminalStates_NoOutgoing confirms terminal states have zero legal moves.
func TestTerminalStates_NoOutgoing(t *testing.T) {
	for _, from := range []State{StateIntegrated, StateFailed, StateCancelled, StateExpired} {
		if !IsTerminal(from) {
			t.Errorf("IsTerminal(%s) = false, want true", from)
		}
		if IsActive(from) {
			t.Errorf("IsActive(%s) = true, want false", from)
		}
		for _, to := range AllStates() {
			if CanTransition(from, to) {
				t.Errorf("terminal %s should have no move, but %s → %s is legal", from, from, to)
			}
		}
	}
}

// TestWorkerCannotReachVerified is the load-bearing invariant: no worker-driven
// transition may reach `verified` (or any state beyond candidate). The worker's
// entire reachable set is {working→candidate, working→input_required,
// input_required→working}.
func TestWorkerCannotReachVerified(t *testing.T) {
	// Enumerate the only transitions a worker may perform.
	wantWorker := map[State]map[State]bool{
		StateWorking:       {StateCandidate: true, StateInputRequired: true},
		StateInputRequired: {StateWorking: true},
	}
	for _, from := range AllStates() {
		for _, to := range AllStates() {
			got := WorkerCanTransition(from, to)
			want := wantWorker[from][to]
			if got != want {
				t.Errorf("WorkerCanTransition(%s → %s) = %v, want %v", from, to, got, want)
			}
		}
	}

	// Specifically: a worker can never produce `verified` from anywhere.
	for _, from := range AllStates() {
		if WorkerCanTransition(from, StateVerified) {
			t.Errorf("worker illegally reached verified from %s", from)
		}
	}
	// But the control plane can, from candidate/verifying.
	if !CanTransition(StateVerifying, StateVerified) {
		t.Error("control plane should be able to move verifying → verified")
	}
}

// TestWorkerIsSubsetOfPlane asserts every worker-legal move is also plane-legal
// (the worker table is a restriction, never an extension, of authority).
func TestWorkerIsSubsetOfPlane(t *testing.T) {
	for _, from := range AllStates() {
		for _, to := range AllStates() {
			if WorkerCanTransition(from, to) && !CanTransition(from, to) {
				t.Errorf("worker move %s → %s is not plane-legal — worker must be a subset", from, to)
			}
		}
	}
}

// TestCanTransitionBy dispatches correctly on the byWorker flag.
func TestCanTransitionBy(t *testing.T) {
	// working → candidate: legal for both.
	if !CanTransitionBy(StateWorking, StateCandidate, true) {
		t.Error("worker working → candidate should be legal")
	}
	if !CanTransitionBy(StateWorking, StateCandidate, false) {
		t.Error("plane working → candidate should be legal")
	}
	// verifying → verified: plane only.
	if CanTransitionBy(StateVerifying, StateVerified, true) {
		t.Error("worker verifying → verified must be illegal")
	}
	if !CanTransitionBy(StateVerifying, StateVerified, false) {
		t.Error("plane verifying → verified should be legal")
	}
	// planned → queued: plane only (dispatch), not a worker move.
	if CanTransitionBy(StatePlanned, StateQueued, true) {
		t.Error("worker planned → queued must be illegal")
	}
	if !CanTransitionBy(StatePlanned, StateQueued, false) {
		t.Error("plane planned → queued should be legal")
	}
}

// TestExecutionResultValidation covers the closed execution_result set.
func TestExecutionResultValidation(t *testing.T) {
	for _, r := range []ExecutionResult{ExecNone, ExecSuccess, ExecFailed, ExecTimeout, ExecCancelled} {
		if !IsValidExecutionResult(r) {
			t.Errorf("IsValidExecutionResult(%q) = false, want true", r)
		}
	}
	if IsValidExecutionResult(ExecutionResult("bogus")) {
		t.Error("IsValidExecutionResult(bogus) = true, want false")
	}
}
