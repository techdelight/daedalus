// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// recordingReviewer records the spec it was given and returns a fixed verdict.
type recordingReviewer struct {
	called bool
	pass   bool
	detail string
	spec   ReviewSpec
}

func (r *recordingReviewer) Review(_ context.Context, spec ReviewSpec) ReviewOutcome {
	r.called = true
	r.spec = spec
	out := ReviewOutcome{Passed: r.pass, Detail: r.detail, Reasoning: r.detail, Reviewer: "recording"}
	if !r.pass {
		out.Findings = []Finding{
			{Severity: SeverityBlocking, File: "a.txt", Line: 3, What: r.detail, Why: "it is already in helpers.go"},
			{Severity: SeverityNote, What: "no test covers the new branch"},
		}
	}
	return out
}

// TestReview_DoesNotGateIntegration.
//
// Until M20 a configured reviewer blocked integration until it had passed. That
// gave a language model a veto over a human's work — and, worse in the other
// direction, made its PASS load-bearing, so a diff that talked the reviewer round
// would have talked its way into the trunk. The judgement is evidence now; the
// human at the approval gate is the gate.
func TestReview_DoesNotGateIntegration(t *testing.T) {
	repo := gitRepo(t)
	rev := &recordingReviewer{pass: true, detail: "reads fine"}
	svc, store, task := verifiedTask(t, repo, StubVerifyRunner{Pass: true}, "a.txt")
	svc.SetReviewRunner(rev)

	// Unreviewed, and it lands: an artifact nobody has read is a decision for the
	// human, not a refusal from a component that has not run.
	if _, err := svc.IntegrateTask(task.ID, IntegrateRequest{}); err != nil {
		t.Fatalf("integrate without a review = %v, want it allowed", err)
	}
	got, _ := store.GetTask(task.ID)
	if got.State != StateIntegrated {
		t.Fatalf("state = %q, want integrated", got.State)
	}
}

// TestReview_ReportsAndDoesNotAct is the property the whole rung rests on.
//
// A failing review must record everything it found and move NOTHING. A reviewer
// that could reject is an oracle nobody bounded: nothing constrains it, nothing
// reproduces it, and the diff it reads is untrusted input that can address it
// directly.
func TestReview_ReportsAndDoesNotAct(t *testing.T) {
	repo := gitRepo(t)
	rev := &recordingReviewer{pass: false, detail: "this duplicates an existing helper"}
	svc, store, task := verifiedTask(t, repo, StubVerifyRunner{Pass: true}, "a.txt")
	svc.SetReviewRunner(rev)

	res, err := svc.ReviewTask(task.ID)
	if err != nil {
		t.Fatalf("ReviewTask: %v", err)
	}
	if !rev.called {
		t.Fatal("the reviewer was not called")
	}
	if res.Passed {
		t.Error("res.Passed = true, want the reviewer's actual verdict")
	}
	if res.Reason != "" {
		t.Errorf("res.Reason = %q, want empty — a review rejects nothing, so it has no rejection reason", res.Reason)
	}

	// NOTHING MOVED.
	got, _ := store.GetTask(task.ID)
	if got.State != StateVerified {
		t.Errorf("task state = %q, want it UNCHANGED at verified — the reviewer must not transition it", got.State)
	}
	if job, _ := s2job(t, store, task.ID); job.State != StateVerified {
		t.Errorf("job state = %q, want it unchanged at verified", job.State)
	}
	// …and the work is still there to be looked at. Reclaiming the worktree on a
	// model's opinion was the most expensive half of the old behaviour.
	if job, _ := s2job(t, store, task.ID); !svc.worktrees.Exists(job.ID) {
		t.Error("the worktree was reclaimed on a failed review; the artifact must survive the reading")
	}

	// EVERYTHING WAS RECORDED.
	if res.Artifact == nil || res.Artifact.Review != ReviewFail {
		t.Errorf("artifact review = %+v, want fail recorded", res.Artifact)
	}
	if len(res.Findings) != 2 || res.Findings[0].Severity != SeverityBlocking {
		t.Fatalf("findings = %+v, want the reviewer's two, blocking first", res.Findings)
	}
	if res.Findings[0].Why == "" || res.Findings[0].File == "" {
		t.Error("a finding must carry where to look and why it matters; without both it is an opinion")
	}
	if res.Reviewer != "recording" {
		t.Errorf("reviewer = %q, want it attributed — an unattributed verdict is a rumour", res.Reviewer)
	}

	stored, err := store.ReviewsForTask(task.ID)
	if err != nil || len(stored) != 1 {
		t.Fatalf("ReviewsForTask = %d (%v), want 1", len(stored), err)
	}
	if len(stored[0].Findings) != 2 || stored[0].Passed || stored[0].Reviewer != "recording" {
		t.Errorf("stored review = %+v, want the full judgement", stored[0])
	}

	// A second reading accumulates rather than overwriting: the earlier one is
	// part of the record.
	rev.pass = true
	if _, err := svc.ReviewTask(task.ID); err != nil {
		t.Fatalf("second ReviewTask: %v", err)
	}
	stored, _ = store.ReviewsForTask(task.ID)
	if len(stored) != 2 {
		t.Fatalf("%d reviews after two passes, want 2 — an earlier reading must not be overwritten", len(stored))
	}
	if !stored[1].Passed || stored[0].Passed {
		t.Errorf("stored order/verdicts = %+v, want the failing one first", stored)
	}

	// And the log carries it as a DECISION, never as a rejection.
	events, _ := store.ListEventsForTask(task.ID)
	if !hasKind(events, EventReview) {
		t.Error("the review pass itself should be recorded")
	}
	if hasEvent(events, EventRejection, ReasonReviewFailed) {
		t.Error("the log carries a review_failed rejection; nothing was rejected")
	}
}

