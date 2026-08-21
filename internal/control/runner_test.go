// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/techdelight/daedalus/internal/executor"
)

// TestCoordinatorRunner_DeregistersThrowawayProject is the regression for the
// registry leak: the real runner registers a throwaway `daedalus-job-<id>`
// project to launch the agent, and before this fix never removed it — one dead
// entry per Job, forever, pointing at a worktree that gets reclaimed.
//
// The container run itself is host-only, but the *registration lifecycle* is
// pure command execution, so it is asserted here through the injected Executor.
func TestCoordinatorRunner_DeregistersThrowawayProject(t *testing.T) {
	tests := []struct {
		name       string
		runErr     error
		wantResult ExecutionResult
	}{
		{"after a successful run", nil, ExecSuccess},
		{"after a failed run", errors.New("agent exited 1"), ExecFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exec := executor.NewMockExecutor()
			if tc.runErr != nil {
				exec.Results["/bin/daedalus"] = executor.MockResult{Err: tc.runErr}
			}
			r := CoordinatorRunner{Exec: exec, BinPath: "/bin/daedalus"}
			out := r.Run(context.Background(), JobSpec{
				JobID: "J-7", Project: "app", Objective: "do work", WorktreeDir: "/wt",
			})
			if out.Result != tc.wantResult {
				t.Errorf("result = %q, want %q", out.Result, tc.wantResult)
			}
			name := JobProjectName("J-7")
			var launched, removed bool
			for _, c := range exec.Calls {
				if len(c.Args) >= 2 && c.Args[0] == name && c.Args[1] == "/wt" {
					launched = true
				}
				if len(c.Args) >= 3 && c.Args[0] == "remove" && c.Args[1] == name && c.Args[2] == "--force" {
					removed = true
				}
			}
			if !launched {
				t.Errorf("expected a launch under %s, got calls %+v", name, exec.Calls)
			}
			if !removed {
				t.Errorf("throwaway project %s was never deregistered — the registry leaks one entry per Job. Calls: %+v", name, exec.Calls)
			}
		})
	}
}

// TestCoordinatorRunner_PinsTheSpawnedCLIToTheDaemonsDataDir is the regression
// for a Job inheriting no login even though seeding "succeeded".
//
// SeedJobHome writes the owning project's credentials to <DataDir>/<jobProject>/,
// but the spawned CLI resolves its own data dir (DAEDALUS_DATA_DIR → config.json →
// <its scriptDir>/.cache) and has no --data-dir flag, while the daemon has one and
// is normally spawned with it. Diverge them and the CLI mounts a different
// /home/claude, so the Job dies on `Not logged in` while every log line above it
// says the home was seeded — the failure and its cause end up in different
// directories. Both spawns must carry the pin: the launch so the seeded home is
// the mounted one, and the deregistration so it edits the registry the launch
// actually wrote to.
func TestCoordinatorRunner_PinsTheSpawnedCLIToTheDaemonsDataDir(t *testing.T) {
	exec := executor.NewMockExecutor()
	r := CoordinatorRunner{Exec: exec, BinPath: "/bin/daedalus", DataDir: "/var/lib/daedalus"}
	r.Run(context.Background(), JobSpec{
		JobID: "J-6", Project: "app", Objective: "do work", WorktreeDir: "/wt",
	})

	const want = "DAEDALUS_DATA_DIR=/var/lib/daedalus"
	if len(exec.Calls) == 0 {
		t.Fatal("no commands were run at all")
	}
	for _, c := range exec.Calls {
		if !slices.Contains(c.Env, want) {
			t.Errorf("spawn %v carries env %v, want it to include %q — an unpinned spawn "+
				"resolves its own data dir and may mount a home the plane never seeded",
				c.Args, c.Env, want)
		}
	}
}

// An adapter built without a DataDir has nothing to pin to. It must still launch
// rather than exporting an empty DAEDALUS_DATA_DIR, which the CLI would take as
// an explicit "" and, being highest-precedence, would beat config.json — turning
// a missing setting into a wrong one.
func TestCoordinatorRunner_NoDataDirPinsNothing(t *testing.T) {
	exec := executor.NewMockExecutor()
	r := CoordinatorRunner{Exec: exec, BinPath: "/bin/daedalus"}
	r.Run(context.Background(), JobSpec{JobID: "J-8", WorktreeDir: "/wt"})

	for _, c := range exec.Calls {
		for _, e := range c.Env {
			if strings.HasPrefix(e, "DAEDALUS_DATA_DIR=") {
				t.Errorf("spawn %v exported %q with no DataDir configured", c.Args, e)
			}
		}
	}
}

// TestJobProjectName is deterministic and job-scoped, so concurrent jobs never
// collide and a deregistration can never target another job's entry.
func TestJobProjectName(t *testing.T) {
	if a, b := JobProjectName("J-1"), JobProjectName("J-2"); a == b {
		t.Errorf("names collide: %s == %s", a, b)
	}
	if got := JobProjectName("J-1"); got != "daedalus-job-J-1" {
		t.Errorf("JobProjectName = %q, want daedalus-job-J-1", got)
	}
}

// --- per-job logs (#77) -------------------------------------------------------

