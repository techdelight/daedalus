// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// --- test doubles --------------------------------------------------------------

// conflictVerifier models a SEMANTIC conflict: each change is fine alone, and the
// combination is not. It fails only when the commit under test contains BOTH
// files — which is exactly the situation that cannot be detected by verifying the
// pre-merge branch, and is the reason the transaction re-verifies the merged
// result instead.
type conflictVerifier struct {
	mu    sync.Mutex
	calls []VerifySpec
}

func (v *conflictVerifier) Verify(_ context.Context, spec VerifySpec) VerifyOutcome {
	v.mu.Lock()
	v.calls = append(v.calls, spec)
	v.mu.Unlock()

	hasA := fileExistsAt(spec.RepoDir, spec.HeadSHA, "a.txt")
	hasB := fileExistsAt(spec.RepoDir, spec.HeadSHA, "b.txt")
	if hasA && hasB {
		return VerifyOutcome{Passed: false,
			Detail: "semantic conflict: a.txt and b.txt cannot both be present"}
	}
	return VerifyOutcome{Passed: true, Detail: "ok"}
}

func (v *conflictVerifier) specs() []VerifySpec {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]VerifySpec(nil), v.calls...)
}

// fileExistsAt reports whether a path is present in a commit's tree.
func fileExistsAt(repoDir, sha, path string) bool {
	_, err := runGit(repoDir, "cat-file", "-e", sha+":"+path)
	return err == nil
}

// racingVerifier advances the project's target while it is "verifying", which is
// precisely the window the compare-and-swap exists to protect: another
// integration landing between our rebase and our swap.
type racingVerifier struct {
	store   *Store
	project string
	repo    string
	t       *testing.T

	mu     sync.Mutex
	raced  bool
	landed string
}

func (v *racingVerifier) Verify(_ context.Context, spec VerifySpec) VerifyOutcome {
	v.mu.Lock()
	first := !v.raced
	v.raced = true
	v.mu.Unlock()
	if first {
		// Land an unrelated commit on the target, exactly as a concurrent
		// integration would have.
		landed := commitFileOn(v.t, v.repo, spec.BaseSHA, "other-work.txt", "another task landed")
		if _, err := v.store.AdvanceTarget(repoKeyNoT(v.repo), spec.BaseSHA, landed, "test: a concurrent integration landed"); err != nil {
			v.t.Errorf("racing verifier could not advance the target: %v", err)
		}
		v.mu.Lock()
		v.landed = landed
		v.mu.Unlock()
	}
	return VerifyOutcome{Passed: true, Detail: "ok"}
}

// commitFileOn creates a commit adding one file on top of `parent`, without
// disturbing the checkout's branch, and returns its sha.
func commitFileOn(t *testing.T, repoDir, parent, name, content string) string {
	t.Helper()
	scratch := t.TempDir()
	if out, err := runGit(repoDir, "worktree", "add", "--detach", scratch, parent); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}
	defer func() {
		_, _ = runGit(repoDir, "worktree", "remove", "--force", scratch)
		_, _ = runGit(repoDir, "worktree", "prune")
	}()
	writeFileForTest(t, scratch, name, content)
	if out, err := runGit(scratch, "add", "-A"); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := runGit(scratch, "-c", "user.email=t@t", "-c", "user.name=t",
		"commit", "-m", "add "+name); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	sha, err := runGit(scratch, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return trim(sha)
}

// verifiedTask drives a task to `verified` with a Job that writes `marker`.
func verifiedTask(t *testing.T, repo string, verifier VerifyRunner, marker string) (*Service, *Store, Task) {
	t.Helper()
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: marker}, nil, verifier)
	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "do " + marker})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	res, err := svc.VerifyTask(task.ID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !res.Verified {
		t.Fatalf("precondition: the artifact should verify alone, got %q (%s)", res.Reason, res.Detail)
	}
	got, _ := store.GetTask(task.ID)
	return svc, store, got
}

// --- the happy path ------------------------------------------------------------

