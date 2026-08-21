// Copyright (C) 2026 Techdelight BV

package control

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- test doubles -------------------------------------------------------------

type mapResolver map[string]string

func (m mapResolver) ProjectDir(name string) (string, error) {
	dir, ok := m[name]
	if !ok {
		return "", &ErrNotGitRepo{Dir: name} // any error; content unused
	}
	return dir, nil
}

// fakeSessions reports session liveness. It implements BOTH observer interfaces,
// matching production: the coordinator keys a control-plane Job's session by
// JobProjectName(jobID), so per-Job liveness is the real question and the
// project-level one is only a legacy signal.
type fakeSessions struct {
	live     map[string]bool // by project (legacy)
	liveJobs map[string]bool // by job id — what reconcile actually asks
	err      error
}

func (f fakeSessions) HasSession(project string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.live[project], nil
}

func (f fakeSessions) HasSessionForJob(jobID string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.liveJobs[jobID], nil
}

// gitRepo makes a temp repo with one commit and returns its dir.
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	git(t, dir, "init")
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "init")
	return dir
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newService wires a Service over a temp DB + worktree root, with the given
// runner and (optional) sessions. The verifier defaults to a passing stub;
// pass one explicitly to override (verify tests).
func newService(t *testing.T, resolver ProjectResolver, runner AgentRunner, sessions SessionObserver, verifier ...VerifyRunner) (*Service, *WorktreeManager, *Store) {
	t.Helper()
	dataDir := t.TempDir()
	store, err := Open(filepath.Join(dataDir, "control.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	wt := NewWorktreeManager(dataDir)
	var v VerifyRunner = StubVerifyRunner{Pass: true}
	if len(verifier) > 0 {
		v = verifier[0]
	}
	svc := NewService(store, resolver, wt, runner, v, sessions)
	// Mirror the daemon, which always knows its data dir — so per-job logs (#77)
	// are exercised by every dispatch test rather than only the one that asks.
	svc.SetDataDir(dataDir)
	return svc, wt, store
}

// --- dispatch: success → candidate -------------------------------------------

func TestDispatch_Success_PromotesToCandidate(t *testing.T) {
	repo := gitRepo(t)
	svc, wt, store := newService(t, mapResolver{"app": repo}, StubRunner{Result: ExecSuccess, WriteFile: true}, nil)

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "do work"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	base := task.BaseSHA

	res, err := svc.DispatchTask(task.ID)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	// Job reached candidate, execution_result=success.
	if res.Job.State != StateCandidate {
		t.Errorf("job state = %q, want candidate", res.Job.State)
	}
	if res.Job.ExecutionResult != ExecSuccess {
		t.Errorf("execution_result = %q, want success", res.Job.ExecutionResult)
	}
	// output_snapshot is a NEW head (the stub wrote a file that Capture committed).
	if res.Job.OutputSnapshot == "" || res.Job.OutputSnapshot == base {
		t.Errorf("output_snapshot %q should differ from base %q", res.Job.OutputSnapshot, base)
	}
	// A candidate Artifact exists on the deterministic branch.
	if res.Artifact == nil {
		t.Fatal("expected a candidate artifact, got none")
	}
	wantBranch := BranchName(task.ID, res.Job.ID)
	if res.Artifact.Branch != wantBranch {
		t.Errorf("artifact branch = %q, want %q", res.Artifact.Branch, wantBranch)
	}
	if res.Artifact.HeadSHA != res.Job.OutputSnapshot {
		t.Errorf("artifact head %q != job snapshot %q", res.Artifact.HeadSHA, res.Job.OutputSnapshot)
	}
	// Task promoted to candidate.
	got, _ := store.GetTask(task.ID)
	if got.State != StateCandidate {
		t.Errorf("task state = %q, want candidate", got.State)
	}
	// Worktree KEPT on success (candidate is non-terminal) — the branch/commit
	// must remain available for the future verifier.
	if !wt.Exists(res.Job.ID) {
		t.Error("worktree should be kept on success (candidate)")
	}
	// The worktree really is isolated at the deterministic path, on the branch.
	branchOut, _ := runGit(wt.Path(res.Job.ID), "rev-parse", "--abbrev-ref", "HEAD")
	if got := trim(branchOut); got != wantBranch {
		t.Errorf("worktree branch = %q, want %q", got, wantBranch)
	}
}

// --- dispatch: failure → failed, NOT candidate -------------------------------

func TestDispatch_Failure_NotCandidate(t *testing.T) {
	repo := gitRepo(t)
	svc, wt, store := newService(t, mapResolver{"app": repo}, StubRunner{Result: ExecFailed}, nil)

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "will fail"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	res, err := svc.DispatchTask(task.ID)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if res.Job.State != StateFailed {
		t.Errorf("job state = %q, want failed", res.Job.State)
	}
	if res.Job.ExecutionResult != ExecFailed {
		t.Errorf("execution_result = %q, want failed", res.Job.ExecutionResult)
	}
	if res.Artifact != nil {
		t.Error("failed job must NOT produce a candidate artifact")
	}
	// No artifacts recorded.
	arts, _ := store.ListArtifactsForJob(res.Job.ID)
	if len(arts) != 0 {
		t.Errorf("failed job has %d artifacts, want 0", len(arts))
	}
	// The JOB failed; the TASK is REJECTED, not failed (#80). The two answer
	// different questions: nothing will resume this attempt, but nobody has said
	// the work is not worth doing — and `failed` is terminal, so routing the Task
	// there killed the objective and its unspent attempts over one bad exit. It
	// happened five times before this changed: four Tasks in the `Not logged in`
	// era, and T-15 four seconds into a run with two of three attempts left.
	got, _ := store.GetTask(task.ID)
	if got.State != StateRejected {
		t.Errorf("task state = %q, want rejected — a failed attempt must leave the ladder reachable", got.State)
	}
	// And the ladder actually works from here, which is the whole point.
	if _, err := svc.RetryTask(task.ID, RetryRequest{}); err != nil {
		t.Errorf("retry after a failed job: %v", err)
	}
	if wt.Exists(res.Job.ID) {
		t.Error("worktree should be removed when the attempt ends")
	}
}

// --- dispatch guards ----------------------------------------------------------

func TestDispatch_NotDispatchableState(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)
	task, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	// Now candidate; dispatching again must be rejected.
	if _, err := svc.DispatchTask(task.ID); err == nil {
		t.Error("dispatch of a candidate task should be rejected")
	}
}

// --- reconcile: working job with no live session → failed + cleaned ----------

func TestReconcile_VanishedSession_FailsAndCleans(t *testing.T) {
	repo := gitRepo(t)
	// No live sessions: the observer says "app is not live".
	sessions := fakeSessions{live: map[string]bool{}}
	svc, wt, store := newService(t, mapResolver{"app": repo}, StubRunner{}, sessions)

	// Manually stage a "working" job with a real worktree (as if a prior daemon
	// dispatched it and then crashed mid-run).
	task, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	_, _ = store.TransitionTask(task.ID, StateQueued, false, "")
	_, _ = store.TransitionTask(task.ID, StateWorking, false, "")
	job, _ := store.CreateJob(task.ID, task.BaseSHA, "claude", 0, StateWorking)
	if _, err := wt.Add(repo, task.ID, job.ID, task.BaseSHA); err != nil {
		t.Fatalf("seed worktree: %v", err)
	}

	rep, err := svc.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(rep.FailedVanished) != 1 || rep.FailedVanished[0] != job.ID {
		t.Errorf("FailedVanished = %v, want [%s]", rep.FailedVanished, job.ID)
	}
	gotJob, _ := store.GetJob(job.ID)
	if gotJob.State != StateFailed {
		t.Errorf("job state = %q, want failed", gotJob.State)
	}
	// The JOB is over; the TASK is not. Nothing was ever judged, so the objective
	// is returned to the retry ladder rather than destroyed — `failed` is terminal
	// and would take every recovery command with it.
	gotTask, _ := store.GetTask(task.ID)
	if gotTask.State != StateRejected {
		t.Errorf("task state = %q, want rejected", gotTask.State)
	}
	if wt.Exists(job.ID) {
		t.Error("vanished job's worktree should be cleaned")
	}
}

// --- reconcile: live session → adopted (left alone) --------------------------

func TestReconcile_LiveSession_Adopted(t *testing.T) {
	repo := gitRepo(t)
	// Liveness is asked per JOB, so the fake is keyed by job id — the same key the
	// coordinator uses in production (JobProjectName).
	sessions := fakeSessions{liveJobs: map[string]bool{"J-1": true}}
	svc, wt, store := newService(t, mapResolver{"app": repo}, StubRunner{}, sessions)

	task, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	_, _ = store.TransitionTask(task.ID, StateQueued, false, "")
	_, _ = store.TransitionTask(task.ID, StateWorking, false, "")
	job, _ := store.CreateJob(task.ID, task.BaseSHA, "claude", 0, StateWorking)
	_, _ = wt.Add(repo, task.ID, job.ID, task.BaseSHA)
	if job.ID != "J-1" {
		t.Fatalf("precondition: expected job J-1, got %s", job.ID)
	}

	rep, err := svc.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(rep.FailedVanished) != 0 {
		t.Errorf("live session should not be failed: %v", rep.FailedVanished)
	}
	if !wt.Exists(job.ID) {
		t.Error("adopted job's worktree should be kept")
	}
	gotJob, _ := store.GetJob(job.ID)
	if gotJob.State != StateWorking {
		t.Errorf("adopted job state = %q, want working", gotJob.State)
	}
}

// --- reconcile: unverifiable liveness → skip (safety) ------------------------

func TestReconcile_Unverifiable_Skips(t *testing.T) {
	repo := gitRepo(t)
	sessions := fakeSessions{err: os.ErrClosed} // observer can't answer
	svc, wt, store := newService(t, mapResolver{"app": repo}, StubRunner{}, sessions)

	task, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	_, _ = store.TransitionTask(task.ID, StateQueued, false, "")
	_, _ = store.TransitionTask(task.ID, StateWorking, false, "")
	job, _ := store.CreateJob(task.ID, task.BaseSHA, "claude", 0, StateWorking)
	_, _ = wt.Add(repo, task.ID, job.ID, task.BaseSHA)

	rep, err := svc.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rep.SkippedUnverified != 1 {
		t.Errorf("SkippedUnverified = %d, want 1", rep.SkippedUnverified)
	}
	if len(rep.FailedVanished) != 0 {
		t.Error("unverifiable job must not be failed")
	}
	if !wt.Exists(job.ID) {
		t.Error("unverifiable job's worktree must be kept")
	}
}

// --- reconcile: orphaned worktree (no DB row) → removed ----------------------

func TestReconcile_OrphanWorktree_Removed(t *testing.T) {
	repo := gitRepo(t)
	svc, wt, _ := newService(t, mapResolver{"app": repo}, StubRunner{}, fakeSessions{})

	// A worktree whose job id has no DB row at all (leftover from a crash).
	base, _ := ReadHeadSHA(repo)
	if _, err := wt.Add(repo, "T-99", "J-99", base); err != nil {
		t.Fatalf("seed orphan worktree: %v", err)
	}
	if !wt.Exists("J-99") {
		t.Fatal("precondition: orphan worktree should exist")
	}

	rep, err := svc.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	found := false
	for _, id := range rep.RemovedOrphans {
		if id == "J-99" {
			found = true
		}
	}
	if !found {
		t.Errorf("RemovedOrphans = %v, want to contain J-99", rep.RemovedOrphans)
	}
	if wt.Exists("J-99") {
		t.Error("orphan worktree should be removed")
	}
}

// TestReconcile_Idempotent runs reconcile twice; the second pass is a no-op.
func TestReconcile_Idempotent(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{Result: ExecSuccess, WriteFile: true}, fakeSessions{live: map[string]bool{"app": true}})
	task, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if _, err := svc.DispatchTask(task.ID); err != nil { // → candidate, worktree kept
		t.Fatalf("dispatch: %v", err)
	}
	r1, _ := svc.Reconcile()
	r2, _ := svc.Reconcile()
	if len(r1.FailedVanished)+len(r1.RemovedOrphans) != 0 {
		t.Errorf("reconcile over a healthy candidate changed things: %+v", r1)
	}
	if len(r2.RemovedOrphans) != 0 || len(r2.FailedVanished) != 0 {
		t.Errorf("second reconcile not a no-op: %+v", r2)
	}
}

