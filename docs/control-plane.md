# The Daedalus Control Plane

*The host-side authority over what agents do, whether they may, and whether the
result is acceptable. This document tracks what is **built** versus what is
**planned**; the full design is in [`guild-master-plan.md`](guild-master-plan.md)
(§5 is the model this implements).*

> **The Guild Master has initiative. The control plane has authority.**

## What this is

Daedalus is growing a **host-side control plane**: a Daedalus-owned store of
**authoritative** Task / Job / Artifact state, plus the machinery to dispatch,
verify, and integrate work. Authoritative state lives host-side (never in an
agent's workspace) so a controlling agent can never edit the state that decides
whether its own work is valid.

## Data model (Sprint 54 / M13 foundation — built)

Implemented in package [`internal/control`](../internal/control):

- **Task** — the unit of intent: `project`, `objective`, `acceptance_ref`,
  `base_sha`, `state`, timestamps. One **active** (non-terminal) Task per project.
- **Job** — one attempt at a Task: `base_sha`, `runner`, `budget`,
  `execution_result` ∈ {`""`, `success`, `failed`, `timeout`, `cancelled`}
  (*how the run ended*), `output_snapshot` head_sha (*the committed tree, captured
  even on failure as a salvage snapshot*), `state`. **Only a `success` execution
  promotes its snapshot to a candidate Artifact** — commit-exists never implies
  job-succeeded.
- **Artifact** — the durable result of a successful Job: `base_sha`, `head_sha`,
  `branch`, independent `verify` status, independent `review` status.

### State machine (control-plane owned)

```text
planned → queued → working → candidate → verifying →(PASS)→ verified → approval_required → approved → integrated
                     │  ▲                     │
              input_required                  └─(FAIL)→ rejected → retry (queued) / replan (planned)
terminal: failed · cancelled · expired · integrated
```

**The load-bearing invariant.** A *worker* may only drive `working → candidate`
("I think it's done"), plus the `working ↔ input_required` detour. **Only the
control plane** performs `candidate → verifying → verified`. This is enforced
structurally, not by convention: transitions have two entry points —
`WorkerCanTransition` (a strict subset) versus the full `CanTransition` — and the
store's `TransitionTask(..., byWorker)` dispatches between them. A worker request
literally cannot name `verified` as a target. That makes verification structural
rather than conversational.

### Storage

State is held in a SQLite database at `<data-dir>/control.db`
(`Config.ControlDBPath()`), using the **pure-Go** `modernc.org/sqlite` driver so
release builds stay `CGO_ENABLED=0`. The store is a thin, explicit-SQL layer (not
an ORM). State transitions are **atomic and optimistic**: each is an
`UPDATE … WHERE id=? AND state=?` (a stale or illegal move affects zero rows and
errors), and every successful transition writes an **append-only** row to an
`events` table **in the same SQL transaction**. SQLite holds *desired* state
only; worktrees and coordinator sessions are derived, reconcilable state.

### The daemon and the CLI (Sprint 55 / M13 — built)

The **`daedalus-control` daemon** ([`cmd/daedalus-control`](../cmd/daedalus-control))
is the single owner of `control.db`. It serves an HTTP-over-Unix-socket API at
`<data-dir>/.daedalus/control.sock` (`POST/GET /tasks`, `GET /tasks/{id}`,
`POST /tasks/{id}/dispatch`, `DELETE /tasks/{id}`). The `daedalus task` CLI is a
**thin client**: it obtains a client via `control.EnsureRunning`, which
auto-spawns the daemon detached (ssh-agent style, exactly like
`daedalus coordinator`) and reuses a running one via a pidfile + live-dial probe.
Because the daemon is the only writer, there are never two writers on SQLite.

The whole control-plane logic lives in a host-tested `control.Service`; the
daemon is a thin HTTP translation over it. Both the in-process `Service` and the
socket `Client` implement one `TaskAPI`, so the CLI is identical whether it runs
the logic directly (tests) or over the socket (production).

| Command | Effect |
|---|---|
| `task create --project <name> --objective <text> [--acceptance <ref>]` | Daemon resolves the project via the registry, requires a **Git repo**, captures `base_sha` from HEAD, enforces one-active-task-per-project, inserts a `planned` Task. |
| `task list` | All tasks: id, project, state, objective snippet. |
| `task status <id>` | A task with its jobs and artifacts. |
| `task dispatch <id>` | Run one headless Job attempt (see below). |
| `task cancel <id>` | Cancel the task, cancel its active jobs, reclaim their worktrees. |

### The execution model (Sprint 55 / M13 — built)

`task dispatch <id>` runs **one headless Job attempt**, Git-native throughout:

1. Drive the Task `planned → queued → working`; create a `Job` (records
   `base_sha`, runner, budget) in `working`.
2. **Isolated worktree.** `git worktree add` a clean checkout at the Task's
   `base_sha` on branch `daedalus/<task>/<job>`, at the deterministic path
   `<data-dir>/control/worktrees/<job>` — never the developer's checkout.
   Deterministic naming makes the side-effect idempotent.
3. **Run the agent headless** through an injectable `AgentRunner`. **Process exit
   is the Job boundary.** The real adapter (`CoordinatorRunner`) goes through the
   coordinator/Docker (`daedalus … -p` semantics, worktree mounted as
   `/workspace`); a Docker-free `StubRunner` drives host tests and the
   `DAEDALUS_CONTROL_FAKE_RUNNER` smoke.
4. **Capture** the worktree tree as `output_snapshot` (the wrapper auto-commits,
   since agents don't reliably commit) — captured **even on failure** as a
   salvage snapshot. `execution_result` records *how the run ended*.
5. **Promote only `success`.** A `success` run → Job `candidate` + a candidate
   `Artifact` on the branch; the Task → `candidate`, worktree kept (a candidate
   is non-terminal, its commit must survive for the future verifier).
   `failed`/`timeout`/`cancelled` → terminal Job + Task and the worktree is
   reclaimed. **Commit-exists never implies job-succeeded.**

### Reconciliation (Sprint 55 / M13 — built)

The daemon reconciles **on boot and on a 30s tick** (the level-triggered
controller pattern, minimal single-host form — the dual-write fix, §6). For every
non-terminal Job it compares desired (DB) vs observed reality:

- A `working` Job whose **coordinator session has vanished** (checked via an
  injectable `SessionObserver`) is captured, **failed**, and its worktree
  reclaimed. If liveness can't be verified (no observer / an error), the Job is
  **left alone** — never fail what you can't prove is dead.
- A live session → the Job is **adopted** as-is.
- An **orphaned worktree** (no live, non-terminal DB Job) is removed.

Side-effects are idempotent and deterministically named, so a re-run is a no-op.

## V1 scope boundary — what is deliberately NOT here yet

Milestone 13 (Sprints 54–55) delivers the model, store, daemon, human CLI, the
isolated-worktree headless execution path, and reconciliation. Still ahead:

- **No independent verifier.** The clean-container verification that performs
  `candidate → verified`, image digest-pinning by `sha256:`, and the
  test-integrity gate land in **M14**. Until then an Artifact stays `verify:
  pending` and a Task rests at `candidate`.
- **No governance / integration / Guild Master client.** Budgets + request
  rejection, the merge-queue integration transaction (rebase → re-verify merged →
  compare-and-swap), human approval, retry/replan, an independent reviewer, and
  the (tiered, injection-safe) `guild-control-mcp` client land in **M15**.
- **No parallelism.** One active Job per project; multiple concurrent worktrees
  are **M16**. `task dispatch` runs the attempt synchronously.
- **The real `CoordinatorRunner` is host-only.** It needs a Docker daemon and is
  not exercised in CI; the control-plane logic is proven with the fake runner.

Because there is still no agent client, the prompt-injection surface does not
exist yet and the foundation stays small — boring reliability from a small
surface area.
