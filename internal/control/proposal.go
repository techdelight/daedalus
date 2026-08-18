// Copyright (C) 2026 Techdelight BV

package control

import (
	"fmt"
	"strings"
)

// The human-confirmed proposal flow (docs/guild-master-plan.md §6).
//
// An agent that asks for a consequential operation gets a Proposal instead of an
// execution. A human then confirms — at which point the operation runs AS THE
// HUMAN — or denies it, and nothing happens.
//
// The property this buys, stated exactly: a poisoned project document can cause a
// row to appear in a human's queue. It cannot cancel a Job, land a change, or
// approve anything, because the only path from "proposal" to "effect" runs
// through a request on the human socket. That is §6's lethal-trifecta defence
// made structural — not a prompt asking an agent to behave.

// ListProposals returns proposals, optionally filtered by state.
func (s *Service) ListProposals(state ProposalState) ([]Proposal, error) {
	return s.store.ListProposals(state)
}

// ResolveProposal confirms or denies a proposal (human callers only; the agent
// path is refused in callerScope before it reaches here).
func (s *Service) ResolveProposal(id string, confirm bool, note string) (Proposal, error) {
	return s.resolveProposal(Human(), id, confirm, note)
}

// resolveProposal is ResolveProposal with an explicit caller identity.
//
// The caller is checked again here rather than trusted from the scope layer. Two
// checks for one rule is deliberate: this is the single place where an agent's
// request becomes an effect, and a future caller reaching the Service directly
// must not be able to slip past a guard that lived only in the wrapper.
func (s *Service) resolveProposal(caller Caller, id string, confirm bool, note string) (Proposal, error) {
	if caller.IsAgent() {
		return Proposal{}, &RejectionError{
			Reason:  ReasonForbidden,
			Message: "confirming or denying a proposal is reserved to human callers",
			Entity:  id,
		}
	}
	p, err := s.store.GetProposal(id)
	if err != nil {
		return Proposal{}, err
	}
	if p.State != ProposalPending {
		return Proposal{}, fmt.Errorf("%w: proposal %s is already %s", ErrWrongState, id, p.State)
	}

	meta := EventMeta{Kind: EventProposal, Actor: caller.Actor()}
	if !confirm {
		detail := "denied by a human"
		if note != "" {
			detail += ": " + note
		}
		return s.store.ResolveProposal(id, ProposalDenied, meta, detail)
	}

	// Mark the proposal resolved BEFORE running the operation. The transition is
	// optimistic (pending-only), so this is what makes confirmation single-use: a
	// second confirm — or a concurrent one — finds the row no longer pending and
	// loses, rather than executing the operation twice.
	confirmDetail := "confirmed by a human"
	if note != "" {
		confirmDetail += ": " + note
	}
	resolved, err := s.store.ResolveProposal(id, ProposalConfirmed, meta, confirmDetail)
	if err != nil {
		return Proposal{}, err
	}

	// Execute AS THE CONFIRMING HUMAN. The proposal records who asked; the effect
	// is attributed to who allowed it, which is the distinction the event log
	// needs to answer "why did this happen".
	if err := s.executeProposal(caller, resolved); err != nil {
		failDetail := "confirmed, but the operation failed: " + err.Error()
		if _, ferr := s.store.MarkProposalFailed(id, meta, failDetail); ferr != nil {
			// The row is already `confirmed` and cannot move again; the failure is
			// on the event log regardless, which is what matters for the record.
			return resolved, fmt.Errorf("%s (and the proposal could not be marked failed: %v)", failDetail, ferr)
		}
		// Wrapped in ErrWrongState so the daemon answers 409, not 500. This is a
		// CORRECTLY HANDLED case — the proposal is recorded `failed`, nothing was
		// mutated — and an operator confirming from the Web UI should not be shown
		// a server fault for it. A policy refusal nested inside still wins the
		// mapping (statusFor checks RejectionError first) and surfaces as 422,
		// which is also right.
		return resolved, fmt.Errorf("%w: proposal %s confirmed but the operation failed: %v", ErrWrongState, id, err)
	}
	return resolved, nil
}

// executeProposal performs the operation a confirmed proposal describes.
//
// Every branch calls the same Service method the human CLI calls, with the
// human's caller identity — there is no privileged back door, and a proposal for
// an operation that a human could not perform either (wrong state, over budget)
// fails exactly as it would have from the CLI.
func (s *Service) executeProposal(caller Caller, p Proposal) error {
	switch p.Operation {
	case OpDispatch:
		_, err := s.DispatchTask(p.TaskID)
		return err
	case OpCancel:
		_, err := s.cancelTask(caller, p.TaskID)
		return err
	case OpRetry:
		_, err := s.retryTask(caller, p.TaskID, RetryRequest{Rebase: p.Argument == "rebase=true"})
		return err
	case OpReplan:
		_, err := s.replanTask(caller, p.TaskID, ReplanRequest{Objective: p.Argument})
		return err
	case OpApprove:
		_, err := s.approveTask(caller, p.TaskID, proposalNote(p))
		return err
	case OpRejectAppr:
		_, err := s.rejectApproval(caller, p.TaskID, proposalNote(p))
		return err
	case OpIntegrate:
		// A confirmed proposal never advances anybody's branch: the operator who
		// confirms it is not necessarily sitting in that checkout, and a surprise
		// fast-forward is exactly the kind of side effect a proposal must not carry.
		_, err := s.IntegrateTask(p.TaskID, IntegrateRequest{})
		return err
	case OpAddDependency:
		_, err := s.AddDependency(p.TaskID, p.Argument)
		return err
	case OpSteer:
		jobID, instruction := decodeSteerArgument(p.Argument)
		_, err := s.steerJob(caller, jobID, instruction)
		return err
	case OpCancelSteer:
		_, err := s.cancelSteering(caller, p.Argument)
		return err
	case OpSyncTarget:
		_, err := s.syncTarget(caller, p.Argument)
		return err
	case OpVerify:
		// Never waived: a proposal carries no IgnoreResult, so an agent cannot get
		// one granted by way of a human's confirmation click.
		_, err := s.VerifyTask(p.TaskID, VerifyRequest{})
		return err
	case OpReview:
		_, err := s.ReviewTask(p.TaskID)
		return err
	default:
		// An operation nobody taught this switch about must not silently succeed:
		// the proposal would be marked confirmed with nothing having happened.
		return fmt.Errorf("control: proposal %s names an operation this plane cannot execute (%q)", p.ID, p.Operation)
	}
}

// A steering proposal has to carry TWO values — the Job and the instruction —
// through a row that has one Argument column. They are encoded as
// "<job-id> <instruction>" and split on the FIRST space: a Job id never contains
// one, and an instruction may contain anything at all, including newlines, so the
// split can never cut the instruction short.
//
// The Proposal's TaskID still holds the Task, not the Job, so the proposal appears
// on the Task's event log where an operator reads it.
func encodeSteerArgument(jobID, instruction string) string {
	return jobID + " " + instruction
}

func decodeSteerArgument(argument string) (jobID, instruction string) {
	jobID, instruction, _ = strings.Cut(argument, " ")
	return jobID, instruction
}

// proposalNote renders the agent's argument as the note recorded on the effect,
// attributed so the log never reads as though a human wrote it.
func proposalNote(p Proposal) string {
	if p.Argument == "" {
		return fmt.Sprintf("via proposal %s (proposed by %s)", p.ID, p.ProposedBy)
	}
	return fmt.Sprintf("%s — via proposal %s (proposed by %s)", p.Argument, p.ID, p.ProposedBy)
}
