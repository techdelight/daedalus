// Copyright (C) 2026 Techdelight BV

package control

// Tiered authority — the lethal-trifecta defence, made structural
// (docs/guild-master-plan.md §6, "Project documents are untrusted input").
//
// The Guild Master reads project-controlled documents (README, VISION, ROADMAP…)
// AND holds action tools. That is the textbook lethal trifecta: private data +
// untrusted content + an action vector. Prompt hardening is not a defence against
// it; the defence is that the consequential actions are not available to that
// agent in the first place.
//
// So operations are tiered by caller class:
//
//	Allowed   — the agent may execute it directly.
//	Proposal  — the agent may only ASK; the operation is recorded as a proposal
//	            and a human confirms or denies it.
//
// A poisoned README can therefore cause a proposal to appear in a human's queue.
// It cannot cancel a Job, land a change, or approve anything — which holds only
// because TierFor grants direct execution to an EXPLICITLY human class and
// treats everything else, including a zero-valued Caller, as an agent. Written
// the other way round ("not an agent → allowed") the sentence would be false for
// any caller nobody remembered to classify.
//
// Note what is absent by construction rather than by rule: there is no
// raise-a-budget operation at all. A budget is resolved at task create against a
// ceiling held in a host-side file, and a request may only ever narrow it
// (budget.go), so "the Guild Master raises its own budget" is not an operation
// that can be attempted, tiered, or refused — it does not exist.

// Tier is the authority a caller class has over one operation.
type Tier int

const (
	// TierAllowed: execute directly.
	TierAllowed Tier = iota
	// TierProposal: record a proposal for a human to confirm; do not execute.
	TierProposal
)

// Operation names. These are the strings that appear in a proposal row and in
// the event log, so they are part of the record and must stay stable.
const (
	OpCreateTask    = "create_task"
	OpDispatch      = "dispatch_task"
	OpVerify        = "request_verification"
	OpReview        = "request_review"
	OpRetry         = "retry_task"
	OpReverify      = "reverify_task"
	OpReplan        = "replan_task"
	OpCancel        = "cancel_task"
	OpApprove       = "approve_task"
	OpRejectAppr    = "reject_task"
	OpIntegrate     = "request_integration"
	OpSyncTarget    = "sync_target"
	OpListTasks     = "list_tasks"
	OpTaskStatus    = "get_task"
	OpTaskEvents    = "task_events"
	OpApprovals     = "list_pending_approvals"
	OpTargets       = "list_targets"
	OpProposalAct   = "confirm_or_deny_proposal"
	OpAddDependency = "add_dependency"
	OpSteer         = "steer_job"
	OpCancelSteer   = "cancel_steering"
	OpJobSteering   = "job_steering"
	OpBoard         = "programme_board"
)

