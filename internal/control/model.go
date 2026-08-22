// Copyright (C) 2026 Techdelight BV

// Package control is the foundation of the host-side control plane described in
// docs/guild-master-plan.md §5. It defines the authoritative Task / Job /
// Artifact data model, the control-plane-owned state machine, and (in
// store.go) a durable SQLite store that holds *desired* state.
//
// Sprint 54 scope: the model, the store, and the human `daedalus task` CLI that
// drives the store in-process. There is deliberately NO execution here — no
// daemon, no control socket, no Git worktree, no coordinator, no verifier, no
// Guild Master client. Those land in Sprint 55 (M13) and later (M14/M15). See
// docs/control-plane.md for the V1 scope boundary.
package control

// State is a control-plane task/job lifecycle state. The legal moves encode the
// state machine from docs/guild-master-plan.md §5:
//
//	planned → queued → working → candidate → verifying →(PASS)→ verified
//	                     │  ▲                    │                  → approval_required
//	              input_required                 └─(FAIL)→ rejected → retry/replan
//	                                                                  → approved → integrated
//	terminal: failed · cancelled · expired · integrated
//
// The load-bearing invariant (§5): a *worker* may only drive up to `candidate`
// ("I think it's done"); only the control plane performs candidate → verified.
// That is enforced by two transition entry points — WorkerCanTransition vs the
// full CanTransition — see below and store.TransitionTask's byWorker argument.
type State string

const (
	// Pre-execution.
	StatePlanned State = "planned"
	// StateBlocked is a Task waiting on another Task (Sprint 62's dependency
	// graph). It is NOT a failure: the work is well-formed and simply not yet
	// runnable. The scheduler never admits it, and satisfying the last dependency
	// returns it to `planned`.
	StateBlocked State = "blocked"
	StateQueued  State = "queued"

	// Execution.
	StateWorking       State = "working"
	StateInputRequired State = "input_required"

	// Worker's terminal reach: it may declare a candidate, nothing further.
	StateCandidate State = "candidate"

	// Control-plane-only verification + governance.
	StateVerifying        State = "verifying"
	StateVerified         State = "verified"
	StateRejected         State = "rejected"
	StateApprovalRequired State = "approval_required"
	StateApproved         State = "approved"

	// Terminal states.
	StateIntegrated State = "integrated"
	StateFailed     State = "failed"
	StateCancelled  State = "cancelled"
	StateExpired    State = "expired"
)

// AllStates lists every valid state (useful for validation and tests).
func AllStates() []State {
	return []State{
		StatePlanned, StateBlocked, StateQueued, StateWorking, StateInputRequired,
		StateCandidate, StateVerifying, StateVerified, StateRejected,
		StateApprovalRequired, StateApproved, StateIntegrated,
		StateFailed, StateCancelled, StateExpired,
	}
}

// terminalStates are states with no outgoing transitions. A Task/Job in one of
// these is "done" for the one-active-per-project invariant.
var terminalStates = map[State]bool{
	StateIntegrated: true,
	StateFailed:     true,
	StateCancelled:  true,
	StateExpired:    true,
}

// IsTerminal reports whether s is a terminal state (no legal outgoing move).
func IsTerminal(s State) bool { return terminalStates[s] }

// IsActive reports whether s is a non-terminal (in-flight) state.
func IsActive(s State) bool { return validState(s) && !terminalStates[s] }

// runningStates are the states in which a Job occupies a runner slot — the ones
// the scheduler's concurrency limits count.
//
// `candidate`, `verifying` and `rejected` are non-terminal but IDLE: they are
// waiting on a plane or human decision, not on a container, so counting them
// would make a project look full while nothing was executing.
var runningStates = map[State]bool{
	StateQueued: true, StateWorking: true, StateInputRequired: true,
}

// IsRunningState reports whether a Job in this state is occupying a runner slot.
func IsRunningState(s State) bool { return runningStates[s] }