func TestIntegrate_AdvancesTheTarget(t *testing.T) {
	repo := gitRepo(t)
	rv := &conflictVerifier{}
	svc, store, task := verifiedTask(t, repo, rv, "a.txt")

	before, _ := svc.Target("app")
	res, err := svc.IntegrateTask(task.ID, IntegrateRequest{})
	if err != nil {
		t.Fatalf("IntegrateTask: %v", err)
	}
	if res.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", res.Attempts)
	}
	if res.PreviousTarget != before.SHA {
		t.Errorf("previous target = %s, want %s", res.PreviousTarget, before.SHA)
	}
	if res.NewTarget == before.SHA || res.NewTarget != res.MergedSHA {
		t.Errorf("target should have advanced to the merged commit: new=%s merged=%s prev=%s",
			res.NewTarget, res.MergedSHA, before.SHA)
	}
	// The task and its job are terminal, the artifact records what landed.
	if res.Task.State != StateIntegrated {
		t.Errorf("task state = %q, want integrated", res.Task.State)
	}
	if res.Artifact == nil || res.Artifact.IntegratedSHA != res.MergedSHA {
		t.Errorf("artifact should record the landed commit, got %+v", res.Artifact)
	}
	job, _ := s2job(t, store, task.ID)
	if job.State != StateIntegrated {
		t.Errorf("job state = %q, want integrated", job.State)
	}
	// A rejected/landed Job must end terminal too, not linger as "active" forever.
	if !IsTerminal(job.State) {
		t.Errorf("job state %q is not terminal after integration", job.State)
	}
	// The plane's target row moved, and the projection ref followed.
	after, _ := svc.Target("app")
	if after.SHA != res.MergedSHA {
		t.Errorf("target = %s, want the merged commit %s", after.SHA, res.MergedSHA)
	}
	if got := trim(mustGit(t, repo, "rev-parse", targetRefName)); got != res.MergedSHA {
		t.Errorf("%s = %s, want %s", targetRefName, got, res.MergedSHA)
	}
	// The landed commit really contains the work.
	if !fileExistsAt(repo, res.MergedSHA, "a.txt") {
		t.Error("the landed commit does not contain the job's file")
	}
}

// --- the semantic conflict -----------------------------------------------------

// TestIntegrate_SemanticConflict_PassesAloneFailsMerged is the test the whole
// transaction exists for: an artifact that verifies against its own base and
// fails once combined with what landed in the meantime. No textual conflict is
// involved — the rebase succeeds cleanly and the *result* is what fails.
func TestIntegrate_SemanticConflict_PassesAloneFailsMerged(t *testing.T) {
	repo := gitRepo(t)
	rv := &conflictVerifier{}
	svc, store, task := verifiedTask(t, repo, rv, "a.txt") // verified alone: passes

	// Another integration lands b.txt on the target while this task waits.
	base := task.BaseSHA
	landed := commitFileOn(t, repo, base, "b.txt", "the other half of the conflict")
	if _, err := store.AdvanceTarget(repoKey(t, repo), base, landed, "test: another task integrated"); err != nil {
		t.Fatalf("AdvanceTarget: %v", err)
	}

	_, err := svc.IntegrateTask(task.ID, IntegrateRequest{})
	var rej *RejectionError
	if !errors.As(err, &rej) {
		t.Fatalf("IntegrateTask = %v, want a *RejectionError", err)
	}
	if rej.Reason != ReasonMergedVerifyFailed {
		t.Fatalf("reason = %q, want %q", rej.Reason, ReasonMergedVerifyFailed)
	}
	if !strings.Contains(rej.Message, "MERGED") {
		t.Errorf("the rejection should say it was the merged result that failed: %q", rej.Message)
	}

	// THE TARGET IS UNTOUCHED — nothing landed.
	after, _ := svc.Target("app")
	if after.SHA != landed {
		t.Errorf("target = %s, want %s — a failed integration must not move it", after.SHA, landed)
	}
	// The task is recoverable through the normal ladder.
	got, _ := store.GetTask(task.ID)
	if got.State != StateRejected {
		t.Errorf("task state = %q, want rejected", got.State)
	}

	// And the evidence that this is genuinely the merged-result check: the
	// verifier was asked about a commit containing BOTH files, which is not the
	// artifact's own head.
	specs := rv.specs()
	last := specs[len(specs)-1]
	if last.HeadSHA == task.BaseSHA {
		t.Error("the last verification was not of a merged commit")
	}
	if !fileExistsAt(repo, last.HeadSHA, "a.txt") || !fileExistsAt(repo, last.HeadSHA, "b.txt") {
		t.Error("the re-verified commit should contain both changes (it is the merged result)")
	}
	// The FIRST verification (the candidate) saw only the artifact's own tree.
	first := specs[0]
	if fileExistsAt(repo, first.HeadSHA, "b.txt") {
		t.Error("the pre-merge verification should not have seen the other change")
	}
}

// --- the compare-and-swap race -------------------------------------------------

