// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"errors"
	"fmt"
	"log"
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

// SessionObserver reports whether the coordinator currently has a live session
// for a project. Reconcile uses it to detect a Job whose run has vanished. It is
// the coordinator dependency behind an interface (host-tested with a fake); the
// real impl wraps a coordinator client.
type SessionObserver interface {
	HasSession(project string) (bool, error)
}

// CreateTaskRequest is the input to Service.CreateTask (and the daemon's POST
// /tasks body).
type CreateTaskRequest struct {
	Project    string `json:"project"`
	Objective  string `json:"objective"`
	Acceptance string `json:"acceptance,omitempty"`
	// Budget optionally narrows the project's ceiling for this task. Unset axes
	// inherit the ceiling; an axis that *widens* it is refused with
	// ReasonOverBudget (§6 — "budget too high → REJECTED"), and a NEGATIVE axis is
	// refused with ReasonInvalidBudget (it would read as unbounded — see
	// Budget.invalidAxis). Raising a ceiling means editing the host-side policy
	// file; no request over control.sock can do it.
	Budget *Budget `json:"budget,omitempty"`
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

// StatusView is a Task plus its jobs (each with artifacts).
type StatusView struct {
	Task Task      `json:"task"`
	Jobs []JobView `json:"jobs"`
}

// DispatchResult is the terminal-of-this-attempt view returned by dispatch.
type DispatchResult struct {
	Job      Job       `json:"job"`
	Artifact *Artifact `json:"artifact,omitempty"`
}

// VerifyResult is the outcome of a plane-owned verify pass over a candidate Job.
type VerifyResult struct {
	Job          Job       `json:"job"`
	Task         Task      `json:"task"`
	Artifact     *Artifact `json:"artifact,omitempty"`
	GateTouched  bool      `json:"gateTouched"` // integrity gate matched → rejected without the verifier
	TouchedFiles []string  `json:"touchedFiles,omitempty"`
	// Reason is the machine-readable "why" of a negative verdict (stale_base,
	// null_agent_floor, policy_drift, integrity_gate, verify_failed) and is empty
	// on a pass — so a client never has to parse Detail to know what happened.
	Reason         RejectionReason `json:"reason,omitempty"`
	VerifierCalled bool            `json:"verifierCalled"` // false when the gate short-circuited
	Verified       bool            `json:"verified"`       // final verdict: reached `verified`
	Detail         string          `json:"detail"`
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
	VerifyTask(id string) (VerifyResult, error)
	RetryTask(id string, req RetryRequest) (RetryResult, error)
	ReplanTask(id string, req ReplanRequest) (Task, error)
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
	budgets   BudgetSource    // may be nil (then DefaultBudget() for every project)

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
		inflight: map[string]inflightOp{},
	}
}