func trim(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}

// --- per-job logs (#77) -------------------------------------------------------

// TestDispatch_RecordsThePerJobLogPath closes the loop the fix exists for: it is
// not enough for the runner to write a log, the plane has to be able to POINT at
// it afterwards. The row is what `task status` reads.
func TestDispatch_RecordsThePerJobLogPath(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo}, StubRunner{Result: ExecFailed}, nil)

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "do work"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	res, err := svc.DispatchTask(task.ID)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	job, err := store.GetJob(res.Job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.LogPath == "" {
		t.Fatal("a dispatched job recorded no log path — a failed Job is undiagnosable again (#77)")
	}
	// The path must resolve. A row pointing at nothing is worse than an empty one,
	// because it sends a reader looking for a file that was never there.
	body, err := os.ReadFile(job.LogPath)
	if err != nil {
		t.Fatalf("recorded log path %q does not resolve: %v", job.LogPath, err)
	}
	if len(body) == 0 {
		t.Error("the per-job log is empty — a log that records nothing is the bug, not the fix")
	}
	// Keyed by Job, not by Task: a retry must not overwrite the failed attempt's
	// account of itself.
	if !strings.Contains(job.LogPath, res.Job.ID) {
		t.Errorf("log path %q is not keyed by job id %q", job.LogPath, res.Job.ID)
	}
}

