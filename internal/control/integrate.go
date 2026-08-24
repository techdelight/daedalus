// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// The integration transaction (docs/guild-master-plan.md §6, "Integration is a
// race-safe transaction, not a merge").
//
// Two artifacts that each pass verification against base A can conflict when
// combined, with no textual conflict at all — a *semantic* merge conflict. So
// landing is the merge-queue pattern, and every word of it is load-bearing:
//
//	serialize → rebase onto the current target → RE-VERIFY THE MERGED RESULT →
//	compare-and-swap the target ref (retry if it moved)
//
// The step people drop is the third one. Verifying the pre-merge branch proves
// something about a tree that will never exist; only the merged result is what
// actually lands. Re-verification runs through the same VerifyRunner seam as the
// candidate verification, so the real clean verifier is used in production and a
// fake drives the host tests.
//
// The transaction is all-or-nothing with respect to the target: every failure
// path leaves the target ref exactly where it was, and leaves the Task in a state
// from which retry/replan can make progress.

// integrateAttempts bounds the CAS retry loop. Each attempt is a full rebase +
// re-verify against a fresh target, so a bound is needed: an unbounded loop under
// a busy project would spin forever, and an operator would rather be told the
// queue is too hot than watch a request never return.
const integrateAttempts = 3

// IntegrationResult reports the outcome of an integration transaction.
// IntegrateRequest is the input to Service.IntegrateTask (and POST
// /tasks/{id}/integrate).
type IntegrateRequest struct {
	// IntoBranch additionally fast-forwards the project checkout's CURRENT branch
	// to the landed commit, once the integration itself has succeeded.
	//
	// Opt-in, because the plane's target is deliberately not a branch: it is
	// projected to `refs/daedalus/target`, which nobody checks out, so landing can
	// never disturb a working tree. That is the right default and it is also why
	// "I integrated it and my branch is unchanged" is a reasonable thing to think.
	// This closes the gap without changing what is authoritative — the branch is
	// moved to a commit the plane has ALREADY landed and verified, never to one it
	// is asked to trust.
	IntoBranch bool `json:"intoBranch,omitempty"`
}

type IntegrationResult struct {
	Task     Task      `json:"task"`
	Artifact *Artifact `json:"artifact,omitempty"`
	// MergedSHA is the commit that was actually verified and landed — the rebased
	// result, NOT the artifact's original head.
	MergedSHA string `json:"mergedSha"`
	// PreviousTarget/NewTarget bracket the compare-and-swap.
	PreviousTarget string `json:"previousTarget"`
	NewTarget      string `json:"newTarget"`
	// Attempts is how many rebase+re-verify rounds it took (>1 means the target
	// moved under us and the transaction correctly recomputed).
	Attempts int    `json:"attempts"`
	Detail   string `json:"detail"`
	// Branch reports what --into-branch did, if it was asked for. BranchNote is
	// filled whether or not it worked: a refusal here is NOT an integration
	// failure — the landing is already committed and the target already advanced —
	// so it is reported rather than returned as an error.
	Branch         string `json:"branch,omitempty"`
	BranchAdvanced bool   `json:"branchAdvanced,omitempty"`
	BranchNote     string `json:"branchNote,omitempty"`
	// BranchAdvice is the answer to "I integrated it — where is my code?", filled
	// on EVERY landing, including the default one that was never asked to move a
	// branch. It exists because that answer used to be assembled by whichever
	// surface happened to be reporting: the CLI said it, and the Ledger — which
	// receives this struct as JSON and cannot call into Go — said "landed" and
	// nothing else.
	BranchAdvice string `json:"branchAdvice,omitempty"`
}

// The one explanation of where landed work actually is. Owned by the plane, said
// by every surface, because the surprising property is the plane's: it lands on
// refs/daedalus/target, which nobody checks out, so a branch never moves on its
// own.
const (
	// AdoptTarget is how anyone takes a landed commit into a branch of their own.
	AdoptTarget = "adopt it with `git merge --ff-only " + targetRefName + "`"
	// LandedNote is for a surface that has only the STATE to go on — a board
	// column, an archive row — and so cannot know whether that particular landing
	// was asked to move a branch. It is therefore worded to stay true either way.
	LandedNote = "landed work is at " + targetRefName +
		" — a landing moves no branch unless it was asked to; " + AdoptTarget
)