// legalTransitions is the full transition table the control plane may drive.
// A terminal state is intentionally absent (no outgoing edges). Cancellation
// and expiry are reachable from every active state; failure from the execution
// path.
var legalTransitions = map[State]map[State]bool{
	StatePlanned: {
		// planned → blocked: a dependency was declared and is not yet satisfied.
		StateBlocked: true,
		StateQueued:  true, StateCancelled: true, StateExpired: true, StateFailed: true,
	},
	StateBlocked: {
		// blocked → planned: the last dependency completed. Both edges are
		// plane-only — a worker cannot declare itself unblocked, which is the same
		// rule that keeps it out of `verified`.
		StatePlanned:   true,
		StateCancelled: true, StateExpired: true, StateFailed: true,
	},
	StateQueued: {
		StateWorking: true, StateCancelled: true, StateExpired: true, StateFailed: true,
	},
	StateWorking: {
		StateCandidate: true, StateInputRequired: true,
		// A `working` Task with NO Job at all is a crash between the transition and
		// the Job insert (Sprint 62). Reconcile returns it here rather than to a
		// terminal state, because nothing was ever attempted: the objective is
		// still good and `rejected` is the state the retry/replan ladder already
		// understands. A downgrade, plane-only, and it brings nothing closer to
		// `verified`.
		StateRejected:  true,
		StateCancelled: true, StateExpired: true, StateFailed: true,
	},
	StateInputRequired: {
		StateWorking: true, StateCancelled: true, StateExpired: true, StateFailed: true,
	},
	StateCandidate: {
		StateVerifying: true, StateRejected: true,
		// candidate → planned: REPLANNING WORK NOBODY HAS GRADED YET (#84's
		// sibling). Until this edge existed the only ways out of `candidate` were
		// to grade it, reject it, or cancel it — so an operator who realised the
		// INSTRUCTION was wrong while the work sat ungraded had to run a
		// verification they did not care about, purely to reach the `rejected`
		// state the ladder opens from. Spending a review cycle to earn permission
		// to say "I asked for the wrong thing" is a toll, not a safeguard.
		//
		// Plane-only, and a DOWNGRADE: absent from workerReachable, it brings
		// nothing closer to `verified`. It throws away an attempt the operator has
		// already decided was aimed at the wrong target, which is the one direction
		// that can never launder anything.
		StatePlanned:   true,
		StateCancelled: true, StateExpired: true, StateFailed: true,
	},
	StateVerifying: {
		StateVerified: true, StateRejected: true,
		// Back to candidate: the ONLY way out of `verifying` without a verdict.
		// A verification that was interrupted (a daemon crash, an aborted verifier)
		// left the artifact unexamined, so the plane returns it to `candidate` for
		// another attempt rather than stranding it in a state nothing but cancel can
		// leave. Reconcile and VerifyTask's abort path are the only users. This edge
		// is plane-only — it is deliberately absent from workerReachable, so it
		// brings a worker no closer to `verified`.
		StateCandidate: true,
		StateCancelled: true, StateExpired: true, StateFailed: true,
	},
	StateVerified: {
		StateApprovalRequired: true,
		// verified → planned: REFINING WORK THAT PASSED (#91). A review can find
		// four real things in an artifact the machine oracle was happy with, and
		// until this edge existed the answer was to throw the work away (replan
		// re-dispatches from a clean tree) or to leave the plane entirely. A
		// DOWNGRADE — plane-only, absent from workerReachable — and it brings
		// nothing closer to `integrated`: the refined attempt is graded from the
		// same base by the same frozen oracle.
		StatePlanned: true,

		// A gate AFTER verification can still say no: a failed independent review,
		// or a human declining at the approval gate. This is a downgrade, never an
		// escalation — it cannot bring anything closer to `verified`/`approved`/
		// `integrated`, which is the invariant this table exists to protect — and it
		// feeds the retry/replan ladder rather than stranding the Task.
		StateRejected:  true,
		StateCancelled: true, StateExpired: true,
	},
	StateRejected: {
		// retry (re-queue) or replan (back to planned).
		StateQueued: true, StatePlanned: true,
		// Back to candidate: RE-VERIFICATION. The artifact is unchanged and still
		// reachable; what is being redone is the GRADING of it, not the work. A
		// verdict can be wrong for reasons that have nothing to do with the artifact
		// — a verifier that never ran the check, an oracle that failed on an
		// advisory finding — and before this edge existed the only remedy was
		// `retry`, which dispatches a fresh Job and discards work that was never in
		// question.
		//
		// Symmetry with the `verifying → candidate` edge above is the argument for
		// it: the plane already treats a verification that produced NO verdict as
		// recoverable, and a verdict produced by a broken harness examined the
		// artifact no more than a crashed one did. Treating the wrong verdict as
		// more final than no verdict was backwards.
		//
		// Plane-only, and a DOWNGRADE: absent from workerReachable, it brings
		// nothing closer to `verified` — it returns an artifact to the queue to be
		// judged again, by the same verification path. What it must never become is
		// an appeal against the artifact itself; that guard lives in ReverifyTask,
		// which refuses the reasons that were statements about the diff.
		StateCandidate: true,
		// Back to the approval ladder. Two users, one meaning — a human moving work
		// past the gate on their own authority rather than the oracle's: a WAIVED
		// verification result (`task verify --ignore-result`), and reconcile settling
		// a rejected Task whose commits a human merged into the target by hand.
		// Note what this edge deliberately is NOT:
		// it is not a path to `verified`. A waived artifact is never marked verified,
		// because `verified` means "the plane applied its own oracle and the artifact
		// passed", and saying that about something that failed would put a false
		// statement into an append-only log that approval, integration and dependency
		// satisfaction all read as true. What a waiver changes is who is answerable:
		// the rejection stands on the record, the artifact keeps verify=fail, and a
		// named human takes the change forward on their own authority. Plane-only,
		// human-only, and impossible to reach without the explicit flag.
		StateApprovalRequired: true,
		StateCancelled:        true, StateExpired: true,
	},
	StateApprovalRequired: {
		StateApproved: true, StateRejected: true,
		// approval_required → planned: the state a Task sits in when a human is
		// reading a review and decides the work needs one more pass (#91). Same
		// downgrade, same reasoning as verified → planned.
		StatePlanned:   true,
		StateCancelled: true, StateExpired: true,
	},
	StateApproved: {
		StateIntegrated: true,
		// approved → planned: a human approved, then read the review again and
		// changed their mind before landing (#91). Refusing this would make the
		// approval the point of no return for a correction, which it is not — the
		// landing is.
		StatePlanned: true,
		// An integration that fails (a rebase conflict, or the MERGED result failing
		// verification) routes here so the Sprint-58 retry/replan ladder can pick the
		// Task up. Plane-only, like every edge past `candidate`.
		StateRejected:  true,
		StateCancelled: true,
	},
	// Terminal: StateIntegrated, StateFailed, StateCancelled, StateExpired have
	// no outgoing edges.
}