// TestReview_SeesThePromiseAndTheReason: a diff answers "did it do the thing".
// Only the objective, the rationale and the programme let a reviewer ask whether
// the thing was worth doing — which is the question it exists for.
func TestReview_SeesThePromiseAndTheReason(t *testing.T) {
	repo := gitRepo(t)
	rev := &recordingReviewer{pass: true, detail: "fine"}
	svc, _, task := verifiedTask(t, repo, StubVerifyRunner{Pass: true}, "a.txt")
	svc.SetReviewRunner(rev)

	prog, err := svc.CreateProgramme(ProgrammeRequest{Name: "fluency", Description: "get fluent by spring"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.SetTaskProgramme(task.ID, prog.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.db.Exec(`UPDATE tasks SET rationale = ?, rationale_by = ? WHERE id = ?`,
		"the daily review hangs off it", string(CallerHuman), task.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.ReviewTask(task.ID); err != nil {
		t.Fatalf("ReviewTask: %v", err)
	}
	// Commits, never a working tree — a reviewer must not be shown something
	// different from what would land.
	if rev.spec.HeadSHA == "" || rev.spec.BaseSHA == "" {
		t.Errorf("spec names no commits: %+v", rev.spec)
	}
	if rev.spec.Objective != task.Objective {
		t.Errorf("objective = %q, want %q", rev.spec.Objective, task.Objective)
	}
	if rev.spec.Rationale != "the daily review hangs off it" || rev.spec.RationaleBy != CallerHuman {
		t.Errorf("rationale = %q by %q, want it carried through with its author",
			rev.spec.Rationale, rev.spec.RationaleBy)
	}
	if rev.spec.ProgrammeName != "fluency" || rev.spec.ProgrammeFor != "get fluent by spring" {
		t.Errorf("programme = %q/%q, want the name and what it is for",
			rev.spec.ProgrammeName, rev.spec.ProgrammeFor)
	}
}

// TestReview_NoReviewerSaysSo: asking for a review with no reviewer configured is
// an error rather than a silent pass — the one thing worse than no review is a
// record claiming one happened. Integration is unaffected either way now.
func TestReview_NoReviewerSaysSo(t *testing.T) {
	repo := gitRepo(t)
	svc, store, task := verifiedTask(t, repo, StubVerifyRunner{Pass: true}, "a.txt")
	if _, err := svc.ReviewTask(task.ID); err == nil {
		t.Error("ReviewTask with no reviewer should say so")
	}
	if _, err := svc.IntegrateTask(task.ID, IntegrateRequest{}); err != nil {
		t.Fatalf("integration must not be gated when no reviewer is configured: %v", err)
	}
	got, _ := store.GetTask(task.ID)
	if got.State != StateIntegrated {
		t.Errorf("state = %q, want integrated", got.State)
	}
}

// TestReview_BoundedByBudget: review passes are bounded by max-review-cycles, and
// counted SEPARATELY from verification cycles rather than summed.
func TestReview_BoundedByBudget(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil, StubVerifyRunner{Pass: true})
	svc.SetPolicySource(StaticBudget(Budget{MaxAttempts: 5, MaxReviewCycles: 1, Concurrency: 1}))
	svc.SetReviewRunner(StubReviewRunner{Pass: true})

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	// One verification cycle is consumed here…
	if _, err := svc.VerifyTask(task.ID, VerifyRequest{}); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// …and a review pass is still available, because the two are not summed.
	if _, err := svc.ReviewTask(task.ID); err != nil {
		t.Fatalf("first review should be allowed despite the verification cycle: %v", err)
	}
	// The second review is over budget.
	_, err = svc.ReviewTask(task.ID)
	var rej *RejectionError
	if !errors.As(err, &rej) || rej.Reason != ReasonReviewCyclesExhausted {
		t.Fatalf("second review = %v, want a review_cycles_exhausted refusal", err)
	}
	if n, _ := store.CountReviewRuns(task.ID); n != 1 {
		t.Errorf("review runs = %d, want 1 (a refused pass must not count)", n)
	}
}

func TestReview_Guards(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)
	svc.SetReviewRunner(StubReviewRunner{Pass: true})
	task, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"}) // planned
	if _, err := svc.ReviewTask(task.ID); !errors.Is(err, ErrWrongState) {
		t.Errorf("reviewing a planned task = %v, want ErrWrongState", err)
	}
	if _, err := svc.ReviewTask("T-404"); !errors.Is(err, ErrNotFound) {
		t.Errorf("reviewing an unknown task = %v, want ErrNotFound", err)
	}
}

func TestStubReviewRunner(t *testing.T) {
	for _, pass := range []bool{true, false} {
		out := StubReviewRunner{Pass: pass}.Review(context.Background(), ReviewSpec{})
		if out.Passed != pass || out.Detail == "" {
			t.Errorf("StubReviewRunner{%v} = %+v", pass, out)
		}
	}
}

// TestReview_ReadsAnArtifactWhateverTheOracleSaid.
//
// The reviewer used to require `verified` and a Job in `verified`, which put the
// second opinion downstream of the first — available only once the machine
// oracle already agreed. That made it useless in the case it exists for: the
// oracle grades documents (#74), so a Task it rejects is exactly the one a human
// needs a reading of, and that reading was the one the plane refused to fetch.
func TestReview_ReadsAnArtifactWhateverTheOracleSaid(t *testing.T) {
	repo := gitRepo(t)
	// A verifier that FAILS: the task lands in `rejected`, which is precisely
	// where an operator wants an agent's eyes.
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil,
		StubVerifyRunner{Pass: false, Detail: "docs lint failed on a file the task never opened"})
	rev := &recordingReviewer{pass: true, detail: "the change is fine; the linter is not about it"}
	svc.SetReviewRunner(rev)

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatal(err)
	}

	// A CANDIDATE — graded by nobody yet — is reviewable.
	if _, err := svc.ReviewTask(task.ID); err != nil {
		t.Fatalf("reviewing a candidate = %v, want it allowed", err)
	}

	// And after the oracle rejects it, still reviewable.
	if _, err := svc.VerifyTask(task.ID, VerifyRequest{}); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetTask(task.ID)
	if got.State != StateRejected {
		t.Fatalf("state = %q, want rejected (the stub verifier fails)", got.State)
	}
	res, err := svc.ReviewTask(task.ID)
	if err != nil {
		t.Fatalf("reviewing a REJECTED task = %v — this is the case the reviewer exists for", err)
	}
	if !res.Passed {
		t.Errorf("res = %+v, want the reviewer's own verdict, not the oracle's", res)
	}
	// And it still moves nothing.
	after, _ := store.GetTask(task.ID)
	if after.State != StateRejected {
		t.Errorf("state = %q after a passing review, want it unchanged at rejected", after.State)
	}
}