// BranchAdviceFor renders BranchAdvice from what the branch step actually did.
//
// Exported so a caller holding a result from an older daemon — one that predates
// the field — still says the same sentence rather than falling silent.
func BranchAdviceFor(advanced bool, note string) string {
	switch {
	case advanced:
		// The branch HAS the landed commit — moved here, or already there. Either
		// way the note says which branch and what happened, and there is nothing to
		// advise: adding "adopt it with git merge" to a branch that already has the
		// work is how this went wrong before.
		return note
	case note != "":
		// The landing SUCCEEDED and only the courtesy did not. Said in that order,
		// so a refusal here can never be read as "my code did not land".
		advice := "the landing succeeded, but your branch was not moved: " + note
		// Several of those notes already name the ref and the way out — a dirty tree
		// and a diverged branch both do. Saying it twice in one sentence is how a
		// sentence stops being read.
		if !strings.Contains(note, targetRefName) {
			advice += "; " + AdoptTarget
		}
		return advice
	default:
		// The default path, and the one that used to be explained on exactly one
		// surface: nobody asked for a branch to move, so none did.
		return "your branch was NOT changed — the landed commit is at " + targetRefName +
			"; " + AdoptTarget + ", or land it into your branch next time"
	}
}

// IntegrateTask runs the integration transaction for an approved Task.
//
// Serialization is per project: the whole transaction runs under the service
// lock except the (potentially long) re-verification, which releases it exactly
// as VerifyTask does, with an in-flight claim so reconcile does not mistake live
// work for a crash.
func (s *Service) IntegrateTask(id string, req IntegrateRequest) (IntegrationResult, error) {
	if s.verifier == nil {
		// Checked before anything moves: an integration that cannot re-verify must
		// not start, because "landed without the merged result being checked" is the
		// exact failure this transaction exists to prevent.
		return IntegrationResult{}, fmt.Errorf("control: no verifier configured — the merged result could not be re-verified")
	}

	var last error
	for attempt := 1; attempt <= integrateAttempts; attempt++ {
		res, retry, err := s.integrateOnce(id, attempt)
		if err != nil {
			return IntegrationResult{}, err
		}
		if !retry {
			// Strictly AFTER the transaction, and deliberately outside it: the landing
			// is already committed to the database and the target ref already advanced,
			// so nothing this does — or fails to do — can unland the work. That is why
			// every outcome below is a note on the result rather than an error.
			if req.IntoBranch {
				res.Branch, res.BranchAdvanced, res.BranchNote = s.advanceCheckoutBranch(res.Task.Project, res.NewTarget)
			}
			res.BranchAdvice = BranchAdviceFor(res.BranchAdvanced, res.BranchNote)
			return res, nil
		}
		last = fmt.Errorf("target moved during attempt %d", attempt)
		log.Printf("control: integrate %s: %v — recomputing against the new target", id, last)
	}
	// Out of attempts: the target kept moving. Nothing was landed and nothing was
	// lost — the Task is still approved and the caller can simply ask again.
	return IntegrationResult{}, s.refuse("task", id, EventIntegration, ReasonIntegrationRaced, fmt.Sprintf(
		"integration of %s lost the compare-and-swap %d times (the target kept moving); nothing was landed — try again",
		id, integrateAttempts))
}