// TestDispatch_NoDataDirNoLogPath: the log is optional wiring. A service that was
// never told a data dir runs Jobs exactly as it did before the fix, and records
// no path rather than a made-up one.
func TestDispatch_NoDataDirNoLogPath(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo}, StubRunner{Result: ExecSuccess}, nil)
	svc.SetDataDir("")

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "do work"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	res, err := svc.DispatchTask(task.ID)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	job, err := store.GetJob(res.Job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.LogPath != "" {
		t.Errorf("log path = %q with no data dir, want \"\"", job.LogPath)
	}
}

// TestRecordJobLog_IgnoresAPathThatWasNeverWritten pins the existence check that
// makes a non-empty log_path a promise rather than a guess. The runner may fail
// to open the file (a read-only or full data dir); the row must then stay empty.
func TestRecordJobLog_IgnoresAPathThatWasNeverWritten(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo}, StubRunner{Result: ExecSuccess}, nil)

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "do work"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	res, err := svc.DispatchTask(task.ID)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	svc.recordJobLog(res.Job.ID, filepath.Join(t.TempDir(), "never-written.log"))

	job, err := store.GetJob(res.Job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if strings.Contains(job.LogPath, "never-written") {
		t.Errorf("log path = %q — a path with no file behind it must not be recorded", job.LogPath)
	}
}
