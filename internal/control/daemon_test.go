// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// serveUDS starts the Server on a temp Unix socket and returns a Client dialing
// it. The server is shut down on cleanup.
func serveUDS(t *testing.T, svc *Service) *Client {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "control.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: NewServer(svc).Handler()}
	go srv.Serve(l)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	// Wait for the socket to accept.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if c, err := net.Dial("unix", sock); err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server never came up")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return NewClient(sock)
}

func TestDaemon_ClientRoundTrip(t *testing.T) {
	repo := gitRepo(t)
	svc, wt, _ := newService(t, mapResolver{"app": repo}, StubRunner{Result: ExecSuccess, WriteFile: true}, nil)
	client := serveUDS(t, svc)

	// Create over the wire.
	task, err := client.CreateTask(CreateTaskRequest{Project: "app", Objective: "wire it"})
	if err != nil {
		t.Fatalf("client CreateTask: %v", err)
	}
	if task.ID != "T-1" || len(task.BaseSHA) != 40 {
		t.Fatalf("bad task over wire: %+v", task)
	}

	// List.
	tasks, err := client.ListTasks()
	if err != nil || len(tasks) != 1 {
		t.Fatalf("client ListTasks: %v len=%d", err, len(tasks))
	}

	// Dispatch → candidate + artifact over the wire.
	res, err := client.DispatchTask(task.ID)
	if err != nil {
		t.Fatalf("client DispatchTask: %v", err)
	}
	if res.Job.State != StateCandidate || res.Artifact == nil {
		t.Fatalf("dispatch over wire: state=%s artifact=%v", res.Job.State, res.Artifact)
	}

	// Status shows the job + artifact.
	view, err := client.TaskStatus(task.ID)
	if err != nil {
		t.Fatalf("client TaskStatus: %v", err)
	}
	if len(view.Jobs) != 1 || len(view.Jobs[0].Artifacts) != 1 {
		t.Fatalf("status over wire wrong: %+v", view.Jobs)
	}

	// Cancel (candidate → cancelled) reclaims the worktree.
	jobID := res.Job.ID
	if _, err := client.CancelTask(task.ID); err != nil {
		t.Fatalf("client CancelTask: %v", err)
	}
	if wt.Exists(jobID) {
		t.Error("cancel should reclaim the candidate job's worktree")
	}
}