// TestIntegrate_TargetMovesMidTransaction_Retries drives a genuine interleaving:
// the target advances *while the merged result is being re-verified*, so the
// compare-and-swap must lose, and the transaction must recompute against the new
// tip rather than landing a commit built on a trunk that no longer exists.
func TestIntegrate_TargetMovesMidTransaction_Retries(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil, StubVerifyRunner{Pass: true})
	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if _, err := svc.VerifyTask(task.ID); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// Swap in the racing verifier only for the integration phase.
	racer := &racingVerifier{store: store, project: "app", repo: repo, t: t}
	svc.verifier = racer

	res, err := svc.IntegrateTask(task.ID, IntegrateRequest{})
	if err != nil {
		t.Fatalf("IntegrateTask: %v", err)
	}
	if res.Attempts != 2 {
		t.Errorf("attempts = %d, want 2 (the first compare-and-swap must lose)", res.Attempts)
	}
	racer.mu.Lock()
	landed := racer.landed
	racer.mu.Unlock()
	if res.PreviousTarget != landed {
		t.Errorf("the winning attempt started from %s, want the concurrently-landed %s", res.PreviousTarget, landed)
	}
	// The final commit contains BOTH the concurrent work and ours — proof the
	// retry rebased onto the new tip instead of clobbering it.
	if !fileExistsAt(repo, res.MergedSHA, "other-work.txt") {
		t.Error("the landed commit lost the concurrently-integrated work")
	}
	if !fileExistsAt(repo, res.MergedSHA, "a.txt") {
		t.Error("the landed commit lost this task's work")
	}
	after, _ := svc.Target("app")
	if after.SHA != res.MergedSHA {
		t.Errorf("target = %s, want %s", after.SHA, res.MergedSHA)
	}
}

// TestIntegrate_ExhaustedRetries_LandsNothing: a target that keeps moving must
// end in a typed refusal, not a corrupted landing.
func TestIntegrate_ExhaustedRetries_LandsNothing(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil, StubVerifyRunner{Pass: true})
	task, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if _, err := svc.VerifyTask(task.ID); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// A verifier that advances the target on EVERY pass: the CAS can never win.
	n := 0
	svc.verifier = verifyFunc(func(spec VerifySpec) VerifyOutcome {
		n++
		landed := commitFileOn(t, repo, spec.BaseSHA, "race-"+string(rune('a'+n))+".txt", "again")
		if _, err := store.AdvanceTarget(repoKey(t, repo), spec.BaseSHA, landed, "test: yet another integration"); err != nil {
			t.Errorf("advance: %v", err)
		}
		return VerifyOutcome{Passed: true}
	})

	_, err := svc.IntegrateTask(task.ID, IntegrateRequest{})
	var rej *RejectionError
	if !errors.As(err, &rej) {
		t.Fatalf("IntegrateTask = %v, want a *RejectionError", err)
	}
	if rej.Reason != ReasonIntegrationRaced {
		t.Errorf("reason = %q, want %q", rej.Reason, ReasonIntegrationRaced)
	}
	if n != integrateAttempts {
		t.Errorf("verifier called %d times, want %d (one per attempt)", n, integrateAttempts)
	}
	// The Task is still approved and nothing of ours landed: the caller may retry.
	got, _ := store.GetTask(task.ID)
	if got.State != StateApproved {
		t.Errorf("task state = %q, want approved (a lost race must not reject the work)", got.State)
	}
	target, _ := svc.Target("app")
	if fileExistsAt(repo, target.SHA, "a.txt") {
		t.Error("the task's work landed despite the transaction failing")
	}
}

// verifyFunc adapts a function to VerifyRunner.
type verifyFunc func(VerifySpec) VerifyOutcome

func (f verifyFunc) Verify(_ context.Context, spec VerifySpec) VerifyOutcome { return f(spec) }

// --- textual conflict ----------------------------------------------------------

func TestIntegrate_RebaseConflict_Rejects(t *testing.T) {
	repo := gitRepo(t)
	// The job edits seed.txt; the target edits the same line differently.
	svc, _, store := newService(t, mapResolver{"app": repo},
		conflictingRunner{}, nil, StubVerifyRunner{Pass: true})
	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "edit the seed"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if _, err := svc.VerifyTask(task.ID); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	landed := commitFileOn(t, repo, task.BaseSHA, "seed.txt", "the other side of the conflict\n")
	if _, err := store.AdvanceTarget(repoKey(t, repo), task.BaseSHA, landed, "test: conflicting integration"); err != nil {
		t.Fatalf("AdvanceTarget: %v", err)
	}

	_, err = svc.IntegrateTask(task.ID, IntegrateRequest{})
	var rej *RejectionError
	if !errors.As(err, &rej) {
		t.Fatalf("IntegrateTask = %v, want a *RejectionError", err)
	}
	if rej.Reason != ReasonMergeConflict {
		t.Errorf("reason = %q, want %q", rej.Reason, ReasonMergeConflict)
	}
	after, _ := svc.Target("app")
	if after.SHA != landed {
		t.Errorf("target moved on a conflict: %s, want %s", after.SHA, landed)
	}
	got, _ := store.GetTask(task.ID)
	if got.State != StateRejected {
		t.Errorf("task state = %q, want rejected", got.State)
	}
	if job, _ := s2job(t, store, task.ID); job.State != StateRejected {
		t.Errorf("job state = %q, want rejected", job.State)
	}
	// No scratch worktree survives a failed rebase.
	if wts, _ := runGit(repo, "worktree", "list"); strings.Contains(wts, "integrate-") {
		t.Errorf("a rebase scratch worktree leaked:\n%s", wts)
	}
}