// Nothing to read is still a refusal: a Task that has produced no artifact has
// no diff, and inventing a reading of one would be worse than saying so.
func TestReview_RefusesWhenNothingHasBeenProduced(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)
	svc.SetReviewRunner(StubReviewRunner{Pass: true})
	task, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"}) // planned
	if _, err := svc.ReviewTask(task.ID); !errors.Is(err, ErrWrongState) {
		t.Errorf("reviewing a planned task = %v, want ErrWrongState", err)
	}
}

// A review is a container run of MINUTES during which nothing about the Task
// changes: no state moves, no job appears. So every surface showed a Task
// sitting still while an agent was reading it — reported from the Ledger as
// "something IS happening, but I cannot see that it is a review".
//
// The plane always knew: withClaim records an inflightOp so a second operation
// on the same Task is refused. The knowledge existed only to say no to a
// machine, never to inform a person. This asserts it now reaches the status a
// human reads — which is what makes it survive a reload, and show a review
// somebody started from the CLI.
func TestTaskStatus_ReportsAReviewInFlight(t *testing.T) {
	var (
		duringReview StatusView
		statusErr    error
	)
	// The reviewer reads the status WHILE it runs — the only moment the claim
	// exists. Asking afterwards would prove nothing.
	svc, store, task := dispatchToCandidate(t, "AGENT_RAN.txt", &recordingVerifier{pass: true})
	svc.reviewer = reviewerFunc(func(ctx context.Context, spec ReviewSpec) ReviewOutcome {
		duringReview, statusErr = svc.TaskStatus(spec.TaskID)
		// CONCURRENTLY too, because that is the real shape: the daemon serves
		// status reads from other goroutines while a review holds its claim, and
		// s.inflight is written under s.mu by beginOp/endOp. Reading it unguarded
		// is a data race, and a single-goroutine test would never show it — this
		// makes `go test -race` able to fail, which is the only way the guard is
		// actually asserted rather than asserted about.
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if v, err := svc.TaskStatus(spec.TaskID); err == nil &&
					v.Scheduling.Operation != "review" {
					t.Errorf("concurrent status saw operation %q, want review", v.Scheduling.Operation)
				}
			}()
		}
		// …while ANOTHER task's claim is taken and released. This is the write
		// half, and it is the half that makes the race real: reading s.inflight
		// during a review races not with this task's own claim (nothing writes it
		// then) but with beginOp/endOp for every OTHER task the daemon is handling.
		// Without that, eight concurrent readers of a map nobody writes prove
		// nothing at all.
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 8; i++ {
				// s.mu HELD, as withClaim documents it must be — every real caller
				// does this. A bare call would manufacture a race in the test rather
				// than exercise the one in the code, which is what the first version
				// of this did and what the detector correctly complained about.
				svc.mu.Lock()
				_ = svc.withClaim("T-other", inflightOp{kind: "dispatch"}, func() error { return nil })
				svc.mu.Unlock()
			}
		}()
		wg.Wait()
		return ReviewOutcome{Passed: true, Reasoning: "read it", Reviewer: "test"}
	})
	if _, err := svc.VerifyTask(task.ID, VerifyRequest{}); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if _, err := svc.ReviewTask(task.ID); err != nil {
		t.Fatalf("review: %v", err)
	}
	if statusErr != nil {
		t.Fatalf("status during review: %v", statusErr)
	}
	if duringReview.Scheduling.Operation != "review" {
		t.Errorf("operation during a review = %q, want %q — the status is where a "+
			"human finds out the plane is busy with this task",
			duringReview.Scheduling.Operation, "review")
	}
	if duringReview.Scheduling.OperationJob == "" {
		t.Error("the job being reviewed should be named, so the entry can point at it")
	}

	// And it is CLEARED afterwards. A stale claim would leave every surface
	// reporting a review that finished, which is worse than reporting none.
	after, err := svc.TaskStatus(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Scheduling.Operation != "" {
		t.Errorf("operation after the review = %q, want empty", after.Scheduling.Operation)
	}
	_ = store
}

