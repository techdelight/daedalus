// Copyright (C) 2026 Techdelight BV

package control

import (
	"strings"
	"testing"
)

// The programme board (Sprint 63, item 3).

// TestBoard_EveryStateHasAColumn is the structural test.
//
// A board is a projection, and the failure mode of a projection is that something
// falls out of it — work appears to have vanished rather than to have gone
// somewhere the reader did not expect. So the mapping is asserted to be TOTAL over
// the state machine: add a state to model.go without deciding where it belongs and
// this fails, which is the moment to decide.
func TestBoard_EveryStateHasAColumn(t *testing.T) {
	known := map[string]bool{}
	for _, col := range boardColumns {
		if known[col.Key] {
			t.Fatalf("duplicate board column %q", col.Key)
		}
		known[col.Key] = true
	}
	for _, state := range AllStates() {
		column, ok := columnForState[state]
		if !ok {
			t.Errorf("state %q has no board column — it would silently vanish from the board", state)
			continue
		}
		if !known[column] {
			t.Errorf("state %q maps to column %q, which is not displayed", state, column)
		}
	}
	if len(columnForState) != len(AllStates()) {
		t.Errorf("columnForState has %d entries for %d states — one of them names a state that no longer exists",
			len(columnForState), len(AllStates()))
	}
}

// TestBoard_PlacesWorkInTheRightColumns walks a Task through the lifecycle and
// checks where the board says it is.
func TestBoard_PlacesWorkInTheRightColumns(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "build it"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	steps := []struct {
		to     State
		column string
	}{
		{StatePlanned, BoardQueued},
		{StateQueued, BoardQueued},
		{StateWorking, BoardRunning},
		{StateCandidate, BoardInReview},
		{StateVerifying, BoardInReview},
		{StateVerified, BoardInReview},
		{StateApprovalRequired, BoardApproval},
		{StateApproved, BoardApproval},
		{StateIntegrated, BoardLanded},
	}
	for _, step := range steps {
		if step.to != StatePlanned {
			if _, err := store.TransitionTask(task.ID, step.to, false, ""); err != nil {
				t.Fatalf("→%s: %v", step.to, err)
			}
		}
		view, err := svc.ProgrammeBoard()
		if err != nil {
			t.Fatalf("ProgrammeBoard: %v", err)
		}
		if got := columnOf(view, task.ID); got != step.column {
			t.Errorf("state %s is in column %q, want %q", step.to, got, step.column)
		}
	}
}

// TestBoard_ShowsWhatBlockedWorkIsWaitingOn: "blocked" without "on what" sends the
// reader somewhere else, which is what the board exists to avoid.
func TestBoard_ShowsWhatBlockedWorkIsWaitingOn(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo, "lib": repo}, StubRunner{}, nil)

	upstream, err := svc.CreateTask(CreateTaskRequest{Project: "lib", Objective: "the dependency"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	downstream, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "the dependent"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.AddDependency(downstream.ID, upstream.ID); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}

	view, err := svc.ProgrammeBoard()
	if err != nil {
		t.Fatalf("ProgrammeBoard: %v", err)
	}
	card, ok := cardOf(view, downstream.ID)
	if !ok {
		t.Fatal("the dependent task is not on the board")
	}
	if columnOf(view, downstream.ID) != BoardBlocked {
		t.Errorf("the dependent is in %q, want blocked", columnOf(view, downstream.ID))
	}
	if len(card.BlockedOn) != 1 || card.BlockedOn[0] != upstream.ID {
		t.Errorf("blockedOn = %v, want [%s]", card.BlockedOn, upstream.ID)
	}
	// And the board spans projects: that is the whole point of it.
	if len(view.Projects) != 2 {
		t.Errorf("projects = %v, want both app and lib", view.Projects)
	}
}