// beginOp claims a task for a long operation. s.mu must be held. It refuses
// rather than blocks: a caller that has to wait an hour for a lock is
// indistinguishable from a hung daemon, so the plane says no immediately and
// says why.
func (s *Service) beginOp(taskID string, op inflightOp) error {
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

// setOpJob records the job id on an in-flight op once it exists. s.mu must be held.
func (s *Service) setOpJob(taskID, jobID string) {
	if op, ok := s.inflight[taskID]; ok {
		op.jobID = jobID
		s.inflight[taskID] = op
	}
}

// endOp releases a task's in-flight claim, taking s.mu itself.
func (s *Service) endOp(taskID string) {
	s.mu.Lock()
	delete(s.inflight, taskID)
	s.mu.Unlock()
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
func (s *Service) SetImageDigester(d ImageDigester) { s.digester = d }

// callerActor returns the actor label for the request currently being served.
//
// The label is decided HERE, at the service boundary, not deep in the store —
// the store's job is to record who acted, never to invent it. Today every client
// of control.sock is the human `daedalus task` CLI, so this is ActorHuman.
//
// Sprint 60 (`guild-control-mcp`) puts an agent on the same socket, and at that
// point this must become a per-request value derived from the TRANSPORT — peer
// credentials, or a separate socket per class of caller — and never from
// anything in the request body. A client that could name its own actor could
// claim to be human, which is worse than not labelling at all. That threading
// touches every TaskAPI signature, so it is deliberately left to Sprint 60; this
// method is the single place it has to land.
func (s *Service) callerActor() string { return ActorHuman }

// governanceMeta is the event annotation for an explicitly-requested governance
// act (retry / replan / rebase).
func (s *Service) governanceMeta() EventMeta {
	return EventMeta{Kind: EventGovernance, Actor: s.callerActor()}
}

// SetBudgetSource installs the per-project budget ceiling source (the daemon
// passes FileBudgetPolicy; tests pass a static one). Nil means every project gets
// DefaultBudget().
func (s *Service) SetBudgetSource(b BudgetSource) { s.budgets = b }

// budgetCeiling resolves the ceiling for a project: the configured source, or
// the built-in default when none is installed.
func (s *Service) budgetCeiling(project string) Budget {
	if s.budgets == nil {
		return DefaultBudget()
	}
	return s.budgets.BudgetFor(project).withDefaults(DefaultBudget())
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
func (s *Service) CreateTask(req CreateTaskRequest) (Task, error) {
	if req.Project == "" {
		return Task{}, fmt.Errorf("control: project is required")
	}
	if req.Objective == "" {
		return Task{}, fmt.Errorf("control: objective is required")
	}
	dir, err := s.projects.ProjectDir(req.Project)
	if err != nil {
		return Task{}, err
	}
	baseSHA, err := ReadHeadSHA(dir)
	if err != nil {
		return Task{}, err
	}
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
	t, err := s.store.CreateTask(NewTask{
		Project: req.Project, Objective: req.Objective, AcceptanceRef: req.Acceptance,
		BaseSHA: baseSHA, AcceptanceHash: policy.Hash(), Budget: budget,
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
	view := StatusView{Task: t}
	for _, j := range jobs {
		arts, err := s.store.ListArtifactsForJob(j.ID)
		if err != nil {
			return StatusView{}, err
		}
		view.Jobs = append(view.Jobs, JobView{Job: j, Artifacts: arts})
	}
	return view, nil
}

// CancelTask cancels a task and any non-terminal jobs, reclaiming their
// worktrees. The task transition is the authority; job/worktree cleanup is
// best-effort follow-through.
func (s *Service) CancelTask(id string) (Task, error) {
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
	return s.store.TransitionTaskWith(id, StateCancelled, false,
		EventMeta{Actor: s.callerActor()}, "cancelled")
}

// DispatchTask runs one headless Job attempt for a task: create the Job, add its
// isolated worktree, run the agent (process exit is the boundary), capture the
// tree as output_snapshot (even on failure), then classify — only ExecSuccess
// promotes to a candidate Artifact; failure/timeout/cancel are terminal and
// reclaim the worktree.
func (s *Service) DispatchTask(id string) (DispatchResult, error) {
	s.mu.Lock()
	prep, err := s.prepareDispatch(id)
	s.mu.Unlock()
	if err != nil {
		return DispatchResult{}, err
	}
	return s.runDispatch(prep)
}

// dispatchPrep is everything the run phase needs, resolved while s.mu was held.
type dispatchPrep struct {
	task     Task
	job      Job
	repoDir  string
	worktree string
}

// prepareDispatch does the whole locked half of a dispatch: guards, budget,
// state transitions, the Job row, and the isolated worktree. s.mu MUST be held.
// On success the task is claimed in `inflight` and the caller owns the claim —
// it must call runDispatch (which releases it) or endOp.
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
	if task.State != StatePlanned && task.State != StateQueued && task.State != StateRejected {
		return dispatchPrep{}, fmt.Errorf("control: task %s is %s, not dispatchable (want planned/queued/rejected)", id, task.State)
	}
	// Claim the task before anything else moves: with the lock released across the
	// run, the task state alone is no longer enough to keep two dispatches apart.
	if err := s.beginOp(id, inflightOp{kind: "dispatch", project: task.Project}); err != nil {
		return dispatchPrep{}, err
	}
	// From here on every failure path must release the claim.
	release := func() { delete(s.inflight, id) }

	// Budget gate, BEFORE any state change or side-effect: an over-budget dispatch
	// must leave the task exactly as it found it.
	if err := s.checkDispatchBudget(task); err != nil {
		release()
		return dispatchPrep{}, err
	}
	repoDir, err := s.projects.ProjectDir(task.Project)
	if err != nil {
		release()
		return dispatchPrep{}, err
	}

	// Drive the task into working: planned/rejected → queued → working.
	if task.State == StatePlanned {
		if _, err := s.store.TransitionTask(id, StateQueued, false, "dispatch"); err != nil {
			release()
			return dispatchPrep{}, err
		}
	} else if task.State == StateRejected {
		if _, err := s.store.TransitionTask(id, StateQueued, false, "retry after rejection"); err != nil {
			release()
			return dispatchPrep{}, err
		}
	}
	if _, err := s.store.TransitionTask(id, StateWorking, false, "dispatch: worktree + run"); err != nil {
		release()
		return dispatchPrep{}, err
	}

	// Create the Job (records base_sha, runner, the wall-clock budget) in working.
	job, err := s.store.CreateJob(id, task.BaseSHA, "claude", task.Budget.WallClockSeconds, StateWorking)
	if err != nil {
		release()
		return dispatchPrep{}, err
	}
	s.setOpJob(id, job.ID)

	// Isolated worktree at base_sha on the deterministic branch.
	wtPath, err := s.worktrees.Add(repoDir, id, job.ID, task.BaseSHA)
	if err != nil {
		// Could not even prepare the workspace: fail the job + task, no worktree.
		s.failJobAndTask(id, job.ID, ExecFailed, "", "worktree add failed: "+err.Error())
		release()
		return dispatchPrep{}, err
	}
	return dispatchPrep{task: task, job: job, repoDir: repoDir, worktree: wtPath}, nil
}

// runDispatch runs the agent WITHOUT s.mu held (process exit is the boundary,
// bounded by the wall-clock budget), then retakes the lock to capture, classify
// and promote. It always releases the task's in-flight claim.
//
// Not holding the lock across the run is what keeps `task cancel` and the
// reconcile loop responsive while a Job runs — at the cost of having to cope with
// the Task being cancelled underneath us, which the post-run bookkeeping does
// explicitly rather than by fighting the state machine.
func (s *Service) runDispatch(prep dispatchPrep) (DispatchResult, error) {
	task, job := prep.task, prep.job
	defer s.endOp(task.ID)

	outcome := runUnderWallClock(s.runner, JobSpec{
		TaskID: task.ID, JobID: job.ID, Project: task.Project, Objective: task.Objective,
		Runner: "claude", Budget: task.Budget.WallClockSeconds, BaseSHA: task.BaseSHA,
		WorktreeDir: prep.worktree,
	})

	s.mu.Lock()
	defer s.mu.Unlock()

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
		s.terminate(task.ID, job.ID, prep.repoDir, StateFailed, note(outcome, "failed"))
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
	if b.Concurrency > 0 {
		running, err := s.store.CountRunningJobsForProject(task.Project)
		if err != nil {
			return err
		}
		// The DB count alone is authoritative here: prepareDispatch holds s.mu from
		// the guards through the Job insert without ever releasing it, so no other
		// dispatch can observe the window between claiming a task and its Job row
		// existing. (Counting in-flight claims as well would double-count this very
		// dispatch and refuse every first attempt.)
		if running >= b.Concurrency {
			return s.refuse("task", task.ID, EventBudget, ReasonConcurrencyExceeded, fmt.Sprintf(
				"project %q already has %d running job(s) (concurrency budget %d)",
				task.Project, running, b.Concurrency))
		}
	}
	return nil
}

// runUnderWallClock runs one attempt bounded by the Job's wall-clock budget, the
// first of §6's "strongly enforceable" axes. The runner is handed a deadline
// context AND raced against it, so the plane's verdict does not depend on the
// runner cooperating: an overrun is classified execution_result=timeout on the
// spot and the Job is terminated.
//
// Honest limit, worth stating: the plane can guarantee its own bookkeeping and
// the context cancellation, not the death of a process it did not fork. A runner
// that ignores its context keeps running in the background until it exits (the
// goroutine below is parked on a buffered channel, so nothing here blocks or
// leaks a lock, but the container may outlive the verdict). Killing the
// underlying container needs a runner that honours the context — the real
// CoordinatorRunner is exec-based and does not abort a command mid-flight today.
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
func (s *Service) VerifyTask(id string) (VerifyResult, error) {
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
		return VerifyResult{}, fmt.Errorf("control: task %s is %s, not verifiable (want candidate)", id, task.State)
	}
	job, ok, err := s.candidateJob(id)
	if err != nil {
		return VerifyResult{}, err
	}
	if !ok {
		return VerifyResult{}, fmt.Errorf("control: task %s has no candidate job to verify", id)
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

	// Null-agent floor (§6): an artifact identical to base_sha is no change at all
	// — it must never verify as "done". Reject before any gate/verifier work so a
	// do-nothing (or capture-failed) job can't earn a vacuous pass.
	if job.OutputSnapshot == "" || job.OutputSnapshot == task.BaseSHA {
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
	stale, tip, err := IsStaleBase(repoDir, task.BaseSHA)
	if err != nil {
		return VerifyResult{}, err
	}
	if stale {
		// The message deliberately does NOT tell the operator to run --rebase. The
		// tip may have moved *because a Job moved it* (a linked worktree shares the
		// parent repo's refs — see TargetTipSHA), and --rebase re-freezes the
		// acceptance oracle at that tip. Reflexively recommending it would turn a
		// detection into a self-completing attack. So: name the condition, name the
		// remedy, and say to look first. The plane also refuses the unsafe case.
		note := fmt.Sprintf("stale base: artifact built on %s but the project tip is now %s — it must be rebased onto the current tip and re-verified. "+
			"Inspect that tip before rebasing (`git log %s`): `daedalus task retry %s --rebase` re-freezes the acceptance policy at it",
			shortSHA(task.BaseSHA), shortSHA(tip), shortSHA(tip), id)
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
	if task.AcceptanceHash != "" && policy.Hash() != task.AcceptanceHash {
		note := "acceptance policy hash drift since base_sha — rejected"
		res := s.doReject(task, job, art, repoDir, ReasonPolicyDrift, note)
		res.Detail = note
		return res, nil
	}

	// INTEGRITY GATE FIRST — before any VerifyRunner call.
	touched, files, err := DiffTouchesAcceptanceFiles(repoDir, task.BaseSHA, job.OutputSnapshot, policy.AcceptanceGlobs)
	if err != nil {
		return VerifyResult{}, err
	}
	if touched {
		note := "integrity gate: job diff edits frozen acceptance files: " + strings.Join(files, ", ")
		res := s.doReject(task, job, art, repoDir, ReasonIntegrityGate, note)
		res.GateTouched = true
		res.TouchedFiles = files
		res.VerifierCalled = false
		res.Detail = note
		return res, nil
	}

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
	if err := s.beginOp(id, inflightOp{kind: "verify", jobID: job.ID, project: task.Project}); err != nil {
		return VerifyResult{}, err
	}

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
	spec := VerifySpec{
		TaskID: id, JobID: job.ID, Project: task.Project, RepoDir: repoDir,
		BaseSHA: task.BaseSHA, HeadSHA: job.OutputSnapshot,
		Branch: BranchName(id, job.ID), Policy: policy, ImageDigest: task.ImageDigest,
	}
	// The unlock/relock is a closure with `defer s.mu.Lock()` rather than a bare
	// pair, so a PANIC inside the verifier still leaves the mutex held on the way
	// out — which is what lets the deferred stranded-recovery above run correctly
	// instead of deadlocking or unlocking an unlocked mutex. net/http recovers a
	// handler panic, so the daemon survives; the task must survive with it.
	outcome := func() VerifyOutcome {
		s.mu.Unlock()
		defer s.mu.Lock()
		return s.verifier.Verify(context.Background(), spec)
	}()
	delete(s.inflight, id)

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
		res := s.doReject(task, job, art, repoDir, ReasonVerifyFailed, withDetail("verify failed", outcome.Detail))
		res.VerifierCalled = true
		res.Detail = outcome.Detail
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
	return VerifyResult{Job: jb, Task: tk, Artifact: art, VerifierCalled: true, Verified: true, Detail: outcome.Detail}, nil
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
	s.mu.Lock()
	res, prep, err := s.prepareRetry(id, req)
	s.mu.Unlock()
	if err != nil {
		return RetryResult{}, err
	}
	dispatch, err := s.runDispatch(prep)
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
func (s *Service) prepareRetry(id string, req RetryRequest) (RetryResult, dispatchPrep, error) {
	task, err := s.store.GetTask(id)
	if err != nil {
		return RetryResult{}, dispatchPrep{}, err
	}
	if task.State != StateRejected {
		return RetryResult{}, dispatchPrep{}, fmt.Errorf("control: task %s is %s, not retryable (want rejected)", id, task.State)
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
		tip, err := TargetTipSHA(repoDir)
		if err != nil {
			return RetryResult{}, dispatchPrep{}, err
		}
		if tip != task.BaseSHA {
			// REFUSE a rebase onto a tip that contains this Task's own Job commits.
			// A linked worktree shares the parent repo's refs, so a Job that can reach
			// the common git dir can point the target branch at its own commit; a
			// rebase would then re-freeze the acceptance oracle onto a policy the
			// worker wrote, which is precisely what §6 forbids ("the oracle must live
			// outside the agent's write scope"). Detecting it is one merge-base per
			// attempt and does not care how the tip came to move.
			if err := s.refuseSelfAuthoredRebase(task, repoDir, tip); err != nil {
				return RetryResult{}, dispatchPrep{}, err
			}
			policy, err := ReadAcceptancePolicyAt(repoDir, tip)
			if err != nil {
				return RetryResult{}, dispatchPrep{}, err
			}
			note := fmt.Sprintf("rebase: %s → %s (acceptance policy re-frozen at the new base)",
				shortSHA(task.BaseSHA), shortSHA(tip))
			updated, err := s.store.RebaseTask(id, tip, policy.Hash(), s.governanceMeta(), note)
			if err != nil {
				return RetryResult{}, dispatchPrep{}, err
			}
			task = updated
			res.Rebased = true
		}
	}
	res.BaseSHA = task.BaseSHA

	if err := s.store.LogDecision("task", id,
		s.governanceMeta(),
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
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.Objective == "" {
		return Task{}, fmt.Errorf("control: replan requires --objective")
	}
	task, err := s.store.GetTask(id)
	if err != nil {
		return Task{}, err
	}
	if task.State != StateRejected {
		return Task{}, fmt.Errorf("control: task %s is %s, not replannable (want rejected)", id, task.State)
	}
	// A replan that could never be dispatched is worth refusing now rather than
	// leaving a `planned` task that only fails at the next dispatch.
	if err := s.checkDispatchBudget(task); err != nil {
		return Task{}, err
	}
	return s.store.ReplanTask(id, req.Objective, s.governanceMeta(),
		fmt.Sprintf("replan: objective %q → %q", task.Objective, req.Objective))
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
	jobs, err := s.store.ListJobsForTask(taskID)
	if err != nil {
		return Job{}, false, err
	}
	for i := len(jobs) - 1; i >= 0; i-- {
		if jobs[i].State == StateCandidate {
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

func note(o RunOutcome, base string) string {
	if o.Detail == "" {
		return base
	}
	return base + " (" + o.Detail + ")"
}

// ReconcileReport summarises what a reconcile pass changed. Returned for tests
// and daemon logging.
type ReconcileReport struct {
	FailedVanished    []string // job ids failed because their run was gone
	RemovedOrphans    []string // worktree job ids removed (no live non-terminal job)
	RecoveredVerifies []string // job ids returned to candidate from a stranded `verifying`
	CheckedActive     int      // non-terminal jobs examined
	SkippedUnverified int      // jobs left alone because liveness couldn't be verified
	SkippedInflight   int      // jobs left alone because this process is running them
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
		if j.State != StateWorking {
			liveWorktreeJobs[j.ID] = true
			continue
		}
		task, err := s.store.GetTask(j.TaskID)
		if err != nil {
			return rep, err
		}
		live, verifiable := s.sessionLive(task.Project)
		if !verifiable {
			rep.SkippedUnverified++
			liveWorktreeJobs[j.ID] = true // don't reclaim what we can't judge
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
		s.terminate(j.TaskID, j.ID, repoDir, StateFailed, "reconcile: no live session")
		rep.FailedVanished = append(rep.FailedVanished, j.ID)
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
