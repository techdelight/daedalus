// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

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
	Job            Job       `json:"job"`
	Task           Task      `json:"task"`
	Artifact       *Artifact `json:"artifact,omitempty"`
	GateTouched    bool      `json:"gateTouched"` // integrity gate matched → rejected without the verifier
	TouchedFiles   []string  `json:"touchedFiles,omitempty"`
	VerifierCalled bool      `json:"verifierCalled"` // false when the gate short-circuited
	Verified       bool      `json:"verified"`       // final verdict: reached `verified`
	Detail         string    `json:"detail"`
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
	t, err := s.store.CreateTask(req.Project, req.Objective, req.Acceptance, baseSHA, policy.Hash(), StatePlanned)
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
			if _, err := s.store.TransitionJob(j.ID, StateCancelled, false, "task cancelled"); err != nil {
				log.Printf("control: cancel job %s: %v", j.ID, err)
			}
			if s.worktrees != nil {
				_ = s.worktrees.Remove(repoDir, j.ID)
			}
		}
	}
	return s.store.TransitionTask(id, StateCancelled, false, "cancelled via CLI")
}

// DispatchTask runs one headless Job attempt for a task: create the Job, add its
// isolated worktree, run the agent (process exit is the boundary), capture the
// tree as output_snapshot (even on failure), then classify — only ExecSuccess
// promotes to a candidate Artifact; failure/timeout/cancel are terminal and
// reclaim the worktree.
func (s *Service) DispatchTask(id string) (DispatchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

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

	// Create the Job (records base_sha, runner, budget) in working.
	job, err := s.store.CreateJob(id, task.BaseSHA, "claude", 0, StateWorking)
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

	// Run the agent to process exit. NOT under a DB transaction — the store is
	// touched only before and after.
	outcome := s.runner.Run(context.Background(), JobSpec{
		TaskID: id, JobID: job.ID, Project: task.Project, Objective: task.Objective,
		Runner: "claude", Budget: 0, BaseSHA: task.BaseSHA, WorktreeDir: wtPath,
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

	// Null-agent floor (§6): an artifact identical to base_sha is no change at all
	// — it must never verify as "done". Reject before any gate/verifier work so a
	// do-nothing (or capture-failed) job can't earn a vacuous pass.
	if job.OutputSnapshot == "" || job.OutputSnapshot == task.BaseSHA {
		note := "null-agent floor: empty change (head == base) — nothing to verify"
		res := s.doReject(task, job, art, repoDir, note)
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
		res := s.doReject(task, job, art, repoDir, note)
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
		res := s.doReject(task, job, art, repoDir, note)
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
		res := s.doReject(task, job, art, repoDir, withDetail("verify failed", outcome.Detail))
		res.VerifierCalled = true
		res.Detail = outcome.Detail
		return res, nil
	}

	// verifying → verified (job + task); artifact verify = pass. The worktree is
	// KEPT: a verified candidate awaits approval/integration (M15).
	jb, err := s.store.TransitionJob(job.ID, StateVerified, false, withDetail("verified", outcome.Detail))
	if err != nil {
		return VerifyResult{}, err
	}
	tk, err := s.store.TransitionTask(id, StateVerified, false, "verified")
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
func (s *Service) doReject(task Task, job Job, art *Artifact, repoDir, note string) VerifyResult {
	jb, err := s.store.TransitionJob(job.ID, StateRejected, false, note)
	if err != nil {
		log.Printf("control: reject job %s: %v", job.ID, err)
		jb = job
	}
	tk, err := s.store.TransitionTask(task.ID, StateRejected, false, note)
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
	return VerifyResult{Job: jb, Task: tk, Artifact: art, Verified: false}
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