func TestDaemon_VerifyRoundTrip(t *testing.T) {
	repo := gitRepo(t)
	// gate-clean marker + passing stub verifier (the helper's default).
	svc, _, _ := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "AGENT_RAN.txt"}, nil)
	client := serveUDS(t, svc)

	task, err := client.CreateTask(CreateTaskRequest{Project: "app", Objective: "verify me"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := client.DispatchTask(task.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	res, err := client.VerifyTask(task.ID)
	if err != nil {
		t.Fatalf("client VerifyTask: %v", err)
	}
	if !res.Verified || res.Job.State != StateVerified {
		t.Fatalf("verify over wire: verified=%v state=%s", res.Verified, res.Job.State)
	}
}

func TestDaemon_ErrorMapping(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo, "plain": t.TempDir()}, StubRunner{}, nil)
	client := serveUDS(t, svc)

	// Not found → ErrNotFound survives the wire.
	if _, err := client.TaskStatus("T-404"); !errors.Is(err, ErrNotFound) {
		t.Errorf("status missing = %v, want ErrNotFound", err)
	}

	// Non-git project → 400.
	if _, err := client.CreateTask(CreateTaskRequest{Project: "plain", Objective: "x"}); err == nil {
		t.Error("create non-git over wire = nil, want error")
	}

	// A second active task on one project is ALLOWED since Sprint 61 — concurrency
	// is the scheduler's decision at dispatch, not a create-time refusal.
	if _, err := client.CreateTask(CreateTaskRequest{Project: "app", Objective: "first"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := client.CreateTask(CreateTaskRequest{Project: "app", Objective: "second"}); err != nil {
		t.Errorf("second active create over wire = %v, want success", err)
	}
}

// TestDaemon_GovernanceRoundTrip exercises the Sprint-58 routes end to end over
// the socket: retry, replan, and the read-only event log.
func TestDaemon_GovernanceRoundTrip(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, StubVerifyRunner{Pass: false})
	client := serveUDS(t, svc)

	task, err := client.CreateTask(CreateTaskRequest{Project: "app", Objective: "first go"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// The budget survives the wire (it is part of the Task).
	if task.Budget != DefaultBudget() {
		t.Errorf("budget over wire = %+v, want DefaultBudget", task.Budget)
	}
	if _, err := client.DispatchTask(task.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	vres, err := client.VerifyTask(task.ID)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if vres.Reason != ReasonVerifyFailed {
		t.Errorf("rejection reason over wire = %q, want %q", vres.Reason, ReasonVerifyFailed)
	}

	rres, err := client.RetryTask(task.ID, RetryRequest{})
	if err != nil {
		t.Fatalf("client RetryTask: %v", err)
	}
	if rres.Attempt != 2 || rres.Dispatch.Job.ID == "" {
		t.Errorf("retry over wire = %+v, want attempt 2 with a fresh job", rres)
	}

	if _, err := client.VerifyTask(task.ID); err != nil {
		t.Fatalf("verify 2: %v", err)
	}
	replanned, err := client.ReplanTask(task.ID, ReplanRequest{Objective: "second go"})
	if err != nil {
		t.Fatalf("client ReplanTask: %v", err)
	}
	if replanned.Objective != "second go" || replanned.State != StatePlanned {
		t.Errorf("replan over wire = %+v, want 'second go' in planned", replanned)
	}

	events, err := client.TaskEvents(task.ID)
	if err != nil {
		t.Fatalf("client TaskEvents: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("no events over the wire")
	}
	if !hasEvent(events, EventRejection, ReasonVerifyFailed) {
		t.Error("the typed rejection reason should survive the wire")
	}
	if _, err := client.TaskEvents("T-404"); !errors.Is(err, ErrNotFound) {
		t.Errorf("events for an unknown task = %v, want ErrNotFound", err)
	}
}

// TestDaemon_RejectionSurvivesTheWire is the load-bearing half of "a client can
// tell 'refused by policy' from 'failed'": the typed refusal must arrive as the
// same *RejectionError the in-process Service raised, with its reason intact and
// its message un-doubled.
func TestDaemon_RejectionSurvivesTheWire(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, StubVerifyRunner{Pass: false})
	svc.SetBudgetSource(StaticBudget(Budget{WallClockSeconds: 60, MaxAttempts: 1, MaxReviewCycles: 1, Concurrency: 1}))
	client := serveUDS(t, svc)

	// 1. An over-budget create.
	_, err := client.CreateTask(CreateTaskRequest{Project: "app", Objective: "x", Budget: &Budget{MaxAttempts: 99}})
	var rej *RejectionError
	if !errors.As(err, &rej) {
		t.Fatalf("over-budget create over wire = %v, want *RejectionError", err)
	}
	if rej.Reason != ReasonOverBudget {
		t.Errorf("reason = %q, want %q", rej.Reason, ReasonOverBudget)
	}
	if strings.Count(err.Error(), "refused by policy") != 1 {
		t.Errorf("the refusal message is doubled on the wire: %q", err.Error())
	}

	// 2. An exhausted attempt budget on dispatch.
	task, err := client.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := client.DispatchTask(task.ID); err != nil {
		t.Fatalf("dispatch 1: %v", err)
	}
	if _, err := client.VerifyTask(task.ID); err != nil {
		t.Fatalf("verify: %v", err)
	}
	_, err = client.RetryTask(task.ID, RetryRequest{})
	if !errors.As(err, &rej) || rej.Reason != ReasonAttemptsExhausted {
		t.Fatalf("retry past the budget = %v, want attempts_exhausted", err)
	}
	// A refusal is NOT a not-found and NOT a bare error — the distinction is the
	// whole point.
	if errors.Is(err, ErrNotFound) {
		t.Error("a policy refusal must not masquerade as ErrNotFound")
	}
}

// TestDaemon_NegativeBudgetRefusedOverTheWire is the regression for the audit's
// critical finding. The CLI rejects negative budget flags, but the CLI is not the
// security boundary — the socket API is, and Sprint 60 puts an agent on it. This
// posts the raw JSON a non-CLI client would send.
func TestDaemon_NegativeBudgetRefusedOverTheWire(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo}, StubRunner{Result: ExecSuccess, WriteFile: true}, nil)
	svc.SetBudgetSource(StaticBudget(Budget{WallClockSeconds: 60, MaxAttempts: 2, MaxReviewCycles: 2, Concurrency: 1}))
	handler := NewServer(svc).Handler()

	for _, body := range []string{
		`{"project":"app","objective":"x","budget":{"maxAttempts":-1}}`,
		`{"project":"app","objective":"x","budget":{"wallClockSeconds":-1}}`,
		`{"project":"app","objective":"x","budget":{"concurrency":-1}}`,
		`{"project":"app","objective":"x","budget":{"maxReviewCycles":-1}}`,
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("POST %s = %d, want 422 (refused by policy)\n%s", body, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), string(ReasonInvalidBudget)) {
			t.Errorf("POST %s body = %s, want reason %q", body, rec.Body.String(), ReasonInvalidBudget)
		}
	}
	if tasks, _ := store.ListTasks(); len(tasks) != 0 {
		t.Fatalf("a negative budget created %d task(s) — it must create none", len(tasks))
	}

	// And the client rebuilds the typed refusal on the far side.
	client := serveUDS(t, svc)
	_, err := client.CreateTask(CreateTaskRequest{Project: "app", Objective: "x", Budget: &Budget{MaxAttempts: -1}})
	var rej *RejectionError
	if !errors.As(err, &rej) || rej.Reason != ReasonInvalidBudget {
		t.Fatalf("client CreateTask(negative) = %v, want an invalid_budget rejection", err)
	}
}

// TestDaemon_WrongStateIsConflictNot500 pins the documented wire contract: a
// request refused because the entity is in the wrong state is a 409 conflict, not
// a 500. A 500 says "the plane broke"; these are ordinary, expected answers.
func TestDaemon_WrongStateIsConflictNot500(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, StubVerifyRunner{Pass: true})
	handler := NewServer(svc).Handler()

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"}) // planned
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	base := "/tasks/" + task.ID

	// On a `planned` task: retry, replan and verify all fail their state
	// precondition.
	planned := []struct {
		name, method, path, body string
	}{
		{"retry a task that was never rejected", http.MethodPost, base + "/retry", `{}`},
		{"replan a task that was never rejected", http.MethodPost, base + "/replan", `{"objective":"new"}`},
		{"verify a task that is not a candidate", http.MethodPost, base + "/verify", ``},
	}
	for _, tc := range planned {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
			if rec.Code != http.StatusConflict {
				t.Errorf("%s %s = %d, want 409\n%s", tc.method, tc.path, rec.Code, rec.Body.String())
			}
		})
	}

	// And on a `verified` task, dispatch is not allowed either.
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err := svc.VerifyTask(task.ID); err != nil {
		t.Fatalf("verify: %v", err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, base+"/dispatch", nil))
	if rec.Code != http.StatusConflict {
		t.Errorf("dispatch of a verified task = %d, want 409\n%s", rec.Code, rec.Body.String())
	}
}

