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
	// ReasonOverBudget (§6 — "budget too high → REJECTED"). Raising a ceiling is a
	// host-side act, out of reach of anything speaking to control.sock.
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

	// mu serialises Dispatch, Verify and Reconcile: V1 is one active Job per
	// project with no parallelism, and a single SQLite writer conn. It is NOT
	// held across the (potentially long) runner.Run — only around the DB
	// bookkeeping.
	mu sync.Mutex
}

// NewService wires a Service. runner/worktrees are required for Dispatch;
// projects for CreateTask; verifier for VerifyTask; sessions may be nil.
func NewService(store *Store, projects ProjectResolver, worktrees *WorktreeManager, runner AgentRunner, verifier VerifyRunner, sessions SessionObserver) *Service {
	return &Service{store: store, projects: projects, worktrees: worktrees, runner: runner, verifier: verifier, sessions: sessions}
}

// Store exposes the underlying store (daemon reconcile ticker, tests).
func (s *Service) Store() *Store { return s.store }

// SetImageDigester installs the image-digest seam (Docker-dependent) after
// construction, so NewService callers that don't pin images (host tests) stay
// unchanged. Nil disables digest pinning.
func (s *Service) SetImageDigester(d ImageDigester) { s.digester = d }

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
		EventMeta{Actor: ActorHuman}, "cancelled via CLI")
}

// DispatchTask runs one headless Job attempt for a task: create the Job, add its
// isolated worktree, run the agent (process exit is the boundary), capture the
// tree as output_snapshot (even on failure), then classify — only ExecSuccess
// promotes to a candidate Artifact; failure/timeout/cancel are terminal and
// reclaim the worktree.
func (s *Service) DispatchTask(id string) (DispatchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dispatchLocked(id)
}

