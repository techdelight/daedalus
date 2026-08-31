// Copyright (C) 2026 Techdelight BV

package control

import (
	"fmt"
	"strings"
)

// THE ONE PLACE THAT ANSWERS "WHICH OPERATIONS DOES THIS STATE ADMIT?"
//
// Until this file existed, that question was not answerable by any single piece
// of code (#95, docs/no-dead-ends.md). The answer was spread over four tables
// (`amendableStates`, `steerableStates`, `refinableStates`, `reviewableStates`),
// a dozen inline `task.State != …` guards across service.go, reverify.go,
// approval.go, budgetamend.go and integrate.go, and a THIRD copy as
// `states: [...]` on the Ledger's COMMANDS entries — in JavaScript, where nothing
// in Go can see it.
//
// The cost was not merely that the three could drift. It was that no refusal
// could COMPUTE what to suggest, so every remedy was hand-written prose, and
// prose goes stale exactly the way a hand-written list of subcommands does. It
// produced worse than staleness: the guard added for the first dead end told the
// operator to run `daedalus task retry`, and retry refuses a `candidate` — a
// remedy that was itself refused.
//
// THE PRINCIPLE THIS FILE ENFORCES:
//
//	Every refusal names at least one operation the caller can actually perform
//	from the state they are in — and if there is none, that is a bug in the state
//	machine, not a message to be worded better.
//
// WHAT THIS IS NOT. It is not a capability registry, a policy engine or a
// surface generator. It is one table of operation → states, plus the guard that
// reads it and the refusal that derives its remedies from it. Authority (WHO may
// call) stays in authority.go, which is a different question with a different
// answer per caller class; this table is about the ENTITY's state, and the two
// are deliberately not merged.

// OperationTarget says which entity's state an operation is guarded against. It
// matters because `steer` is the one operation keyed on a JOB's state — a Task
// can be `working` while the Job that would receive the instruction has already
// exited — and a remedy list that mixed the two would suggest steering a job
// that is not there.
type OperationTarget string

const (
	TargetTask OperationTarget = "task"
	TargetJob  OperationTarget = "job"
)

// Operation names one thing a caller can ask the plane to do.
//
// IT IS THE SAME IDENTITY authority.go already uses. Those `Op*` constants are
// the strings that appear in proposal rows and in the event log, so they are part
// of the record and cannot be renamed — and defining a second, prettier
// vocabulary here would have made this file the fifth copy of the thing it was
// written to abolish. The two tables answer different questions about the same
// operation: authority.go says WHO may call it, this file says FROM WHAT STATE.
type Operation string

// Two operations had no name at all before this file, and authority.go says why:
// amending a Task's checks and raising its budget are refused to an agent
// OUTRIGHT rather than offered as proposals, and having no operation name was one
// of the ways that was enforced.
//
// Naming them here does not tier them. A name in the STATE table is not an
// authority entry: `TierFor` fails closed on an unknown operation, neither
// appears in `mutatingOps`, and TestChecksAndBudgetAreNeverTiered pins the
// absence directly rather than leaving it resting on nobody having typed them.
// What the names buy is that a refusal can now offer `task budget` as the way out
// of an exhausted envelope — which is the whole of #95 item 4's usefulness, and
// was unreachable while the operation was anonymous.
const (
	OpChecks = "amend_checks"
	OpBudget = "amend_budget"
)

// operationSpec is what the table holds.
type operationSpec struct {
	target OperationTarget
	// surface is the CLI subcommand and the Ledger's command key. It differs from
	// the operation's record name (`dispatch` vs `dispatch_task`) because the
	// record name is frozen by the rows already written and the surface name is
	// what an operator types. Both are here, once, rather than in two files.
	surface string
	// states is the enumerated admit-set. Exactly one of states/derive is set.
	states map[State]bool
	// derive computes the admit-set from a table that already exists. Preferred
	// over states wherever such a table exists: the repository's recurring defect
	// is a hand-written list that stops matching the thing it describes.
	derive func(State) bool
	// summary is the one line a refusal prints beside the operation. It says what
	// the operation DOES, not when to use it — "why this one" is a human sentence
	// that belongs at the call site.
	summary string
}

