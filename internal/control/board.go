// Copyright (C) 2026 Techdelight BV

package control

import (
	"fmt"
	"sort"
)

// The programme board (docs/guild-master-plan.md M17): one cross-project view of
// everything the control plane is doing.
//
// It is DERIVED, not stored. There is no board table, no board state, and nothing
// here that another part of the plane must remember to update — the board is a
// projection of the same rows the CLI, the daemon and the event log already read.
// A board with its own state would be a second answer to "what is happening", and
// the whole arc has been about there being exactly one.
//
// It REUSES rather than duplicates, deliberately:
//
//   - the pending-approvals queue is Sprint 59's PendingApprovals;
//   - "blocked, and on what" is Sprint 62's DependencyStatusFor;
//   - the concurrency picture is Sprint 61's PlaneStatus;
//   - the agent-facing projection carries Sprint 60's OPAQUE QUEUE IDS and never
//     the host repository path — an agent that can see the board learns which
//     projects serialize against each other, and nothing about host layout.
//
// Every state in the state machine maps to exactly one column, asserted by
// TestBoard_EveryStateHasAColumn. A board that silently dropped a state would be
// worse than no board: work would appear to have vanished rather than to have
// gone somewhere the reader did not expect.

// Board column keys. These appear in JSON and in the Web/TUI, so they are stable.
//
// `in_review` was split into three in Sprint 65, after an operator read the board
// and found that "In verification" held work needing APPROVAL and "Awaiting
// approval" held work needing INTEGRATION. Both titles were true of the lifecycle
// phase and false about what to do, which is the wrong trade for a board: a board
// is read to answer "whose move is it, and what is the move", and a column whose
// title names a phase buries the answer under work the plane is still handling.
// Nothing consumed the old keys by name — the CLI and Web render whatever columns
// the response carries — so the rename cost nothing but this paragraph.
const (
	BoardRunning     = "running"
	BoardQueued      = "queued"
	BoardBlocked     = "blocked"
	BoardVerifying   = "verifying"
	BoardNeedsAction = "needs_decision"
	BoardApproval    = "awaiting_approval"
	BoardReadyToLand = "ready_to_land"
	BoardLanded      = "landed"
	BoardWithdrawn   = "withdrawn"
)

// boardColumns is the display order, with the human-readable title for each.
//
// The order is the pipeline, and the titles say who is holding it: the plane is
// working on the first four, and the operator is holding every one after that
// until `Landed`.
var boardColumns = []struct{ Key, Title string }{
	{BoardQueued, "Queued"},
	{BoardBlocked, "Blocked"},
	{BoardRunning, "Running"},
	{BoardVerifying, "Being verified"},
	{BoardNeedsAction, "Rejected — needs a decision"},
	{BoardApproval, "Awaiting your approval"},
	{BoardReadyToLand, "Approved — ready to land"},
	{BoardLanded, "Landed"},
	{BoardWithdrawn, "Closed without landing"},
}

// columnForState maps every State to exactly one column, grouped by WHOSE MOVE
// IT IS rather than by lifecycle phase.
//
//   - candidate/verifying — the plane is grading it; nothing is being asked of
//     anyone.
//   - rejected — the operator's move, and a decision rather than a defeat:
//     retry, replan, or re-verify. It gets its own column rather than sitting
//     with the cancelled work, because it is one command from running again, and
//     rather than sitting with the in-flight work, because nothing will move it
//     until a human chooses.
//   - verified/approval_required — the operator's move, and it is `approve`.
//     `verified` used to sit with the in-flight work, which is how a Task could
//     wait indefinitely under a heading that said the plane was busy with it.
//   - approved — the operator's move, and it is `integrate`, NOT approve. Filing
//     it under "awaiting approval" described something that had already happened.
var columnForState = map[State]string{
	StatePlanned:          BoardQueued,
	StateQueued:           BoardQueued,
	StateBlocked:          BoardBlocked,
	StateWorking:          BoardRunning,
	StateInputRequired:    BoardRunning,
	StateCandidate:        BoardVerifying,
	StateVerifying:        BoardVerifying,
	StateRejected:         BoardNeedsAction,
	StateVerified:         BoardApproval,
	StateApprovalRequired: BoardApproval,
	StateApproved:         BoardReadyToLand,
	StateIntegrated:       BoardLanded,
	StateFailed:           BoardWithdrawn,
	StateCancelled:        BoardWithdrawn,
	StateExpired:          BoardWithdrawn,
}

// BoardCard is one Task as the board shows it.
type BoardCard struct {
	TaskID    string `json:"taskId"`
	Project   string `json:"project"`
	Objective string `json:"objective"`
	State     string `json:"state"`
	UpdatedAt string `json:"updatedAt"`
	// QueueID is the opaque identity of the merge queue this project lands on
	// (Sprint 60). Always present, always safe to show any caller; two cards with
	// the same QueueID will serialize against each other at integration.
	QueueID string `json:"queueId,omitempty"`
	// RepoPath is the absolute host path, for HUMAN callers only.
	RepoPath string `json:"repoPath,omitempty"`
	// BlockedOn / Unsatisfiable are the dependency answer to "and on what".
	BlockedOn     []string `json:"blockedOn,omitempty"`
	Unsatisfiable []string `json:"unsatisfiable,omitempty"`
	// QueuedForCapacity distinguishes "waiting for a dependency" from "waiting for
	// a free slot" — both look like "not started" and have completely different
	// remedies.
	QueuedForCapacity bool `json:"queuedForCapacity,omitempty"`
	// Steering summarises the latest instruction aimed at this Task's Jobs, when
	// there is one — including, importantly, when it was UNDELIVERABLE.
	Steering string `json:"steering,omitempty"`
}

