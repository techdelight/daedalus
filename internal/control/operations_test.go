// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The tests that make operations.go load-bearing (#95, docs/no-dead-ends.md §1).
//
// The first is the one that matters: for every operation and every state, the
// GUARD's answer and the TABLE's answer agree. That is a second, independent
// statement of the same rule — TestCanTransition_Exhaustive's shape, which is the
// one kind of test in this repository that has repeatedly caught real mistakes.
// Without it the table is documentation, and documentation drifts.

// callOperation invokes each operation against a real Service. This dispatcher is
// deliberately hand-written: it is the INDEPENDENT half of the cross-check, and
// generating it from the same table it is checking would prove nothing.
//
// Each call is made with whatever arguments get it past request validation and to
// the state guard. What happens after the guard is not this test's business — an
// operation may well fail for want of an artifact, a verifier or a budget, and
// that is a different refusal with a different name.
var callOperation = map[Operation]func(svc *Service, taskID, jobID string) error{
	OpDispatch: func(svc *Service, id, _ string) error { _, err := svc.DispatchTask(id); return err },
	OpVerify:   func(svc *Service, id, _ string) error { _, err := svc.VerifyTask(id, VerifyRequest{}); return err },
	OpRetry: func(svc *Service, id, _ string) error {
		_, err := svc.RetryTask(id, RetryRequest{})
		return err
	},
	OpReverify: func(svc *Service, id, _ string) error {
		_, err := svc.ReverifyTask(id, ReverifyRequest{})
		return err
	},
	OpReplan: func(svc *Service, id, _ string) error {
		_, err := svc.ReplanTask(id, ReplanRequest{Objective: "a different objective"})
		return err
	},
	OpRefine: func(svc *Service, id, _ string) error {
		_, err := svc.RefineTask(id, RefineRequest{Note: "put this right"})
		return err
	},
	OpReview: func(svc *Service, id, _ string) error { _, err := svc.ReviewTask(id); return err },
	OpChecks: func(svc *Service, id, _ string) error {
		_, err := svc.AmendTaskChecks(id, AmendChecksRequest{Checks: []string{"true"}})
		return err
	},
	OpBudget: func(svc *Service, id, _ string) error {
		_, err := svc.AmendTaskBudget(id, AmendBudgetRequest{MaxAttempts: 9})
		return err
	},
	OpApprove: func(svc *Service, id, _ string) error { _, err := svc.ApproveTask(id, ""); return err },
	OpRejectAppr: func(svc *Service, id, _ string) error {
		_, err := svc.RejectApproval(id, "")
		return err
	},
	OpIntegrate: func(svc *Service, id, _ string) error {
		_, err := svc.IntegrateTask(id, IntegrateRequest{})
		return err
	},
	// A REAL upstream task, not a made-up id. `AddDependency` checks that the
	// dependency exists BEFORE it checks the dependent's state, so passing a
	// nonexistent id made the call fail with "not found" and hid the state rule
	// entirely — the exhaustive check then reported a missing guard that has been
	// there all along, inside the insert's transaction. A fixture without the
	// property that makes the rule reachable proves nothing about the rule.
	OpAddDependency: func(svc *Service, id, _ string) error {
		up, err := svc.CreateTask(CreateTaskRequest{Project: "upstream", Objective: "the thing waited for"})
		if err != nil {
			return err
		}
		_, err = svc.AddDependency(id, up.ID)
		return err
	},
	OpCancel: func(svc *Service, id, _ string) error { _, err := svc.CancelTask(id); return err },
	OpSteer: func(svc *Service, _, jobID string) error {
		_, err := svc.SteerJob(jobID, "go left instead")
		return err
	},
}

// TestEveryOperationIsExercised fails when an operation is added to the table and
// nobody wires it into the cross-check below — which would let a new operation's
// admit-set be pure fiction while every test stayed green.
func TestEveryOperationIsExercised(t *testing.T) {
	for _, op := range AllOperations() {
		if _, ok := callOperation[op]; !ok {
			t.Errorf("operation %q is in the table but callOperation has no entry, so "+
				"TestOperationTable_MatchesGuards_Exhaustive proves nothing about it", op)
		}
		if op.Surface() == "" {
			t.Errorf("operation %q has no surface name, so no refusal can name it", op)
		}
		if op.Summary() == "" {
			t.Errorf("operation %q has no summary, so a remedy list would print it bare", op)
		}
	}
}

