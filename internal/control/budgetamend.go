// Copyright (C) 2026 Techdelight BV

package control

import "fmt"

// Amending a Task's frozen budget (backlog #95, item 4).
//
// THE GAP IT CLOSES. A Task's envelope is captured at create and stored
// authoritatively, so nothing can widen the bound on its own work. That is
// right, and it is why `budgets.json` reaches only new tasks — a running Task's
// ceiling must not move under it.
//
// But the OPERATOR could not move it either, and they are the party whose money
// it is. Two tasks died of that on 2026-08-25: T-28 exhausted three attempts on
// faults that were mostly the plane's own, and T-29 exhausted three on a
// milestone that genuinely took four passes. In both cases the only route was
// cancel-and-recreate, which destroys the task's history, its reviews, its
// rationale and its lineage — and none of that is what the operator wanted to
// throw away. Recreating also loses the thing most worth keeping: the record of
// how many attempts the work actually took, which is the number that would
// justify a different ceiling next time.
//
// MODELLED ON `task checks`, which is the existing precedent for amending
// something frozen: one transaction, an event carrying the caller and the
// lineage, and human callers only. Three properties do the work:
//
//   - HUMAN ONLY, and refused outright rather than offered as a proposal. An
//     agent that could ask for a wider envelope — even with a human confirming —
//     turns the bound into a negotiation, and the whole point of §6 is that it is
//     not one. This is the same reasoning that keeps budgets.json host-side.
//   - NEVER ABOVE THE PROJECT CEILING. Raising that is still an edit to the
//     host-side policy file, which is where authority over spend belongs. An
//     amendment can move a Task within its project's envelope and no further.
//   - RECORDED WITH ITS LINEAGE. "This task was given two more attempts, by a
//     human, at this time" is a fact the record must carry, or the budget stops
//     being evidence of anything.
//
// It deliberately does NOT touch wall-clock, concurrency, or the advisory axes.
// Attempts and review cycles are the two that produced dead ends; the rest can
// be added when something argues for them.

// AmendBudgetRequest raises a Task's frozen limits, within its project ceiling.
// A zero axis means "leave this one alone" — not "set it to zero", which would
// read as unbounded at every enforcement site.
type AmendBudgetRequest struct {
	MaxAttempts     int `json:"maxAttempts,omitempty"`
	MaxReviewCycles int `json:"maxReviewCycles,omitempty"`
}

// empty reports whether the request asks for nothing at all.
func (r AmendBudgetRequest) empty() bool { return r.MaxAttempts == 0 && r.MaxReviewCycles == 0 }

// AmendTaskBudget raises a Task's attempt or review-cycle limit.
func (s *Service) AmendTaskBudget(id string, req AmendBudgetRequest) (Task, error) {
	return s.amendTaskBudget(Human(), id, req)
}

// amendTaskBudget is AmendTaskBudget with an explicit caller identity.
func (s *Service) amendTaskBudget(caller Caller, id string, req AmendBudgetRequest) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if caller.IsAgent() {
		return Task{}, s.refuse("task", id, EventBudget, ReasonForbidden,
			"an agent may not amend a budget, and may not propose one either: the envelope is what "+
				"bounds an agent's own work, and a bound it can ask to move is not a bound. A human "+
				"raises it with `daedalus task budget`, within the project ceiling")
	}
	if req.empty() {
		return Task{}, fmt.Errorf("control: nothing to amend — give --attempts or --review-cycles")
	}

	task, err := s.store.GetTask(id)
	if err != nil {
		return Task{}, err
	}
	// A terminal Task has nothing left to spend. Refused rather than allowed as a
	// no-op, because an operator raising the budget of a cancelled task has
	// misunderstood something and should be told now.
	if IsTerminal(task.State) {
		return Task{}, fmt.Errorf("%w: task %s is %s — a finished task has nothing to spend",
			ErrWrongState, id, task.State)
	}

	amended := task.Budget
	if req.MaxAttempts > 0 {
		amended.MaxAttempts = req.MaxAttempts
	}
	if req.MaxReviewCycles > 0 {
		amended.MaxReviewCycles = req.MaxReviewCycles
	}
	if axis, bad := amended.invalidAxis(); bad {
		return Task{}, s.refuse("task", id, EventBudget, ReasonInvalidBudget, fmt.Sprintf(
			"budget %s is negative; 0 means unbounded and negative is not a budget", axis))
	}
	// The project's ceiling still binds. This is an amendment WITHIN the envelope
	// the operator has already set for the project, never a way around it.
	ceiling := s.budgetCeiling(task.Project)
	if axis, over := ceiling.exceededBy(amended); over {
		// NAME THE FILE. "Raise it in the host-side budget policy" is a remedy an
		// operator cannot act on without already knowing where that file is and
		// what goes in it — and it is optional, so most installations have never
		// had one. A refusal that names an action nobody can find is the shape
		// docs/no-dead-ends.md is about, one notch softer.
		return Task{}, s.refuse("task", id, EventBudget, ReasonOverBudget, fmt.Sprintf(
			"%s would exceed the ceiling for %q (%s). Raise it in %s — the host-side budget "+
				`policy, which an agent cannot edit: {"projects": {%q: {"maxAttempts": N}}}. `+
				"It is re-read on every check, so no restart is needed",
			axis, task.Project, ceiling, DefaultBudgetPolicyPath(s.dataDir), task.Project))
	}

	// Said in full, because a budget that changed silently is a budget that
	// explains nothing later. Both the before and the after, and by whom.
	note := fmt.Sprintf("budget amended by hand: attempts %d → %d, review cycles %d → %d",
		task.Budget.MaxAttempts, amended.MaxAttempts,
		task.Budget.MaxReviewCycles, amended.MaxReviewCycles)
	return s.store.SetTaskBudget(id, amended, governanceMetaFor(caller), note)
}
