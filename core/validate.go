// Copyright (C) 2026 Techdelight BV

package core

import "fmt"

// Severity ranks a validation finding.
type Severity string

const (
	// SeverityError marks a document that contradicts itself or another: the
	// convention is broken and something downstream will read it wrong.
	SeverityError Severity = "error"
	// SeverityWarning marks a document that parses but loses information —
	// a link that cannot be followed, a status a reader cannot classify.
	SeverityWarning Severity = "warning"
)

// Finding is one problem found in a project's documents.
type Finding struct {
	Severity Severity `json:"severity"`
	// Doc names the document to go and edit, which for a cross-file finding
	// is the one holding the claim rather than the one contradicting it.
	Doc     string `json:"doc"`
	Message string `json:"message"`
}

func (f Finding) String() string {
	return fmt.Sprintf("%s: %s: %s", f.Severity, f.Doc, f.Message)
}

// ValidateDocs reports what is wrong across a project's parsed documents.
//
// This is where cross-file questions are answered, deliberately kept out of
// the parsers: a parser sees one file and must stay total, so ParseSprints
// happily records a link to milestone 9 that ROADMAP.md has never heard of.
// Only something holding both documents at once can say so.
//
// Pure and zero-I/O, like the parsers it consumes: the caller reads the files.
//
// Absence is never a finding. A project with no ROADMAP.md has no milestones
// and no milestone conventions to break; checks that need milestones are
// skipped rather than failed, because the docs badge already reports which
// documents are missing and saying it twice would drown the findings that
// matter in noise about an early-life project.
//
// Findings come back in document order — milestones, then sprints — so the
// output reads like a walk through the documents rather than a ranking.
func ValidateDocs(milestones []Milestone, sprints []Sprint) []Finding {
	findings := []Finding{}

	findings = append(findings, validateMilestones(milestones)...)
	findings = append(findings, validateSprints(sprints)...)
	findings = append(findings, validateLinks(milestones, sprints)...)

	return findings
}

func validateMilestones(milestones []Milestone) []Finding {
	findings := []Finding{}
	if len(milestones) == 0 {
		return findings
	}

	seen := map[int]bool{}
	for _, m := range milestones {
		if seen[m.Number] {
			findings = append(findings, Finding{
				Severity: SeverityError,
				Doc:      "ROADMAP.md",
				Message: fmt.Sprintf("milestone %d is defined more than once; a sprint linking to it cannot say which is meant",
					m.Number),
			})
		}
		seen[m.Number] = true
	}

	// The arc nests the current sprint inside the one in-progress milestone,
	// so a document with none or several leaves the timeline with nowhere —
	// or a choice of places — to put it.
	var inProgress []int
	for _, m := range milestones {
		if m.Status == StatusInProgress {
			inProgress = append(inProgress, m.Number)
		}
	}
	switch {
	case len(inProgress) == 0:
		findings = append(findings, Finding{
			Severity: SeverityWarning,
			Doc:      "ROADMAP.md",
			Message:  "no milestone is marked (In Progress); the roadmap does not say what is being worked on now",
		})
	case len(inProgress) > 1:
		findings = append(findings, Finding{
			Severity: SeverityWarning,
			Doc:      "ROADMAP.md",
			Message: fmt.Sprintf("milestones %v are all marked (In Progress); the roadmap should name one current focus",
				inProgress),
		})
	}

	return findings
}

func validateSprints(sprints []Sprint) []Finding {
	findings := []Finding{}
	if len(sprints) == 0 {
		return findings
	}

	seen := map[int]bool{}
	var current []int
	for _, s := range sprints {
		if seen[s.Number] {
			findings = append(findings, Finding{
				Severity: SeverityError,
				Doc:      "SPRINTS.md",
				Message:  fmt.Sprintf("sprint %d is defined more than once", s.Number),
			})
		}
		seen[s.Number] = true

		if s.IsCurrent {
			current = append(current, s.Number)
		}

		// The status text is carried through verbatim, so a status outside the
		// vocabulary parses fine and then quietly fails every comparison
		// against it — "In progress" is not StatusInProgress. Nothing errors;
		// the item just stops counting as in progress wherever it is read.
		for _, item := range s.Items {
			switch item.Status {
			case StatusPending, StatusDone, StatusInProgress:
			default:
				findings = append(findings, Finding{
					Severity: SeverityWarning,
					Doc:      "SPRINTS.md",
					Message: fmt.Sprintf("sprint %d item %d has status %q, which is not one of %q, %q or empty; it will not be recognised as either",
						s.Number, item.Number, string(item.Status), string(StatusDone), string(StatusInProgress)),
				})
			}
		}
	}

	if len(current) > 1 {
		findings = append(findings, Finding{
			Severity: SeverityError,
			Doc:      "SPRINTS.md",
			Message: fmt.Sprintf("sprints %v are all under \"## Current Sprint\"; only one sprint can be current",
				current),
		})
	}

	return findings
}

// validateLinks answers the question no single-file parser can: does the
// sprint's "Milestone: N" name a milestone that exists, and is that milestone
// the one actually under way?
func validateLinks(milestones []Milestone, sprints []Sprint) []Finding {
	findings := []Finding{}

	// With no ROADMAP.md there is nothing to link against. A sprint naming a
	// milestone is then unverifiable rather than wrong.
	if len(milestones) == 0 {
		return findings
	}

	byNumber := map[int]Milestone{}
	for _, m := range milestones {
		byNumber[m.Number] = m
	}

	for _, s := range sprints {
		if s.Milestone == 0 {
			// Only the current sprint is worth flagging: history predates the
			// convention, and retro-fitting links to closed sprints is busywork.
			if s.IsCurrent {
				findings = append(findings, Finding{
					Severity: SeverityWarning,
					Doc:      "SPRINTS.md",
					Message: fmt.Sprintf("sprint %d is current but has no \"Milestone: N\" line; the roadmap arc cannot show what it is working toward",
						s.Number),
				})
			}
			continue
		}

		m, ok := byNumber[s.Milestone]
		if !ok {
			findings = append(findings, Finding{
				Severity: SeverityError,
				Doc:      "SPRINTS.md",
				Message: fmt.Sprintf("sprint %d links to milestone %d, which ROADMAP.md does not define",
					s.Number, s.Milestone),
			})
			continue
		}

		// A current sprint driving a milestone that is Done or Planned means
		// one of the two documents was not updated: work is happening against
		// a milestone the roadmap does not show as under way.
		if s.IsCurrent && m.Status != StatusInProgress {
			findings = append(findings, Finding{
				Severity: SeverityWarning,
				Doc:      "ROADMAP.md",
				Message: fmt.Sprintf("sprint %d is current and links to milestone %d, but that milestone is (%s), not (%s)",
					s.Number, m.Number, m.Status, StatusInProgress),
			})
		}
	}

	return findings
}
