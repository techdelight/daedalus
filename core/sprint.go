// Copyright (C) 2026 Techdelight BV

package core

// SprintStatus represents the status of a sprint item.
//
// An alias, not a defined type: sprint items and milestones share one status
// vocabulary (see Status in status.go), and an alias keeps every existing
// SprintStatus call site — and the JSON it produces — untouched while letting
// the two be compared and assigned freely.
type SprintStatus = Status

// StatusPending is a sprint item that is neither done nor in progress: an
// empty status cell in the table. It is the sprint-side default only —
// milestones default to StatusPlanned instead. StatusDone and StatusInProgress
// are shared; they live in status.go.
const StatusPending SprintStatus = ""

// SprintItem represents a single item in a sprint.
type SprintItem struct {
	Number      int          `json:"number"`
	Description string       `json:"description"`
	Status      SprintStatus `json:"status"`
}

// Sprint represents a parsed sprint from a SPRINTS.md (or legacy ROADMAP.md) file.
type Sprint struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Version string `json:"version,omitempty"`
	Goal    string `json:"goal,omitempty"`
	// Milestone is the number of the ROADMAP.md milestone this sprint serves,
	// from a "Milestone: N" line under the sprint header. Zero means unlinked
	// — milestones are numbered from 1 — which is the honest reading of a
	// sprint that names no milestone and of one that names an unparseable
	// value. Whether the number matches a milestone that exists is a
	// cross-file question, and deliberately not this parser's to answer.
	Milestone int          `json:"milestone,omitempty"`
	Items     []SprintItem `json:"items"`
	IsCurrent bool         `json:"isCurrent,omitempty"`
	// Status is an optional lifecycle marker on the sprint itself, from a
	// "Status: <status>" line in the sprint's header block (parsed like the
	// "Milestone: N" line). It is empty for the common case — a sprint's state
	// is normally derived by PhaseOf from its Version and item statuses — and
	// is set only to park a sprint out of that flow: "Status: Paused" makes
	// PhaseOf report PhasePaused regardless of item state. Empty means "no
	// explicit marker; derive as usual".
	Status Status `json:"status,omitempty"`
}
