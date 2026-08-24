// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

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
	// LogPath is where this Job's own output is to be recorded, or "" for
	// nowhere. Chosen by the caller rather than the runner so that every runner
	// writes to the one place the plane will later point a reader at; a runner
	// that cannot honour it simply leaves no file, which is how the caller tells
	// that there is nothing to point at. See JobLogPath.
	LogPath string
	// Deliverables are what must EXIST when this Job is done, one line each. The
	// agent is given them as a checklist under the objective: the objective says
	// what to do, and these say what "done" looks like from the outside.
	//
	// Empty for a Task that named none, and the prompt then says nothing about
	// them — an empty "Deliverables:" heading would read as a list the agent had
	// somehow failed to receive.
	Deliverables []string
	// Continuation is what a REFINED attempt is answering (#91): the worktree
	// already holds earlier work, plus the findings a human chose to forward and
	// their own instruction. Empty for every ordinary Job, which starts from a
	// clean tree and is told the objective alone.
	Continuation string
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
	// Honour LogPath too, so the no-Docker smoke exercises the SAME chain the real
	// adapter does — path chosen by the service, file written by the runner, path
	// recorded on the Job row because the file is there. A stub that skipped this
	// would leave the wiring untested on the only path this environment can run.
	if spec.LogPath != "" {
		if f, err := openJobLog(spec.LogPath); err == nil {
			_, _ = io.WriteString(f, "stub runner for "+spec.JobID+": "+string(res)+"\n")
			f.Close()
		}
	}
	return RunOutcome{Result: res, Detail: r.Detail}
}

// JobProjectName is the throwaway registry project name a Job's headless run is
// launched under. Deterministic (keyed to the job id) so concurrent jobs never
// collide and the deregistration cannot target the wrong entry.
func JobProjectName(jobID string) string { return "daedalus-job-" + jobID }

// JobLogPath is where a Job's own output is recorded: one file per Job under the
// data dir. Empty dataDir yields "", meaning "nowhere" — the same degradation
// dataDirEnv and seedJobHomeOrWarn make when there is no data dir to work from.
//
// A file PER JOB is the whole point. The agent's output already reached the
// daemon's stdout and from there the single shared control.log, where it was
// interleaved with every concurrent Job's and with the plane's own logging,
// keyed by nothing — present, and unreadable. Keying by job id is what turns it
// back into an account of one attempt (Backlog #77).
func JobLogPath(dataDir, jobID string) string {
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, ".daedalus", "jobs", jobID+".log")
}

// openJobLog creates the per-job log and returns it ready to be written.
//
// O_APPEND, not O_TRUNC: a job id is never reused, so in practice this always
// creates, but appending means the one case that could surprise (a re-run
// against an existing id) adds to the record instead of destroying it.
//
// 0o700/0o600 rather than the 0o755/0o644 the daemon's own logs use, because
// this file holds RAW AGENT OUTPUT — which is exactly where a leaked token or
// credential would show up. The directory is new, so tightening it costs no
// compatibility.
func openJobLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