// conflictingRunner rewrites the repo's seed file, so the artifact and the target
// touch the same line.
type conflictingRunner struct{}

func (conflictingRunner) Run(_ context.Context, spec JobSpec) RunOutcome {
	if err := writeFileForTestNoT(spec.WorktreeDir, "seed.txt", "the job's version of the line\n"); err != nil {
		return RunOutcome{Result: ExecFailed, Detail: err.Error()}
	}
	return RunOutcome{Result: ExecSuccess}
}

// --- guards --------------------------------------------------------------------

func TestIntegrate_WrongState(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)
	task, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"}) // planned
	if _, err := svc.IntegrateTask(task.ID, IntegrateRequest{}); !errors.Is(err, ErrWrongState) {
		t.Errorf("integrating a planned task = %v, want ErrWrongState", err)
	}
	if _, err := svc.IntegrateTask("T-404", IntegrateRequest{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("integrating an unknown task = %v, want ErrNotFound", err)
	}
}

func TestIntegrate_NoVerifier_Refuses(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, nil)
	if _, err := svc.IntegrateTask("T-1", IntegrateRequest{}); err == nil {
		t.Error("integration with no verifier must refuse — the merged result could not be checked")
	}
}

// --- the rebase primitive ------------------------------------------------------

func TestRebaseOnto(t *testing.T) {
	repo := gitRepo(t)
	base, _ := ReadHeadSHA(repo)
	head := commitFileOn(t, repo, base, "work.txt", "the change")
	onto := commitFileOn(t, repo, base, "other.txt", "landed first")

	t.Run("replays onto the new base", func(t *testing.T) {
		merged, err := RebaseOnto(repo, onto, base, head, t.TempDir()+"/scratch")
		if err != nil {
			t.Fatalf("RebaseOnto: %v", err)
		}
		if !fileExistsAt(repo, merged, "work.txt") || !fileExistsAt(repo, merged, "other.txt") {
			t.Error("the rebased commit should contain both changes")
		}
		if merged == head {
			t.Error("the rebased commit should be a new sha")
		}
	})

	t.Run("already on the target is a no-op", func(t *testing.T) {
		merged, err := RebaseOnto(repo, base, base, head, t.TempDir()+"/scratch")
		if err != nil {
			t.Fatalf("RebaseOnto: %v", err)
		}
		if merged != head {
			t.Errorf("merged = %s, want the unchanged head %s", merged, head)
		}
	})

	t.Run("an empty artifact is refused", func(t *testing.T) {
		if _, err := RebaseOnto(repo, onto, head, head, t.TempDir()+"/scratch"); err == nil {
			t.Error("rebasing base==head should refuse rather than land nothing")
		}
	})

	t.Run("a conflict is typed", func(t *testing.T) {
		mine := commitFileOn(t, repo, base, "seed.txt", "mine\n")
		theirs := commitFileOn(t, repo, base, "seed.txt", "theirs\n")
		_, err := RebaseOnto(repo, theirs, base, mine, t.TempDir()+"/scratch")
		var conflict *ErrRebaseConflict
		if !errors.As(err, &conflict) {
			t.Fatalf("err = %v, want *ErrRebaseConflict", err)
		}
		if conflict.Detail == "" {
			t.Error("a conflict should carry git's explanation")
		}
	})
}

// writeFileForTest writes a file into dir, creating parent directories.
func writeFileForTest(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := writeFileForTestNoT(dir, name, content); err != nil {
		t.Fatal(err)
	}
}

// writeFileForTestNoT is the error-returning form, usable from a runner double
// that has no *testing.T.
func writeFileForTestNoT(dir, name, content string) error {
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(content), 0o644)
}

// s2job returns a task's only job.
func s2job(t *testing.T, store *Store, taskID string) (Job, bool) {
	t.Helper()
	jobs, err := store.ListJobsForTask(taskID)
	if err != nil || len(jobs) == 0 {
		t.Fatalf("ListJobsForTask(%s) = (%d jobs, %v)", taskID, len(jobs), err)
	}
	return jobs[len(jobs)-1], true
}

