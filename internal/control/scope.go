// Copyright (C) 2026 Techdelight BV

package control

import "fmt"

// callerScope is a TaskAPI bound to one caller identity.
//
// It is where tiered authority (authority.go) is applied: the same Service
// method is reached directly for a human and turned into a proposal for an
// agent. Doing it here rather than inside each Service method keeps the
// authority decision in ONE readable table next to ONE dispatch, instead of a
// caller check scattered through a dozen operations where a missing one would be
// invisible.
//
// The caller is fixed at construction by the daemon, from the listener the
// request arrived on (caller.go). Nothing a client sends can change it.
type callerScope struct {
	svc    *Service
	caller Caller
}

// WithCaller returns a TaskAPI bound to a caller identity. The daemon builds one
// per listener; the in-process Service remains the human path.
func (s *Service) WithCaller(c Caller) TaskAPI { return &callerScope{svc: s, caller: c} }

// propose records a consequential operation for a human to confirm, and returns
// the typed refusal the caller sees in place of execution.
//
// The refusal is deliberate and loud rather than a silent success: an agent that
// believed it had cancelled a Job when it had only asked would go on to reason
// from a false premise, which is a worse failure than being told no.
func (c *callerScope) propose(op, taskID, argument string) error {
	// The caller class recorded on the row is the EFFECTIVE class, not the raw
	// field: a zero-valued or unrecognised Caller is an agent, and the proposal
	// must say so rather than recording an empty string that later reads as
	// something else.
	p, err := c.svc.store.CreateProposal(op, taskID, argument, CallerClass(c.caller.String()))
	if err != nil {
		return err
	}
	return &RejectionError{
		Reason: ReasonProposalRecorded,
		Message: fmt.Sprintf(
			"%s is not available to an %s caller; recorded as proposal %s for a human to confirm (daedalus task proposals confirm %s)",
			op, c.caller, p.ID, p.ID),
		Entity: taskID,
	}
}

// allowed reports whether this caller may execute op directly.
func (c *callerScope) allowed(op string) bool {
	return TierFor(c.caller.Class, op) == TierAllowed
}

// --- reads: always allowed ------------------------------------------------------

func (c *callerScope) ListTasks() ([]Task, error)               { return c.svc.ListTasks() }
func (c *callerScope) TaskStatus(id string) (StatusView, error) { return c.svc.TaskStatus(id) }
func (c *callerScope) TaskEvents(id string) ([]Event, error)    { return c.svc.TaskEvents(id) }
func (c *callerScope) PendingApprovals() ([]Task, error)        { return c.svc.PendingApprovals() }

// PlaneStatus is a read: how much is running is not sensitive, and an agent that
// cannot see saturation cannot reason about why its dispatch was refused. It
// carries counts and limits only — no paths, and no project names the caller
// could not already list.
func (c *callerScope) PlaneStatus() (PlaneStatus, error) { return c.svc.PlaneStatus() }

// TaskDependencies is a read: the graph is plane-owned, and an agent that can see
// what a Task waits on can plan around it without being able to change it.
func (c *callerScope) TaskDependencies(taskID string) (DependencyView, error) {
	return c.svc.TaskDependencies(taskID)
}

// AddDependency declares a graph edge. It is TIERED: the edge decides what must
// happen before a Task is graded, which is as load-bearing as what grades it, so
// an agent may propose it and a human confirms.
func (c *callerScope) AddDependency(taskID, dependsOn string) (DependencyEdge, error) {
	if !c.allowed(OpAddDependency) {
		return DependencyEdge{}, c.propose(OpAddDependency, taskID, dependsOn)
	}
	return c.svc.AddDependency(taskID, dependsOn)
}

// ProjectTargets renders the queue list for this caller: an agent receives the
// opaque queue id and never the host path (QueueIDFor).
func (c *callerScope) ProjectTargets() ([]TargetView, error) {
	return c.svc.projectTargets(c.caller)
}

// RefineTask re-runs work against its own review, which is precisely the shape
// the proposal tier exists for: an agent that could arm a continuation from a
// reading of its own output has a loop with nobody in it. A human confirms.
func (c *callerScope) RefineTask(id string, req RefineRequest) (Task, error) {
	if !c.allowed(OpRefine) {
		return Task{}, c.propose(OpRefine, id, req.ReviewID+" "+req.Note)
	}
	return c.svc.refineTask(c.caller, id, req)
}

