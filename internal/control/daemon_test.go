// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"errors"
	"net"
	"net/http"
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

	// Second active task → conflict.
	if _, err := client.CreateTask(CreateTaskRequest{Project: "app", Objective: "first"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := client.CreateTask(CreateTaskRequest{Project: "app", Objective: "second"}); err == nil {
		t.Error("second active create over wire = nil, want conflict error")
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