// jobToIntegrate finds the Job whose artifact is the one being landed.
//
// Normally that is the Job in `verified`. It is ALSO a Job a human WAIVED: the
// waiver (service.go's waiveVerification) records the failure honestly, leaves
// the artifact carrying verify=fail, and moves the Job to `approval_required` on
// a named human's authority — deliberately never to `verified`, because writing
// that would put a false statement into a log that approval and dependency
// satisfaction both read as true.
//
// Which left the waiver leading nowhere. It got a Task to the approval gate and
// then integration refused it, because integration looked for a job in
// `verified` and a waived one is not and must never be. So a human could take
// responsibility for a failing check and still be unable to land the work — the
// legitimate path missing exactly where the override was supposed to be, which
// is the shape this repository has already had to fix once.
//
// The waiver is what makes the difference, and it is checked rather than
// inferred from the state: a Job sitting in `approval_required` for any other
// reason has nobody's name against it, and must not land on the strength of
// where it happens to be standing.
func (s *Service) jobToIntegrate(taskID string) (Job, bool, error) {
	if job, ok, err := s.jobInState(taskID, StateVerified); err != nil || ok {
		return job, ok, err
	}
	jobs, err := s.store.ListJobsForTask(taskID)
	if err != nil {
		return Job{}, false, err
	}
	for i := len(jobs) - 1; i >= 0; i-- {
		j := jobs[i]
		if j.State != StateApprovalRequired && j.State != StateApproved {
			continue
		}
		waived, err := s.store.WaiverForJob(j.ID)
		if err != nil {
			return Job{}, false, err
		}
		if waived {
			return j, true, nil
		}
	}
	return Job{}, false, nil
}

