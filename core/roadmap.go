// Copyright (C) 2026 Techdelight BV

package core

import (
	"regexp"
	"strconv"
	"strings"
)

// sprintHeaderRe matches "### Sprint N: Title (vX.Y.Z)" or "### Sprint N: Title".
var sprintHeaderRe = regexp.MustCompile(`^###\s+Sprint\s+(\d+):\s+(.+?)(?:\s+\(v([^)]+)\))?\s*$`)

// tableRowRe matches "| N | description | status |" where N is a number.
var tableRowRe = regexp.MustCompile(`^\|\s*(\d+)\s*\|(.+)\|([^|]*)\|\s*$`)

// ParseSprints parses a SPRINTS.md (or legacy ROADMAP.md) into a list of sprints.
// Returns sprints in the order they appear. Sprints under "## Current Sprint"
// are marked with IsCurrent=true.
func ParseSprints(markdown string) []Sprint {
	lines := strings.Split(markdown, "\n")
	var sprints []Sprint
	var current *Sprint
	inCurrent := false

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Track top-level section headers.
		if strings.HasPrefix(trimmed, "## ") && !strings.HasPrefix(trimmed, "### ") {
			if strings.HasPrefix(trimmed, "## Current Sprint") {
				inCurrent = true
			} else {
				inCurrent = false
			}
			continue
		}

		// Match sprint header.
		if m := sprintHeaderRe.FindStringSubmatch(trimmed); m != nil {
			if current != nil {
				sprints = append(sprints, *current)
			}
			num, _ := strconv.Atoi(m[1])
			current = &Sprint{
				Number:    num,
				Title:     m[2],
				Version:   m[3],
				IsCurrent: inCurrent,
			}
			continue
		}

		// Match goal line (appears after header, before table).
		if current != nil && len(current.Items) == 0 && current.Goal == "" {
			if strings.HasPrefix(trimmed, "Goal:") {
				current.Goal = strings.TrimSpace(strings.TrimPrefix(trimmed, "Goal:"))
				continue
			}
		}

		// Match the milestone link line (also between header and table).
		//
		// This needs its own guard block. Folding it into the Goal block above
		// would gate it on `current.Goal == ""`, so a sprint whose Goal: line
		// comes first — the usual order — would never reach it and would drop
		// its milestone silently, the parse depending on which line the author
		// happened to write first.
		if current != nil && len(current.Items) == 0 && current.Milestone == 0 {
			if strings.HasPrefix(trimmed, "Milestone:") {
				// Tolerant by design: a value that is not a number leaves
				// Milestone at 0 (unlinked) rather than failing the parse. A
				// half-written document is a normal state, and whether the
				// number names a real milestone is a cross-file question for
				// a validator, not for this parser.
				num, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(trimmed, "Milestone:")))
				if err == nil && num > 0 {
					current.Milestone = num
				}
				continue
			}
		}

		// Match the sprint's own status line (a header-block line, like Goal:
		// and Milestone:). "Status: Paused" parks the sprint out of the derived
		// ship-pipeline flow; see Sprint.Status and PhaseOf.
		//
		// Its own guard block for the same reason Milestone: has one — folding
		// it into another block would gate it on a sibling field being empty and
		// drop it depending on which header line the author wrote first. Gated
		// on len(Items) == 0 so a "| ... | Status |" table header (which is a
		// table row, not a header-block line, and never starts with "Status:")
		// and any later prose cannot be mistaken for the marker.
		if current != nil && len(current.Items) == 0 && current.Status == "" {
			if strings.HasPrefix(trimmed, "Status:") {
				current.Status = Status(strings.TrimSpace(strings.TrimPrefix(trimmed, "Status:")))
				continue
			}
		}

		// Match table rows.
		if current != nil {
			if m := tableRowRe.FindStringSubmatch(trimmed); m != nil {
				num, _ := strconv.Atoi(strings.TrimSpace(m[1]))
				desc := strings.TrimSpace(m[2])
				status := strings.TrimSpace(m[3])

				current.Items = append(current.Items, SprintItem{
					Number:      num,
					Description: desc,
					Status:      SprintStatus(status),
				})
			}
		}
	}

	// Append the last sprint being parsed.
	if current != nil {
		sprints = append(sprints, *current)
	}

	return sprints
}

// ParseRoadmap is a backward-compatible alias for ParseSprints.
// Deprecated: use ParseSprints for new code.
func ParseRoadmap(markdown string) []Sprint {
	return ParseSprints(markdown)
}