// TargetLags is a read, allowed to any caller. It reports how far a checkout has
// moved past the target and carries no host paths — the project name and two
// commit ids, all of which an agent can already see on a Task.
//
// Worth an agent knowing: a Task it files is dispatched against the target, so a
// large gap means its work will be written against a tree the repository has
// moved past. It cannot fix that (syncing is TierProposal via OpSyncTarget), but
// it can say so rather than filing work that was doomed before it started.
func (c *callerScope) TargetLags() ([]TargetLag, error) { return c.svc.TargetLags() }

// ProgrammeBoard renders the cross-project board for this caller. Same rule as
// ProjectTargets, and for the same reason: an agent sees the opaque queue id, so
// it can tell which projects serialize against each other without learning where
// anything lives on the host.
func (c *callerScope) ProgrammeBoard() (BoardView, error) {
	return c.svc.programmeBoard(c.caller)
}

// JobSteering is a read: what a Job has been told is part of its history, and an
// agent that can read the event log can already see it.
func (c *callerScope) JobSteering(jobID string) ([]SteeringEvent, error) {
	return c.svc.JobSteering(jobID)
}

// SteerJob injects an instruction into RUNNING work. Tiered: an agent may ask, a
// human confirms.
//
// The Job is resolved to its Task before proposing, so the proposal lands on the
// Task's event log where an operator will actually see it — and so an agent naming
// a Job that does not exist gets ErrNotFound rather than a proposal for a human to
// puzzle over.
func (c *callerScope) SteerJob(jobID, instruction string) (SteeringEvent, error) {
	if !c.allowed(OpSteer) {
		job, err := c.svc.store.GetJob(jobID)
		if err != nil {
			return SteeringEvent{}, err
		}
		return SteeringEvent{}, c.propose(OpSteer, job.TaskID, encodeSteerArgument(jobID, instruction))
	}
	return c.svc.steerJob(c.caller, jobID, instruction)
}

// CancelSteering withdraws an undelivered instruction. Tiered with SteerJob: an
// agent that could cancel a human's pending steer would have the same control by
// subtraction.
func (c *callerScope) CancelSteering(steerID string) (SteeringEvent, error) {
	if !c.allowed(OpCancelSteer) {
		steer, err := c.svc.store.GetSteering(steerID)
		if err != nil {
			return SteeringEvent{}, err
		}
		return SteeringEvent{}, c.propose(OpCancelSteer, steer.TaskID, steerID)
	}
	return c.svc.cancelSteering(c.caller, steerID)
}

// --- bounded creation: allowed within policy ------------------------------------

func (c *callerScope) CreateTask(req CreateTaskRequest) (Task, error) {
	if !c.allowed(OpCreateTask) {
		return Task{}, c.propose(OpCreateTask, "", req.Project+": "+req.Objective)
	}
	return c.svc.createTask(c.caller, req)
}

// VerifyTask asks the plane to apply its OWN oracle to a candidate. The caller
// cannot influence the verdict, so there is nothing to gate.
func (c *callerScope) VerifyTask(id string, req VerifyRequest) (VerifyResult, error) {
	if !c.allowed(OpVerify) {
		return VerifyResult{}, c.propose(OpVerify, id, "")
	}
	return c.svc.verifyTask(c.caller, id, req)
}

func (c *callerScope) ReviewTask(id string) (ReviewResult, error) {
	if !c.allowed(OpReview) {
		return ReviewResult{}, c.propose(OpReview, id, "")
	}
	return c.svc.ReviewTask(id)
}

// --- consequential: proposals for an agent --------------------------------------

func (c *callerScope) DispatchTask(id string) (DispatchResult, error) {
	if !c.allowed(OpDispatch) {
		return DispatchResult{}, c.propose(OpDispatch, id, "")
	}
	return c.svc.DispatchTask(id)
}

func (c *callerScope) CancelTask(id string) (Task, error) {
	if !c.allowed(OpCancel) {
		return Task{}, c.propose(OpCancel, id, "")
	}
	return c.svc.cancelTask(c.caller, id)
}

func (c *callerScope) RetryTask(id string, req RetryRequest) (RetryResult, error) {
	if !c.allowed(OpRetry) {
		return RetryResult{}, c.propose(OpRetry, id, fmt.Sprintf("rebase=%v", req.Rebase))
	}
	return c.svc.retryTask(c.caller, id, req)
}

// AmendTaskChecks has NO tier check and NO proposal path, deliberately — see the
// note in authority.go. It calls straight through, and the service refuses an
// agent caller when it validates the checks, which is the same rule and the same
// function that guards them at create.
func (c *callerScope) AmendTaskChecks(id string, req AmendChecksRequest) (Task, error) {
	return c.svc.amendTaskChecks(c.caller, id, req)
}

