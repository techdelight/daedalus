// Copyright (C) 2026 Techdelight BV

package control

import (
	"strings"
	"testing"
)

// fakeDigester returns a fixed digest and counts calls.
type fakeDigester struct {
	digest string
	calls  int
}

func (f *fakeDigester) Digest(project string) (string, error) {
	f.calls++
	return f.digest, nil
}

// --- null-agent floor ---------------------------------------------------------

func TestVerify_NullAgentFloor_Rejects(t *testing.T) {
	repo := gitRepo(t)
	rv := &recordingVerifier{pass: true} // would pass — must never be reached
	// WriteFile:false → the stub makes no change, so capture leaves head == base.
	runner := StubRunner{Result: ExecSuccess, WriteFile: false}
	svc, _, store := newService(t, mapResolver{"app": repo}, runner, nil, rv)

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "do nothing"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	// Precondition: the candidate's snapshot equals base (an empty change).
	jobs, _ := store.ListJobsForTask(task.ID)
	if jobs[0].OutputSnapshot != task.BaseSHA {
		t.Fatalf("precondition: snapshot %q should equal base %q", jobs[0].OutputSnapshot, task.BaseSHA)
	}

	res, err := svc.VerifyTask(task.ID)
	if err != nil {
		t.Fatalf("VerifyTask: %v", err)
	}
	if rv.called {
		t.Error("null-agent floor must reject BEFORE the verifier — it was called")
	}
	if res.Verified {
		t.Error("an empty change must not verify as done")
	}
	if !strings.Contains(res.Detail, "null-agent floor") {
		t.Errorf("detail %q should mention the null-agent floor", res.Detail)
	}
	got, _ := store.GetTask(task.ID)
	if got.State != StateRejected {
		t.Errorf("task state = %s, want rejected", got.State)
	}
}

// --- digest capture / record / plumb -----------------------------------------

func TestDigest_CapturedAtCreate_AndPlumbedToVerifier(t *testing.T) {
	repo := gitRepo(t)
	rv := &recordingVerifier{pass: true}
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, rv)
	fd := &fakeDigester{digest: "sha256:deadbeef"}
	svc.SetImageDigester(fd)

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// Recorded on the task at create.
	if task.ImageDigest != "sha256:deadbeef" {
		t.Errorf("task digest = %q, want sha256:deadbeef", task.ImageDigest)
	}
	stored, _ := store.GetTask(task.ID)
	if stored.ImageDigest != "sha256:deadbeef" {
		t.Errorf("stored digest = %q, want sha256:deadbeef", stored.ImageDigest)
	}

	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if _, err := svc.VerifyTask(task.ID); err != nil {
		t.Fatalf("VerifyTask: %v", err)
	}
	// The verifier ran against the pinned digest.
	if rv.spec.ImageDigest != "sha256:deadbeef" {
		t.Errorf("verifier spec digest = %q, want sha256:deadbeef", rv.spec.ImageDigest)
	}
}

func TestDigest_LazyCaptureAtVerify(t *testing.T) {
	repo := gitRepo(t)
	rv := &recordingVerifier{pass: true}
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, rv)

	// No digester at create → digest empty.
	task, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if task.ImageDigest != "" {
		t.Fatalf("precondition: digest should be empty at create, got %q", task.ImageDigest)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	// Install the digester only now; verify should capture it lazily.
	fd := &fakeDigester{digest: "sha256:lazy123"}
	svc.SetImageDigester(fd)

	if _, err := svc.VerifyTask(task.ID); err != nil {
		t.Fatalf("VerifyTask: %v", err)
	}
	if fd.calls == 0 {
		t.Error("digester should have been called lazily at verify")
	}
	if rv.spec.ImageDigest != "sha256:lazy123" {
		t.Errorf("verifier spec digest = %q, want sha256:lazy123", rv.spec.ImageDigest)
	}
	stored, _ := store.GetTask(task.ID)
	if stored.ImageDigest != "sha256:lazy123" {
		t.Errorf("digest not persisted at verify: %q", stored.ImageDigest)
	}
}

// --- verifier env policy (pure) ----------------------------------------------

func TestVerifierEnvPolicy_DockerRunArgs(t *testing.T) {
	args := DefaultVerifierEnvPolicy().DockerRunArgs("sha256:abc", "/host/checkout", "go test ./...")
	joined := strings.Join(args, " ")

	// Isolation: network off, ephemeral, clean checkout mounted at /workspace.
	if !containsPair(args, "--network", "none") {
		t.Errorf("args should set --network none: %v", args)
	}
	if !contains(args, "--rm") {
		t.Error("args should include --rm")
	}
	if !contains(args, "-v") || !contains(args, "/host/checkout:/workspace") {
		t.Errorf("args should mount the clean checkout at /workspace: %v", args)
	}
	if !containsPair(args, "-w", "/workspace") {
		t.Error("args should set workdir /workspace")
	}
	if !contains(args, "sha256:abc") {
		t.Error("args should run the pinned image digest")
	}
	if args[len(args)-1] != "go test ./..." {
		t.Errorf("last arg should be the shell command, got %q", args[len(args)-1])
	}
	// Hermetic: no credential mount, no /opt/tools, no env-file, no host home.
	for _, leak := range []string{".config", ".docker", "credentials", "/opt/tools", "--env-file", "/home/"} {
		if strings.Contains(joined, leak) {
			t.Errorf("verifier args must not leak %q: %v", leak, joined)
		}
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func containsPair(ss []string, a, b string) bool {
	for i := 0; i+1 < len(ss); i++ {
		if ss[i] == a && ss[i+1] == b {
			return true
		}
	}
	return false
}