// workerReachable is the *subset* of transitions a worker-driven request may
// perform. Everything else — most importantly anything into `verified` and the
// whole governance/integration tail — is control-plane-only. This is the
// structural encoding of "verification is not conversational" (§5): a worker
// literally cannot name `verified` as a target.
//
// Sprint 58 (governance) deliberately added NOTHING here and nothing to
// legalTransitions: retry reuses rejected → queued, replan reuses
// rejected → planned, a wall-clock kill reuses working → failed, and a budget
// refusal changes no state at all. The event log's `actor` label (EventMeta) is
// likewise a label only — authority still comes from this table plus the
// byWorker flag, never from what an event says.
var workerReachable = map[State]map[State]bool{
	StateWorking: {
		StateCandidate:     true, // "I think it's done."
		StateInputRequired: true, // "I'm blocked, need input."
	},
	StateInputRequired: {
		StateWorking: true, // resumed after input.
	},
}

// validState reports whether s is a known state.
func validState(s State) bool {
	if _, ok := legalTransitions[s]; ok {
		return true
	}
	return terminalStates[s]
}

// CanTransition reports whether the control plane may move from → to. This is
// the full authority table; use WorkerCanTransition for worker-driven requests.
func CanTransition(from, to State) bool {
	return legalTransitions[from][to]
}

// WorkerCanTransition reports whether a *worker-driven* request may move
// from → to. It is strictly a subset of CanTransition: a worker may reach at
// most `candidate` (plus the input_required detour). candidate → verified is
// therefore impossible for a worker — only the control plane can perform it.
func WorkerCanTransition(from, to State) bool {
	return workerReachable[from][to]
}