// TestBoard_AgentProjectionCarriesTheOpaqueQueueIDAndNoPath is the Sprint-60
// property, re-asserted on a new surface.
//
// Every read an agent can reach has to be checked for this: the queue id tells it
// which projects serialize against each other, which it legitimately needs, while
// the host path is layout it has no business learning — and once it is in the
// append-only log, it is there permanently.
func TestBoard_AgentProjectionCarriesTheOpaqueQueueIDAndNoPath(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "work"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	human, err := svc.ProgrammeBoard()
	if err != nil {
		t.Fatalf("human board: %v", err)
	}
	hCard, ok := cardOf(human, task.ID)
	if !ok {
		t.Fatal("task missing from the human board")
	}
	if hCard.QueueID == "" {
		t.Error("the human board has no queue id")
	}
	if hCard.RepoPath == "" {
		t.Error("the human board should include the repository path")
	}

	agentView, err := svc.WithCaller(Agent()).ProgrammeBoard()
	if err != nil {
		t.Fatalf("agent board: %v", err)
	}
	aCard, ok := cardOf(agentView, task.ID)
	if !ok {
		t.Fatal("task missing from the agent board")
	}
	if aCard.RepoPath != "" {
		t.Errorf("the agent board leaked a host path: %q", aCard.RepoPath)
	}
	if aCard.QueueID != hCard.QueueID {
		t.Errorf("agent queue id %q != human queue id %q — the opaque id must be the SAME identity, "+
			"or two callers cannot talk about the same queue", aCard.QueueID, hCard.QueueID)
	}
}

// TestBoard_EmptyColumnsAreStillRendered: "nothing is blocked" is an answer, and a
// column that disappears when it empties makes it indistinguishable from a board
// that forgot about blocking.
func TestBoard_EmptyColumnsAreStillRendered(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)

	view, err := svc.ProgrammeBoard()
	if err != nil {
		t.Fatalf("ProgrammeBoard: %v", err)
	}
	if len(view.Columns) != len(boardColumns) {
		t.Fatalf("%d columns on an empty board, want all %d", len(view.Columns), len(boardColumns))
	}
	for i, col := range view.Columns {
		if col.Key != boardColumns[i].Key {
			t.Errorf("column %d is %q, want %q — the display order is part of the contract", i, col.Key, boardColumns[i].Key)
		}
	}
}

// TestBoard_ReportsUndeliveredSteering: an operator who steered a Job finds out
// here that nothing was delivered, rather than by wondering why the Job carried on
// exactly as before.
func TestBoard_ReportsUndeliveredSteering(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)
	task, job := stageSteerableJob(t, svc, store, "app")

	steer, err := svc.SteerJob(job.ID, "change course")
	if err != nil {
		t.Fatalf("SteerJob: %v", err)
	}
	if steer.State != SteerUndeliverable {
		t.Fatalf("precondition: state = %q, want undeliverable", steer.State)
	}

	view, err := svc.ProgrammeBoard()
	if err != nil {
		t.Fatalf("ProgrammeBoard: %v", err)
	}
	card, ok := cardOf(view, task.ID)
	if !ok {
		t.Fatal("task missing from the board")
	}
	if card.Steering == "" {
		t.Fatal("the board says nothing about the steering attempt")
	}
	if !strings.Contains(card.Steering, string(SteerUndeliverable)) {
		t.Errorf("steering summary = %q, want it to name the undeliverable outcome", card.Steering)
	}
}

// TestBoard_CountsTheHumanQueues.
func TestBoard_CountsTheHumanQueues(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "work"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	for _, to := range []State{StateQueued, StateWorking, StateCandidate, StateVerifying, StateVerified, StateApprovalRequired} {
		if _, err := store.TransitionTask(task.ID, to, false, ""); err != nil {
			t.Fatalf("→%s: %v", to, err)
		}
	}
	// An agent proposal, awaiting a human.
	if _, err := svc.WithCaller(Agent()).CancelTask(task.ID); err == nil {
		t.Fatal("an agent cancel should have been refused as a proposal")
	}

	view, err := svc.ProgrammeBoard()
	if err != nil {
		t.Fatalf("ProgrammeBoard: %v", err)
	}
	if view.PendingApprovals != 1 {
		t.Errorf("pendingApprovals = %d, want 1", view.PendingApprovals)
	}
	if view.PendingProposals != 1 {
		t.Errorf("pendingProposals = %d, want 1", view.PendingProposals)
	}
}

// --- helpers --------------------------------------------------------------------

func columnOf(view BoardView, taskID string) string {
	for _, col := range view.Columns {
		for _, c := range col.Cards {
			if c.TaskID == taskID {
				return col.Key
			}
		}
	}
	return ""
}

func cardOf(view BoardView, taskID string) (BoardCard, bool) {
	for _, col := range view.Columns {
		for _, c := range col.Cards {
			if c.TaskID == taskID {
				return c, true
			}
		}
	}
	return BoardCard{}, false
}
