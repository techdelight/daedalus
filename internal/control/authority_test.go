// Copyright (C) 2026 Techdelight BV

package control

import (
	"errors"
	"strings"
	"testing"
)

// The adversarial tests for tiered authority. The threat model is explicit: the
// Guild Master reads project-controlled documents, so assume the agent is
// actively trying to do the thing a poisoned README would tell it to.

// agentPlane builds a Service with a task driven to `verified` and returns both
// caller scopes over it, so the same operation can be tried as each.
func agentPlane(t *testing.T) (human, agent TaskAPI, svc *Service, store *Store, task Task) {
	t.Helper()
	repo := gitRepo(t)
	svc, _, store = newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil, StubVerifyRunner{Pass: true})
	created, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "do work"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(created.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if _, err := svc.VerifyTask(created.ID); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	task, _ = store.GetTask(created.ID)
	return svc.WithCaller(Human()), svc.WithCaller(Agent()), svc, store, task
}

// TestAgent_EveryRestrictedOpIsRefused is the headline adversarial test: an agent
// tries every consequential operation directly. None may execute; each must come
// back as a proposal, and the world must be unchanged.
func TestAgent_EveryRestrictedOpIsRefused(t *testing.T) {
	_, agent, _, store, task := agentPlane(t)
	before, _ := store.GetTask(task.ID)

	attempts := []struct {
		op   string
		call func() error
	}{
		{OpDispatch, func() error { _, err := agent.DispatchTask(task.ID); return err }},
		{OpCancel, func() error { _, err := agent.CancelTask(task.ID); return err }},
		{OpRetry, func() error { _, err := agent.RetryTask(task.ID, RetryRequest{}); return err }},
		{OpReplan, func() error { _, err := agent.ReplanTask(task.ID, ReplanRequest{Objective: "mine"}); return err }},
		{OpApprove, func() error { _, err := agent.ApproveTask(task.ID, "trust me"); return err }},
		{OpRejectAppr, func() error { _, err := agent.RejectApproval(task.ID, "no"); return err }},
		{OpIntegrate, func() error { _, err := agent.IntegrateTask(task.ID); return err }},
		{OpSyncTarget, func() error { _, err := agent.SyncTarget("app"); return err }},
	}
	for _, a := range attempts {
		t.Run(a.op, func(t *testing.T) {
			err := a.call()
			var rej *RejectionError
			if !errors.As(err, &rej) {
				t.Fatalf("%s as an agent = %v, want a refusal", a.op, err)
			}
			if rej.Reason != ReasonProposalRecorded {
				t.Errorf("%s reason = %q, want %q", a.op, rej.Reason, ReasonProposalRecorded)
			}
			if !strings.Contains(rej.Message, "proposal") {
				t.Errorf("%s message should say a proposal was recorded: %q", a.op, rej.Message)
			}
		})
	}

	// NOTHING happened. The task is exactly where it was.
	after, _ := store.GetTask(task.ID)
	if after.State != before.State {
		t.Errorf("task state changed under an agent's attempts: %s → %s", before.State, after.State)
	}
	// One proposal per attempt, all pending, all attributed to the agent.
	proposals, _ := store.ListProposals(ProposalPending)
	if len(proposals) != len(attempts) {
		t.Fatalf("proposals = %d, want %d", len(proposals), len(attempts))
	}
	for _, p := range proposals {
		if p.ProposedBy != CallerAgent {
			t.Errorf("proposal %s attributed to %q, want agent", p.ID, p.ProposedBy)
		}
	}
}

