// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
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