// --- audit follow-up (Sprint 59) -----------------------------------------------

// TestRebaseOnto_CleansUpItsScratchWorktree pins the cleanup the audit found was
// the sole mutation survivor: deleting the deferred `worktree remove` left the
// whole package green while leaking a worktree per failed integration.
func TestRebaseOnto_CleansUpItsScratchWorktree(t *testing.T) {
	repo := gitRepo(t)
	base, _ := ReadHeadSHA(repo)
	head := commitFileOn(t, repo, base, "work.txt", "the change")
	onto := commitFileOn(t, repo, base, "other.txt", "landed first")
	mine := commitFileOn(t, repo, base, "seed.txt", "mine\n")
	theirs := commitFileOn(t, repo, base, "seed.txt", "theirs\n")

	// A deterministic scratch path, as the integration transaction uses.
	scratch := filepath.Join(t.TempDir(), "integrate-J-1")

	countWorktrees := func() int {
		out, err := runGit(repo, "worktree", "list", "--porcelain")
		if err != nil {
			t.Fatalf("worktree list: %v", err)
		}
		return strings.Count(out, "worktree ")
	}
	before := countWorktrees()

	tests := []struct {
		name             string
		onto, base, head string
		wantErr          bool
	}{
		{"after a successful rebase", onto, base, head, false},
		{"after a conflicting rebase", theirs, base, mine, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RebaseOnto(repo, tc.onto, tc.base, tc.head, scratch)
			if (err != nil) != tc.wantErr {
				t.Fatalf("RebaseOnto err = %v, wantErr %v", err, tc.wantErr)
			}
			if got := countWorktrees(); got != before {
				out, _ := runGit(repo, "worktree", "list")
				t.Errorf("worktree count = %d, want %d — the scratch worktree leaked:\n%s", got, before, out)
			}
			if _, err := os.Stat(scratch); !os.IsNotExist(err) {
				t.Errorf("the scratch directory %s survived (err=%v)", scratch, err)
			}
		})
	}

	// A crashed prior attempt must not block the next one: the path is
	// deterministic, so a leftover directory is cleared rather than fatal.
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := RebaseOnto(repo, onto, base, head, scratch); err != nil {
		t.Errorf("RebaseOnto over a leftover scratch dir: %v", err)
	}
	if got := countWorktrees(); got != before {
		t.Errorf("worktree count = %d, want %d after recovering from a leftover", got, before)
	}
}

// TestIntegrate_AlreadyLanded_IsIdempotent is the regression for the audit's F8.
// The compare-and-swap commits before the Task transition, so a failure in that
// window leaves the target advanced with the Task still `approved`. Re-integrating
// must SETTLE the task, not land the same work twice.
func TestIntegrate_AlreadyLanded_IsIdempotent(t *testing.T) {
	repo := gitRepo(t)
	svc, store, task := verifiedTask(t, repo, StubVerifyRunner{Pass: true}, "a.txt")

	// Land it normally.
	first, err := svc.IntegrateTask(task.ID, IntegrateRequest{})
	if err != nil {
		t.Fatalf("IntegrateTask: %v", err)
	}
	landedTarget := first.NewTarget

	// Simulate the post-CAS failure: the target stays advanced, the Task is put
	// back to `approved` as though the transition after the swap had failed.
	if _, err := store.TransitionTaskWith(task.ID, StateApprovalRequired, false, EventMeta{}, "test: rewind"); err != nil {
		// integrated is terminal, so rewind by hand at the SQL level instead.
		if _, err := store.db.Exec(`UPDATE tasks SET state = ? WHERE id = ?`, string(StateApproved), task.ID); err != nil {
			t.Fatalf("rewinding the task: %v", err)
		}
	}
	if _, err := store.db.Exec(`UPDATE jobs SET state = ? WHERE task_id = ?`, string(StateVerified), task.ID); err != nil {
		t.Fatalf("rewinding the job: %v", err)
	}

	// Re-integrating must notice the work is already contained in the target.
	second, err := svc.IntegrateTask(task.ID, IntegrateRequest{})
	if err != nil {
		t.Fatalf("re-integration should settle, not fail: %v", err)
	}
	if second.NewTarget != landedTarget {
		t.Errorf("the target moved again: %s → %s — the work was landed twice",
			landedTarget, second.NewTarget)
	}
	if second.PreviousTarget != landedTarget {
		t.Errorf("previous target = %s, want the already-landed %s", second.PreviousTarget, landedTarget)
	}
	after, _ := store.GetTask(task.ID)
	if after.State != StateIntegrated {
		t.Errorf("task state = %q, want integrated", after.State)
	}
	// No duplicate commit: the target's history did not grow.
	count := func(sha string) int {
		out, err := runGit(repo, "rev-list", "--count", sha)
		if err != nil {
			t.Fatalf("rev-list: %v", err)
		}
		n := 0
		fmt.Sscanf(strings.TrimSpace(out), "%d", &n)
		return n
	}
	if got, want := count(second.NewTarget), count(landedTarget); got != want {
		t.Errorf("history length %d != %d — a duplicate commit was landed", got, want)
	}
}

