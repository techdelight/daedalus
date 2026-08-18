// Copyright (C) 2026 Techdelight BV

package control

import (
	"os"
	"path/filepath"
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

	res, err := svc.VerifyTask(task.ID, VerifyRequest{})
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
	if _, err := svc.VerifyTask(task.ID, VerifyRequest{}); err != nil {
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

	if _, err := svc.VerifyTask(task.ID, VerifyRequest{}); err != nil {
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

// TestVerifierBypassesTheImageEntrypoint pins the override itself. The project
// image's ENTRYPOINT is a launcher, not a shell: handing it `sh -c <check>` runs
// the AGENT with those words as argv and never runs the check. A verifier that
// grades a container which never executed the check reports a verdict about
// nothing, so the shape of the argv is a correctness property.
func TestVerifierBypassesTheImageEntrypoint(t *testing.T) {
	args := DefaultVerifierEnvPolicy().DockerRunArgs("sha256:abc", "/host/checkout", "go test ./...")

	if !containsPair(args, "--entrypoint", "sh") {
		t.Errorf("verifier must override the image entrypoint with sh: %v", args)
	}

	// The image must be followed by exactly `-c <check>`. A stray "sh" positional
	// here (the pre-override argv) would make the entrypoint run a script NAMED
	// "sh" instead of the check.
	i := indexOf(args, "sha256:abc")
	if i < 0 {
		t.Fatalf("pinned image digest missing from args: %v", args)
	}
	rest := args[i+1:]
	want := []string{"-c", "go test ./..."}
	if len(rest) != len(want) || rest[0] != want[0] || rest[1] != want[1] {
		t.Errorf("after the image, args should be exactly %v, got %v", want, rest)
	}
}

// TestVerifierOverrideIsRequiredByTheImageEntrypoint ties the override to the
// reason for it. entrypoint.sh ends by exec'ing the agent with "$@" rather than
// exec'ing "$@" itself, so a check passed through it is swallowed. If the
// entrypoint ever grows a real `exec "$@"` passthrough this test stops demanding
// the override — the requirement is derived, not hardcoded.
func TestVerifierOverrideIsRequiredByTheImageEntrypoint(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "entrypoint.sh"))
	if err != nil {
		t.Skipf("entrypoint.sh not readable from here: %v", err)
	}
	script := string(data)

	passesThrough := strings.Contains(script, `exec "$@"`)
	execsAgentWithArgs := strings.Contains(script, `--dangerously-skip-permissions "$@"`)

	if passesThrough {
		return // a check handed to the entrypoint would run; the override is optional
	}
	if !execsAgentWithArgs {
		t.Skip("entrypoint.sh no longer matches either known shape; re-derive this test")
	}

	args := DefaultVerifierEnvPolicy().DockerRunArgs("sha256:abc", "/host/checkout", "true")
	if !containsPair(args, "--entrypoint", "sh") {
		t.Error("entrypoint.sh execs the agent with \"$@\" and has no `exec \"$@\"` " +
			"passthrough, so the verifier MUST override the entrypoint — otherwise " +
			"every check command is passed to the agent as argv and never runs")
	}
}

func indexOf(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return -1
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
