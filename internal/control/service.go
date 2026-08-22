// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/techdelight/daedalus/internal/registry"
)

// ProjectResolver maps a registry project name to its on-disk directory. The
// control plane resolves projects only through this trusted seam — never a
// caller-supplied path — so a task target cannot be spoofed (§4). The real impl
// wraps the registry; tests inject a map.
type ProjectResolver interface {
	ProjectDir(name string) (string, error)
}

// ProjectLister is an optional extension to ProjectResolver: a resolver that can
// enumerate the projects it knows about. The registry-backed resolver implements
// it; a test map may not, so callers must type-assert and degrade.
//
// It exists so the shared-queue view is derived from the REGISTRY rather than
// from the tasks table — a queue shared by a project that has no tasks yet would
// otherwise look unshared, which is exactly when an operator most needs to know
// two projects will serialize against each other.
type ProjectLister interface {
	ProjectNames() ([]string, error)
}

// SessionObserver reports whether the coordinator currently has a live session
// for a project. It is the coordinator dependency behind an interface
// (host-tested with a fake); the real impl wraps a coordinator client.
//
// PROJECT-LEVEL LIVENESS IS THE WRONG QUESTION, and Sprint 62 established that it
// always was. A control-plane Job does not run under its project's name: the
// runner launches `daedalus daedalus-job-<jobID> …`, and the coordinator keys
// sessions by that name, so a Job's session is `daedalus-job-J-7` while this asks
// about `app`. The answer was therefore only accidentally related to the Job being
// judged — false while a Job ran happily (unless a human happened to have an
// interactive session open on the same project), true for every Job of a project
// somebody was working in. Which way it erred depended on unrelated human
// activity, which is no basis for deciding whether to destroy work.
//
// It survives only as a fallback signal; JobSessionObserver is the real question.
type SessionObserver interface {
	HasSession(project string) (bool, error)
}

// JobSessionObserver reports liveness for ONE Job — the question reconcile
// actually needs answered once several Jobs can share a project.
//
// The key already existed: CoordinatorRunner registers and launches each Job
// under JobProjectName(jobID), so the coordinator already tracks the session under
// exactly that name. No coordinator change was needed; the control plane was
// simply asking about the wrong string.
//
// Optional: an observer that does not implement it falls back to the heuristic in
// reconcile.go, which is honestly labelled as one.
type JobSessionObserver interface {
	HasSessionForJob(jobID string) (bool, error)
}

// CreateTaskRequest is the input to Service.CreateTask (and the daemon's POST
// /tasks body).
type CreateTaskRequest struct {
	Project    string `json:"project"`
	Objective  string `json:"objective"`
	Acceptance string `json:"acceptance,omitempty"`
	// Checks are per-task acceptance commands, APPENDED to the project's frozen
	// policy at verify — they can only raise the bar, never lower it. Human
	// callers only (resolveTaskChecks). This is where "did this task deliver what
	// it promised" gets a machine-checkable answer, since `.daedalus/verify.json`
	// is project-level and cannot know what any one Task set out to do.
	Checks []string `json:"checks,omitempty"`
	// Budget optionally narrows the project's ceiling for this task. Unset axes
	// inherit the ceiling; an axis that *widens* it is refused with
	// ReasonOverBudget (§6 — "budget too high → REJECTED"), and a NEGATIVE axis is
	// refused with ReasonInvalidBudget (it would read as unbounded — see
	// Budget.invalidAxis). Raising a ceiling means editing the host-side policy
	// file; no request over control.sock can do it.
	Budget *Budget `json:"budget,omitempty"`
	// Programme is the shared intent this Task serves — an id or a name, resolved
	// by the plane (programme.go). Optional: a Task that serves no programme is a
	// normal Task, and requiring one would make people invent programmes to
	// satisfy a field.
	Programme string `json:"programme,omitempty"`
	// Rationale is why this work is worth doing. WHO said so is not in this
	// struct and cannot be: it is derived from the transport, so an agent-drafted
	// reason can never arrive labelled as the operator's (M20).
	Rationale string `json:"rationale,omitempty"`
}

// RetryRequest is the input to Service.RetryTask (and POST /tasks/{id}/retry).
type RetryRequest struct {
	// Rebase re-pins the task to the project's current tip and re-freezes the
	// acceptance policy there before the new attempt — the documented remedy for a
	// `stale_base` rejection. Off by default: it is deliberately an explicit human
	// act, because it re-freezes the acceptance oracle at a newer commit.
	Rebase bool `json:"rebase,omitempty"`
}

// ReplanRequest is the input to Service.ReplanTask (and POST /tasks/{id}/replan).
type ReplanRequest struct {
	Objective string `json:"objective"`
	// Rebase re-pins the task to the project's current target and re-freezes the
	// acceptance policy there, before the new objective is dispatched (#84).
	//
	// It exists because replan and retry each did half of what a wrong Task needs:
	// replan corrected the instruction and left the Task pinned to the tree it was
	// created on, retry moved the base and carried the old instruction, and neither
	// could be chained because both refuse from any state but `rejected`. A Task
	// that was asked the wrong question against a tree that has since moved on had
	// no door at all — the advice was to abandon it and create a new one, which
	// works and quietly discards the history, the reviews, and every recorded
	// reason the first attempt was wrong.
	//
	// Opt-in by name for the same reason it is on retry: adopting a newer oracle is
	// a governance act, not a detail of correcting an objective.
	Rebase bool `json:"rebase,omitempty"`
}

// RetryResult reports a retry attempt: the dispatch outcome plus the governance
// bookkeeping a caller needs to reason about the budget.
type RetryResult struct {
	Dispatch    DispatchResult `json:"dispatch"`
	Attempt     int            `json:"attempt"`     // this attempt's ordinal (1-based)
	Attempts    int            `json:"attempts"`    // attempts used after this one
	Rebased     bool           `json:"rebased"`     // the task was re-pinned to a new base
	BaseSHA     string         `json:"baseSha"`     // the base this attempt ran from
	MaxAttempts int            `json:"maxAttempts"` // 0 = unbounded
}

// JobView is a Job plus its artifacts, for status rendering.
type JobView struct {
	Job       Job        `json:"job"`
	Artifacts []Artifact `json:"artifacts"`
}

// StatusView is a Task plus its jobs (each with artifacts), and where it stands
// with the scheduler.
type StatusView struct {
	Task Task      `json:"task"`
	Jobs []JobView `json:"jobs"`
	// Dependencies is the Task's position in the cross-project graph: what it is
	// waiting on, and what is waiting on it.
	Dependencies DependencyView `json:"dependencies"`
	// Scheduling describes what the plane is doing with this Task right now: it is
	// what makes a Task that is QUEUED FOR CAPACITY visibly different from one that
	// is running, which are otherwise both just "not finished".
	Scheduling TaskScheduling `json:"scheduling"`
	// Reviews are the judgements passed on this Task's artifacts, oldest first
	// (M20). They ride on the status rather than living behind a route of their
	// own because a review is EVIDENCE FOR A DECISION, and the decision is taken
	// while looking at this: a finding one fetch away from the approval gate is a
	// finding nobody reads.
	Reviews []Review `json:"reviews,omitempty"`
}

// TaskScheduling is a Task's position with respect to concurrency.
type TaskScheduling struct {
	// Running is true when this Task has a Job actually executing.
	Running bool `json:"running"`
	// QueuedForCapacity is true when a dispatch was refused for capacity and the
	// Task is holding a place in line.
	QueuedForCapacity bool `json:"queuedForCapacity"`
	// QueuePosition is 1-based among waiting Tasks, 0 when not waiting.
	QueuePosition int `json:"queuePosition,omitempty"`
	// ProjectRunning and GlobalRunning are the counts the next admission decision
	// will be made against.
	ProjectRunning int `json:"projectRunning"`
	GlobalRunning  int `json:"globalRunning"`
	// Limits in force.
	Limits SchedulerLimits `json:"limits"`
}

// DispatchResult is the terminal-of-this-attempt view returned by dispatch.
type DispatchResult struct {
	Job      Job       `json:"job"`
	Artifact *Artifact `json:"artifact,omitempty"`
}

// VerifyResult is the outcome of a plane-owned verify pass over a candidate Job.
type VerifyResult struct {
	Job      Job       `json:"job"`
	Task     Task      `json:"task"`
	Artifact *Artifact `json:"artifact,omitempty"`
	// GateTouched now means the frozen oracle could NOT be restored, so nothing
	// was graded. It no longer means "the Job edited an acceptance file" — that is
	// TouchedFiles, which is reported on a pass as well as a rejection and costs
	// the Job nothing, because the verifier grades against the restored oracle.
	GateTouched  bool     `json:"gateTouched"`
	TouchedFiles []string `json:"touchedFiles,omitempty"`
	// Reason is the machine-readable "why" of a negative verdict (stale_base,
	// null_agent_floor, policy_drift, integrity_gate, verify_failed) and is empty
	// on a pass — so a client never has to parse Detail to know what happened.
	Reason RejectionReason `json:"reason,omitempty"`
	// Waived reports that a failing result was carried forward by a human. The
	// artifact is NOT verified and never will be; this says a person took it on.
	Waived         bool   `json:"waived,omitempty"`
	VerifierCalled bool   `json:"verifierCalled"` // false when the gate short-circuited
	Verified       bool   `json:"verified"`       // final verdict: reached `verified`
	Detail         string `json:"detail"`
	// PreExisting names project checks that failed against BOTH the artifact and
	// the Job's base. They did not affect the verdict — they were already failing
	// when the Job was handed the repository — but they are somebody's problem, and
	// a pass that quietly swallowed them would be how a repository rots.
	PreExisting []string `json:"preExisting,omitempty"`
}

// TaskAPI is the surface the CLI drives and the daemon serves. Both the
// in-process Service and the over-the-socket Client implement it, so the CLI is
// identical whether it runs the logic directly (tests) or via the daemon.
type TaskAPI interface {
	CreateTask(req CreateTaskRequest) (Task, error)
	ListTasks() ([]Task, error)
	TaskStatus(id string) (StatusView, error)
	CancelTask(id string) (Task, error)
	DispatchTask(id string) (DispatchResult, error)
	VerifyTask(id string, req VerifyRequest) (VerifyResult, error)
	RetryTask(id string, req RetryRequest) (RetryResult, error)
	ReverifyTask(id string, req ReverifyRequest) (ReverifyResult, error)
	AmendTaskChecks(id string, req AmendChecksRequest) (Task, error)
	ReplanTask(id string, req ReplanRequest) (Task, error)
	ReviewTask(id string) (ReviewResult, error)
	ApproveTask(id, note string) (Task, error)
	RejectApproval(id, note string) (Task, error)
	IntegrateTask(id string, req IntegrateRequest) (IntegrationResult, error)
	PendingApprovals() ([]Task, error)
	ProjectTargets() ([]TargetView, error)
	PlaneStatus() (PlaneStatus, error)
	AddDependency(taskID, dependsOn string) (DependencyEdge, error)
	TaskDependencies(taskID string) (DependencyView, error)
	ProgrammeBoard() (BoardView, error)
	// Programmes (M20): the shared intent Tasks serve. Reads are free to any
	// caller; forming, amending and dissolving are proposals for an agent.
	ListProgrammes() ([]Programme, error)
	GetProgramme(id string) (Programme, error)
	ProgrammeStatusFor(id string) (ProgrammeStatus, error)
	CreateProgramme(req ProgrammeRequest) (Programme, error)
	UpdateProgramme(id string, req ProgrammeRequest) (Programme, error)
	DeleteProgramme(id string) error
	SteerJob(jobID, instruction string) (SteeringEvent, error)
	CancelSteering(steerID string) (SteeringEvent, error)
	JobSteering(jobID string) ([]SteeringEvent, error)
	SyncTarget(project string) (Target, error)
	ListProposals(state ProposalState) ([]Proposal, error)
	ResolveProposal(id string, confirm bool, note string) (Proposal, error)
	// TaskEvents is READ-ONLY and is the only event-facing operation on this
	// interface — there is deliberately no append/amend/delete counterpart, which
	// is what "immutable through the API" means in practice (§6).
	TaskEvents(id string) ([]Event, error)
}

// Service is the host-side control plane: the single owner of the store plus the
// worktree, runner, project-resolution, and session-observation seams. All
// business logic lives here so it is host-tested with fakes; the daemon (daemon.go)
// is a thin HTTP translation over it.
type Service struct {
	store     *Store
	projects  ProjectResolver
	worktrees *WorktreeManager
	runner    AgentRunner
	verifier  VerifyRunner    // may be nil until a candidate is verified
	digester  ImageDigester   // may be nil (digest pinning then skipped)
	sessions  SessionObserver // may be nil (reconcile then skips session checks)
	budgets   PolicySource    // may be nil (then DefaultBudget(), no approval)
	reviewer  ReviewRunner    // may be nil (then review is not a gate)
	sched     *Scheduler      // admission control; never nil after NewService
	// steerTimeout bounds how long a steering handoff may take before the plane
	// records it undeliverable. A field rather than a bare const so a test can
	// exercise the "the runner never answered" path in milliseconds instead of
	// adding the real timeout to every suite run.
	steerTimeout time.Duration

	// dataDir is the host directory the plane's own files live under. Needed only
	// to site per-job logs (#77); optional, and an empty one simply means Jobs run
	// without a log of their own, exactly as they did before.
	dataDir string

	// now is the clock the reconcile heuristic measures against. Injected so a
	// test can make "long past its budget" deterministic rather than slow.
	now func() time.Time

	// mu serialises the DB bookkeeping of Dispatch, Verify, Cancel and Reconcile
	// (V1 has a single SQLite writer conn). It is deliberately NOT held across the
	// long calls — runner.Run and verifier.Verify — because those can legitimately
	// run for the whole wall-clock budget, and holding it there made `task cancel`
	// and the reconcile loop inert for up to an hour.
	//
	// Releasing it means two things must be handled explicitly rather than
	// implicitly by the lock:
	//   - two operations racing on one Task → the `inflight` set below;
	//   - reconcile observing a Job that is running *in this process* and mistaking
	//     it for a crashed one → also the `inflight` set.
	mu sync.Mutex

	// inflight tracks the long operations this process is currently running, keyed
	// by task id. Guarded by mu. It is the explicit form of what the over-held
	// lock used to provide by accident.
	inflight map[string]inflightOp
}

