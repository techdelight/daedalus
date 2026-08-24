// Copyright (C) 2026 Techdelight BV

package main

import (
	"testing"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/control"
)

// The first tests this command has ever had, and they cover the one function in
// it that can destroy something.
//
// An amendment from the Guild Master is a PROPOSAL, so a human sees the result
// before it takes effect — but they see the finished programme, not a patch, and
// a field the merge dropped is a field that reads as deliberately emptied. The
// rule is that an omitted field is kept.

func programme() control.Programme {
	return control.Programme{
		ID: "PR-1", Name: "fluency", Description: "one way to theme, everywhere",
		Projects: []string{"app", "other"},
		Deps:     []core.DependencyEdge{{Upstream: "other", Downstream: "app"}},
	}
}

func TestMergeProgramme_AnOmittedFieldIsKept(t *testing.T) {
	cur := programme()
	got := mergeProgramme(cur, AmendProgrammeInput{
		Programme:   "fluency",
		Description: "one way to theme, everywhere — and one place to change it",
	})

	if got.Description == cur.Description {
		t.Error("the field that WAS supplied should have changed")
	}
	// The three that were not supplied. This is the data-loss case: an agent
	// fixing a sentence must not empty the programme.
	if got.Name != "fluency" {
		t.Errorf("name = %q, want it kept", got.Name)
	}
	if len(got.Projects) != 2 {
		t.Errorf("projects = %v, want both kept", got.Projects)
	}
	if len(got.Deps) != 1 {
		t.Errorf("deps = %v, want the declared order kept", got.Deps)
	}
}

// A name that is only whitespace is not a name. Treating it as one would rename
// a programme to nothing on a caller's stray space, and the name is what a
// person types at the CLI.
func TestMergeProgramme_BlankStringsAreNotAmendments(t *testing.T) {
	cur := programme()
	got := mergeProgramme(cur, AmendProgrammeInput{Programme: "fluency", Name: "   "})
	if got.Name != "fluency" {
		t.Errorf("name = %q, want the whitespace ignored", got.Name)
	}
}

// The Guild Master can now propose the ORDER as well as the membership. It was
// missing from the first version of the tool, which left the agent able to say
// which projects a programme draws on and not which of them goes first —
// noticing exactly that is what cross-project sight is for.
func TestMergeProgramme_CanAmendTheDeclaredOrder(t *testing.T) {
	cur := programme()
	got := mergeProgramme(cur, AmendProgrammeInput{
		Programme: "fluency",
		Deps: []ProgrammeEdgeInput{
			{Upstream: "app", Downstream: "other"},
			{Upstream: "other", Downstream: "third"},
		},
	})
	if len(got.Deps) != 2 {
		t.Fatalf("deps = %v, want both edges", got.Deps)
	}
	if got.Deps[0].Upstream != "app" || got.Deps[0].Downstream != "other" {
		t.Errorf("first edge = %+v, want app → other", got.Deps[0])
	}
	// Supplying deps must not disturb the membership.
	if len(got.Projects) != 2 {
		t.Errorf("projects = %v, want them untouched by a deps amendment", got.Projects)
	}
}

// An EMPTY list is an amendment and an ABSENT one is not. Collapsing the two
// would make "remove every declared edge" unexpressible, and the difference is
// the reason this takes a parsed struct rather than a map of raw JSON.
func TestMergeProgramme_AnEmptyListClearsAndNilKeeps(t *testing.T) {
	cur := programme()

	cleared := mergeProgramme(cur, AmendProgrammeInput{
		Programme: "fluency", Deps: []ProgrammeEdgeInput{}, Projects: []string{},
	})
	if len(cleared.Deps) != 0 || len(cleared.Projects) != 0 {
		t.Errorf("an explicit empty list should clear: deps=%v projects=%v",
			cleared.Deps, cleared.Projects)
	}

	kept := mergeProgramme(cur, AmendProgrammeInput{Programme: "fluency"})
	if len(kept.Deps) != 1 || len(kept.Projects) != 2 {
		t.Errorf("an absent list should keep: deps=%v projects=%v", kept.Deps, kept.Projects)
	}
}

