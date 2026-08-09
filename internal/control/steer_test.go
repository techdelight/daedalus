// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Typed steering (Sprint 63, items 1 and 2).
//
// The tests are organised around the two things that must be true: steering
// cannot influence acceptance, and steering never claims a delivery that did not
// happen.

// --- test doubles ---------------------------------------------------------------

// steerableRunner is a StubRunner that also accepts steering, with a scripted
// answer. It is the host-testable stand-in for a runner that has a boundary.
type steerableRunner struct {
	StubRunner
	answer   error
	delay    time.Duration
	seen     []string
	seenJobs []string
}

func (r *steerableRunner) DeliverSteering(ctx context.Context, target SteerTarget, instruction string) error {
	r.seen = append(r.seen, instruction)
	r.seenJobs = append(r.seenJobs, target.JobID)
	if r.delay > 0 {
		select {
		case <-time.After(r.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return r.answer
}

// nilDelivererRunner is the typed-nil hazard in runner form: a nil *pointer*
// whose method set satisfies SteeringDeliverer, so the type assertion SUCCEEDS
// and the first call panics.
type nilDelivererRunner struct{ StubRunner }

func (r *nilDelivererRunner) DeliverSteering(context.Context, SteerTarget, string) error {
	return nil
}

func (r *nilDelivererRunner) Run(ctx context.Context, spec JobSpec) RunOutcome {
	return r.StubRunner.Run(ctx, spec)
}

// stageSteerableJob leaves a Task and Job in `working`, which is where an
// instruction could conceivably reach a worker.
func stageSteerableJob(t *testing.T, svc *Service, store *Store, project string) (Task, Job) {
	t.Helper()
	task, err := svc.CreateTask(CreateTaskRequest{Project: project, Objective: "original objective"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.TransitionTask(task.ID, StateQueued, false, ""); err != nil {
		t.Fatalf("→queued: %v", err)
	}
	if _, err := store.TransitionTask(task.ID, StateWorking, false, ""); err != nil {
		t.Fatalf("→working: %v", err)
	}
	job, err := store.CreateJob(task.ID, task.BaseSHA, "claude", 0, StateWorking)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	return task, job
}

// --- item 1: the invariant ------------------------------------------------------

// TestSteering_ChangesNothingThatDecidesDone is the load-bearing test of M17.
//
// If steering could influence acceptance it would re-open M14's and M15's entire
// argument through a new door: the acceptance policy is frozen at the plane-owned
// target precisely so nothing a worker (or an operator nudging a worker) does can
// change what "done" means. So this asserts that a steer moves NOTHING an oracle
// reads — and, deliberately, that it moves no state at all.
func TestSteering_ChangesNothingThatDecidesDone(t *testing.T) {
	repo := gitRepo(t)
	runner := &steerableRunner{}
	svc, _, store := newService(t, mapResolver{"app": repo}, runner, nil)

	task, job := stageSteerableJob(t, svc, store, "app")
	before, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}

	if _, err := svc.SteerJob(job.ID, "do it differently"); err != nil {
		t.Fatalf("SteerJob: %v", err)
	}

	after, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	// The acceptance oracle, the base it is frozen at, and the budget that bounds
	// the work: all untouched.
	if after.AcceptanceHash != before.AcceptanceHash {
		t.Errorf("steering changed the frozen acceptance hash: %q → %q", before.AcceptanceHash, after.AcceptanceHash)
	}
	if after.BaseSHA != before.BaseSHA {
		t.Errorf("steering changed base_sha: %q → %q", before.BaseSHA, after.BaseSHA)
	}
	if after.AcceptanceRef != before.AcceptanceRef {
		t.Errorf("steering changed the acceptance ref")
	}
	if after.Budget != before.Budget {
		t.Errorf("steering changed the budget: %v → %v", before.Budget, after.Budget)
	}
	// And the objective — the thing an operator might most expect a steer to edit.
	// It does not: a steer is an additional instruction, recorded separately, not a
	// rewrite of what the Task was created to do. Replan is the operation that
	// rewrites an objective, and it goes through `rejected` for a reason.
	if after.Objective != before.Objective {
		t.Errorf("steering rewrote the objective: %q → %q", before.Objective, after.Objective)
	}
	// No state moved, on either entity.
	if after.State != before.State {
		t.Errorf("steering moved the task: %s → %s", before.State, after.State)
	}
	if got, err := store.GetJob(job.ID); err != nil || got.State != job.State {
		t.Errorf("steering moved the job: %s → %s (err %v)", job.State, got.State, err)
	}
}

// TestSteering_AddsNoTransitions guards the structural half of the same claim.
//
// M16 added a state and three edges, and said so. M17 adds NEITHER — steering is
// not a state machine move — and the way to keep that true as the code changes is
// to assert it rather than to remember it.
func TestSteering_AddsNoTransitions(t *testing.T) {
	for from, tos := range legalTransitions {
		for to := range tos {
			if !validState(from) || !validState(to) {
				t.Errorf("transition %s → %s names an unknown state", from, to)
			}
		}
	}
	// Nothing steering-shaped may appear in the worker's table: the whole point is
	// that being told something new brings a worker no closer to `verified`.
	for from, tos := range workerReachable {
		for to := range tos {
			if to == StateVerified || to == StateApproved || to == StateIntegrated {
				t.Errorf("workerReachable contains %s → %s, which no worker may drive", from, to)
			}
		}
	}
}

// TestSteering_ProvenanceComesFromTheTransport asserts the issuer is the caller
// class, not anything a request could name.
func TestSteering_ProvenanceComesFromTheTransport(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo}, &steerableRunner{}, nil)
	_, job := stageSteerableJob(t, svc, store, "app")

	steer, err := svc.SteerJob(job.ID, "as a human")
	if err != nil {
		t.Fatalf("SteerJob: %v", err)
	}
	if steer.IssuedBy != CallerHuman {
		t.Errorf("issuedBy = %q, want human", steer.IssuedBy)
	}
	// A zero-valued caller is an agent, never a human — the same fail-closed rule
	// as TierFor and parseCallerClass.
	steer2, err := svc.steerJob(Caller{}, job.ID, "as nobody in particular")
	if err != nil {
		t.Fatalf("steerJob(zero caller): %v", err)
	}
	if steer2.IssuedBy != CallerAgent {
		t.Errorf("a zero-valued caller issued as %q, want agent", steer2.IssuedBy)
	}
}

// TestSteering_AgentMustPropose asserts the tiering: an agent may ask, never act.
func TestSteering_AgentMustPropose(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo}, &steerableRunner{}, nil)
	task, job := stageSteerableJob(t, svc, store, "app")

	agent := svc.WithCaller(Agent())
	_, err := agent.SteerJob(job.ID, "quietly change direction")
	reason, refused := Rejected(err)
	if !refused || reason != ReasonProposalRecorded {
		t.Fatalf("agent steer: err = %v (reason %q), want proposal_recorded", err, reason)
	}
	// Nothing was recorded against the Job: an agent's ask is a proposal row, not a
	// steering row.
	steers, err := svc.JobSteering(job.ID)
	if err != nil {
		t.Fatalf("JobSteering: %v", err)
	}
	if len(steers) != 0 {
		t.Fatalf("agent steer recorded %d steering rows, want 0", len(steers))
	}

	proposals, err := svc.ListProposals(ProposalPending)
	if err != nil {
		t.Fatalf("ListProposals: %v", err)
	}
	if len(proposals) != 1 || proposals[0].Operation != OpSteer {
		t.Fatalf("proposals = %+v, want one steer_job", proposals)
	}
	// The proposal is filed against the TASK, so it appears where an operator reads.
	if proposals[0].TaskID != task.ID {
		t.Errorf("proposal task = %q, want %q", proposals[0].TaskID, task.ID)
	}

	// Confirming it executes the steer as the human.
	if _, err := svc.ResolveProposal(proposals[0].ID, true, "go on then"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	steers, err = svc.JobSteering(job.ID)
	if err != nil {
		t.Fatalf("JobSteering after confirm: %v", err)
	}
	if len(steers) != 1 {
		t.Fatalf("after confirm: %d steering rows, want 1", len(steers))
	}
	if steers[0].Instruction != "quietly change direction" {
		t.Errorf("instruction = %q, want the proposed text intact", steers[0].Instruction)
	}
	if steers[0].IssuedBy != CallerHuman {
		t.Errorf("issuedBy = %q; a confirmed proposal executes AS THE HUMAN", steers[0].IssuedBy)
	}
}

// TestSteerArgument_SurvivesAnInstructionWithSpacesAndNewlines pins the encoding
// a steering proposal has to squeeze through one Argument column.
func TestSteerArgument_SurvivesAnInstructionWithSpacesAndNewlines(t *testing.T) {
	instruction := "stop editing README\nuse the existing helper instead"
	jobID, decoded := decodeSteerArgument(encodeSteerArgument("J-42", instruction))
	if jobID != "J-42" {
		t.Errorf("jobID = %q, want J-42", jobID)
	}
	if decoded != instruction {
		t.Errorf("instruction = %q, want it intact", decoded)
	}
}

// --- item 2: honest failure -----------------------------------------------------

// TestSteering_UndeliverableWhenTheRunnerHasNoBoundary is the honest-failure
// requirement itself.
//
// The DEFAULT runner in this project has no steering boundary — `daedalus <name>
// <dir> -p <objective>` is a single-shot headless invocation whose only boundary
// is process exit. So this is not an edge case; it is the production path.
func TestSteering_UndeliverableWhenTheRunnerHasNoBoundary(t *testing.T) {
	repo := gitRepo(t)
	// A plain StubRunner: an AgentRunner and nothing more, exactly like
	// CoordinatorRunner.
	svc, _, store := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)
	_, job := stageSteerableJob(t, svc, store, "app")

	steer, err := svc.SteerJob(job.ID, "try the other approach")
	if err != nil {
		t.Fatalf("SteerJob: %v", err)
	}
	if steer.State != SteerUndeliverable {
		t.Fatalf("state = %q, want undeliverable", steer.State)
	}
	if !strings.Contains(steer.Detail, "NOT delivered") {
		t.Errorf("detail = %q, want it to say plainly that nothing was delivered", steer.Detail)
	}
	if steer.DeliveredAt != "" {
		t.Errorf("deliveredAt = %q on an undelivered instruction", steer.DeliveredAt)
	}
	// The instruction is still RECORDED. An op that only wrote a row on success
	// would lose exactly the events an operator most needs.
	stored, err := store.GetSteering(steer.ID)
	if err != nil {
		t.Fatalf("GetSteering: %v", err)
	}
	if stored.Instruction != "try the other approach" {
		t.Errorf("instruction = %q, want it recorded verbatim", stored.Instruction)
	}
}