// pathTo returns a sequence of states leading from `planned` to target using only
// edges legalTransitions allows, or nil when the state is unreachable. Derived
// rather than enumerated: a hand-written route to each state is the fifth copy of
// the transition table, and it would go stale the moment an edge moved.
func pathTo(target State) []State {
	if target == StatePlanned {
		return []State{}
	}
	type node struct {
		state State
		path  []State
	}
	seen := map[State]bool{StatePlanned: true}
	queue := []node{{state: StatePlanned}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range AllStates() {
			if seen[next] || !CanTransition(cur.state, next) {
				continue
			}
			path := append(append([]State{}, cur.path...), next)
			if next == target {
				return path
			}
			seen[next] = true
			queue = append(queue, node{state: next, path: path})
		}
	}
	return nil
}

// TestOperationTable_MatchesGuards_Exhaustive walks every (operation, state) pair
// and asserts the guard refuses on state grounds exactly when the table says the
// state is not admitted.
//
// The assertion is deliberately narrow. "Admitted" does not mean the call
// succeeds — an operation may fail afterwards for want of an artifact, a running
// container or a budget, and those are different refusals with different names.
// It means the call is not refused BECAUSE OF THE STATE, which is precisely the
// claim the table makes and the only claim it should be held to.
func TestOperationTable_MatchesGuards_Exhaustive(t *testing.T) {
	repo := gitRepo(t)

	for _, op := range AllOperations() {
		for _, state := range AllStates() {
			op, state := op, state
			t.Run(string(op)+"/"+string(state), func(t *testing.T) {
				path := pathTo(state)
				if path == nil {
					t.Skipf("state %s is not reachable through legalTransitions", state)
				}
				svc, _, store := newService(t, mapResolver{"app": repo, "upstream": repo},
					StubRunner{Result: ExecSuccess, WriteFile: true}, nil)
				task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "work"})
				if err != nil {
					t.Fatalf("CreateTask: %v", err)
				}
				for _, step := range path {
					if _, err := store.TransitionTaskWith(task.ID, step, false,
						EventMeta{Kind: EventGovernance}, "walked here by the exhaustive test"); err != nil {
						t.Fatalf("walking to %s via %s: %v", state, step, err)
					}
				}
				// The steer operation is keyed on a JOB's state, so it gets a Job
				// walked to the same state rather than a Task.
				jobID := ""
				if op.Target() == TargetJob {
					job, err := store.CreateJob(task.ID, task.BaseSHA, "stub", 0, StatePlanned)
					if err != nil {
						t.Fatalf("CreateJob: %v", err)
					}
					jobID = job.ID
					for _, step := range path {
						if _, err := store.TransitionJobWith(jobID, step, false,
							EventMeta{Kind: EventGovernance}, "walked here by the exhaustive test"); err != nil {
							t.Fatalf("walking job to %s via %s: %v", state, step, err)
						}
					}
				}

				err = callOperation[op](svc, task.ID, jobID)
				refusedOnState := isStateRefusal(err, op)
				admitted := op.Admits(state)

				switch {
				case admitted && refusedOnState:
					t.Errorf("the table says %s admits %q, but the guard refused it on state grounds: %v",
						state, op, err)
				case !admitted && !refusedOnState:
					t.Errorf("the table says %s does NOT admit %q, but the guard did not refuse it "+
						"on state grounds (err = %v). One of the two is wrong, and a surface reading "+
						"the table would now offer an operation the plane rejects", state, op, err)
				}
			})
		}
	}
}

// isStateRefusal reports whether err is this operation's own state guard saying
// no — as opposed to any other failure the call may hit afterwards.
func isStateRefusal(err error, op Operation) bool {
	if err == nil {
		return false
	}
	var se *StateError
	if errors.As(err, &se) {
		return se.Op == op
	}
	// The steer guard is a typed policy refusal rather than a StateError, because
	// it logs a decision and answers 422 — see steerJob.
	var rej *RejectionError
	if errors.As(err, &rej) {
		return rej.Reason == ReasonNotSteerable
	}
	// `depends` is the one operation whose state rule lives in the STORE, inside
	// the transaction that inserts the edge, so that the dependent's state cannot
	// change between the check and the row. It answers ErrDependencyInvalid, and
	// the table is still held to it: what is being cross-checked is whether the
	// operation is refused because of the state, not which layer says so.
	if op == OpAddDependency && errors.Is(err, ErrDependencyInvalid) {
		return true
	}
	return false
}