// integrateOnce performs one rebase → re-verify → compare-and-swap round.
// It returns (result, retry, err): retry=true means the CAS lost and the caller
// should recompute against the new target.
func (s *Service) integrateOnce(id string, attempt int) (IntegrationResult, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, err := s.store.GetTask(id)
	if err != nil {
		return IntegrationResult{}, false, err
	}
	// The graph gates LANDING, and this is the gate (Sprint 64). Three things are
	// worth saying about why it is here and not somewhere else.
	//
	// NOT AT VERIFICATION. Grading an artifact against its own frozen oracle is
	// independent of what else is in flight, and refusing to verify would burn a
	// review cycle to learn nothing. What a dependency actually constrains is the
	// order in which work enters the trunk.
	//
	// NOT ONLY AT ADMISSION. Blocking dispatch is what the scheduler already does
	// for a `planned` Task, but on its own it would not deliver what the graph
	// claims: `base_sha` is frozen at task CREATION and only `retry --rebase` ever
	// moves it, so a dependent that merely STARTS after its dependency landed still
	// runs against a tree that predates it. The place the two pieces of work are
	// genuinely combined is the rebase-and-re-verify below — which makes this, not
	// dispatch, the point where "B before A" has content.
	//
	// SAFE THIS EARLY, before the approval walk and before the already-landed
	// recovery path, because satisfaction is MONOTONIC: `integrated` is terminal
	// (no outgoing edge) and no operation deletes a Task, so a dependency that was
	// satisfied cannot become unsatisfied. A Task that got past this gate to the
	// compare-and-swap therefore cannot be refused here on a retry — which matters
	// enormously, since refusing the idempotent settle would strand a Task in
	// `approved` forever while its commits were already live in the trunk.
	if err := s.requireDependenciesLanded(task); err != nil {
		return IntegrationResult{}, false, err
	}
	// Walk the approval gate. For a project that requires human approval this
	// refuses (and parks the Task in `approval_required` where an operator can see
	// it); for one that does not, it walks the same edges automatically and records
	// in the log that policy — not an oversight — is why no human was asked.
	task, err = s.ensureApproved(task)
	if err != nil {
		return IntegrationResult{}, false, err
	}
	job, ok, err := s.jobToIntegrate(id)
	if err != nil {
		return IntegrationResult{}, false, err
	}
	if !ok {
		return IntegrationResult{}, false, fmt.Errorf("%w: task %s has no verified job to integrate", ErrWrongState, id)
	}
	art := s.firstArtifact(job.ID)
	if art == nil {
		return IntegrationResult{}, false, fmt.Errorf("%w: task %s has no artifact to integrate", ErrWrongState, id)
	}
	// …and one that names no commit cannot be landed either. Said in the plane's
	// words here rather than git's three calls later.
	if err := usableArtifact(art); err != nil {
		return IntegrationResult{}, false, err
	}
	// An independent review, when one is configured, gates integration (§6's
	// ladder rung 5). Checked here rather than at approval so the review verdict
	// is fresh with respect to the artifact actually being landed.
	if err := s.requireReviewPassed(task, art); err != nil {
		return IntegrationResult{}, false, err
	}
	repoDir, err := s.projects.ProjectDir(task.Project)
	if err != nil {
		return IntegrationResult{}, false, err
	}
	// A pure read, and a missing target here is a REAL ERROR rather than a cue to
	// adopt one. Landing work is the operation that moves the plane's trunk; doing
	// it against a target invented from the checkout's HEAD, at the moment of the
	// compare-and-swap, would hand the trunk to whoever can write the repository's
	// refs — the precise failure the plane-owned target was built to prevent.
	target, err := s.Target(task.Project)
	if err != nil {
		return IntegrationResult{}, false, err
	}

	// --- 0. ALREADY LANDED? ---------------------------------------------------
	//
	// The compare-and-swap commits before the Task is marked `integrated`, and
	// those are two different stores (git + the target row, then the task row).
	// If the second write fails, the target has advanced while the Task still
	// says `approved` — the one place in this transaction where a write survives
	// an error. That cannot be made atomic across SQLite and git, so instead
	// re-integration is made IDEMPOTENT: if the artifact's commits are already
	// contained in the target, the work has landed and the Task is settled to
	// match rather than landed a second time.
	//
	// Without this, a retry would cherry-pick --allow-empty the same commits onto
	// the new target and swap again — an empty duplicate commit, and a second
	// "integrated" event for one piece of work.
	if landed, err := ArtifactIsLanded(repoDir, target.SHA, art.BaseSHA, art.HeadSHA); err != nil {
		log.Printf("control: integrate %s: checking whether the artifact already landed: %v", id, err)
	} else if landed {
		note := fmt.Sprintf("already landed: the artifact's commits are contained in target %s — settling the task rather than landing twice",
			shortSHA(target.SHA))
		log.Printf("control: integrate %s: %s", id, note)
		return s.settleAlreadyLanded(task, job, art, repoDir, target, note)
	}

	// --- 1. REBASE the artifact onto the current target -----------------------
	//
	// Replaying base..head onto the target (rather than merging) keeps the landed
	// history linear and, more importantly, produces the exact tree that will
	// exist after landing — which is the thing step 2 must check.
	merged, err := RebaseOnto(repoDir, target.SHA, art.BaseSHA, art.HeadSHA, integrationWorktree(s.worktrees, job.ID))
	if err != nil {
		var conflict *ErrRebaseConflict
		if errors.As(err, &conflict) {
			note := fmt.Sprintf("integration: %s does not rebase cleanly onto target %s: %s",
				shortSHA(art.HeadSHA), shortSHA(target.SHA), conflict.Detail)
			s.rejectFromIntegration(task, job, art, repoDir, ReasonMergeConflict, note)
			return IntegrationResult{}, false, &RejectionError{
				Reason: ReasonMergeConflict, Message: note, Entity: id}
		}
		return IntegrationResult{}, false, err
	}

	// --- 2. RE-VERIFY THE MERGED RESULT ---------------------------------------
	//
	// Not the pre-merge branch. This is the whole point: the artifact already
	// passed verification against its own base, and that says nothing about the
	// combination. A change that verifies alone and fails merged is a semantic
	// conflict, and this is the only step that can see one.
	policy, err := ReadAcceptancePolicyAt(repoDir, task.BaseSHA)
	if err != nil {
		return IntegrationResult{}, false, err
	}
	spec := VerifySpec{
		TaskID: id, JobID: job.ID, Project: task.Project, RepoDir: repoDir,
		BaseSHA: target.SHA, HeadSHA: merged,
		Branch: BranchName(id, job.ID), Policy: policy, ImageDigest: task.ImageDigest,
	}
	var outcome VerifyOutcome
	if err := s.withClaim(id, inflightOp{kind: "integrate", jobID: job.ID, project: task.Project}, func() error {
		s.unlockedDuring(func() { outcome = s.verifier.Verify(context.Background(), spec) })
		return nil
	}); err != nil {
		return IntegrationResult{}, false, err
	}

	if !outcome.Passed {
		note := fmt.Sprintf("integration: the MERGED result %s failed verification against target %s: %s",
			shortSHA(merged), shortSHA(target.SHA), outcome.Detail)
		// A WAIVED Job carries its waiver through to here, and only here.
		//
		// Without this the waiver led to a gate with no way through, one step
		// further along than before: a human accepts answerability for an artifact
		// the oracle refused, reaches the approval gate, lands — and the merged
		// re-verify refuses it with the same broken check, for the same irrelevant
		// reason. Asking the same person to accept the same thing twice adds no
		// information; it just makes the override useless in the case that
		// motivated it, which is an oracle that is wrong about everything.
		//
		// It is narrow on purpose. It applies to the Job that was waived and to
		// nothing else — a retry produces a new Job with no waiver against it — and
		// the failure is still RECORDED on the integration rather than dropped, so
		// the record says a merged verification failed and a named human landed it
		// anyway. That is the same trade the waiver itself makes: waive the
		// consequence, never the truth.
		waived, werr := s.store.WaiverForJob(job.ID)
		if werr != nil {
			log.Printf("control: reading waiver for %s: %v", job.ID, werr)
		}
		if !waived {
			s.rejectFromIntegration(task, job, art, repoDir, ReasonMergedVerifyFailed, note)
			return IntegrationResult{}, false, &RejectionError{
				Reason: ReasonMergedVerifyFailed, Message: note, Entity: id}
		}
		carried := note + " — CARRIED ANYWAY on the waiver recorded against " + job.ID +
			"; the merged result was never verified"
		if err := s.store.LogDecision("task", id, EventMeta{Kind: EventWaiver, Actor: ActorHuman},
			carried); err != nil {
			log.Printf("control: recording carried waiver for %s: %v", id, err)
		}
		outcome.Detail = carried
	}

	// --- 3. COMPARE-AND-SWAP the target ---------------------------------------
	//
	// If another integration landed while we were rebasing and re-verifying, this
	// merged commit was computed against a trunk that no longer exists. The CAS
	// fails, nothing is written, and the caller recomputes — the merge-queue's
	// entire safety property in one UPDATE ... WHERE sha = <what we started from>.
	newTarget, err := s.store.AdvanceTarget(target.RepoPath, target.SHA, merged, fmt.Sprintf(
		"integrated %s (job %s, artifact %s): target %s → %s",
		id, job.ID, art.ID, shortSHA(target.SHA), shortSHA(merged)))
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return IntegrationResult{}, true, nil // lost the race: recompute
		}
		return IntegrationResult{}, false, err
	}
	s.writeTargetProjection(repoDir, newTarget.SHA)

	// --- 4. Record the landing -------------------------------------------------
	meta := EventMeta{Kind: EventIntegration}
	// The Job follows its Task through the integration gate. It cannot jump
	// straight from `verified` to `integrated`: Jobs and Tasks share one
	// transition table, so a verified → integrated shortcut would also hand a TASK
	// a way around the approval gate, and the table cannot tell the two apart.
	// Walking the same edges costs two extra events and keeps the state machine
	// exactly as strict as it is documented to be.
	s.driveJob(job.ID, []State{StateApprovalRequired, StateApproved, StateIntegrated}, meta,
		fmt.Sprintf("integrated as %s (following task %s)", shortSHA(merged), id))
	tk, err := s.store.TransitionTaskWith(id, StateIntegrated, false, meta, fmt.Sprintf(
		"integrated: target %s → %s", shortSHA(target.SHA), shortSHA(merged)))
	if err != nil {
		return IntegrationResult{}, false, err
	}
	if a, err := s.store.SetArtifactIntegrated(art.ID, merged); err == nil {
		art = &a
	}
	// The Task is terminal now, so its worktree is reclaimable.
	if s.worktrees != nil {
		_ = s.worktrees.Remove(repoDir, job.ID)
	}
	// This Task has landed, so anything waiting on it may now be runnable. The
	// fast path; Reconcile re-evaluates regardless, so a missed wake self-heals.
	s.wakeDependents(id)

	return IntegrationResult{
		Task: tk, Artifact: art, MergedSHA: merged,
		PreviousTarget: target.SHA, NewTarget: newTarget.SHA, Attempts: attempt,
		Detail: outcome.Detail,
	}, false, nil
}

