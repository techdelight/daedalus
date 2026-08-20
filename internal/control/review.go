// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// The independent reviewer pass (docs/guild-master-plan.md §6, rung 5 of the
// defence ladder).
//
// Verification answers "does this committed artifact, in this pinned
// environment, make the frozen procedure report success". Review answers a
// different question — "is this change *acceptable*" — which tests cannot, and
// which §12 is blunt about: tests are an incomplete oracle, so "verified" is
// evidence and not proof. A reviewer is another rung, not a replacement.
//
// It is a seam, exactly as VerifyRunner was in Sprint 56: a stub here, a real
// implementation later. Nothing in this package assumes the reviewer is an agent,
// a human, or a linter — only that it returns a verdict on a committed artifact.

// ReviewSpec is the input to a ReviewRunner: the committed artifact to review and
// the context needed to look at it. Like VerifySpec it names commits, never a
// mutable working tree, so a reviewer cannot be shown something different from
// what would land.
type ReviewSpec struct {
	TaskID  string
	JobID   string
	Project string
	RepoDir string
	BaseSHA string
	HeadSHA string
	Branch  string
	// Objective is what the Task set out to do — the promise the diff is judged
	// against.
	Objective string
	// Rationale is what the work was FOR, and RationaleBy who said so. Added in
	// M20 because the two questions worth asking are "did this deliver what it
	// promised" and "was this worth doing", and a diff answers neither on its own.
	// The author matters to a reviewer for the same reason it matters in the
	// record: a reason the agent drafted is weaker evidence of intent than one a
	// human wrote.
	Rationale   string
	RationaleBy CallerClass
	// Programme names the shared intent this Task serves, if any, so a reviewer
	// can ask whether the change actually advances it.
	ProgrammeName string
	ProgrammeFor  string
}

// Severity ranks a finding. It is deliberately coarse: a reviewer that can
// express five gradations will spend its judgement on choosing between them.
type Severity string

const (
	// SeverityBlocking: the reviewer believes this should not land as it stands.
	// It is still only a belief — nothing in the plane acts on it (see ReviewTask).
	SeverityBlocking Severity = "blocking"
	// SeverityConcern: worth a human's attention before deciding.
	SeverityConcern Severity = "concern"
	// SeverityNote: an observation, recorded because the record is the point.
	SeverityNote Severity = "note"
)

// Finding is one thing a reviewer noticed, with somewhere to look and a reason.
//
// The location and the WHY are both required by shape rather than by validation:
// a finding with neither is an opinion, and an opinion is what the old
// {Passed, Detail} pair could already express.
type Finding struct {
	Severity Severity `json:"severity"`
	// File and Line point at the diff. Line may be 0 when the finding is about a
	// file as a whole, or File empty when it is about the change as a whole.
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
	// What is the finding; Why is the reason it matters. Two fields, because a
	// reviewer that writes only the first is describing the code back to you.
	What string `json:"what"`
	Why  string `json:"why,omitempty"`
}

// ReviewOutcome is a ReviewRunner's judgement.
//
// It replaced {Passed bool, Detail string} in M20 — the shape that cannot say
// anything useful. A boolean is what an exit code already gives, and the whole
// argument for a reviewer is that it answers a question exit codes cannot.
type ReviewOutcome struct {
	// Passed is the reviewer's overall reading. It is ADVISORY: nothing in the
	// plane transitions on it. Kept as one field anyway because an operator
	// scanning a queue needs a summary, and "the reviewer had concerns" is the
	// first thing they want to know.
	Passed bool `json:"passed"`
	// Reasoning is the reviewer's account of how it read the change — the part a
	// human actually needs in order to disagree with it.
	Reasoning string `json:"reasoning,omitempty"`
	// Findings are what it noticed. An empty list with Passed=false is a reviewer
	// that has not done its job, and reads that way in the record.
	Findings []Finding `json:"findings,omitempty"`
	// Reviewer identifies WHO judged — an agent name, a model, "stub". Recorded
	// so a judgement is attributable the way a rationale is; a verdict from an
	// unnamed source is not evidence, it is a rumour.
	Reviewer string `json:"reviewer,omitempty"`
	// Detail is the one-line summary, kept for the CLI and the event log.
	Detail string `json:"detail"`
}

// Blocking counts the findings the reviewer considered disqualifying. Reported,
// never acted on — see ReviewTask.
func (o ReviewOutcome) Blocking() int {
	n := 0
	for _, f := range o.Findings {
		if f.Severity == SeverityBlocking {
			n++
		}
	}
	return n
}

// ReviewRunner performs an independent review of a committed artifact. It is
// injectable so the control-plane logic (gating, budget, transitions) is
// host-tested without one, and so a real reviewer can be added without touching
// any of that logic.
type ReviewRunner interface {
	Review(ctx context.Context, spec ReviewSpec) ReviewOutcome
}

