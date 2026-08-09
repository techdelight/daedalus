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
| `task create --project <name> --objective <text> [--acceptance <ref>]` | Daemon resolves the project via the registry, requires a **Git repo**, pins `base_sha` to the plane-owned target, inserts a `planned` Task. Several active Tasks per project are allowed. |
| `task list` | All tasks: id, project, state, objective snippet. |
| `task status <id>` | A task with its jobs and artifacts. |
| `task dispatch <id>` | Run one headless Job attempt (see below). |
| `task verify <id>` | Plane-owned verification of a candidate (see below). |
| `task retry <id> [--rebase]` | Retry a rejected task as a fresh Job (see *Governance*). |
| `task review <id>` | Run the independent reviewer over a verified artifact. |
| `task approve\|reject <id> [--note]` | The human approval gate. |
| `task integrate <id>` | Land it: rebase → re-verify merged → compare-and-swap. |
| `task approvals` | Everything awaiting a human decision. |
| `task proposals [list\|confirm\|deny]` | Consequential operations an agent asked for. |
| `task target [<project> --sync]` | Show or resync the plane-owned integration targets. |
| `task replan <id> --objective <text>` | Return a rejected task to `planned` with a revised objective. |
| `task events <id>` | The control-plane-managed event log for a task. |
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

### Independent verification (Milestone 14 — built)

"Done" is decided by the control plane checking a committed artifact against an
oracle the worker cannot edit (§6) — never by the worker's self-report. A worker
only ever reaches `candidate`; **only the control plane** performs
`candidate → verified`. The result is an **independently reproducible verification
result, not a proof of correctness**: it proves *"this committed artifact, in this
pinned environment, causes this frozen procedure to report success"* — which
closes uncommitted/environment-resident cheating and (via the integrity gate)
committed-test edits, but a sufficiently adversarial agent that writes weakened
*product* code that its own committed tests still pass is a limit tests share.

**Acceptance contract.** A project declares its verify policy in a committed
`.daedalus/verify.json`:

```json
{
  "checks": ["go build ./...", "go test ./...", "daedalus docs lint --ci"],
  "acceptanceGlobs": ["**/*_test.go", "testdata/**", ".daedalus/verify.json"]
}
```

`checks` are the commands the clean verifier runs; `acceptanceGlobs` are the paths
whose edits invalidate a Job. `ReadAcceptancePolicy` reads it from a checkout; a
project that declares none gets a built-in default (`daedalus docs lint --ci` —
daedalus is language-agnostic and cannot know a project's build/test command, so
those are declared per-project — plus the conventional test/fixture globs and the
verify config itself).

**Frozen acceptance oracle.** At `task create` the policy is read **as committed
at `base_sha`** (`git show <base>:.daedalus/verify.json`, immutable to later
working-tree edits) and a stable hash of the normalized (commands + globs) is
frozen on the Task row (`acceptance_hash`). Editing the policy in the working tree
afterward does not change it — the oracle is pinned outside the agent's reach.

**Image digest pin.** So the artifact is verified in the same environment it was
authored against, the project image is captured by **`sha256:` digest** (via
`docker image inspect --format '{{.Id}}'`) — not a mutable tag — at task create,
or lazily at first verify if the image was built later, and recorded on the Task
(`image_digest`). The verifier runs against that digest. Capture is behind an
injectable `ImageDigester` seam so the plumbing is host-tested without Docker.

