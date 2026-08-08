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
only; containers/worktrees will be derived, reconcilable state (M13, Sprint 55).

### Human CLI

`daedalus task` drives the store in-process — the deterministic, human-only
reference path that makes the plane useful at N=1:

| Command | Effect |
|---|---|
| `task create --project <name> --objective <text> [--acceptance <ref>]` | Resolve the project via the registry, require it to be a **Git repo**, capture the current `base_sha` from HEAD, enforce one-active-task-per-project, insert a `planned` Task, print its id. |
| `task list` | All tasks: id, project, state, objective snippet. |
| `task status <id>` | A task with its jobs and artifacts. |
| `task cancel <id>` | Legal transition to `cancelled`. |

Orchestration is **Git-native**: `base_sha`, branches, and commits are
load-bearing, so a guild-managed project must be a Git repository — this is a
stated prerequisite, not a generic "artifact" abstraction. `task create` reads
`<dir>/.git` HEAD directly (no shelling out, no container) and rejects a non-Git
directory clearly.

## V1 scope boundary — what is deliberately NOT here yet

Sprint 54 is **the model, the store, and the human CLI, only.** There is
intentionally **no execution and no agent client**:

- **No daemon / no `control.sock`.** The CLI opens the DB in-process. The
  `daedalus-control` daemon and socket API land in **Sprint 55 (M13)**.
- **No Git worktree, no Job wrapper, no headless run.** No agent is dispatched;
  no `candidate` is produced by real work. Isolated per-Job worktrees and the
  process-exit execution boundary land in **Sprint 55 (M13)**.
- **No reconcile loop.** Reconcile-on-boot + periodic loop (the desired-vs-real
  repair mechanism) lands in **Sprint 55 (M13)**.
- **No verifier.** The clean-container verification that performs
  `candidate → verified`, image digest-pinning, and the test-integrity gate land
  in **M14**.
- **No governance / integration / Guild Master client.** Budgets, the merge-queue
  integration transaction, human approval, and the (tiered, injection-safe)
  `guild-control-mcp` client land in **M15**.

Because there is no agent client in V1, the prompt-injection surface does not
exist yet and the foundation stays small — boring reliability from a small
surface area.