// TestArtifactIsLanded answers by CONTENT, not by sha: a rebased commit has a
// different sha from the artifact's head, so an ancestry check would wrongly say
// "not landed" for work that plainly did.
func TestArtifactIsLanded(t *testing.T) {
	repo := gitRepo(t)
	base, _ := ReadHeadSHA(repo)
	head := commitFileOn(t, repo, base, "work.txt", "the change")
	other := commitFileOn(t, repo, base, "other.txt", "unrelated")

	// Rebase the artifact onto `other`: same content, new sha.
	merged, err := RebaseOnto(repo, other, base, head, filepath.Join(t.TempDir(), "scratch"))
	if err != nil {
		t.Fatalf("RebaseOnto: %v", err)
	}
	if merged == head {
		t.Fatal("precondition: the rebase should produce a new sha")
	}

	tests := []struct {
		name   string
		target string
		want   bool
	}{
		{"landed as a rebased commit with a different sha", merged, true},
		{"not landed on an unrelated target", other, false},
		{"not landed on its own base", base, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ArtifactIsLanded(repo, tc.target, base, head)
			if err != nil {
				t.Fatalf("ArtifactIsLanded: %v", err)
			}
			if got != tc.want {
				t.Errorf("ArtifactIsLanded = %v, want %v", got, tc.want)
			}
		})
	}
}

// --- the dependency gate ---------------------------------------------------------

// dependencyPair sets up one project with two tasks and returns the service, its
// store, and (downstream, upstream). The downstream task is dispatched and
// verified BEFORE the edge is declared, which is the case the graph used to
// record and then ignore.
func dependencyPair(t *testing.T) (*Service, *Store, Task, Task) {
	t.Helper()
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		// No MarkerName: each Job writes a job-scoped file, so the two artifacts are
		// genuinely independent and a clean rebase — not a conflict — is what the
		// gate is being tested against.
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, StubVerifyRunner{Pass: true})

	upstream, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "upstream"})
	if err != nil {
		t.Fatalf("CreateTask upstream: %v", err)
	}
	downstream, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "downstream"})
	if err != nil {
		t.Fatalf("CreateTask downstream: %v", err)
	}
	if _, err := svc.DispatchTask(downstream.ID); err != nil {
		t.Fatalf("Dispatch downstream: %v", err)
	}
	// The edge lands while the downstream task is well past `planned`.
	if _, err := svc.AddDependency(downstream.ID, upstream.ID); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	return svc, store, downstream, upstream
}

// The review's exact scenario: an edge declared after dispatch used to be
// recorded, displayed, and completely inert — the task verified and landed with
// its dependency still sitting in `planned`.
func TestIntegrate_RefusesWhileADependencyHasNotLanded(t *testing.T) {
	svc, store, downstream, upstream := dependencyPair(t)

	// Verification is deliberately NOT gated: grading an artifact against its own
	// frozen oracle says nothing about what else must land first, and refusing
	// here would spend a review cycle to learn nothing.
	res, err := svc.VerifyTask(downstream.ID)
	if err != nil {
		t.Fatalf("VerifyTask: %v", err)
	}
	if !res.Verified {
		t.Fatalf("the artifact should still verify with a dependency outstanding, got %q", res.Reason)
	}

	before, err := svc.Target("app")
	if err != nil {
		t.Fatalf("Target: %v", err)
	}
	_, err = svc.IntegrateTask(downstream.ID, IntegrateRequest{})
	if err == nil {
		t.Fatal("integration succeeded with an unlanded dependency")
	}
	var rej *RejectionError
	if !errors.As(err, &rej) {
		t.Fatalf("want a typed rejection, got %T: %v", err, err)
	}
	if rej.Reason != ReasonDependenciesUnmet {
		t.Errorf("reason = %q, want %q", rej.Reason, ReasonDependenciesUnmet)
	}
	if !strings.Contains(rej.Message, upstream.ID) {
		t.Errorf("the refusal should name what it is waiting on, got %q", rej.Message)
	}

	// Nothing moved: not the trunk, not the task.
	after, err := svc.Target("app")
	if err != nil {
		t.Fatalf("Target after: %v", err)
	}
	if after.SHA != before.SHA {
		t.Errorf("target advanced despite the refusal: %s → %s", before.SHA, after.SHA)
	}
	got, _ := store.GetTask(downstream.ID)
	if got.State == StateIntegrated {
		t.Error("task reached integrated with an unlanded dependency")
	}
}