// reviewerFunc adapts a function to ReviewRunner.
type reviewerFunc func(context.Context, ReviewSpec) ReviewOutcome

func (f reviewerFunc) Review(ctx context.Context, spec ReviewSpec) ReviewOutcome { return f(ctx, spec) }

// brokenReviewer is a harness that never delivers a reading — the shape a
// reviewer takes when its container will not start, its agent exits non-zero,
// or it writes nothing.
type brokenReviewer struct{ calls int }

func (r *brokenReviewer) Review(_ context.Context, _ ReviewSpec) ReviewOutcome {
	r.calls++
	return reviewUnavailable("the reviewing agent exited with an error: exit status 1")
}

// A REVIEW THAT NEVER HAPPENED MUST NOT SPEND THE BUDGET.
//
// Reported on real work: a review failed because the freshly rebuilt image had
// never been logged into, the operator retried, and the Task was then refused
// with `review_cycles_exhausted` — unable to be reviewed at all, by two passes
// in which no reviewer ever read anything. The budget bounds how many times an
// artifact may be graded, not how many times we may get the grading wrong;
// CountReviewCycles has applied exactly that rule to harness-fault
// re-verifications since Sprint 62, and review passes never got it.
func TestReview_AHarnessFailureDoesNotSpendACycle(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil, StubVerifyRunner{Pass: true})
	svc.SetPolicySource(StaticBudget(Budget{MaxAttempts: 5, MaxReviewCycles: 1, Concurrency: 1}))
	broken := &brokenReviewer{}
	svc.SetReviewRunner(broken)

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	// Three passes against a budget of one. Every one of them must be allowed,
	// because none of them is a reading.
	for i := 1; i <= 3; i++ {
		if _, err := svc.ReviewTask(task.ID); err != nil {
			t.Fatalf("review %d refused, but no reviewer has read anything yet: %v", i, err)
		}
	}
	if broken.calls != 3 {
		t.Errorf("the reviewer was called %d times, want 3", broken.calls)
	}
	if n, _ := store.CountReviewRuns(task.ID); n != 0 {
		t.Errorf("review runs = %d, want 0 — a pass that produced no judgement is not a pass", n)
	}

	// The record still HAS them. Not charging for a failure is not the same as
	// pretending it did not happen: three attempts are in the log, and the
	// operator needs them to see that the harness is broken.
	view, err := svc.TaskStatus(task.ID)
	if err != nil {
		t.Fatalf("TaskStatus: %v", err)
	}
	if len(view.Reviews) != 3 {
		t.Errorf("recorded reviews = %d, want all 3 kept in the record", len(view.Reviews))
	}

	// And a REAL reading still costs, immediately. The budget is not disabled by
	// a failure, it is simply not spent by one.
	svc.SetReviewRunner(StubReviewRunner{Pass: true})
	if _, err := svc.ReviewTask(task.ID); err != nil {
		t.Fatalf("the first real review should be allowed: %v", err)
	}
	_, err = svc.ReviewTask(task.ID)
	var rej *RejectionError
	if !errors.As(err, &rej) || rej.Reason != ReasonReviewCyclesExhausted {
		t.Fatalf("the second real review = %v, want a review_cycles_exhausted refusal", err)
	}
}