// TestAgent_CannotConfirmItsOwnProposal is the hole one level up: an agent that
// could confirm would have full authority through a two-step dance.
func TestAgent_CannotConfirmItsOwnProposal(t *testing.T) {
	human, agent, _, store, task := agentPlane(t)

	// The agent proposes.
	if _, err := agent.IntegrateTask(task.ID); err == nil {
		t.Fatal("precondition: the integrate attempt should have been refused")
	}
	proposals, _ := store.ListProposals(ProposalPending)
	if len(proposals) != 1 {
		t.Fatalf("proposals = %d, want 1", len(proposals))
	}
	id := proposals[0].ID

	// …and cannot confirm it.
	_, err := agent.ResolveProposal(id, true, "please")
	var rej *RejectionError
	if !errors.As(err, &rej) || rej.Reason != ReasonForbidden {
		t.Fatalf("agent confirming its own proposal = %v, want a forbidden refusal", err)
	}
	// Nor deny it — the decision is the human's either way.
	if _, err := agent.ResolveProposal(id, false, "actually no"); !errors.As(err, &rej) || rej.Reason != ReasonForbidden {
		t.Errorf("agent denying = %v, want a forbidden refusal", err)
	}
	// Nor propose the confirmation, which would be the same hole with an extra step.
	if _, err := store.GetProposal(id); err != nil {
		t.Fatalf("the proposal should still exist: %v", err)
	}
	still, _ := store.GetProposal(id)
	if still.State != ProposalPending {
		t.Errorf("proposal state = %q, want pending after the agent's attempts", still.State)
	}

	// A human confirms, and only then does anything happen.
	if _, err := human.ResolveProposal(id, true, "reviewed"); err != nil {
		t.Fatalf("human confirm: %v", err)
	}
	after, _ := store.GetTask(task.ID)
	if after.State != StateIntegrated {
		t.Errorf("task state = %q, want integrated after the human confirmed", after.State)
	}
}

// TestAgent_ProposalDeniedDoesNothing: denial must be inert, not a soft-execute.
func TestAgent_ProposalDeniedDoesNothing(t *testing.T) {
	human, agent, _, store, task := agentPlane(t)
	if _, err := agent.CancelTask(task.ID); err == nil {
		t.Fatal("precondition: cancel should have been refused")
	}
	proposals, _ := store.ListProposals(ProposalPending)
	before, _ := store.GetTask(task.ID)

	p, err := human.ResolveProposal(proposals[0].ID, false, "not warranted")
	if err != nil {
		t.Fatalf("deny: %v", err)
	}
	if p.State != ProposalDenied {
		t.Errorf("state = %q, want denied", p.State)
	}
	after, _ := store.GetTask(task.ID)
	if after.State != before.State {
		t.Errorf("a denied proposal changed the task: %s → %s", before.State, after.State)
	}
}

// TestProposal_ConfirmIsSingleUse: a confirmed proposal cannot be confirmed
// again, so an operation cannot be executed twice through one authorisation.
func TestProposal_ConfirmIsSingleUse(t *testing.T) {
	human, agent, _, store, task := agentPlane(t)
	if _, err := agent.RetryTask(task.ID, RetryRequest{}); err == nil {
		t.Fatal("precondition: retry should have been refused")
	}
	proposals, _ := store.ListProposals(ProposalPending)
	id := proposals[0].ID

	// The first confirm runs the op (which fails here — a verified task is not
	// retryable — and that failure is recorded rather than hidden).
	if _, err := human.ResolveProposal(id, true, ""); err == nil {
		t.Log("retry of a verified task succeeded unexpectedly; the single-use check below is what matters")
	}
	// The second confirm finds it no longer pending.
	if _, err := human.ResolveProposal(id, true, ""); err == nil {
		t.Error("a proposal was confirmed twice — one authorisation must permit one execution")
	}
}

// TestAgent_ReadsAndBoundedCreationAreAllowed: the tier is not "refuse
// everything". An agent that cannot see or create is not a supervisor.
func TestAgent_ReadsAndBoundedCreationAreAllowed(t *testing.T) {
	_, agent, svc, store, task := agentPlane(t)

	if _, err := agent.ListTasks(); err != nil {
		t.Errorf("list_tasks: %v", err)
	}
	if _, err := agent.TaskStatus(task.ID); err != nil {
		t.Errorf("get_task: %v", err)
	}
	if _, err := agent.TaskEvents(task.ID); err != nil {
		t.Errorf("task_events: %v", err)
	}
	if _, err := agent.PendingApprovals(); err != nil {
		t.Errorf("list_pending_approvals: %v", err)
	}

	// Creation on a DIFFERENT project (the first still holds the active slot).
	repo2 := gitRepo(t)
	svc.projects = mapResolver{"app": mustDir(t, svc, "app"), "other": repo2}
	created, err := agent.CreateTask(CreateTaskRequest{Project: "other", Objective: "agent-initiated"})
	if err != nil {
		t.Fatalf("create_task as an agent should be allowed: %v", err)
	}
	if created.ID == "" {
		t.Error("create_task returned no task")
	}
	// And it is bounded: the budget came from policy, not from the request.
	if created.Budget.MaxAttempts <= 0 {
		t.Errorf("agent-created task has no attempt bound: %+v", created.Budget)
	}
	// An over-budget ask is still refused, as it would be for a human.
	if _, err := agent.CreateTask(CreateTaskRequest{
		Project: "other", Objective: "greedy", Budget: &Budget{MaxAttempts: 9999},
	}); err == nil {
		t.Error("an agent widening its budget should be refused")
	}
	_ = store
}