// TestSteering_DeliveryOutcomes covers the seam's whole contract.
func TestSteering_DeliveryOutcomes(t *testing.T) {
	cases := []struct {
		name    string
		runner  AgentRunner
		want    DeliveryState
		wantSaw bool
	}{
		{"delivered", &steerableRunner{answer: nil}, SteerDelivered, true},
		{"unsupported", &steerableRunner{answer: ErrSteeringUnsupported}, SteerUndeliverable, true},
		{"deferred stays pending", &steerableRunner{answer: ErrSteeringDeferred}, SteerPending, true},
		{"handoff failed", &steerableRunner{answer: errors.New("pipe closed")}, SteerUndeliverable, true},
		{"no deliverer at all", StubRunner{}, SteerUndeliverable, false},
		// The typed-nil hazard: the assertion SUCCEEDS and a naive implementation
		// panics on the first call. The guard must see through the interface.
		{"typed-nil deliverer", (*nilDelivererRunner)(nil), SteerUndeliverable, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := gitRepo(t)
			svc, _, store := newService(t, mapResolver{"app": repo}, tc.runner, nil)
			_, job := stageSteerableJob(t, svc, store, "app")

			steer, err := svc.SteerJob(job.ID, "instruction")
			if err != nil {
				t.Fatalf("SteerJob: %v", err)
			}
			if steer.State != tc.want {
				t.Errorf("state = %q, want %q (detail %q)", steer.State, tc.want, steer.Detail)
			}
			if (steer.State == SteerDelivered) != (steer.DeliveredAt != "") {
				t.Errorf("deliveredAt = %q for state %q — the timestamp and the claim must agree",
					steer.DeliveredAt, steer.State)
			}
			if r, ok := tc.runner.(*steerableRunner); ok && r != nil {
				if got := len(r.seen) > 0; got != tc.wantSaw {
					t.Errorf("runner saw the instruction = %v, want %v", got, tc.wantSaw)
				}
			}
		})
	}
}