// The invariant CountReviewRuns is counting on: `reviewer = ”` means "no
// judgement" and nothing else. AgentReviewer stamps itself and the stub names
// itself; this closes the seam a later ReviewRunner would open by judging and
// forgetting to say who, which would silently make its pass free.
func TestReview_AJudgementAlwaysNamesAReviewer(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil, StubVerifyRunner{Pass: true})
	svc.SetReviewRunner(anonymousReviewer{})

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if _, err := svc.ReviewTask(task.ID); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if n, _ := store.CountReviewRuns(task.ID); n != 1 {
		t.Errorf("review runs = %d, want 1 — a judgement from an unnamed reviewer is still a "+
			"judgement, and must be charged for", n)
	}
}

// anonymousReviewer judges the artifact and says nothing about itself.
type anonymousReviewer struct{}

func (anonymousReviewer) Review(_ context.Context, _ ReviewSpec) ReviewOutcome {
	return ReviewOutcome{Passed: true, Detail: "fine", Reasoning: "read it"}
}

// A REVIEW THAT NEVER HAPPENED MAKES NO STATEMENT ABOUT THE ARTIFACT.
//
// `reviewUnavailable` returns Passed=false because there is no verdict, and
// writing that through as `review=fail` stamps the artifact with "a reviewer
// read this and rejected it" — the exact confusion its own Reasoning field
// exists to deny. Worse, it erases a genuine earlier pass.
func TestReview_AnUnavailableReviewDoesNotMarkTheArtifact(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil, StubVerifyRunner{Pass: true})
	svc.SetReviewRunner(StubReviewRunner{Pass: true})

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	res, err := svc.DispatchTask(task.ID)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	// A real reading first: the artifact records a pass.
	if _, err := svc.ReviewTask(task.ID); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if a, _ := store.GetArtifact(res.Artifact.ID); a.Review != ReviewPass {
		t.Fatalf("artifact review = %q, want pass", a.Review)
	}

	// Then the harness breaks. The earlier reading must survive it.
	svc.SetReviewRunner(&brokenReviewer{})
	if _, err := svc.ReviewTask(task.ID); err != nil {
		t.Fatalf("the unavailable review should still be recorded: %v", err)
	}
	a, _ := store.GetArtifact(res.Artifact.ID)
	if a.Review == ReviewFail {
		t.Error("a review that never ran marked the artifact `review=fail` — the operator now " +
			"reads it as a reviewer's rejection, and a genuine pass was erased")
	}
	if a.Review != ReviewPass {
		t.Errorf("artifact review = %q, want the earlier pass left intact", a.Review)
	}
}

// The stamp must not collide with the word the surfaces already use for "nobody
// is attributed". If it does, a judgement nobody signed and a review that never
// happened read identically — while one is charged to the budget and one is not.
func TestReview_TheStampIsDistinctFromAnEmptyReviewer(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil, StubVerifyRunner{Pass: true})
	svc.SetReviewRunner(anonymousReviewer{})

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if _, err := svc.ReviewTask(task.ID); err != nil {
		t.Fatalf("Review: %v", err)
	}
	reviews, _ := store.ReviewsForTask(task.ID)
	if len(reviews) != 1 {
		t.Fatalf("reviews = %d, want 1", len(reviews))
	}
	if reviews[0].Reviewer == "" {
		t.Fatal("a judged review recorded no reviewer, so CountReviewRuns will not charge it")
	}
	// "unattributed" is what every surface prints for an EMPTY reviewer, so the
	// stamp must be something else or the two become indistinguishable.
	if reviews[0].Reviewer == "unattributed" {
		t.Errorf("the stamp is the same word the surfaces use for an empty reviewer (%q), so a "+
			"judged review and a review that never happened now read identically",
			reviews[0].Reviewer)
	}
}
