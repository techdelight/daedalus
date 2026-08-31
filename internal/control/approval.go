// Copyright (C) 2026 Techdelight BV

package control

import "fmt"

// Human approval (docs/guild-master-plan.md §6, rung 6 — and the reason the
// Guild Master "can never approve its own work").
//
//	verified → approval_required → approved → integrated
//
// Approval is a PLANE-AUTHORITY transition. `approval_required → approved` is
// absent from workerReachable, exactly like everything past `candidate`, so no
// worker-driven request can approve anything — that part is structural.
//
// WHAT THIS DOES NOT YET PROVE, stated plainly because it is easy to oversell.
// The state machine stops a *worker* approving. It does not by itself stop an
// agent CLIENT of control.sock approving, because today the actor recorded on an
// approval is a label of the request's origin, not an authenticated identity
// (see Actor in store.go). "The Guild Master cannot approve its own work" is
// therefore enforced by the SOCKET BOUNDARY that Sprint 60 introduces — a
// separate socket per class of caller, since peer credentials alone cannot
// separate an agent from a human running under the same uid — and not by this
// state machine alone. Until then, every client of control.sock is the human CLI,
// and that is the whole of the guarantee.
//
// Approval is also OPT-IN per project (§9: "keep governance opt-in"). A project
// that has not asked for it goes `verified → approval_required → approved` driven
// by the plane in one step at integration time, with an event recording that the
// policy did not require a human — so the audit trail says why, rather than
// silently skipping two states.

// ApprovalPolicy declares which projects require a human approval before a
// verified artifact may be integrated. It lives in the same host-side governance
// file as budgets (see BudgetPolicy) — never in a project checkout, for the same
// reason: a project that could grant itself an approval exemption by committing a
// file would not be governed at all.
type ApprovalPolicy struct {
	// Default applies to any project without an explicit entry.
	Default bool `json:"default"`
	// Projects overrides the default per project.
	Projects map[string]bool `json:"projects,omitempty"`
}

// RequiredFor reports whether a project requires human approval.
func (p ApprovalPolicy) RequiredFor(project string) bool {
	if v, ok := p.Projects[project]; ok {
		return v
	}
	return p.Default
}

// requiresApproval resolves the project's approval policy through the policy
// source, defaulting to false (opt-in) when NO SOURCE IS CONFIGURED AT ALL.
//
// Note the distinction, which matters: "no policy source installed" is a
// deliberate deployment choice (host tests, a plane with no governance file
// wired) and means approval is opt-in per §9. "A policy source that cannot read
// its file" is a different thing entirely, and the source itself answers that by
// requiring approval — see FileBudgetPolicy.RequiresApproval. The plane never
// converts "I could not read the policy" into "no approval needed".
func (s *Service) requiresApproval(project string) bool {
	if s.budgets == nil {
		return false
	}
	return s.budgets.RequiresApproval(project)
}

// noApprovalNote is the note recorded when the plane approves without a human.
//
// It says what is actually known — the policy source did not require approval —
// rather than asserting what a policy "said". An event that claims a policy
// declared something is a lie whenever no policy could be read, and this is the
// one gate where the log is the only evidence a human was or was not involved.
func noApprovalNote(project string) string {
	return "auto-approved: no human approval is required for project " + project +
		" (the configured policy source did not request one)"
}

// ApproveTask records a human approval: approval_required → approved.
//
// A task resting at `verified` in a project that does not require approval is
// accepted too — approving something that did not need approving is harmless and
// avoids a confusing refusal — and is driven through `approval_required` so the
// log shows the full path rather than a shortcut.
func (s *Service) ApproveTask(id, note string) (Task, error) { return s.approveTask(Human(), id, note) }

// approveTask is ApproveTask with an explicit caller identity. Approval is
// reserved to human callers by the authority table (authority.go); the caller is
// threaded here so the EVENT records who actually approved.
func (s *Service) approveTask(caller Caller, id, note string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, err := s.store.GetTask(id)
	if err != nil {
		return Task{}, err
	}
	meta := EventMeta{Kind: EventApproval, Actor: caller.Actor()}
	detail := "approved"
	if note != "" {
		detail += ": " + note
	}
	// The guard first, so the switch below is only ever reached in a state the
	// table admits and the two cannot disagree about which those are.
	if err := requireOperable(OpApprove, id, task.State); err != nil {
		return Task{}, err
	}
	switch task.State {
	case StateVerified:
		if _, err := s.store.TransitionTaskWith(id, StateApprovalRequired, false, meta,
			"approval requested explicitly"); err != nil {
			return Task{}, err
		}
	case StateApprovalRequired:
		// already awaiting a decision
	case StateApproved:
		return task, nil // idempotent: approving twice is not an error
	}
	return s.store.TransitionTaskWith(id, StateApproved, false, meta, detail)
}

