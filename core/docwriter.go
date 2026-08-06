// Copyright (C) 2026 Techdelight BV

package core

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// This file mutates ROADMAP.md / SPRINTS.md as *text*, by surgical edits
// anchored on the same structure the parsers recognise. It deliberately never
// parses a document into a model and re-serialises it: doing so would discard
// every hand-written line the parsers ignore — the End Goal prose, the Phasing
// diagram, a sprint's "Out of scope" note, the exact blank-line rhythm — and
// flatten the file into whatever the writer happened to emit. Instead each
// function locates the one heading or header-block line it must change and
// rewrites, inserts or deletes only those lines, leaving every other byte of
// the document identical. Every function is pure: text in, text out.
//
// The split-then-join round-trip is byte-exact by construction. strings.Split
// on "\n" and strings.Join on "\n" are inverses (a trailing newline becomes a
// trailing empty element and back), so any line a function does not touch
// survives verbatim, including the file's final-newline state.

// milestoneHeadingRewriteRe splits a milestone heading into everything up to
// and including its title (group 1) and an optional trailing status
// parenthetical that is dropped. Rewriting is then group 1 + a fresh suffix,
// which preserves the exact bytes of the prefix and title — only the status
// parenthetical is ever rewritten. The title group is lazy and the status
// group pinned to the known statuses, mirroring milestoneHeaderRe, so an
// unrecognised parenthetical in a title ("… (Phase 2)") stays part of it.
var milestoneHeadingRewriteRe = regexp.MustCompile(`^(\s*###\s+Milestone\s+(\d+):\s+.+?)(?:\s+\((?:Done|In Progress|Paused)\))?\s*$`)

// sprintHeadingRewriteRe is the sprint-heading analogue: group 1 is the prefix
// up to and including the title, and an optional trailing "(vX)" is dropped so
// a fresh version parenthetical can be appended.
var sprintHeadingRewriteRe = regexp.MustCompile(`^(\s*###\s+Sprint\s+(\d+):\s+.+?)(?:\s+\(v[^)]+\))?\s*$`)

// --- milestone operations (ROADMAP.md) ---------------------------------------

// SetMilestoneStatus rewrites milestone number's heading parenthetical. A
// StatusPlanned (or empty) status removes the parenthetical entirely; any other
// status replaces or adds it. Everything else on the heading — and everywhere
// else in the document — is left byte-identical. Errors if the milestone is
// absent.
func SetMilestoneStatus(roadmap string, number int, status Status) (string, error) {
	lines := strings.Split(roadmap, "\n")
	i := milestoneHeadingLine(lines, number)
	if i < 0 {
		return "", fmt.Errorf("milestone %d not found in ROADMAP.md", number)
	}
	sub := milestoneHeadingRewriteRe.FindStringSubmatch(lines[i])
	if sub == nil {
		return "", fmt.Errorf("milestone %d heading %q could not be parsed for rewrite", number, lines[i])
	}
	lines[i] = sub[1] + milestoneStatusSuffix(status)
	return strings.Join(lines, "\n"), nil
}

// AddMilestone appends a new milestone, numbered one past the highest existing
// milestone, as a "### Milestone <n>: <title>" heading (Planned — no
// parenthetical) followed by the description. It is inserted just before the
// "## Phasing" section when one exists, otherwise at the end of the document.
// Returns the new text and the assigned number.
func AddMilestone(roadmap, title, description string) (string, int, error) {
	lines := strings.Split(roadmap, "\n")
	number := nextMilestoneNumber(lines)

	insert := len(lines)
	for i, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "## Phasing") {
			insert = i
			break
		}
	}

	var block []string
	// When appending at the very end, ensure a blank line separates the new
	// heading from whatever text precedes it. Before "## Phasing" the existing
	// blank line above that heading already provides the separation.
	if insert == len(lines) && insert > 0 && strings.TrimSpace(lines[insert-1]) != "" {
		block = append(block, "")
	}
	block = append(block, fmt.Sprintf("### Milestone %d: %s", number, title), "")
	if description != "" {
		block = append(block, strings.Split(description, "\n")...)
		block = append(block, "")
	}

	return strings.Join(insertLines(lines, insert, block), "\n"), number, nil
}

// RemoveMilestone deletes milestone number's heading and its body — every line
// from the heading up to (not including) the next heading of any level, which
// also removes the blank line that trailed the body. Errors if absent.
func RemoveMilestone(roadmap string, number int) (string, error) {
	lines := strings.Split(roadmap, "\n")
	i := milestoneHeadingLine(lines, number)
	if i < 0 {
		return "", fmt.Errorf("milestone %d not found in ROADMAP.md", number)
	}
	end := blockEnd(lines, i)
	lines = append(lines[:i], lines[end:]...)
	return strings.Join(lines, "\n"), nil
}

// --- sprint operations (SPRINTS.md) ------------------------------------------

