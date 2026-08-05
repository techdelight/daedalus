// Copyright (C) 2026 Techdelight BV

package core

// SprintPhase is where a sprint sits in the ship pipeline. Milestone 6 frames
// sprints by this flow state — how agentic development actually moves (verified
// batches that cut a release) — rather than by calendar time (past/current/
// future). It is derived, not stored: PhaseOf reads it off a sprint's existing
// Version and item statuses, so there is no new schema.
type SprintPhase string

const (
	// PhaseShipped — the sprint cut a release (Version is set).
	PhaseShipped SprintPhase = "Shipped"
	// PhaseReady — work is complete but not yet released: the verify/ship gate,
	// the binding constraint in an agentic flow (velocity is verification-bound).
	PhaseReady SprintPhase = "Ready"
	// PhaseBuilding — work is in flight (some items done or in progress).
	PhaseBuilding SprintPhase = "Building"
	// PhaseProposed — declared but not started (no items, or all pending).
	PhaseProposed SprintPhase = "Proposed"
	// PhasePaused — deliberately parked (the sprint carries "Status: Paused").
	// Unlike the other phases this one is not derived from Version or item
	// state: it is an explicit override, so it is reported regardless of how
	// far the items have progressed.
	PhasePaused SprintPhase = "Paused"
)

// SprintProgress returns how many items are Done and the total item count.
func SprintProgress(s Sprint) (done, total int) {
	total = len(s.Items)
	for _, it := range s.Items {
		if it.Status == StatusDone {
			done++
		}
	}
	return done, total
}

// PhaseOf derives a sprint's ship-pipeline phase (see SprintPhase). A sprint
// that cut a release is Shipped regardless of item state; otherwise one with no
// started items is Proposed, one with every item Done (but no version yet) is
// Ready, and anything in between is Building.
func PhaseOf(s Sprint) SprintPhase {
	// An explicit "Status: Paused" overrides the derived flow: a parked sprint
	// is Paused whatever its version or item state, so this is checked first.
	if s.Status == StatusPaused {
		return PhasePaused
	}
	if s.Version != "" {
		return PhaseShipped
	}
	if !anyStarted(s) {
		return PhaseProposed
	}
	if done, total := SprintProgress(s); done == total {
		return PhaseReady
	}
	return PhaseBuilding
}

// anyStarted reports whether any item has moved off the pending default (Done
// or In Progress) — i.e. work has actually begun.
func anyStarted(s Sprint) bool {
	for _, it := range s.Items {
		if it.Status == StatusDone || it.Status == StatusInProgress {
			return true
		}
	}
	return false
}
