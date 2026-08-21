// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The baseline: a verdict is a statement about a CHANGE, so a check that was
// already failing on the tree the Job was handed cannot be part of it.
//
// These exercise the REAL CleanVerifier — the git worktree plumbing runs for
// real against a real repository, and only the container is faked. That matters
// here more than usual: the whole mechanism is "check out the base and run the
// same command against it", and a fake that answered by inspecting the command
// string would prove nothing about whether the right tree was ever checked out.
// So fakeDocker reads the MOUNTED DIRECTORY and decides from its contents,
// exactly as a real check would.

// fakeDocker stands in for the container runtime. It parses the argv
// CleanVerifier builds, reads the file the "check" names FROM THE MOUNTED TREE,
// and fails when that file says "broken". Every invocation is recorded.
type fakeDocker struct {
	runs []dockerRun
}

type dockerRun struct {
	dir   string // host directory mounted at /workspace
	check string // the shell command
}

func (f *fakeDocker) Output(name string, args ...string) (string, error) {
	if name != "docker" {
		return "", nil
	}
	var run dockerRun
	for i, a := range args {
		switch a {
		case "-v":
			run.dir = strings.TrimSuffix(args[i+1], ":/workspace")
		case "-c":
			run.check = args[i+1]
		}
	}
	f.runs = append(f.runs, run)

	// The "check" is the name of a file to read out of the mounted tree. A check
	// whose file is missing or says "broken" fails, like a real linter would.
	body, err := os.ReadFile(filepath.Join(run.dir, run.check))
	if err != nil {
		return "no such check subject: " + run.check, errExitOne{}
	}
	if strings.TrimSpace(string(body)) == "broken" {
		return run.check + ": broken", errExitOne{}
	}
	return run.check + ": ok", nil
}

type errExitOne struct{}

func (errExitOne) Error() string { return "exit status 1" }

// Unused by CleanVerifier, present to satisfy executor.Executor.
func (f *fakeDocker) Run(string, ...string) error                  { return nil }
func (f *fakeDocker) RunWithEnv([]string, string, ...string) error { return nil }
func (f *fakeDocker) RunWithEnvTee([]string, io.Writer, string, ...string) error {
	return nil
}
func (f *fakeDocker) Exec(string, ...string) error                  { return nil }
func (f *fakeDocker) ExecWithEnv([]string, string, ...string) error { return nil }
func (f *fakeDocker) LookPath(name string) (string, error)          { return name, nil }

// baseAndHead builds a repository with a base commit and a descendant, writing
// name→content at each. Returns (repoDir, baseSHA, headSHA).
func baseAndHead(t *testing.T, atBase, atHead map[string]string) (string, string, string) {
	t.Helper()
	repo := gitRepo(t)
	base := writeCommit(t, repo, "HEAD", atBase, "base")
	head := writeCommit(t, repo, base, atHead, "head")
	return repo, base, head
}