// CanTransitionBy dispatches to the worker or plane table based on byWorker.
// The store uses this so a single atomic UPDATE path enforces the invariant.
func CanTransitionBy(from, to State, byWorker bool) bool {
	if byWorker {
		return WorkerCanTransition(from, to)
	}
	return CanTransition(from, to)
}

// ExecutionResult classifies how a Job's run ended — the "how it ended" axis,
// distinct from the committed tree it left behind (see §5). Only a `success`
// result may promote its snapshot to a candidate Artifact; commit-exists never
// implies job-succeeded.
type ExecutionResult string

const (
	ExecNone      ExecutionResult = "" // not yet run / in flight
	ExecSuccess   ExecutionResult = "success"
	ExecFailed    ExecutionResult = "failed"
	ExecTimeout   ExecutionResult = "timeout"
	ExecCancelled ExecutionResult = "cancelled"
)

// validExecutionResults is the closed set persisted for jobs.execution_result.
var validExecutionResults = map[ExecutionResult]bool{
	ExecNone: true, ExecSuccess: true, ExecFailed: true,
	ExecTimeout: true, ExecCancelled: true,
}

// IsValidExecutionResult reports whether r is a known execution_result value.
func IsValidExecutionResult(r ExecutionResult) bool { return validExecutionResults[r] }

// VerifyStatus is an Artifact's independent-verification outcome (§6). In
// Sprint 54 nothing sets it beyond "pending" — the clean-verifier that performs
// candidate → verified lands in Sprint 55/M14.
type VerifyStatus string

const (
	VerifyPending VerifyStatus = "pending"
	VerifyPass    VerifyStatus = "pass"
	VerifyFail    VerifyStatus = "fail"
)

// ReviewStatus is an Artifact's independent-review outcome (§6). Also inert in
// Sprint 54.
type ReviewStatus string

const (
	ReviewPending ReviewStatus = "pending"
	ReviewPass    ReviewStatus = "pass"
	ReviewFail    ReviewStatus = "fail"
)