// StubReviewRunner is a dependency-free ReviewRunner returning a fixed verdict —
// the Sprint-56 StubVerifyRunner pattern. Exported so the daemon can select it
// via DAEDALUS_CONTROL_FAKE_REVIEW and so tests can drive both outcomes.
type StubReviewRunner struct {
	Pass   bool
	Detail string
}

// Review implements ReviewRunner.
func (r StubReviewRunner) Review(_ context.Context, spec ReviewSpec) ReviewOutcome {
	detail := r.Detail
	if detail == "" {
		if r.Pass {
			detail = "stub reviewer: pass"
		} else {
			detail = "stub reviewer: fail"
		}
	}
	out := ReviewOutcome{Passed: r.Pass, Detail: detail, Reviewer: "stub", Reasoning: detail}
	if !r.Pass {
		// A failing stub produces a real finding rather than a bare false, so the
		// no-Docker smoke exercises the same recording path a real reviewer does.
		out.Findings = []Finding{{
			Severity: SeverityBlocking, What: "stub reviewer was configured to fail",
			Why: "DAEDALUS_CONTROL_FAKE_REVIEW=fail", File: spec.Branch,
		}}
	}
	return out
}

// SetReviewRunner installs the independent reviewer. Nil means no reviewer is
// configured, and review is then not a gate at all — governance stays opt-in
// (§9), so a project that has not asked for a reviewer is not blocked by one.
func (s *Service) SetReviewRunner(r ReviewRunner) { s.reviewer = r }

// ReviewResult is the outcome of a plane-owned review pass.
type ReviewResult struct {
	Task     Task      `json:"task"`
	Artifact *Artifact `json:"artifact,omitempty"`
	Passed   bool      `json:"passed"`
	// Reason is retained for wire compatibility and is now always empty: a review
	// no longer rejects anything, so there is no rejection reason to give.
	Reason    RejectionReason `json:"reason,omitempty"`
	Detail    string          `json:"detail"`
	Reasoning string          `json:"reasoning,omitempty"`
	Findings  []Finding       `json:"findings,omitempty"`
	Reviewer  string          `json:"reviewer,omitempty"`
	ReviewID  string          `json:"reviewId,omitempty"`
	Cycles    int             `json:"cycles"`    // review passes used
	MaxCycle  int             `json:"maxCycles"` // 0 = unbounded
}

// reviewableStates are the states in which an artifact can be read.
//
// The list is what it is because a reviewer answers a question about a DIFF, and
// a diff either exists or it does not. What the machine oracle thought of it is
// beside the point — the reviewer is the second opinion, and a second opinion
// available only after the first one agrees is not one.
var reviewableStates = map[State]bool{
	StateCandidate:        true, // graded by nobody yet
	StateRejected:         true, // the oracle said no — the case that needs a reading most
	StateVerified:         true,
	StateApprovalRequired: true,
	StateApproved:         true,
}

func reviewableStateNames() string {
	return "candidate/rejected/verified/approval_required/approved"
}

// jobToReview finds the Job whose artifact should be read: the most recent one
// that produced anything.
//
// Newest first, because a retried Task carries its whole Job chain and the
// reading anyone wants is of the latest attempt. A Job with no artifact is
// skipped rather than refused — an earlier attempt that produced nothing should
// not hide a later one that did.
func (s *Service) jobToReview(taskID string) (Job, *Artifact, bool, error) {
	jobs, err := s.store.ListJobsForTask(taskID)
	if err != nil {
		return Job{}, nil, false, err
	}
	for i := len(jobs) - 1; i >= 0; i-- {
		if art := s.firstArtifact(jobs[i].ID); art != nil {
			return jobs[i], art, true, nil
		}
	}
	return Job{}, nil, false, nil
}

