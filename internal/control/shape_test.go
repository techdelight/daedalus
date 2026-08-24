// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// A task shaped like a milestone gets said so, and nothing else happens.
//
// The threshold is a hint, not a rule (shape.go says why at length): every
// assertion here is about what the operator is TOLD, and none is about anything
// being refused, because nothing is.
func TestObjectiveAdvice(t *testing.T) {
	long := strings.Repeat("do a thing, and then another thing, and after that a third. ", 8)
	cases := []struct {
		name         string
		objective    string
		deliverables []string
		wantEmpty    bool
		wantSubstr   string
	}{
		{
			name:         "a task that looks like a task says nothing",
			objective:    "Add a --since flag to `daedalus task list`",
			deliverables: []string{"--since filters by age", "it appears in the man page"},
			wantEmpty:    true,
		},
		{
			name:         "a milestone-length objective is called one",
			objective:    long,
			deliverables: []string{"something exists"},
			wantSubstr:   "milestone",
		},
		{
			name:       "no deliverables is its own observation",
			objective:  "Add a --since flag",
			wantSubstr: "no deliverables",
		},
		{
			name:         "blank lines are not deliverables",
			objective:    "Add a --since flag",
			deliverables: []string{"", "   "},
			wantSubstr:   "no deliverables",
		},
		{
			name:       "both at once, in one line",
			objective:  long,
			wantSubstr: "; ",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ObjectiveAdvice(tc.objective, tc.deliverables)
			if tc.wantEmpty {
				if got != "" {
					t.Errorf("a well-shaped task was given advice it does not need: %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.wantSubstr) {
				t.Errorf("advice = %q, want it to mention %q", got, tc.wantSubstr)
			}
		})
	}
}

// DELIVERABLES SURVIVE THE ROUND TRIP, and reach the agent that has to produce
// them (#95).
//
// The column is additive: a control.db written before this existed opens, and
// its Tasks read back naming none — which is what those Tasks did.
func TestCreateTask_DeliverablesAreStoredAndReachTheJob(t *testing.T) {
	repo := gitRepo(t)
	captured := &capturingRunner{}
	svc, _, store := newService(t, mapResolver{"app": repo}, captured, nil)

	task, err := svc.CreateTask(CreateTaskRequest{
		Project: "app", Objective: "Add a --since flag to task list",
		Deliverables: []string{
			"`daedalus task list --since 7d` filters by age",
			"   ", // whitespace is not a deliverable
			"--since appears in the man page",
		},
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if len(task.Deliverables) != 2 {
		t.Fatalf("deliverables = %v, want the two real ones and not the blank", task.Deliverables)
	}

	// Durable, not just returned: the Ledger and the reviewer both read this back
	// from the store long after the create.
	reread, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if len(reread.Deliverables) != 2 || reread.Deliverables[1] != "--since appears in the man page" {
		t.Errorf("deliverables did not survive the store: %v", reread.Deliverables)
	}

	// And the agent is actually told. A list nobody hands to the worker is a
	// list that describes work no one was asked to do.
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !slices.Equal(captured.spec.Deliverables, reread.Deliverables) {
		t.Errorf("the Job was launched with %v, want the Task's own %v",
			captured.spec.Deliverables, reread.Deliverables)
	}
}

// capturingRunner records the JobSpec it was handed and does nothing else.
type capturingRunner struct{ spec JobSpec }

func (r *capturingRunner) Run(_ context.Context, spec JobSpec) RunOutcome {
	r.spec = spec
	return RunOutcome{Result: ExecSuccess}
}
