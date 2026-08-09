// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- budget resolution (pure) -------------------------------------------------

func TestBudgetPolicy_Layering(t *testing.T) {
	policy := BudgetPolicy{
		Default:  Budget{MaxAttempts: 2},
		Projects: map[string]Budget{"big": {WallClockSeconds: 7200}},
	}
	tests := []struct {
		name    string
		project string
		want    Budget
	}{
		{
			name:    "unknown project gets policy default over built-in",
			project: "small",
			// MaxAttempts from the policy default; everything else from DefaultBudget.
			want: Budget{WallClockSeconds: 3600, MaxAttempts: 2, MaxReviewCycles: 3, Concurrency: 1},
		},
		{
			name:    "project override layers over the policy default",
			project: "big",
			want:    Budget{WallClockSeconds: 7200, MaxAttempts: 2, MaxReviewCycles: 3, Concurrency: 1},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := policy.BudgetFor(tc.project); got != tc.want {
				t.Errorf("BudgetFor(%q) = %+v, want %+v", tc.project, got, tc.want)
			}
		})
	}
}

func TestBudget_ExceededBy(t *testing.T) {
	ceiling := Budget{WallClockSeconds: 600, MaxAttempts: 2, MaxReviewCycles: 0, Concurrency: 1}
	tests := []struct {
		name      string
		requested Budget
		wantAxis  string
	}{
		{"narrower is fine", Budget{WallClockSeconds: 60, MaxAttempts: 1}, ""},
		{"equal is fine", Budget{WallClockSeconds: 600, MaxAttempts: 2}, ""},
		{"unset inherits, never widens", Budget{}, ""},
		{"wider wall-clock is refused", Budget{WallClockSeconds: 601}, "wallClockSeconds"},
		{"wider attempts is refused", Budget{MaxAttempts: 3}, "maxAttempts"},
		{"wider concurrency is refused", Budget{Concurrency: 2}, "concurrency"},
		// A zero ceiling axis is unbounded, so nothing can widen it.
		{"unbounded ceiling axis accepts anything", Budget{MaxReviewCycles: 99}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			axis, over := ceiling.exceededBy(tc.requested)
			if (tc.wantAxis != "") != over {
				t.Fatalf("exceededBy = (%q, %v), want axis %q", axis, over, tc.wantAxis)
			}
			if axis != tc.wantAxis {
				t.Errorf("axis = %q, want %q", axis, tc.wantAxis)
			}
		})
	}
}

func TestLoadBudgetPolicy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "budgets.json")

	// A missing file is not an error: everything falls back to the built-in.
	p, err := LoadBudgetPolicy(path)
	if err != nil {
		t.Fatalf("LoadBudgetPolicy(missing): %v", err)
	}
	if got := p.BudgetFor("anything"); got != DefaultBudget() {
		t.Errorf("missing policy → %+v, want DefaultBudget %+v", got, DefaultBudget())
	}

	body := `{"default":{"maxAttempts":1},"projects":{"app":{"maxAttempts":5,"wallClockSeconds":10}}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err = LoadBudgetPolicy(path)
	if err != nil {
		t.Fatalf("LoadBudgetPolicy: %v", err)
	}
	if got := p.BudgetFor("app"); got.MaxAttempts != 5 || got.WallClockSeconds != 10 {
		t.Errorf("app budget = %+v, want maxAttempts 5 / wallClock 10", got)
	}
	if got := p.BudgetFor("other"); got.MaxAttempts != 1 {
		t.Errorf("other budget = %+v, want the policy default maxAttempts 1", got)
	}

	// A malformed file must not take the plane down.
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBudgetPolicy(path); err == nil {
		t.Error("LoadBudgetPolicy(malformed) should error")
	}
	// With no successful read ever — the same state as a missing file — the
	// built-in default is all there is to fall back to.
	if got := NewFileBudgetPolicy(path).BudgetFor("app"); got != DefaultBudget() {
		t.Errorf("malformed policy with no known-good → %+v, want DefaultBudget", got)
	}
}

// TestLoadBudgetPolicy_RejectsNegative: a negative axis in the policy file would
// read as "unbounded" at every enforcement site, so a typo must not silently
// disable a budget.
func TestLoadBudgetPolicy_RejectsNegative(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		body string
	}{
		{"negative in the default", `{"default":{"maxAttempts":-1}}`},
		{"negative in a project override", `{"projects":{"app":{"wallClockSeconds":-1}}}`},
		{"negative on a policy-only axis", `{"default":{"maxTokens":-5}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "_")+".json")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadBudgetPolicy(path); err == nil {
				t.Errorf("LoadBudgetPolicy(%s) = nil error, want a rejection", tc.body)
			}
		})
	}
}