// ReviewTask runs the independent reviewer over a Task's artifact.
//
// It is deliberately a separate, explicit operation rather than something folded
// into verification: the two answer different questions, they can fail for
// different reasons, and a reviewer may be slow or expensive.
//
// THE REVIEWER REPORTS; IT DOES NOT ACT. Until M20 a failed review drove the
// Task to `rejected` and reclaimed its worktree. That is now recorded and NOT
// acted on, and the reason is the reviewer's nature rather than a change of
// heart about review. A verifier runs a frozen, human-authored command whose
// output is an exit code; a reviewer is a language model reading a diff it did
// not write, which is untrusted input by construction. Two consequences follow
// and they point the same way:
//
//   - A verdict that moves plane state on its own is an oracle nobody bounded.
//     Nothing constrains it, nothing reproduces it, and a wrong reading would
//     spend a human's work on a machine's opinion.
//   - The same diff can talk to the reviewer. A PASS that carried authority
//     would be the lethal-trifecta shape one level up: untrusted content
//     reaching an action vector.
//
// So the judgement is EVIDENCE, presented at the gate where a human already
// decides. That is not a weaker design than gating on it — it is the only one
// that stays honest about what a model's reading is worth.
func (s *Service) ReviewTask(id string) (ReviewResult, error) {
	if s.reviewer == nil {
		return ReviewResult{}, fmt.Errorf("control: no reviewer configured for this control plane")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	task, err := s.store.GetTask(id)
	if err != nil {
		return ReviewResult{}, err
	}
	// ANYTHING WITH AN ARTIFACT IS REVIEWABLE, and correcting this is the whole
	// point of the rung.
	//
	// Review used to require `verified` and a Job in `verified` — the reviewer was
	// downstream of the machine oracle. That made it useless in exactly the case
	// it exists for. The oracle grades documents (it cannot run a project's build
	// with the network off — backlog #74), so a Task it rejects is precisely the
	// one a human needs a reading of, and that was the one reading the plane
	// refused to fetch. A `candidate` nobody has graded yet was refused too.
	//
	// Nothing is risked by widening it. Since M20 a review transitions nothing and
	// gates nothing: it records a judgement for a human. A judgement about an
	// artifact the linter disliked is not a claim that the artifact passed, and
	// treating "the reviewer may look" as a privilege earned by passing a
	// different test was the error.
	//
	// `verifying` is excluded on purpose — a grading is in flight and its verdict
	// is about to land — as are states with no artifact to read at all.
	if !reviewableStates[task.State] {
		return ReviewResult{}, fmt.Errorf("%w: task %s is %s, not reviewable (want %s)",
			ErrWrongState, id, task.State, reviewableStateNames())
	}
	job, art, ok, err := s.jobToReview(id)
	if err != nil {
		return ReviewResult{}, err
	}
	if !ok {
		return ReviewResult{}, fmt.Errorf("%w: task %s has no artifact to review — nothing has been produced yet",
			ErrWrongState, id)
	}
	repoDir, err := s.projects.ProjectDir(task.Project)
	if err != nil {
		return ReviewResult{}, err
	}

	// Bounded by the max-review-cycles budget. The SAME LIMIT is applied to
	// verification cycles and to review passes, but they are counted SEPARATELY
	// and not summed — a task is allowed N verifications and N reviews, not N of
	// both combined. Stated explicitly because "the existing budget" could
	// reasonably be read either way, and a silent shared counter would make a
	// verification failure quietly consume a review the operator was expecting.
	used, err := s.store.CountReviewRuns(id)
	if err != nil {
		return ReviewResult{}, err
	}
	max := task.Budget.MaxReviewCycles
	if max > 0 && used >= max {
		return ReviewResult{}, s.refuse("task", id, EventBudget, ReasonReviewCyclesExhausted, fmt.Sprintf(
			"task %s has used all %d review pass(es); the artifact is unchanged — cancel it or raise the project budget",
			id, max))
	}

	spec := ReviewSpec{
		TaskID: id, JobID: job.ID, Project: task.Project, RepoDir: repoDir,
		BaseSHA: art.BaseSHA, HeadSHA: art.HeadSHA,
		Branch: BranchName(id, job.ID), Objective: task.Objective,
		// What the work was FOR, and who said so (M20). A diff plus an objective
		// answers "did it do the thing"; only the rationale lets a reviewer ask
		// whether the thing was worth doing — and the author tells it how much
		// weight the reason carries.
		Rationale: task.Rationale, RationaleBy: task.RationaleBy,
	}
	if task.ProgrammeID != "" {
		if prog, err := s.store.GetProgramme(task.ProgrammeID); err == nil {
			spec.ProgrammeName, spec.ProgrammeFor = prog.Name, prog.Description
		}
	}
	// Released across the reviewer for the same reason as the verifier: it may be
	// slow, and cancel/reconcile must stay responsive. Both the claim and the
	// unlock are scoped helpers (claim.go), so a panic in the reviewer cannot leak
	// either.
	var outcome ReviewOutcome
	if err := s.withClaim(id, inflightOp{kind: "review", jobID: job.ID, project: task.Project}, func() error {
		s.unlockedDuring(func() { outcome = s.reviewer.Review(context.Background(), spec) })
		return nil
	}); err != nil {
		return ReviewResult{}, err
	}

	// The judgement is kept in FULL, before anything summarises it. The findings
	// are the reason a reviewer is worth having; a row that stored only the
	// boolean would have thrown away everything an exit code could not already say.
	review, err := s.store.RecordReview(Review{
		TaskID: id, JobID: job.ID, ArtifactID: art.ID, Reviewer: outcome.Reviewer,
		Passed: outcome.Passed, Reasoning: outcome.Reasoning,
		Findings: outcome.Findings, Detail: outcome.Detail,
	})
	if err != nil {
		log.Printf("control: recording review of %s: %v", id, err)
	}
	// Logged as a DECISION and never as a rejection: the event log should not
	// carry a word for something that did not happen to the Task.
	if err := s.store.LogDecision("task", id, EventMeta{Kind: EventReview}, fmt.Sprintf(
		"review pass %d by %s: passed=%v, %d finding(s), %d blocking (%s)",
		used+1, reviewerName(outcome.Reviewer), outcome.Passed, len(outcome.Findings),
		outcome.Blocking(), outcome.Detail)); err != nil {
		log.Printf("control: logging review of %s: %v", id, err)
	}

	res := ReviewResult{
		Passed: outcome.Passed, Detail: outcome.Detail, Cycles: used + 1, MaxCycle: max,
		Reasoning: outcome.Reasoning, Findings: outcome.Findings,
		Reviewer: outcome.Reviewer, ReviewID: review.ID,
	}

	// The artifact records what the reviewer thought, and the Task does not move.
	// Recording the truth and acting on it are different things, and only the
	// second is reserved to a human here.
	status := ReviewPass
	if !outcome.Passed {
		status = ReviewFail
	}
	if a, err := s.store.SetArtifactReview(art.ID, status); err == nil {
		art = &a
	}
	tk, _ := s.store.GetTask(id)
	res.Task, res.Artifact = tk, art
	return res, nil
}

func reviewerName(s string) string {
	if s == "" {
		return "an unnamed reviewer"
	}
	return s
}

// requireReviewPassed gates integration on the independent review when a reviewer
// is configured. s.mu must be held.
//
// When no reviewer is configured this is a no-op: review is opt-in, and a plane
// with no reviewer must not block every landing forever.
// It is now a NO-OP, deliberately, and is kept as a named function rather than
// deleted so that the decision is visible at the place it used to be enforced.
//
// It refused to integrate an artifact the reviewer had failed, or one it had not
// seen. Both refusals gave a language model a veto over a human's work, which is
// the authority M20 took back: findings are reported and the human at the
// approval gate decides, with the findings in front of them. What replaced the
// gate is not nothing — it is `ReviewsForTask`, surfaced at approval and in the
// Ledger's record, which is strictly more information than a refusal carried.
func (s *Service) requireReviewPassed(task Task, art *Artifact) error {
	return nil
}

// --- the record -----------------------------------------------------------------

// Review is one recorded judgement of an artifact.
type Review struct {
	ID         string    `json:"id"` // "RV-1"
	TaskID     string    `json:"taskId"`
	JobID      string    `json:"jobId"`
	ArtifactID string    `json:"artifactId"`
	Reviewer   string    `json:"reviewer,omitempty"`
	Passed     bool      `json:"passed"`
	Reasoning  string    `json:"reasoning,omitempty"`
	Findings   []Finding `json:"findings,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	CreatedAt  string    `json:"createdAt"`
}

// RecordReview stores a judgement. Reviews accumulate; nothing here updates.
func (s *Store) RecordReview(r Review) (Review, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Review{}, err
	}
	defer tx.Rollback()

	id, err := nextID(tx, "reviews", "RV")
	if err != nil {
		return Review{}, err
	}
	findings := ""
	if len(r.Findings) > 0 {
		b, err := json.Marshal(r.Findings)
		if err != nil {
			return Review{}, fmt.Errorf("encoding findings: %w", err)
		}
		findings = string(b)
	}
	r.ID, r.CreatedAt = id, s.now()
	passed := 0
	if r.Passed {
		passed = 1
	}
	if _, err := tx.Exec(
		`INSERT INTO reviews (id, task_id, job_id, artifact_id, reviewer, passed, reasoning, findings, detail, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.TaskID, r.JobID, r.ArtifactID, r.Reviewer, passed, r.Reasoning, findings, r.Detail, r.CreatedAt,
	); err != nil {
		return Review{}, fmt.Errorf("inserting review: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Review{}, err
	}
	return r, nil
}

const reviewSelect = `SELECT id, task_id, job_id, artifact_id, reviewer, passed, reasoning, findings, detail, created_at FROM reviews`

// ReviewsForTask returns every judgement of a Task's artifacts, oldest first.
func (s *Store) ReviewsForTask(taskID string) ([]Review, error) {
	rows, err := s.db.Query(reviewSelect+` WHERE task_id = ? ORDER BY seq ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Review{}
	for rows.Next() {
		var r Review
		var passed int
		var findings string
		if err := rows.Scan(&r.ID, &r.TaskID, &r.JobID, &r.ArtifactID, &r.Reviewer,
			&passed, &r.Reasoning, &findings, &r.Detail, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Passed = passed == 1
		if findings != "" {
			_ = json.Unmarshal([]byte(findings), &r.Findings)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