// inflightOp describes a long operation in progress in this process.
type inflightOp struct {
	kind    string // "dispatch" | "verify"
	jobID   string // the job being run/verified ("" before it exists)
	project string
}

// NewService wires a Service. runner/worktrees are required for Dispatch;
// projects for CreateTask; verifier for VerifyTask; sessions may be nil.
func NewService(store *Store, projects ProjectResolver, worktrees *WorktreeManager, runner AgentRunner, verifier VerifyRunner, sessions SessionObserver) *Service {
	return &Service{
		store: store, projects: projects, worktrees: worktrees,
		runner: runner, verifier: verifier, sessions: sessions,
		inflight:     map[string]inflightOp{},
		sched:        NewScheduler(DefaultSchedulerLimits()),
		now:          func() time.Time { return time.Now().UTC() },
		steerTimeout: steeringDeliveryTimeout,
	}
}

// beginOp claims a task for a long operation. s.mu must be held. It refuses
// rather than blocks: a caller that has to wait an hour for a lock is
// indistinguishable from a hung daemon, so the plane says no immediately and
// says why.
//
// In practice the state guards usually fire first (a task with a dispatch in
// flight is already `working`, so a second dispatch is refused on state before it
// gets here). This claim is what reconcile reads to tell live work from crashed
// work, and it is the backstop if a state guard is ever loosened — it is not the
// primary refusal path, and it does not pretend to be.
// The claimWitness parameter is unused at runtime and load-bearing at compile
// time: it is what stops this being callable from anywhere but withClaim (see
// claim.go for the honest scope of that guarantee).
func (s *Service) beginOp(taskID string, op inflightOp, _ claimWitness) error {
	if existing, busy := s.inflight[taskID]; busy {
		return &RejectionError{
			Reason: ReasonOperationInFlight,
			Message: fmt.Sprintf("task %s already has a %s in progress (job %s)",
				taskID, existing.kind, orNone(existing.jobID)),
			Entity: taskID,
		}
	}
	s.inflight[taskID] = op
	return nil
}

// jobIsInflight reports whether a job is being run or verified by this process
// right now. s.mu must be held. Reconcile uses it so it never "repairs" live work.
func (s *Service) jobIsInflight(taskID, jobID string) bool {
	op, ok := s.inflight[taskID]
	return ok && op.jobID == jobID
}

func orNone(s string) string {
	if s == "" {
		return "none yet"
	}
	return s
}

// Store exposes the underlying store (daemon reconcile ticker, tests).
func (s *Service) Store() *Store { return s.store }

// SetImageDigester installs the image-digest seam (Docker-dependent) after
// construction, so NewService callers that don't pin images (host tests) stay
// unchanged. Nil disables digest pinning.
// SetDataDir tells the service where the plane's host-side files live, which is
// what lets it give each Job a log of its own (#77). Unset, Jobs run exactly as
// before — output to the daemon's stdout only, and no log_path on the row.
func (s *Service) SetDataDir(dir string) { s.dataDir = dir }

func (s *Service) SetImageDigester(d ImageDigester) { s.digester = d }

// governanceMetaFor is the event annotation for an explicitly-requested
// governance act (retry / replan / rebase), attributed to its caller.
//
// The actor is decided at the SERVICE boundary from the caller identity the
// daemon derived from the transport (caller.go) — never from anything in the
// request body, and never invented by the store. This is the seam Sprint 58
// promised and Sprint 60 filled: it is now a real per-request value.
func governanceMetaFor(c Caller) EventMeta {
	return EventMeta{Kind: EventGovernance, Actor: c.Actor()}
}