// operations is THE table.
//
// Each entry's admit-set is the same set its guard enforces, and
// TestOperationTable_MatchesGuards_Exhaustive drives the real Service across
// every (operation, state) pair to prove it — a second, independent statement of
// the same rule, which is the one kind of test in this repository that has
// repeatedly caught real mistakes (TestCanTransition_Exhaustive's shape).
var operations = map[Operation]operationSpec{
	// Dispatchable from planned/queued (a first attempt) or rejected (the retry
	// path from §6's ladder). `blocked` is deliberately absent: a Task waiting on
	// the graph is not runnable, and the scheduler never admits one.
	OpDispatch: {
		target: TargetTask, surface: "dispatch",
		states:  set(StatePlanned, StateQueued, StateRejected),
		summary: "start an attempt",
	},
	OpVerify: {
		target: TargetTask, surface: "verify",
		states:  set(StateCandidate),
		summary: "grade the artifact against the frozen oracle",
	},
	OpRetry: {
		target: TargetTask, surface: "retry",
		states:  set(StateRejected),
		summary: "a fresh attempt from the same base — costs an attempt",
	},
	OpReverify: {
		target: TargetTask, surface: "reverify",
		states:  set(StateRejected),
		summary: "grade the SAME artifact again — costs no attempt",
	},
	// `candidate` is the one that was missing for a long time: the work is done,
	// nobody has graded it, and the operator has realised the question was wrong.
	OpReplan: {
		target: TargetTask, surface: "replan",
		states:  set(StateRejected, StateCandidate),
		summary: "replace the objective and start over from a clean tree",
	},
	// The states with an artifact worth continuing. `verified` and
	// `approval_required` are the interesting inclusions: they are where a Task
	// sits after a review, and neither retry nor replan opens from them — so a
	// reading of good work used to lead nowhere the plane could act on. `working`
	// and `verifying` are excluded because an attempt is in flight and its result
	// is about to land.
	OpRefine: {
		target: TargetTask, surface: "refine",
		states:  set(StateCandidate, StateRejected, StateVerified, StateApprovalRequired, StateApproved),
		summary: "continue from the work already done, answering a review",
	},
	// A reviewer answers a question about a DIFF, and a diff either exists or it
	// does not. What the machine oracle thought of it is beside the point — the
	// reviewer is the second opinion, and a second opinion available only after
	// the first one agrees is not one. `rejected` is in the set for that reason:
	// it is the case that needs a reading most. `verifying` is excluded because a
	// grading is in flight and its verdict is about to land.
	OpReview: {
		target: TargetTask, surface: "review",
		states:  set(StateCandidate, StateRejected, StateVerified, StateApprovalRequired, StateApproved),
		summary: "send an agent to read the change — advisory, it moves nothing",
	},
	// The exclusions matter more than the inclusions. `verifying` is refused
	// because changing the criteria while they are being applied is a race with no
	// correct outcome. Everything from `verified` onward is refused because it
	// would make the record incoherent rather than because it would change a
	// verdict already given: a Task shown as having passed criteria it no longer
	// carries is a worse artefact than a wrong verdict, since nothing about it
	// looks wrong. A human who wants a stricter bar after a pass has the approval
	// gate, which is designed to say no.
	OpChecks: {
		target: TargetTask, surface: "checks",
		states:  set(StatePlanned, StateBlocked, StateQueued, StateCandidate, StateRejected),
		summary: "change this task's own acceptance commands",
	},
	// DERIVED, not enumerated: a budget amendment is refused exactly when the Task
	// is terminal, and `terminalStates` already says which those are. Writing the
	// list again here is how the fifth copy gets born.
	OpBudget: {
		target: TargetTask, surface: "budget",
		derive:  func(s State) bool { return validState(s) && !IsTerminal(s) },
		summary: "raise this task's attempts or review cycles, within the project ceiling",
	},
	// `approved` is admitted because approving twice is idempotent, not an error.
	// The Ledger declines to OFFER it there, which is a presentation choice and
	// belongs in the Ledger; the plane's answer is that the call is legal.
	OpApprove: {
		target: TargetTask, surface: "approve",
		states:  set(StateVerified, StateApprovalRequired, StateApproved),
		summary: "accept the change at the approval gate",
	},
	OpRejectAppr: {
		target: TargetTask, surface: "reject",
		states:  set(StateVerified, StateApprovalRequired),
		summary: "decline the change — it returns to the retry ladder",
	},
	OpIntegrate: {
		target: TargetTask, surface: "integrate",
		states:  set(StateVerified, StateApprovalRequired, StateApproved),
		summary: "rebase onto the target, re-verify the merged result, and land it",
	},
	// DERIVED from the transition table: cancellation has no guard of its own, it
	// simply drives an edge, and `legalTransitions` is where that edge is written.
	OpCancel: {
		target: TargetTask, surface: "cancel",
		derive:  func(s State) bool { return CanTransition(s, StateCancelled) },
		summary: "end the task and any running job — terminal, there is no way back",
	},
	// The one operation whose state rule is NOT enforced through requireOperable.
	// It lives inside the transaction that inserts the edge (store.AddDependency),
	// because a check made outside can be true when it runs and false when the row
	// lands — and it answers ErrDependencyInvalid, which the daemon maps
	// deliberately. The admit-set is here anyway, because the Ledger has to know
	// whether to offer the plate; what it does not do is enforce the rule twice.
	// See the note at Service.AddDependency for the guard that was added here and
	// then removed.
	OpAddDependency: {
		target: TargetTask, surface: "depends",
		derive:  func(s State) bool { return validState(s) && !IsTerminal(s) },
		summary: "make this task wait for another to land",
	},
	// The one JOB-keyed operation: the states in which an instruction could
	// conceivably reach a worker. Everything else is refused rather than
	// recorded-and-dropped.
	OpSteer: {
		target: TargetJob, surface: "steer",
		states:  set(StateWorking, StateInputRequired),
		summary: "inject an instruction into the running job",
	},
}

