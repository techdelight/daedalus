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

**The target does not follow your branch, and the plane now says when it has
stopped following one (#89).** It moves only when work integrates or when
somebody runs `task target <project> --sync`. Nothing is wrong with a branch
that is ahead of it — but a Task freezes the TARGET as its base, so an unsynced
repository dispatches agents against a tree it has moved past, and the work comes
back coherent for the tree it was given and obsolete for the repository.
`stale_base` cannot catch that: it compares a candidate's base against the target
tip, and the target tip *is* its base. So `task create` warns, and
`daedalus control status` reports the gap per project. Both report; neither
refuses, because only the operator knows whether the gap is intended. A target
that is no longer an ancestor of HEAD is reported as a **divergence** rather than
a count, since a commit count across one means nothing. Measured 2026-08-22: the
target sat four days and seven commits behind, and T-17 was built against a tree
in which plane-owned programmes did not exist.

**`daedalus control start|stop|restart|status`** is the explicit lifecycle, added
because auto-spawn covers starting and covers nothing else. Stopping had no
command at all — the documented answer was `kill $(cat …/control.pid)`, which
asks an operator to know a path and reach for `kill(1)` against a daemon they
never started by hand. And **`restart` is the answer to an upgrade**: a running
daemon serves the routes it was *built* with, `EnsureRunning` reuses a live one,
and there is no version handshake — so a new operation returns 404 from a daemon
that is behaving perfectly. `status` reports that case when it can tell, by
comparing the daemon binary's mtime against the pidfile's; it is a heuristic and
says so, because the daemon serves no version of its own to ask.

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

   **Git inside the Job is READ-ONLY, and it has to be mounted for it to work at
   all.** A linked worktree's `.git` is a one-line file naming an absolute *host*
   path, so mounting only the worktree gives the container a checkout where every
   git command is fatal — which is worse than no git, because the checkout looks
   like a repository and an agent that opens it concludes the task is impossible.
   That is not hypothetical: a Job hit it, reported `fatal: not a git repository:
   <host path>`, wrote nothing, and was rejected on the null-agent floor. The
   launch now mounts the repository's common `.git` **read-only** at `/gitcommon`
   and shadows `/workspace/.git` with a pointer naming it (`core/gitworktree.go`).
   Read-only is the posture, not an accident: this is the developer's real object
   store and refs, and `log`/`diff`/`status`/`show`/`blame` all work without any
   of it being writable by an agent acting on an objective the plane treats as
   untrusted. The Job is told so in its prompt, because an unexplained permission
   error is the same failure one step later.
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
  injectable `SessionObserver`) is captured, its worktree reclaimed, and the
  **Job failed** — but its **Task is returned to `rejected`, not to a terminal
  state**. The two entities answer different questions: the attempt really is
  over and nothing will resume it, while nobody has grounds to say the
  *objective* is finished, because no artifact was ever examined and no verdict
  reached. A daemon restarted mid-run reads exactly like a dead session, and so
  does a container removed by hand. `rejected` is the state the retry ladder
  already understands, so `task dispatch` / `task retry` remain available.
  (Before Sprint 65 both went to `failed`, which is terminal: a liveness reading
  that could be wrong destroyed the Task, its budget, and every recovery command
  at once.) The attempt is still charged against `max-attempts` — the plane
  cannot tell a Job killed by a daemon bounce from one that died on its own, and
  refunding on an uncertain reading would be the worse error.
  If liveness can't be verified (no observer / an error), the Job is
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

`checks` are the commands the clean verifier runs; `acceptanceGlobs` are the
paths that make up the **oracle**, restored to their base state before grading.
`ReadAcceptancePolicy` reads it from a checkout; a project that declares none gets
a built-in default (`daedalus docs lint` — daedalus is language-agnostic and
cannot know a project's build/test command, so those are declared per-project —
plus the conventional test/fixture globs and the verify config itself).

**A glob should name a file that encodes the requirement independently of the
work**, which is narrower than "a file the checks read". `go test ./...` makes
`**/*_test.go` an oracle: the test states the requirement. `daedalus docs lint`
does *not* make ROADMAP.md one, even though that is exactly what it reads — the
requirement lives in the linter, and a Job making the documents well-formed is
complying with the check rather than evading it. Freezing them would restore away
the work and leave the check grading the base's documents, which is a green
verdict about nothing.

Getting this wrong is cheap in one direction and quiet in the other: freezing
files no check reads protects nothing and discards work at grade time, while
leaving a genuine oracle unfrozen lets a Job rewrite what it is being asked to do.
Daedalus's own policy did the first (it froze `**/*_test.go` while running only
`docs lint`) and `TestOwnAcceptancePolicy_GlobsMatchTheChecks` now derives both
rules from the declared checks, so the two cannot drift apart silently again.

**Per-task checks (post-v0.53.0).** The policy above is project-level and
task-independent: it answers *"does this artifact still meet the project's
standing bar"*, and cannot answer *"did this task deliver what it promised"* —
it was committed before the task existed. `task create --check <cmd>` (repeatable)
records commands on the Task row that are **appended** to the frozen policy at
verify, never substituted for it, and run **after** it. Three properties, each
chosen so a per-task check cannot become a loophole:

- *Append-only* means a Task can only ever raise its own bar; there is no request
  shape that lowers it.
- *Project checks run first* matters because the verifier's checkout is writable
  and checks run in sequence — so by the time a task check runs, the project's own
  checks have already passed against an unmutated tree. A task check can only
  sabotage itself.
- *Human callers only* (`resolveTaskChecks`): a check is a command executed inside
  the verifier, and the party being graded does not choose those. An agent is
  refused with a typed `forbidden`. This is stricter than the append-only property
  strictly requires, and deliberately so — every consequential capability here
  starts human-only until something earns otherwise.

They sit **outside** `acceptance_hash`, which covers the project's policy alone,
so a Task carrying checks does not read as policy drift.

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
3. **The oracle is restored, not defended.** `AcceptanceFileChanges(base..head,
   globs)` lists every frozen acceptance file the Job touched, and the verifier
   puts each one back to its **base** state inside the clean checkout before a
   single check runs: edited and deleted files are restored, added ones removed.
   The artifact is therefore graded by the oracle as frozen, and an edit to it
   **cannot influence the verdict** — you cannot grade your own exam because your
   answers to it are not the ones marked.

   This *replaced a refusal* (M22, after T-17). The gate used to reject any Job
   whose diff touched an acceptance file. The rule was right and the enforcement
   was reading a diff, which cannot tell *"added the test that pins this fix"*
   from *"deleted the assertion that was failing"* — they are the same operation
   to anything reading file names. So it refused both, and a repository whose
   practice is to land a change with its test could not use the plane at all,
   while a determined agent lost nothing it could not have got by simply not
   touching the tests. Making the edit **ineffective** is the same protection
   without the collateral refusal. Added files are removed rather than kept
   because "add a file that changes how the suite runs" is a real move: a Go
   `TestMain` that exits 0, a `conftest.py`, a jest setup file.

   The paths are still **reported** — on a pass as well as a rejection — because a
   human deciding should know the change rewrites part of the oracle, and will do
   so from the next base onward.
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
| **wall-clock** (per Job) | **Enforced plane-side — it is not a kill** | the plane races the runner against a deadline context; an overrun is `execution_result=timeout` and the Job/Task **rows** go terminal, on time, whether or not the runner cooperates. What is enforced is the plane's own bookkeeping plus a cancellation *request*; the process keeps running if it ignores that request. See the honest limit below |
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