// SetClock injects the clock the reconcile heuristic uses (tests).
func (s *Service) SetClock(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

// Scheduler exposes the admission scheduler (daemon wiring, status views, tests).
func (s *Service) Scheduler() *Scheduler { return s.sched }

// SetSchedulerLimits applies the host-side concurrency limits.
func (s *Service) SetSchedulerLimits(l SchedulerLimits) { s.sched.SetLimits(l) }

// SetPolicySource installs the per-project governance policy source — budget
// ceilings and the approval requirement (the daemon passes FileBudgetPolicy;
// tests pass a static one). Nil means DefaultBudget() and no approval.
func (s *Service) SetPolicySource(p PolicySource) { s.budgets = p }

// SetBudgetSource is SetPolicySource under its Sprint-58 name.
//
// Deprecated: use SetPolicySource; the seam now carries the approval policy too.
func (s *Service) SetBudgetSource(p PolicySource) { s.SetPolicySource(p) }

// budgetCeiling resolves the ceiling for a project: the configured source, or
// the built-in default when none is installed.
// The result is sanitized: PolicySource is an exported, injectable interface, so
// the ceiling can arrive from a caller that never went through LoadBudgetPolicy
// (StaticBudget, or any third implementation). A negative ceiling would make
// exceededBy's `permitted > 0` guard permit *everything*, turning the ceiling
// into a rubber stamp — so the untrusted-input rule applies to the ceiling too,
// not just the request.
func (s *Service) budgetCeiling(project string) Budget {
	if s.budgets == nil {
		return DefaultBudget()
	}
	return s.budgets.BudgetFor(project).withDefaults(DefaultBudget()).sanitized(DefaultBudget())
}

// refuse records a policy refusal in the event log and returns the typed error.
// Refusals change no state — that is the point: the plane declined to act, and
// the only trace is the log entry plus the RejectionError the caller sees.
func (s *Service) refuse(entityType, entityID string, kind string, reason RejectionReason, msg string) error {
	if err := s.store.LogDecision(entityType, entityID,
		EventMeta{Kind: kind, Reason: reason, Actor: ActorPlane}, msg); err != nil {
		log.Printf("control: logging %s refusal for %s: %v", reason, entityID, err)
	}
	return &RejectionError{Reason: reason, Message: msg, Entity: entityID}
}

// CreateTask resolves the project through the trusted registry, enforces the
// Git-native prerequisite (captures base_sha from HEAD), and inserts a planned
// Task — rejecting a second active task per project (store invariant).
func (s *Service) CreateTask(req CreateTaskRequest) (Task, error) { return s.createTask(Human(), req) }

// createTask is CreateTask with an explicit caller identity.
func (s *Service) createTask(caller Caller, req CreateTaskRequest) (Task, error) {
	if req.Project == "" {
		return Task{}, fmt.Errorf("control: project is required")
	}
	if req.Objective == "" {
		return Task{}, fmt.Errorf("control: objective is required")
	}
	checks, err := resolveTaskChecks(caller, req.Checks)
	if err != nil {
		return Task{}, err
	}
	dir, err := s.projects.ProjectDir(req.Project)
	if err != nil {
		return Task{}, err
	}
	// The base is the PLANE-OWNED integration target, never the checkout's HEAD
	// (target.go). This is what makes the acceptance oracle unreachable from the
	// repository: an agent may rewrite every branch it likes — by cherry-pick,
	// commit-tree, or anything else — and none of them is the commit this Task is
	// based on or graded against. A project with no target yet adopts its HEAD
	// once, before any Job for it has ever run.
	//
	// THIS IS THE ONLY PLACE ADOPTION HAPPENS, and it is a command run before the
	// read rather than a side effect of it (target.go). Task creation is the one
	// moment where "this repository has no target" is an ordinary state rather than
	// a fault; everywhere else a missing target means something is wrong, and
	// inventing one from the worker-writable checkout HEAD would be the worst
	// available response.
	if err := s.ensureTarget(req.Project); err != nil {
		return Task{}, err
	}
	// Re-read rather than have ensureTarget hand the row back: a human resync could
	// have moved the target between the two calls, and this Task should freeze at
	// whatever the plane holds NOW. Both values are plane-owned, so either is safe —
	// the later one is simply the more truthful.
	target, err := s.Target(req.Project)
	if err != nil {
		return Task{}, err
	}
	baseSHA := target.SHA
	// Freeze the acceptance policy as it stands at base_sha: read the committed
	// .daedalus/verify.json at that sha (or the default) and store its stable
	// hash. Because it is read from the commit — not the working tree — a later
	// edit to the policy cannot change this frozen value (§6).
	policy, err := ReadAcceptancePolicyAt(dir, baseSHA)
	if err != nil {
		return Task{}, err
	}
	// Resolve the governance envelope BEFORE inserting anything: a request that
	// tries to widen the project's ceiling is refused outright, so an over-budget
	// task never exists (§6 — "budget too high → REJECTED").
	budget, err := s.resolveBudget(req)
	if err != nil {
		return Task{}, err
	}
	programmeID, err := s.resolveProgramme(req.Programme)
	if err != nil {
		return Task{}, err
	}
	// The rationale's author is the CALLER CLASS, taken from the transport and
	// never from the request — the same rule as proposals.proposed_by and
	// steering.issued_by. Recorded rather than refused: an agent may well draft a
	// good reason, and the useful property is that it is visibly the agent's
	// rather than silently the operator's. Stamped only when there is something to
	// attribute, so an unattributed Task reads as unattributed instead of as
	// "authored by a human who wrote nothing".
	rationaleBy := CallerClass("")
	if strings.TrimSpace(req.Rationale) != "" {
		rationaleBy = caller.Class
	}
	t, err := s.store.CreateTask(NewTask{
		Project: req.Project, Objective: req.Objective, AcceptanceRef: req.Acceptance,
		BaseSHA: baseSHA, AcceptanceHash: policy.Hash(), Checks: checks, Budget: budget,
		ProgrammeID: programmeID, Rationale: strings.TrimSpace(req.Rationale), RationaleBy: rationaleBy,
	}, StatePlanned)
	if err != nil {
		return Task{}, err
	}
	// Best-effort image-digest pin at create. If no digester (host tests) or the
	// image is not built yet, the digest stays empty and is captured lazily at
	// first verify instead.
	if t2, ok := s.captureDigest(t); ok {
		t = t2
	}
	return t, nil
}

// maxTaskChecks bounds how many per-task commands one Task may carry. Not a
// security boundary — the human-only rule is — just a guard against a pasted
// script arriving as a thousand one-line checks.
const maxTaskChecks = 16

// resolveTaskChecks validates the per-task acceptance commands and decides who
// may supply them.
//
// HUMAN CALLERS ONLY, and the reasoning is worth stating because the rule looks
// stricter than it needs to be. Per-task checks are APPENDED to the project's
// frozen policy and run AFTER it, so an agent-supplied check could never weaken
// the oracle — the worst it could do is add a command that passes trivially. But
// a check is a command executed inside the verifier, and the verifier is the one
// environment in this system whose contents are supposed to be decided entirely
// before the work begins. Letting the party being graded put commands in there is
// a door worth leaving shut until someone has a use for opening it; every other
// consequential capability in this design started human-only and stayed that way
// unless it earned otherwise (§6, tiered authority).
//
// An agent that wants a task graded a particular way can say so in the objective
// and let a human write the check — which is the same shape as every other
// proposal in the system.
func resolveTaskChecks(caller Caller, raw []string) ([]string, error) {
	var checks []string
	var multiline string
	for _, c := range raw {
		c = strings.TrimSpace(c)
		if c == "" {
			continue // a stray empty --check is a typo, not an instruction
		}
		// Recorded, not refused yet: the agent refusal below must answer first, so
		// that a caller who may not set checks at all learns nothing about their
		// shape from the order of the error messages.
		if multiline == "" && strings.ContainsAny(c, "\n\r") {
			multiline = c
		}
		checks = append(checks, c)
	}
	if len(checks) == 0 {
		return nil, nil
	}
	if caller.IsAgent() {
		return nil, &RejectionError{
			Reason: ReasonForbidden,
			Message: "per-task acceptance checks may only be set by a human caller: " +
				"they are commands run inside the verifier, and the party being graded does not choose them",
		}
	}
	// A check containing a newline is TWO commands, and only the last one's exit
	// status becomes the verdict.
	//
	// Measured, 2026-08-18: a check pasted with a line break between the pattern
	// and the path became `grep -qE '<pattern>'` (no file, so it read empty stdin
	// and matched nothing) followed by `internal/web/static/style.css`, which the
	// shell tried to EXECUTE — exit 126, "Permission denied". The task was rejected
	// without grep ever reading the file it named, and the artifact was correct.
	//
	// The paste accident is the cheap half. The expensive half is that multi-line
	// checks are silently wrong even when nobody makes a mistake:
	//
	//	grep -q FORBIDDEN file
	//	! grep -q REQUIRED file
	//
	// reads as two assertions and enforces one, because the first line's failure is
	// discarded. A check that appears to assert more than it does is worse than no
	// check, since it produces a confident pass. Nothing needs multi-line checks —
	// `--check` is repeatable and bounded at maxTaskChecks — so the whole class is
	// refused rather than made to work.
	//
	// Note the trim above already removes a trailing newline from a paste, so only
	// an INTERIOR break reaches here: this refuses the dangerous case without
	// rejecting a check somebody simply copied with a stray line ending.
	if multiline != "" {
		return nil, &RejectionError{
			Reason: ReasonInvalidCheck,
			Message: fmt.Sprintf("a check may not contain a line break — %q would run as two commands, "+
				"and only the last one's exit status would be the verdict. Pass separate --check flags "+
				"instead (up to %d)", multiline, maxTaskChecks),
		}
	}
	if len(checks) > maxTaskChecks {
		return nil, &RejectionError{
			Reason:  ReasonInvalidCheck,
			Message: fmt.Sprintf("%d checks requested, limit is %d", len(checks), maxTaskChecks),
		}
	}
	return checks, nil
}

// resolveBudget layers a create request's budget over the project's ceiling. A
// request may only narrow: any axis that widens the ceiling is refused with
// ReasonOverBudget rather than silently clamped, because a caller that asked for
// more than it may have should be told so — a governed plane says no out loud.
func (s *Service) resolveBudget(req CreateTaskRequest) (Budget, error) {
	ceiling := s.budgetCeiling(req.Project)
	if req.Budget == nil {
		return ceiling, nil
	}
	// Validate BEFORE comparing against the ceiling. A negative axis reads as
	// "unbounded" at every enforcement site, so it is not merely a big ask — it is
	// invalid input, and it must be rejected HERE, in the service, not in the CLI:
	// the CLI is a convenience, the socket API is the security boundary, and
	// Sprint 60 puts an agent on it.
	if axis, bad := req.Budget.invalidAxis(); bad {
		return Budget{}, s.refuse("project", req.Project, EventBudget, ReasonInvalidBudget, fmt.Sprintf(
			"budget %s is negative; 0 means unbounded and negative is not a budget", axis))
	}
	// The concurrency axis needs its ceiling from the SCHEDULER, not the budget.
	// Since Sprint 61 the budget's own concurrency default is unset (= unbounded),
	// so exceededBy could never refuse a concurrency ask: a request for 1000 would
	// be stored and echoed back by `task status` as though it were the limit,
	// while the real bound was the operator's per-project setting. Every other
	// axis refuses an over-ask out loud (Sprint 58's doctrine); this one now does
	// too, rather than accepting a number that means nothing.
	if asked := req.Budget.Concurrency; asked > 0 {
		if perProject := s.sched.Limits().PerProject; perProject > 0 && asked > perProject {
			return Budget{}, s.refuse("project", req.Project, EventBudget, ReasonOverBudget, fmt.Sprintf(
				"requested concurrency %d exceeds the project limit of %d; raise concurrency.perProject in the host-side policy, not in the request",
				asked, perProject))
		}
	}
	if axis, over := ceiling.exceededBy(*req.Budget); over {
		// Recorded against the PROJECT, not a task: the refusal happened before any
		// task existed, and an event with an empty entity id would be unqueryable.
		return Budget{}, s.refuse("project", req.Project, EventBudget, ReasonOverBudget, fmt.Sprintf(
			"requested %s exceeds project %q ceiling (%s); raise it in the host-side budget policy, not in the request",
			axis, req.Project, ceiling))
	}
	return req.Budget.withDefaults(ceiling), nil
}

// captureDigest records the project image's sha256 digest on the task if a
// digester is configured and returns a non-empty value. Returns (updated, true)
// when it changed the task. Best-effort: any error is swallowed (empty digest).
func (s *Service) captureDigest(t Task) (Task, bool) {
	if s.digester == nil || t.ImageDigest != "" {
		return t, false
	}
	digest, err := s.digester.Digest(t.Project)
	if err != nil || digest == "" {
		return t, false
	}
	updated, err := s.store.SetTaskImageDigest(t.ID, digest)
	if err != nil {
		return t, false
	}
	return updated, true
}

// ListTasks returns all tasks.
func (s *Service) ListTasks() ([]Task, error) { return s.store.ListTasks() }

// TaskStatus returns a task with its jobs and per-job artifacts.
func (s *Service) TaskStatus(id string) (StatusView, error) {
	t, err := s.store.GetTask(id)
	if err != nil {
		return StatusView{}, err
	}
	jobs, err := s.store.ListJobsForTask(id)
	if err != nil {
		return StatusView{}, err
	}
	view := StatusView{Task: t, Scheduling: s.schedulingFor(t)}
	// Best-effort: a status that cannot list reviews is still a useful status, and
	// failing the whole call over the evidence would be worse than showing none.
	if reviews, err := s.store.ReviewsForTask(id); err == nil {
		view.Reviews = reviews
	}
	if deps, err := s.TaskDependencies(id); err == nil {
		view.Dependencies = deps
	}
	for _, j := range jobs {
		arts, err := s.store.ListArtifactsForJob(j.ID)
		if err != nil {
			return StatusView{}, err
		}
		view.Jobs = append(view.Jobs, JobView{Job: j, Artifacts: arts})
		if IsRunningState(j.State) {
			view.Scheduling.Running = true
		}
	}
	return view, nil
}

// schedulingFor describes a Task's standing with the scheduler.
func (s *Service) schedulingFor(t Task) TaskScheduling {
	sched := TaskScheduling{Limits: s.sched.Limits()}
	if n, err := s.store.CountRunningJobsForProject(t.Project); err == nil {
		sched.ProjectRunning = n
	}
	if n, err := s.store.CountRunningJobs(); err == nil {
		sched.GlobalRunning = n
	}
	for i, id := range s.sched.Waiting() {
		if id == t.ID {
			sched.QueuedForCapacity = true
			sched.QueuePosition = i + 1
			break
		}
	}
	return sched
}

// PlaneStatus is the plane-wide concurrency picture behind `daedalus task list`.
type PlaneStatus struct {
	Limits         SchedulerLimits `json:"limits"`
	GlobalRunning  int             `json:"globalRunning"`
	ProjectRunning map[string]int  `json:"projectRunning"`
	Waiting        []string        `json:"waiting"`
	// ProgrammeRunning and ProgrammeWaiting are the same two numbers per
	// PROGRAMME (M22), keyed by programme id.
	//
	// Reporting only. The scheduler admits on the global and per-project limits and
	// has never heard of a programme, and this does not change that — it answers
	// "which shared intent is the machine actually spending itself on", which
	// nothing could answer before. Making it a scheduling INPUT waits on backlog
	// #70: `waiting` is an in-memory map a restart erases, and fairness built over
	// something that forgets is worse than no fairness, because it looks like a
	// guarantee.
	ProgrammeRunning map[string]int `json:"programmeRunning,omitempty"`
	ProgrammeWaiting map[string]int `json:"programmeWaiting,omitempty"`
}

// PlaneStatus reports what is actually running, per project and globally.
func (s *Service) PlaneStatus() (PlaneStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := PlaneStatus{Limits: s.sched.Limits(), ProjectRunning: map[string]int{}, Waiting: s.sched.Waiting()}
	global, err := s.store.CountRunningJobs()
	if err != nil {
		return PlaneStatus{}, err
	}
	out.GlobalRunning = global
	tasks, err := s.store.ListTasks()
	if err != nil {
		return PlaneStatus{}, err
	}
	seen := map[string]bool{}
	for _, t := range tasks {
		if seen[t.Project] {
			continue
		}
		seen[t.Project] = true
		if n, err := s.store.CountRunningJobsForProject(t.Project); err == nil && n > 0 {
			out.ProjectRunning[t.Project] = n
		}
	}
	// Per programme. A failure here costs the two extra maps and never the
	// answer: "what is running" must not stop being answerable because a
	// reporting roll-up could not be computed.
	if byProg, err := s.store.CountRunningJobsByProgramme(); err == nil && len(byProg) > 0 {
		out.ProgrammeRunning = byProg
	}
	if len(out.Waiting) > 0 {
		waiting := map[string]int{}
		for _, id := range out.Waiting {
			if t, err := s.store.GetTask(id); err == nil && t.ProgrammeID != "" {
				waiting[t.ProgrammeID]++
			}
		}
		if len(waiting) > 0 {
			out.ProgrammeWaiting = waiting
		}
	}
	return out, nil
}

// CancelTask cancels a task and any non-terminal jobs, reclaiming their
// worktrees. The task transition is the authority; job/worktree cleanup is
// best-effort follow-through.
func (s *Service) CancelTask(id string) (Task, error) { return s.cancelTask(Human(), id) }

// cancelTask is CancelTask with an explicit caller identity.
func (s *Service) cancelTask(caller Caller, id string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, err := s.store.GetTask(id)
	if err != nil {
		return Task{}, err
	}
	repoDir, _ := s.projects.ProjectDir(t.Project) // best-effort for cleanup
	jobs, _ := s.store.ListJobsForTask(id)
	for _, j := range jobs {
		if IsActive(j.State) {
			if _, err := s.store.TransitionJobWith(j.ID, StateCancelled, false,
				EventMeta{Actor: ActorHuman}, "task cancelled"); err != nil {
				log.Printf("control: cancel job %s: %v", j.ID, err)
			}
			if s.worktrees != nil {
				_ = s.worktrees.Remove(repoDir, j.ID)
			}
		}
	}
	// Drop any place in the scheduler's queue: a cancelled Task must not keep
	// blocking younger ones by holding the oldest ticket forever.
	s.sched.Forget(id)
	cancelled, err := s.store.TransitionTaskWith(id, StateCancelled, false,
		EventMeta{Actor: caller.Actor()}, "cancelled")
	if err != nil {
		return Task{}, err
	}
	// Cancellation is a decision that this work will not happen, so its dependents
	// can never become runnable. Leaving them blocked forever is the stranding this
	// avoids; the decision propagates transitively instead.
	if propagated := s.cancelDependentsOf(id); len(propagated) > 0 {
		log.Printf("control: cancelling %s also cancelled its dependents %v", id, propagated)
	}
	return cancelled, nil
}

// DispatchTask runs one headless Job attempt for a task: create the Job, add its
// isolated worktree, run the agent (process exit is the boundary), capture the
// tree as output_snapshot (even on failure), then classify — only ExecSuccess
// promotes to a candidate Artifact; failure/timeout/cancel are terminal and
// reclaim the worktree.
func (s *Service) DispatchTask(id string) (DispatchResult, error) {
	// One lock for the whole operation, with the (long) agent run released from it
	// by unlockedDuring inside runDispatch. `defer s.mu.Unlock()` rather than a
	// bare pair: prepareDispatch touches the DB, shells out to git and reads
	// files, and a panic in any of that must not leave the plane deadlocked —
	// net/http recovers a handler panic, so a deadlocked daemon would stay up and
	// simply stop answering, the worst available failure mode.
	s.mu.Lock()
	defer s.mu.Unlock()

	prep, err := s.prepareDispatch(id)
	if err != nil {
		return DispatchResult{}, err
	}
	return s.runClaimedDispatch(prep)
}

// runClaimedDispatch runs a prepared dispatch under an in-flight claim. s.mu must
// be held; it is held again on return.
func (s *Service) runClaimedDispatch(prep dispatchPrep) (DispatchResult, error) {
	var res DispatchResult
	err := s.withClaim(prep.task.ID,
		inflightOp{kind: "dispatch", jobID: prep.job.ID, project: prep.task.Project},
		func() error {
			var runErr error
			res, runErr = s.runDispatch(prep)
			return runErr
		})
	return res, err
}

// dispatchPrep is everything the run phase needs, resolved while s.mu was held.
type dispatchPrep struct {
	task     Task
	job      Job
	repoDir  string
	worktree string
}

// prepareDispatch does the whole locked half of a dispatch: guards, budget,
// state transitions, the Job row, and the isolated worktree. s.mu MUST be held
// throughout, which is what keeps two dispatches from interleaving here — it
// takes no claim of its own, so it has nothing to leak.
func (s *Service) prepareDispatch(id string) (dispatchPrep, error) {
	if s.runner == nil || s.worktrees == nil {
		return dispatchPrep{}, fmt.Errorf("control: dispatch not configured (no runner/worktrees)")
	}

	task, err := s.store.GetTask(id)
	if err != nil {
		return dispatchPrep{}, err
	}
	// Dispatchable from planned/queued (first attempt) or rejected (retry after a
	// failed verify — rejected → queued is the retry path from §6's ladder).
	// `blocked` is deliberately absent: a Task waiting on the graph is not
	// runnable, and the scheduler never admits one.
	if task.State != StatePlanned && task.State != StateQueued && task.State != StateRejected {
		return dispatchPrep{}, fmt.Errorf("%w: task %s is %s, not dispatchable (want planned/queued/rejected)", ErrWrongState, id, task.State)
	}
	// Re-check the graph before admitting: a dependency may have been declared, or
	// have become unsatisfiable, since this Task was last evaluated.
	if status, err := s.store.DependencyStatusFor(id); err == nil && !status.Ready() {
		if _, blockErr := s.refreshBlockedState(id); blockErr != nil {
			log.Printf("control: blocking %s: %v", id, blockErr)
		}
		return dispatchPrep{}, s.refuse("task", id, EventGraph, ReasonDependenciesUnmet, fmt.Sprintf(
			"task %s is waiting on %v (unsatisfiable: %v)", id, status.Unmet, status.Unsatisfiable))
	}
	// Budget gate, BEFORE any state change or side-effect: an over-budget dispatch
	// must leave the task exactly as it found it.
	if err := s.checkDispatchBudget(task); err != nil {
		return dispatchPrep{}, err
	}
	// Admission: capacity, and fairness about who gets it.
	if err := s.admitDispatch(task); err != nil {
		return dispatchPrep{}, err
	}
	repoDir, err := s.projects.ProjectDir(task.Project)
	if err != nil {
		return dispatchPrep{}, err
	}

	// Drive the task into working: planned/rejected → queued → working.
	if task.State == StatePlanned {
		if _, err := s.store.TransitionTask(id, StateQueued, false, "dispatch"); err != nil {
			return dispatchPrep{}, err
		}
	} else if task.State == StateRejected {
		if _, err := s.store.TransitionTask(id, StateQueued, false, "retry after rejection"); err != nil {
			return dispatchPrep{}, err
		}
	}
	if _, err := s.store.TransitionTask(id, StateWorking, false, "dispatch: worktree + run"); err != nil {
		return dispatchPrep{}, err
	}

	// Create the Job (records base_sha, runner, the wall-clock budget) in working.
	job, err := s.store.CreateJob(id, task.BaseSHA, "claude", task.Budget.WallClockSeconds, StateWorking)
	if err != nil {
		return dispatchPrep{}, err
	}
	// Isolated worktree at base_sha on the deterministic branch.
	wtPath, err := s.worktrees.Add(repoDir, id, job.ID, task.BaseSHA)
	if err != nil {
		// Could not even prepare the workspace: fail the job + task, no worktree.
		s.failJobAndTask(id, job.ID, ExecFailed, "", "worktree add failed: "+err.Error())
		return dispatchPrep{}, err
	}
	return dispatchPrep{task: task, job: job, repoDir: repoDir, worktree: wtPath}, nil
}

// runDispatch runs the agent with s.mu RELEASED (process exit is the boundary,
// bounded by the wall-clock budget), then retakes it to capture, classify and
// promote. s.mu must be held on entry and is held on return; the caller holds the
// in-flight claim.
//
// Not holding the lock across the run is what keeps `task cancel` and the
// reconcile loop responsive while a Job runs — at the cost of having to cope with
// the Task being cancelled underneath us, which the post-run bookkeeping does
// explicitly rather than by fighting the state machine.
func (s *Service) runDispatch(prep dispatchPrep) (DispatchResult, error) {
	task, job := prep.task, prep.job

	logPath := JobLogPath(s.dataDir, job.ID)

	var outcome RunOutcome
	s.unlockedDuring(func() {
		outcome = runUnderWallClock(s.runner, JobSpec{
			TaskID: task.ID, JobID: job.ID, Project: task.Project, Objective: task.Objective,
			Runner: "claude", Budget: task.Budget.WallClockSeconds, BaseSHA: task.BaseSHA,
			WorktreeDir: prep.worktree, LogPath: logPath,
		})
	})

	// Record the log BEFORE anything can return early, and only if the runner
	// actually left a file there — a row pointing at a path that resolves to
	// nothing is worse than an empty one, because it sends a reader looking.
	//
	// Existence rather than the outcome is the test on purpose: it is the one
	// signal that survives every exit path. A timeout abandons the runner
	// goroutine mid-write (runUnderWallClock returns without it), a cancellation
	// returns before the runner does, and an open failure inside the runner is
	// reported by log line only — in all three the file on disk still answers
	// "is there something to read" correctly.
	s.recordJobLog(job.ID, logPath)

	// Capture the tree (salvage snapshot even on failure). Best-effort: a capture
	// failure leaves output_snapshot empty but does not lose the outcome.
	headSHA, capErr := s.worktrees.Capture(prep.worktree)
	if capErr != nil {
		log.Printf("control: capture worktree for %s: %v", job.ID, capErr)
	}
	if _, err := s.store.SetJobExecutionResult(job.ID, outcome.Result, headSHA); err != nil {
		return DispatchResult{}, err
	}

	// The Task may have been cancelled while the agent ran — that is the whole
	// point of releasing the lock. A terminal Job is not ours to move any more:
	// record what happened (done above) and report it, rather than erroring on a
	// transition the state machine is right to refuse.
	if current, err := s.store.GetJob(job.ID); err == nil && IsTerminal(current.State) {
		log.Printf("control: job %s reached %s while running (cancelled?) — keeping that outcome", job.ID, current.State)
		return DispatchResult{Job: current}, nil
	}

	switch outcome.Result {
	case ExecSuccess:
		// Promote: job → candidate, create the candidate Artifact, task → candidate.
		// The worktree is KEPT — candidate is non-terminal; the branch/commit must
		// remain available for the clean-verifier step.
		if _, err := s.store.TransitionJob(job.ID, StateCandidate, false, note(outcome, "success → candidate")); err != nil {
			return DispatchResult{}, err
		}
		art, err := s.store.CreateArtifact(job.ID, task.BaseSHA, headSHA, BranchName(task.ID, job.ID))
		if err != nil {
			return DispatchResult{}, err
		}
		if _, err := s.store.TransitionTask(task.ID, StateCandidate, false, "job candidate"); err != nil {
			return DispatchResult{}, err
		}
		j, _ := s.store.GetJob(job.ID)
		return DispatchResult{Job: j, Artifact: &art}, nil

	case ExecCancelled:
		s.terminate(task.ID, job.ID, prep.repoDir, StateCancelled, note(outcome, "cancelled"))
	default: // ExecFailed, ExecTimeout
		// The JOB failed; the TASK did not (#80). An agent that exits non-zero is a
		// legitimate attempt that did not work, and retrying is the textbook
		// remedy — so the objective and its remaining attempts must survive it.
		//
		// This used to drive BOTH to `failed`, which is terminal: one bad exit and
		// the Task was unreachable by dispatch, retry, replan and reverify alike,
		// with its budget unspent. Measured twice. The `Not logged in` era killed
		// four Tasks that way for an environment fault that had nothing to do with
		// any of their objectives; T-15 was killed by a four-second exit with two
		// of three attempts left. Every one had to be recreated by hand, losing its
		// history and its reviews.
		//
		// The asymmetry is the same one reapJob already uses, and for the same
		// reason: the two entities answer different questions. "Did this attempt
		// finish?" — no, and nothing will resume it, so the Job is `failed`. "Is
		// this work still worth doing?" — nobody has said otherwise, so the Task is
		// `rejected`, the state the retry/replan ladder is built on.
		//
		// The attempt is still charged. The plane cannot tell a broken environment
		// from a genuinely bad run, and refunding on an unsure reading is the worse
		// error: it would make a Job that fails instantly free to repeat forever.
		s.failJobRejectTask(task.ID, job.ID, prep.repoDir, note(outcome, "failed"))
	}

	j, _ := s.store.GetJob(job.ID)
	return DispatchResult{Job: j}, nil
}

// checkDispatchBudget enforces the two dispatch-time budget axes the plane can
// genuinely enforce (§6): max-attempts (how many Jobs this Task may ever have)
// and concurrency (how many of this project's Jobs may be running at once).
// Both are refusals: nothing moves, a decision event is logged, and the caller
// gets a *RejectionError it can tell apart from a failure.
func (s *Service) checkDispatchBudget(task Task) error {
	b := task.Budget
	if b.MaxAttempts > 0 {
		used, err := s.store.CountJobsForTask(task.ID)
		if err != nil {
			return err
		}
		if used >= b.MaxAttempts {
			// Deliberately does NOT suggest a replan: a replan is bounded by the same
			// counter, so pointing at it would be advice that cannot work.
			return s.refuse("task", task.ID, EventBudget, ReasonAttemptsExhausted, fmt.Sprintf(
				"task %s has used all %d attempt(s); cancel it, or raise maxAttempts for %q in the host-side budget policy and create a new task",
				task.ID, b.MaxAttempts, task.Project))
		}
	}
	// Concurrency is now the SCHEDULER's decision, not a lone budget check: the
	// per-Task budget axis, the operator's per-project limit and the global limit
	// all apply, and fairness decides who takes free capacity. See admitDispatch.
	return nil
}

// admitDispatch asks the scheduler whether this Task may start a Job now, and
// records the decision either way. s.mu must be held, so the counts it reads
// cannot drift between the count and the decision.
//
// Every refusal is a typed rejection and a logged event — a scheduler that
// silently declines is indistinguishable from one that is broken.
func (s *Service) admitDispatch(task Task) error {
	projectRunning, err := s.store.CountRunningJobsForProject(task.Project)
	if err != nil {
		return err
	}
	globalRunning, err := s.store.CountRunningJobs()
	if err != nil {
		return err
	}
	req := admissionRequest{
		taskID: task.ID, project: task.Project,
		projectRunning: projectRunning, globalRunning: globalRunning,
		taskConcurrency: task.Budget.Concurrency,
	}
	if err := s.sched.admit(req); err != nil {
		reason, _ := Rejected(err)
		if logErr := s.store.LogDecision("task", task.ID,
			EventMeta{Kind: EventSchedule, Reason: reason, Actor: ActorPlane},
			fmt.Sprintf("not admitted: %s (project running %d, global running %d)",
				err.Error(), projectRunning, globalRunning)); logErr != nil {
			log.Printf("control: logging admission refusal for %s: %v", task.ID, logErr)
		}
		return err
	}
	if err := s.store.LogDecision("task", task.ID,
		EventMeta{Kind: EventSchedule, Actor: ActorPlane},
		fmt.Sprintf("admitted (project running %d, global running %d)",
			projectRunning, globalRunning)); err != nil {
		log.Printf("control: logging admission for %s: %v", task.ID, err)
	}
	return nil
}

// runUnderWallClock runs one attempt bounded by the Job's wall-clock budget.
// What that bound actually is, stated precisely because the budget is easy to
// oversell: the runner is handed a deadline context AND raced against it, so the
// plane reaches its own verdict on time whether or not the runner cooperates —
// an overrun is classified execution_result=timeout on the spot and the Job ROW
// goes terminal. That is bookkeeping plus a cancellation request. It is not the
// death of a process the plane did not fork.
//
// So a runner that ignores its context keeps running in the background until it
// exits (the goroutine below is parked on a buffered channel, so nothing here
// blocks or leaks a lock, but the container may outlive the verdict, and the
// budget cannot be described as terminating it). Killing the underlying container
// needs a runner that honours the context — the real CoordinatorRunner is
// exec-based and does not abort a command mid-flight today; real termination
// wants a persisted execution handle with an idempotent Stop/Kill, which is a
// backlog item, not something this function can fake. `steer.go`'s delivery race
// carries the same limit for the same reason.
// A budget of 0 means unbounded and skips all of this.
func runUnderWallClock(runner AgentRunner, spec JobSpec) RunOutcome {
	if spec.Budget <= 0 {
		return runner.Run(context.Background(), spec)
	}
	limit := time.Duration(spec.Budget) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()

	done := make(chan RunOutcome, 1) // buffered: an abandoned runner never blocks
	go func() { done <- runner.Run(ctx, spec) }()

	select {
	case outcome := <-done:
		return outcome
	case <-ctx.Done():
		return RunOutcome{Result: ExecTimeout, Detail: fmt.Sprintf(
			"wall-clock budget of %ds exceeded", spec.Budget)}
	}
}

// VerifyTask runs the plane-owned verify pass over a task's candidate Job (§6).
// The test-integrity gate runs FIRST: if the Job's diff (base..head) touches any
// frozen acceptance file, it goes straight to `rejected` and the VerifyRunner is
// never consulted. Otherwise candidate → verifying → VerifyRunner →
// verified | rejected. Structurally this is a plane transition, so only the
// control plane can reach `verified` (workers cannot).
// VerifyRequest is the input to Service.VerifyTask (and POST /tasks/{id}/verify).
type VerifyRequest struct {
	// IgnoreResult waives a FAILING verification: the verifier still runs, its true
	// outcome is still recorded, the artifact still carries verify=fail — and the
	// Task moves on to the approval gate anyway, on the named human's authority.
	//
	// It is deliberately NOT a way to record a pass. `verified` means "the plane
	// applied its own oracle and the artifact passed", and a waived artifact never
	// reaches that state, because writing it would put a false statement into a log
	// that approval, integration and dependency satisfaction all read as true. What
	// a waiver changes is not the finding but who is answerable for proceeding past
	// it — which is exactly what an operator overriding a check is actually doing.
	IgnoreResult bool `json:"ignoreResult,omitempty"`
}

func (s *Service) VerifyTask(id string, req VerifyRequest) (VerifyResult, error) {
	return s.verifyTask(Human(), id, req)
}

func (s *Service) verifyTask(caller Caller, id string, req VerifyRequest) (VerifyResult, error) {
	// A waiver is the one thing in verification a caller can influence, so it is
	// the one thing an agent may not ask for. Refused here rather than tiered as a
	// proposal: the two tiers are "execute" and "ask a human to execute", and the
	// second would let the party being graded put its own waiver in front of a
	// human as a routine-looking confirmation.
	if req.IgnoreResult && caller.IsAgent() {
		return VerifyResult{}, &RejectionError{
			Reason: ReasonForbidden,
			Entity: id,
			Message: "a verification result may only be waived by a human caller: " +
				"waiving is accepting answerability for an artifact the oracle refused, " +
				"and the party being graded cannot accept it on its own behalf",
		}
	}
	return s.verifyTaskLocked(caller, id, req)
}

func (s *Service) verifyTaskLocked(caller Caller, id string, req VerifyRequest) (VerifyResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Configuration is checked FIRST, before anything moves. Discovering there is
	// no verifier *after* driving the task into `verifying` would strand it there:
	// nothing but cancel can leave that state, and it would have cost a review
	// cycle for a verification that never ran.
	if s.verifier == nil {
		return VerifyResult{}, fmt.Errorf("control: no verifier configured")
	}

	task, err := s.store.GetTask(id)
	if err != nil {
		return VerifyResult{}, err
	}
	if task.State != StateCandidate {
		return VerifyResult{}, fmt.Errorf("%w: task %s is %s, not verifiable (want candidate)", ErrWrongState, id, task.State)
	}
	job, ok, err := s.candidateJob(id)
	if err != nil {
		return VerifyResult{}, err
	}
	if !ok {
		return VerifyResult{}, fmt.Errorf("%w: task %s has no candidate job to verify", ErrWrongState, id)
	}
	repoDir, err := s.projects.ProjectDir(task.Project)
	if err != nil {
		return VerifyResult{}, err
	}
	art := s.firstArtifact(job.ID)

	// Review-cycle budget (§6, strongly enforceable). A REFUSAL, not a verdict:
	// the plane declines to spend another verification on this task and leaves it
	// resting at `candidate` for a human to inspect, cancel, or supersede. A
	// verdict here would be a lie — nothing about the artifact was examined.
	if max := task.Budget.MaxReviewCycles; max > 0 {
		used, err := s.store.CountReviewCycles(id)
		if err != nil {
			return VerifyResult{}, err
		}
		if used >= max {
			return VerifyResult{}, s.refuse("task", id, EventBudget, ReasonReviewCyclesExhausted, fmt.Sprintf(
				"task %s has used all %d review cycle(s); the candidate is unchanged — cancel it or raise the project budget",
				id, max))
		}
	}

	// The Job's OWN base, not the Task's, is what the next two checks measure
	// against — and the distinction only became visible with re-verification.
	//
	// Both questions below are about what THIS JOB DID, and a Job's diff is defined
	// relative to the commit it was checked out at. The two bases are normally the
	// same value: a Job is created at the Task's base, and `retry --rebase` moves
	// the Task and then dispatches a fresh Job at the new base. `reverify --amended`
	// is the first operation that re-pins a Task while keeping an EXISTING artifact,
	// so it is the first time they can differ — and against the Task's base the
	// diff would no longer describe the Job at all. It would describe the divergence
	// between two trees, and every file the new base added would read as a file the
	// Job deleted. An amended re-grade whose corrected policy was itself an
	// acceptance file (`.daedalus/verify.json` is in the default globs) would then
	// trip the integrity gate on the very commit that fixed the oracle.
	//
	// Using the Job's base weakens nothing: the gate still catches every acceptance
	// file the Job touched, measured from where the Job actually started.
	jobBase := job.BaseSHA
	if jobBase == "" {
		jobBase = task.BaseSHA
	}

	// Null-agent floor (§6): an artifact identical to its base is no change at all
	// — it must never verify as "done". Reject before any gate/verifier work so a
	// do-nothing (or capture-failed) job can't earn a vacuous pass.
	if job.OutputSnapshot == "" || job.OutputSnapshot == jobBase {
		note := "null-agent floor: empty change (head == base) — nothing to verify"
		res := s.doReject(task, job, art, repoDir, ReasonNullAgentFloor, note)
		res.Detail = note
		return res, nil
	}

	// Stale base (§6): the artifact was built on a base the project has since moved
	// past, so verifying it would prove something about a tree nobody will
	// integrate. Rejected as a verdict, with the remedy named in the note —
	// `task retry --rebase` re-pins the task to the new tip and re-freezes the
	// acceptance policy there. Checked before the gate/verifier so a doomed
	// artifact never costs a verifier container.
	stale, tip, err := s.staleAgainstTarget(task)
	if err != nil {
		return VerifyResult{}, err
	}
	if stale {
		// The tip here is the PLANE-OWNED target, which only a completed integration
		// advances — so a stale base now means "another integration landed while
		// this Task was in flight", not "somebody moved a branch". Recommending a
		// rebase is therefore safe again: the commit being rebased onto is one the
		// plane itself landed, not one a worker could have planted.
		note := fmt.Sprintf("stale base: artifact built on %s but the integration target is now %s — rebase onto it and re-verify (daedalus task retry %s --rebase)",
			shortSHA(task.BaseSHA), shortSHA(tip), id)
		res := s.doReject(task, job, art, repoDir, ReasonStaleBase, note)
		res.Detail = note
		return res, nil
	}

	// Re-derive the frozen policy from base_sha (immutable) and confirm it still
	// hashes to what we froze at create; a mismatch means the acceptance oracle
	// drifted in history → reject outright.
	policy, err := ReadAcceptancePolicyAt(repoDir, task.BaseSHA)
	if err != nil {
		return VerifyResult{}, err
	}
	// NOTE the ordering here matters: the drift check compares the REPO policy
	// alone against the hash frozen at create. Per-task checks are deliberately
	// outside that hash — they are the Task's own addition, not the project's
	// policy, and folding them in would make every task with a check look like
	// drift. They are appended to the policy further down, after this gate.
	if task.AcceptanceHash != "" && policy.Hash() != task.AcceptanceHash {
		note := "acceptance policy hash drift since base_sha — rejected"
		res := s.doReject(task, job, art, repoDir, ReasonPolicyDrift, note)
		res.Detail = note
		return res, nil
	}

	// THE ACCEPTANCE FILES THE JOB TOUCHED — noted, not refused.
	//
	// This used to reject the Job outright. The rule it enforced is right — a Job
	// that can edit the files grading it can pass by changing the grader — but the
	// enforcement was reading a diff, and a diff cannot tell "added the test that
	// pins this fix" from "deleted the assertion that was failing". They are the
	// same operation to anything reading file names, so the gate refused both, and
	// the repository's own practice of landing a change with its test became
	// unlandable by any Job.
	//
	// The oracle is now RESTORED to its frozen state inside the verifier's clean
	// checkout, before a single check runs (CleanVerifier.Verify). An edit to an
	// acceptance file therefore cannot influence the verdict, which is the whole
	// protection — achieved by making the edit ineffective rather than fatal.
	//
	// The paths are still recorded and reported: a human deciding, and the reviewer
	// reading the diff, should both know the change touched the oracle, even though
	// the grading did not use it.
	changes, err := AcceptanceFileChanges(repoDir, jobBase, job.OutputSnapshot, policy.AcceptanceGlobs)
	if err != nil {
		return VerifyResult{}, err
	}
	touchedFiles := AcceptancePaths(changes)

	// Gate clean → candidate → verifying (job + task), then run the verifier.
	if _, err := s.store.TransitionJob(job.ID, StateVerifying, false, "integrity gate passed → verifying"); err != nil {
		return VerifyResult{}, err
	}
	if _, err := s.store.TransitionTask(id, StateVerifying, false, "verifying"); err != nil {
		return VerifyResult{}, err
	}
	// From here the task is in `verifying`, a state only this function or a
	// reconcile can leave. Every error path below must therefore put it back, or
	// the task is stranded: verify/retry/replan/dispatch all refuse a `verifying`
	// task and only cancel would escape. `stranded` is cleared on each path that
	// resolves the state itself.
	stranded := true
	defer func() {
		if stranded {
			s.recoverStrandedVerify(id, job.ID, "verify aborted before a verdict — returned to candidate")
		}
	}()
	// Refresh job/task snapshots for the return value + reject path.
	job, _ = s.store.GetJob(job.ID)
	task, _ = s.store.GetTask(id)

	// Pin the image by digest. Lazily capture at first verify if it was not
	// captured at create (e.g. the image was built after the task was created).
	if task.ImageDigest == "" {
		if updated, ok := s.captureDigest(task); ok {
			task = updated
		}
	}

	// The verifier is a container run: it can take minutes. Release the lock
	// across it for the same reason as runner.Run — otherwise cancel and reconcile
	// are dead for the duration. The in-flight claim above keeps reconcile from
	// mistaking this live verify for a stranded one.
	// jobBase, not task.BaseSHA — the same choice the integrity gate makes above,
	// and for the same reason. The verifier's baseline asks "was this check already
	// failing on the tree this Job was handed", and only the Job's own base answers
	// that. After `reverify --amended` the Task's base can be a commit the Job never
	// saw, and a check trunk FIXED there would then look like a check this artifact
	// broke — the failure mode inverted, but the same mistake.
	//
	// The Task's checks travel separately rather than through withTaskChecks: a
	// per-task check is meant to fail at the base, so it must never be baselined.
	spec := VerifySpec{
		TaskID: id, JobID: job.ID, Project: task.Project, RepoDir: repoDir,
		BaseSHA: jobBase, HeadSHA: job.OutputSnapshot,
		Branch: BranchName(id, job.ID), Policy: policy, TaskChecks: task.Checks,
		ImageDigest: task.ImageDigest,
	}
	// The claim and the unlock are both scoped helpers (claim.go): the claim is
	// released on every exit including a panic, and the mutex is re-taken the same
	// way. Neither is a statement written here, so neither can be forgotten here.
	var outcome VerifyOutcome
	if err := s.withClaim(id, inflightOp{kind: "verify", jobID: job.ID, project: task.Project}, func() error {
		s.unlockedDuring(func() { outcome = s.verifier.Verify(context.Background(), spec) })
		return nil
	}); err != nil {
		return VerifyResult{}, err
	}

	// The task may have been cancelled while the verifier ran; a terminal task is
	// not ours to move, and it is not stranded either.
	if current, err := s.store.GetTask(id); err == nil && IsTerminal(current.State) {
		stranded = false
		log.Printf("control: task %s reached %s during verification — keeping that outcome", id, current.State)
		j, _ := s.store.GetJob(job.ID)
		return VerifyResult{Job: j, Task: current, Artifact: art, VerifierCalled: true,
			Detail: "task became " + string(current.State) + " during verification"}, nil
	}

	if !outcome.Passed {
		stranded = false
		// A tree whose frozen oracle could not be restored was never graded, so the
		// verdict is not about the change and must not be recorded as one. It keeps
		// the integrity reason, and with it the unappealable status: re-grading a
		// tree we could not normalise produces the same nothing.
		reason := ReasonVerifyFailed
		label := "verify failed"
		if outcome.OracleUnrestorable {
			reason, label = ReasonIntegrityGate, "integrity"
		}
		res := s.doReject(task, job, art, repoDir, reason, withDetail(label, outcome.Detail))
		res.VerifierCalled = true
		res.Detail = outcome.Detail
		res.GateTouched = outcome.OracleUnrestorable
		res.TouchedFiles = touchedFiles
		// Anything already broken at the base is reported even when the verdict is a
		// rejection. It played no part in the rejection, and dropping it would make
		// the report of a repository's condition depend on whether some other check
		// happened to fail in the same run.
		res.PreExisting = outcome.PreExisting
		if req.IgnoreResult {
			// The rejection above STANDS: it is on the record, the artifact keeps
			// verify=fail, and nothing here claims the checks passed. The waiver adds
			// a second fact next to the first — a named human read that failure and
			// chose to carry the change forward — and moves the Task to the approval
			// gate, where the same human must still approve it in the open.
			res = s.waiveVerification(caller, res, outcome.Detail)
		}
		return res, nil
	}

	// verifying → verified (job + task); artifact verify = pass. The worktree is
	// KEPT: a verified candidate awaits approval/integration (Sprint 59).
	verifyMeta := EventMeta{Kind: EventVerification}
	jb, err := s.store.TransitionJobWith(job.ID, StateVerified, false, verifyMeta, withDetail("verified", outcome.Detail))
	if err != nil {
		return VerifyResult{}, err
	}
	stranded = false // the job moved on; the task follows or reconcile repairs it
	tk, err := s.store.TransitionTaskWith(id, StateVerified, false, verifyMeta, "verified")
	if err != nil {
		return VerifyResult{}, err
	}
	if art != nil {
		if a, err := s.store.SetArtifactVerify(art.ID, VerifyPass); err == nil {
			art = &a
		}
	}
	// A project that requires human approval parks here, so the Task shows up in
	// the pending-approvals queue immediately rather than only when somebody tries
	// to integrate it. Projects that do not require approval rest at `verified`.
	if s.requiresApproval(task.Project) {
		if moved, err := s.store.TransitionTaskWith(id, StateApprovalRequired, false,
			EventMeta{Kind: EventApproval},
			"awaiting human approval: required for project "+task.Project+" by policy"); err == nil {
			tk = moved
		} else {
			log.Printf("control: moving %s to approval_required: %v", id, err)
		}
	}
	// A pass carries the acceptance files the Job touched, which the grading
	// deliberately ignored. Silence here would be the wrong kind: the human at the
	// gate should know the change rewrites part of the oracle and will do so from
	// the NEXT base onwards, even though it earned nothing on this one.
	return VerifyResult{Job: jb, Task: tk, Artifact: art, VerifierCalled: true, Verified: true,
		Detail: outcome.Detail, PreExisting: outcome.PreExisting, TouchedFiles: touchedFiles}, nil
}

// recoverStrandedVerify returns a job+task that entered `verifying` but never
// reached a verdict back to `candidate`. s.mu must be held.
//
// A verification that did not happen must not cost anything: the artifact is
// unexamined, so the task goes back to being a candidate, and CountReviewCycles
// discounts the recovered cycle so the budget is not silently spent on a daemon
// crash. `verifying → candidate` is a plane-only edge (see legalTransitions); no
// worker-reachable transition was added for it.
func (s *Service) recoverStrandedVerify(taskID, jobID, note string) {
	meta := EventMeta{Kind: EventGovernance}
	if jobID != "" {
		if j, err := s.store.GetJob(jobID); err == nil && j.State == StateVerifying {
			if _, err := s.store.TransitionJobWith(jobID, StateCandidate, false, meta, note); err != nil {
				log.Printf("control: recovering stranded job %s: %v", jobID, err)
			}
		}
	}
	if t, err := s.store.GetTask(taskID); err == nil && t.State == StateVerifying {
		if _, err := s.store.TransitionTaskWith(taskID, StateCandidate, false, meta, note); err != nil {
			log.Printf("control: recovering stranded task %s: %v", taskID, err)
		}
	}
}

// doReject drives a candidate-or-verifying job+task to `rejected` (legal from
// both), marks the artifact verify=fail, and reclaims the attempt's worktree (a
// retry makes a fresh one). job and task are kept in lockstep so `from` matches.
// The typed reason is carried onto both transition events and back to the
// caller, so "why was this rejected" is answerable without parsing prose.
func (s *Service) doReject(task Task, job Job, art *Artifact, repoDir string, reason RejectionReason, note string) VerifyResult {
	meta := EventMeta{Kind: EventRejection, Reason: reason}
	jb, err := s.store.TransitionJobWith(job.ID, StateRejected, false, meta, note)
	if err != nil {
		log.Printf("control: reject job %s: %v", job.ID, err)
		jb = job
	}
	tk, err := s.store.TransitionTaskWith(task.ID, StateRejected, false, meta, note)
	if err != nil {
		log.Printf("control: reject task %s: %v", task.ID, err)
		tk = task
	}
	if art != nil {
		if a, err := s.store.SetArtifactVerify(art.ID, VerifyFail); err == nil {
			art = &a
		}
	}
	if s.worktrees != nil {
		_ = s.worktrees.Remove(repoDir, job.ID)
	}
	return VerifyResult{Job: jb, Task: tk, Artifact: art, Reason: reason, Verified: false}
}

// waiveVerification records a human's decision to proceed past a failing
// verification and moves the Task to the approval gate.
//
// Two events, not one, and the order matters: the rejection is already written
// when this runs, so the log reads "the artifact failed" and then "a human waived
// that result" — never a single entry that could be mistaken for a pass. The
// waiver is recorded against the JOB, because it is a judgement about one
// artifact; a Task-level flag would silently pre-authorise whatever a later retry
// produced, which nobody waived and nobody has seen.
//
// If the move to the approval gate fails, the Task simply stays `rejected` and
// the waiver stands in the log as an intent that did not take effect. That is the
// right failure direction: a waiver that half-applied must leave the artifact
// harder to land, never easier.
func (s *Service) waiveVerification(caller Caller, res VerifyResult, detail string) VerifyResult {
	meta := EventMeta{Kind: EventWaiver, Actor: governanceMetaFor(caller).Actor}
	note := fmt.Sprintf("verification result WAIVED by %s: the checks failed (%s) and a human "+
		"chose to carry this artifact forward on their own authority; it was never verified",
		meta.Actor, detail)
	if err := s.store.LogDecision("job", res.Job.ID, meta, note); err != nil {
		log.Printf("control: recording waiver for %s: %v", res.Job.ID, err)
		return res
	}
	if _, err := s.store.TransitionJobWith(res.Job.ID, StateApprovalRequired, false, meta, note); err != nil {
		log.Printf("control: waived job %s → approval_required: %v", res.Job.ID, err)
		return res
	}
	tk, err := s.store.TransitionTaskWith(res.Task.ID, StateApprovalRequired, false, meta, note)
	if err != nil {
		log.Printf("control: waived task %s → approval_required: %v", res.Task.ID, err)
		return res
	}
	res.Task = tk
	res.Waived = true
	res.Detail = note
	if j, err := s.store.GetJob(res.Job.ID); err == nil {
		res.Job = j
	}
	return res
}

// RetryTask retries a rejected Task: a FRESH Job on the same Task, with the
// attempt counter advanced and the budget re-checked (§6's retry rung). The
// previous Job is left exactly as it is — attempt history is preserved, never
// overwritten, so a Task carries its whole Job chain and the record of why each
// attempt was rejected survives the retry.
//
// With Rebase, the Task is first re-pinned to the project's current tip and its
// acceptance policy re-frozen there — the remedy for a `stale_base` rejection.
// That is deliberately opt-in: re-freezing the oracle at a newer commit adopts
// whatever verify policy that commit carries, so a human asks for it by name.
func (s *Service) RetryTask(id string, req RetryRequest) (RetryResult, error) {
	return s.retryTask(Human(), id, req)
}

// retryTask is RetryTask with an explicit caller identity.
func (s *Service) retryTask(caller Caller, id string, req RetryRequest) (RetryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, prep, err := s.prepareRetry(caller, id, req)
	if err != nil {
		return RetryResult{}, err
	}
	dispatch, err := s.runClaimedDispatch(prep)
	if err != nil {
		return RetryResult{}, err
	}
	res.Dispatch = dispatch
	if n, err := s.store.CountJobsForTask(id); err == nil {
		res.Attempts = n
	}
	return res, nil
}

// prepareRetry is the locked half of RetryTask: guards, budget, the optional
// rebase, the governance event, and the dispatch preparation. s.mu MUST be held.
func (s *Service) prepareRetry(caller Caller, id string, req RetryRequest) (RetryResult, dispatchPrep, error) {
	task, err := s.store.GetTask(id)
	if err != nil {
		return RetryResult{}, dispatchPrep{}, err
	}
	if task.State != StateRejected {
		return RetryResult{}, dispatchPrep{}, fmt.Errorf("%w: task %s is %s, not retryable (want rejected)", ErrWrongState, id, task.State)
	}
	// Re-check the budget up front so an exhausted Task is refused before the
	// rebase touches anything.
	if err := s.checkDispatchBudget(task); err != nil {
		return RetryResult{}, dispatchPrep{}, err
	}

	used, err := s.store.CountJobsForTask(id)
	if err != nil {
		return RetryResult{}, dispatchPrep{}, err
	}
	res := RetryResult{Attempt: used + 1, MaxAttempts: task.Budget.MaxAttempts}

	if req.Rebase {
		repoDir, err := s.projects.ProjectDir(task.Project)
		if err != nil {
			return RetryResult{}, dispatchPrep{}, err
		}
		// Shared with `reverify --amended` (reverify.go): both adopt a newer oracle,
		// so both must refuse a self-authored tip and record the same lineage.
		updated, rebased, err := s.rebaseTaskToTip(caller, task, repoDir)
		if err != nil {
			return RetryResult{}, dispatchPrep{}, err
		}
		task, res.Rebased = updated, rebased
	}
	res.BaseSHA = task.BaseSHA

	if err := s.store.LogDecision("task", id,
		governanceMetaFor(caller),
		fmt.Sprintf("retry: attempt %d of %d", res.Attempt, task.Budget.MaxAttempts)); err != nil {
		log.Printf("control: logging retry of %s: %v", id, err)
	}

	prep, err := s.prepareDispatch(id)
	if err != nil {
		return RetryResult{}, dispatchPrep{}, err
	}
	return res, prep, nil
}

// refuseSelfAuthoredRebase refuses a rebase whose target tip is reachable from
// any commit this Task's Jobs produced — i.e. the tip contains the worker's own
// work. See IsSelfAuthoredTip for why this matters.
func (s *Service) refuseSelfAuthoredRebase(task Task, repoDir, tip string) error {
	jobs, err := s.store.ListJobsForTask(task.ID)
	if err != nil {
		return err
	}
	commits := make([]string, 0, len(jobs))
	for _, j := range jobs {
		commits = append(commits, j.OutputSnapshot)
	}
	selfAuthored, offender, err := IsSelfAuthoredTip(repoDir, tip, commits)
	if err != nil {
		// A failure to *check* must not be read as "safe": re-freezing the oracle is
		// the consequential act, so an unverifiable check refuses the rebase.
		return s.refuse("task", task.ID, EventRejection, ReasonUnsafeRebase, fmt.Sprintf(
			"cannot confirm the project tip %s is free of this task's own commits: %v", shortSHA(tip), err))
	}
	if selfAuthored {
		return s.refuse("task", task.ID, EventRejection, ReasonUnsafeRebase, fmt.Sprintf(
			"refusing to rebase onto %s: it is contained in this task's own job commit %s, so re-freezing the acceptance policy there would adopt an oracle the worker authored. "+
				"Reset the project's target branch to a commit the jobs did not write, then retry",
			shortSHA(tip), shortSHA(offender)))
	}
	return nil
}

// ReplanTask revises a rejected Task's objective and returns it to `planned`
// (§6's replan rung) — for when the attempt failed because the instruction was
// wrong, not because the work was.
//
// It does NOT reset the attempt counter: the budget bounds the Task, not the
// objective, so replanning cannot be used to buy more attempts. The Job chain is
// preserved intact, and the new objective is recorded in the event log next to
// the one it replaced.
func (s *Service) ReplanTask(id string, req ReplanRequest) (Task, error) {
	return s.replanTask(Human(), id, req)
}

// replanTask is ReplanTask with an explicit caller identity.
func (s *Service) replanTask(caller Caller, id string, req ReplanRequest) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.Objective == "" {
		return Task{}, fmt.Errorf("control: replan requires --objective")
	}
	task, err := s.store.GetTask(id)
	if err != nil {
		return Task{}, err
	}
	// `rejected` is the ordinary case: the work was graded and found wanting.
	// `candidate` is the one that was missing — the work is done, nobody has
	// graded it, and the operator has realised the question was wrong. Making them
	// grade it first to unlock the ladder taught them nothing and cost a review
	// cycle.
	if task.State != StateRejected && task.State != StateCandidate {
		return Task{}, fmt.Errorf("%w: task %s is %s, not replannable (want rejected or candidate)",
			ErrWrongState, id, task.State)
	}
	// A replan that could never be dispatched is worth refusing now rather than
	// leaving a `planned` task that only fails at the next dispatch.
	if err := s.checkDispatchBudget(task); err != nil {
		return Task{}, err
	}
	// Re-pin FIRST, so a refused rebase leaves the objective alone. The order
	// matters: correcting the instruction and then failing to move the base would
	// leave a Task that reads as fixed and is not.
	rebased := false
	if req.Rebase {
		repoDir, err := s.projects.ProjectDir(task.Project)
		if err != nil {
			return Task{}, err
		}
		// The SAME helper retry and `reverify --amended` use. A second copy would be
		// a second place for the Sprint-59 laundering fix to be forgotten, and this
		// is the third caller — which is exactly when a copy starts to look
		// reasonable and stops being so.
		updated, did, err := s.rebaseTaskToTip(caller, task, repoDir)
		if err != nil {
			return Task{}, err
		}
		task, rebased = updated, did
	}
	// An ungraded attempt does not just stay behind: a Job left in `candidate`
	// under a `planned` Task is a live attempt nothing will ever move, and
	// `candidateJob` would keep offering it to a verify that is now about a
	// different objective. It is set aside as rejected — the state the ladder
	// already understands for "this attempt is not going anywhere" — and its
	// branch survives, so the work is still readable afterwards.
	if task.State == StateCandidate {
		if job, ok, err := s.candidateJob(id); err == nil && ok {
			s.driveJob(job.ID, []State{StateRejected}, EventMeta{Kind: EventGovernance},
				fmt.Sprintf("set aside: the task's objective was replaced (%q → %q)",
					task.Objective, req.Objective))
		}
	}
	note := fmt.Sprintf("replan: objective %q → %q", task.Objective, req.Objective)
	if rebased {
		note += fmt.Sprintf(" (re-pinned to %s)", shortSHA(task.BaseSHA))
	}
	return s.store.ReplanTask(id, req.Objective, governanceMetaFor(caller), note)
}