// Task is the unit of intent: what to accomplish for a project (§5). Its state
// is control-plane-authoritative; the Guild Master's TASKS.md would be a
// read-only projection of it (not built in this sprint).
type Task struct {
	ID            string `json:"id"`            // human-friendly, sortable, e.g. "T-1"
	Project       string `json:"project"`       // registry project name
	Objective     string `json:"objective"`     // what to do
	AcceptanceRef string `json:"acceptanceRef"` // optional acceptance-policy reference
	BaseSHA       string `json:"baseSha"`       // git HEAD captured at creation
	// AcceptanceHash freezes the project's verify policy (commands + globs) as it
	// stood at BaseSHA (see AcceptancePolicy.Hash). Captured once at create; a
	// later working-tree edit to the policy does not change it — the acceptance
	// oracle is pinned outside the agent's reach (§6).
	AcceptanceHash string `json:"acceptanceHash"`
	// ImageDigest pins the project image by sha256: digest (not a mutable tag),
	// captured at create or first verify, so the clean verifier runs the artifact
	// in the same environment it was authored against (§6). Empty until captured.
	ImageDigest string `json:"imageDigest"`
	// Budget is the governance envelope resolved at create (request narrowed
	// against the project ceiling) and stored authoritatively here, so the bounds
	// on a Task cannot drift and no agent can widen its own (§6). Legacy rows
	// written before Sprint 58 carry no budget and read back as DefaultBudget().
	Budget Budget `json:"budget"`
	// Checks are PER-TASK acceptance commands, supplied by a human at create and
	// APPENDED to the project's frozen policy at verify — never replacing it, so a
	// task can only ever raise the bar it is graded against. They exist because
	// `.daedalus/verify.json` is project-level and task-independent: it answers
	// "does this artifact still meet the project's standing bar", and cannot answer
	// "did this task deliver what it promised". These are where the second question
	// gets a machine-checkable answer.
	Checks []string `json:"checks,omitempty"`
	State  State    `json:"state"`
	// ProgrammeID is the shared intent this Task serves, or "" for a Task that
	// serves none. It stores the programme's ID and never its name, so renaming a
	// programme cannot dangle the work that serves it (programme.go).
	ProgrammeID string `json:"programmeId,omitempty"`
	// RefineFrom is the artifact commit the NEXT dispatch starts from, instead of
	// the clean checkout at BaseSHA every other Job gets (#91).
	//
	// It exists because a Job could only ever start from nothing. After a review
	// found four things wrong with otherwise good work, the only routes were to
	// re-run the whole objective from a clean tree — throwing away the code to get
	// the corrections — or to fix it by hand outside the plane. Neither keeps the
	// work and acts on the findings, which is the ordinary thing to want.
	//
	// BaseSHA is deliberately NOT changed by a refine. The Job starts from the
	// artifact and is still GRADED from the base, so the original work stays inside
	// the diff the oracle sees. Moving the base would let an artifact carry itself
	// past the verifier by being declared the new starting point.
	//
	// Consumed and cleared by the dispatch that uses it: a continuation applies to
	// the attempt it was asked for and never silently to the next one.
	RefineFrom string `json:"refineFrom,omitempty"`
	// RefineReview is the review whose findings that dispatch is answering, or ""
	// for a refine with only a note. Kept so the record says the work was corrected
	// after a reading rather than got right second time.
	RefineReview string `json:"refineReview,omitempty"`
	// RefineNote is a human's own instruction for that attempt, carried into the
	// prompt beside any findings.
	RefineNote string `json:"refineNote,omitempty"`
	// Rationale is why this work is worth doing — the answer the record could not
	// give before M20, when a Task carried an objective and nothing else. An
	// objective says WHAT to do; this says what it is FOR, and only one of them is
	// still interesting a year later.
	Rationale string `json:"rationale,omitempty"`
	// RationaleBy is the caller class that authored the rationale, derived from
	// the transport exactly as Proposal.ProposedBy and SteeringEvent.IssuedBy are.
	// It is never supplied by a request — which is what makes "the rationale is
	// the human's own words" a property you can check rather than one you hope
	// for. An agent-drafted reason is visible as an agent's.
	RationaleBy CallerClass `json:"rationaleBy,omitempty"`
	CreatedAt   string      `json:"createdAt"` // ISO 8601 UTC
	UpdatedAt   string      `json:"updatedAt"` // ISO 8601 UTC
}

// Job is one attempt at a Task (§5): a headless runner invocation pinned to a
// base_sha. execution_result records how the run ended; output_snapshot records
// the committed tree (head_sha) captured even on failure as a salvage snapshot.
type Job struct {
	ID              string          `json:"id"` // e.g. "J-1"
	TaskID          string          `json:"taskId"`
	BaseSHA         string          `json:"baseSha"`
	Runner          string          `json:"runner"`
	Budget          int             `json:"budget"`          // wall-clock seconds; 0 = unset
	ExecutionResult ExecutionResult `json:"executionResult"` // how the run ended
	OutputSnapshot  string          `json:"outputSnapshot"`  // committed head_sha, if any
	// LogPath is this Job's own log on the HOST, or "" when none was written.
	// Recorded only once the file exists, so a non-empty value is a promise that
	// there is something to read there (Backlog #77).
	LogPath   string `json:"logPath,omitempty"`
	State     State  `json:"state"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// Artifact is the durable result of a successful Job (§5): a committed tree on a
// dedicated branch, with independent verify/review status the control plane
// owns. Only a `success` execution promotes a snapshot to an Artifact.
type Artifact struct {
	ID      string       `json:"id"` // e.g. "A-1"
	JobID   string       `json:"jobId"`
	BaseSHA string       `json:"baseSha"`
	HeadSHA string       `json:"headSha"`
	Branch  string       `json:"branch"` // e.g. daedalus/T-1/J-1
	Verify  VerifyStatus `json:"verify"`
	Review  ReviewStatus `json:"review"`
	// IntegratedSHA is the commit that actually landed — the artifact REBASED onto
	// the integration target and re-verified in that combined form, which is
	// generally NOT HeadSHA. Empty until the integration transaction completes.
	IntegratedSHA string `json:"integratedSha"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}