// TestSteering_ARunnerThatNeverAnswersIsUndeliverable: a deliverer that neither
// answers nor defers has told us nothing, and "nothing" is recorded as
// undeliverable rather than left hanging as pending.
func TestSteering_ARunnerThatNeverAnswersIsUndeliverable(t *testing.T) {
	repo := gitRepo(t)
	// A deliberately short timeout: the behaviour under test is "the deadline
	// bounds the call", not "ten seconds elapse".
	runner := &steerableRunner{delay: 30 * time.Second}
	svc, _, store := newService(t, mapResolver{"app": repo}, runner, nil)
	svc.steerTimeout = 50 * time.Millisecond
	_, job := stageSteerableJob(t, svc, store, "app")

	done := make(chan SteeringEvent, 1)
	go func() {
		steer, err := svc.SteerJob(job.ID, "hello?")
		if err != nil {
			t.Errorf("SteerJob: %v", err)
		}
		done <- steer
	}()
	select {
	case steer := <-done:
		if steer.State != SteerUndeliverable {
			t.Errorf("state = %q, want undeliverable", steer.State)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("SteerJob never returned: the delivery timeout did not bound the runner call")
	}
}

// TestSteering_SupersedesTheUndeliveredPredecessor: two instructions, and only one
// can be what the worker is about to be told.
func TestSteering_SupersedesTheUndeliveredPredecessor(t *testing.T) {
	repo := gitRepo(t)
	runner := &steerableRunner{answer: ErrSteeringDeferred}
	svc, _, store := newService(t, mapResolver{"app": repo}, runner, nil)
	_, job := stageSteerableJob(t, svc, store, "app")

	first, err := svc.SteerJob(job.ID, "do A")
	if err != nil {
		t.Fatalf("first steer: %v", err)
	}
	if first.State != SteerPending {
		t.Fatalf("first state = %q, want pending", first.State)
	}
	second, err := svc.SteerJob(job.ID, "no, do B")
	if err != nil {
		t.Fatalf("second steer: %v", err)
	}

	got, err := store.GetSteering(first.ID)
	if err != nil {
		t.Fatalf("GetSteering: %v", err)
	}
	if got.State != SteerSuperseded {
		t.Errorf("first instruction is %q, want superseded", got.State)
	}
	if second.State != SteerPending {
		t.Errorf("second instruction is %q, want pending", second.State)
	}
	// Exactly one instruction is outstanding — "what is this worker being told"
	// must not have two answers.
	steers, err := svc.JobSteering(job.ID)
	if err != nil {
		t.Fatalf("JobSteering: %v", err)
	}
	outstanding := 0
	for _, s := range steers {
		if s.State == SteerPending {
			outstanding++
		}
	}
	if outstanding != 1 {
		t.Errorf("%d outstanding instructions, want exactly 1", outstanding)
	}
}

// TestSteering_CancelOnlyWithdrawsWhatWasNotDelivered.
func TestSteering_CancelOnlyWithdrawsWhatWasNotDelivered(t *testing.T) {
	repo := gitRepo(t)
	runner := &steerableRunner{answer: ErrSteeringDeferred}
	svc, _, store := newService(t, mapResolver{"app": repo}, runner, nil)
	_, job := stageSteerableJob(t, svc, store, "app")

	pending, err := svc.SteerJob(job.ID, "reconsider")
	if err != nil {
		t.Fatalf("SteerJob: %v", err)
	}
	withdrawn, err := svc.CancelSteering(pending.ID)
	if err != nil {
		t.Fatalf("CancelSteering: %v", err)
	}
	if withdrawn.State != SteerCancelled {
		t.Errorf("state = %q, want cancelled", withdrawn.State)
	}

	// A delivered instruction cannot be un-said. Recording `cancelled` over
	// `delivered` would make the log describe a world in which the worker was never
	// told.
	runner.answer = nil
	delivered, err := svc.SteerJob(job.ID, "actually, carry on")
	if err != nil {
		t.Fatalf("second SteerJob: %v", err)
	}
	if delivered.State != SteerDelivered {
		t.Fatalf("state = %q, want delivered", delivered.State)
	}
	if _, err := svc.CancelSteering(delivered.ID); !errors.Is(err, ErrWrongState) {
		t.Errorf("cancelling a delivered instruction: err = %v, want ErrWrongState", err)
	}
	if got, _ := store.GetSteering(delivered.ID); got.State != SteerDelivered {
		t.Errorf("a refused cancel changed the state to %q", got.State)
	}
}

// TestSteering_ConfirmDeliveryIsSingleUse: a late runner callback cannot
// resurrect an instruction that was already settled.
func TestSteering_ConfirmDeliveryIsSingleUse(t *testing.T) {
	repo := gitRepo(t)
	runner := &steerableRunner{answer: ErrSteeringDeferred}
	svc, _, store := newService(t, mapResolver{"app": repo}, runner, nil)
	_, job := stageSteerableJob(t, svc, store, "app")

	steer, err := svc.SteerJob(job.ID, "at your next boundary, please")
	if err != nil {
		t.Fatalf("SteerJob: %v", err)
	}
	confirmed, err := svc.ConfirmSteeringDelivery(steer.ID, "handed over at the Stop hook")
	if err != nil {
		t.Fatalf("ConfirmSteeringDelivery: %v", err)
	}
	if confirmed.State != SteerDelivered || confirmed.DeliveredAt == "" {
		t.Fatalf("state = %q deliveredAt = %q, want delivered with a timestamp", confirmed.State, confirmed.DeliveredAt)
	}
	if _, err := svc.ConfirmSteeringDelivery(steer.ID, "again"); !errors.Is(err, ErrConflict) {
		t.Errorf("second confirm: err = %v, want ErrConflict", err)
	}
	// And a cancelled instruction cannot be delivered by a runner that lost the race.
	second, err := svc.SteerJob(job.ID, "and this one")
	if err != nil {
		t.Fatalf("second SteerJob: %v", err)
	}
	if _, err := svc.CancelSteering(second.ID); err != nil {
		t.Fatalf("CancelSteering: %v", err)
	}
	if _, err := svc.ConfirmSteeringDelivery(second.ID, "too late"); !errors.Is(err, ErrConflict) {
		t.Errorf("confirming a cancelled instruction: err = %v, want ErrConflict", err)
	}
	if got, _ := store.GetSteering(second.ID); got.State != SteerCancelled {
		t.Errorf("a late confirmation changed the state to %q", got.State)
	}
}

// TestSteering_RefusedWhenNothingCouldHearIt: a steer aimed at a Job that is not
// running is refused, not recorded-and-dropped.
func TestSteering_RefusedWhenNothingCouldHearIt(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo}, &steerableRunner{}, nil)
	_, job := stageSteerableJob(t, svc, store, "app")
	if _, err := store.TransitionJob(job.ID, StateCandidate, false, ""); err != nil {
		t.Fatalf("→candidate: %v", err)
	}

	_, err := svc.SteerJob(job.ID, "too late")
	reason, refused := Rejected(err)
	if !refused || reason != ReasonNotSteerable {
		t.Fatalf("err = %v (reason %q), want not_steerable", err, reason)
	}
	steers, err := svc.JobSteering(job.ID)
	if err != nil {
		t.Fatalf("JobSteering: %v", err)
	}
	if len(steers) != 0 {
		t.Errorf("%d steering rows recorded for a refused steer, want 0", len(steers))
	}
}

