// Copyright (C) 2026 Techdelight BV

package core

import (
	"fmt"
	"regexp"
	"strings"
)

// looseMilestoneRe and looseSprintRe detect a line that is *trying* to be a
// milestone or sprint heading: any hash-prefixed heading that names Milestone
// or Sprint and a number. They are deliberately looser than the strict parser
// regexes (milestoneHeaderRe, sprintHeaderRe) — the whole point is to find the
// lines the strict parser rejects.
//
// The number is required so the section headings "## Milestones" and
// "## Sprint History" — which name the concept without an instance — are not
// mistaken for malformed entries.
var (
	looseMilestoneRe = regexp.MustCompile(`^#{1,6}\s+Milestone\s+\d+`)
	looseSprintRe    = regexp.MustCompile(`^#{1,6}\s+Sprint\s+\d+`)
)

// fenceRe matches the start or end of a fenced code block (``` or ~~~).
var fenceRe = regexp.MustCompile("^(```|~~~)")

// LintHeadings finds headings that look like a milestone or sprint entry but
// do not match the strict format the parsers require, and so are dropped from
// the document without a word.
//
// This is the one arc-corruption case ValidateDocs cannot see. ValidateDocs
// reasons over the *parsed* structs; a heading the parser rejected never
// becomes a struct, so it is invisible there. It has to be caught in the raw
// text, before parsing throws it away — a mistyped "## Milestone 4:" (wrong
// level) or "### Milestone 4 Foo" (no colon) silently vanishes from the arc,
// which is exactly the failure a format gate exists to stop.
//
// doc names the file, for the Finding. Lines inside fenced code blocks are
// skipped: a ROADMAP.md phasing diagram legitimately draws "M4 (In Progress)"
// and the like inside a fence, and that is prose about the arc, not an entry
// in it.
//
// Pure and zero-I/O, like ParseMilestones and ValidateDocs: the caller reads
// the file and hands over its contents.
func LintHeadings(doc, content string) []Finding {
	findings := []Finding{}
	inFence := false

	for i, line := range strings.Split(content, "\n") {
		if fenceRe.MatchString(strings.TrimSpace(line)) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}

		switch {
		case looseMilestoneRe.MatchString(line) && milestoneHeaderRe.FindStringSubmatch(strings.TrimSpace(line)) == nil:
			findings = append(findings, droppedHeading(doc, i+1, line,
				"milestone", "### Milestone N: Title (Done|In Progress)"))
		case looseSprintRe.MatchString(line) && sprintHeaderRe.FindStringSubmatch(strings.TrimSpace(line)) == nil:
			findings = append(findings, droppedHeading(doc, i+1, line,
				"sprint", "### Sprint N: Title (vX.Y.Z)"))
		}
	}

	return findings
}

// droppedHeading builds the finding for a heading that will not parse. It is
// an error, not a warning: the entry does not lose information, it disappears
// entirely — the reader downstream sees a document with a milestone or sprint
// simply missing.
func droppedHeading(doc string, line int, raw, kind, form string) Finding {
	return Finding{
		Severity: SeverityError,
		Doc:      doc,
		Message: fmt.Sprintf("line %d: %q looks like a %s heading but does not match %q; it will be dropped from the arc",
			line, strings.TrimSpace(raw), kind, form),
	}
}