// TaskEvents returns the control-plane-managed event log for a task: its own
// events plus those of every Job and Artifact beneath it, in the order they
// happened. Read-only — there is no counterpart that writes, amends, or deletes.
func (s *Service) TaskEvents(id string) ([]Event, error) {
	if _, err := s.store.GetTask(id); err != nil {
		return nil, err
	}
	return s.store.ListEventsForTask(id)
}

// candidateJob returns the latest Job of a task that is in the candidate state.
func (s *Service) candidateJob(taskID string) (Job, bool, error) {
	return s.jobInState(taskID, StateCandidate)
}

// jobInState returns a task's latest Job in the given state.
func (s *Service) jobInState(taskID string, state State) (Job, bool, error) {
	jobs, err := s.store.ListJobsForTask(taskID)
	if err != nil {
		return Job{}, false, err
	}
	for i := len(jobs) - 1; i >= 0; i-- {
		if jobs[i].State == state {
			return jobs[i], true, nil
		}
	}
	return Job{}, false, nil
}

// firstArtifact returns a job's first artifact, or nil.
func (s *Service) firstArtifact(jobID string) *Artifact {
	arts, err := s.store.ListArtifactsForJob(jobID)
	if err != nil || len(arts) == 0 {
		return nil
	}
	return &arts[0]
}