// settleAlreadyLanded finishes a transaction whose work is already contained in
// the target — the recovery path for a failure between the compare-and-swap and
// the Task transition. It advances no target and creates no commit.
func (s *Service) settleAlreadyLanded(task Task, job Job, art *Artifact, repoDir string, target Target, note string) (IntegrationResult, bool, error) {
	meta := EventMeta{Kind: EventIntegration}
	s.driveJob(job.ID, []State{StateApprovalRequired, StateApproved, StateIntegrated}, meta, note)
	tk, err := s.store.TransitionTaskWith(task.ID, StateIntegrated, false, meta, note)
	if err != nil {
		return IntegrationResult{}, false, err
	}
	if art != nil && art.IntegratedSHA == "" {
		if a, err := s.store.SetArtifactIntegrated(art.ID, target.SHA); err == nil {
			art = &a
		}
	}
	if s.worktrees != nil {
		_ = s.worktrees.Remove(repoDir, job.ID)
	}
	return IntegrationResult{
		Task: tk, Artifact: art, MergedSHA: target.SHA,
		PreviousTarget: target.SHA, NewTarget: target.SHA, Attempts: 1,
		Detail: note,
	}, false, nil
}

// rejectFromIntegration routes a failed integration to `rejected` so the Sprint-58
// retry/replan ladder can pick it up. The target is untouched by construction —
// nothing below step 3 writes it.
func (s *Service) rejectFromIntegration(task Task, job Job, art *Artifact, repoDir string, reason RejectionReason, note string) {
	meta := EventMeta{Kind: EventRejection, Reason: reason}
	// Same reasoning as the success path: `verified → rejected` is not an edge, so
	// the Job reaches `rejected` the way its Task does, through the gate.
	s.driveJob(job.ID, []State{StateApprovalRequired, StateRejected}, meta, note)
	if _, err := s.store.TransitionTaskWith(task.ID, StateRejected, false, meta, note); err != nil {
		log.Printf("control: integrate reject task %s: %v", task.ID, err)
	}
	if art != nil {
		_, _ = s.store.SetArtifactVerify(art.ID, VerifyFail)
	}
	if s.worktrees != nil {
		_ = s.worktrees.Remove(repoDir, job.ID)
	}
}