**Honest limit: the wall-clock budget is not a kill.** The plane guarantees its
own verdict and cancels the Job's context; it cannot guarantee the death of a
process it did not fork. A runner that ignores its context keeps running in the
background until it exits, so the budget bounds *how long the plane waits and
what it records*, never *how long the work runs*. Killing the underlying
container needs a context-honouring runner — the real `CoordinatorRunner` is
`exec`-based and does not abort a command mid-flight today.

This is stated twice, in the table and here, because it was previously stated
*inconsistently*: the code comment above `runUnderWallClock` called this the
first "strongly enforceable" axis and said the Job "is terminated" two lines
above the paragraph explaining that nothing is terminated (corrected in Sprint
64). The design note the phrase came from
([`guild-master-plan.md`](guild-master-plan.md) §6) is intent; this document is
as-built, and where they differ this one is right. Real termination — a persisted
execution handle with an idempotent `Stop`/`Kill`, with capacity released only on
*confirmed* death — is [BACKLOG #69](../BACKLOG.md), not a property to imply
before it exists.

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
| `branch_not_advanced` | refusal | an adoption could not wind a project's checkout branch forward — a dirty tree, a detached HEAD, or a diverged branch; nothing was touched |
| `stale_base` | verdict | the candidate's `base_sha` is no longer the project's target tip |
| `null_agent_floor` | verdict | `head_sha == base_sha` — an empty change |
| `policy_drift` | verdict | the acceptance policy at `base_sha` no longer hashes to the frozen value |
| `integrity_gate` | verdict | the frozen oracle could not be established, so nothing was graded |
| `verify_failed` | verdict | the clean verifier ran and reported failure |

**Stale base.** An artifact built on a base the plane has moved past proves
something about a tree nobody will integrate, so it is rejected **before** the
the oracle restoration or the verifier — a doomed artifact never costs a verifier
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

### Re-verification — when the verdict was wrong, not the work (Sprint 65 / M19 — built)

```text
rejected ──reverify──→ candidate → verifying → …   (the SAME artifact, no new Job)
```

Retry and replan both answer "the artifact was wrong". Neither answers "the
*grading* was wrong", and that gap used to cost an attempt: an operator whose
verifier never ran its check, or whose acceptance policy failed on an advisory
finding, had to dispatch a fresh Job and discard an artifact that was never in
question.

Verification is a function of `(artifact, policy, environment)`, and the artifact
is immutable and content-addressed — rejection removes a Job's worktree but never
its branch, so the commit stays reachable. Re-grading it is therefore cheap and
repeatable. `daedalus task reverify <id>` returns the Task to `candidate` and
lets the **one** verification path grade it again, with every gate intact. There
is deliberately no second grading path: two would drift, and the weaker one would
become the oracle.

Two modes, because they have different trust properties:

| Mode | What changed | Review cycle | Trust |
|---|---|---|---|
| `reverify <id>` (replay) | nothing — same artifact, same frozen policy | **not charged** | none at stake: the policy is the one the artifact already faced |
| `reverify <id> --amended` | the Task is re-pinned to the project tip and the policy re-frozen there | **charged** | a real grading under an oracle the artifact did *not* face |

The discount is the same principle `CountReviewCycles` already applied to an
interrupted verification: entering `verifying` is not being verified, and a
verdict from a verifier that never ran its check examined the artifact no more
than a crashed one did. A defect in our own harness must not spend the operator's
budget. Re-verification creates no Job, so it can consume no *attempt* either —
that falls out of `Attempt = CountJobsForTask + 1` rather than being enforced.

**What it must never become is an appeal.** Two rejections are refused outright
(`unappealable`): an **integrity** rejection and the **null-agent floor**. An
integrity rejection means the frozen oracle could not be established, so nothing
was graded — re-grading would produce the same nothing. The null-agent floor
means there is no change to grade, and no number of re-gradings will make one. `verify_failed` *is* appealable, because from outside it cannot be told
apart from a broken oracle; the answer to that ambiguity is that the operation is
tiered (agents propose, humans confirm) and every re-grading is recorded with the
verdict it set aside.

Under `--amended` the policy lineage (old hash → new hash) is written to the
event log, because a verdict produced under a policy amended *after* the artifact
existed is weaker than one produced under the policy the artifact faced, and the
log is the only place that difference survives.

One subtlety worth stating, because it was a latent bug the feature exposed: the
integrity gate and the null-agent floor measure against the **Job's** base, not
the Task's. They ask what *this Job did*, and a Job's diff is defined relative to
the commit it was checked out at. The two are normally the same value — only
`--amended` re-pins a Task while keeping an existing artifact — but against the
Task's base the diff would describe the divergence between two trees rather than
the Job, and every file the new base added would read as a file the Job deleted.
An amended re-grade whose corrected policy was itself `.daedalus/verify.json`
would then trip the gate on the very commit that fixed the oracle.

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

1. **Adoption is trust-on-first-use, and it is a command, not a side effect of a
   read.** A repository with no target yet takes the operator's checkout `HEAD`,
   once, at the first Task for it — before any Job for that repository has run
   under the plane. The plane cannot invent a trusted starting commit; it can only
   refuse to keep taking new ones. Adoption happens **only** when the target
   genuinely does not exist: any other failure reading it is surfaced, never
   treated as "there isn't one", because that fallback reads the worker-writable
   checkout `HEAD`.

   Reading and adopting are **two operations**
   (`CONTRIBUTING.md` § Command-Query Separation):

   | | Operation | Effect |
   |---|---|---|
   | `Service.Target(project)` | query | reads the target; returns `ErrNotFound` when there is none. **Writes nothing.** |
   | `Service.ensureTarget(project)` | command | adopts `HEAD` if and only if there is no target. Idempotent, unexported, called from `CreateTask` and nowhere else. |

   They were one function until this split — a query by name and type that wrote a
   database row and a git ref on first call. That mattered more here than it would
   almost anywhere else, because this read decides which commit the acceptance
   oracle is frozen at: every caller that merely wanted to *know* the target was
   one missing row away from *creating* one out of the worker-writable checkout,
   on a retry, during a re-verification, or in the middle of landing code.
   Outside task creation, **a missing target is a fault, not a cue to adopt** —
   `retry --rebase`, `integrate` and the staleness check all now fail rather than
   invent one.
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

### Adopting a landing into a checkout

The target is deliberately not a branch, so **a landing moves nobody's branch**.
That is the property this whole design rests on, and it is also why "I integrated
it and my repository looks untouched" is the first honest reaction to a successful
landing. `integrate --into-branch` opts into a guarded fast-forward at the moment
of landing; `GET /adoptions` and `POST /adoptions/{project}` are the same courtesy
**after the fact**, which is when most people want it.

The unit is a **project**, not a task. A branch does not lag by a task, it lags by
a commit: six landings onto one target leave one fast-forward to make, so there is
one row, one action, and one refusal to reason about. `GET /adoptions` answers per
project — the branch that would move, the landed commit, how far behind it is, the
tasks whose work is waiting in it, and a sentence saying all of that in words. A
project whose branch already **has** the landed commit (at it, or ahead of it) is
reported as having nothing to adopt rather than being offered an action that would
do nothing, and adopting one that is already there is a **success**, not an error.

`POST /adoptions/{project}` calls the same `advanceCheckoutBranch` the
`--into-branch` flag calls — not a copy of it — so its refusals hold unchanged:
fast-forward only, refused on a dirty tree, refused on a detached HEAD, never a
force. A refusal is `branch_not_advanced` (422) carrying that function's own note,
because the note is the part the operator needs, and it is filled on every path
including success for the same reason. Every adoption, refusals included, is
recorded against the project.

Agent callers are **proposal-tier** (`adopt_landed`). Everything else the plane
does changes plane state; this writes to the working tree a person is sitting in,
which is a larger blast radius rather than a smaller one, and precisely what a
poisoned project document would reach for. Reading which projects are behind is
free: an agent that can see the gap can explain why the human it reports to cannot
find the work it landed.

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

### The reviewer, and what `verified` is worth (M20, Sprint 67)

> Verification runbooks: [`m21-m22-verification.md`](m21-m22-verification.md) for
> the programme surface and the divergence report, and
> [`m20-verification.md`](m20-verification.md) — what is
> proven, what is only argued for, and `scripts/verify-m20.sh` (19 assertions, no
> Docker) for the half a host is not needed to check.

**A reviewer now exists, and it reports rather than gates.** `AgentReviewer` runs
a separate agent over a clean checkout of the artifact, handed the diff, the
objective, the **rationale** and the **programme** — and it writes a judgement:
a verdict, its reasoning, and findings that each carry a location and a reason.
Judgements are recorded in a `reviews` table, accumulate rather than overwrite,
and ride on `StatusView` so they are in front of whoever is deciding.

**Three things make it independent**, and they are separate: a fresh checkout of
the commit rather than the Job's worktree; its own throwaway project and home, so
it cannot read the Job's transcript — a reviewer that can see how the work was
argued for is being lobbied by it — and a diff computed by the plane rather than
derived by the reviewer.

**What it costs, stated rather than absorbed:** unlike the verifier this container
has the **network and credentials**. It must; it is a language model making a
call. So the clean-room property does not hold here, and the compensating control
is that its output is advisory.

**Why advisory is the design and not a hedge.** A verifier runs a frozen,
human-authored command and returns an exit code. A reviewer is a model reading a
diff it did not write — untrusted input, by construction. Two consequences point
the same way: a verdict that moved plane state would be an oracle nobody bounded,
and a PASS that carried authority would be the lethal trifecta with the parts
relabelled, since the diff can address the reviewer directly. So `ReviewTask`
records everything and transitions nothing, `requireReviewPassed` is a no-op kept
under its old name so the decision is visible where it used to be enforced, and a
harness failure comes back as *no judgement* rather than as disapproval — because
"the reviewer could not be made to report" and "the reviewer disliked this" are
different facts.

#### The first real reading (2026-08-20)

`AgentReviewer` ran for the first time on a host, against **T-14** — the Task
fixing the very `docs lint` errors that had rejected T-13 — and produced `RV-1`
on artifact `A-8`: a pass, recorded, with the Task moving nowhere. The operator's
verdict on the judgement itself was that it **looked good**.

That is one data point and it is worth exactly one data point, but it is the only
evidence that exists about whether an agent reviewer earns its place, so it is
written down rather than left in a terminal. What it establishes: the host-only
path works end to end — a separate container, its own throwaway project, a clean
checkout, the diff and the objective in the prompt, a judgement file the plane
could read back. None of that had ever run before.

What it does **not** establish is whether the reading is good enough to lean on.
That needs several, on work of varying quality, and the honest bar is in
`ReviewPrompt`: does a finding name something you would have to open the file to
know, does it say *why* it matters, does it judge against the Task's objective
rather than drifting into generic code review, and does it notice what is
**missing** rather than only what is present. `ReviewPrompt` is pure and exported
so it can be tuned against those answers without touching the plane.

**Three defects were found getting to that first reading, and none of them were
the reviewer.** The rung was gated behind the machine oracle, so it could not run
on a rejected Task — the case it exists for. The Ledger's message for a review
still spoke the pre-M20 vocabulary and reported a real judgement as a bland
half-sentence. And the entry window fetched its detail once per selection and
never again, so a review run from a terminal never appeared at all. A subsystem
whose only product is a report failed three times at reporting, which is worth
recording as the pattern it is.

#### The second reading, and what it settles (2026-08-20)

`RV-2` on **T-13** — the repository split — is the case that makes the argument
the first reading could not. Its blocking finding, in the operator's paraphrase:

> Neither half of the task landed. Part A was built but never pushed, so the new
> repository does not exist; Part B was correctly not started, so `langlearn/` is
> still in snowball and every file that referenced it is unchanged. **The entire
> deliverable is a patch file plus a plan.**

Put beside what the machine oracle said about the same artifact, this is the
whole milestone in two lines:

| | Verdict on T-13 |
|---|---|
| The oracle (`daedalus docs lint`) | rejected it — for pre-existing errors in `SPRINTS.md`, **a file the Task never opened** |
| The reviewer | rejected it — because *the deliverable is a plan, not the change* |

The oracle was not merely unhelpful, it was **actively misleading**: its rejection
pointed at documentation, and an operator acting on it would have fixed the
documentation and retried into the same wall. The real defect — that the Job
produced instructions for a human rather than the work — was invisible to any
exit code, and was found in one reading.

That is the evidence for the rung. Not "the judgement looked good", but: it saw
the thing the gate could not see, and the thing it saw was the one that mattered.

It also found the cause the plane could not: the Job could not push, because a
Job container had no credentials for a git remote. That is **#83**, filed and
fixed the same day — opt-in, per project, in the host-side governance file.

#### What `verified` is worth

**It means "the plane applied what checks it could" — no more.** The honest tally,
over the machine oracle's entire history to 2026-08-20:

| Verdict | What it was actually about |
|---|---|
| pre-`72b2108` | the `daedalus` CLI was not in the image |
| pre-`c21b75a` | the check never reached a shell; **every green verify before this was vacuous** |
| T-8 | `--ci` turning a deliberate roadmap warning fatal |
| T-8 (reverify) | ✅ the work |
| T-10 | a check string containing a newline, run as two commands |
| T-11 | git fatally broken in the Job container |
| T-13 | pre-existing `SPRINTS.md` errors in a file the Task never opened |

One verdict in seven was a statement about the work being graded. The cause is
not a run of bugs: the verifier runs `--network none` with only the checkout
mounted and no dependency cache, so for any project that is not fully vendored it
**cannot run the real build or tests** (backlog #74) and falls back to the one
check that always works, `daedalus docs lint` — which grades documents. Until #74
is closed, treat the gate as advice with a state machine attached.

That is why `verify --ignore-result` exists, and since Sprint 67 the waiver
actually leads somewhere: a waived Job can be approved **and landed**, with the
merged re-verification's failure carried on the same recorded waiver rather than
refusing a second time for the same reason. Waive the consequence, never the
truth — the artifact still says `verify=fail` and the log still says who carried it.

### Programmes — what the work is for (M20, Sprint 66)

A **Programme** is the shared intent several projects serve, and since Sprint 66
it is a row in `control.db` rather than a JSON file. A Task points at one by ID,
and carries a **rationale** plus the caller class that authored it.

**Why the plane owns it.** Identity. The file-backed store keyed a programme by
its filename, so a rename broke every reference — silently, because nothing held
a reference the store could check. That is the same defect Sprint 59 fixed for the
integration target by keying it to a canonical repo path instead of a name. A
programme now has an ID, the ID is what a Task stores, and the name is free to
change. Definitions written before this are adopted on daemon start, once and
idempotently by name, so nothing anybody wrote is lost and there is no migration
flag to forget.

**Why the rationale carries its author.** `rationale_by` is the caller class,
derived from the socket the request arrived on and never present in the request —
the same rule as `proposals.proposed_by` and `steering.issued_by`. It is what
makes "the rationale is the human's own words" a property you can check rather
than one you hope for: an agent may draft a good reason, and it will read as the
agent's rather than silently as the operator's. An agent-created Task with no
rationale is *visibly* unattributed, which is the honest rendering.

**Two edge kinds, and only one has teeth.** A programme carries `deps` —
project→project ordering imported from the file store — which declare a *plan* and
gate nothing. What blocks a landing is the Task→Task graph (`task_dependencies`,
Sprint 62). `programmes status` rolls that graph up, and reports the part no
per-project view can show: **edges that leave the programme**, i.e. work it waits
on that nobody put in it.

| Operation | Human | Agent |
|---|---|---|
| `programmes list` / `show` / `status` | yes | yes — reading is most of the point |
| `programmes create` / `add-project` / `add-dep` | yes | proposal |
| `programmes remove` | yes | proposal |

Forming a programme stays a human act because it is a statement about what the
work is *for* — the Guild Master is expected to notice common interest across
projects and propose one, and a human agreeing is what turns a noticing into a
programme. Since M21 the agent can also propose an **amendment** and a
**dissolution**, because noticing that a programme has drifted from what it was
formed for is the same act as noticing it should exist.

### The distance between the two graphs (M22, Sprint 70)

**Both graphs stay.** The declared order plans and the Task graph gates, which is
the standard shape — MSP's benefits dependency map plans and the delivery plan
gates. Merging them here would be worse than untidy: an agent that can draft a
plan would gain the power to gate work, which is the authority the proposal tier
exists to withhold.

What was missing is that **nothing ever compared them**. A programme could declare
that `web` follows `api` while no Task edge made any landing wait, and the only
mention of that was a Note printed once, at write time, to whoever already knew. A
declared order nobody checks is a claim that cannot be wrong, which is another way
of saying it cannot be useful.

`ProgrammeStatus` now carries two more findings, printed by `programmes status`,
served over HTTP and rendered in the Ledger:

- **`declared`** — every declared edge with whether the Task graph enforces it,
  and the Task edges that do. An edge onto work **outside** the programme counts:
  it still makes the landing wait, and refusing to count it would report an
  ordering as unenforced while the plane was enforcing it.
- **`undeclared`** — every cross-project Task edge the plan does not mention. The
  more interesting direction: the work found a dependency the plan did not
  anticipate, so either the plan is out of date or the edge is wrong.

An unenforced edge says **why**, because the reasons need different answers: work
open on both sides is a missing declaration, while an empty side is work that does
not exist yet. `programmes status <name> --suggest-deps` prints the exact
`daedalus task depends` commands for the first case. It prints them and runs none
— a dependency edge decides what must happen before a Task is graded, so a tool
that added them from somebody's plan would be writing the enforcing graph on the
wrong authority.

**Per-programme numbers, and what they are not.** `PlaneStatus` reports running
and queued Jobs per programme, so "which shared intent is the machine spending
itself on" is answerable. **This is reporting.** The scheduler admits on the
global and per-project limits and knows nothing about programmes.
Programme-aware admission — fair-share or priority across programmes — waits on
backlog **#70**: capacity lives in an in-memory `waiting` map that a restart
erases, and fairness policy built over something that forgets is worse than none,
because it looks like a guarantee.

### What the person deciding is shown (M21, Sprints 68–69)

The reviewer agent is handed the diff, the objective, the **rationale** and the
**programme**. Until M21 the human at the approval gate was handed an objective
and a base SHA: the Ledger's JavaScript contained no reference to `programme` or
`rationale`, and the approvals payload carried neither field. The party that only
reports was being shown more of the intent than the party with the authority to
act, which makes the rationale a field the system collects and never spends.

Now:

- The **Ledger has a programme view** — what it is for, its projects, the work
  serving it by state, the edges that leave it, and the divergence report above.
  It is **read-only**: forming, amending and dissolving a programme stay at a CLI
  or a confirmed proposal, because a page that could dissolve one between two
  clicks would make the most consequential gesture the easiest to reach.
- The **Task entry and both approval queues** (browser and TUI) carry the
  programme, its description, the rationale and **who authored it**.
- A Task at a gate with **neither** a programme nor a reason says so in a
  sentence. Deciding on the objective alone is a fact about the decision, not an
  empty field to skip past.
- A programme list that cannot be read costs a **name**, never a **row**. An
  approval that vanished because a lookup failed would read as "nothing needs
  you".

### Driving the plane from the Web UI and TUI

Both surfaces are **clients** of `control.sock` with no authority the CLI lacks:
the Web UI's **Ledger** under `/api/control/*`, and `[A]` in the TUI. Neither
**spawns** the control daemon — a dashboard that started one because somebody
opened a tab or pressed a key would be a surprising side effect — and when the
plane is unreachable both say so explicitly rather than rendering an empty queue,
because "nothing needs you" and "I could not ask" are different answers and only
one of them is reassuring.

The TUI is the approval gate alone. The Ledger is the **whole** `TaskAPI`: its
routes mirror the daemon's own one for one, and a test derives that requirement by
reflecting over the interface, so an operation added to the plane and not surfaced
on the page fails the build rather than being discovered by an operator reaching
for it.

**Why a route per operation and not a proxy.** Forwarding `/api/control/*` to the
socket would be a third of the code and fails open in the direction that matters:
every future daemon route would be exposed to the browser the day it was written,
and the caller class would still be human, so "the plane grew an operation" would
silently become "the page can do it". Explicit handlers make that a decision
somebody writes down. What the web layer does NOT do is decide anything — it binds
a body, calls one method, and relays the plane's own status and reason code, using
the daemon's own `StatusFor`. There is no second implementation of any rule.

**This changes what `daedalus web --no-auth` gives away.** It is no longer a
dashboard with two write buttons; it is `daedalus task` over HTTP. It can create
and dispatch Jobs, waive a failing verification, approve an agent's work, land
code and cancel anything running. The handlers are behind the same auth middleware
as everything else and auth is on by default, so the shipped configuration is safe
— but `--no-auth` hands all of it to anyone who can reach the port, and WSL2
auto-detection binds `0.0.0.0`. The approval gate is only as strong as the weakest
surface that can operate it.

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

That last point holds for the whole of M16 — the runner, worktree, verifier and
container paths are untouched. The **state machine is not**: the dependency graph
below adds the `blocked` state, three plane-only edges and a plane-owned table.
"No new execution machinery" is a narrower claim than "nothing structural
changed", and only the narrower one is true.

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

### Fairness needs liveness

A ticket is a **lease**, not a permanent reservation. Fairness without liveness is
a deadlock: a Task refused for capacity keeps its place while sitting in
`planned`, and nothing wakes it — dispatch is synchronous, so the queue would only
advance if a human re-issued dispatch for that exact Task. One abandoned dispatch
attempt would brick a project's parallelism, and an abandoned **global** waiter
would stall every project at once.

So a ticket is renewed each time its owner re-asks, and expires otherwise:

- **passovers** — every time a ticket blocks somebody else, it spends one. A
  ticket that has been passed over more than a few times without its owner
  re-asking is dropped. This heals a *busy* queue in a few attempts, made by the
  very Tasks being blocked.
- **TTL** — a ticket not renewed within a couple of minutes lapses regardless.
  This heals a *quiet* queue, where there are no passovers to spend.

Re-asking renews a ticket **without losing its place**, so a Task that keeps
asking cannot be aged out by its own competitors. The invariant: *free capacity
must eventually become usable without human intervention.*

### What bounds what — containers, not disk

The scheduler bounds **running containers**. It does not bound **disk**, and
lifting the one-Job-per-project invariant made that worth saying out loud.

`candidate`, `verifying` and `rejected` Jobs hold no container, which is why they
are correctly excluded from the running count — but each still holds its
**worktree**, because the commit has to survive for verification, review and
integration. With N Tasks per project there can now be N simultaneous candidate
worktrees, where before there was at most one. Worktrees are reclaimed when a Job
reaches a terminal state or is rejected, and `Reconcile` removes orphans, so this
is accumulation bounded by *unfinished work*, not a leak — but an operator running
wide parallelism on a large repository should expect the disk cost to scale with
the number of Tasks in flight, not with the concurrency limit.

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

## Reconciliation, repaired (Sprint 62 / M16 — built)

Lifting the one-Job-per-project invariant turned two benign defects into serious
ones. Both are repaired in a single reconcile pass that now covers **both
entities** — active Jobs *and* active Tasks.

**Liveness was asking the wrong question, and always had been.** `HasSession`
takes a *project*, but a control-plane Job does not run under its project's name:
the runner launches `daedalus daedalus-job-<jobID> …`, and the coordinator keys
sessions by that name. So the plane asked about `app` while the Job's session was
`daedalus-job-J-7`, and the answer was only accidentally related to the Job being
judged — false while a Job ran happily, true for every Job of a project somebody
had an interactive session open on. Which way it erred depended on unrelated human
activity.

While one Job per project was the rule this was survivable, because the Job *was*
the project's work. With several Jobs sharing a project it became a **capacity
denial-of-service**: a crashed Job stays `working`, so the scheduler counts it,
denies against it, and it holds its worktree — accumulating until the project can
never dispatch again without a human cancelling each ghost by hand.

The fix needed no coordinator change. `JobSessionObserver.HasSessionForJob(jobID)`
asks about `JobProjectName(jobID)` — the key the coordinator has been using since
M13.

**The heuristic, and it is labelled a heuristic.** Where per-Job liveness is
unavailable, reconcile falls back to guessing, from two signals:

- the Job's **worktree is gone** — near-certain: a `working` Job without its
  isolated checkout cannot be producing anything;
- the Job has been `working` **far past its own wall-clock budget** (×2 plus five
  minutes) — the actual guess.

**It cannot distinguish a crashed Job from a slow one whose budget was set too
low.** Both look like "still working long after it should have finished", and the
margin is generosity rather than correctness — it changes how often the guess is
wrong, not whether it can be. It is used because the alternative is worse: a wrong
guess costs one Job that has to be retried, and no guess at all costs the project.
A heuristically-reaped Job says so in its event and in the reconcile report, so an
operator investigating one can tell an inference from an observation. Where a
per-Job observer is available it always wins, so a slow-but-alive Job is never
reaped by the guess.

**A Task can also be wedged with no Job at all.** A crash between the `working`
transition and the Job insert leaves one invisible to a Job-only census: not
dispatchable, retryable or replannable, with only cancel to escape. Reconcile now
sweeps active **Tasks** too and returns such a Task to `rejected` — the state the
retry/replan ladder already understands, so the operator gets the same recovery
vocabulary as for every other failure.

## The cross-project task graph (Sprint 62 / M16 — built)

A Task may depend on other Tasks, in any project. A dependency gates the
dependent at **two** points, and both are needed:

- **Admission.** A `planned` Task with unmet dependencies is **`blocked`**, and
  the scheduler never admits it.
- **Landing.** The integration transaction refuses a Task whose dependencies have
  not landed, with the typed `dependencies_unmet` rejection.

```text
planned ⇄ blocked        (plane-only, both directions)
```

**A dependency is satisfied only when the upstream Task reaches `integrated`** —
the point at which its work is actually in the trunk. Verified or approved is not
enough: the work exists but has not landed, and a dependent built on it would be
building on something that may still be rejected.

**Why landing is gated and not just admission** (Sprint 64). A Task's `base_sha`
is frozen at *creation* and only `retry --rebase` ever moves it, so a dependent
that merely *starts* after its dependency landed still runs against a tree that
predates it — admission ordering alone does not put B's work under A's. The place
the two are genuinely combined is the integration rebase-and-re-verify, which
makes landing the point where "B before A" has content. Until Sprint 64 only
admission was enforced, so an edge declared once a Task had left `planned` was
recorded, shown in `task depends` and on the board, and gated nothing whatsoever.

**Grading is deliberately not gated.** A dependent is still verified against its
own frozen oracle with dependencies outstanding: that verdict is about the
artifact, not about what else must land first, and refusing it would spend a
review cycle to learn nothing.

**An in-flight Task with an unmet dependency is not `blocked`.** That pair of
transitions connects `planned` and `blocked` only; a Task whose worker is
mid-flight claiming to be waiting would be a worse misstatement than the one this
fixes. It carries the unmet edge instead, and meets it at the landing gate.

**A terminal Task cannot acquire a dependency at all.** Once a Task is
`integrated`, `failed`, `cancelled` or `expired` there is no dispatch left to
block and no landing left to gate, and no later event can give the edge force —
so declaring one is refused rather than recorded as decoration.

**Cycles are refused at creation, never detected at dispatch.** A cycle found at
dispatch time is a wedged graph somebody has to unpick; refused at declaration it
is a validation error, caught by whoever wrote it while they still remember why.
`daedalus task depends <id> --on <other>` walks the existing edges first and
refuses anything that would close a loop, naming the path.

**The edge is plane-owned state**, held in `control.db` and never read from a file
in a project checkout. An agent that could declare its own dependencies could
declare them satisfied — and "what must happen before this lands" is exactly
as load-bearing as "what grades it", so a repo-side dependency file would re-open
M15's whole acceptance-oracle argument through a side door. Declaring an edge is
correspondingly a **proposal** for an agent caller, not a direct action.

### When a dependency can never be satisfied

Failure and cancellation are treated differently, deliberately:

| Upstream ends as | Dependents | Why |
|---|---|---|
| `integrated` | woken, become `planned` | the prerequisite landed |
| `failed` | stay `blocked`, marked *unsatisfiable* | the dependency is **permanent** — see below |
| `cancelled` | **cancelled too**, transitively | cancellation is a **decision** that the work will not happen, so leaving dependents waiting forever is the stranding |

**A failed dependency is permanent, and its dependents must be cancelled and
recreated.** This is worth stating flatly, because the obvious assumption is
wrong: `failed` is a terminal state with no outgoing edge, and there is no
operation to remove a dependency edge, so a dependent blocked on failed work
cannot be rescued in place. Marking it *unsatisfiable* rather than cancelling it
is therefore a smaller distinction than it looks — it keeps the dependent visible,
with the reason legible in `task depends`, and leaves the decision to a person
instead of cascading automatically. The manual route reaches the same end as the
cancelled path.

Nothing is silently stranded: the state says `blocked`, the status says
*unsatisfiable*, and the CLI names the upstream and says to cancel or retry as a
new Task. But an operator should know that "retry the failed work and keep the
dependents" is not available. (Removing a dependency edge is tracked as backlog;
it is deliberately not part of this milestone.)

**The landing gate makes this cost more, and that is stated rather than hidden**
(Sprint 64). A dependent that acquired its edge while already in flight can now be
*verified and approved* and still unable to land — and if its upstream then fails,
the only route out is still cancel-and-recreate, discarding work that has already
been graded and signed off. The refusal names the unsatisfiable upstream
specifically, rather than reporting it as "wait a bit longer", because the two need
different actions from an operator. This raises the value of a `RemoveDependency`
operation from convenience to something closer to a missing escape hatch; it stays
in the backlog, now with a sharper reason.

### Waking, and why reconcile does it too

A dependency landing wakes its dependents directly — the fast path. But **a wake
that only ever happens on an event is a wake that is missed when the process dies
mid-event**, and a Task blocked on a dependency that has already landed would then
wait forever. Sprint 61 established the invariant for the scheduler's queue lease:
*free capacity must become usable without human intervention.* The same discipline
applies here, so every reconcile pass re-evaluates every blocked Task. The event
path makes it fast; reconcile makes it eventually true regardless.

### Reading a fair queue

One observation worth recording, because it looks like a bug and is not. When
several Tasks are queued behind a limit, a single pass of retries admits **one**
of them — the oldest — and refuses the rest with `queued_behind`. Sampled once,
that reads as a stall. It is the fairness rule working: successive passes drain
the queue in order. Anyone "fixing" the apparent stall by removing the yield would
be reintroducing starvation.

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
| Consequential | dispatch, retry, replan, cancel, integrate, approve/reject, target resync, declare a dependency (M16), steer / withdraw a steer (M17) | **recorded as a proposal** |
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

## Typed steering (Sprint 63 / M17 — built)

Steering is "tell the worker something else while it is running". Until now the
only way to do it was to attach to a terminal and type, which is unrecorded,
unattributable and unavailable to anything but a human at a keyboard. M17 makes it
a control-plane operation instead: `steer_job(job, instruction)`, recorded as a
row with an issuer, a timestamp and — the part that matters — an explicit
**delivery state**.

### What steering may and may not do

    Steering changes what the worker is TOLD. It never changes what counts as DONE.

A steered Job still reaches `candidate` and is still independently verified
against the acceptance policy frozen at the plane-owned target. M17 adds **no
state, no transition, and no edge** — neither to `legalTransitions` nor to
`workerReachable` — and touches nothing an oracle reads: not the acceptance hash,
not `base_sha`, not the budget, not even the Task's objective. If steering could
influence acceptance, M14's and M15's entire argument would be re-opened through a
new door, and it is exactly the kind of door a "just let the operator nudge it"
feature tends to open. `TestSteering_ChangesNothingThatDecidesDone` and
`TestSteering_AddsNoTransitions` hold it shut.

Note the deliberate omission: steering does **not** rewrite the objective.
`replan` is the operation that rewrites an objective, and it goes through
`rejected` so the change is a decision with a record, not an edit.

### Provenance comes from the transport

The issuer is the Sprint-60 caller class, derived from the socket the request
arrived on, never a request field — a client that can name its own issuer can name
"human". Agent callers are **proposal-tier**: an instruction injected into work
that is already running is at least as consequential as cancelling it, and rather
more subtle, because the Job carries on and the change of direction shows up only
in the log. So a poisoned project document can cause a steering *proposal* to
appear in a human's queue, and nothing else. Withdrawing an instruction is tiered
with issuing it — an agent that could cancel a human's pending steer would have the
same control by subtraction.

### Delivery state, and why it is the honest part

| State | Means |
|---|---|
| `pending` | A runner took custody; the boundary has not arrived. **Not** known to have reached the worker. |
| `delivered` | It reached the Job at a boundary the runner supports. The only state that claims the worker was told. |
| `undeliverable` | It did not reach the worker and will not — no steering boundary, or the handoff failed. |
| `superseded` | A newer instruction replaced it before delivery. |
| `cancelled` | A human withdrew it before delivery. |

A steering op that reported success without delivering would be **worse than one
that refuses**: it leaves an operator believing they redirected a Job that never
heard them, and they then reason from that belief. So `undeliverable` is a
first-class outcome rather than an error swallowed on the way out, the CLI prints
it in red and says "the job was not told anything", and the programme board carries
it on the card.

The brief for this sprint named four states; there are five. `cancelled` is the
addition, because a human withdrawing an instruction and a newer instruction
replacing it are different facts, and collapsing the first into `superseded` would
make the log say something that did not happen.

### The runner seam

Delivery is the **one genuinely runner-specific piece** in the whole control plane
(§9), so it sits behind an optional `SteeringDeliverer` interface exactly as
`AgentRunner` and `VerifyRunner` do. The authority path stays runner-agnostic; a
runner that does not implement the seam is not a broken runner, it is a runner
with no steering boundary, and the plane records that as such. The contract is
narrow on purpose: return `nil` **only** when the instruction reached the Job —
never for "I wrote it somewhere the agent may or may not read", which is the
failure mode the whole design exists to prevent.

**The plane does not trust the adapter to honour its deadline** (Sprint 64). The
delivery call is *raced* against the timeout on a buffered channel, not merely
handed a context — the same shape `runUnderWallClock` uses for the wall-clock
budget, and for the same reason: a context is a request, and an adapter is
runner-specific code the plane cannot vouch for. An adapter that ignores
cancellation would otherwise block its caller forever and the timeout would bound
nothing it claims to bound. The honest limit is the same one the wall-clock budget
carries, and is worth stating rather than implying: this bounds how long the
**plane waits**, not how long the adapter runs. A deliverer that never returns
keeps its goroutine until the process exits. What is guaranteed is that the caller
gets an answer, the row is settled, and `undeliverable` is the truthful record of
an adapter that told us nothing in time. The service lock is not held across the
call, so one wedged adapter costs its own caller and never the whole plane.

**Replacing an instruction is one transaction.** Superseding the pending
instruction and inserting its replacement commit together or not at all. As two
calls it guaranteed there were never *two* pending instructions for a Job by
permitting the opposite: a failure between them left **zero**, silently discarding
a valid instruction the operator believed was still standing. The invariant is now
a property of the transaction rather than of the order of two writes.

### Honest assessment: what steering actually bought

The plan demoted M17 and said live steering should prove its value in real use
before earning a milestone. Having built it, the assessment is:

**For the runner Daedalus actually ships, steering delivers nothing.** Three facts,
in increasing order of how much weight they carry:

1. `daedalus-control` is spawned detached — `Setsid: true` and `cmd.Stdin = nil`
   (`internal/control/bootstrap.go`) — so the daemon that owns steering has no
   stdin of its own to write down.
2. The agent is invoked as `claude --print -p <objective>`: a **one-shot print
   invocation** that takes its prompt from the flag and exits. It does not read
   stdin as a steering channel, so a pipe reaching the process would not be a
   boundary even if one were arranged.
3. Decisively: **`SteeringDeliverer` has no implementation anywhere** — the only
   references outside tests are the interface declaration and the resolver that
   type-asserts against it. `CoordinatorRunner` does not implement it, so every
   steer against the shipped runner records `undeliverable`.

An earlier draft of this section argued the point from "the container is launched
with stdin closed". That was **wrong**, and the correction is worth keeping rather
than quietly deleting: `RealExecutor.Run` sets `cmd.Stdin = os.Stdin`
(`internal/executor/executor.go`), and the container is launched via
`docker compose run --rm` without `-T`, so a stdin pipe does exist structurally all
the way to the agent process. The conclusion survives on (2) and (3); the original
reasoning did not. It was checked by grepping for a line, finding it, and never
asking *which process that line launches* — verifying the fact and not the
inference, which is exactly the failure this review process exists to catch.

This is not a gap a small patch would fill. A steering boundary has to be something
the runner genuinely supports, and adding one means changing how a Job is invoked —
the same decision that keeps the rest of the plane runner-agnostic.

**Against cancel-plus-redispatch, the ledger is honest but thin.** For a short Job,
`daedalus task cancel` followed by `replan --objective` achieves the actual goal —
work proceeding in a new direction — using machinery that already existed, and it
does so *reliably* rather than *maybe*. What M17 adds over that is:

- an **audited record** of the instruction, its issuer and its fate, on the Task's
  event log, which cancel-and-redispatch does not produce and terminal-typing
  cannot;
- a **refusal that is legible**: an operator who tries to steer learns immediately
  that this runner cannot be steered, instead of assuming it worked;
- a **seam** for a future runner that does have a boundary (a `Stop`-hook nudge,
  an interactive coordinator session), so the authority, provenance and tiering do
  not have to be designed again under time pressure when one appears.

That is a real but modest return, and it does not retrospectively justify a
milestone. **The plan's demotion was right.** The correct reading is that M17
should have stayed in the backlog until a runner with a steering boundary existed,
at which point the delivery adapter would have been the only new work.

And there is a fourth item on that ledger, which is really the largest one: **the
honest verdict is itself the deliverable.** The sprint's most valuable output is
not `steer_job` — it is the demonstration, with working code and a runner that
cannot accept it, that the plan's demotion was correct. A judgement made in advance
and then *tested* is worth more than the feature would have been, and it is the
kind of result a project only gets by being willing to write down that it built
something marginal. Anyone reading this arc for guidance should take the demotion,
not the completion, as the lesson.

## The programme board (Sprint 63 / M17 — built)

`daedalus task board`, `GET /board`, a Web panel and a TUI header: one
cross-project view of what is running, queued, blocked (**and on what**), in
verification, awaiting approval, landed, and closed without landing.

It is **derived, not stored**. There is no board table and nothing that another
part of the plane must remember to update — it is a projection of the same rows
the CLI and the event log already read, so it cannot disagree with them. It reuses
rather than duplicates: the approvals queue is Sprint 59's, "blocked on what" is
Sprint 62's `DependencyStatusFor`, the concurrency picture is Sprint 61's
`PlaneStatus`, and the agent-facing projection carries Sprint 60's **opaque queue
ids** and never a host path — so an agent can see which projects will serialize
against each other at integration without learning where anything lives.

Two deliberate details. Every state maps to exactly one column, asserted by a test,
because the failure mode of a projection is that work appears to have *vanished*
rather than to have gone somewhere the reader did not expect. And empty columns are
rendered rather than skipped: "nothing is blocked" is an answer.

## The arc, closed: what M13–M17 guarantee, and what they do not

Five milestones, ten sprints. Collected here so a reader does not have to assemble
the picture from five sprint sections — and, more importantly, so the limits are in
one place rather than scattered where each was honestly disclosed and easily
missed.

### What the control plane guarantees

- **Work is isolated.** Every Job runs in its own Git worktree at a pinned
  `base_sha`, on a deterministic branch. Concurrent Jobs cannot see or corrupt each
  other's trees.
- **"Done" is decided by the plane, never by the worker.** A worker may drive a
  Task at most to `candidate`. Everything past that — verification, review,
  approval, integration — is control-plane-only, enforced by two transition tables
  rather than by convention.
- **The acceptance oracle is outside the agent's reach.** The verify policy is
  hashed and frozen at task creation, at a **plane-owned integration target** that
  only a completed integration transaction advances. Rewriting the repository's
  branches, including by cherry-pick, cannot change how a Task is graded.
  The reason is stronger than "the ancestry check catches it", and this is the M15
  insight worth stating on its own: **the target is not a git ref at all.** It is a
  row in `control.db`. A Job's worktree shares the repository's refs, so *any* ref
  living there is writable by the worker — which means there is no ref an agent
  could rewrite that the oracle is ever read from. The ancestry guard survives as
  defence-in-depth; the absence of a writable ref is the mechanism.
- **Verification is independent.** A clean checkout of the artifact's `head_sha`,
  in a digest-pinned container, with no worker state, no ambient credentials and no
  inherited tools; the frozen acceptance files restored to their base state before
  any check runs, so a Job's edits to the oracle cannot reach the verdict; and a
  null-agent floor that fails an empty change.
- **Landing is a transaction.** Serialize per repository → rebase onto the current
  target → **re-verify the merged result** → compare-and-swap. A semantic conflict
  that passes alone and fails merged is caught. Any step failing leaves the target
  untouched.
- **The plane can say no, in a way a machine can act on.** Typed rejection reasons,
  HTTP 422, CLI exit 3 — a policy refusal is distinguishable from a failure.
- **Capacity is not starved or leaked *where per-Job liveness is available*.**
  Global, per-project and per-Task limits with fair admission (oldest waiter first)
  and queue tickets that expire. The qualifier is load-bearing: without a
  `JobSessionObserver` the plane falls back to a heuristic that may answer "I don't
  know" and leave a Job alone — which is a slot still held. That is the right
  trade (see the limits below), but it means the no-leak property is conditional,
  not absolute.
- **A refusal is never silent.** Every "no" the plane says — over-budget,
  scheduler-saturated, queued-behind, dependencies-unmet, forbidden,
  proposal-recorded, not-steerable, unsafe-rebase — carries a typed machine-readable
  reason *and* appends an event row, and reaches a client as HTTP 422 / CLI exit 3.
  Five audits across this arc looked for a request that was dropped without a
  reason and found none. That is a stronger claim than "the plane can say no": a
  plane that only ever said "error" would be indistinguishable from a broken one,
  and one that sometimes said nothing at all would be worse than either.
- **Recovery is unattended.** Every wedge this arc has found is repaired by
  `Reconcile` without a human: a Task stranded in `verifying` by an interrupted
  verifier, a `working` Task with no Job at all, a crashed Job holding a scheduler
  slot and a worktree, an abandoned queue ticket blocking every waiter behind it,
  and a dependency wake lost to a process death. Sprint 58 alone shipped three
  separate permanent wedges whose only escape was `cancel`; none of them survives.
- **Dependencies are plane-owned and acyclic.** Cross-project Task→Task edges live
  in `control.db`, never in an agent-writable file; cycles are refused at
  declaration; a dependency counts as satisfied only at `integrated`.
- **Agent authority is structural.** Caller class comes from which socket accepted
  the connection. Reads and bounded creation execute; cancel, dispatch, retry,
  replan, integrate, approve, sync-target, add-dependency and steer come back as
  proposals a human must confirm. A poisoned README can put a row in a queue and
  nothing more.
- **History is append-only through the API.** Every transition, budget decision,
  rejection, verdict, approval, proposal, schedule decision, graph move and steering
  outcome is a typed event, written in the same transaction as the change.

### What it explicitly does not guarantee

- **"Verified" is evidence, not proof.** Tests are an incomplete oracle. A change
  can pass every frozen check and still be wrong; restoring the oracle stops a
  worker's edits to the checks from counting, it does not make the checks
  sufficient.
- **The event log is control-plane-managed, not tamper-proof.** There is no
  update or delete path in the API — that is what "immutable" means here. Anyone
  with the SQLite file can edit it. Hash-chaining remains an optional later
  property, and calling this "audit-proof" would be a lie.
- **Turn, token and cost budgets are policy, not enforcement.** They are recorded
  and honoured by convention. Daedalus cannot measure them, so it does not claim
  to enforce them. Wall-clock, max-attempts, max-review-cycles and concurrency
  *are* enforced.
- **Wall-clock is bookkeeping at the boundary, not a process kill.** The plane
  bounds the run it supervises and records `timeout`; it does not reach inside a
  container and stop a runaway mid-turn. Nothing in this design can pause a token
  stream, and §3 says so deliberately: the answer is structural verification of a
  committed artifact, not mid-turn control.
- **Liveness is heuristic where no per-Job observer exists.** `HasSessionForJob` is
  the real answer; without it the plane falls back to "worktree gone, or long past
  its budget", which cannot distinguish a crashed Job from a slow one. It is
  labelled a heuristic in the code, and "I don't know" leaves the Job alone.
- **The caller is a class, not an identity.** Two agent clients on the restricted
  socket are indistinguishable, and anything already running as the operator can
  open the human socket. The boundary is the container's mount namespace, not the
  socket's file mode.
- **Steering delivery is not guaranteed, and today is not available at all.** See
  the honest assessment above.
- **Nothing lands by itself.** Integration is always an explicit act. The plane
  never advances a target on its own.
- **The real runner and the real verifier container are host-only.** Both sit
  behind injectable seams and are proven in tests with fakes; the container paths
  are exercised on a real host by `scripts/verify-m13.sh`, not in CI.

### The one-line version

The control plane makes *reliable, isolated, reproducibly-verified, independently
approved* execution the default, and is honest about the four places it cannot see:
inside a running turn, inside the tests' blind spots, inside the database file, and
behind a caller's class.

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
- **Steering has no delivery path on the shipped runner.** `steer_job` is built,
  typed and audited (M17), but `CoordinatorRunner` has no steering boundary, so
  every instruction against it is recorded `undeliverable`. Cancel-and-redispatch
  remains the working remedy. See the honest assessment above.
- **Dependency scheduling is not a scheduler.** The graph decides *whether* a Task
  may run; it does not order or prioritise ready Tasks beyond the queue's
  first-asked-first-served fairness. There is no critical-path or priority notion.
- **The caller class is a class, not an identity.** Two agent clients on the
  restricted socket are indistinguishable from each other, and anything already
  running as the operator can open either socket. The boundary is the container's
  mount namespace, not the socket's file mode.
- **`task dispatch` runs its attempt synchronously.** Several dispatches may be in
  flight at once (Sprint 61), but each call blocks until its own Job ends.
- **The board is uncached, and it is the most expensive read.** Rendering it
  resolves every registered project's canonical repository path — a `git rev-parse`
  per project — plus a dependency query per Task. The Web panel therefore polls it
  more slowly than the approvals queue. A TTL cache would fix it, and is not built
  because a stale queue identity is a *wrong* answer about which work serializes,
  which is worse than a slow one.
- **The real `CoordinatorRunner` is host-only.** It needs a Docker daemon and is
  not exercised in CI; the control-plane logic is proven with the fake runner.

This section used to close by saying that because there was still no agent client,
the prompt-injection surface did not exist. That was true when written and stopped
being true in Sprint 60: `guild-control-mcp` exists, so the surface exists. It is
answered by tiered authority and the human-confirmed proposal flow rather than by
absence — corrected here rather than left standing as a guarantee the plane no
longer makes.

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