// TestEveryRemedyIsLegalFromTheStateItWasOfferedIn is the test that would have
// caught the original defect: the guard added for the first dead end told the
// operator to run `daedalus task retry`, and retry refuses a `candidate`.
//
// Every operation a refusal names must be legal from the state the refusal was
// issued in. Nothing else is a remedy; it is a second refusal with a friendlier
// tone.
func TestEveryRemedyIsLegalFromTheStateItWasOfferedIn(t *testing.T) {
	for _, state := range AllStates() {
		for _, target := range []OperationTarget{TargetTask, TargetJob} {
			for _, remedy := range RemediesFrom(target, state) {
				if !remedy.Admits(state) {
					t.Errorf("a refusal in state %s would offer %q, which %s does not admit",
						state, remedy, state)
				}
				if remedy.Target() != target {
					t.Errorf("RemediesFrom(%s, %s) offered %q, whose target is %s",
						target, state, remedy, remedy.Target())
				}
			}
		}
	}
}

// TestNoStateIsADeadEnd is the measurement from docs/no-dead-ends.md, asserted
// rather than waited for: no operator should reach a non-terminal state whose only
// exit is `cancel`.
//
// Cancel is excluded from the count on purpose. It is always available and it
// always "works", which is exactly what made the five dead ends of 2026-08-25 look
// like states with a way out. A state where destroying the task is the only move
// is the thing being forbidden, not the thing being counted.
func TestNoStateIsADeadEnd(t *testing.T) {
	for _, state := range AllStates() {
		if IsTerminal(state) {
			continue
		}
		ways := withoutOps(RemediesFrom(TargetTask, state), OpCancel)
		if len(ways) == 0 {
			t.Errorf("task state %s has no way forward but `cancel` — that is a bug in the "+
				"state machine, not a message to be worded better", state)
		}
	}
}

// TestRefusalsNameAWayForward asserts the sentence an operator actually reads.
// A StateError that trails off without naming anything is how five dead ends went
// unnoticed for an evening.
func TestRefusalsNameAWayForward(t *testing.T) {
	for _, state := range AllStates() {
		if IsTerminal(state) {
			continue // a finished task legitimately has nowhere to go
		}
		for _, op := range AllOperations() {
			if op.Target() != TargetTask || op.Admits(state) {
				continue
			}
			err := requireOperable(op, "T-1", state)
			if err == nil {
				t.Fatalf("requireOperable(%s, %s) returned nil for a state it does not admit", op, state)
			}
			msg := err.Error()
			if !strings.Contains(msg, "From here you can:") {
				t.Errorf("refusing %q in state %s says nothing about what to do instead: %s", op, state, msg)
			}
			if !errors.Is(err, ErrWrongState) {
				t.Errorf("refusing %q in state %s no longer satisfies errors.Is(ErrWrongState), so the "+
					"daemon would answer 500 rather than 409", op, state)
			}
		}
	}
}

// TestEveryOperationIsARealSubcommand derives the CLI's task subcommands from
// task.go's dispatch switch and checks every surface name against it.
//
// DERIVED, NOT ENUMERATED — this repository's recurring defect is a hand-written
// list that stops matching the thing it describes, and a remedy naming a command
// that does not exist is exactly that defect with an operator on the other end of
// it.
func TestEveryOperationIsARealSubcommand(t *testing.T) {
	src, err := os.ReadFile("../../cmd/daedalus/task.go")
	if err != nil {
		t.Skipf("cannot read the CLI's dispatch switch: %v", err)
	}
	// `case "retry":` and `case "reverify", "regrade":` — every quoted string in a
	// case clause of the subcommand switch.
	cases := regexp.MustCompile(`(?m)^\tcase ("[a-z-]+"(?:, "[a-z-]+")*):`).FindAllStringSubmatch(string(src), -1)
	if len(cases) == 0 {
		t.Fatal("found no dispatch switch in cmd/daedalus/task.go; this test cannot find what to check")
	}
	known := map[string]bool{}
	for _, c := range cases {
		for _, name := range strings.Split(c[1], ", ") {
			known[strings.Trim(name, `"`)] = true
		}
	}
	for _, op := range AllOperations() {
		if !known[op.Surface()] {
			t.Errorf("operation %q renders as `daedalus task %s`, which is not a subcommand of "+
				"`daedalus task` — a refusal would name a command the operator cannot run",
				op, op.Surface())
		}
	}
}

