// Copyright (C) 2026 Techdelight BV

package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSprints_MilestoneLink(t *testing.T) {
	md := `## Current Sprint

### Sprint 41: Trust-Prompt (v0.40.0)

Milestone: 4

Goal: close the trust-prompt gap.
`
	got := ParseSprints(md)
	if len(got) != 1 {
		t.Fatalf("parsed %d sprints, want 1", len(got))
	}
	if got[0].Milestone != 4 {
		t.Errorf("Milestone = %d, want 4", got[0].Milestone)
	}
	if got[0].Goal != "close the trust-prompt gap." {
		t.Errorf("Goal = %q", got[0].Goal)
	}
}

// The case the adversarial review caught, and the reason Milestone: has its
// own guard block rather than living inside the Goal block: the Goal block is
// gated on `current.Goal == ""`, so once Goal is set it stops running. A
// Milestone: line folded in there would be unreachable for every sprint that
// writes Goal: first — which is the natural order, and the order the real
// SPRINTS.md uses. The link would vanish depending on line order alone.
func TestParseSprints_MilestoneAfterGoalIsNotDropped(t *testing.T) {
	md := `### Sprint 41: Trust-Prompt (v0.40.0)

Goal: close the trust-prompt gap.

Milestone: 4
`
	got := ParseSprints(md)
	if len(got) != 1 {
		t.Fatalf("parsed %d sprints, want 1", len(got))
	}
	if got[0].Milestone != 4 {
		t.Errorf("Milestone = %d, want 4 — a Goal: line before Milestone: must not drop the link", got[0].Milestone)
	}
	if got[0].Goal != "close the trust-prompt gap." {
		t.Errorf("Goal = %q, want it parsed too", got[0].Goal)
	}
}

// The mirror of the above: Milestone: first must not eat the Goal.
func TestParseSprints_GoalAfterMilestoneIsNotDropped(t *testing.T) {
	md := `### Sprint 41: Trust-Prompt (v0.40.0)

Milestone: 4

Goal: close the trust-prompt gap.
`
	got := ParseSprints(md)
	if len(got) != 1 {
		t.Fatalf("parsed %d sprints, want 1", len(got))
	}
	if got[0].Milestone != 4 {
		t.Errorf("Milestone = %d, want 4", got[0].Milestone)
	}
	if got[0].Goal != "close the trust-prompt gap." {
		t.Errorf("Goal = %q, want it parsed too", got[0].Goal)
	}
}

// Zero is the unlinked sentinel: milestones are numbered from 1.
func TestParseSprints_NoMilestoneLineIsZero(t *testing.T) {
	md := `### Sprint 40: Coordinator (v0.39.0)

Goal: promote the coordinator.
`
	got := ParseSprints(md)
	if len(got) != 1 {
		t.Fatalf("parsed %d sprints, want 1", len(got))
	}
	if got[0].Milestone != 0 {
		t.Errorf("Milestone = %d, want 0 for a sprint with no link", got[0].Milestone)
	}
}

// A non-numeric or nonsensical value leaves the sprint unlinked rather than
// failing the parse — a half-written document must still render.
func TestParseSprints_TolerantMilestoneValue(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string
	}{
		{"non-numeric", "Milestone: four"},
		{"empty", "Milestone:"},
		{"zero", "Milestone: 0"},
		{"negative", "Milestone: -2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			md := "### Sprint 41: Trust-Prompt\n\n" + tc.line + "\n\nGoal: something.\n"
			got := ParseSprints(md)
			if len(got) != 1 {
				t.Fatalf("parsed %d sprints, want 1", len(got))
			}
			if got[0].Milestone != 0 {
				t.Errorf("Milestone = %d, want 0 (unlinked)", got[0].Milestone)
			}
			if got[0].Goal != "something." {
				t.Errorf("Goal = %q, want the parse to carry on past a bad milestone", got[0].Goal)
			}
		})
	}
}