// TestDaemon_IntegrationRoundTrip exercises the Sprint-59 routes over the socket:
// review, approve/reject, integrate, the approvals queue, and the targets view.
func TestDaemon_IntegrationRoundTrip(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil, StubVerifyRunner{Pass: true})
	svc.SetPolicySource(StaticPolicy{Budget: DefaultBudget(), Approval: true})
	svc.SetReviewRunner(StubReviewRunner{Pass: true})
	client := serveUDS(t, svc)

	task, err := client.CreateTask(CreateTaskRequest{Project: "app", Objective: "land something"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// The target is visible over the wire and was adopted at create.
	targets, err := client.ProjectTargets()
	if err != nil || len(targets) != 1 {
		t.Fatalf("ProjectTargets = (%+v, %v), want one target", targets, err)
	}
	if len(targets[0].Projects) != 1 || targets[0].Projects[0] != "app" {
		t.Errorf("target projects = %v, want [app]", targets[0].Projects)
	}
	if targets[0].SHA != task.BaseSHA {
		t.Errorf("target %s != task base %s", targets[0].SHA, task.BaseSHA)
	}

	if _, err := client.DispatchTask(task.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err := client.VerifyTask(task.ID); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// Approval is required, so it shows up in the queue.
	pending, err := client.PendingApprovals()
	if err != nil || len(pending) != 1 || pending[0].ID != task.ID {
		t.Fatalf("PendingApprovals = (%+v, %v), want [%s]", pending, err, task.ID)
	}
	// The reviewer gates integration; run it over the wire.
	rres, err := client.ReviewTask(task.ID)
	if err != nil {
		t.Fatalf("ReviewTask: %v", err)
	}
	if !rres.Passed || rres.Artifact == nil || rres.Artifact.Review != ReviewPass {
		t.Fatalf("review over wire = %+v", rres)
	}
	// Integrating before approval is a typed refusal, not a 500.
	_, err = client.IntegrateTask(task.ID)
	var rej *RejectionError
	if !errors.As(err, &rej) || rej.Reason != ReasonApprovalRequired {
		t.Fatalf("integrate before approval = %v, want approval_required", err)
	}

	approved, err := client.ApproveTask(task.ID, "ship it")
	if err != nil {
		t.Fatalf("ApproveTask: %v", err)
	}
	if approved.State != StateApproved {
		t.Fatalf("state = %q, want approved", approved.State)
	}
	ires, err := client.IntegrateTask(task.ID)
	if err != nil {
		t.Fatalf("IntegrateTask: %v", err)
	}
	if ires.NewTarget == ires.PreviousTarget || ires.MergedSHA == "" {
		t.Errorf("integration result over wire looks wrong: %+v", ires)
	}
	// The target advanced, over the wire.
	targets, _ = client.ProjectTargets()
	if targets[0].SHA != ires.NewTarget {
		t.Errorf("target = %s, want %s", targets[0].SHA, ires.NewTarget)
	}
	// Nothing is pending any more.
	if pending, _ := client.PendingApprovals(); len(pending) != 0 {
		t.Errorf("PendingApprovals = %+v, want empty", pending)
	}
}

func TestDaemon_RejectApprovalAndSyncTarget(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil, StubVerifyRunner{Pass: true})
	svc.SetPolicySource(StaticPolicy{Budget: DefaultBudget(), Approval: true})
	client := serveUDS(t, svc)

	task, err := client.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := client.DispatchTask(task.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err := client.VerifyTask(task.ID); err != nil {
		t.Fatalf("verify: %v", err)
	}
	got, err := client.RejectApproval(task.ID, "not this way")
	if err != nil {
		t.Fatalf("RejectApproval: %v", err)
	}
	if got.State != StateRejected {
		t.Errorf("state = %q, want rejected", got.State)
	}

	// The human resync moves the target to the checkout HEAD, over the wire.
	moved := commitFile(t, repo, "human.txt", "landed by hand")
	synced, err := client.SyncTarget("app")
	if err != nil {
		t.Fatalf("SyncTarget: %v", err)
	}
	if synced.SHA != moved {
		t.Errorf("synced target = %s, want %s", synced.SHA, moved)
	}
}

// TestDaemon_ConfirmedButInapplicableProposalIsConflictNot500: confirming a
// proposal whose operation is no longer applicable is a correctly-handled case —
// the proposal is recorded `failed` and nothing is mutated — so an operator
// confirming from the Web UI must not be shown a server fault for it.
func TestDaemon_ConfirmedButInapplicableProposalIsConflictNot500(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil, StubVerifyRunner{Pass: true})
	agent := svc.WithCaller(Agent())

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// The agent asks to retry a task that was never rejected: a valid ASK, an
	// inapplicable operation.
	if _, err := agent.RetryTask(task.ID, RetryRequest{}); err == nil {
		t.Fatal("precondition: retry should have been refused for an agent")
	}
	proposals, _ := store.ListProposals(ProposalPending)
	if len(proposals) != 1 {
		t.Fatalf("proposals = %d, want 1", len(proposals))
	}

	handler := NewServerForCaller(svc, Human()).Handler()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/proposals/"+proposals[0].ID+"/confirm", nil))

	if rec.Code == http.StatusInternalServerError {
		t.Errorf("confirming an inapplicable proposal = 500; a handled outcome must not read as a server fault\n%s", rec.Body.String())
	}
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 (state conflict)\n%s", rec.Code, rec.Body.String())
	}
	// The record is accurate: confirmed-then-failed, and the task untouched.
	final, _ := store.GetProposal(proposals[0].ID)
	if final.State != ProposalFailed {
		t.Errorf("proposal state = %q, want failed", final.State)
	}
	after, _ := store.GetTask(task.ID)
	if after.State != StatePlanned {
		t.Errorf("task state = %q, want planned — nothing should have been mutated", after.State)
	}
}