// SetSprintStatus sets, replaces or removes the "Status:" line in sprint
// number's header block. A StatusPending (empty) status removes the line if
// present (and is a no-op if absent); any other status replaces an existing
// line or inserts a new one after the Milestone: line (else the Goal: line,
// else the heading). Errors if the sprint is absent.
func SetSprintStatus(sprints string, number int, status Status) (string, error) {
	lines := strings.Split(sprints, "\n")
	i := sprintHeadingLine(lines, number)
	if i < 0 {
		return "", fmt.Errorf("sprint %d not found in SPRINTS.md", number)
	}
	hbEnd := sprintHeaderBlockEnd(lines, i)

	statusIdx := -1
	for j := i + 1; j < hbEnd; j++ {
		if strings.HasPrefix(strings.TrimSpace(lines[j]), "Status:") {
			statusIdx = j
			break
		}
	}

	remove := status == StatusPending
	switch {
	case statusIdx >= 0 && remove:
		lines = append(lines[:statusIdx], lines[statusIdx+1:]...)
	case statusIdx >= 0:
		lines[statusIdx] = "Status: " + string(status)
	case remove:
		// No line to remove; leave the document untouched.
	default:
		at := headerLineInsertIndex(lines, i, hbEnd, "Milestone:", "Goal:")
		lines = insertLines(lines, at, []string{"Status: " + string(status)})
	}
	return strings.Join(lines, "\n"), nil
}

// AddSprint inserts a new sprint at the top of the "## Current Sprint" section,
// numbered one past the highest existing sprint, with a "Milestone: <n>" line
// (when milestone > 0) and an item table whose rows all start Pending (empty
// status cell). Returns the new text and the assigned number. Errors if there
// is no "## Current Sprint" section.
func AddSprint(sprints, title string, milestone int, items []string) (string, int, error) {
	lines := strings.Split(sprints, "\n")
	number := nextSprintNumber(lines)

	cur := sectionLine(lines, "## Current Sprint")
	if cur < 0 {
		return "", 0, fmt.Errorf("no \"## Current Sprint\" section found in SPRINTS.md")
	}
	insert := cur + 1
	if insert < len(lines) && strings.TrimSpace(lines[insert]) == "" {
		insert++ // keep the blank line right under the section heading
	}

	block := []string{fmt.Sprintf("### Sprint %d: %s", number, title), ""}
	if milestone > 0 {
		block = append(block, fmt.Sprintf("Milestone: %d", milestone), "")
	}
	block = append(block, "| # | Item | Status |", "|---|------|--------|")
	for idx, it := range items {
		block = append(block, fmt.Sprintf("| %d | %s |  |", idx+1, it))
	}
	block = append(block, "")

	return strings.Join(insertLines(lines, insert, block), "\n"), number, nil
}

// RemoveSprint deletes sprint number's section — heading through the line
// before the next heading of any level. Errors if absent.
func RemoveSprint(sprints string, number int) (string, error) {
	lines := strings.Split(sprints, "\n")
	i := sprintHeadingLine(lines, number)
	if i < 0 {
		return "", fmt.Errorf("sprint %d not found in SPRINTS.md", number)
	}
	end := blockEnd(lines, i)
	lines = append(lines[:i], lines[end:]...)
	return strings.Join(lines, "\n"), nil
}

// MoveSprint re-points sprint number's "Milestone:" line at toMilestone,
// rewriting the line in place. When the sprint has no Milestone: line one is
// added after the Goal: line (else after the heading). Errors if the sprint is
// absent.
func MoveSprint(sprints string, number, toMilestone int) (string, error) {
	lines := strings.Split(sprints, "\n")
	i := sprintHeadingLine(lines, number)
	if i < 0 {
		return "", fmt.Errorf("sprint %d not found in SPRINTS.md", number)
	}
	hbEnd := sprintHeaderBlockEnd(lines, i)
	for j := i + 1; j < hbEnd; j++ {
		if strings.HasPrefix(strings.TrimSpace(lines[j]), "Milestone:") {
			lines[j] = fmt.Sprintf("Milestone: %d", toMilestone)
			return strings.Join(lines, "\n"), nil
		}
	}
	at := headerLineInsertIndex(lines, i, hbEnd, "Goal:")
	lines = insertLines(lines, at, []string{fmt.Sprintf("Milestone: %d", toMilestone)})
	return strings.Join(lines, "\n"), nil
}