// TestFileBudgetPolicy_FailsClosed is the regression for "a corrupt budgets.json
// widens every budget". A partial write from a non-atomic editor must hold the
// last known-good policy, never fall back to the (wider) built-in default.
func TestFileBudgetPolicy_FailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budgets.json")
	strict := `{"default":{"wallClockSeconds":30,"maxAttempts":1,"maxReviewCycles":1,"concurrency":1}}`
	if err := os.WriteFile(path, []byte(strict), 0o644); err != nil {
		t.Fatal(err)
	}
	src := NewFileBudgetPolicy(path)
	good := src.BudgetFor("app")
	if good.WallClockSeconds != 30 || good.MaxAttempts != 1 {
		t.Fatalf("precondition: strict policy = %+v", good)
	}

	// Now simulate a partial write mid-edit.
	if err := os.WriteFile(path, []byte(`{"default":{"wallClockSec`), 0o644); err != nil {
		t.Fatal(err)
	}
	degraded := src.BudgetFor("app")
	if degraded != good {
		t.Errorf("corrupt policy → %+v, want the last known-good %+v", degraded, good)
	}
	if degraded.WallClockSeconds > good.WallClockSeconds || degraded.MaxAttempts > good.MaxAttempts {
		t.Errorf("corrupt policy WIDENED the budget: %+v → %+v", good, degraded)
	}
	// A negative slipped into the file is a parse error too, so it also holds.
	if err := os.WriteFile(path, []byte(`{"default":{"maxAttempts":-1}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := src.BudgetFor("app"); got != good {
		t.Errorf("negative policy → %+v, want the last known-good %+v", got, good)
	}
	// And a good write is picked up again without a restart.
	looser := `{"default":{"wallClockSeconds":60,"maxAttempts":2,"maxReviewCycles":2,"concurrency":1}}`
	if err := os.WriteFile(path, []byte(looser), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := src.BudgetFor("app"); got.WallClockSeconds != 60 || got.MaxAttempts != 2 {
		t.Errorf("recovered policy = %+v, want the new values", got)
	}
}

// TestBudget_NegativeIsNeverUnbounded is the regression for the audit's critical
// finding: 0 means unbounded and every enforcement site guards `> 0`, so a
// negative that reached a Task row would disable that axis entirely.
//
// The subtests below cover every door a value can arrive through — a create
// request, the host-side policy file, an injected BudgetSource (the ceiling
// itself, where a negative would make exceededBy permit everything), and a row
// already in the database. Each is closed independently rather than relying on
// any one of them being the only entry point.
func TestBudget_NegativeIsNeverUnbounded(t *testing.T) {
	t.Run("invalidAxis names every negative axis", func(t *testing.T) {
		for i, b := range []Budget{
			{WallClockSeconds: -1}, {MaxAttempts: -1}, {MaxReviewCycles: -1},
			{Concurrency: -1}, {MaxTurns: -1}, {MaxTokens: -1}, {MaxCostCents: -1},
		} {
			if axis, bad := b.invalidAxis(); !bad {
				t.Errorf("budget %d (%+v): invalidAxis = (%q, false), want a rejection", i, b, axis)
			}
		}
		if _, bad := DefaultBudget().invalidAxis(); bad {
			t.Error("DefaultBudget must be valid")
		}
	})

	t.Run("exceededBy treats a negative as a widening", func(t *testing.T) {
		ceiling := DefaultBudget()
		if axis, over := ceiling.exceededBy(Budget{MaxAttempts: -1}); !over || axis != "maxAttempts" {
			t.Errorf("exceededBy(-1) = (%q, %v), want (maxAttempts, true)", axis, over)
		}
	})

	t.Run("sanitized replaces a negative with the fallback", func(t *testing.T) {
		got := Budget{WallClockSeconds: -1, MaxAttempts: 2}.sanitized(DefaultBudget())
		if got.WallClockSeconds != DefaultBudget().WallClockSeconds {
			t.Errorf("wallClock = %d, want the default", got.WallClockSeconds)
		}
		if got.MaxAttempts != 2 {
			t.Errorf("a valid axis must be left alone, got %d", got.MaxAttempts)
		}
	})

	t.Run("a create request with a negative axis is refused server-side", func(t *testing.T) {
		repo := gitRepo(t)
		svc, _, store := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)
		// Straight at the Service — the CLI's own flag validation is bypassed,
		// exactly as an HTTP client (or Sprint 60's agent) would bypass it.
		_, err := svc.CreateTask(CreateTaskRequest{
			Project: "app", Objective: "x", Budget: &Budget{MaxAttempts: -1},
		})
		var rej *RejectionError
		if !errors.As(err, &rej) {
			t.Fatalf("negative budget create err = %v, want *RejectionError", err)
		}
		if rej.Reason != ReasonInvalidBudget {
			t.Errorf("reason = %q, want %q", rej.Reason, ReasonInvalidBudget)
		}
		if tasks, _ := store.ListTasks(); len(tasks) != 0 {
			t.Errorf("a negative budget must not create a task, got %d", len(tasks))
		}
	})

	t.Run("a negative CEILING cannot turn the check into a rubber stamp", func(t *testing.T) {
		repo := gitRepo(t)
		svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)
		// BudgetSource is exported and injectable, so a ceiling can arrive without
		// passing LoadBudgetPolicy. A negative ceiling axis would make exceededBy's
		// `permitted > 0` guard permit anything at all.
		svc.SetBudgetSource(StaticBudget(Budget{
			WallClockSeconds: -1, MaxAttempts: -1, MaxReviewCycles: -1, Concurrency: -1,
		}))
		ceiling := svc.budgetCeiling("app")
		if axis, bad := ceiling.invalidAxis(); bad {
			t.Fatalf("a negative survived into the ceiling on %s: %+v", axis, ceiling)
		}
		if ceiling != DefaultBudget() {
			t.Errorf("sanitized ceiling = %+v, want DefaultBudget", ceiling)
		}
		// And an over-budget request against that ceiling is still refused.
		_, err := svc.CreateTask(CreateTaskRequest{
			Project: "app", Objective: "x", Budget: &Budget{MaxAttempts: 9999},
		})
		var rej *RejectionError
		if !errors.As(err, &rej) || rej.Reason != ReasonOverBudget {
			t.Errorf("create against a negative ceiling = %v, want an over_budget refusal", err)
		}
	})

	t.Run("a negative already in the DB is sanitized on read", func(t *testing.T) {
		s := openTestStore(t)
		if _, err := s.CreateTask(NewTask{Project: "p", Objective: "o", BaseSHA: "sha",
			Budget: DefaultBudget()}, StatePlanned); err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := s.db.Exec(`UPDATE tasks SET budget = ? WHERE id = 'T-1'`,
			`{"wallClockSeconds":-1,"maxAttempts":-1,"maxReviewCycles":-1,"concurrency":-1}`); err != nil {
			t.Fatalf("plant negative budget: %v", err)
		}
		got, err := s.GetTask("T-1")
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		if axis, bad := got.Budget.invalidAxis(); bad {
			t.Errorf("a negative survived the row scan on %s: %+v", axis, got.Budget)
		}
		if got.Budget != DefaultBudget() {
			t.Errorf("sanitized budget = %+v, want DefaultBudget", got.Budget)
		}
	})
}

// --- budget captured at create ------------------------------------------------

func TestCreateTask_BudgetCapturedAndNarrowed(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)
	svc.SetBudgetSource(StaticBudget(Budget{WallClockSeconds: 600, MaxAttempts: 2, MaxReviewCycles: 2, Concurrency: 1}))

	task, err := svc.CreateTask(CreateTaskRequest{
		Project: "app", Objective: "x",
		Budget: &Budget{MaxAttempts: 1}, // narrower — allowed
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task.Budget.MaxAttempts != 1 {
		t.Errorf("requested narrowing lost: maxAttempts = %d, want 1", task.Budget.MaxAttempts)
	}
	if task.Budget.WallClockSeconds != 600 {
		t.Errorf("unset axis should inherit the ceiling: wallClock = %d, want 600", task.Budget.WallClockSeconds)
	}
	// It is stored authoritatively, not just returned.
	reread, _ := store.GetTask(task.ID)
	if reread.Budget != task.Budget {
		t.Errorf("stored budget %+v != returned %+v", reread.Budget, task.Budget)
	}
}

func TestCreateTask_OverBudgetRefused(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)
	svc.SetBudgetSource(StaticBudget(Budget{WallClockSeconds: 600, MaxAttempts: 2, MaxReviewCycles: 2, Concurrency: 1}))

	_, err := svc.CreateTask(CreateTaskRequest{
		Project: "app", Objective: "x",
		Budget: &Budget{MaxAttempts: 99}, // wider than the ceiling
	})
	var rej *RejectionError
	if !errors.As(err, &rej) {
		t.Fatalf("CreateTask(over budget) err = %v, want *RejectionError", err)
	}
	if rej.Reason != ReasonOverBudget {
		t.Errorf("reason = %q, want %q", rej.Reason, ReasonOverBudget)
	}
	// The refusal created nothing…
	tasks, _ := store.ListTasks()
	if len(tasks) != 0 {
		t.Errorf("an over-budget request must not create a task, got %d", len(tasks))
	}
	// …but it is on the record.
	events, _ := store.ListEvents()
	if len(events) != 1 || events[0].Reason != ReasonOverBudget || events[0].Kind != EventBudget {
		t.Fatalf("expected one budget-refusal event, got %+v", events)
	}
	// No task existed yet, so the refusal is filed against the project — an event
	// with an empty entity id would be unqueryable.
	if events[0].EntityType != "project" || events[0].EntityID != "app" {
		t.Errorf("refusal filed against %s/%s, want project/app", events[0].EntityType, events[0].EntityID)
	}
}

// --- max-attempts -------------------------------------------------------------

func TestDispatch_MaxAttemptsRefused(t *testing.T) {
	repo := gitRepo(t)
	// A verifier that always rejects, so each attempt lands back in `rejected`
	// and is retryable until the attempt budget runs out.
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, StubVerifyRunner{Pass: false})
	svc.SetBudgetSource(StaticBudget(Budget{MaxAttempts: 2, MaxReviewCycles: 5, Concurrency: 1}))

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// Attempt 1 → candidate → rejected.
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("attempt 1: %v", err)
	}
	if _, err := svc.VerifyTask(task.ID); err != nil {
		t.Fatalf("verify 1: %v", err)
	}
	// Attempt 2 → candidate → rejected.
	if _, err := svc.RetryTask(task.ID, RetryRequest{}); err != nil {
		t.Fatalf("attempt 2: %v", err)
	}
	if _, err := svc.VerifyTask(task.ID); err != nil {
		t.Fatalf("verify 2: %v", err)
	}
	// Attempt 3 is over budget.
	_, err = svc.RetryTask(task.ID, RetryRequest{})
	var rej *RejectionError
	if !errors.As(err, &rej) {
		t.Fatalf("attempt 3 err = %v, want *RejectionError", err)
	}
	if rej.Reason != ReasonAttemptsExhausted {
		t.Errorf("reason = %q, want %q", rej.Reason, ReasonAttemptsExhausted)
	}
	// The refusal changed nothing: still rejected, still exactly 2 jobs.
	got, _ := store.GetTask(task.ID)
	if got.State != StateRejected {
		t.Errorf("task state = %q, want rejected (a refusal changes no state)", got.State)
	}
	jobs, _ := store.ListJobsForTask(task.ID)
	if len(jobs) != 2 {
		t.Errorf("job count = %d, want 2 (the refused attempt must not create a Job)", len(jobs))
	}
	// A plain dispatch is refused identically — the budget is not a CLI-path quirk.
	if _, err := svc.DispatchTask(task.ID); !errors.As(err, &rej) || rej.Reason != ReasonAttemptsExhausted {
		t.Errorf("dispatch past max-attempts err = %v, want attempts_exhausted", err)
	}
}

// --- max-review-cycles --------------------------------------------------------

func TestVerify_ReviewCyclesRefused(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, StubVerifyRunner{Pass: false})
	svc.SetBudgetSource(StaticBudget(Budget{MaxAttempts: 5, MaxReviewCycles: 1, Concurrency: 1}))

	task, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("dispatch 1: %v", err)
	}
	if _, err := svc.VerifyTask(task.ID); err != nil { // cycle 1 → rejected
		t.Fatalf("verify 1: %v", err)
	}
	if _, err := svc.RetryTask(task.ID, RetryRequest{}); err != nil { // → candidate again
		t.Fatalf("retry: %v", err)
	}
	_, err := svc.VerifyTask(task.ID) // cycle 2 — over the review budget
	var rej *RejectionError
	if !errors.As(err, &rej) {
		t.Fatalf("verify 2 err = %v, want *RejectionError", err)
	}
	if rej.Reason != ReasonReviewCyclesExhausted {
		t.Errorf("reason = %q, want %q", rej.Reason, ReasonReviewCyclesExhausted)
	}
	// A refusal, not a verdict: the candidate is untouched and still inspectable.
	got, _ := store.GetTask(task.ID)
	if got.State != StateCandidate {
		t.Errorf("task state = %q, want candidate (a review-budget refusal is not a verdict)", got.State)
	}
}

// --- concurrency --------------------------------------------------------------

func TestDispatch_ConcurrencyRefused(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)
	svc.SetBudgetSource(StaticBudget(Budget{MaxAttempts: 5, MaxReviewCycles: 5, Concurrency: 1}))

	task, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	// Stage a job that is already running for this project (as a prior dispatch
	// would have left it), then ask for another.
	_, _ = store.TransitionTask(task.ID, StateQueued, false, "")
	_, _ = store.TransitionTask(task.ID, StateWorking, false, "")
	if _, err := store.CreateJob(task.ID, task.BaseSHA, "claude", 0, StateWorking); err != nil {
		t.Fatalf("seed running job: %v", err)
	}
	_, _ = store.TransitionTask(task.ID, StateCandidate, false, "")
	_, _ = store.TransitionTask(task.ID, StateRejected, false, "")

	_, err := svc.DispatchTask(task.ID)
	var rej *RejectionError
	if !errors.As(err, &rej) {
		t.Fatalf("dispatch err = %v, want *RejectionError", err)
	}
	if rej.Reason != ReasonConcurrencyExceeded {
		t.Errorf("reason = %q, want %q", rej.Reason, ReasonConcurrencyExceeded)
	}
}

func TestCountRunningJobsForProject_ExcludesIdleStates(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.CreateTask(NewTask{Project: "proj", Objective: "o", BaseSHA: "sha"}, StatePlanned); err != nil {
		t.Fatalf("create task: %v", err)
	}
	mustTransition(t, s, "T-1", StateQueued, false)
	mustTransition(t, s, "T-1", StateWorking, false)
	job, err := s.CreateJob("T-1", "sha", "claude", 0, StateWorking)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if n, _ := s.CountRunningJobsForProject("proj"); n != 1 {
		t.Errorf("working job counted %d, want 1", n)
	}
	// candidate/rejected are non-terminal but IDLE — they hold no runner slot, so
	// a legitimate retry must not look like a concurrency breach.
	for _, st := range []State{StateCandidate, StateRejected} {
		if _, err := s.TransitionJob(job.ID, st, false, ""); err != nil {
			t.Fatalf("→ %s: %v", st, err)
		}
		if n, _ := s.CountRunningJobsForProject("proj"); n != 0 {
			t.Errorf("%s job counted %d as running, want 0", st, n)
		}
	}
}

// --- wall-clock ---------------------------------------------------------------

// blockingRunner blocks until release is closed (or its context ends), so the
// wall-clock race is exercised deterministically without sleeping for real.
type blockingRunner struct {
	release chan struct{}
}

func (r blockingRunner) Run(ctx context.Context, _ JobSpec) RunOutcome {
	select {
	case <-r.release:
		return RunOutcome{Result: ExecSuccess}
	case <-ctx.Done():
		// A cooperative runner notices its context; the plane's verdict does not
		// depend on that, which is the point of the race in runUnderWallClock.
		return RunOutcome{Result: ExecCancelled, Detail: "ctx"}
	}
}

func TestRunUnderWallClock(t *testing.T) {
	t.Run("overrun is classified as timeout", func(t *testing.T) {
		r := blockingRunner{release: make(chan struct{})}
		out := runUnderWallClock(r, JobSpec{Budget: 1, JobID: "J-1"})
		if out.Result != ExecTimeout {
			t.Errorf("result = %q, want timeout", out.Result)
		}
		if out.Detail == "" {
			t.Error("a timeout should say why")
		}
		close(r.release) // let the abandoned goroutine finish
	})

	t.Run("a run inside the budget keeps its own outcome", func(t *testing.T) {
		r := blockingRunner{release: make(chan struct{})}
		close(r.release)
		out := runUnderWallClock(r, JobSpec{Budget: 60, JobID: "J-2"})
		if out.Result != ExecSuccess {
			t.Errorf("result = %q, want success", out.Result)
		}
	})

	t.Run("budget 0 is unbounded", func(t *testing.T) {
		out := runUnderWallClock(StubRunner{Result: ExecSuccess}, JobSpec{Budget: 0})
		if out.Result != ExecSuccess {
			t.Errorf("result = %q, want success", out.Result)
		}
	})
}

func TestDispatch_WallClockTimeout_FailsJob(t *testing.T) {
	repo := gitRepo(t)
	r := blockingRunner{release: make(chan struct{})}
	t.Cleanup(func() { close(r.release) })
	svc, wt, store := newService(t, mapResolver{"app": repo}, r, nil)
	svc.SetBudgetSource(StaticBudget(Budget{WallClockSeconds: 1, MaxAttempts: 3, MaxReviewCycles: 3, Concurrency: 1}))

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "hang"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	start := time.Now()
	res, err := svc.DispatchTask(task.ID)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("dispatch took %s — the wall-clock budget did not bound it", elapsed)
	}
	if res.Job.ExecutionResult != ExecTimeout {
		t.Errorf("execution_result = %q, want timeout", res.Job.ExecutionResult)
	}
	if res.Job.State != StateFailed {
		t.Errorf("job state = %q, want failed", res.Job.State)
	}
	if res.Artifact != nil {
		t.Error("a timed-out job must not produce a candidate artifact")
	}
	// The job records the budget it ran under.
	if res.Job.Budget != 1 {
		t.Errorf("job budget = %d, want 1", res.Job.Budget)
	}
	got, _ := store.GetTask(task.ID)
	if got.State != StateFailed {
		t.Errorf("task state = %q, want failed", got.State)
	}
	if wt.Exists(res.Job.ID) {
		t.Error("a timed-out job's worktree should be reclaimed")
	}
}

// --- persistence --------------------------------------------------------------

func TestBudget_LegacyRowReadsAsDefault(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.CreateTask(NewTask{Project: "p", Objective: "o", BaseSHA: "sha",
		Budget: Budget{WallClockSeconds: 42, MaxAttempts: 1, MaxReviewCycles: 1, Concurrency: 1}}, StatePlanned); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Simulate a row written before the budget column existed.
	if _, err := s.db.Exec(`UPDATE tasks SET budget = '' WHERE id = 'T-1'`); err != nil {
		t.Fatalf("blank budget: %v", err)
	}
	got, err := s.GetTask("T-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Budget != DefaultBudget() {
		t.Errorf("legacy budget = %+v, want DefaultBudget %+v", got.Budget, DefaultBudget())
	}
	// Unparseable JSON degrades the same way rather than making the row unreadable.
	if _, err := s.db.Exec(`UPDATE tasks SET budget = 'not json' WHERE id = 'T-1'`); err != nil {
		t.Fatalf("corrupt budget: %v", err)
	}
	if got, err = s.GetTask("T-1"); err != nil || got.Budget != DefaultBudget() {
		t.Errorf("corrupt budget → (%+v, %v), want DefaultBudget and no error", got.Budget, err)
	}
}

func TestBudget_JSONRoundTrip(t *testing.T) {
	// The wire form is what the CLI and (later) guild-control-mcp exchange.
	b := Budget{WallClockSeconds: 10, MaxAttempts: 2, MaxReviewCycles: 1, Concurrency: 1, MaxTokens: 500}
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var back Budget
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back != b {
		t.Errorf("round trip = %+v, want %+v", back, b)
	}
}

// TestBudget_PolicyOnlyAxesAreNotEnforced pins the honest §6 split in place: the
// turn/token/cost axes are recorded and surfaced, and nothing enforces them.
// If a future sprint starts enforcing one, this test must be updated
// deliberately — that is the point of asserting it.
func TestBudget_PolicyOnlyAxesAreNotEnforced(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{Result: ExecSuccess, WriteFile: true}, nil)
	svc.SetBudgetSource(StaticBudget(Budget{
		MaxAttempts: 3, MaxReviewCycles: 3, Concurrency: 1,
		MaxTurns: 1, MaxTokens: 1, MaxCostCents: 1, // absurdly low, and inert
	}))
	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task.Budget.MaxTurns != 1 || task.Budget.MaxTokens != 1 || task.Budget.MaxCostCents != 1 {
		t.Errorf("policy-only axes should be recorded on the task: %+v", task.Budget)
	}
	// They must not block anything: the dispatch runs to a candidate regardless.
	res, err := svc.DispatchTask(task.ID)
	if err != nil {
		t.Fatalf("dispatch must not be blocked by unenforced axes: %v", err)
	}
	if res.Job.State != StateCandidate {
		t.Errorf("job state = %q, want candidate", res.Job.State)
	}
	for _, axis := range PolicyOnlyAxes() {
		if axis == "" {
			t.Error("PolicyOnlyAxes must name the unenforced axes for the docs")
		}
	}
}