// TestCoordinatorRunner_TeesAgentOutputToAPerJobLog is the regression for the
// gap that made a failed Job undiagnosable: the agent's output went to the
// daemon's stdout and from there to the single shared control.log, interleaved
// with every other Job's and keyed by nothing.
//
// The container run is host-only, but the TEE is pure command execution, so it
// is asserted here through the injected Executor — whose RunWithEnvTee writes
// the canned output to whatever writer it was handed.
func TestCoordinatorRunner_TeesAgentOutputToAPerJobLog(t *testing.T) {
	dataDir := t.TempDir()
	exec := executor.NewMockExecutor()
	exec.Results["/bin/daedalus"] = executor.MockResult{
		Output: "Not logged in · authentication_failed\n",
		Err:    errors.New("exit status 1"),
	}

	logPath := JobLogPath(dataDir, "J-7")
	r := CoordinatorRunner{Exec: exec, BinPath: "/bin/daedalus", DataDir: dataDir}
	out := r.Run(context.Background(), JobSpec{
		JobID: "J-7", Project: "app", Objective: "do work", WorktreeDir: "/wt",
		LogPath: logPath,
	})

	if out.Result != ExecFailed {
		t.Fatalf("result = %q, want failed", out.Result)
	}
	// The outcome the DB keeps is still just the exit status — that is precisely
	// why the log has to carry the rest.
	if out.Detail != "exit status 1" {
		t.Errorf("detail = %q, want %q", out.Detail, "exit status 1")
	}

	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading per-job log: %v", err)
	}
	if !strings.Contains(string(body), "authentication_failed") {
		t.Errorf("per-job log = %q, want the agent's own account of the failure", body)
	}
}

// TestJobLogPath_IsKeyedByJob pins the two properties the whole fix rests on:
// one file per Job (so concurrent Jobs cannot interleave), and no data dir means
// no path — the same degradation dataDirEnv makes.
func TestJobLogPath_IsKeyedByJob(t *testing.T) {
	a := JobLogPath("/data", "J-7")
	b := JobLogPath("/data", "J-8")
	if a == b {
		t.Fatalf("J-7 and J-8 share a log path (%q) — concurrent Jobs would interleave", a)
	}
	if want := filepath.Join("/data", ".daedalus", "jobs", "J-7.log"); a != want {
		t.Errorf("JobLogPath = %q, want %q", a, want)
	}
	if got := JobLogPath("", "J-7"); got != "" {
		t.Errorf("JobLogPath with no data dir = %q, want \"\"", got)
	}
}

// TestCoordinatorRunner_NoLogPathWritesNothing: an adapter with nowhere to write
// still runs the Job. The log is a diagnostic, never a precondition.
func TestCoordinatorRunner_NoLogPathWritesNothing(t *testing.T) {
	dataDir := t.TempDir()
	exec := executor.NewMockExecutor()
	r := CoordinatorRunner{Exec: exec, BinPath: "/bin/daedalus"}

	out := r.Run(context.Background(), JobSpec{
		JobID: "J-7", Project: "app", Objective: "do work", WorktreeDir: "/wt",
	})
	if out.Result != ExecSuccess {
		t.Errorf("result = %q, want success", out.Result)
	}
	if _, err := os.Stat(filepath.Join(dataDir, ".daedalus", "jobs")); !os.IsNotExist(err) {
		t.Errorf("a jobs dir was created with no LogPath asked for (err=%v)", err)
	}
}

// TestOpenJobLog_IsNotWorldReadable: the file holds raw agent output, which is
// exactly where a leaked token would appear.
func TestOpenJobLog_IsNotWorldReadable(t *testing.T) {
	path := JobLogPath(t.TempDir(), "J-7")
	f, err := openJobLog(path)
	if err != nil {
		t.Fatalf("openJobLog: %v", err)
	}
	f.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("job log mode = %#o, want 0600 — agent output must not be world-readable", perm)
	}
}

// TestCoordinatorRunner_TellsTheJobItsGitIsReadOnly.
//
// Git inside a Job container is read-only by construction (core/gitworktree.go),
// and the reason to SAY so is the failure that made the mount necessary: an agent
// that meets an unexplained broken git concludes the task cannot be done and
// stops, having written nothing. Fixing the mount without explaining it would
// trade a fatal git for a puzzling one — the same shape of failure, one step
// later.
//
// The objective must survive verbatim: the note is context, and an agent that
// received a paraphrase of what it was asked to do would be graded against the
// original.
func TestCoordinatorRunner_TellsTheJobItsGitIsReadOnly(t *testing.T) {
	exec := executor.NewMockExecutor()
	r := CoordinatorRunner{Exec: exec, BinPath: "/bin/daedalus"}
	const objective = "Add cursor pagination to GET /items"
	r.Run(context.Background(), JobSpec{
		JobID: "J-12", Project: "app", Objective: objective, WorktreeDir: "/wt",
	})

	var prompt string
	for _, c := range exec.Calls {
		for i, a := range c.Args {
			if a == "-p" && i+1 < len(c.Args) {
				prompt = c.Args[i+1]
			}
		}
	}
	if prompt == "" {
		t.Fatal("the launch carried no -p prompt at all")
	}
	if !strings.HasSuffix(prompt, objective) {
		t.Errorf("the objective did not survive verbatim at the end of the prompt:\n%q", prompt)
	}
	if prompt == objective {
		t.Fatal("the prompt is the bare objective: a Job is never told that git here is read-only, " +
			"so an agent meeting a permission error has no way to know it is deliberate")
	}
	// The third fact is the one a Task learned the expensive way: a repo-split Job
	// spent its whole attempt discovering it had no credentials and then delivered
	// a plan, which looks like a result until somebody reads it.
	for _, want := range []string{
		"READ-ONLY", "do not need to commit", "/workspace",
		"cannot reach a git remote", "STOP AND SAY SO", "handoff document",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the environment note does not mention %q:\n%s", want, prompt)
		}
	}
}