// driveJob walks a Job through a sequence of states, skipping any it is already
// past and logging (never failing) if one is refused: a Job's bookkeeping lagging
// behind its Task is a reconcilable inconsistency, not a reason to claim a
// landing failed after the target has already moved.
func (s *Service) driveJob(jobID string, path []State, meta EventMeta, note string) {
	for _, to := range path {
		cur, err := s.store.GetJob(jobID)
		if err != nil {
			log.Printf("control: driving job %s: %v", jobID, err)
			return
		}
		if cur.State == to {
			continue
		}
		if !CanTransition(cur.State, to) {
			log.Printf("control: driving job %s: %s → %s is not a legal move", jobID, cur.State, to)
			return
		}
		if _, err := s.store.TransitionJobWith(jobID, to, false, meta, note); err != nil {
			log.Printf("control: driving job %s → %s: %v", jobID, to, err)
			return
		}
	}
}

// integrationWorktree returns a scratch path for the rebase checkout, distinct
// from the Job's own worktree so the transaction never mutates the artifact it is
// integrating.
func integrationWorktree(m *WorktreeManager, jobID string) string {
	if m == nil {
		return filepath.Join(os.TempDir(), "daedalus-integrate-"+jobID)
	}
	return filepath.Join(m.Root(), "integrate-"+jobID)
}

