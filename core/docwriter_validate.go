// Copyright (C) 2026 Techdelight BV

package core

import (
	"fmt"
	"regexp"
)

// doubledStatusRe matches a milestone TITLE that still ends in a status
// parenthetical. The parser consumes the last marker on a heading as the
// status, so a second one can only be left behind in the title — which makes
// this the signature of a heading carrying two markers.
var doubledStatusRe = regexp.MustCompile(`\((?:Done|In Progress|Paused|Planned)\)\s*$`)

// This file guards the docwriter mutations against edits that would leave the
// documents inconsistent. The writers themselves stay pure (text in, text out)
// and unconditional — a caller composing several edits may pass through an
// intermediate state that is briefly inconsistent — so the invariant checks
// live here, for the caller to run before committing a write to disk. The
// cross-file invariants reuse ValidateDocs rather than re-deriving them.

// InvariantError is returned when a mutation is refused because it would break
// a document invariant (as opposed to a plain error like "sprint not found").
// It is a distinct type so a caller — e.g. an MCP tool — can render a refusal
// differently from an operational failure.
type InvariantError struct {
	Message string
}

func (e *InvariantError) Error() string { return e.Message }

// ValidateWrite reports whether a prospective roadmap+sprints pair is
// self-consistent, returning an *InvariantError describing the first violation
// or nil when the pair is clean. A caller runs it on the *result* of a mutation
// (or of a batch of them) and only writes to disk when it returns nil.
//
// It refuses more than one In Progress milestone — the arc nests the current
// sprint inside the single milestone under way, so a second one has nowhere to
// sit (ValidateDocs reports this only as a warning; a write must not create it,
// so it is elevated to a refusal here) — and refuses any error-severity
// ValidateDocs finding, the outright contradictions: a duplicate milestone or
// sprint number, two current sprints, or a link to a milestone that does not
// exist.
func ValidateWrite(roadmap, sprints string) error {
	ms := ParseMilestones(roadmap)
	sp := ParseSprints(sprints)

	// A heading carrying two status markers parses cleanly — the parser takes the
	// last one — so nothing downstream would ever complain, and the milestone's
	// title silently accumulates markers. That is why this is checked on the way
	// in rather than reported by ValidateDocs: it is not a contradiction between
	// documents, it is corruption that reads as valid.
	for _, m := range ms {
		if doubledStatusRe.MatchString(m.Title) {
			return &InvariantError{Message: fmt.Sprintf(
				"write rejected: milestone %d's heading carries two status markers (title ends %q); a heading may carry at most one",
				m.Number, m.Title)}
		}
	}

	var inProgress []int
	for _, m := range ms {
		if m.Status == StatusInProgress {
			inProgress = append(inProgress, m.Number)
		}
	}
	if len(inProgress) > 1 {
		return &InvariantError{Message: fmt.Sprintf(
			"write rejected: milestones %v would all be In Progress; exactly one milestone may be In Progress at a time",
			inProgress)}
	}

	for _, f := range ValidateDocs(ms, sp) {
		if f.Severity == SeverityError {
			return &InvariantError{Message: "write rejected: " + f.Message}
		}
	}
	return nil
}

// FinishSprint rolls sprint number into history at version, refusing when the
// sprint still has unfinished (not-Done) items unless force is set. It wraps
// RollSprintToHistory: the guard is the only thing added. An absent sprint is
// an ordinary error, not an InvariantError.
func FinishSprint(sprints string, number int, version string, force bool) (string, error) {
	if !force {
		s := findSprint(ParseSprints(sprints), number)
		if s == nil {
			return "", fmt.Errorf("sprint %d not found in SPRINTS.md", number)
		}
		if unfinished := unfinishedItems(*s); len(unfinished) > 0 {
			return "", &InvariantError{Message: fmt.Sprintf(
				"finish rejected: sprint %d has unfinished item(s) %v; pass force to roll it to history anyway",
				number, unfinished)}
		}
	}
	return RollSprintToHistory(sprints, number, version)
}

// FinishMilestone marks milestone number Done, refusing while a current
// (non-history) sprint is still linked to it — finishing a milestone whose work
// is visibly open is the contradiction this guards. Move or roll that sprint
// first. It wraps SetMilestoneStatus; an absent milestone surfaces as that
// call's ordinary error.
func FinishMilestone(roadmap, sprints string, number int) (string, error) {
	for _, s := range ParseSprints(sprints) {
		if s.IsCurrent && s.Milestone == number {
			return "", &InvariantError{Message: fmt.Sprintf(
				"finish rejected: milestone %d still has an open current sprint (sprint %d); roll or move it before finishing the milestone",
				number, s.Number)}
		}
	}
	return SetMilestoneStatus(roadmap, number, StatusDone)
}

// findSprint returns a pointer to the sprint with the given number, or nil.
func findSprint(sprints []Sprint, number int) *Sprint {
	for i := range sprints {
		if sprints[i].Number == number {
			return &sprints[i]
		}
	}
	return nil
}

// unfinishedItems lists the numbers of a sprint's items that are not Done.
func unfinishedItems(s Sprint) []int {
	var out []int
	for _, it := range s.Items {
		if it.Status != StatusDone {
			out = append(out, it.Number)
		}
	}
	return out
}