// TestChecksAndBudgetAreNeverTiered pins what authority.go argues for in prose:
// amending a Task's checks and raising its budget are refused to an agent
// OUTRIGHT, never offered as proposals.
//
// Naming those two operations in the state table (operations.go) made them
// typeable for the first time, and a tier entry is now one line away. This test is
// what stops that line being added by accident: a proposal for either would
// launder the exact thing both rules exist to forbid — a bound the graded party
// can ask to move, and a command the graded party wrote running inside the
// verifier.
func TestChecksAndBudgetAreNeverTiered(t *testing.T) {
	for _, op := range []string{OpChecks, OpBudget} {
		if tier, listed := agentAuthority[op]; listed {
			t.Errorf("%q has an authority entry (%v). It must have none: an agent may not "+
				"perform it and may not propose it either", op, tier)
		}
		for _, m := range mutatingOps {
			if m == op {
				t.Errorf("%q is listed in mutatingOps, which requires it to be tiered — "+
					"and it must never be", op)
			}
		}
		// Fail-closed is the backstop, not the rule. Assert it anyway: if someone
		// ever routes these through the generic dispatch, an agent must land on
		// TierProposal rather than TierAllowed.
		if TierFor(CallerAgent, op) != TierProposal {
			t.Errorf("TierFor(agent, %q) is not TierProposal — the fail-closed default has moved", op)
		}
	}
}