// withDetail appends an optional detail in parentheses.
func withDetail(base, detail string) string {
	if detail == "" {
		return base
	}
	return base + " (" + detail + ")"
}

// terminate drives a job+task to a terminal state and reclaims the worktree.
func (s *Service) terminate(taskID, jobID, repoDir string, term State, note string) {
	// A Task that has finished releases its place in line along with its capacity.
	s.sched.Forget(taskID)
	if _, err := s.store.TransitionJob(jobID, term, false, note); err != nil {
		log.Printf("control: terminate job %s → %s: %v", jobID, term, err)
	}
	if _, err := s.store.TransitionTask(taskID, term, false, note); err != nil {
		log.Printf("control: terminate task %s → %s: %v", taskID, term, err)
	}
	if err := s.worktrees.Remove(repoDir, jobID); err != nil {
		log.Printf("control: remove worktree %s: %v", jobID, err)
	}
}

// failJobRejectTask settles a Job whose run reported failure: the JOB goes to
// `failed`, the TASK to `rejected` (#80).
//
// It is terminate's asymmetric sibling, and the asymmetry is the point — see the
// call site. The worktree is reclaimed either way: a retry checks out a fresh
// one, and the branch survives, so whatever the failed attempt did commit is
// still reachable.
func (s *Service) failJobRejectTask(taskID, jobID, repoDir, note string) {
	// A Task that is not running releases its place in line with its capacity.
	s.sched.Forget(taskID)
	meta := EventMeta{Kind: EventRejection, Reason: ReasonExecutionFailed}
	if _, err := s.store.TransitionJobWith(jobID, StateFailed, false, meta, note); err != nil {
		log.Printf("control: failing job %s: %v", jobID, err)
	}
	if _, err := s.store.TransitionTaskWith(taskID, StateRejected, false, meta,
		note+" — the attempt failed; the task is still worth doing (retry, replan, or cancel)"); err != nil {
		log.Printf("control: rejecting task %s after a failed job: %v", taskID, err)
	}
	if err := s.worktrees.Remove(repoDir, jobID); err != nil {
		log.Printf("control: remove worktree %s: %v", jobID, err)
	}
}