// The gate opens when the dependency actually lands — and only then. Landing is
// also where the two pieces of work are genuinely combined: the downstream
// artifact is rebased onto a trunk that now contains the upstream one.
func TestIntegrate_ProceedsOnceTheDependencyLands(t *testing.T) {
	svc, store, downstream, upstream := dependencyPair(t)
	if _, err := svc.VerifyTask(downstream.ID); err != nil {
		t.Fatalf("VerifyTask downstream: %v", err)
	}
	if _, err := svc.IntegrateTask(downstream.ID, IntegrateRequest{}); err == nil {
		t.Fatal("precondition: integration should be refused before the dependency lands")
	}

	// Land the upstream task.
	if _, err := svc.DispatchTask(upstream.ID); err != nil {
		t.Fatalf("Dispatch upstream: %v", err)
	}
	if _, err := svc.VerifyTask(upstream.ID); err != nil {
		t.Fatalf("VerifyTask upstream: %v", err)
	}
	if _, err := svc.IntegrateTask(upstream.ID, IntegrateRequest{}); err != nil {
		t.Fatalf("IntegrateTask upstream: %v", err)
	}

	res, err := svc.IntegrateTask(downstream.ID, IntegrateRequest{})
	if err != nil {
		t.Fatalf("IntegrateTask downstream after the dependency landed: %v", err)
	}
	if res.Task.State != StateIntegrated {
		t.Errorf("task state = %q, want integrated", res.Task.State)
	}
	got, _ := store.GetTask(upstream.ID)
	if got.State != StateIntegrated {
		t.Fatalf("precondition: upstream state = %q, want integrated", got.State)
	}
}

// A dependency that can never be satisfied must say so, rather than reading as
// "wait a bit longer" — the two need different actions from an operator.
func TestIntegrate_NamesAnUnsatisfiableDependency(t *testing.T) {
	svc, _, downstream, upstream := dependencyPair(t)
	if _, err := svc.VerifyTask(downstream.ID); err != nil {
		t.Fatalf("VerifyTask: %v", err)
	}
	if _, err := svc.CancelTask(upstream.ID); err != nil {
		t.Fatalf("CancelTask upstream: %v", err)
	}

	_, err := svc.IntegrateTask(downstream.ID, IntegrateRequest{})
	var rej *RejectionError
	if !errors.As(err, &rej) {
		t.Fatalf("want a typed rejection, got %T: %v", err, err)
	}
	if !strings.Contains(rej.Message, "never be satisfied") {
		t.Errorf("refusal should distinguish unsatisfiable from merely unmet, got %q", rej.Message)
	}
}

// --- --into-branch ---------------------------------------------------------------

// approveAndIntegrate drives a verified Task through the approval gate and lands
// it with the given request.
func approveAndIntegrate(t *testing.T, svc *Service, id string, req IntegrateRequest) IntegrationResult {
	t.Helper()
	if _, err := svc.ApproveTask(id, ""); err != nil {
		t.Fatalf("ApproveTask: %v", err)
	}
	res, err := svc.IntegrateTask(id, req)
	if err != nil {
		t.Fatalf("IntegrateTask: %v", err)
	}
	return res
}

// TestIntegrate_IntoBranch_FastForwardsTheCheckout is the answer to "I integrated
// it and I cannot see any changes": by design the plane lands on
// refs/daedalus/target, which nobody checks out, so a branch never moves on its
// own. --into-branch opts into the courtesy.
func TestIntegrate_IntoBranch_FastForwardsTheCheckout(t *testing.T) {
	repo := gitRepo(t)
	svc, _, task := verifiedTask(t, repo, &conflictVerifier{}, "a.txt")

	branchBefore := trim(mustGit(t, repo, "rev-parse", "HEAD"))
	res := approveAndIntegrate(t, svc, task.ID, IntegrateRequest{IntoBranch: true})

	if !res.BranchAdvanced {
		t.Fatalf("branch was not advanced: %q", res.BranchNote)
	}
	head := trim(mustGit(t, repo, "rev-parse", "HEAD"))
	if head != res.NewTarget {
		t.Errorf("HEAD = %s, want the landed target %s", shortSHA(head), shortSHA(res.NewTarget))
	}
	if head == branchBefore {
		t.Error("HEAD did not move at all")
	}
	if res.BranchNote == "" {
		t.Error("a successful advance must still say what it did — silence is the complaint this feature answers")
	}
}