**Verifier environment policy.** The clean verifier is hermetic-ish, **not** the
project's dev environment: `--network none` (off), **no ambient credentials**, **no
inherited `/opt/tools`**, no project caches — only the clean checkout is mounted,
read-write, at `/workspace`, and `--rm` leaves nothing behind. Expressed as a pure
`VerifierEnvPolicy.DockerRunArgs` so what is (and isn't) mounted is asserted in a
host test.

**Plane-owned verify flow** (`daedalus task verify <id>`, behind the injectable
`VerifyRunner`):

1. Re-derive the policy from `base_sha` and confirm it still hashes to the frozen
   value (a drift → straight to `rejected`).
2. **Null-agent floor.** If `head_sha == base_sha` (the Job made no change at all),
   reject with an "empty change" note — a do-nothing job can never verify as done.
3. **Test-integrity gate.** `DiffTouchesAcceptanceFiles(base..head, globs)`
   (`git diff --no-renames --name-only`, `**`-aware glob match) — if the Job's diff
   edits any frozen acceptance file, it goes **straight to `rejected`** and **the
   `VerifyRunner` is never called** (you cannot grade your own exam).
4. Otherwise `candidate → verifying → VerifyRunner → verified | rejected`. The
   **real `CleanVerifier`** checks out the artifact's `head_sha` into a **fresh,
   separate clean worktree** (never the Job's mutable worktree), runs each
   `policy.checks` command in a container built from the **pinned image** under the
   env policy, and fails on the first non-zero check. A pass sets the Artifact
   `verify: pass`; a rejection sets `verify: fail`, reclaims the attempt's worktree,
   and rests the Task at `rejected`.
5. **Retry:** `task dispatch` accepts a `rejected` Task (`rejected → queued →
   working`), creating a fresh Job attempt.

The `CleanVerifier` (Docker) and the `dockerImageDigester` are **host-only**; the
control-plane logic (floor, gate, freeze, digest plumbing, env-policy args,
transitions) is fully host-tested with a fake `VerifyRunner`, and
`DAEDALUS_CONTROL_FAKE_VERIFY` selects the stub so CI and the verify scripts stay
Docker-free. See `scripts/verify-m14.sh`.

## Governance — budgets, rejection, retry/replan, the event log (Sprint 58 / M15 — built)

A plane that can only execute is not an authority. Governance is what lets it say
**no**, say *why* in a form a machine can act on, and keep a record of having said
it.

### Budgets

Every Task carries a **budget**, resolved at create and stored authoritatively on
the Task row (`budget`, new column + idempotent migration). §6 splits the axes
honestly, and so does the code:

| Axis | Status | How |
|---|---|---|
| **wall-clock** (per Job) | **Enforced** | the plane races the runner against a deadline context; an overrun is `execution_result=timeout` and the Job/Task go terminal |
| **max-attempts** (per Task) | **Enforced** | Jobs are counted; a dispatch/retry beyond the count is refused |
| **max-review-cycles** (per Task) | **Enforced** | transitions into `verifying` are counted **from the event log**; a further verify is refused |
| **concurrency** (per project) | **Enforced** | running Jobs (`queued`/`working`/`input_required`) are counted; a further dispatch is refused |
| **turns · tokens · cost** | **Policy only — NOT enforced** | Daedalus takes *process exit* as the Job boundary and never sees an agent's turn/token/cost accounting. These are **runner-dependent measurement**: recorded on the Task, surfaced in `task status`, and honoured only by something that can measure them. **Nothing in Daedalus stops a Job that exceeds them.** |

Defaults are `wall-clock=3600s, attempts=3, review-cycles=3, concurrency=1`, and
are **per-project overridable** from a **host-side** file,
`<data-dir>/control/budgets.json`:

```json
{
  "default":  {"wallClockSeconds": 1800, "maxAttempts": 2},
  "projects": {"big-app": {"wallClockSeconds": 7200, "maxAttempts": 5}}
}
```

Unset (zero) fields inherit: request → project override → policy default →
built-in. The file lives under the Daedalus data dir and **never in a project
checkout** — a budget read from an agent-writable repo would let an agent raise
its own ceiling by committing a file, the exact authority inversion §5 forbids.

It is re-read per lookup, so an operator's edit applies to the next task without
a restart — and it **fails closed**. A file that cannot be read or parsed does
not take the plane down, but it must not *widen* the envelope either: falling
back to the built-in default would do exactly that whenever a project's policy is
stricter than the default, and a non-atomic editor's partial write is a real
widening window. So the last successfully-parsed policy is cached and reused;
only when no good read has ever happened — the same state as a missing file —
does the built-in default apply.

`task create` flags (`--wall-clock`, `--max-attempts`, `--max-review-cycles`,
`--concurrency`) may only ever **narrow** the project's ceiling. Asking for more
is refused with `over_budget`; raising a ceiling means editing the host-side
policy file.

**Zero means unbounded, so negative is invalid.** Every enforcement site guards
`budget.X > 0`, which makes a negative value read as *unbounded* — wider than any
number a caller could legitimately request. A negative axis is therefore rejected
as malformed input (`invalid_budget`) at every door a value can come through: the
create request **in the service, not the CLI** (the CLI is a convenience; the
socket API is the security boundary, and an agent joins it in Sprint 60), the
policy file at load, and the row scan as a backstop for a hand-edited
`control.db`.

**Honest limit on the wall-clock kill.** The plane guarantees its own verdict and
cancels the Job's context; it cannot guarantee the death of a process it did not
fork. A runner that ignores its context keeps running in the background until it
exits. Killing the underlying container needs a context-honouring runner — the
real `CoordinatorRunner` is `exec`-based and does not abort a command mid-flight
today.

### Typed rejection

Two shapes of "no" share one machine-readable vocabulary:

- a **refusal** — the plane declines a request; **nothing changes state**, a
  decision event is appended, and the caller gets a `*RejectionError`
  (HTTP **422** with `{"error", "reason", "message"}`, CLI **exit code 3**);
- a **verdict** — the plane acted and the artifact was rejected; Task/Job land in
  `rejected`, and the reason rides on the transition event and `VerifyResult.Reason`.

| Reason | Kind | Meaning |
|---|---|---|
| `over_budget` | refusal | the requested budget widens the project's ceiling |
| `invalid_budget` | refusal | a budget axis is negative — malformed input, never "unbounded" |
| `attempts_exhausted` | refusal | the Task has used all its attempts |
| `review_cycles_exhausted` | refusal | the Task has used all its review cycles (the candidate is left untouched — refusing to look is not a verdict) |
| `concurrency_exceeded` | refusal | the project already has its budgeted running Jobs |
| `unsafe_rebase` | refusal | the rebase target contains commits this Task's own Jobs authored |
| `operation_in_flight` | refusal | the Task already has a dispatch or verify running |
| `stale_base` | verdict | the candidate's `base_sha` is no longer the project's target tip |
| `null_agent_floor` | verdict | `head_sha == base_sha` — an empty change |
| `policy_drift` | verdict | the acceptance policy at `base_sha` no longer hashes to the frozen value |
| `integrity_gate` | verdict | the Job's diff edits frozen acceptance files |
| `verify_failed` | verdict | the clean verifier ran and reported failure |

**Stale base.** An artifact built on a base the plane has moved past proves
something about a tree nobody will integrate, so it is rejected **before** the
integrity gate or the verifier — a doomed artifact never costs a verifier
container. Since Sprint 59 "the tip" is the **plane-owned integration target**
(see below), so a stale base means *another integration landed*, not *somebody
moved a branch*: recommending a rebase is safe again, because the commit being
rebased onto is one the plane itself landed. The remedy is
`daedalus task retry <id> --rebase`.

Exit codes: `0` success, `1` failure, **`3` refused by policy**. That distinction
is the whole point — a governed plane that only ever said "error" would be
indistinguishable from a broken one.

### Retry and replan

```text
rejected ──retry──→ queued → working → …      (a FRESH Job, attempt counter advanced)
rejected ──replan─→ planned                    (a revised objective, same Task)
```

- `daedalus task retry <id> [--rebase]` — a fresh Job on the same Task, budget
  re-checked first. **Attempt history is preserved, never overwritten**: the
  previous Jobs (and the record of why each was rejected) stay exactly as they
  were, so a Task carries its whole chain. `--rebase` first re-pins the Task to
  the **plane-owned integration target** and re-freezes the acceptance policy
  there — the remedy for `stale_base`. It stays opt-in because re-freezing the
  oracle adopts whatever verify policy the new base carries, and it is still
  refused outright when that target contains the Task's own Job commits (defence
  in depth; the target itself is no longer worker-writable).
- `daedalus task replan <id> --objective <text>` — for when the *instruction* was
  wrong rather than the work. Objective and state change in one transaction (no
  window where `planned` carries the objective that was just rejected). It does
  **not** reset the attempt counter: the budget bounds the Task, not the
  objective, so replanning cannot be used to buy more attempts.

Neither added a state or an edge: retry reuses `rejected → queued`, replan reuses
`rejected → planned`. The two-transition-table invariant (`workerReachable` vs
`legalTransitions`) is untouched, so nothing here brings a worker any closer to
`verified`.

### Long operations, cancellation, and interrupted verifications

A dispatch and a verification are both long: a Job may legitimately run for its
whole wall-clock budget, and a verifier is a container run. The service lock is
therefore held only for **DB bookkeeping** — it is released across `runner.Run`
and `verifier.Verify` — so `task cancel` and the reconcile loop stay responsive
while work is in progress rather than being inert for up to an hour.

What the lock used to provide by accident is now explicit:

- an **in-flight set** (task id → dispatch/verify) means a second operation on the
  same Task is *refused immediately* (`operation_in_flight`) rather than queued
  behind an hour-long lock;
- **reconcile skips in-flight work.** A Job this process is running right now is
  live by definition; without the check, a 30s tick could "repair" a perfectly
  healthy Job;
- **cancellation wins.** If a Task is cancelled while its Job runs, the post-run
  bookkeeping records the execution result and *keeps the terminal state* instead
  of fighting the state machine.

The concurrency budget is enforced from the Job rows, which now genuinely reflect
reality for the duration of a run — previously the lock serialised everything so
tightly that a second dispatch never saw the first one running.

**An interrupted verification does not strand a Task.** `verifying` is a state
only the plane can leave, so a crash (or an error) between the transition and the
verdict used to wedge a Task permanently: verify, retry, replan and dispatch all
refuse a `verifying` Task, and only `cancel` escaped. Now:

- `VerifyTask` checks its configuration **before** anything moves, and rolls the
  Task back to `candidate` on any abort;
- `Reconcile` returns a Task stranded in `verifying` (with no verification in
  flight) to `candidate` — the same level-triggered repair that already existed
  for a crashed dispatch. This adds one plane-only edge, `verifying → candidate`;
  it is deliberately absent from `workerReachable`, so it brings a worker no
  closer to `verified`;
- **a verification that never ran costs nothing.** Review cycles are counted as
  entries into `verifying` *minus* recoveries back to `candidate`. Because the
  log is append-only the entry can never be erased, so without the subtraction a
  daemon crash would permanently spend a cycle of the budget for work that never
  happened.

### The control-plane-managed event log

Every transition, budget decision, rejection, and verification outcome is a typed
row in `events`: `kind` ∈ {`created`, `transition`, `budget`, `rejection`,
`verification`, `governance`}, an optional machine-readable `reason`, the state
change, and an `actor`.

`daedalus task events <id>` renders the whole chain — the Task plus every Job it
ever had and every Artifact those Jobs produced — in the order things happened.

**Named honestly.** The log is **control-plane-managed** and **immutable through
the API**: `INSERT` is the only statement the package ever runs against `events`,
there is no update/delete/amend operation anywhere, the `TaskAPI` exposes exactly
one event method and it reads, and `/tasks/{id}/events` answers `GET` only. Tests
assert all four, including a source scan that fails the moment someone writes a
mutating statement. It is **not cryptographically tamper-proof**: anyone with
access to `control.db` can still edit the file. Hash-chaining remains an optional
later property and is not claimed here.

**On `actor`.** `human` / `control-plane` / `worker` / `system` label a request's
*origin*, not an authenticated identity — today the only thing that can reach
`control.sock` is the human CLI. When the Guild Master joins as a client the
caller identity must be carried on the request and checked. The label grants no
authority in any case: transitions are gated by the two tables in `model.go`.

## The plane-owned integration target (Sprint 59 / M15 — built)

Each **repository** has a target commit that only the control plane can move. It
is a row in `control.db`, not a git ref in the project repository, and it is the
answer to the sharpest finding of the Sprint-58 audit.

It is keyed by **canonical repository path**, not by project name. Two registry
projects can point at one checkout — a clone registered twice, or a project
registered on a subdirectory of another — and per-project rows would give them
two merge queues on one trunk, each rebasing onto its own notion of it and
swapping a row the other never reads. The queue's entire safety property would
silently not apply between them. `daedalus task target` shows which projects
share a queue, because sharing is surprising unless it is said out loud.

**The problem it closes.** Until Sprint 59 a Task's `base_sha` and its frozen
acceptance policy both came from the project checkout's `HEAD`. A Job's worktree
shares the parent repository's **refs**, not merely its objects, so a worker able
to reach the common git dir could move the branch the oracle was read from. The
Sprint-58 ancestry guard caught the naive form — pointing the branch at the Job's
own commit — but two ways past it were confirmed, and neither could be closed by a
better ancestry check:

- **cherry-pick laundering.** `git cherry-pick`, `git commit-tree`, or
  `format-patch | am` reproduce the same weakened `.daedalus/verify.json` under a
  **new SHA with no ancestry link** to any Job commit. Structurally that commit is
  indistinguishable from an honest developer commit containing the same bytes.
- **waiting for the next Task.** The guard was bolted onto `retry --rebase`, but
  `CreateTask` froze whatever `HEAD` returned with no ancestry check at all — so
  an attacker never needed `--rebase`. Poison the branch, wait.

**The fix is structural, not a better check.** The plane stops reading the oracle
from a ref the worker can write:

| | Before (Sprint 58) | Now (Sprint 59) |
|---|---|---|
| Task `base_sha` | checkout `HEAD` | the plane-owned target |
| Acceptance policy frozen at | checkout `HEAD` | the plane-owned target |
| Staleness measured against | checkout `HEAD` | the plane-owned target |
| Advanced by | anything that can write a git ref | a completed integration transaction, or an explicit human resync |

An agent may now rewrite every branch in the repository — cherry-pick,
`commit-tree`, `update-ref`, anything — to **no effect**: none of them is the
commit its work is based on or graded against. Both attacks are covered by tests
that first assert the attack still defeats ancestry, then assert it changes
nothing (`TestAttack_CherryPickLaundering`, `TestAttack_WaitForTheNextTask`,
`TestAttack_WorkerRewritesEveryRef`). The Sprint-58 ancestry guard survives as
**defence in depth** — it would still catch a target that became self-authored
some other way, e.g. an operator resyncing onto a commit a Job had planted — but
it is no longer the mechanism, and nothing rests on it.

`refs/daedalus/target` is written in the repository as a **projection**, so the
landed commit is visible and reachable. Nothing reads a decision from it; a worker
that overwrites it changes nothing the plane believes, which is asserted rather
than assumed. It is deliberately not a branch anyone checks out, so updating it
cannot disturb a working tree.

**Two honest caveats:**

1. **Adoption is trust-on-first-use.** A repository with no target yet takes the
   operator's checkout `HEAD`, once, at the first Task for it — before any Job for
   that repository has run under the plane. The plane cannot invent a trusted
   starting commit; it can only refuse to keep taking new ones. Adoption happens
   **only** when the target genuinely does not exist: any other failure reading it
   is surfaced, never treated as "there isn't one", because that fallback reads
   the worker-writable checkout `HEAD` and it is the single most
   security-relevant read in the package.
2. **The resync is consequential.** `daedalus task target <project> --sync`
   re-points the target at the checkout's `HEAD`, adopting whatever policy that
   commit carries. It is manual, logged, and belongs on the Sprint-60 list of
   operations an agent client must never be granted.

## The integration transaction (Sprint 59 / M15 — built)

`daedalus task integrate <id>` lands an approved artifact as a **race-safe
transaction** (§6, "Integration is a race-safe transaction, not a merge"):

```text
serialize per project
  → rebase the artifact onto the current target
  → RE-VERIFY THE MERGED RESULT (not the pre-merge branch)
  → compare-and-swap the target   (retry if it moved under us)
```

The step that is easy to drop is the third. Two artifacts that each pass
verification against base A can conflict when combined with **no textual conflict
at all** — a *semantic* merge conflict. Verifying the pre-merge branch proves
something about a tree that will never exist; only the merged result is what
lands. Re-verification runs through the same `VerifyRunner` seam as candidate
verification, so production uses the real clean verifier and host tests use a
fake.

The compare-and-swap is the whole safety property in one statement:
`UPDATE integration_targets SET sha = ? WHERE repo_path = ? AND sha = <what we
started from>`.
If another integration landed while this one was rebasing and re-verifying, it
affects zero rows, **nothing is written**, and the transaction recomputes against
the new tip — bounded to 3 attempts, after which it refuses with
`integration_raced` having landed nothing.

Every failure path leaves the target exactly where it was and the Task recoverable
through the retry/replan ladder:

| Failure | Reason | Result |
|---|---|---|
| The artifact does not replay onto the target | `merge_conflict` | Task → `rejected`, target untouched |
| The **merged** result fails verification | `merged_verify_failed` | Task → `rejected`, target untouched |
| The target kept moving | `integration_raced` | Task stays `approved`, nothing landed, try again |

**The queue is load-bearing as of Sprint 61.** Until then, one active Task per
project meant two integrations could never be in flight for a single repository,
so the compare-and-swap had no concurrent writer to lose to — it was insurance.
Lifting that invariant made it real work: two Tasks on one project can now be
verified against the same target and try to land at the same time, and the CAS is
what decides. The loser rebases onto the winner and re-verifies, so both changes
end up in the trunk and neither is lost. This is exercised by competing landings
from real goroutines under `-race`, not by simulated interleaving.

**Re-integration is idempotent.** The compare-and-swap commits before the Task is
marked `integrated`, and those are two different stores — git and the target row,
then the task row. If the second write fails, the target has advanced while the
Task still says `approved`: the one place in the transaction where a write
survives an error. That cannot be made atomic across SQLite and git, so instead a
re-integration first asks whether the artifact's commits are **already contained
in the target** — by ancestry for a fast-forward landing, by patch id
(`git cherry`) for a rebased one, since a rebase changes the sha but not the
content — and settles the Task to `integrated` rather than landing the same work
a second time.

## Human approval and the independent reviewer (Sprint 59 / M15 — built)

```text
verified → approval_required → approved → integrated
```

**Approval is plane authority.** Every edge on that tail is absent from
`workerReachable`, so no worker-driven request can approve, integrate, or walk any
part of the gate. That much is structural and exhaustively tested.

**What the state machine alone does not prove.** It stops a *worker* approving. It
does not by itself stop an agent **client** approving, because the actor on an
approval is a label of the request's origin rather than a cryptographic identity.
That gap is closed by the **socket boundary** (shipped in Sprint 60): the daemon
listens on a human socket and a restricted agent socket, the agent's container is
given only the latter, and approval is refused for the agent caller class. Peer
credentials could not have done this — the socket is `srwxr-xr-x` and the agent
runs as the same uid — so the split is the mechanism, not a supplement to one.
What remains true: the caller class is a *class*, not an identity, so two agent
clients are indistinguishable from each other, and anything already running as the
operator can open either socket.

**Approval fails closed.** "Fail closed" means something different on each
governance axis, which is why they are not one function. For a **budget** it means
the narrower envelope — an unreadable policy holds the last known-good ceiling,
falling back to the built-in default. For **approval** it means **requiring a
human**: if the governance file cannot be read and nothing good was ever read
from it, the plane does not know whether this project needs approval, and "I
don't know" has to mean "ask someone". The cost of being wrong that way is a
person asked unnecessarily; the cost of the other direction is an unapproved
change landing while the log claims policy said it was fine. For the same reason
the auto-approval event says only that *the configured policy source did not
request* a human — never that "policy said" something, which would be a lie
whenever no policy was read.

**Approval is opt-in per project** (§9, "keep governance opt-in"), declared in the
same host-side governance file as budgets:

```json
{
  "default":  {"maxAttempts": 3},
  "approval": {"default": false, "projects": {"payments": true}}
}
```

A project that does not require it still *walks* `verified → approval_required →
approved` at integration time, driven by the plane, with an event recording that
policy — not oversight — is why no human was asked. The states are never skipped,
so the log always answers "who approved this, and if nobody, why not".

**The independent reviewer** (§6 ladder, rung 5) is an injectable `ReviewRunner`,
a stub for now exactly as `VerifyRunner` was in Sprint 56. `daedalus task review
<id>` runs it over the verified artifact and sets the Artifact's `review` status;
a failure routes to `rejected` with `review_failed` and feeds retry/replan. When a
reviewer is configured it **gates integration**; when none is, review is not a gate
at all, so a plane without one is not blocked forever. Review passes are bounded by
`maxReviewCycles` — the same *limit* as verification cycles, counted **separately
and not summed**, so a Task gets N verifications and N reviews rather than N of the
two combined.

### Approving from the Web UI and TUI

Both surfaces are **clients** of `control.sock` with no authority the CLI lacks:
`GET /api/approvals` plus approve/reject in the Web dashboard, and `[A]` in the
TUI. Neither **spawns** the control daemon — a dashboard that started one because
somebody opened a tab or pressed a key would be a surprising side effect — and
when the plane is unreachable both say so explicitly rather than rendering an
empty queue, because "nothing needs you" and "I could not ask" are different
answers and only one of them is reassuring.

**This changes what `daedalus web --no-auth` gives away.** The dashboard now
carries **write authority over human approval**: `POST
/api/approvals/{id}/approve|reject` decides whether an agent's work may be
integrated. The handlers are behind the same auth middleware as everything else
and auth is on by default, so the shipped configuration is safe — but `--no-auth`
now hands the approve button to anyone who can reach the port, and WSL2
auto-detection binds `0.0.0.0`. The approval gate is only as strong as the
weakest surface that can operate it.

## Concurrency and the scheduler (Sprint 61 / M16 — built)

Until Sprint 61 a project could have **one active Task**, and therefore one Job.
That invariant had held since M13 and was quietly relied on in several places, so
lifting it was mostly an audit: find what assumed it, then decide each case.

**What actually changed is smaller than it looks, and the shape decides the rest:**

- A **Task** still has at most one Job in flight. The state machine guarantees it
  — a Task is dispatchable only from `planned`/`queued`/`rejected`, and
  dispatching moves it to `working`. So every per-Task singular lookup
  (`candidateJob`, `firstArtifact`, `jobInState`) stays correct unchanged.
- What was forbidden and is now allowed is **several Tasks active on one
  project**. That is the unit of parallelism.
- The service lock was **already** released across the long calls (Sprint 58's
  `unlockedDuring`), so two dispatches genuinely overlap the moment two Tasks
  exist. No execution machinery was added.

**The trap, named because it is easy to get wrong:** `withClaim` is keyed by
**Task**, and concurrency is per **project**. While only one Task per project
could be active those were the same sentence; they are not any more. N Tasks on
one project hold N independent claims, so the claim set does not limit project
concurrency and never did — it stops two operations racing on *one* Task. The
per-project limit had to come from somewhere else, and that is the scheduler.
Building the limiter on the claim set would have produced one that silently never
fires.

### Limits

| Limit | Source | Meaning |
|---|---|---|
| **Global** | `concurrency.global` in the governance file | running Jobs across every project — the host's capacity, since each Job is a container |
| **Per-project** | `concurrency.perProject` | running Jobs within one project |
| **Per-Task** | the Task budget's `concurrency` axis | an optional cap a Task sets on *itself*, for a Task that wants to be stricter than its project |

The tightest applicable limit binds. "Running" means `queued`/`working`/
`input_required` — `candidate`, `verifying` and `rejected` are non-terminal but
**idle**, waiting on a plane or human decision rather than a container, so
counting them would make a project look full while nothing executed.

```json
{
  "default":     {"maxAttempts": 3},
  "concurrency": {"global": 8, "perProject": 3}
}
```

**Defaults preserve the old behaviour**: `perProject: 1`. Lifting the invariant
changes what the plane *can* do; it must not silently change what an existing
installation *does* do. Parallelism is opt-in.

This is also what finally makes the budget's `concurrency` axis fire at all —
Sprint 58's audit noted it could effectively never trigger, because only one Job
per project was possible. Its default moved from `1` to *unset* for the same
reason: a Task-level default of 1 would silently override the operator's
per-project limit and make parallelism impossible to switch on.

### Fairness

When capacity is available, **only the oldest waiter may take it**.

Without that rule a project dispatching in a tight loop starves the Task that
asked first — and because a refusal is a typed rejection the caller retries, so
the starved Task would retry forever while newer work sailed past. A Task refused
for capacity keeps a place in line; a newer Task offered free capacity is refused
with `queued_behind` and told which Task it is yielding to.

Fairness is **per project**, because freeing project A's slot does nothing for a
Task waiting on project B — making B wait would be starvation dressed as fairness.
The exception is the global limit, which *is* shared: a Task waiting on global
saturation competes with every project. A Task that is cancelled or finishes drops
its ticket, so work that will never run cannot block the queue head forever.

Every admission decision — allowed or refused — is a typed `schedule` event.
A scheduler that quietly declines is indistinguishable from one that is broken.

### What stayed correct, and what needed care

- **Reconcile** skips Jobs this process is running (the in-flight set), so a pass
  during N concurrent Jobs adopts nothing and reclaims nothing.
- **Cancellation** targets one Task's Job and leaves its siblings running.
- **Worktrees** are named per Job (`J-n`), which is globally unique, so concurrent
  create/remove cannot collide.
- **Session liveness is per PROJECT, not per Job** — an honest limitation.
  `SessionObserver.HasSession(project)` cannot say *which* of a project's Jobs is
  alive. The consequence is conservative in the safe direction: with no session at
  all, every Job of that project is correctly reaped; with at least one session,
  none is, so a crashed Job among healthy siblings is adopted rather than failed
  and survives until the daemon restarts with none running. That leaks a stale Job
  rather than destroying a live one, which is the right way round, but it is a
  real imprecision and per-Job liveness is the fix.

## The Guild Master as a gated client (Sprint 60 / M15 — built)

The Guild Master can finally **act**. What makes that safe is not that it is
asked to behave — it is that the consequential operations are not available to
it.

### Caller identity comes from the transport

The actor on an event is derived from **which socket a request arrived on**, and
nothing else.

- Not a request field. A client that can name its own actor can name `human`,
  which is worse than no label at all.
- Not peer credentials. The control socket is `srwxr-xr-x` and the Guild Master's
  agent runs as the **same uid** as the operator, so `SO_PEERCRED` separates
  *users*, not *caller classes* — it would return the same answer for both.

So the daemon listens on two sockets: `control.sock` (human — CLI, Web, TUI) and
`control-agent.sock` (agent). The agent's container is given exactly one of them.
**The socket split is the mechanism**, not a supplement to one: the class is
fixed by the mount namespace before a byte of request is parsed. What this does
*not* defend against is anything already running as the user, which can open
either socket — the boundary is the container, not the file mode.

### Tiered authority

| Tier | Operations | Caller: agent |
|---|---|---|
| Read | `list_tasks`, `get_task`, `task_events`, pending approvals, queues | executes |
| Bounded write | `create_task`, `request_verification`, `request_review` | executes |
| Consequential | dispatch, retry, replan, cancel, integrate, approve/reject, target resync | **recorded as a proposal** |
| Human-only | confirming or denying a proposal | **refused outright** |

Reads and creation execute because they cannot exceed policy: a created task is
budget-clamped and graded against an oracle frozen at the plane-owned target, so
the worst a poisoned document achieves is a task nobody wanted — visible,
bounded, and cancellable. Verification and review execute because they apply the
**plane's own oracle**, which the caller cannot influence.

Everything consequential becomes a **proposal**: a row a human confirms or
denies. Confirming runs the operation *as the confirming human*; denying does
nothing at all. The refusal returned to the agent is loud and typed
(`proposal_recorded`) rather than a silent success — an agent that believed it
had cancelled a Job when it had only asked would reason on from a false premise.

Two properties are worth stating precisely:

- **An agent cannot confirm its own proposal.** Confirmation is refused for agent
  callers at **two independent layers, both tested** — in the caller scope and
  again in the service beneath it — and it carries a proposal-tier entry so a
  future generic dispatch could not route around them. That is defence in depth,
  not type-level impossibility: it is enforced by checks, and the tests are what
  hold the checks in place. It is what makes "the Guild Master cannot approve its
  own work" true in practice.
- **There is no raise-a-budget operation to tier.** A budget is resolved at create
  against a host-side ceiling and a request may only narrow it, so raising one is
  not an operation that can be attempted, tiered, or refused — it does not exist.

An unknown operation is `TierProposal` for an agent, never allowed: a new
operation nobody thought to tier fails closed into "ask a human", and a table
test fails if a mutating operation has no explicit entry.

### `guild-control-mcp`

The MCP server the Guild Master drives, env-gated into its container exactly as
`guild-mcp` is, and wired only when the restricted socket is actually mounted —
no socket, no tool. It exposes intent-level operations only: **never**
`run_shell`, `docker_run`, `mount`, `git_exec` or `start_container`, and never
`coordinator.sock`. The plane resolves the project through the trusted registry
and constructs every execution detail itself, so the Guild Master cannot become a
privileged remote shell: there is no tool through which to ask for one.

### Opaque queue ids

`GET /targets` used to list every queue with its absolute host path, and the
target operations wrote those paths into the **append-only** event log as entity
ids — so once an agent could read the log, host layout would be historical and
unerasable, and an agent on one project would learn the on-disk location of every
other repository including the Guild Master's. That is the line M12 drew when it
made cross-project access read-only and mount-mediated.

Each queue now has a stable opaque id, `sha256(canonical path)` truncated to 64
bits, stored beside the path and used as the event entity id. An agent keeps what
it legitimately needs — the id is stable and comparable, so it can still tell
that two projects share a queue — and learns nothing about host layout. Humans
still see the path, because for a person the path is the useful part.

## Scope boundary — what is deliberately NOT here yet

Milestones 13–14 deliver the model, store, daemon, human CLI, isolated-worktree
headless execution, reconciliation, and independent verification (clean checkout,
digest pin, env policy, integrity gate, null-agent floor); Sprint 58 adds the
governance core (budgets, typed rejection, retry/replan, the event log). Still
ahead:

- **No real reviewer.** `ReviewRunner` is a seam with a stub behind it, wired only
  under `DAEDALUS_CONTROL_FAKE_REVIEW`. With no reviewer configured, review is not
  a gate.
- **Nothing lands by itself.** Integration is always an explicit
  `daedalus task integrate`; the plane never advances a target on its own.
- **No dependency scheduling.** Jobs run concurrently (Sprint 61), but there is
  no cross-project task graph and no blocked/ready ordering between Tasks — that
  is **Sprint 62**. Typed steering is **M17**.
- **The caller class is a class, not an identity.** Two agent clients on the
  restricted socket are indistinguishable from each other, and anything already
  running as the operator can open either socket. The boundary is the container's
  mount namespace, not the socket's file mode.
- **`task dispatch` runs its attempt synchronously.** Several dispatches may be in
  flight at once (Sprint 61), but each call blocks until its own Job ends.
- **The real `CoordinatorRunner` is host-only.** It needs a Docker daemon and is
  not exercised in CI; the control-plane logic is proven with the fake runner.

Because there is still no agent client, the prompt-injection surface does not
exist yet and the foundation stays small — boring reliability from a small
surface area.

## Verifying on a real host

`scripts/verify-m13.sh` runs the control plane end to end against an **isolated**
data dir (it never touches your real registry, `control.db`, or a running
`daedalus-control` — the daemon it starts is killed by its own pidfile):

- `bash scripts/verify-m13.sh fake` — the full daemon + CLI + isolated-worktree +
  reconcile loop, asserted, **with no Docker** (uses `DAEDALUS_CONTROL_FAKE_RUNNER`).
- `bash scripts/verify-m13.sh real <project> [objective]` — dispatches the **real**
  headless agent (the `CoordinatorRunner`) against an isolated worktree of a
  registered project. Needs a built image + working runner credentials; this is
  the one seam CI cannot exercise. Your main checkout is untouched (the job runs
  on a `daedalus/<task>/<job>` branch in a separate worktree).
