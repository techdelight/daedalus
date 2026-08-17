// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"log"
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
// returns a fixed result. The marker name defaults to a JOB-SCOPED one so
// concurrent Jobs do not collide by accident; set MarkerName to force a shared
// path (a deliberate merge conflict) or a name that trips the integrity gate. It is deliberately exported so the daemon can select
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
			// JOB-SCOPED by default. A fixed filename made every concurrent Job write
			// the same path, so any two artifacts landing on one queue collided — the
			// integration CONFLICT path was reached by accident, and the clean-rebase
			// path was the one going unexercised. Distinct markers make each Job's
			// diff genuinely independent, so a test has to opt IN to a conflict.
			name = spec.JobID + "-AGENT_RAN.txt"
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

// JobProjectName is the throwaway registry project name a Job's headless run is
// launched under. Deterministic (keyed to the job id) so concurrent jobs never
// collide and the deregistration cannot target the wrong entry.
func JobProjectName(jobID string) string { return "daedalus-job-" + jobID }

// CoordinatorRunner is the REAL, HOST-ONLY adapter. It runs the project agent
// headless against the Job's isolated worktree via the standard daedalus launch
// path (`daedalus <name> <worktree> -p <objective>` semantics — the worktree
// becomes /workspace inside the container), taking process exit as the Job
// boundary. It requires a Docker daemon and is therefore NOT unit-tested in this
// environment; it is exercised only on a real host.
type CoordinatorRunner struct {
	Exec    executor.Executor // real command execution
	BinPath string            // path to the `daedalus` binary
	DataDir string            // where project homes live; needed to seed the Job's
}

// dataDirEnv is the environment that pins a spawned daedalus CLI to the daemon's
// data dir, so the home the plane seeds is the home the container mounts and the
// registry the cleanup edits is the registry the launch wrote to.
//
// DAEDALUS_DATA_DIR is the right lever because it is the CLI's HIGHEST-precedence
// source: it is read before config.json is loaded and ApplyAppConfig only fills a
// still-empty value (core/appconfig.go:24), so a project-local config.json cannot
// silently win. Empty DataDir yields nil — an adapter built without one has
// nothing to pin to, matching seedJobHomeOrWarn's "nothing to copy from".
func (r CoordinatorRunner) dataDirEnv() []string {
	if r.DataDir == "" {
		return nil
	}
	return []string{"DAEDALUS_DATA_DIR=" + r.DataDir}
}

// Run implements AgentRunner by invoking the daedalus CLI headless. Exit status
// classifies the outcome: nil error → success; any error → failed. Timeout and
// cancellation are surfaced by the caller's context in a future refinement;
// here process exit is authoritative.
func (r CoordinatorRunner) Run(ctx context.Context, spec JobSpec) RunOutcome {
	// A throwaway project name keyed to the job so concurrent jobs never collide.
	name := JobProjectName(spec.JobID)
	// The registration is a side-effect of launching, and the worktree it points
	// at is reclaimed when the Job ends — so without this the registry accumulates
	// one dead `daedalus-job-*` entry per Job forever. Deregistering here keeps the
	// side-effect as short-lived as the thing it describes. Best-effort and
	// deferred so it runs on every exit path; `--force` because there is no human
	// to confirm.
	defer func() {
		if err := r.Exec.RunWithEnv(r.dataDirEnv(), r.BinPath, "remove", name, "--force"); err != nil {
			log.Printf("control: deregistering throwaway project %s: %v", name, err)
		}
	}()
	// The throwaway project gets a throwaway HOME, so the agent inside it has no
	// login — copy the owning project's credentials in first. Without this every
	// Job exits 1 within seconds on "Not logged in", which is precisely what
	// happened on the first real host to run one. See jobhome.go.
	seedJobHomeOrWarn(r.DataDir, spec.Project, name)
	// `daedalus <name> <dir> -p <objective>` registers the worktree and runs a
	// headless single-prompt task, exiting when the agent finishes.
	//
	// The data dir is PINNED to the daemon's, and that is load-bearing rather than
	// tidiness. Seeding writes to <r.DataDir>/<name>/, but the spawned CLI resolves
	// its own data dir independently — DAEDALUS_DATA_DIR, then config.json, then
	// <its own scriptDir>/.cache (internal/config/config.go:196-207) — and it has no
	// --data-dir flag, while the daemon does and is normally spawned with one. Any
	// divergence means the CLI mounts a DIFFERENT directory as /home/claude, the
	// seeded credentials sit in a home nobody mounts, and the Job dies on
	// `Not logged in` with the seeding step having reported success. Pinning removes
	// the divergence rather than detecting it.
	err := r.Exec.RunWithEnv(r.dataDirEnv(), r.BinPath, name, spec.WorktreeDir, "-p", spec.Objective)
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