// allOperations is the stable order every surface renders in: roughly the order
// an operator meets them, execution before governance before the exits. Declared
// rather than sorted alphabetically because "cancel, checks, depends" first is
// not the order anybody thinks in.
var allOperations = []Operation{
	OpDispatch, OpVerify, OpRetry, OpReverify, OpReplan, OpRefine, OpReview,
	OpChecks, OpBudget, OpApprove, OpRejectAppr, OpIntegrate, OpAddDependency,
	OpSteer, OpCancel,
}

func set(states ...State) map[State]bool {
	m := make(map[State]bool, len(states))
	for _, s := range states {
		m[s] = true
	}
	return m
}

// AllOperations lists every operation in render order.
func AllOperations() []Operation {
	out := make([]Operation, len(allOperations))
	copy(out, allOperations)
	return out
}

// Target says which entity's state this operation is guarded against.
func (o Operation) Target() OperationTarget { return operations[o].target }

// Summary is the one line a refusal prints beside the operation.
func (o Operation) Summary() string { return operations[o].summary }

// Admits reports whether the operation is legal from state s. An unknown
// operation admits nothing — a typo must not read as permission.
func (o Operation) Admits(s State) bool {
	spec, ok := operations[o]
	if !ok {
		return false
	}
	if spec.derive != nil {
		return spec.derive(s)
	}
	return spec.states[s]
}

// States lists the states this operation is legal from, in AllStates order so
// two surfaces never disagree about the order either.
func (o Operation) States() []State {
	var out []State
	for _, s := range AllStates() {
		if o.Admits(s) {
			out = append(out, s)
		}
	}
	return out
}

// Command is the CLI form, for a refusal that wants to name a way forward. The
// operation key IS the subcommand — see the Operation doc — and
// TestEveryOperationIsARealSubcommand reads task.go's dispatch switch to prove
// it rather than trusting this sentence.
func (o Operation) Command(id string) string {
	return fmt.Sprintf("daedalus task %s %s", o.Surface(), id)
}

// Surface is the CLI subcommand and the Ledger's command key for this operation.
func (o Operation) Surface() string { return operations[o].surface }