// BoardColumn is one column with its cards, oldest Task first.
type BoardColumn struct {
	Key   string      `json:"key"`
	Title string      `json:"title"`
	Cards []BoardCard `json:"cards"`
}

// BoardView is the whole board: the columns plus the two numbers an operator asks
// for next ("is anything waiting on me?").
type BoardView struct {
	Columns []BoardColumn `json:"columns"`
	Plane   PlaneStatus   `json:"plane"`
	// PendingApprovals and PendingProposals are the two human queues. They are
	// counts here and full views elsewhere: the board says whether to look, the
	// existing surfaces say at what.
	PendingApprovals int `json:"pendingApprovals"`
	PendingProposals int `json:"pendingProposals"`
	// Projects is every project with a Task on the board, sorted.
	Projects []string `json:"projects"`
}

// ProgrammeBoard renders the cross-project board for a HUMAN caller.
func (s *Service) ProgrammeBoard() (BoardView, error) { return s.programmeBoard(Human()) }

// programmeBoard renders the board for a caller class.
//
// It takes no lock of its own. Every read below is individually consistent and the
// board is a snapshot by nature; taking s.mu would additionally deadlock against
// PlaneStatus, which takes it itself.
func (s *Service) programmeBoard(caller Caller) (BoardView, error) {
	tasks, err := s.store.ListTasks()
	if err != nil {
		return BoardView{}, err
	}

	// Resolving queue identity shells out to git once per REGISTERED project
	// (CanonicalRepoPath), so it is skipped entirely when there is nothing to
	// label. See queuesByProject for the cost this view carries.
	queues := map[string]queueIdentity{}
	if len(tasks) > 0 {
		queues = s.queuesByProject(caller)
	}
	waiting := map[string]bool{}
	plane, planeErr := s.PlaneStatus()
	if planeErr == nil {
		for _, id := range plane.Waiting {
			waiting[id] = true
		}
	}

	cards := map[string][]BoardCard{}
	projects := map[string]bool{}
	for _, t := range tasks {
		column, known := columnForState[t.State]
		if !known {
			// Unreachable through the state machine, but a hand-edited database or a
			// future state must not vanish from the operator's view.
			column = BoardWithdrawn
		}
		card := BoardCard{
			TaskID: t.ID, Project: t.Project, Objective: t.Objective,
			State: string(t.State), UpdatedAt: t.UpdatedAt,
			QueuedForCapacity: waiting[t.ID],
		}
		if q, ok := queues[t.Project]; ok {
			card.QueueID, card.RepoPath = q.queueID, q.repoPath
		}
		if status, err := s.store.DependencyStatusFor(t.ID); err == nil {
			card.BlockedOn, card.Unsatisfiable = status.Unmet, status.Unsatisfiable
		}
		card.Steering = s.latestSteeringSummary(t.ID)
		projects[t.Project] = true
		cards[column] = append(cards[column], card)
	}

	view := BoardView{Plane: plane}
	for _, col := range boardColumns {
		// Every column is emitted even when empty: "nothing is blocked" is an answer
		// an operator wants, and a missing column reads as a missing feature.
		view.Columns = append(view.Columns, BoardColumn{
			Key: col.Key, Title: col.Title, Cards: cards[col.Key],
		})
	}
	for name := range projects {
		view.Projects = append(view.Projects, name)
	}
	sort.Strings(view.Projects)

	if approvals, err := s.PendingApprovals(); err == nil {
		view.PendingApprovals = len(approvals)
	}
	if proposals, err := s.store.ListProposals(ProposalPending); err == nil {
		view.PendingProposals = len(proposals)
	}
	return view, nil
}

// latestSteeringSummary describes the most recent instruction aimed at a Task, or
// "" when there is none.
//
// An UNDELIVERABLE instruction is reported as loudly as a delivered one. That is
// the point of the column existing at all: the operator who issued it needs to
// find out here, not by wondering why the Job carried on as before.
func (s *Service) latestSteeringSummary(taskID string) string {
	steers, err := s.store.ListSteeringForTask(taskID)
	if err != nil || len(steers) == 0 {
		return ""
	}
	last := steers[len(steers)-1]
	return fmt.Sprintf("%s (%s)", last.ID, last.State)
}

// queueIdentity is a project's merge-queue identity as one caller may see it.
type queueIdentity struct{ queueID, repoPath string }

// queuesByProject inverts the target view into a project → queue lookup, honouring
// the caller class: an agent gets the opaque id and an empty path (Sprint 60).
//
// KNOWN COST, stated rather than hidden: this resolves every registered project's
// canonical repository path, which is a `git rev-parse` per project. It is the
// most expensive thing the board does, and the board is the first control-plane
// read a UI polls, so the Web panel polls it at a slower interval than the
// approvals queue. A cache with a TTL would fix it properly; it is not built here
// because a stale queue identity is a wrong answer about which work serializes,
// and that is worse than a slow one.
func (s *Service) queuesByProject(caller Caller) map[string]queueIdentity {
	out := map[string]queueIdentity{}
	targets, err := s.projectTargets(caller)
	if err != nil {
		return out
	}
	for _, t := range targets {
		for _, project := range t.Projects {
			out[project] = queueIdentity{queueID: t.QueueID, repoPath: t.RepoPath}
		}
	}
	return out
}