// TestExhaustedAttemptsOffersTheBudgetAmendment is the regression for the message
// that sent operators to cancel-and-recreate for a whole evening.
//
// It read "cancel it, or raise maxAttempts in the host-side budget policy and
// create a new task" — advice that destroyed the Task's history, and that was
// still being printed after `task budget` shipped precisely because it was prose.
func TestExhaustedAttemptsOffersTheBudgetAmendment(t *testing.T) {
	repo := gitRepo(t)
	// The attempt FAILS, which is the situation an operator is actually in when
	// they meet this refusal: one attempt, spent, the task back on the retry
	// ladder, and the ladder closed.
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{Result: ExecFailed}, nil)

	task, err := svc.CreateTask(CreateTaskRequest{
		Project: "app", Objective: "work", Budget: &Budget{MaxAttempts: 1},
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	_, err = svc.RetryTask(task.ID, RetryRequest{})
	if err == nil {
		t.Fatal("a retry against a spent one-attempt budget should be refused")
	}
	var rej *RejectionError
	if !errors.As(err, &rej) || rej.Reason != ReasonAttemptsExhausted {
		t.Fatalf("err = %v, want a %s refusal", err, ReasonAttemptsExhausted)
	}
	msg := err.Error()
	if !strings.Contains(msg, "task budget "+task.ID) {
		t.Errorf("an exhausted task is not told about `daedalus task budget`, which is the "+
			"whole point of #95 item 4:\n%s", msg)
	}
	// And it must NOT name the operations this very refusal is about to refuse.
	for _, bad := range []string{"task retry", "task dispatch", "task replan"} {
		if strings.Contains(msg, bad) {
			t.Errorf("the refusal offers `%s`, which is bounded by the same counter that just "+
				"refused — a remedy that is itself refused:\n%s", bad, msg)
		}
	}
}

// TestSteerRefusalPointsAtTheTask covers the one operation keyed on a Job's state.
// A job that cannot be steered has nothing that can be done to IT either, so the
// remedies must come from the Task — otherwise the refusal is an empty list
// dressed up as an answer.
func TestSteerRefusalPointsAtTheTask(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil)

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "work"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	res, err := svc.DispatchTask(task.ID)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	// The Job is a finished `candidate` by now — nothing is listening.
	job, err := store.GetJob(res.Job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if Operation(OpSteer).Admits(job.State) {
		t.Fatalf("job is %s, which is steerable; this test needs one that is not", job.State)
	}
	_, err = svc.SteerJob(job.ID, "go left")
	if err == nil {
		t.Fatal("steering a candidate job should be refused")
	}
	msg := err.Error()
	if !strings.Contains(msg, "From here you can:") {
		t.Errorf("a refused steer names no way forward:\n%s", msg)
	}
	if !strings.Contains(msg, task.ID) {
		t.Errorf("a refused steer's remedies should name the TASK (%s), where the work continues:\n%s",
			task.ID, msg)
	}
	if strings.Contains(msg, job.ID+" ") && strings.Contains(msg, "daedalus task steer "+job.ID) {
		t.Errorf("a refused steer offers steering the same job again:\n%s", msg)
	}
}

// selfDestructingRunner exits 0 having removed its own worktree, so the plane's
// Capture has nothing to commit. That is the shape of the fault this is about:
// the AGENT DID ITS JOB and the plane could not save the result.
type selfDestructingRunner struct{}

func (selfDestructingRunner) Run(_ context.Context, spec JobSpec) RunOutcome {
	_ = os.RemoveAll(spec.WorktreeDir)
	return RunOutcome{Result: ExecSuccess}
}

// TestCaptureFailureIsNotChargedToTheOperator is #95 item 3, asserted in BOTH
// directions because the asymmetry is the entire argument.
//
// A capture failure is refunded: the agent exited 0, and the commit is missing
// because our capture did not work. An ordinary execution failure is NOT, because
// the plane cannot tell a broken environment from a genuinely bad run, and
// refunding on an unsure reading would make a Job that fails instantly free to
// repeat forever.
//
// T-28 died of the first half on 2026-08-25 — three attempts gone, mostly to
// this, and cancel-and-recreate the only way out.
func TestCaptureFailureIsNotChargedToTheOperator(t *testing.T) {
	t.Run("a capture failure is refunded", func(t *testing.T) {
		repo := gitRepo(t)
		svc, _, store := newService(t, mapResolver{"app": repo}, selfDestructingRunner{}, nil)

		task, err := svc.CreateTask(CreateTaskRequest{
			Project: "app", Objective: "work", Budget: &Budget{MaxAttempts: 2},
		})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		if _, err := svc.DispatchTask(task.ID); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		// The Job happened and is on the record...
		all, err := store.CountAllJobsForTask(task.ID)
		if err != nil {
			t.Fatalf("CountAllJobsForTask: %v", err)
		}
		if all != 1 {
			t.Fatalf("jobs on the record = %d, want 1 — a refund must not erase the attempt", all)
		}
		// ...and it cost nothing.
		spent, err := store.CountJobsForTask(task.ID)
		if err != nil {
			t.Fatalf("CountJobsForTask: %v", err)
		}
		if spent != 0 {
			t.Errorf("attempts spent = %d, want 0: the agent exited 0 and OUR capture failed, "+
				"so this is a fault of the plane's and must not come out of the operator's budget", spent)
		}
		// The consequence that matters: the retry ladder is still open.
		if _, err := svc.RetryTask(task.ID, RetryRequest{}); err != nil {
			var rej *RejectionError
			if errors.As(err, &rej) && rej.Reason == ReasonAttemptsExhausted {
				t.Fatalf("a task whose only attempt was lost to a capture failure is out of "+
					"budget — this is exactly how T-28 died: %v", err)
			}
		}
	})

	t.Run("an ordinary failure is still charged", func(t *testing.T) {
		repo := gitRepo(t)
		svc, _, store := newService(t, mapResolver{"app": repo}, StubRunner{Result: ExecFailed}, nil)

		task, err := svc.CreateTask(CreateTaskRequest{
			Project: "app", Objective: "work", Budget: &Budget{MaxAttempts: 2},
		})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		if _, err := svc.DispatchTask(task.ID); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		spent, err := store.CountJobsForTask(task.ID)
		if err != nil {
			t.Fatalf("CountJobsForTask: %v", err)
		}
		if spent != 1 {
			t.Errorf("attempts spent = %d, want 1. An agent that exits non-zero may have failed "+
				"for any reason, and a budget that refunded on that reading would never bind", spent)
		}
	})
}

// exhaustedWithArtifact builds the situation an operator actually meets: one
// attempt, SPENT, an artifact on the record, and the task resting at `rejected`
// where the retry ladder opens from.
//
// The artifact matters. A task with no artifact refuses `refine` for want of
// something to continue from, which is a different refusal, and a fixture without
// it would have let the missing-refine bug through a second time — the guard the
// test is aiming at is never reached.
func exhaustedWithArtifact(t *testing.T) (*Service, string) {
	t.Helper()
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil)
	task, err := svc.CreateTask(CreateTaskRequest{
		Project: "app", Objective: "work", Budget: &Budget{MaxAttempts: 1},
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	// candidate → rejected: the oracle said no. Driven directly rather than by
	// running the verifier, because what is under test is the budget, not the
	// grading.
	if _, err := store.TransitionTaskWith(task.ID, StateRejected, false,
		EventMeta{Kind: EventGovernance}, "graded and found wanting"); err != nil {
		t.Fatalf("to rejected: %v", err)
	}
	return svc, task.ID
}

// TestExhaustedBudgetRefusesEverythingItDoesNotOffer is the test that would have
// caught the bug a screenshot caught instead.
//
// The first version of the exhausted-attempts refusal dropped dispatch, retry and
// replan from its remedy list and left REFINE in — and refine calls
// checkDispatchBudget like the other three, so the Ledger told an operator to
// refine a task whose refine had just been refused. That is #95's original defect
// reproduced inside the fix for #95, and it survived every assertion because the
// assertions checked the three names somebody had thought of.
//
// So this checks the list by EXERCISING it, in both directions:
//
//   - every operation in attemptSpendingOps really is refused on an exhausted
//     task (otherwise the list is over-broad and hides a way out);
//   - every operation NOT in it really is not (otherwise the list is short and
//     the refusal offers something that cannot work).
func TestExhaustedBudgetRefusesEverythingItDoesNotOffer(t *testing.T) {
	spending := map[Operation]bool{}
	for _, op := range attemptSpendingOps {
		spending[op] = true
	}

	for _, op := range AllOperations() {
		if op.Target() != TargetTask {
			continue
		}
		op := op
		t.Run(string(op), func(t *testing.T) {
			if !op.Admits(StateRejected) {
				t.Skipf("%s is not admitted from rejected, so the budget never arises", op)
			}
			svc, id := exhaustedWithArtifact(t)
			err := callOperation[op](svc, id, "")
			var rej *RejectionError
			budgetRefused := errors.As(err, &rej) && rej.Reason == ReasonAttemptsExhausted

			if spending[op] && !budgetRefused {
				t.Errorf("%q is listed in attemptSpendingOps but an exhausted task did not "+
					"refuse it on budget grounds (err = %v). The list is over-broad, and a "+
					"refusal is hiding a way out that would have worked", op, err)
			}
			if !spending[op] && budgetRefused {
				t.Errorf("%q is NOT in attemptSpendingOps and an exhausted task refused it "+
					"with %s. So the exhausted-attempts refusal offers it as a remedy, and it "+
					"is itself refused — the exact defect #95 was filed for",
					op, ReasonAttemptsExhausted)
			}
		})
	}
}

// TestExhaustedRefusalOffersNothingItWouldRefuse closes the loop end to end: take
// the refusal an exhausted task actually produces, and try every operation it
// names. Each must get past the budget.
//
// The test above checks the LIST. This checks the SENTENCE, which is what an
// operator reads, and the two are only the same while nothing sits between them.
func TestExhaustedRefusalOffersNothingItWouldRefuse(t *testing.T) {
	svc, id := exhaustedWithArtifact(t)
	_, err := svc.RetryTask(id, RetryRequest{})
	var rej *RejectionError
	if !errors.As(err, &rej) || rej.Reason != ReasonAttemptsExhausted {
		t.Fatalf("retry on a spent budget = %v, want %s", err, ReasonAttemptsExhausted)
	}
	if len(rej.Remedies) == 0 {
		t.Fatal("an exhausted task was refused with no way forward at all")
	}
	for _, remedy := range rej.Remedies {
		if remedy == OpCancel {
			continue // always works, and always destroys the history
		}
		// A fresh plane per remedy: several of these move the task, and a remedy
		// must be reachable from where the refusal was issued, not from where the
		// previous remedy left it.
		fresh, fid := exhaustedWithArtifact(t)
		got := callOperation[remedy](fresh, fid, "")
		var r2 *RejectionError
		if errors.As(got, &r2) && r2.Reason == ReasonAttemptsExhausted {
			t.Errorf("the refusal offers %q as a way out and %q is itself refused with %s",
				remedy, remedy, ReasonAttemptsExhausted)
		}
		var se *StateError
		if errors.As(got, &se) {
			t.Errorf("the refusal offers %q and it is refused on state grounds: %v", remedy, got)
		}
	}
}