// reapJob settles a Job whose run reconcile could not find: the JOB goes to
// `failed`, and its TASK goes to `rejected` rather than to a terminal state.
//
// That asymmetry is the whole point. The two entities are answering different
// questions. "Did this attempt finish?" — no, and nothing will resume it, so the
// Job is genuinely over. "Is this objective finished with?" — nobody has any
// grounds to say so: no artifact was ever examined, no verdict was reached, and
// the reason the Job died may have nothing to do with the work at all. A daemon
// restarted mid-run produces exactly this reading, and so does a container
// removed by hand.
//
// Before this, both went to `failed`, which is terminal — so a liveness reading
// that could be wrong permanently destroyed the Task, its budget, and every
// recovery command at once: `dispatch`, `retry`, `replan` and `reverify` all
// refuse a `failed` Task, and no transition leaves that state. `rejected` is the
// state the retry ladder already understands, and `prepareDispatch` already
// accepts it, so this restores a remedy rather than inventing one.
//
// The precedent is two cases up in the same function: a Task whose dispatch died
// before any Job existed is returned to `rejected` on the reasoning that nothing
// was ever attempted. A Job reaped for a missing session is the same situation
// with one more row in the database.
//
// The attempt IS still charged against max-attempts — the Job row exists and
// CountJobsForTask counts it. That is deliberate: the plane cannot tell a Job
// killed by a daemon bounce from one whose container died of its own accord, and
// silently refunding attempts on a reading it is not sure of would be a worse
// error than charging for one.
func (s *Service) reapJob(taskID, jobID, repoDir, note string) {
	// A Task that is no longer running releases its place in line with its capacity.
	s.sched.Forget(taskID)
	if _, err := s.store.TransitionJob(jobID, StateFailed, false, note); err != nil {
		log.Printf("control: reap job %s → failed: %v", jobID, err)
	}
	if _, err := s.store.TransitionTask(taskID, StateRejected, false, note); err != nil {
		log.Printf("control: reap task %s → rejected: %v", taskID, err)
	}
	// The worktree goes; a retry checks out a fresh one. The BRANCH survives, so
	// anything the salvage above captured is still reachable.
	if err := s.worktrees.Remove(repoDir, jobID); err != nil {
		log.Printf("control: remove worktree %s: %v", jobID, err)
	}
}

