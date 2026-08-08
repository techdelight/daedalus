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
		StatePlanned, StateQueued, StateWorking, StateInputRequired,
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

// IsActive reports whether s is a non-terminal (in-flight) state. Used to
// enforce "one active Task/Job per project".
func IsActive(s State) bool { return validState(s) && !terminalStates[s] }

// legalTransitions is the full transition table the control plane may drive.
// A terminal state is intentionally absent (no outgoing edges). Cancellation
// and expiry are reachable from every active state; failure from the execution
// path.
var legalTransitions = map[State]map[State]bool{
	StatePlanned: {
		StateQueued: true, StateCancelled: true, StateExpired: true, StateFailed: true,
	},
	StateQueued: {
		StateWorking: true, StateCancelled: true, StateExpired: true, StateFailed: true,
	},
	StateWorking: {
		StateCandidate: true, StateInputRequired: true,
		StateCancelled: true, StateExpired: true, StateFailed: true,
	},
	StateInputRequired: {
		StateWorking: true, StateCancelled: true, StateExpired: true, StateFailed: true,
	},
	StateCandidate: {
		StateVerifying: true, StateRejected: true,
		StateCancelled: true, StateExpired: true, StateFailed: true,
	},
	StateVerifying: {
		StateVerified: true, StateRejected: true,
		StateCancelled: true, StateExpired: true, StateFailed: true,
	},
	StateVerified: {
		StateApprovalRequired: true, StateCancelled: true, StateExpired: true,
	},
	StateRejected: {
		// retry (re-queue) or replan (back to planned).
		StateQueued: true, StatePlanned: true,
		StateCancelled: true, StateExpired: true,
	},
	StateApprovalRequired: {
		StateApproved: true, StateRejected: true,
		StateCancelled: true, StateExpired: true,
	},
	StateApproved: {
		StateIntegrated: true, StateCancelled: true,
	},
	// Terminal: StateIntegrated, StateFailed, StateCancelled, StateExpired have
	// no outgoing edges.
}

// workerReachable is the *subset* of transitions a worker-driven request may
// perform. Everything else — most importantly anything into `verified` and the
// whole governance/integration tail — is control-plane-only. This is the
// structural encoding of "verification is not conversational" (§5): a worker
// literally cannot name `verified` as a target.
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
	State       State  `json:"state"`
	CreatedAt   string `json:"createdAt"` // ISO 8601 UTC
	UpdatedAt   string `json:"updatedAt"` // ISO 8601 UTC
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
	State           State           `json:"state"`
	CreatedAt       string          `json:"createdAt"`
	UpdatedAt       string          `json:"updatedAt"`
}

// Artifact is the durable result of a successful Job (§5): a committed tree on a
// dedicated branch, with independent verify/review status the control plane
// owns. Only a `success` execution promotes a snapshot to an Artifact.
type Artifact struct {
	ID        string       `json:"id"` // e.g. "A-1"
	JobID     string       `json:"jobId"`
	BaseSHA   string       `json:"baseSha"`
	HeadSHA   string       `json:"headSha"`
	Branch    string       `json:"branch"` // e.g. daedalus/T-1/J-1
	Verify    VerifyStatus `json:"verify"`
	Review    ReviewStatus `json:"review"`
	CreatedAt string       `json:"createdAt"`
	UpdatedAt string       `json:"updatedAt"`
}