// ReverifyTask sets aside a verdict the plane already reached. Tiered with the
// consequential operations rather than with VerifyTask, because an agent that
// could re-roll a grading at will has an oracle bounded only by patience.
func (c *callerScope) ReverifyTask(id string, req ReverifyRequest) (ReverifyResult, error) {
	if !c.allowed(OpReverify) {
		return ReverifyResult{}, c.propose(OpReverify, id, fmt.Sprintf("amended=%v", req.Amended))
	}
	return c.svc.reverifyTask(c.caller, id, req)
}

func (c *callerScope) ReplanTask(id string, req ReplanRequest) (Task, error) {
	if !c.allowed(OpReplan) {
		return Task{}, c.propose(OpReplan, id, req.Objective)
	}
	return c.svc.replanTask(c.caller, id, req)
}

func (c *callerScope) ApproveTask(id, note string) (Task, error) {
	if !c.allowed(OpApprove) {
		return Task{}, c.propose(OpApprove, id, note)
	}
	return c.svc.approveTask(c.caller, id, note)
}

func (c *callerScope) RejectApproval(id, note string) (Task, error) {
	if !c.allowed(OpRejectAppr) {
		return Task{}, c.propose(OpRejectAppr, id, note)
	}
	return c.svc.rejectApproval(c.caller, id, note)
}

func (c *callerScope) IntegrateTask(id string, req IntegrateRequest) (IntegrationResult, error) {
	if !c.allowed(OpIntegrate) {
		return IntegrationResult{}, c.propose(OpIntegrate, id, "")
	}
	return c.svc.IntegrateTask(id, req)
}

func (c *callerScope) SyncTarget(project string) (Target, error) {
	if !c.allowed(OpSyncTarget) {
		return Target{}, c.propose(OpSyncTarget, "", project)
	}
	return c.svc.syncTarget(c.caller, project)
}

// --- proposals ------------------------------------------------------------------

func (c *callerScope) ListProposals(state ProposalState) ([]Proposal, error) {
	return c.svc.ListProposals(state)
}

// ResolveProposal is HUMAN-ONLY, and refused rather than proposed.
//
// Making it proposable would let an agent queue "confirm my other proposal" and
// reproduce the hole one level up; that is why proposalOnly exists and why this
// returns a forbidden refusal instead of recording anything.
func (c *callerScope) ResolveProposal(id string, confirm bool, note string) (Proposal, error) {
	if c.caller.IsAgent() {
		return Proposal{}, &RejectionError{
			Reason: ReasonForbidden,
			Message: fmt.Sprintf(
				"confirming or denying a proposal is reserved to human callers; an %s caller was refused", c.caller),
			Entity: id,
		}
	}
	return c.svc.resolveProposal(c.caller, id, confirm, note)
}

// compile-time assertion: a caller-scoped API is a full TaskAPI.
var _ TaskAPI = (*callerScope)(nil)

// --- programmes (M20) -----------------------------------------------------------

// Reads. An agent that cannot see the programmes cannot notice what projects
// have in common, which is the one thing the Guild Master exists to do.
func (c *callerScope) ListProgrammes() ([]Programme, error)      { return c.svc.ListProgrammes() }
func (c *callerScope) GetProgramme(id string) (Programme, error) { return c.svc.GetProgramme(id) }
func (c *callerScope) ProgrammeStatusFor(id string) (ProgrammeStatus, error) {
	return c.svc.ProgrammeStatusFor(id)
}

// Writes. Forming a programme is a statement about what the work is FOR, so an
// agent may only ask. That is not a limitation on the Guild Master, it is its
// job description: it notices, a human agrees, and the agreement is what makes
// the noticing a programme.
func (c *callerScope) CreateProgramme(req ProgrammeRequest) (Programme, error) {
	if !c.allowed(OpFormProgramme) {
		return Programme{}, c.propose(OpFormProgramme, "", encodeProgrammeArgument(req))
	}
	return c.svc.CreateProgramme(req)
}

func (c *callerScope) UpdateProgramme(id string, req ProgrammeRequest) (Programme, error) {
	if !c.allowed(OpAmendProgramme) {
		return Programme{}, c.propose(OpAmendProgramme, id, encodeProgrammeArgument(req))
	}
	return c.svc.UpdateProgramme(id, req)
}

func (c *callerScope) DeleteProgramme(id string) error {
	if !c.allowed(OpDissolveProgramme) {
		return c.propose(OpDissolveProgramme, id, "dissolve "+id)
	}
	return c.svc.DeleteProgramme(id)
}
