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