func writeCommit(t *testing.T, repoDir, parent string, files map[string]string, msg string) string {
	t.Helper()
	scratch := t.TempDir()
	if out, err := runGit(repoDir, "worktree", "add", "--detach", scratch, parent); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}
	defer func() {
		_, _ = runGit(repoDir, "worktree", "remove", "--force", scratch)
		_, _ = runGit(repoDir, "worktree", "prune")
	}()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(scratch, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := runGit(scratch, "add", "-A"); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := runGit(scratch, "-c", "user.email=t@t", "-c", "user.name=t",
		"commit", "-m", msg); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	sha, err := runGit(scratch, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return strings.TrimSpace(sha)
}

func verifierFor(exec *fakeDocker) CleanVerifier {
	return CleanVerifier{Exec: exec, Policy: DefaultVerifierEnvPolicy()}
}

func specFor(repo, base, head string, checks, taskChecks []string) VerifySpec {
	return VerifySpec{
		TaskID: "T-1", JobID: "J-1", Project: "app", RepoDir: repo,
		BaseSHA: base, HeadSHA: head, ImageDigest: "sha256:image",
		Policy:     AcceptancePolicy{Checks: checks},
		TaskChecks: taskChecks,
	}
}

// TestBaseline_APreExistingFailureIsNotChargedToTheChange.
//
// This is T-13 and T-16, reproduced: `daedalus docs lint` failed on a SPRINTS.md
// neither Job's diff touched, and both Tasks were rejected for it. The check is
// genuinely failing — the verdict on the CHANGE is still a pass, and the broken
// check is reported as what it is.
func TestBaseline_APreExistingFailureIsNotChargedToTheChange(t *testing.T) {
	repo, base, head := baseAndHead(t,
		map[string]string{"lint": "broken"},
		map[string]string{"lint": "broken", "feature": "ok"})

	exec := &fakeDocker{}
	out := verifierFor(exec).Verify(context.Background(),
		specFor(repo, base, head, []string{"lint", "feature"}, nil))

	if !out.Passed {
		t.Fatalf("a check that was ALREADY failing at the base rejected the change:\n%s", out.Detail)
	}
	if strings.Join(out.PreExisting, "|") != "lint" {
		t.Errorf("PreExisting = %v, want [lint]", out.PreExisting)
	}
	// Passing must not mean the breakage was swallowed — somebody still has to fix
	// it, and the record is where they find out.
	if !strings.Contains(out.Detail, "already broken") || !strings.Contains(out.Detail, "lint") {
		t.Errorf("detail does not report the pre-existing failure:\n%s", out.Detail)
	}
	// The base was checked out and the SAME check run against it — the evidence for
	// the verdict, not an inference from the command string.
	var ranAtBase int
	for _, r := range exec.runs {
		if strings.HasSuffix(r.dir, "/base") && r.check == "lint" {
			ranAtBase++
		}
	}
	if ranAtBase != 1 {
		t.Errorf("the failing check ran %d times against the base, want exactly 1: %+v", ranAtBase, exec.runs)
	}
}

// A check the change actually broke is still a rejection. The baseline narrows
// what counts as a verdict; it does not stop the gate being a gate.
func TestBaseline_ARegressionIsStillRejected(t *testing.T) {
	repo, base, head := baseAndHead(t,
		map[string]string{"lint": "ok"},
		map[string]string{"lint": "broken"})

	exec := &fakeDocker{}
	out := verifierFor(exec).Verify(context.Background(),
		specFor(repo, base, head, []string{"lint"}, nil))

	if out.Passed {
		t.Fatal("a check the change broke was excused by the baseline")
	}
	if !strings.Contains(out.Detail, "PASSES at the base") {
		t.Errorf("detail should say the base was clean, so the reader knows who broke it:\n%s", out.Detail)
	}
	if len(out.PreExisting) != 0 {
		t.Errorf("PreExisting = %v, want none", out.PreExisting)
	}
}

// TestBaseline_ATaskCheckIsNeverBaselined.
//
// The inverse failure, and the one that would matter more: a per-task check
// asserts what the change was supposed to MAKE true, so it fails at the base by
// construction. Baselining it would excuse a Job for not doing the thing it was
// sent to do — a verifier that passes empty work.
func TestBaseline_ATaskCheckIsNeverBaselined(t *testing.T) {
	repo, base, head := baseAndHead(t,
		map[string]string{"lint": "ok"},
		map[string]string{"lint": "ok", "unrelated": "ok"})

	exec := &fakeDocker{}
	// "pagination" exists at neither commit, so it fails at head AND would fail at
	// the base — exactly the shape a pre-existing project failure has.
	out := verifierFor(exec).Verify(context.Background(),
		specFor(repo, base, head, []string{"lint"}, []string{"pagination"}))

	if out.Passed {
		t.Fatal("a failing TASK check was excused because it also failed at the base — the Job was passed for not doing its job")
	}
	if !strings.Contains(out.Detail, "TASK's own acceptance checks") {
		t.Errorf("detail should say why a task check gets no baseline:\n%s", out.Detail)
	}
	for _, r := range exec.runs {
		if strings.HasSuffix(r.dir, "/base") {
			t.Errorf("a task check was run against the base: %+v", r)
		}
	}
}

// TestBaseline_NoCheckRunsAgainstTheBaseWhenEverythingPasses.
//
// The common case must cost what it did before. What this pins is the dominant
// cost — container runs, which are the minutes — and it fails against any
// implementation that grades the base unconditionally.
//
// It does NOT observe the `git worktree add`, which is seconds and has no seam to
// watch it through. That the checkout is lazy as well is asserted by
// construction in newBaseline rather than here; if that laziness ever became
// load-bearing rather than a saving, it would need a seam of its own.
func TestBaseline_NoCheckRunsAgainstTheBaseWhenEverythingPasses(t *testing.T) {
	repo, base, head := baseAndHead(t,
		map[string]string{"lint": "ok"},
		map[string]string{"lint": "ok", "feature": "ok"})

	exec := &fakeDocker{}
	out := verifierFor(exec).Verify(context.Background(),
		specFor(repo, base, head, []string{"lint", "feature"}, nil))

	if !out.Passed {
		t.Fatalf("expected a pass: %s", out.Detail)
	}
	for _, r := range exec.runs {
		if strings.HasSuffix(r.dir, "/base") {
			t.Errorf("a check was graded against the base although nothing failed: %+v", r)
		}
	}
	if len(exec.runs) != 2 {
		t.Errorf("ran %d checks, want exactly the 2 at head: %+v", len(exec.runs), exec.runs)
	}
}

// TestBaseline_IsBuiltOnceForSeveralFailures: several failing checks share one
// base checkout. A second `git worktree add` of the same commit would be pure
// cost on the path that is already the slow one.
func TestBaseline_IsBuiltOnceForSeveralFailures(t *testing.T) {
	// head must differ from base or git has nothing to commit — and an artifact
	// identical to its base is the null-agent floor's business anyway.
	repo, base, head := baseAndHead(t,
		map[string]string{"a": "broken", "b": "broken"},
		map[string]string{"a": "broken", "b": "broken", "work": "ok"})

	exec := &fakeDocker{}
	out := verifierFor(exec).Verify(context.Background(),
		specFor(repo, base, head, []string{"a", "b"}, nil))

	if !out.Passed || len(out.PreExisting) != 2 {
		t.Fatalf("want a pass with both checks reported pre-existing, got passed=%v pre=%v\n%s",
			out.Passed, out.PreExisting, out.Detail)
	}
	// 2 at head + 2 at base = 4; a per-failure checkout would still be 4 runs, so
	// count the checkouts instead by looking at what git left behind.
	list, err := runGit(repo, "worktree", "list")
	if err != nil {
		t.Fatalf("worktree list: %v", err)
	}
	if strings.Contains(list, "/base") {
		t.Errorf("the base worktree outlived the verify:\n%s", list)
	}
}

// TestBaseline_WhenItCannotBeEstablishedTheFailureStands.
//
// No baseline means no evidence either way, and the gate is not dropped on a
// maybe. But "this change broke it" and "we could not tell" must not read
// identically to whoever picks it up — the whole class of bug this fixes came
// from a verdict that sounded like a judgement of the work and was not.
func TestBaseline_WhenItCannotBeEstablishedTheFailureStands(t *testing.T) {
	repo, _, head := baseAndHead(t,
		map[string]string{"lint": "ok"},
		map[string]string{"lint": "broken"})

	exec := &fakeDocker{}
	// A base sha that is not in the repository: the worktree add fails.
	out := verifierFor(exec).Verify(context.Background(),
		specFor(repo, "0000000000000000000000000000000000000000", head, []string{"lint"}, nil))

	if out.Passed {
		t.Fatal("an unverifiable baseline waved a failing check through")
	}
	if !strings.Contains(out.Detail, "comparison could not be made") {
		t.Errorf("detail must say the baseline was unavailable, not imply a judgement:\n%s", out.Detail)
	}
}