// dispatchLocked is DispatchTask's body, callable from another already-locked
// governance op (RetryTask). s.mu must be held.
func (s *Service) dispatchLocked(id string) (DispatchResult, error) {
	if s.runner == nil || s.worktrees == nil {
		return DispatchResult{}, fmt.Errorf("control: dispatch not configured (no runner/worktrees)")
	}

	task, err := s.store.GetTask(id)
	if err != nil {
		return DispatchResult{}, err
	}
	// Dispatchable from planned/queued (first attempt) or rejected (retry after a
	// failed verify — rejected → queued is the retry path from §6's ladder).
	if task.State != StatePlanned && task.State != StateQueued && task.State != StateRejected {
		return DispatchResult{}, fmt.Errorf("control: task %s is %s, not dispatchable (want planned/queued/rejected)", id, task.State)
	}
	// Budget gate, BEFORE any state change or side-effect: an over-budget dispatch
	// must leave the task exactly as it found it.
	if err := s.checkDispatchBudget(task); err != nil {
		return DispatchResult{}, err
	}
	repoDir, err := s.projects.ProjectDir(task.Project)
	if err != nil {
		return DispatchResult{}, err
	}

	// Drive the task into working: planned/rejected → queued → working.
	if task.State == StatePlanned {
		if _, err := s.store.TransitionTask(id, StateQueued, false, "dispatch"); err != nil {
			return DispatchResult{}, err
		}
	} else if task.State == StateRejected {
		if _, err := s.store.TransitionTask(id, StateQueued, false, "retry after rejection"); err != nil {
			return DispatchResult{}, err
		}
	}
	if _, err := s.store.TransitionTask(id, StateWorking, false, "dispatch: worktree + run"); err != nil {
		return DispatchResult{}, err
	}

	// Create the Job (records base_sha, runner, the wall-clock budget) in working.
	job, err := s.store.CreateJob(id, task.BaseSHA, "claude", task.Budget.WallClockSeconds, StateWorking)
	if err != nil {
		return DispatchResult{}, err
	}

	// Isolated worktree at base_sha on the deterministic branch.
	wtPath, err := s.worktrees.Add(repoDir, id, job.ID, task.BaseSHA)
	if err != nil {
		// Could not even prepare the workspace: fail the job + task, no worktree.
		s.failJobAndTask(id, job.ID, ExecFailed, "", "worktree add failed: "+err.Error())
		return DispatchResult{}, err
	}

	// Run the agent to process exit, under the wall-clock budget. NOT under a DB
	// transaction — the store is touched only before and after.
	outcome := runUnderWallClock(s.runner, JobSpec{
		TaskID: id, JobID: job.ID, Project: task.Project, Objective: task.Objective,
		Runner: "claude", Budget: task.Budget.WallClockSeconds, BaseSHA: task.BaseSHA, WorktreeDir: wtPath,
	})

	// Capture the tree (salvage snapshot even on failure). Best-effort: a capture
	// failure leaves output_snapshot empty but does not lose the outcome.
	headSHA, capErr := s.worktrees.Capture(wtPath)
	if capErr != nil {
		log.Printf("control: capture worktree for %s: %v", job.ID, capErr)
	}
	if _, err := s.store.SetJobExecutionResult(job.ID, outcome.Result, headSHA); err != nil {
		return DispatchResult{}, err
	}

	switch outcome.Result {
	case ExecSuccess:
		// Promote: job → candidate, create the candidate Artifact, task → candidate.
		// The worktree is KEPT — candidate is non-terminal; the branch/commit must
		// remain available for the (future) clean-verifier step.
		if _, err := s.store.TransitionJob(job.ID, StateCandidate, false, note(outcome, "success → candidate")); err != nil {
			return DispatchResult{}, err
		}
		art, err := s.store.CreateArtifact(job.ID, task.BaseSHA, headSHA, BranchName(id, job.ID))
		if err != nil {
			return DispatchResult{}, err
		}
		if _, err := s.store.TransitionTask(id, StateCandidate, false, "job candidate"); err != nil {
			return DispatchResult{}, err
		}
		j, _ := s.store.GetJob(job.ID)
		return DispatchResult{Job: j, Artifact: &art}, nil

	case ExecCancelled:
		s.terminate(id, job.ID, repoDir, StateCancelled, note(outcome, "cancelled"))
	default: // ExecFailed, ExecTimeout
		s.terminate(id, job.ID, repoDir, StateFailed, note(outcome, "failed"))
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
		used, err := s.store.CountTaskTransitionsTo(id, StateVerifying)
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
		note := fmt.Sprintf("stale base: artifact built on %s but the project tip is now %s — rebase + re-verify (daedalus task retry %s --rebase)",
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
	if s.verifier == nil {
		return VerifyResult{}, fmt.Errorf("control: no verifier configured")
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

	outcome := s.verifier.Verify(context.Background(), VerifySpec{
		TaskID: id, JobID: job.ID, Project: task.Project, RepoDir: repoDir,
		BaseSHA: task.BaseSHA, HeadSHA: job.OutputSnapshot,
		Branch: BranchName(id, job.ID), Policy: policy, ImageDigest: task.ImageDigest,
	})

	if !outcome.Passed {
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
	defer s.mu.Unlock()

	task, err := s.store.GetTask(id)
	if err != nil {
		return RetryResult{}, err
	}
	if task.State != StateRejected {
		return RetryResult{}, fmt.Errorf("control: task %s is %s, not retryable (want rejected)", id, task.State)
	}
	// Re-check the budget up front so an exhausted Task is refused before the
	// rebase touches anything.
	if err := s.checkDispatchBudget(task); err != nil {
		return RetryResult{}, err
	}

	used, err := s.store.CountJobsForTask(id)
	if err != nil {
		return RetryResult{}, err
	}
	res := RetryResult{Attempt: used + 1, MaxAttempts: task.Budget.MaxAttempts}

	if req.Rebase {
		repoDir, err := s.projects.ProjectDir(task.Project)
		if err != nil {
			return RetryResult{}, err
		}
		tip, err := TargetTipSHA(repoDir)
		if err != nil {
			return RetryResult{}, err
		}
		if tip != task.BaseSHA {
			policy, err := ReadAcceptancePolicyAt(repoDir, tip)
			if err != nil {
				return RetryResult{}, err
			}
			note := fmt.Sprintf("rebase: %s → %s (acceptance policy re-frozen at the new base)",
				shortSHA(task.BaseSHA), shortSHA(tip))
			updated, err := s.store.RebaseTask(id, tip, policy.Hash(), note)
			if err != nil {
				return RetryResult{}, err
			}
			task = updated
			res.Rebased = true
		}
	}
	res.BaseSHA = task.BaseSHA

	if err := s.store.LogDecision("task", id,
		EventMeta{Kind: EventGovernance, Actor: ActorHuman},
		fmt.Sprintf("retry: attempt %d of %d", res.Attempt, task.Budget.MaxAttempts)); err != nil {
		log.Printf("control: logging retry of %s: %v", id, err)
	}

	dispatch, err := s.dispatchLocked(id)
	if err != nil {
		return RetryResult{}, err
	}
	res.Dispatch = dispatch
	if n, err := s.store.CountJobsForTask(id); err == nil {
		res.Attempts = n
	}
	return res, nil
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
	return s.store.ReplanTask(id, req.Objective,
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
	CheckedActive     int      // non-terminal jobs examined
	SkippedUnverified int      // jobs left alone because liveness couldn't be verified
}

// Reconcile drives observed reality toward desired (DB) state (§6, the dual-write
// fix): (1) any working Job whose coordinator session has vanished is captured,
// failed, and its worktree reclaimed; (2) any orphaned worktree (no live,
// non-terminal DB job) is removed. Idempotent via deterministic names, so
// re-running is a no-op. If session liveness cannot be verified (no observer or
// an error), the vanished-check is skipped for safety — we never fail a Job we
// can't prove is dead.
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