// TestSteering_EmptyInstructionIsMalformedInput.
func TestSteering_EmptyInstructionIsMalformedInput(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo}, &steerableRunner{}, nil)
	_, job := stageSteerableJob(t, svc, store, "app")

	for _, instruction := range []string{"", "   ", "\n\t"} {
		if _, err := svc.SteerJob(job.ID, instruction); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("SteerJob(%q): err = %v, want ErrInvalidRequest", instruction, err)
		}
	}
}

// TestSteering_IsOnTheTaskEventLog: the record has to be findable where an
// operator actually reads.
func TestSteering_IsOnTheTaskEventLog(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)
	task, job := stageSteerableJob(t, svc, store, "app")

	if _, err := svc.SteerJob(job.ID, "a visible instruction"); err != nil {
		t.Fatalf("SteerJob: %v", err)
	}
	events, err := svc.TaskEvents(task.ID)
	if err != nil {
		t.Fatalf("TaskEvents: %v", err)
	}
	var steering int
	for _, e := range events {
		if e.Kind == EventSteering {
			steering++
		}
	}
	if steering == 0 {
		t.Fatal("no steering event on the task's log; the record would be unfindable")
	}
}

// TestSteering_SurvivesAnExistingDatabase asserts the migration is additive and
// idempotent — an existing control.db opens, migrates, and keeps its rows.
func TestSteering_SurvivesAnExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/control.db"
	store, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := store.CreateTask(NewTask{Project: "app", Objective: "old work", BaseSHA: "abc"}, StatePlanned); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen (migration not idempotent?): %v", err)
	}
	defer reopened.Close()
	tasks, err := reopened.ListTasks()
	if err != nil || len(tasks) != 1 {
		t.Fatalf("after migration: %d tasks, err %v — want the existing row kept", len(tasks), err)
	}
	if _, err := reopened.ListSteeringForTask(tasks[0].ID); err != nil {
		t.Fatalf("steering table missing after migration: %v", err)
	}
}