// ArtifactIsLanded reports whether an artifact's own commits (base..head) are
// already contained in `target`.
//
// Two ways an artifact can be in, and both must be recognised:
//
//  1. BY ANCESTRY — it landed unrebased (the target was its base, so the swap was
//     a fast-forward and the landed commit IS the artifact's head).
//  2. BY CONTENT — it was rebased, so the landed commit has a different sha. Only
//     the patch id survives that, which is what `git cherry` compares: it marks a
//     commit `-` when an equivalent change is already upstream and `+` when it is
//     not.
//
// Checking only ancestry would miss every rebased landing; checking only
// `git cherry` would miss every fast-forward one, because with target == head the
// range base..head-not-in-target is empty and it reports nothing at all. Getting
// this wrong in the "not landed" direction lands the work twice, which is the
// failure this function exists to prevent — so it errs toward asking both.
//
// An artifact with no commits of its own is not "landed"; that case is refused
// earlier by RebaseOnto.
func ArtifactIsLanded(repoDir, target, base, head string) (bool, error) {
	if target == "" || base == "" || head == "" || base == head {
		return false, nil
	}
	// 1. Unrebased: the artifact's head is contained in the target.
	contained, err := IsAncestor(repoDir, head, target)
	if err != nil {
		return false, err
	}
	if contained {
		return true, nil
	}
	// 2. Rebased: every commit in base..head has an equivalent upstream.
	out, err := runGit(repoDir, "cherry", target, head, base)
	if err != nil {
		return false, wrapGit("git cherry", out, err)
	}
	counted := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "+") {
			return false, nil // at least one commit is not upstream yet
		}
		if strings.HasPrefix(line, "-") {
			counted++
		}
	}
	return counted > 0, nil
}

// ErrRebaseConflict reports that an artifact could not be replayed onto the
// target — a textual conflict, distinct from a clean rebase whose *result* then
// fails verification.
type ErrRebaseConflict struct {
	Onto   string
	Head   string
	Detail string
}

func (e *ErrRebaseConflict) Error() string {
	return fmt.Sprintf("rebase of %s onto %s conflicts: %s", shortSHA(e.Head), shortSHA(e.Onto), e.Detail)
}

// RebaseOnto replays the commits base..head onto `onto` in a scratch worktree and
// returns the resulting commit. It never touches the caller's checkout, the
// Job's worktree, or any branch: the scratch worktree is detached and removed
// afterwards, so a failed integration leaves the repository exactly as it was
// apart from unreferenced objects.
//
// base==head (an artifact with no commits of its own) is refused rather than
// silently landing nothing.
func RebaseOnto(repoDir, onto, base, head, scratchDir string) (string, error) {
	if onto == "" || base == "" || head == "" {
		return "", fmt.Errorf("control: rebase needs onto/base/head (%q/%q/%q)", onto, base, head)
	}
	if base == head {
		return "", fmt.Errorf("control: nothing to integrate — the artifact is identical to its base")
	}
	// Already on top of the target: no replay needed, and cherry-pick would make
	// an empty commit out of it.
	if onto == base {
		return head, nil
	}
	_ = os.RemoveAll(scratchDir) // deterministic name: a crashed prior attempt must not block this one
	if err := os.MkdirAll(filepath.Dir(scratchDir), 0o755); err != nil {
		return "", fmt.Errorf("control: creating rebase scratch dir: %w", err)
	}
	if out, err := runGit(repoDir, "worktree", "add", "--detach", scratchDir, onto); err != nil {
		return "", fmt.Errorf("control: preparing rebase worktree at %s: %w\n%s", shortSHA(onto), err, out)
	}
	defer func() {
		_, _ = runGit(repoDir, "worktree", "remove", "--force", scratchDir)
		_ = os.RemoveAll(scratchDir)
		_, _ = runGit(repoDir, "worktree", "prune")
	}()

	// Replay base..head. --allow-empty keeps a commit whose changes are already
	// upstream from aborting the whole replay.
	if out, err := runGit(scratchDir, "cherry-pick", "--allow-empty", base+".."+head); err != nil {
		abort, _ := runGit(scratchDir, "cherry-pick", "--abort")
		_ = abort
		return "", &ErrRebaseConflict{Onto: onto, Head: head, Detail: firstLines(out, 12)}
	}
	merged, err := runGit(scratchDir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("control: reading rebased head: %w\n%s", err, merged)
	}
	return strings.TrimSpace(merged), nil
}

// firstLines trims git's output to something a rejection note can carry.
func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = append(lines[:n], "…")
	}
	return strings.Join(lines, "\n")
}

