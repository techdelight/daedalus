// Copyright (C) 2026 Techdelight BV

package core

import (
	"regexp"
	"strconv"
	"strings"
)

// StatusPlanned is a milestone that has not been started: a heading with no
// status parenthetical. It is the milestone-side default, distinct from the
// sprint-side StatusPending — a planned milestone is a deliberate future
// commitment, whereas a pending sprint item is simply an unticked cell.
//
// It is never written in the document. ROADMAP.md marks only the milestones
// that are Done or In Progress, so "planned" is what the absence of a marker
// means; naming it keeps callers from testing for an empty string.
const StatusPlanned Status = "Planned"

// Milestone represents one strategic milestone parsed from a ROADMAP.md
// heading.
//
// Status is deliberately tri-state (Done / In Progress / Planned) with no
// per-milestone percentage: progress lives at the sprint level, where the
// work is actually itemised, and a milestone-level number could only ever be
// invented. Which milestone a sprint belongs to is a separate link (a
// "Milestone: N" line on the sprint) and not yet parsed.
type Milestone struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Status Status `json:"status"`
	// Description is the prose under the heading, verbatim and newline-joined
	// (so a bullet list survives as a bullet list). Empty when the heading
	// carries no body.
	Description string `json:"description,omitempty"`
}

// milestoneHeaderRe matches "### Milestone N: Title (Done)",
// "### Milestone N: Title (In Progress)", "### Milestone N: Title (Paused)",
// or "### Milestone N: Title".
//
// The status group is pinned to the literal statuses rather than a permissive
// ([^)]+). The title group is lazy, so a permissive group would swallow any
// trailing parenthetical: "Milestone 2: Rework (Phase 2)" would parse as title
// "Rework" with status "Phase 2". Pinning makes an unrecognised parenthetical
// stay part of the title, which is what it is.
var milestoneHeaderRe = regexp.MustCompile(`^###\s+Milestone\s+(\d+):\s+(.+?)(?:\s+\((Done|In Progress|Paused|Planned)\))?\s*$`)

// ParseMilestones parses a ROADMAP.md into its milestones, in document order.
//
// Every "### Milestone N:" heading is taken, wherever it sits, mirroring how
// ParseSprints takes every sprint heading regardless of section. A heading
// with no status parenthetical is StatusPlanned, and an explicit "(Planned)" is
// recognised and stripped rather than left in the title — the tooling writes it,
// so a parser that did not know it would render "Homebrew Distribution (Planned)"
// as the milestone's name everywhere the title is shown.
//
// Pure and total: unparseable input yields no milestones rather than an error,
// because a half-written ROADMAP.md is a normal state for a project and must
// not fail the view that renders it.
func ParseMilestones(markdown string) []Milestone {
	lines := strings.Split(markdown, "\n")
	var milestones []Milestone
	var current *Milestone
	var body []string

	// flush attaches the accumulated prose to the milestone it followed.
	flush := func() {
		if current == nil {
			return
		}
		current.Description = strings.TrimSpace(strings.Join(body, "\n"))
		milestones = append(milestones, *current)
		current = nil
		body = nil
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if m := milestoneHeaderRe.FindStringSubmatch(trimmed); m != nil {
			flush()
			num, err := strconv.Atoi(m[1])
			if err != nil {
				// Unreachable while the pattern requires \d+, but a number too
				// large for an int would land here rather than silently as 0.
				continue
			}
			status := Status(m[3])
			if status == "" {
				status = StatusPlanned
			}
			current = &Milestone{Number: num, Title: m[2], Status: status}
			continue
		}

		// Any other heading ends the current milestone's prose. Without this
		// the trailing milestone would absorb every following section of the
		// document ("## Phasing" and beyond) into its description.
		if strings.HasPrefix(trimmed, "#") {
			flush()
			continue
		}

		if current != nil {
			body = append(body, trimmed)
		}
	}

	flush()
	return milestones
}