// TestIntegrate_WithoutIntoBranch_LeavesTheCheckoutAlone pins the default, which
// is the property the target ref exists to provide.
func TestIntegrate_WithoutIntoBranch_LeavesTheCheckoutAlone(t *testing.T) {
	repo := gitRepo(t)
	svc, _, task := verifiedTask(t, repo, &conflictVerifier{}, "a.txt")

	before := trim(mustGit(t, repo, "rev-parse", "HEAD"))
	res := approveAndIntegrate(t, svc, task.ID, IntegrateRequest{})

	if after := trim(mustGit(t, repo, "rev-parse", "HEAD")); after != before {
		t.Errorf("HEAD moved from %s to %s without --into-branch", shortSHA(before), shortSHA(after))
	}
	if res.BranchAdvanced || res.BranchNote != "" {
		t.Errorf("nothing should be reported about a branch nobody asked to move: advanced=%v note=%q",
			res.BranchAdvanced, res.BranchNote)
	}
	// The landing itself still happened, and is still reachable.
	if trim(mustGit(t, repo, "rev-parse", targetRefName)) != res.NewTarget {
		t.Error("the landed commit should be reachable through the projection ref")
	}
}

// TestIntegrate_IntoBranch_RefusesToTouchADirtyTree is the guard that matters
// most: the courtesy must never cost somebody uncommitted work, and — equally
// important — refusing it must NOT read as a failed integration.
func TestIntegrate_IntoBranch_RefusesToTouchADirtyTree(t *testing.T) {
	repo := gitRepo(t)
	svc, store, task := verifiedTask(t, repo, &conflictVerifier{}, "a.txt")

	// An edit in progress, of the kind anyone might have open.
	if err := os.WriteFile(filepath.Join(repo, "seed.txt"), []byte("work in progress"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := trim(mustGit(t, repo, "rev-parse", "HEAD"))

	res := approveAndIntegrate(t, svc, task.ID, IntegrateRequest{IntoBranch: true})

	if res.BranchAdvanced {
		t.Error("a dirty working tree must never be fast-forwarded over")
	}
	if after := trim(mustGit(t, repo, "rev-parse", "HEAD")); after != before {
		t.Errorf("HEAD moved despite the dirty tree: %s → %s", shortSHA(before), shortSHA(after))
	}
	body, err := os.ReadFile(filepath.Join(repo, "seed.txt"))
	if err != nil || string(body) != "work in progress" {
		t.Errorf("the uncommitted edit was not preserved: %q (%v)", body, err)
	}
	// And the landing itself succeeded regardless — this is the whole reason the
	// branch step sits outside the transaction.
	got, _ := store.GetTask(task.ID)
	if got.State != StateIntegrated {
		t.Errorf("task state = %s, want integrated — a refused courtesy must not unland the work", got.State)
	}
	if res.NewTarget == res.PreviousTarget {
		t.Error("the target should still have advanced")
	}
}

// TestIntegrate_IntoBranch_RefusesADivergedBranch — winding forward is
// impossible, and anything else is a merge decision belonging to the operator.
//
// Note what this does and does not pin. The SAFETY comes from `git merge
// --ff-only`, which refuses a non-fast-forward by itself; deleting the ancestor
// check in advanceCheckoutBranch leaves this test green, which was verified. So
// the note assertion below is the part that earns its keep: it pins the useful
// message, which is the only thing the redundant check actually buys.
func TestIntegrate_IntoBranch_RefusesADivergedBranch(t *testing.T) {
	repo := gitRepo(t)
	svc, store, task := verifiedTask(t, repo, &conflictVerifier{}, "a.txt")

	// A commit on the checkout's branch that the landed target will not contain.
	if err := os.WriteFile(filepath.Join(repo, "local-only.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "add", "-A")
	mustGit(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-m", "local work")
	before := trim(mustGit(t, repo, "rev-parse", "HEAD"))

	res := approveAndIntegrate(t, svc, task.ID, IntegrateRequest{IntoBranch: true})

	if res.BranchAdvanced {
		t.Error("a diverged branch must not be advanced")
	}
	if after := trim(mustGit(t, repo, "rev-parse", "HEAD")); after != before {
		t.Errorf("HEAD moved despite divergence: %s → %s", shortSHA(before), shortSHA(after))
	}
	if got, _ := store.GetTask(task.ID); got.State != StateIntegrated {
		t.Errorf("task state = %s, want integrated", got.State)
	}
	if !strings.Contains(res.BranchNote, "diverged") || !strings.Contains(res.BranchNote, targetRefName) {
		t.Errorf("BranchNote = %q; it should say the branch diverged and name the ref to merge, "+
			"rather than leaving the operator with git's bare refusal", res.BranchNote)
	}
}
