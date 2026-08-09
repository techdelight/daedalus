// Copyright (C) 2026 Techdelight BV

package control

import (
	"errors"
	"fmt"
)

// RejectionReason is the machine-readable "why" behind a control-plane refusal
// or a negative verification verdict (docs/guild-master-plan.md §6: "The plane
// can reject the Guild Master"). It exists so a client can distinguish
// *refused by policy* from *failed* — a governed plane that only ever said
// "error" would be indistinguishable from a broken one.
//
// Two shapes of "no" share this vocabulary, deliberately:
//
//   - a REFUSAL — the plane declines to act on a request; nothing changes state,
//     the reason travels back as a *RejectionError (HTTP 422, CLI exit 3), and a
//     decision event is appended to the log;
//   - a VERDICT — the plane acted, and the outcome was a rejection; the Task/Job
//     land in `rejected` and the reason is recorded on the transition event and
//     returned on VerifyResult.Reason.
type RejectionReason string

const (
	// ReasonNone is the zero value: no rejection.
	ReasonNone RejectionReason = ""

	// --- refusals (policy said no; no state change) ---

	// ReasonOverBudget: the requested budget widens the project's ceiling.
	ReasonOverBudget RejectionReason = "over_budget"
	// ReasonInvalidBudget: the requested budget is not a budget at all — a
	// negative axis. Separated from over_budget because it is malformed input, not
	// an ambitious ask; see Budget.invalidAxis for why it must never be treated as
	// "unbounded".
	ReasonInvalidBudget RejectionReason = "invalid_budget"
	// ReasonAttemptsExhausted: the Task has already used its max-attempts.
	ReasonAttemptsExhausted RejectionReason = "attempts_exhausted"
	// ReasonReviewCyclesExhausted: the Task has already used its max-review-cycles.
	ReasonReviewCyclesExhausted RejectionReason = "review_cycles_exhausted"
	// ReasonConcurrencyExceeded: the project already has its budgeted number of
	// running Jobs.
	ReasonConcurrencyExceeded RejectionReason = "concurrency_exceeded"
	// ReasonSchedulerSaturated: the control plane is at its GLOBAL job limit —
	// nothing is wrong with this task, the host is simply full.
	ReasonSchedulerSaturated RejectionReason = "scheduler_saturated"
	// ReasonDependenciesUnmet: the Task is waiting on another Task in the graph.
	// Not a fault — the work is well-formed and simply not yet runnable.
	ReasonDependenciesUnmet RejectionReason = "dependencies_unmet"
	// ReasonJoblessTask: a Task was left non-terminal with no Job at all — the
	// dispatch died between the state transition and the Job insert.
	ReasonJoblessTask RejectionReason = "jobless_task"
	// ReasonQueuedBehind: capacity exists, but an older Task is waiting for it.
	// This is the anti-starvation rule doing its job, not a fault.
	ReasonQueuedBehind RejectionReason = "queued_behind"
	// ReasonUnsafeRebase: the rebase target contains commits the Job itself
	// authored, so re-freezing the acceptance oracle there would adopt an oracle
	// the worker wrote. Refused (§6 — the oracle must live outside the agent's
	// write scope).
	ReasonUnsafeRebase RejectionReason = "unsafe_rebase"
	// ReasonOperationInFlight: the same Task already has a dispatch or verify
	// running in this process.
	ReasonOperationInFlight RejectionReason = "operation_in_flight"
	// ReasonProposalRecorded: an agent asked for a consequential operation; it was
	// recorded as a proposal for a human to confirm, and did NOT execute.
	ReasonProposalRecorded RejectionReason = "proposal_recorded"
	// ReasonForbidden: the caller class may not perform this operation at all, and
	// may not propose it either.
	ReasonForbidden RejectionReason = "forbidden"
	// ReasonApprovalRequired: the project's policy requires a human approval that
	// has not been given.
	ReasonApprovalRequired RejectionReason = "approval_required"
	// ReasonReviewRequired: an independent reviewer is configured and has not
	// passed this artifact.
	ReasonReviewRequired RejectionReason = "review_required"
	// ReasonIntegrationRaced: the integration transaction lost the compare-and-swap
	// repeatedly — the target kept moving. Nothing was landed.
	ReasonIntegrationRaced RejectionReason = "integration_raced"

	// --- verdicts (the plane acted; the artifact was rejected) ---

	// ReasonStaleBase: the candidate was built on a base_sha that is no longer the
	// project's target tip — it must be rebased and re-verified (§6).
	ReasonStaleBase RejectionReason = "stale_base"
	// ReasonNullAgentFloor: head_sha == base_sha, an empty change.
	ReasonNullAgentFloor RejectionReason = "null_agent_floor"
	// ReasonPolicyDrift: the acceptance policy at base_sha no longer hashes to the
	// value frozen at create.
	ReasonPolicyDrift RejectionReason = "policy_drift"
	// ReasonIntegrityGate: the Job's diff edits frozen acceptance files.
	ReasonIntegrityGate RejectionReason = "integrity_gate"
	// ReasonVerifyFailed: the clean verifier ran and reported failure.
	ReasonVerifyFailed RejectionReason = "verify_failed"
	// ReasonReviewFailed: the independent reviewer rejected the artifact.
	ReasonReviewFailed RejectionReason = "review_failed"
	// ReasonApprovalRejected: a human declined the change at the approval gate.
	ReasonApprovalRejected RejectionReason = "approval_rejected"
	// ReasonMergeConflict: the artifact does not replay cleanly onto the target.
	ReasonMergeConflict RejectionReason = "merge_conflict"
	// ReasonMergedVerifyFailed: the artifact verified alone but the MERGED result
	// failed — a semantic conflict, the failure the integration transaction exists
	// to catch.
	ReasonMergedVerifyFailed RejectionReason = "merged_verify_failed"
)