// The Guild Master can finally say what its work is FOR (#88).
//
// Every task it filed was an orphan until this: the plane has carried a
// programme and a rationale on CreateTaskRequest since Sprint 66, a human's CLI
// has passed them since, and the agent's own tool dropped both on the floor.
// Same shape as #82 and #85 — the plane supports it and the agent cannot reach
// it — so the pass-through is tested rather than assumed.
func TestCreateTaskRequest_CarriesTheProgrammeAndTheReason(t *testing.T) {
	got := createTaskRequest(CreateTaskInput{
		Project: "app", Objective: "unify the theming",
		Programme: "fluency", Rationale: "three projects grew their own",
	})
	if got.Programme != "fluency" {
		t.Errorf("programme = %q, want it passed through UNRESOLVED for the plane to match", got.Programme)
	}
	if got.Rationale != "three projects grew their own" {
		t.Errorf("rationale = %q, want it carried", got.Rationale)
	}
	if got.Project != "app" || got.Objective != "unify the theming" {
		t.Errorf("the original fields must survive: %+v", got)
	}
}

// Both fields are optional. A task with no stated reason should be visibly
// unattributed rather than impossible to file — requiring them would only make
// an agent invent a programme to satisfy a field.
func TestCreateTaskRequest_ProgrammeAndReasonAreOptional(t *testing.T) {
	got := createTaskRequest(CreateTaskInput{Project: "app", Objective: "x"})
	if got.Programme != "" || got.Rationale != "" {
		t.Errorf("nothing should be invented: %+v", got)
	}
}

// An all-zero Budget is not "no budget" to the plane — it reads as a request for
// zero attempts. So one is attached only when the agent actually narrowed
// something; otherwise a task is filed that can never run.
func TestCreateTaskRequest_BudgetOnlyWhenNarrowed(t *testing.T) {
	if got := createTaskRequest(CreateTaskInput{Project: "app", Objective: "x"}); got.Budget != nil {
		t.Errorf("no narrowing should mean no budget, got %+v", got.Budget)
	}
	got := createTaskRequest(CreateTaskInput{Project: "app", Objective: "x", MaxAttempts: 2})
	if got.Budget == nil || got.Budget.MaxAttempts != 2 {
		t.Errorf("a narrowed budget must reach the plane: %+v", got.Budget)
	}
}

// THE GUILD MASTER CAN SAY WHAT A TASK WILL PRODUCE (#95).
//
// The tasks it filed were milestone-sized paragraphs in `objective` with nothing
// on them anybody could check off — reported by the operator reading them as "a
// big blob of text about what to do for a milestone with no clear deliverables".
// The plane carries the list; this is the pass-through that lets the agent fill
// it, tested rather than assumed for the reason #88's was.
func TestCreateTaskRequest_CarriesTheDeliverables(t *testing.T) {
	got := createTaskRequest(CreateTaskInput{
		Project: "app", Objective: "add a --since flag to task list",
		Deliverables: []string{
			"`daedalus task list --since 7d` runs and filters by age",
			"--since appears in the man page",
		},
	})
	if len(got.Deliverables) != 2 {
		t.Fatalf("deliverables = %v, want both carried to the plane", got.Deliverables)
	}
	if got.Deliverables[1] != "--since appears in the man page" {
		t.Errorf("order and content must survive: %v", got.Deliverables)
	}
}

// Optional, like the programme and the reason. A thin task should be visibly
// thin rather than impossible to file — and the create is answered with advice
// saying so, which is the part that actually teaches.
func TestCreateTaskRequest_DeliverablesAreOptional(t *testing.T) {
	if got := createTaskRequest(CreateTaskInput{Project: "app", Objective: "x"}); got.Deliverables != nil {
		t.Errorf("nothing should be invented: %+v", got.Deliverables)
	}
}