// Each sprint carries its own link; the value must not leak into the next.
func TestParseSprints_MilestonePerSprint(t *testing.T) {
	md := `## Current Sprint

### Sprint 41: Trust-Prompt (v0.40.0)

Goal: current work.
Milestone: 4

## Sprint History

### Sprint 40: Coordinator (v0.39.0)

Goal: older work.
Milestone: 3

### Sprint 39: Runner (v0.38.0)

Goal: oldest work.
`
	got := ParseSprints(md)
	if len(got) != 3 {
		t.Fatalf("parsed %d sprints, want 3", len(got))
	}
	want := []int{4, 3, 0}
	for i, w := range want {
		if got[i].Milestone != w {
			t.Errorf("sprint %d (Sprint %d): Milestone = %d, want %d", i, got[i].Number, got[i].Milestone, w)
		}
	}
}

// The link is a header-block line, like Goal:. A "Milestone:" appearing after
// the table has begun is prose, not a link.
func TestParseSprints_MilestoneAfterTableIgnored(t *testing.T) {
	md := `### Sprint 41: Trust-Prompt

Goal: something.

| # | Item | Status |
|---|------|--------|
| 1 | Do it | Done |

Milestone: 4
`
	got := ParseSprints(md)
	if len(got) != 1 {
		t.Fatalf("parsed %d sprints, want 1", len(got))
	}
	if got[0].Milestone != 0 {
		t.Errorf("Milestone = %d, want 0 — a line after the table is prose", got[0].Milestone)
	}
}

// A Proposed sprint is declared with just a header and a Milestone: link and
// no item table — the "## Planned Sprints" authoring convention (Milestone 6).
// ParseSprints must keep such a sprint (rather than drop the itemless one),
// carry its milestone link, and PhaseOf must report it as Proposed so the
// endpoint's Proposed bucket is real.
func TestParseSprints_PlannedSprintIsProposed(t *testing.T) {
	md := `## Planned Sprints

### Sprint 46: Mobile Sprint Overlay

Milestone: 6
`
	got := ParseSprints(md)
	if len(got) != 1 {
		t.Fatalf("parsed %d sprints, want 1 — an itemless sprint must not be dropped", len(got))
	}
	if got[0].Number != 46 {
		t.Errorf("Number = %d, want 46", got[0].Number)
	}
	if got[0].Milestone != 6 {
		t.Errorf("Milestone = %d, want 6 — the link must survive with no item table", got[0].Milestone)
	}
	if len(got[0].Items) != 0 {
		t.Errorf("Items = %d, want 0 for a planned sprint", len(got[0].Items))
	}
	if p := PhaseOf(got[0]); p != PhaseProposed {
		t.Errorf("PhaseOf = %q, want %q for a no-item sprint", p, PhaseProposed)
	}
}

// The repo's own SPRINTS.md must keep parsing, and its current sprint must
// link to the milestone ROADMAP.md marks in progress. If this fails, the two
// documents have drifted apart.
func TestParseSprints_RealSprintsLinksToInProgressMilestone(t *testing.T) {
	sprintData, err := os.ReadFile(filepath.Join("..", "SPRINTS.md"))
	if err != nil {
		t.Fatalf("reading repo SPRINTS.md: %v", err)
	}
	roadmapData, err := os.ReadFile(filepath.Join("..", "ROADMAP.md"))
	if err != nil {
		t.Fatalf("reading repo ROADMAP.md: %v", err)
	}

	var current *Sprint
	for _, s := range ParseSprints(string(sprintData)) {
		if s.IsCurrent {
			current = &s
			break
		}
	}
	if current == nil {
		// A milestone can be In Progress with no sprint started yet — "active
		// milestone, no sprints" is a valid, first-class state (Milestone 6/7),
		// so there is nothing to cross-check here. The exactly-one-in-progress
		// invariant is enforced by TestParseMilestonesAgainstRealRoadmap.
		return
	}
	if current.Milestone == 0 {
		t.Fatalf("current sprint %d is not linked to a milestone", current.Number)
	}

	for _, m := range ParseMilestones(string(roadmapData)) {
		if m.Number != current.Milestone {
			continue
		}
		if m.Status != StatusInProgress {
			t.Errorf("current sprint %d links to milestone %d, whose status is %q, want %q",
				current.Number, m.Number, m.Status, StatusInProgress)
		}
		return
	}
	t.Errorf("current sprint %d links to milestone %d, which does not exist in ROADMAP.md",
		current.Number, current.Milestone)
}
