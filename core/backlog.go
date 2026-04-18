// Copyright (C) 2026 Techdelight BV

package core

import (
	"regexp"
	"strconv"
	"strings"
)

// BacklogItem represents a single item in a project backlog.
type BacklogItem struct {
	Number      int    `json:"number"`
	Description string `json:"description"`
}

// backlogRowRe matches "| N | description |" where N is a number.
var backlogRowRe = regexp.MustCompile(`^\|\s*(\d+)\s*\|(.+)\|\s*$`)

// ParseBacklog parses a BACKLOG.md file into a list of backlog items.
// It expects a two-column markdown table with | # | Item | rows.
func ParseBacklog(markdown string) []BacklogItem {
	lines := strings.Split(markdown, "\n")
	var items []BacklogItem

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		m := backlogRowRe.FindStringSubmatch(trimmed)
		if m == nil {
			continue
		}
		num, err := strconv.Atoi(strings.TrimSpace(m[1]))
		if err != nil {
			continue
		}
		desc := strings.TrimSpace(m[2])
		// Skip header/separator rows
		if desc == "" || desc == "Item" || strings.HasPrefix(desc, "---") {
			continue
		}
		items = append(items, BacklogItem{
			Number:      num,
			Description: desc,
		})
	}

	return items
}