// mustDir returns a project's directory through the service resolver.
func mustDir(t *testing.T, svc *Service, project string) string {
	t.Helper()
	dir, err := svc.projects.ProjectDir(project)
	if err != nil {
		t.Fatalf("ProjectDir(%s): %v", project, err)
	}
	return dir
}

// TestAgent_NeverSeesHostPaths pins the cross-tenant disclosure fix: an agent
// gets the opaque queue id and never host filesystem layout.
func TestAgent_NeverSeesHostPaths(t *testing.T) {
	human, agent, _, _, _ := agentPlane(t)

	humanView, err := human.ProjectTargets()
	if err != nil {
		t.Fatalf("human ProjectTargets: %v", err)
	}
	if len(humanView) == 0 || humanView[0].RepoPath == "" {
		t.Fatalf("a human should see the repository path: %+v", humanView)
	}
	agentView, err := agent.ProjectTargets()
	if err != nil {
		t.Fatalf("agent ProjectTargets: %v", err)
	}
	if len(agentView) != len(humanView) {
		t.Fatalf("agent saw %d queues, human saw %d", len(agentView), len(humanView))
	}
	for _, v := range agentView {
		if v.RepoPath != "" {
			t.Errorf("agent received a host path: %q", v.RepoPath)
		}
		if v.QueueID == "" {
			t.Error("agent received no queue id — it still needs to tell queues apart")
		}
	}
	// The id is stable and comparable, which is the legitimate need it serves.
	if agentView[0].QueueID != humanView[0].QueueID {
		t.Error("queue ids differ between callers — an agent could not correlate anything")
	}
}

// --- the authority table itself -------------------------------------------------

// TestAuthority_EveryMutatingOpIsTiered: a new mutating operation that nobody
// tiered must not silently default to allowed.
func TestAuthority_EveryMutatingOpIsTiered(t *testing.T) {
	for _, op := range mutatingOps {
		if _, listed := agentAuthority[op]; !listed {
			t.Errorf("mutating operation %q has no entry in the agent authority table", op)
		}
	}
	// An operation nobody has heard of fails closed.
	if TierFor(CallerAgent, "some_future_operation") != TierProposal {
		t.Error("an unknown operation must be TierProposal for an agent, never allowed")
	}
	// A human is unconstrained by this table.
	if TierFor(CallerHuman, "some_future_operation") != TierAllowed {
		t.Error("a human caller should not be gated by the agent authority table")
	}
}

// TestCallerClass_UnknownIsNotHuman: a caller class read back from storage that
// is not recognised must never be treated as the privileged one.
func TestCallerClass_UnknownIsNotHuman(t *testing.T) {
	if got := parseCallerClass("wizard"); got != CallerAgent {
		t.Errorf("parseCallerClass(unknown) = %q, want agent — unknown must mean untrusted", got)
	}
	if got := parseCallerClass(""); got != CallerAgent {
		t.Errorf("parseCallerClass(empty) = %q, want agent", got)
	}
	if got := parseCallerClass(string(CallerHuman)); got != CallerHuman {
		t.Errorf("parseCallerClass(human) = %q, want human", got)
	}
	// And the actor label follows the class, never the other way round.
	if Agent().Actor() != ActorAgent || Human().Actor() != ActorHuman {
		t.Error("actor labels must derive from the caller class")
	}
}
