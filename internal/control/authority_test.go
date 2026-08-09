// Copyright (C) 2026 Techdelight BV

package control

import (
	"errors"
	"strings"
	"sync"
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

// TestProposal_ConfirmIsSingleUse: one authorisation permits exactly one
// execution.
//
// The operation must be one that SUCCEEDS, and whose double-execution leaves a
// visible trace. An earlier version of this test confirmed a `retry` on a
// verified task: the first confirm failed because the op was inapplicable, so
// the second returned an error for that reason too — and the test passed with
// BOTH enforcement layers removed. It asserted nothing.
//
// `dispatch` on a planned task succeeds and creates a Job, so executing twice
// through one authorisation would leave two Jobs. That is the observable the
// assertion needs.
func TestProposal_ConfirmIsSingleUse(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil, StubVerifyRunner{Pass: true})
	human, agent := svc.WithCaller(Human()), svc.WithCaller(Agent())

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// The agent asks for a dispatch; it becomes a proposal.
	if _, err := agent.DispatchTask(task.ID); err == nil {
		t.Fatal("precondition: dispatch should have been refused for an agent")
	}
	proposals, _ := store.ListProposals(ProposalPending)
	if len(proposals) != 1 {
		t.Fatalf("proposals = %d, want 1", len(proposals))
	}
	id := proposals[0].ID
	if jobs, _ := store.ListJobsForTask(task.ID); len(jobs) != 0 {
		t.Fatalf("precondition: a proposal must not have executed anything, got %d jobs", len(jobs))
	}

	// First confirm: the operation RUNS and succeeds.
	if _, err := human.ResolveProposal(id, true, "go ahead"); err != nil {
		t.Fatalf("first confirm: %v", err)
	}
	jobs, _ := store.ListJobsForTask(task.ID)
	if len(jobs) != 1 {
		t.Fatalf("after one confirm: %d jobs, want 1", len(jobs))
	}
	firstJobID := jobs[0].ID

	// Second confirm: refused BECAUSE THE PROPOSAL IS SPENT.
	//
	// Asserting only `err != nil` here would prove nothing — with the guards
	// removed, the second confirm still errors, because the state machine happens
	// to refuse a second dispatch of an already-dispatched task. That incidental
	// error is what made the first version of this test vacuous. So the load-
	// bearing assertions are the error's IDENTITY and the proposal's final STATE:
	// both change if either enforcement layer is removed.
	_, err = human.ResolveProposal(id, true, "again")
	if err == nil {
		t.Fatal("a proposal was confirmed twice — one authorisation must permit one execution")
	}
	if !errors.Is(err, ErrWrongState) {
		t.Errorf("second confirm err = %v, want a state conflict (the proposal is spent), not an incidental failure", err)
	}
	if !strings.Contains(err.Error(), "already") {
		t.Errorf("second confirm should say the proposal is already resolved, got %v", err)
	}
	// The record still says confirmed: a spent proposal is not re-resolvable, and
	// in particular the second attempt must not overwrite it with failed/denied.
	final, _ := store.GetProposal(id)
	if final.State != ProposalConfirmed {
		t.Errorf("proposal state = %q, want confirmed — a spent proposal must not be re-resolved", final.State)
	}
	// Denying an already-confirmed proposal is refused for the same reason.
	if _, err := human.ResolveProposal(id, false, "changed my mind"); !errors.Is(err, ErrWrongState) {
		t.Errorf("denying a confirmed proposal = %v, want a state conflict", err)
	}
	if final, _ := store.GetProposal(id); final.State != ProposalConfirmed {
		t.Errorf("proposal state = %q after an attempted deny, want confirmed", final.State)
	}
	// Belt, not the mechanism: the job chain is unchanged. (The state machine
	// would also refuse a second dispatch, so this alone cannot detect a broken
	// guard — it is here to catch an operation that IS applicable twice.)
	jobs, _ = store.ListJobsForTask(task.ID)
	if len(jobs) != 1 || jobs[0].ID != firstJobID {
		t.Errorf("job chain changed after the refused confirm: %d jobs (first was %s)", len(jobs), firstJobID)
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

// TestTierFor_FailsClosedOnAnUnknownClass is the regression for the audit's F1.
// TierFor was written `if class != CallerAgent { return TierAllowed }`, which
// reads identically to the correct rule and is catastrophically different: it
// hands FULL HUMAN AUTHORITY to a zero-valued Caller, silently. `Caller` is
// exported with an exported field, so a zero value is one refactor, embedder or
// new listener away at any time.
//
// The rule is the same one parseCallerClass already states: human is the
// privileged answer, so it must be proven, never assumed.
func TestTierFor_FailsClosedOnAnUnknownClass(t *testing.T) {
	// Every class that is not explicitly human must be tiered as an agent.
	notHuman := []CallerClass{
		"",                     // the zero value
		"AGENT",                // wrong case
		"hooman",               // typo
		"Human",                // wrong case for the privileged value itself
		CallerAgent,            // the real thing
		CallerClass("unknown"), // anything at all
	}
	for _, class := range notHuman {
		t.Run(string("class="+class), func(t *testing.T) {
			for _, op := range []string{OpIntegrate, OpApprove, OpCancel, OpSyncTarget, OpDispatch} {
				if got := TierFor(class, op); got != TierProposal {
					t.Errorf("TierFor(%q, %s) = %v, want TierProposal — only an explicitly human class may execute", class, op, got)
				}
			}
			// Reads stay free for any class; refusing those would make the Guild
			// Master useless without making anything safer.
			if got := TierFor(class, OpListTasks); got != TierAllowed {
				t.Errorf("TierFor(%q, list_tasks) = %v, want TierAllowed", class, got)
			}
		})
	}
	// And the one privileged value still works.
	if got := TierFor(CallerHuman, OpIntegrate); got != TierAllowed {
		t.Errorf("TierFor(human, integrate) = %v, want TierAllowed", got)
	}
}

// TestCaller_ZeroValueIsConsistentlyUntrusted: an earlier version answered three
// questions about `Caller{}` independently and gave three different answers
// (not-an-agent, actor "system", string "human"). That inconsistency is the shape
// of a privilege bug — every derived answer must agree, and agree on untrusted.
func TestCaller_ZeroValueIsConsistentlyUntrusted(t *testing.T) {
	var zero Caller
	if !zero.IsAgent() {
		t.Error("Caller{}.IsAgent() = false — a zero value must be treated as untrusted")
	}
	if got := zero.Actor(); got != ActorAgent {
		t.Errorf("Caller{}.Actor() = %q, want %q", got, ActorAgent)
	}
	if got := zero.String(); got != string(CallerAgent) {
		t.Errorf("Caller{}.String() = %q, want %q — a refusal message must not claim a privilege the caller lacks", got, CallerAgent)
	}
	// The explicitly-human value is consistent the other way.
	h := Human()
	if h.IsAgent() || h.Actor() != ActorHuman || h.String() != string(CallerHuman) {
		t.Errorf("Human() is inconsistent: isAgent=%v actor=%q string=%q", h.IsAgent(), h.Actor(), h.String())
	}
}

// TestScope_ZeroValueCallerIsRefused proves it end-to-end rather than only at the
// table: a Service scoped to a zero-valued Caller must not be able to integrate.
func TestScope_ZeroValueCallerIsRefused(t *testing.T) {
	_, _, svc, store, task := agentPlane(t)
	zero := svc.WithCaller(Caller{})

	_, err := zero.IntegrateTask(task.ID)
	var rej *RejectionError
	if !errors.As(err, &rej) || rej.Reason != ReasonProposalRecorded {
		t.Fatalf("zero-valued caller integrate = %v, want a proposal refusal", err)
	}
	if _, err := zero.ResolveProposal("P-1", true, ""); !errors.As(err, &rej) || rej.Reason != ReasonForbidden {
		t.Errorf("zero-valued caller confirming = %v, want a forbidden refusal", err)
	}
	after, _ := store.GetTask(task.ID)
	if after.State == StateIntegrated {
		t.Error("a zero-valued caller landed a change")
	}
	// The proposal it did create is attributed to `agent`, not to an empty class.
	proposals, _ := store.ListProposals(ProposalPending)
	if len(proposals) != 1 || proposals[0].ProposedBy != CallerAgent {
		t.Errorf("proposal attribution = %+v, want proposedBy=agent", proposals)
	}
}

// TestProposal_ConcurrentConfirmsExecuteOnce pins the store's optimistic
// pending-only UPDATE, which a serial test cannot distinguish from the service
// guard in front of it: remove the CAS and the sequential single-use test still
// passes, because the guard catches it first. Only concurrency separates them.
//
// N humans hit confirm at the same moment — a real scenario with a CLI and a Web
// UI open — and exactly one authorisation may be spent.
func TestProposal_ConcurrentConfirmsExecuteOnce(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil, StubVerifyRunner{Pass: true})
	human, agent := svc.WithCaller(Human()), svc.WithCaller(Agent())

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := agent.DispatchTask(task.ID); err == nil {
		t.Fatal("precondition: dispatch should have been refused for an agent")
	}
	proposals, _ := store.ListProposals(ProposalPending)
	id := proposals[0].ID

	const racers = 8
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		wins   int
		losses int
	)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := human.ResolveProposal(id, true, "confirm")
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				wins++
			} else {
				losses++
			}
		}()
	}
	close(start)
	wg.Wait()

	if wins != 1 {
		t.Errorf("%d concurrent confirms succeeded, want exactly 1 — one authorisation, one execution", wins)
	}
	if losses != racers-1 {
		t.Errorf("losses = %d, want %d", losses, racers-1)
	}
	// And the operation ran exactly once.
	jobs, _ := store.ListJobsForTask(task.ID)
	if len(jobs) != 1 {
		t.Errorf("jobs = %d, want 1 — the operation executed more than once", len(jobs))
	}
	final, _ := store.GetProposal(id)
	if final.State != ProposalConfirmed {
		t.Errorf("proposal state = %q, want confirmed", final.State)
	}
}
