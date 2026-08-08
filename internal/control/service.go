// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"errors"
	"fmt"
	"log"
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

// TaskAPI is the surface the CLI drives and the daemon serves. Both the
// in-process Service and the over-the-socket Client implement it, so the CLI is
// identical whether it runs the logic directly (tests) or via the daemon.
type TaskAPI interface {
	CreateTask(req CreateTaskRequest) (Task, error)
	ListTasks() ([]Task, error)
	TaskStatus(id string) (StatusView, error)
	CancelTask(id string) (Task, error)
	DispatchTask(id string) (DispatchResult, error)
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
	sessions  SessionObserver // may be nil (reconcile then skips session checks)

	// mu serialises Dispatch and Reconcile: V1 is one active Job per project
	// with no parallelism, and a single SQLite writer conn. It is NOT held
	// across the (potentially long) runner.Run — only around the DB bookkeeping.
	mu sync.Mutex
}

// NewService wires a Service. runner/worktrees are required for Dispatch;
// projects for CreateTask; sessions may be nil.
func NewService(store *Store, projects ProjectResolver, worktrees *WorktreeManager, runner AgentRunner, sessions SessionObserver) *Service {
	return &Service{store: store, projects: projects, worktrees: worktrees, runner: runner, sessions: sessions}
}

// Store exposes the underlying store (daemon reconcile ticker, tests).
func (s *Service) Store() *Store { return s.store }

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
	return s.store.CreateTask(req.Project, req.Objective, req.Acceptance, baseSHA, StatePlanned)
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
	if task.State != StatePlanned && task.State != StateQueued {
		return DispatchResult{}, fmt.Errorf("control: task %s is %s, not dispatchable (want planned/queued)", id, task.State)
	}
	repoDir, err := s.projects.ProjectDir(task.Project)
	if err != nil {
		return DispatchResult{}, err
	}

	// Drive the task into working: planned → queued → working.
	if task.State == StatePlanned {
		if _, err := s.store.TransitionTask(id, StateQueued, false, "dispatch"); err != nil {
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