// RemediesFrom lists every operation legal from state s against the given
// target, in render order. This is what a refusal offers, and it is COMPUTED —
// which is the entire point of the file. A hand-written remedy is a remedy that
// can be wrong.
//
// `cancel` is deliberately last in allOperations and included: it is a real way
// out, and hiding it would be its own kind of dishonesty. What the design forbids
// is a state where cancel is the ONLY way out — see TestNoStateIsADeadEnd.
func RemediesFrom(target OperationTarget, s State) []Operation {
	var out []Operation
	for _, op := range allOperations {
		if op.Target() == target && op.Admits(s) {
			out = append(out, op)
		}
	}
	return out
}

// attemptSpendingOps are the operations bounded by max-attempts: each one leads,
// directly or after one more step, to a new Job, and each therefore calls
// checkDispatchBudget.
//
// THIS LIST IS WHY IT IS A LIST. An exhausted budget admits all four on STATE
// grounds and refuses all four on BUDGET grounds, so a remedy list that did not
// subtract them would name operations about to be refused — #95's original defect
// with the axes swapped. It was in the tree for an afternoon: the first version
// of this dropped dispatch, retry and replan and left REFINE in, and a screenshot
// of the Ledger showed an operator being told to refine a task whose refine had
// just been refused.
//
// `refine` is the easy one to miss, which is exactly why it is named here rather
// than left to whoever writes the next refusal: it reads like an edit to existing
// work rather than a new attempt, and it is a new attempt.
//
// TestExhaustedBudgetRefusesEverythingItDoesNotOffer holds the list to account by
// EXERCISING it — every operation named here must genuinely be refused on an
// exhausted task, and every operation NOT named here must genuinely not be. So
// the list cannot quietly stop matching the code the way a comment would.
var attemptSpendingOps = []Operation{OpDispatch, OpRetry, OpReplan, OpRefine}

// RemediesForExhaustedAttempts is what a task whose attempts are spent can still
// do: everything its state admits, minus everything bounded by the counter that
// just refused.
//
// One function rather than a filter written at the call site, because the call
// site is not the only place that needs the answer — the Ledger's browser fixture
// needs the same list, and a fixture that hand-wrote it would go on passing after
// the real rule changed. That is the shape of defect this whole file exists to
// remove, and a test fixture is not exempt from it.
func RemediesForExhaustedAttempts(state State) []Operation {
	return withoutOps(RemediesFrom(TargetTask, state), attemptSpendingOps...)
}

// withoutOps removes operations a particular refusal knows cannot help, even
// though the state admits them.
//
// It exists for exactly one shape: a budget that is exhausted admits `retry` on
// STATE grounds and refuses it on BUDGET grounds, so an unfiltered remedy list
// would name an operation that is itself about to be refused. That is the
// original defect of #95 with the roles swapped, and the filter is where the
// caller says which axis it is enforcing. Deliberately explicit — a remedy list
// that quietly second-guessed the state table would be a fifth opinion about what
// is legal.
func withoutOps(ops []Operation, drop ...Operation) []Operation {
	skip := make(map[Operation]bool, len(drop))
	for _, d := range drop {
		skip[d] = true
	}
	out := make([]Operation, 0, len(ops))
	for _, op := range ops {
		if !skip[op] {
			out = append(out, op)
		}
	}
	return out
}

// OperationView is the wire shape: what a client needs to know about one
// operation without restating any of it. The Ledger's COMMANDS entries keep
// their labels, hints and request bodies — those are presentation — and take
// their `states` from here.
type OperationView struct {
	// Key is the SURFACE name — what the CLI and the Ledger call this operation.
	// The record name (`dispatch_task`) is deliberately not on the wire: it is an
	// internal identity for proposals and events, and putting it here would invite
	// a client to key off it.
	Key     string          `json:"key"`
	Target  OperationTarget `json:"target"`
	States  []State         `json:"states"`
	Summary string          `json:"summary"`
}

// OperationCatalogue is the whole table, for the API that serves it.
func OperationCatalogue() []OperationView {
	out := make([]OperationView, 0, len(allOperations))
	for _, op := range allOperations {
		out = append(out, OperationView{
			Key:     op.Surface(),
			Target:  op.Target(),
			States:  op.States(),
			Summary: op.Summary(),
		})
	}
	return out
}

