// Copyright (C) 2026 Techdelight BV

package core

// Status is a lifecycle state shared by the things a project's documents
// track: sprint items (SPRINTS.md tables) and milestones (ROADMAP.md
// headings).
//
// It exists so milestones do not have to borrow SprintStatus. The two share
// the "Done" / "In Progress" vocabulary but not their lifecycles: a sprint
// item that is neither is StatusPending (an untouched table cell), while a
// milestone that is neither is StatusPlanned (a heading with no status
// parenthetical). Those defaults are declared with the type that owns them —
// see sprint.go and milestone.go — and only the shared values live here.
//
// The string values are the literal document text, so a parsed status
// round-trips back to the markdown it came from.
type Status string

const (
	// StatusDone marks finished work.
	StatusDone Status = "Done"
	// StatusInProgress marks work under way.
	StatusInProgress Status = "In Progress"
	// StatusPaused marks work put on hold: a milestone or sprint that was
	// started (or planned) and then deliberately parked, distinct from Done
	// (finished), In Progress (under way) and the not-started defaults
	// (StatusPlanned / StatusPending). Like the others its string value is the
	// literal document text — "(Paused)" on a milestone heading, "Status:
	// Paused" in a sprint's header block — so it round-trips.
	StatusPaused Status = "Paused"
)
