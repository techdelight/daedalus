// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"os"
	"path/filepath"

	"github.com/techdelight/daedalus/internal/executor"
)

// JobSpec is the immutable description handed to an AgentRunner for one headless
// attempt. The worktree is already materialised (clean checkout at BaseSHA on
// the job branch); the runner's sole job is to run the agent against it and
// report how the process ended.
type JobSpec struct {
	TaskID      string
	JobID       string
	Project     string
	Objective   string // the prompt / what to do
	Runner      string // "claude", "copilot", …
	Budget      int    // wall-clock seconds; 0 = unbounded
	BaseSHA     string
	WorktreeDir string // the isolated checkout, mounted as /workspace in the real path
}

// RunOutcome is how a headless Job ended — the "how it ended" axis (§5), distinct
// from the committed tree the wrapper captures separately. Only ExecSuccess
// promotes the snapshot to a candidate Artifact.
type RunOutcome struct {
	Result ExecutionResult
	// Detail is a short human-readable note (exit reason) recorded on the event.
	Detail string
}

// AgentRunner runs a headless Job to completion (process exit is the boundary)
// and classifies the outcome. It is the single Docker-dependent seam: the
// control-plane logic (worktree lifecycle, capture, promotion, reconcile) is
// host-tested with a fake; only the real adapter (CoordinatorRunner) needs a
// container runtime.
type AgentRunner interface {
	Run(ctx context.Context, spec JobSpec) RunOutcome
}

// StubRunner is a Docker-free AgentRunner for tests and the fake-runner smoke.
// It optionally writes a marker file into the worktree (so the wrapper's
// auto-commit produces a real new HEAD, exercising output_snapshot capture) and
// returns a fixed result. It is deliberately exported so the daemon can select
// it via DAEDALUS_CONTROL_FAKE_RUNNER for an end-to-end, no-Docker smoke.
type StubRunner struct {
	Result     ExecutionResult // defaults to ExecSuccess when zero-value ""
	WriteFile  bool            // write a marker file to simulate agent work
	MarkerName string          // defaults to "AGENT_RAN.txt"
	Detail     string
}

// Run implements AgentRunner.
func (r StubRunner) Run(_ context.Context, spec JobSpec) RunOutcome {
	if r.WriteFile {
		name := r.MarkerName
		if name == "" {
			name = "AGENT_RAN.txt"
		}
		// Best-effort: a write failure just means no new commit, still a valid
		// outcome (snapshot == base_sha).
		_ = os.WriteFile(filepath.Join(spec.WorktreeDir, name),
			[]byte("stub agent ran for "+spec.JobID+"\n"), 0o644)
	}
	res := r.Result
	if res == "" {
		res = ExecSuccess
	}
	return RunOutcome{Result: res, Detail: r.Detail}
}

// CoordinatorRunner is the REAL, HOST-ONLY adapter. It runs the project agent
// headless against the Job's isolated worktree via the standard daedalus launch
// path (`daedalus <name> <worktree> -p <objective>` semantics — the worktree
// becomes /workspace inside the container), taking process exit as the Job
// boundary. It requires a Docker daemon and is therefore NOT unit-tested in this
// environment; it is exercised only on a real host.
type CoordinatorRunner struct {
	Exec    executor.Executor // real command execution
	BinPath string            // path to the `daedalus` binary
}

// Run implements AgentRunner by invoking the daedalus CLI headless. Exit status
// classifies the outcome: nil error → success; any error → failed. Timeout and
// cancellation are surfaced by the caller's context in a future refinement;
// here process exit is authoritative.
func (r CoordinatorRunner) Run(ctx context.Context, spec JobSpec) RunOutcome {
	// A throwaway project name keyed to the job so concurrent jobs never collide.
	name := "daedalus-job-" + spec.JobID
	// `daedalus <name> <dir> -p <objective>` registers the worktree and runs a
	// headless single-prompt task, exiting when the agent finishes.
	err := r.Exec.Run(r.BinPath, name, spec.WorktreeDir, "-p", spec.Objective)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return RunOutcome{Result: ExecTimeout, Detail: "context deadline exceeded"}
		}
		if ctx.Err() == context.Canceled {
			return RunOutcome{Result: ExecCancelled, Detail: "context cancelled"}
		}
		return RunOutcome{Result: ExecFailed, Detail: err.Error()}
	}
	return RunOutcome{Result: ExecSuccess}
}