// TestDaemon_SteeringAndBoardRoundTrip walks M17's new routes over a real socket.
//
// The wire half matters here for a specific reason: the CLI branches on the
// DELIVERY STATE, so if that field did not survive the round trip an operator
// would be shown "recorded" where the plane said "undeliverable" — the exact lie
// the design exists to prevent.
func TestDaemon_SteeringAndBoardRoundTrip(t *testing.T) {
	repo := gitRepo(t)
	runner := &steerableRunner{answer: ErrSteeringDeferred}
	svc, _, store := newService(t, mapResolver{"app": repo}, runner, nil)
	client := serveUDS(t, svc)

	task, job := stageSteerableJob(t, svc, store, "app")

	steer, err := client.SteerJob(job.ID, "over the wire")
	if err != nil {
		t.Fatalf("client SteerJob: %v", err)
	}
	if steer.State != SteerPending || steer.Instruction != "over the wire" || steer.IssuedBy != CallerHuman {
		t.Fatalf("steering did not survive the wire: %+v", steer)
	}

	steers, err := client.JobSteering(job.ID)
	if err != nil {
		t.Fatalf("client JobSteering: %v", err)
	}
	if len(steers) != 1 || steers[0].ID != steer.ID {
		t.Fatalf("JobSteering over the wire = %+v", steers)
	}

	withdrawn, err := client.CancelSteering(steer.ID)
	if err != nil {
		t.Fatalf("client CancelSteering: %v", err)
	}
	if withdrawn.State != SteerCancelled {
		t.Errorf("state = %q, want cancelled", withdrawn.State)
	}

	// An empty instruction is malformed input, and the 400 survives as a sentinel.
	if _, err := client.SteerJob(job.ID, "  "); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("empty instruction over the wire: err = %v, want ErrInvalidRequest", err)
	}
	// An unknown Job is a 404, not a 500.
	if _, err := client.SteerJob("J-404", "hello"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown job over the wire: err = %v, want ErrNotFound", err)
	}

	board, err := client.ProgrammeBoard()
	if err != nil {
		t.Fatalf("client ProgrammeBoard: %v", err)
	}
	found := false
	for _, col := range board.Columns {
		for _, card := range col.Cards {
			if card.TaskID == task.ID {
				found = true
			}
		}
	}
	if !found {
		t.Error("the task is missing from the board over the wire")
	}
	if len(board.Columns) != len(boardColumns) {
		t.Errorf("%d columns over the wire, want %d", len(board.Columns), len(boardColumns))
	}
}