// agentAuthority is the authority table for CallerAgent. Anything absent is
// TierAllowed for reads; every WRITE is listed explicitly, so adding a new
// mutating operation and forgetting to tier it fails the table test rather than
// silently granting an agent the power to run it.
var agentAuthority = map[string]Tier{
	// Reads: always free. They are how the Guild Master is useful at all.
	OpListTasks:  TierAllowed,
	OpTaskStatus: TierAllowed,
	OpTaskEvents: TierAllowed,
	OpApprovals:  TierAllowed,
	OpTargets:    TierAllowed,
	// The board and a Job's steering history are reads over state the agent can
	// already list, projected through the same caller-aware rendering as
	// ProjectTargets — an agent sees opaque queue ids and no host paths.
	OpBoard:       TierAllowed,
	OpJobSteering: TierAllowed,

	// Bounded creation: allowed, because it cannot exceed policy. The budget is
	// clamped to the project ceiling and the acceptance oracle is frozen at the
	// plane-owned target, so the worst a poisoned doc achieves is a task nobody
	// wanted — visible, budgeted, and cancellable by a human.
	OpCreateTask: TierAllowed,
	// Asking the plane to verify or review a candidate is asking it to APPLY ITS
	// OWN oracle. It cannot influence the verdict, so there is nothing to gate.
	OpVerify: TierAllowed,
	OpReview: TierAllowed,

	// Consequential: proposals only.
	//
	// dispatch/retry/replan spend budget and start containers; cancel destroys
	// another Job's work; integrate lands code; approve/reject is the human
	// authority gate itself, and an agent that could approve would make "the
	// Guild Master cannot approve its own work" false. sync_target re-points the
	// acceptance oracle, which is the one operation that could undo the Sprint-59
	// structural fix.
	OpDispatch: TierProposal,
	OpRetry:    TierProposal,
	// Re-verification looks like a read — it asks the plane to apply its own oracle
	// again — and OpVerify is TierAllowed for exactly that reason. It is tiered
	// anyway, because the two are not the same request. Verifying a candidate is
	// asking for a verdict that has not been given; re-verifying is asking the plane
	// to SET ASIDE a verdict it already reached. An agent that could do that at will
	// would have an unbounded oracle: not one that changes any single verdict, but
	// one it may re-roll until a flaky check happens to pass. The `--amended` mode
	// is more consequential still — it re-freezes the acceptance policy, which is
	// the same authority as OpSyncTarget and tiered the same way.
	OpReverify:   TierProposal,
	OpReplan:     TierProposal,
	OpCancel:     TierProposal,
	OpIntegrate:  TierProposal,
	OpApprove:    TierProposal,
	OpRejectAppr: TierProposal,
	OpSyncTarget: TierProposal,
	// A dependency edge decides what must happen before a Task is graded, which is
	// as load-bearing as what grades it. An agent that could declare its own
	// dependencies could declare them satisfied.
	OpAddDependency: TierProposal,
	// Steering injects an instruction into work that is ALREADY RUNNING — at least
	// as consequential as cancelling it, and rather more subtle, because the Job
	// carries on and the change of direction is only visible in the log. An agent
	// that reads a poisoned README must not be able to redirect a human's in-flight
	// Job on the strength of it. Withdrawing an instruction is tiered with it:
	// letting an agent cancel a human's pending steer would hand it the same
	// control by subtraction.
	OpSteer:       TierProposal,
	OpCancelSteer: TierProposal,

	// Confirming a proposal is the human act the whole tier exists to reserve.
	// The refusal that actually enforces it lives in callerScope.ResolveProposal
	// and again in Service.resolveProposal — two independent layers, both tested —
	// because an agent must never reach the proposal machinery for this operation
	// at all. The entry here is the belt to those braces: if a future caller ever
	// routes this through the generic dispatch, the table answers TierProposal
	// rather than TierAllowed.
	OpProposalAct: TierProposal,
}

// mutatingOps is every operation that changes state. The authority table must
// have an explicit entry for each — see TestAuthority_EveryMutatingOpIsTiered.
var mutatingOps = []string{
	OpCreateTask, OpDispatch, OpVerify, OpReview, OpRetry, OpReverify, OpReplan,
	OpCancel, OpApprove, OpRejectAppr, OpIntegrate, OpSyncTarget, OpProposalAct,
	OpAddDependency, OpSteer, OpCancelSteer,
}

// TierFor returns the authority a caller class has over an operation.
//
// TWO fail-closed rules, and the direction of both matters:
//
//  1. Only an EXPLICITLY human class gets full authority. Anything else — the
//     zero value, an unrecognised string, a class a future listener forgot to
//     set — is treated as an agent. The inverse test (`class != CallerAgent →
//     allowed`) reads identically and is catastrophically different: it hands
//     full human authority to `Caller{}`, silently, with no error and no log
//     line. `Caller` is exported with an exported field, so a zero value is one
//     refactor away at any time.
//
//  2. An unknown OPERATION is TierProposal for a non-human caller, never
//     TierAllowed: an operation nobody thought to tier must fail closed into
//     "ask a human".
//
// This matches parseCallerClass, which resolves an unreadable class to agent for
// exactly the same reason: human is the privileged answer, so it must be the one
// that has to be proven rather than the one that is assumed.
func TierFor(class CallerClass, op string) Tier {
	if class != CallerHuman {
		if tier, known := agentAuthority[op]; known {
			return tier
		}
		return TierProposal
	}
	return TierAllowed
}