// RollSprintToHistory moves sprint number's section out of "## Current Sprint"
// and to the top of "## Sprint History", stamping "(v<version>)" onto its
// heading (a leading "v" in version is tolerated). The current-sprint slot is
// left empty. Errors if the sprint or the "## Sprint History" section is
// absent.
func RollSprintToHistory(sprints string, number int, version string) (string, error) {
	lines := strings.Split(sprints, "\n")
	i := sprintHeadingLine(lines, number)
	if i < 0 {
		return "", fmt.Errorf("sprint %d not found in SPRINTS.md", number)
	}
	end := blockEnd(lines, i)

	// Copy the section before any splicing so the later delete cannot alias it.
	block := make([]string, end-i)
	copy(block, lines[i:end])

	sub := sprintHeadingRewriteRe.FindStringSubmatch(block[0])
	if sub == nil {
		return "", fmt.Errorf("sprint %d heading %q could not be parsed for rewrite", number, block[0])
	}
	block[0] = sub[1] + " (v" + strings.TrimPrefix(version, "v") + ")"

	lines = append(lines[:i], lines[end:]...)

	hist := sectionLine(lines, "## Sprint History")
	if hist < 0 {
		return "", fmt.Errorf("no \"## Sprint History\" section found in SPRINTS.md")
	}
	insert := hist + 1
	if insert < len(lines) && strings.TrimSpace(lines[insert]) == "" {
		insert++ // keep the blank line right under the section heading
	}
	return strings.Join(insertLines(lines, insert, block), "\n"), nil
}

// --- shared helpers ----------------------------------------------------------

// milestoneStatusSuffix renders the trailing " (Status)" for a heading.
// StatusPlanned and the empty status yield no parenthetical — a planned
// milestone is one with no marker.
func milestoneStatusSuffix(status Status) string {
	if status == StatusPlanned || status == "" {
		return ""
	}
	return " (" + string(status) + ")"
}

// milestoneHeadingLine returns the index of milestone number's "###" heading,
// or -1. It uses the same milestoneHeaderRe the parser does, so it recognises
// exactly the headings the parser would.
func milestoneHeadingLine(lines []string, number int) int {
	for i, ln := range lines {
		if m := milestoneHeaderRe.FindStringSubmatch(strings.TrimSpace(ln)); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n == number {
				return i
			}
		}
	}
	return -1
}

// sprintHeadingLine returns the index of sprint number's "###" heading, or -1.
func sprintHeadingLine(lines []string, number int) int {
	for i, ln := range lines {
		if m := sprintHeaderRe.FindStringSubmatch(strings.TrimSpace(ln)); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n == number {
				return i
			}
		}
	}
	return -1
}

// nextMilestoneNumber is one past the highest milestone number present (1 when
// there are none).
func nextMilestoneNumber(lines []string) int {
	return nextNumber(lines, milestoneHeaderRe)
}

// nextSprintNumber is one past the highest sprint number present (1 when there
// are none).
func nextSprintNumber(lines []string) int {
	return nextNumber(lines, sprintHeaderRe)
}

func nextNumber(lines []string, re *regexp.Regexp) int {
	max := 0
	for _, ln := range lines {
		if m := re.FindStringSubmatch(strings.TrimSpace(ln)); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n > max {
				max = n
			}
		}
	}
	return max + 1
}

// blockEnd returns the index of the first heading (any line whose trimmed text
// begins with "#") after start, or len(lines). This mirrors how ParseMilestones
// bounds a body — at the next heading — so a removed block covers exactly what
// the parser attributed to it.
func blockEnd(lines []string, start int) int {
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "#") {
			return i
		}
	}
	return len(lines)
}

// sprintHeaderBlockEnd returns the index of the first line after a sprint
// heading that ends its header block: a table row (starts with "|") or the next
// heading (starts with "#"). The header block is where Goal:/Milestone:/Status:
// live, so these are the only lines a sprint mutation edits or scans.
func sprintHeaderBlockEnd(lines []string, start int) int {
	for i := start + 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "|") {
			return i
		}
	}
	return len(lines)
}

// headerLineInsertIndex chooses where to insert a new header-block line. It
// returns the line just after the first of prefer (in priority order) that is
// present in the header block; failing that, just after the heading, skipping a
// single blank line so the new line does not butt against the heading.
func headerLineInsertIndex(lines []string, headingIdx, hbEnd int, prefer ...string) int {
	found := map[string]int{}
	for j := headingIdx + 1; j < hbEnd; j++ {
		t := strings.TrimSpace(lines[j])
		for _, p := range prefer {
			if _, seen := found[p]; !seen && strings.HasPrefix(t, p) {
				found[p] = j
			}
		}
	}
	for _, p := range prefer {
		if j, ok := found[p]; ok {
			return j + 1
		}
	}
	j := headingIdx + 1
	if j < len(lines) && strings.TrimSpace(lines[j]) == "" {
		j++
	}
	return j
}

// sectionLine returns the index of a "## " section heading matched exactly
// (after trimming), or -1.
func sectionLine(lines []string, heading string) int {
	for i, ln := range lines {
		if strings.TrimSpace(ln) == heading {
			return i
		}
	}
	return -1
}

// insertLines returns lines with block spliced in at index at, without aliasing
// the input's backing array.
func insertLines(lines []string, at int, block []string) []string {
	out := make([]string, 0, len(lines)+len(block))
	out = append(out, lines[:at]...)
	out = append(out, block...)
	out = append(out, lines[at:]...)
	return out
}
