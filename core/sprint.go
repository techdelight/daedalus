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
	Number    int          `json:"number"`
	Title     string       `json:"title"`
	Version   string       `json:"version,omitempty"`
	Goal      string       `json:"goal,omitempty"`
	Items     []SprintItem `json:"items"`
	IsCurrent bool         `json:"isCurrent,omitempty"`
}