// StateError is a refusal on STATE grounds that carries its own way out.
//
// It wraps ErrWrongState so every existing caller — errors.Is in the daemon's
// status mapping, the CLI's exit codes, the tests — keeps working unchanged. What
// it adds is Remedies, derived from the table above rather than written by hand,
// so the sentence an operator reads cannot name an operation that would itself be
// refused.
type StateError struct {
	Op       Operation
	Entity   string
	State    State
	Detail   string // optional: why this particular state cannot, in the plane's words
	Remedies []Operation
}

func (e *StateError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s is %s, so `%s` is not available",
		ErrWrongState.Error(), e.Entity, e.State, e.Op)
	if e.Detail != "" {
		b.WriteString(" — " + e.Detail)
	}
	if states := e.Op.States(); len(states) > 0 {
		b.WriteString(" (it wants " + joinStates(states) + ")")
	}
	b.WriteString(". " + RenderRemedies(e.Entity, e.State, e.Remedies))
	return b.String()
}

// endSentence gives a message exactly one terminating full stop, so a refusal and
// the way out of it read as two sentences rather than one run-on.
func endSentence(s string) string {
	s = strings.TrimRight(s, " ")
	if s == "" || strings.HasSuffix(s, ".") || strings.HasSuffix(s, "!") || strings.HasSuffix(s, "?") {
		return s
	}
	return s + "."
}

// Unwrap keeps errors.Is(err, ErrWrongState) true, which the daemon's 409 mapping
// and the CLI's exit code both depend on.
func (e *StateError) Unwrap() error { return ErrWrongState }

// RenderRemedies turns a computed remedy list into the sentence a human reads.
// Shared by the error string and the CLI so the two cannot word it differently.
//
// THE EMPTY CASE HAS TWO MEANINGS and they must not be collapsed. A TERMINAL
// state legitimately has nowhere to go — that is what terminal means, and saying
// "nothing is available" there is an accurate, complete answer. A NON-TERMINAL
// state with nothing available is the dead end #95 exists to abolish, and the
// message says so in as many words, because a refusal that trails off politely is
// how five of them went unnoticed for an evening.
func RenderRemedies(id string, state State, ops []Operation) string {
	if len(ops) == 0 {
		if IsTerminal(state) {
			return fmt.Sprintf("%s is %s and finished; nothing further is possible on it. "+
				"Work that still needs doing wants a new task.", id, state)
		}
		return fmt.Sprintf("Nothing at all is available from %s, which is a defect in the plane "+
			"rather than in the request — please report it with this task's id (%s).", state, id)
	}
	parts := make([]string, 0, len(ops))
	for _, op := range ops {
		parts = append(parts, fmt.Sprintf("`%s` (%s)", op.Command(id), op.Summary()))
	}
	return "From here you can: " + strings.Join(parts, "; ") + "."
}

func joinStates(states []State) string {
	names := make([]string, 0, len(states))
	for _, s := range states {
		names = append(names, string(s))
	}
	return strings.Join(names, "/")
}

// requireOperable is THE guard. Every state precondition in the package goes
// through it, so the refusal and the rule can never come from different places.
//
// detail is optional and carries what the table cannot: the reason THIS state in
// particular is refused, in the plane's words rather than a generic one.
func requireOperable(op Operation, entity string, s State) error {
	if op.Admits(s) {
		return nil
	}
	return &StateError{
		Op:       op,
		Entity:   entity,
		State:    s,
		Remedies: RemediesFrom(op.Target(), s),
	}
}

// requireOperableWith is requireOperable with a sentence explaining this
// particular refusal. Used where the old hand-written message said something the
// table does not know — that amending checks mid-verification is a race with no
// correct outcome, say.
func requireOperableWith(op Operation, entity string, s State, detail string) error {
	if op.Admits(s) {
		return nil
	}
	return &StateError{
		Op:       op,
		Entity:   entity,
		State:    s,
		Detail:   detail,
		Remedies: RemediesFrom(op.Target(), s),
	}
}