// advanceCheckoutBranch fast-forwards a project checkout's current branch to a
// commit the plane has just landed, for `task integrate --into-branch`.
//
// FAST-FORWARD ONLY, and every guard below refuses rather than resolves. The
// plane is landing on its own target; moving somebody's branch is a courtesy on
// top of that, and a courtesy that can lose work is not one. So: no merge commit,
// no rebase, no stash, no checkout of a different branch, and above all no
// --force. If the branch cannot simply be wound forward, this says so and leaves
// it exactly as it was, with the landed commit still reachable through
// refs/daedalus/target for the operator to merge however they see fit.
//
// It returns (branch, advanced, note). `advanced` means THE BRANCH IS AT THE
// LANDED COMMIT — whether this function moved it or found it already there —
// because that is the question the operator is asking and the only one the
// colour and the wording downstream depend on. Note is filled on every path,
// including success, because "nothing appeared to happen" is the complaint this
// feature exists to answer and silence would reproduce it.
func (s *Service) advanceCheckoutBranch(project, targetSHA string) (string, bool, string) {
	repoDir, err := s.projects.ProjectDir(project)
	if err != nil {
		return "", false, fmt.Sprintf("could not resolve %s to a checkout: %v", project, err)
	}
	branch, err := runGit(repoDir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", false, "the checkout has a detached HEAD, so there is no branch to advance"
	}
	branch = strings.TrimSpace(branch)

	// A dirty tree is the one case where winding the branch forward would touch
	// files somebody is editing. Refused outright: the whole design property of the
	// target ref is that landing never disturbs a working tree, and an opt-in flag
	// is not licence to break that when it would cost the operator uncommitted work.
	if status, err := runGit(repoDir, "status", "--porcelain"); err == nil && strings.TrimSpace(status) != "" {
		return branch, false, fmt.Sprintf(
			"%s has uncommitted changes — left untouched; commit or stash, then `git merge --ff-only %s`",
			branch, targetRefName)
	}

	head, err := runGit(repoDir, "rev-parse", "HEAD")
	if err != nil {
		return branch, false, fmt.Sprintf("could not read HEAD of %s: %v", branch, err)
	}
	if strings.TrimSpace(head) == targetSHA {
		// ALREADY THERE COUNTS AS ADVANCED, and the distinction this collapses is
		// the one that produced a wrong sentence (RV-8).
		//
		// `advanced` reports the OUTCOME the operator cares about — "your branch is
		// at the landed commit" — not whether this function happened to run a merge
		// to get there. Reported as false, it fell into the "the landing succeeded,
		// but your branch was not moved" branch below, which then appended
		// "adopt it with git merge --ff-only …": the operator was told their branch
		// lacked the work and handed a remedy that is a no-op, when the branch was
		// already exactly right. Hit by a second `integrate --into-branch`, or by
		// any project checked out on the branch that just landed.
		return branch, true, fmt.Sprintf("%s was already at the landed commit", branch)
	}
	// Not an ancestor → the branch carries commits the landed target does not, so
	// winding it forward is impossible and anything else would be a merge decision
	// that belongs to the operator.
	//
	// `git merge --ff-only` below is what ENFORCES this — it refuses a non-fast-
	// forward on its own, and removing this check does not make the operation
	// unsafe. What the check buys is the message: an operator who is told their
	// branch has diverged and handed the ref to merge is better served than one
	// given git's "Not possible to fast-forward, aborting". Belt to git's braces,
	// and honest about which is which.
	if _, err := runGit(repoDir, "merge-base", "--is-ancestor", strings.TrimSpace(head), targetSHA); err != nil {
		return branch, false, fmt.Sprintf(
			"%s has diverged from the landed commit — left untouched; merge it yourself (`git merge %s`)",
			branch, targetRefName)
	}
	if out, err := runGit(repoDir, "merge", "--ff-only", targetSHA); err != nil {
		return branch, false, fmt.Sprintf("fast-forwarding %s failed: %v\n%s", branch, err, out)
	}
	return branch, true, fmt.Sprintf("%s fast-forwarded to %s", branch, shortSHA(targetSHA))
}