// failJobAndTask is terminate for the pre-worktree failure path (no worktree to
// remove yet). Records the execution result first.
func (s *Service) failJobAndTask(taskID, jobID string, result ExecutionResult, headSHA, note string) {
	_, _ = s.store.SetJobExecutionResult(jobID, result, headSHA)
	if _, err := s.store.TransitionJob(jobID, StateFailed, false, note); err != nil {
		log.Printf("control: fail job %s: %v", jobID, err)
	}
	if _, err := s.store.TransitionTask(taskID, StateFailed, false, note); err != nil {
		log.Printf("control: fail task %s: %v", taskID, err)
	}
}

// recordJobLog stores the Job's log path if, and only if, the runner left a file
// there. Best-effort throughout: losing the pointer to a log is a worse outcome
// than the one it describes only if it also loses the outcome, so nothing here
// is allowed to fail a dispatch.
func (s *Service) recordJobLog(jobID, logPath string) {
	if logPath == "" {
		return
	}
	if _, err := os.Stat(logPath); err != nil {
		return
	}
	if _, err := s.store.SetJobLogPath(jobID, logPath); err != nil {
		log.Printf("control: recording log path for %s: %v", jobID, err)
	}
}

func note(o RunOutcome, base string) string {
	if o.Detail == "" {
		return base
	}
	return base + " (" + o.Detail + ")"
}

// settleIfLandedOutsideThePlane settles a rejected Task whose artifact's commits
// are already contained in the project's integration target.
//
// Reuses ArtifactIsLanded, the same containment test the integration transaction
// runs before landing anything — so "already in" means the same thing here as it
// does there, including for an artifact that was rebased on its way in and shares
// no sha with what landed.
//
// Deliberately narrow. Only `rejected` Tasks are examined, because that is the
// state a human merges around, and only when an artifact with a real head exists;
// the cost is two git calls per rejected Task per pass, which is bounded by how
// many rejections are outstanding rather than by the size of the board.
func (s *Service) settleIfLandedOutsideThePlane(task Task) (bool, error) {
	job, ok, err := s.jobInState(task.ID, StateRejected)
	if err != nil || !ok {
		return false, err
	}
	art := s.firstArtifact(job.ID)
	if art == nil || art.HeadSHA == "" {
		return false, nil
	}
	repoDir, err := s.projects.ProjectDir(task.Project)
	if err != nil {
		return false, err
	}
	target, err := s.Target(task.Project)
	if err != nil {
		return false, err
	}
	landed, err := ArtifactIsLanded(repoDir, target.SHA, art.BaseSHA, art.HeadSHA)
	if err != nil || !landed {
		return false, err
	}
	note := fmt.Sprintf("landed outside the plane: the artifact's commits are contained in target %s, "+
		"though this Task was rejected — a human merged it. Settling the record to match the repository; "+
		"the rejection and its reason stay in the log, and nothing here marks the artifact verified",
		shortSHA(target.SHA))
	// Walk the gate rather than jumping to `integrated`. There is no
	// rejected → integrated edge and there must not be one: a single hop from a
	// refusal to a landing is the shape of every laundering bug this table exists
	// to make impossible, and adding it for a convenience would make it reachable
	// from everywhere else too. Walking costs two extra events and states the
	// truth — a human did approve this, by merging it — which is the same
	// reasoning `driveJob` already applies to the Job in settleAlreadyLanded.
	meta := EventMeta{Kind: EventIntegration}
	for _, to := range []State{StateApprovalRequired, StateApproved} {
		if _, err := s.store.TransitionTaskWith(task.ID, to, false, meta, note); err != nil {
			return false, err
		}
	}
	if task, err = s.store.GetTask(task.ID); err != nil {
		return false, err
	}
	if _, _, err = s.settleAlreadyLanded(task, job, art, repoDir, target, note); err != nil {
		return false, err
	}
	return true, nil
}

// ReconcileReport summarises what a reconcile pass changed. Returned for tests
// and daemon logging.
type ReconcileReport struct {
	FailedVanished         []string // job ids failed because their run was gone
	RemovedOrphans         []string // worktree job ids removed (no live non-terminal job)
	RecoveredVerifies      []string // job ids returned to candidate from a stranded `verifying`
	SettledOrphanJobs      []string // job ids driven terminal because their Task already was
	RecoveredJoblessTasks  []string // task ids returned to a dispatchable state with no Job
	DependencyStateChanged []string // task ids moved between blocked and planned
	HeuristicallyFailed    []string // job ids failed by the liveness HEURISTIC, not an observer
	CheckedActive          int      // non-terminal jobs examined
	SkippedUnverified      int      // jobs left alone because liveness couldn't be verified
	SettledLandedOutside   []string // task ids settled because their work was merged outside the plane
	SkippedInflight        int      // jobs left alone because this process is running them
}