// RejectApproval records a human rejection: approval_required → rejected, which
// feeds the existing retry/replan ladder.
func (s *Service) RejectApproval(id, note string) (Task, error) {
	return s.rejectApproval(Human(), id, note)
}

// rejectApproval is RejectApproval with an explicit caller identity.
func (s *Service) rejectApproval(caller Caller, id, note string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, err := s.store.GetTask(id)
	if err != nil {
		return Task{}, err
	}
	meta := EventMeta{Kind: EventApproval, Reason: ReasonApprovalRejected, Actor: caller.Actor()}
	detail := "rejected by a human reviewer"
	if note != "" {
		detail += ": " + note
	}
	if err := requireOperable(OpRejectAppr, id, task.State); err != nil {
		return Task{}, err
	}
	// The Job follows its Task so a retry starts from a coherent chain.
	if job, ok, err := s.jobInState(id, StateVerified); err == nil && ok {
		s.driveJob(job.ID, []State{StateRejected}, meta, detail)
	}
	return s.store.TransitionTaskWith(id, StateRejected, false, meta, detail)
}

// ensureApproved brings a verified Task to `approved` when the project's policy
// does not require a human, and refuses when it does. s.mu must be held.
//
// This is what makes approval opt-in without introducing a state-machine
// shortcut: the same edges are walked either way, and the event log records which
// of the two happened.
func (s *Service) ensureApproved(task Task) (Task, error) {
	switch task.State {
	case StateApproved:
		return task, nil
	case StateApprovalRequired:
		if s.requiresApproval(task.Project) {
			return Task{}, s.refuseWithRemedies("task", task.ID, EventApproval, ReasonApprovalRequired,
				fmt.Sprintf("task %s needs human approval before it can be integrated", task.ID),
				task.ID, task.State, RemediesFrom(TargetTask, task.State))
		}
		return s.store.TransitionTaskWith(task.ID, StateApproved, false,
			EventMeta{Kind: EventApproval}, noApprovalNote(task.Project))
	case StateVerified:
		if s.requiresApproval(task.Project) {
			// Move it into the queue a human can see, then say no — the operator's
			// next step is `task approve`, and `task list` should show it waiting.
			moved, err := s.store.TransitionTaskWith(task.ID, StateApprovalRequired, false,
				EventMeta{Kind: EventApproval},
				"awaiting human approval: required for project "+task.Project+" by policy")
			if err != nil {
				return Task{}, err
			}
			_ = moved
			// The remedies are computed from where the Task now IS — it has just been
			// moved to `approval_required` — not from where it was. A refusal that
			// suggested an operation legal only in the state it just left would be the
			// exact defect #95 was filed for.
			return Task{}, s.refuseWithRemedies("task", task.ID, EventApproval, ReasonApprovalRequired,
				fmt.Sprintf("task %s needs human approval before it can be integrated", task.ID),
				task.ID, StateApprovalRequired, RemediesFrom(TargetTask, StateApprovalRequired))
		}
		if _, err := s.store.TransitionTaskWith(task.ID, StateApprovalRequired, false,
			EventMeta{Kind: EventApproval}, noApprovalNote(task.Project)); err != nil {
			return Task{}, err
		}
		return s.store.TransitionTaskWith(task.ID, StateApproved, false,
			EventMeta{Kind: EventApproval}, noApprovalNote(task.Project))
	}
	// Every state OpIntegrate admits has a case above, so reaching here means the
	// table admitted a state this switch does not handle. Refusing with an
	// explicit message beats returning a zero Task and a nil error, which is what
	// a bare `requireOperable` would do in that case.
	if err := requireOperable(OpIntegrate, task.ID, task.State); err != nil {
		return Task{}, err
	}
	return Task{}, fmt.Errorf("%w: task %s is %s — the operation table admits an integration from "+
		"there and ensureApproved has no case for it; this is a plane defect, not a bad request",
		ErrWrongState, task.ID, task.State)
}

// PendingApprovals lists the Tasks waiting on a human decision — the query behind
// `daedalus task list --pending-approval` and the Web/TUI approvals view.
func (s *Service) PendingApprovals() ([]Task, error) {
	all, err := s.store.ListTasks()
	if err != nil {
		return nil, err
	}
	var out []Task
	for _, t := range all {
		if t.State == StateApprovalRequired {
			out = append(out, t)
		}
	}
	return out, nil
}