// allRejectionReasons is the closed set, for validation and tests.
var allRejectionReasons = map[RejectionReason]bool{
	ReasonOverBudget: true, ReasonInvalidBudget: true, ReasonAttemptsExhausted: true,
	ReasonReviewCyclesExhausted: true, ReasonConcurrencyExceeded: true,
	ReasonSchedulerSaturated: true, ReasonQueuedBehind: true, ReasonJoblessTask: true,
	ReasonDependenciesUnmet: true,
	ReasonUnsafeRebase:      true, ReasonOperationInFlight: true,
	ReasonApprovalRequired: true, ReasonReviewRequired: true, ReasonIntegrationRaced: true,
	ReasonProposalRecorded: true, ReasonForbidden: true,
	ReasonStaleBase: true, ReasonNullAgentFloor: true,
	ReasonPolicyDrift: true, ReasonIntegrityGate: true, ReasonVerifyFailed: true,
	ReasonReviewFailed: true, ReasonApprovalRejected: true,
	ReasonMergeConflict: true, ReasonMergedVerifyFailed: true,
}

// IsValidRejectionReason reports whether r is a known reason (ReasonNone is not
// a reason — it is the absence of one).
func IsValidRejectionReason(r RejectionReason) bool { return allRejectionReasons[r] }

// AllRejectionReasons lists every reason, sorted by declaration groups (refusals
// first, then verdicts). Used by the docs/CLI and by tests over the enum.
func AllRejectionReasons() []RejectionReason {
	return []RejectionReason{
		ReasonOverBudget, ReasonInvalidBudget, ReasonAttemptsExhausted,
		ReasonReviewCyclesExhausted, ReasonConcurrencyExceeded, ReasonSchedulerSaturated,
		ReasonQueuedBehind, ReasonJoblessTask, ReasonDependenciesUnmet, ReasonUnsafeRebase,
		ReasonOperationInFlight, ReasonProposalRecorded, ReasonForbidden,
		ReasonApprovalRequired, ReasonReviewRequired,
		ReasonIntegrationRaced, ReasonStaleBase, ReasonNullAgentFloor,
		ReasonPolicyDrift, ReasonIntegrityGate, ReasonVerifyFailed, ReasonReviewFailed,
		ReasonApprovalRejected, ReasonMergeConflict, ReasonMergedVerifyFailed,
	}
}

// RejectionError is a *refusal*: the control plane declined a request as a
// matter of policy. It is distinct from an ordinary error (something broke) and
// travels intact end-to-end — the daemon maps it to HTTP 422 with the reason in
// the error envelope, the client re-raises it, and `daedalus` exits 3. Callers
// branch on it with errors.As.
type RejectionError struct {
	Reason  RejectionReason
	Message string
	// Entity is the id the refusal concerns (task or job), for the event log.
	Entity string
}

func (e *RejectionError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("control: refused by policy (%s)", e.Reason)
	}
	return fmt.Sprintf("control: refused by policy (%s): %s", e.Reason, e.Message)
}

// Rejected reports whether err is (or wraps) a control-plane policy refusal, and
// returns its reason. Convenience over errors.As for callers that only need the
// reason (the CLI's exit-code mapping).
func Rejected(err error) (RejectionReason, bool) {
	var rej *RejectionError
	if errors.As(err, &rej) {
		return rej.Reason, true
	}
	return ReasonNone, false
}