// Reconcile drives observed reality toward desired (DB) state (§6, the dual-write
// fix): (1) any working Job whose coordinator session has vanished is captured,
// failed, and its worktree reclaimed; (2) any Job stranded in `verifying` by an
// interrupted verification is returned to `candidate`; (3) any orphaned worktree
// (no live, non-terminal DB job) is removed. Idempotent via deterministic names,
// so re-running is a no-op. If session liveness cannot be verified (no observer
// or an error), the vanished-check is skipped for safety — we never fail a Job we
// can't prove is dead — and any operation this process is currently running is
// skipped outright, because a live Job is not a crashed one.
func (s *Service) Reconcile() (ReconcileReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var rep ReconcileReport

	jobs, err := s.store.ListActiveJobs()
	if err != nil {
		return rep, err
	}
	// Track which job ids legitimately own a worktree so orphan detection below
	// doesn't reclaim a live one.
	liveWorktreeJobs := map[string]bool{}
	for _, j := range jobs {
		rep.CheckedActive++
		// Anything this process is running right now is live by definition. Without
		// this check, releasing s.mu across runner.Run / verifier.Verify would let a
		// reconcile tick "repair" work that is perfectly healthy.
		if s.jobIsInflight(j.TaskID, j.ID) {
			rep.SkippedInflight++
			liveWorktreeJobs[j.ID] = true
			continue
		}
		// A Job left in `verifying` with no verification running is stranded: the
		// process that was verifying it died (or errored out) between the transition
		// and the verdict. Nothing but cancel can leave that state, so reconcile is
		// the repair — back to `candidate`, where a retry of the verify is possible.
		// The artifact was never examined, so CountReviewCycles discounts it too.
		if j.State == StateVerifying {
			s.recoverStrandedVerify(j.TaskID, j.ID, "reconcile: verification interrupted — returned to candidate")
			rep.RecoveredVerifies = append(rep.RecoveredVerifies, j.ID)
			liveWorktreeJobs[j.ID] = true // the candidate's commit must survive
			continue
		}
		// A Job whose Task is already terminal is a ghost in the census: nothing
		// will ever move it, ListActiveJobs returns it forever, and the checks
		// below skip it because its state is neither `working` nor `verifying`.
		// It happens when a Job's bookkeeping fails after its Task has settled —
		// driveJob logs and continues rather than claiming a completed landing
		// failed, which is right, but the resulting inconsistency has to be
		// reconciled somewhere, and this is that somewhere.
		if task, err := s.store.GetTask(j.TaskID); err == nil && IsTerminal(task.State) {
			if s.settleJobWithTask(j, task) {
				rep.SettledOrphanJobs = append(rep.SettledOrphanJobs, j.ID)
			}
			continue
		}
		if j.State != StateWorking {
			liveWorktreeJobs[j.ID] = true
			continue
		}
		task, err := s.store.GetTask(j.TaskID)
		if err != nil {
			return rep, err
		}
		live, verifiable, viaHeuristic := s.jobLive(j, task)
		if !verifiable {
			// Nothing could establish liveness — not the per-Job observer, not the
			// heuristic. Leave it alone: never fail what you cannot prove is dead.
			rep.SkippedUnverified++
			liveWorktreeJobs[j.ID] = true
			continue
		}
		if live {
			liveWorktreeJobs[j.ID] = true // adopt the running job as-is
			continue
		}
		// The run is gone: salvage the tree, fail, reclaim.
		repoDir, _ := s.projects.ProjectDir(task.Project)
		if s.worktrees.Exists(j.ID) {
			if head, capErr := s.worktrees.Capture(s.worktrees.Path(j.ID)); capErr == nil {
				_, _ = s.store.SetJobExecutionResult(j.ID, ExecFailed, head)
			}
		}
		note := "reconcile: the job's session is gone — the attempt is over; the Task is returned for another (`task dispatch` or `task retry`)"
		if viaHeuristic {
			// Reported separately AND named in the event, because a guessed reaping
			// must be legible as a guess: an operator investigating a failed Job
			// deserves to know the plane inferred its death rather than observed it.
			note = "reconcile: HEURISTIC judged this job dead (worktree gone, or far past its wall-clock budget) — liveness could not be observed directly; the Task is returned for another attempt"
			rep.HeuristicallyFailed = append(rep.HeuristicallyFailed, j.ID)
		}
		s.reapJob(j.TaskID, j.ID, repoDir, note)
		rep.FailedVanished = append(rep.FailedVanished, j.ID)
	}

	// F4: a Task can be non-terminal with NO Job at all — a crash between the
	// `working` transition and CreateJob leaves one wedged, and iterating
	// ListActiveJobs cannot see it because there is no Job to iterate. It is not
	// dispatchable, retryable or replannable, so only cancel escapes; before
	// parallelism that window was hit rarely, and lifting the invariant made it N
	// times as likely. Reconciling both entities is the fix: a pass over active
	// TASKS, not just active Jobs.
	tasks, err := s.store.ListTasks()
	if err != nil {
		return rep, err
	}
	for _, task := range tasks {
		if !IsActive(task.State) {
			continue
		}
		if _, busy := s.inflight[task.ID]; busy {
			continue // this process is working on it
		}
		if s.recoverJoblessTask(task) {
			rep.RecoveredJoblessTasks = append(rep.RecoveredJoblessTasks, task.ID)
		}
		// THE WAKE PATH'S LIVENESS BACKSTOP. A dependency completing wakes its
		// dependents directly, which is the fast path — but a wake that only ever
		// happens on an event is a wake that is missed when the process dies
		// mid-event, and a Task blocked on a dependency that has already landed
		// would then wait forever. Sprint 61 established the invariant (free
		// capacity must become usable without human intervention) for the queue
		// lease; the same discipline applies here, so every pass re-evaluates.
		if task.State == StateBlocked || task.State == StatePlanned {
			if changed, err := s.refreshBlockedState(task.ID); err != nil {
				log.Printf("control: re-evaluating dependencies of %s: %v", task.ID, err)
			} else if changed {
				rep.DependencyStateChanged = append(rep.DependencyStateChanged, task.ID)
			}
		}
		// F5: a REJECTED Task whose work is nonetheless in the target. The plane
		// does not own the repository — a human can merge a branch it refused, and
		// people do, most often when the check rather than the work was wrong. The
		// database then carries a claim anyone can see is false: a Task recorded as
		// rejected and never landed, whose commits are demonstrably in the tree.
		//
		// That claim is not merely untidy. Everything downstream reads it: a Task
		// waiting on this one stays blocked forever, and the board shows work that
		// shipped as work that failed.
		//
		// Observing it is strictly better than either alternative. It is not a
		// bypass — nothing here lets anybody skip a verdict, and no artifact becomes
		// `verified`; the Task lands as `integrated` with a note saying it got there
		// outside the plane, which is exactly what happened. And it beats leaving the
		// divergence silent, because a human merge is otherwise invisible to the
		// plane in a way even a recorded override would not be.
		if task.State == StateRejected {
			if settled, err := s.settleIfLandedOutsideThePlane(task); err != nil {
				log.Printf("control: checking whether %s already landed: %v", task.ID, err)
			} else if settled {
				rep.SettledLandedOutside = append(rep.SettledLandedOutside, task.ID)
			}
		}
	}

	// Orphan worktrees: a checkout dir whose job is unknown or terminal.
	wts, err := s.worktrees.List()
	if err != nil {
		return rep, err
	}
	for _, jobID := range wts {
		if liveWorktreeJobs[jobID] {
			continue
		}
		job, err := s.store.GetJob(jobID)
		orphan := errors.Is(err, ErrNotFound) || (err == nil && IsTerminal(job.State))
		if !orphan {
			continue
		}
		repoDir := ""
		if err == nil {
			if t, terr := s.store.GetTask(job.TaskID); terr == nil {
				repoDir, _ = s.projects.ProjectDir(t.Project)
			}
		}
		if rmErr := s.worktrees.Remove(repoDir, jobID); rmErr != nil {
			log.Printf("control: reconcile remove orphan %s: %v", jobID, rmErr)
			continue
		}
		rep.RemovedOrphans = append(rep.RemovedOrphans, jobID)
	}
	return rep, nil
}

// settleJobWithTask drives a non-terminal Job to its Task's terminal state and
// reclaims its worktree, returning whether anything changed. s.mu must be held.
//
// The Job follows the Task rather than the other way round: the Task is the unit
// of intent and it has already settled, so the Job's own state is the stale side
// of the disagreement.
func (s *Service) settleJobWithTask(job Job, task Task) bool {
	meta := EventMeta{Kind: EventGovernance}
	note := "reconcile: task " + task.ID + " is " + string(task.State) + " — settling its job to match"
	// Walk to the Task's terminal state through whatever edges exist; `cancelled`
	// is reachable from every active state, so it is the fallback when the Task's
	// own terminal state is not reachable from where the Job is stuck.
	before := job.State
	s.driveJob(job.ID, []State{StateApprovalRequired, StateApproved, task.State}, meta, note)
	if got, err := s.store.GetJob(job.ID); err == nil && !IsTerminal(got.State) {
		s.driveJob(job.ID, []State{StateCancelled}, meta, note)
	}
	got, err := s.store.GetJob(job.ID)
	if err != nil || got.State == before {
		return false
	}
	repoDir, _ := s.projects.ProjectDir(task.Project)
	if s.worktrees != nil {
		_ = s.worktrees.Remove(repoDir, job.ID)
	}
	return true
}

// recoverJoblessTask returns a Task that is `working` (or `queued`) with no Job
// at all to a dispatchable state. s.mu must be held; returns whether it acted.
//
// The Task is moved to `rejected` rather than straight to `planned`: `rejected`
// is the state the retry/replan ladder already understands, so the operator gets
// the same recovery vocabulary as every other failure, and the event says why.
func (s *Service) recoverJoblessTask(task Task) bool {
	if task.State != StateWorking && task.State != StateQueued {
		return false
	}
	jobs, err := s.store.ListJobsForTask(task.ID)
	if err != nil || len(jobs) > 0 {
		return false
	}
	note := "reconcile: task is " + string(task.State) + " with no job — the dispatch died before one existed"
	meta := EventMeta{Kind: EventGovernance, Reason: ReasonJoblessTask}
	if task.State == StateQueued {
		// queued → working → rejected: `queued` has no direct edge to `rejected`.
		if _, err := s.store.TransitionTaskWith(task.ID, StateWorking, false, meta, note); err != nil {
			log.Printf("control: recovering jobless task %s: %v", task.ID, err)
			return false
		}
	}
	if _, err := s.store.TransitionTaskWith(task.ID, StateRejected, false, meta, note); err != nil {
		log.Printf("control: recovering jobless task %s: %v", task.ID, err)
		return false
	}
	s.sched.Forget(task.ID)
	return true
}

// jobLive decides whether ONE Job is still running, and how confident that is.
//
// Three sources, in descending order of trustworthiness:
//
//  1. A per-Job session observer — the real answer, and available in production
//     because the coordinator already keys each Job's session by
//     JobProjectName(jobID).
//  2. THE HEURISTIC (see heuristicallyDead). Used only when (1) is unavailable.
//  3. Nothing: unverifiable, and the Job is left alone.
//
// The project-level observer is deliberately NOT consulted as a liveness source:
// it answers a question about a different key (see SessionObserver), so a "yes"
// from it says nothing about this Job.
func (s *Service) jobLive(job Job, task Task) (live, verifiable, viaHeuristic bool) {
	if observer, ok := jobObserver(s.sessions); ok {
		if alive, err := observer.HasSessionForJob(job.ID); err == nil {
			return alive, true, false
		}
		// An observer that errors is an unreachable coordinator, not a dead Job.
		return false, false, false
	}
	if dead, sure := s.heuristicallyDead(job); sure {
		return !dead, true, true
	}
	return false, false, false
}

// jobObserver extracts a per-Job liveness observer from a SessionObserver, if it
// provides one and is actually usable.
//
// The naive form — `s.sessions.(JobSessionObserver)` with a trailing
// `s.sessions != nil` — checks the wrong thing in the wrong order. A type
// assertion on a nil interface already yields ok=false, so the nil check is
// redundant there; and it does not guard the hazard that IS real: a NON-nil
// interface holding a nil pointer whose method set satisfies the interface. That
// asserts successfully, and then panics the moment the method dereferences its
// receiver.
//
// Reflection is the only way to see through the interface to the pointer inside,
// and this runs once per Job per reconcile pass, so the cost is irrelevant next
// to being correct. Anything not usable falls through to the heuristic, which is
// the same answer as having no observer at all.
func jobObserver(sessions SessionObserver) (JobSessionObserver, bool) {
	if sessions == nil {
		return nil, false
	}
	observer, ok := sessions.(JobSessionObserver)
	if !ok || observer == nil {
		return nil, false
	}
	// A typed nil (e.g. (*coordinatorSessions)(nil)) is non-nil as an interface
	// but unusable as a receiver.
	if v := reflect.ValueOf(observer); v.Kind() == reflect.Ptr && v.IsNil() {
		return nil, false
	}
	return observer, true
}

// heuristicallyDead is a HEURISTIC — it guesses, and the guess can be wrong.
//
// It exists because per-Job liveness is not always available, and because the
// alternative is worse: a crashed Job stays `working`, keeps consuming a
// scheduler slot and holding its worktree, and accumulates until the project can
// never dispatch again. A wrong guess costs one Job that has to be retried; no
// guess at all costs the project.
//
// WHAT IT CANNOT DO, stated plainly so nobody reads it as authoritative: it
// cannot tell a crashed Job from a slow one whose wall-clock budget was set too
// low. Both look like "still working long after it should have finished". The
// margin below is generosity, not correctness — it reduces how often the
// heuristic is wrong, and cannot make it right. Per-Job liveness is the real
// answer; this is the fallback when that is unavailable.
//
// Two signals, and only confident ones are reported:
//
//   - The worktree is GONE. A `working` Job whose isolated checkout no longer
//     exists cannot be producing anything; this one is close to certain.
//   - The Job has been `working` far longer than its own wall-clock budget
//     allowed. This is the guess.
//
// Returns (dead, confident). A Job with no wall-clock budget and an intact
// worktree yields no opinion at all.
func (s *Service) heuristicallyDead(job Job) (dead, confident bool) {
	if job.State != StateWorking {
		return false, false
	}
	if s.worktrees != nil && !s.worktrees.Exists(job.ID) {
		return true, true
	}
	if job.Budget <= 0 {
		return false, false // nothing to measure it against
	}
	updated, err := time.Parse(timeFormat, job.UpdatedAt)
	if err != nil {
		return false, false
	}
	// The margin is deliberately generous: overrunning the budget is the plane's
	// own timeout path, so a Job still here well past it has almost certainly lost
	// the process that was supposed to enforce it (a daemon restart).
	deadline := updated.Add(time.Duration(job.Budget)*time.Second*heuristicBudgetMargin + heuristicGrace)
	if s.now().After(deadline) {
		return true, true
	}
	return false, false
}

// Heuristic tuning. These reduce how often the guess is wrong; they cannot make
// it right.
const (
	// heuristicBudgetMargin multiplies a Job's wall-clock budget before it is
	// considered overdue.
	heuristicBudgetMargin = 2
	// heuristicGrace is added on top, so a tiny budget does not make the heuristic
	// trigger-happy.
	heuristicGrace = 5 * time.Minute
)

// sessionLive returns (live, verifiable). verifiable is false when there is no
// observer or it errors — the caller then leaves the job untouched.
func (s *Service) sessionLive(project string) (live, verifiable bool) {
	if s.sessions == nil {
		return false, false
	}
	ok, err := s.sessions.HasSession(project)
	if err != nil {
		return false, false
	}
	return ok, true
}

// --- Real adapters (host-side; thin, so the interfaces above stay the tested seam) ---

// RegistryResolver resolves project directories through the on-disk registry.
type RegistryResolver struct{ Reg *registry.Registry }

// ProjectNames implements ProjectLister.
func (r RegistryResolver) ProjectNames() ([]string, error) {
	entries, err := r.Reg.GetProjectEntries()
	if err != nil {
		return nil, fmt.Errorf("listing registry projects: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
	}
	sort.Strings(names)
	return names, nil
}

// ProjectDir implements ProjectResolver.
func (r RegistryResolver) ProjectDir(name string) (string, error) {
	entry, ok, err := r.Reg.GetProject(name)
	if err != nil {
		return "", fmt.Errorf("reading registry: %w", err)
	}
	if !ok {
		return "", fmt.Errorf("project %q is not registered", name)
	}
	return entry.Directory, nil
}

// inflightFor reports the in-flight operation claimed for a task, if any. Used by
// tests to assert that a claim was released — a leaked claim is invisible from
// the outside until it wedges the task.
func (s *Service) inflightFor(taskID string) (inflightOp, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, ok := s.inflight[taskID]
	return op, ok
}