// jobEnvironmentNote is what a Job is told about the room it is working in.
//
// It exists because the alternative to saying this is letting the agent find it
// out by failing — and the third point was added after watching exactly that.
// A repository-split Task spent its whole attempt discovering there were no
// credentials, then delivered a patch file and a handoff document: a plan for a
// human, which is not a smaller version of the work but a different thing
// entirely, and which looks like a result until somebody reads it. Stating the
// limit up front costs one paragraph and converts that into a refusal in the
// first minute.
//
// It is a STATEMENT, not a setting. Giving a Job container credentials was built
// and then removed: a container with the network, an untrusted objective and a
// push-capable key is the wrong trade, and gating it behind a per-project policy
// file solved that by making the operator maintain configuration — which is the
// opposite of what this tool is for. A Job cannot reach a remote. That is the
// design, so the design says it. Git in a Job container is READ-ONLY (core/gitworktree.go): the
// repository is mounted so history and diffs can be read, and writes are refused
// by the kernel. An agent that reaches for `git commit` gets a permission error
// out of nowhere, and the observed response to an unexplained broken git is to
// conclude the task cannot be done and stop — which is precisely the failure that
// made the mount necessary in the first place. Fixing the mount without saying
// what it is would trade a fatal git for a puzzling one.
//
// It also answers the question the agent would otherwise have to guess at: if it
// cannot commit, how does its work become an artifact. The plane captures the
// tree on the host when the Job ends, which is the existing design and the reason
// the agent never needed to commit.
const jobEnvironmentNote = `You are running as a Daedalus control-plane Job, headless, in an isolated Git
worktree mounted at /workspace. Two things about this environment:

1. Git is READ-ONLY here. git log, diff, status, show and blame all work and are
   how you check what you have changed. Anything that writes — add, commit,
   stash, checkout, push — will be refused by the filesystem. That is deliberate,
   not a fault to work around.
2. You do not need to commit. When this Job ends the control plane commits your
   working tree itself, on the host, and that commit is the artifact it grades.
   Leave the files as you want them and stop.
3. You cannot reach a git remote. There are no credentials here: you cannot
   push, clone a private repository, or create one. This is a property of the
   system, not a fault to route around.

If the objective needs something in point 3, STOP AND SAY SO IMMEDIATELY, in one
paragraph, naming what it is you cannot do. Do not produce a patch file, a
handoff document or a plan for a human to carry out instead — that is not a
smaller version of the task, it is a different deliverable, and it wastes the
attempt while looking like a result.

Your objective follows.

`

// jobPrompt is the objective as the agent receives it: the environment note,
// then the objective verbatim.
//
// The note is prepended at the point of LAUNCH rather than stored on the Task,
// so the record keeps what a human asked for and the agent is told what is true
// about the container it woke up in. The two are different things and only one of
// them belongs in the event log.
func jobPrompt(spec JobSpec) string {
	// The continuation goes AFTER the objective, deliberately. The objective is
	// what the work is for and has not changed; the findings are corrections to an
	// attempt at it. Leading with them would read as the new brief and invite an
	// agent to fix four things and forget what it was building.
	return jobEnvironmentNote + spec.Objective + deliverablesNote(spec.Deliverables) + spec.Continuation
}

// deliverablesNote renders the Task's deliverables as the checklist the agent is
// working to, or "" when it named none.
//
// Said as "what must exist when you are done" rather than as more objective,
// because that is the question it answers and the one an agent otherwise has to
// infer from prose. It is not presented as a limit: an agent that reads a list
// as the full extent of the work will stop at four bullet points in the middle
// of a feature, which is a worse failure than the vagueness this replaces.
func deliverablesNote(deliverables []string) string {
	clean := cleanLines(deliverables)
	if len(clean) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nWHAT MUST EXIST WHEN YOU ARE DONE\n")
	b.WriteString("Each of these is something a person will look for afterwards:\n")
	for _, d := range clean {
		b.WriteString("  - " + d + "\n")
	}
	b.WriteString("\nThis list is the floor, not the ceiling: it is what the work will be checked " +
		"against, not the full extent of what the objective asks for. If you cannot deliver one of " +
		"them, say which and why rather than delivering something else in its place.\n")
	return b.String()
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
	// This Job's own log, opened BEFORE the run and closed after it. Opening it
	// eagerly is what makes the file's existence mean "the tee was wired": the
	// caller records the path only when the file is there, so an open failure
	// leaves nothing to point a reader at rather than a path that resolves to
	// nothing. A failure here is degradation, never a reason not to run the Job.
	var tee io.Writer
	if spec.LogPath != "" {
		f, err := openJobLog(spec.LogPath)
		if err != nil {
			log.Printf("control: opening job log %s: %v (job %s runs without one)", spec.LogPath, err, spec.JobID)
		} else {
			defer f.Close()
			tee = f
		}
	}
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
	err := r.Exec.RunWithEnvTee(r.dataDirEnv(), tee, r.BinPath, name, spec.WorktreeDir,
		"-p", jobPrompt(spec))
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
