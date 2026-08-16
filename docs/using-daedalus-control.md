# Using Daedalus: the Guild Master and the Control Plane

A practical guide to the two capabilities added over Milestones 12–18. It is
task-oriented: what to type, what happens, and — where it matters — what does
*not* happen. The design rationale lives in
[`control-plane.md`](control-plane.md) (as-built) and
[`guild-master-plan.md`](guild-master-plan.md) (design intent); this document is
the one to read first if you just want to use the thing.

Everything below is the state of `development` at **v0.53.0**.

---

## The two pieces, and how they differ

Daedalus grew two separate capabilities, and confusing them is the fastest way to
be disappointed by either.

| | **Guild Master** (M12) | **Control plane** (M13–M18) |
|---|---|---|
| What it is | A built-in project whose agent can *read* every other project | A host-side daemon that runs, grades and lands work |
| What it has | **Visibility** | **Authority** |
| Runs where | In a container, like any project | On the host, outside every container |
| Can it approve its own work? | It has no work to approve | No — approval is human-only, structurally |
| Talk to it with | `daedalus guild-master` | `daedalus task …` |

The intended end state is the two combined: the Guild Master proposes, the plane
adjudicates and executes. **That link is not live yet** — see
[The Guild Master as a control-plane client](#the-guild-master-as-a-control-plane-client),
which says exactly what is missing. Today you drive the control plane yourself,
from the CLI, and that is the deterministic reference path it was built around.

---

## Part 1 — The control plane

### What it does for you

You describe a **Task** ("make the API paginate"). The plane runs it as a **Job**
— one attempt, in an isolated Git worktree checked out clean at a frozen commit —
and the Job's committed result becomes an **Artifact**. The plane then verifies
that artifact *itself*, in a clean container, against a policy the worker could
not edit, and only then does a human approve it and let it land.

The point is the shape of the trust: the agent never gets to say "done".

```
task create ──► planned
   dispatch ──► queued ──► working ──► candidate      (worker's ceiling)
    verify  ──► verifying ──► verified | rejected     (plane only)
   approve  ──► approval_required ──► approved        (human only)
 integrate  ──► integrated                            (rebase → re-verify → CAS)
```

### Prerequisites

- **The project is registered and is a Git repository.** Orchestration is
  Git-native; `task create` captures a `base_sha`.
- **Docker**, for real Jobs and real verification. Without it you can still
  exercise the whole plane with the stubs — see [Without Docker](#without-docker).
- **A verify policy**, ideally. Without one you get a built-in default that only
  runs `daedalus docs lint --ci`, which is a weak oracle for most projects.
- **The daemon.** You do not have to start it: the CLI auto-spawns
  `daedalus-control` and reuses it (single writer over
  `<data-dir>/.daedalus/control.sock`).

### The five-minute walkthrough

```bash
# 1. Describe the work. base_sha, the acceptance policy and the budget are all
#    frozen right here — later edits to any of them do not change this task.
daedalus task create --project my-app \
  --objective "Add cursor pagination to GET /items" \
  --wall-clock 1800 --max-attempts 2

# 2. Run one attempt. Isolated worktree at base_sha, headless agent, process exit
#    is the boundary. Only a successful exit promotes the commit to a candidate.
daedalus task dispatch T-7

# 3. Watch it.
daedalus task status T-7        # budget, jobs, artifacts
daedalus task board                # everything, across every project
daedalus task events T-7        # the append-only decision log

# 4. Grade it. Integrity gate → clean verifier container → verified | rejected.
daedalus task verify T-7

# 5. A human decides. (Optional: `daedalus task review T-7` first, for an
#    independent reviewer pass over the verified artifact.)
daedalus task approvals            # what is waiting on you
daedalus task approve T-7 --note "reviewed the cursor encoding"

# 6. Land it: serialize → rebase onto the current target → re-verify the MERGED
#    result → compare-and-swap the target ref.
daedalus task integrate T-7
```

`daedalus task` with no arguments prints the full command list; every subcommand
prints the next sensible command when it finishes, so you can follow the pipeline
without this page open.

### Writing an acceptance policy

Commit `.daedalus/verify.json` to the project:

```json
{
  "checks": [
    "go build ./...",
    "go test ./...",
    "daedalus docs lint --ci"
  ],
  "acceptanceGlobs": [
    "**/*_test.go",
    "testdata/**",
    ".daedalus/verify.json"
  ]
}
```

- **`checks`** are what the clean verifier runs, in order, in a container built
  from the project image **pinned by `sha256:` digest** — not your dev
  environment: `--network none`, no credentials, no `/opt/tools`, nothing mounted
  but the checkout. If a check needs the network, it will fail here, and that is
  the design rather than a bug to work around.
- **`acceptanceGlobs`** are the files a Job may not touch. If the Job's diff
  edits any of them, the Task is **rejected before the verifier is ever called** —
  you cannot grade your own exam. Frontier agents edit tests to pass in a large
  fraction of adversarial runs; this is the gate for that.

**The policy is frozen at `base_sha`**, read with `git show <base>:.daedalus/verify.json`.
Editing it in the working tree does not affect a task already created.

**So how do you legitimately change tests?** Not inside a Task — the gate will
reject it, correctly. Commit the test change yourself (or land it as its own Task
whose globs do not cover it), then create the next Task: it freezes the *new*
policy at its own `base_sha`. If you need an existing task to pick up a moved
tip, `daedalus task retry <id> --rebase` re-pins and re-freezes.

### Budgets

Defaults are `wall-clock=3600s, attempts=3, review-cycles=3, concurrency=1`.
Raise a ceiling in a **host-side** file — never in the repository, because a
budget an agent can commit is a ceiling an agent can raise:

```jsonc
// <data-dir>/control/budgets.json
{
  "default":  {"wallClockSeconds": 1800, "maxAttempts": 2},
  "projects": {"big-app": {"wallClockSeconds": 7200, "maxAttempts": 5}}
}
```

It is re-read per lookup (no restart needed) and **fails closed**: an unreadable
or half-written file reuses the last good policy rather than widening to the
built-in default.

`task create` flags may only ever **narrow** the project's ceiling. Asking for
more is refused with `over_budget`, and refusals exit **3** — distinct from 1 for
failure, so scripts can tell "policy said no" from "it broke".

What is actually enforced:

| Axis | Enforced? |
|---|---|
| wall-clock, max-attempts, max-review-cycles, concurrency | Yes, host-side |
| turns, tokens, cost | **No** — recorded and shown, never enforced. Daedalus takes process exit as the Job boundary and cannot see an agent's token accounting |

And one honest caveat worth internalising before you rely on it: **the wall-clock
budget is not a kill.** It bounds how long the plane waits and settles the Job on
time; a runner that ignores its context keeps running. See
[Limits you should know about](#limits-you-should-know-about).

### When it says no

Refusals are typed, so you can act on them without reading prose:

| Reason | What happened | What to do |
|---|---|---|
| `over_budget` | You asked to widen a ceiling | Edit `budgets.json`, or ask for less |
| stale base | The target moved under you | `task retry <id> --rebase` |
| integrity gate | The diff touched a frozen acceptance file | Land the test change separately |
| empty change | `head_sha == base_sha` — the Job did nothing | `retry`, or `replan` with a clearer objective |
| `dependencies_unmet` | An upstream Task has not landed | Land it, or drop the edge |
| `proposal_recorded` | An **agent** asked for something consequential | `task proposals confirm <id>` — as a human |

Three recovery verbs, and they mean different things:

- **`retry <id>`** — same objective, fresh Job, attempt counter advances.
- **`retry <id> --rebase`** — same, but re-pinned to the project tip; this
  re-freezes the acceptance policy, so use it deliberately.
- **`replan <id> --objective "…"`** — back to `planned` with a new objective, for
  when the task was wrong rather than the attempt.

### Dependencies across projects

```bash
daedalus task depends T-8 --on T-7   # declare
daedalus task depends T-8               # show
```

A Task with unmet dependencies is `blocked` and is never dispatched. Cycles are
refused at declaration, not discovered at dispatch.

**New in v0.53.0:** the graph also gates **landing**. Previously an edge declared
after a Task left `planned` was recorded, displayed — and enforced nowhere, so a
dependent could verify and land ahead of its dependency. Now `integrate` refuses
with `dependencies_unmet` until the upstream has landed. Grading is deliberately
*not* gated: verifying an artifact against its own frozen oracle says nothing
about what else must land.

Two consequences to plan around:

- A Task can be **verified and approved and still unable to land**. That is
  correct, and it is visible on the board.
- If an upstream Task **fails**, its dependents are stuck permanently: `failed` is
  terminal and there is no `RemoveDependency` yet (backlog #68). The only escape
  is to cancel the dependent and recreate it, discarding work already graded.
  Declare edges deliberately.

### Running several at once

Jobs run concurrently, bounded by the tightest of: the global limit, the
per-project limit, and the Task's own `concurrency` budget. Admission is fair —
an older Task cannot be starved by a newer one — and each decision is a typed
event rather than a silent drop.

```bash
daedalus task board     # running · queued · blocked (and on what) · verifying ·
                        # awaiting approval · landed, across every project
```

One reading tip that has confused people (including the authors): **a fair queue
drains over successive passes**, so sampling a single pass can look like a stall.
Look at the events, not one snapshot.

### Steering a running Job

```bash
daedalus task steer J-12 --instruction "prefer the existing pagination helper"
daedalus task steer J-12                      # show instructions and their fate
daedalus task steer --withdraw S-3           # pull one that was not delivered
```

Steering changes **what the worker is told**, never what counts as done: a
steered Job still reaches `candidate` and is still verified against the frozen
oracle.

**Be aware of what you will actually see today.** The runner Daedalus ships has
no steering boundary, so every instruction is recorded **`undeliverable`** — the
worker was *not* told. That is the honest record rather than a success that did
not happen, and the practical remedy for a short Job remains **cancel and
redispatch** (or `replan`). Milestone 17 shipped this and documented that verdict
rather than justifying itself retroactively; the seam is ready for a runner that
has a boundary.

### Approving from the Web UI and the TUI

- **TUI:** `daedalus tui`, then **`A`** (capital, so it cannot be confused with
  `a` = attach) opens the approvals view: `j`/`k` to move, `a`/`Enter` to approve,
  `x` to reject, `r` to refresh, `q`/`Esc` to close.
- **Web:** `daedalus web`, approvals surface at `/api/approvals` with approve and
  reject actions.

Neither UI spawns the daemon; if the plane is not running they say so rather than
starting it behind your back.

> **`--no-auth` now gives away more than a dashboard.** With the control plane
> live, an unauthenticated Web UI is an unauthenticated *approval* surface. Do not
> expose it.

### Where state lives

| Thing | Where |
|---|---|
| Tasks, Jobs, Artifacts, events, proposals | `<data-dir>/control.db` (SQLite, daemon is the only writer) |
| Human socket | `<data-dir>/.daedalus/control.sock` |
| Agent socket | `<data-dir>/.daedalus/control-agent.sock` |
| Budget policy | `<data-dir>/control/budgets.json` |
| Job worktrees | under `<data-dir>/control/` |
| Integration target | a plane-owned ref, keyed by canonical repo path |

`<data-dir>` defaults to `<install-prefix>/.cache`. **Do not hand-edit
`control.db`.** The plane defends against a few hand-edited shapes (a negative
budget is rejected at the row scan, for instance) but it is a store with
invariants, not a document.

The **integration target** is worth understanding: each repository has a target
commit that only a completed integration advances, and tasks are based on it and
graded against a policy frozen at it. Rewriting the repository's own branches
therefore cannot influence how work is graded. `daedalus task target` shows it;
`daedalus task target <project> --sync` resyncs one by hand.

### Without Docker

The whole control plane is host-testable with stubs, which is how it is developed:

```bash
DAEDALUS_CONTROL_FAKE_RUNNER=success daedalus task dispatch T-7   # or =fail
DAEDALUS_CONTROL_FAKE_VERIFY=1       daedalus task verify   T-7
```

The daemon logs a loud warning under either — no real agent runs and no real
verification happens. `scripts/verify-m13.sh` and `scripts/verify-m14.sh` are
Docker-free smokes built on these.

---

## Part 2 — The Guild Master

### What it is

`guild-master` is a built-in project you cannot remove, prune or rename. Launch
it like any other:

```bash
daedalus guild-master
```

When it starts, every *other* registered project's directory is mounted
**read-only** at `/guild/<name>` in its container, and an in-container `guild-mcp`
server — active for this project alone — lets its agent enumerate and read them:
`list_guild_projects`, `read_project_doc`, `guild_overview`.

Use it for what it is good at: cross-project status, reconciling roadmaps,
spotting that two projects are solving the same problem, drafting programme-level
plans grounded in each project's own `ROADMAP.md` / `SPRINTS.md` / `BACKLOG.md`
rather than in anyone's self-report.

Two limits, both deliberate:

- **Read-only.** It sees every project and can never write another's files.
- **Mounts are a launch-time snapshot.** A project registered after the Guild
  Master started appears on its next launch.

### The Guild Master as a control-plane client

This is the piece that would let the Guild Master *act*, and it is worth being
precise about its status.

**Built and shipped:** `guild-control-mcp`, an intent-level MCP client — it
exposes `list_tasks`, `get_task`, `task_events`, `programme_board`, `create_task`,
`request_verification` and `request_steering`, and
**nothing** resembling `run_shell`, `docker_run`, `git_exec` or `mount`. It talks
only to the **restricted agent socket**, never to `coordinator.sock`. The daemon
derives caller class from *which socket a request arrived on*, so an agent cannot
claim to be human, and the tiers are:

| Tier | Operations | An agent… |
|---|---|---|
| Read | list/get tasks, events, approvals, queues | executes |
| Bounded write | create task, request verification, request review | executes |
| Consequential | dispatch, retry, replan, cancel, integrate, approve/reject, target resync, **declare a dependency**, **steer or withdraw a steer** | gets a **proposal**, not an action |
| Human-only | confirming or denying a proposal | is refused |

Two of those may look over-restricted until you consider the attack: a
**dependency edge** decides what must happen before a Task is graded, so an agent
that could declare its own edges could declare them satisfied; and **steering**
injects an instruction into work already running, which is more subtle than
cancelling it because the Job carries on and only the log shows the change of
direction. Reads, task creation and asking the plane to apply *its own* oracle
(verify, review) execute directly, because none of them can exceed policy.

A proposal is a row a human resolves:

```bash
daedalus task proposals list          # what an agent has asked for
daedalus task proposals confirm P-2  # runs it AS YOU
daedalus task proposals deny P-2     # does nothing at all
```

An agent cannot confirm its own proposal — refused at two independent layers,
both tested. This is what makes "the Guild Master cannot approve its own work"
true in practice rather than by convention, and it is the concrete defence
against prompt injection: the Guild Master reads untrusted project documents, so
a poisoned `README.md` may *propose*, never *execute*.

**Wiring it up (BACKLOG #72, fixed after v0.53.0).** The container half of this
had always been in place — the entrypoint adds the `guild-control` MCP server
only when the restricted socket is actually present, because that socket *is* the
authority — but nothing on the host ever mounted it, so the tool was never wired
and the Guild Master stayed read-only in practice. It is now mounted, under rules
that are all refusals:

```bash
daedalus guild-master     # starts the control plane first, then the container
```

- The launch starts `daedalus-control` if it is not already listening, because a
  bind-mount source must exist before `docker run`. If the plane cannot be
  started you get a warning and a **read-only** Guild Master — never a failed
  launch.
- Only `<data-dir>/.daedalus/control-agent.sock` is ever mounted, at
  `/var/run/daedalus/control-agent.sock`. `core.GuildControlSocketMount` refuses
  anything whose basename is not `control-agent.sock`: mounting the human
  `control.sock` there would silently promote the agent to full authority, since
  the class comes from the file rather than the request.
- The mount is refused unless the path is a **real socket**, so a stopped plane
  yields no mount rather than a directory Docker would helpfully create.
- `DAEDALUS_CONTROL_AGENT_SOCKET` is set **only** alongside a real mount, so the
  in-container gate and the host mount can never disagree.
- No ordinary project gets any of this, at either end.

**Verifying it on your host:** `bash scripts/verify-guild-control.sh static`
proves the host half without Docker (15 assertions — including an agent caller
creating a task, having a cancel turned into a proposal, and being refused when
it tries to confirm that proposal itself), and `… real` inspects a running Guild
Master container for the mount, the socket, and the wired tool. The runbook, with
the failure modes and what each one means, is
[`guild-control-verification.md`](guild-control-verification.md).

The steering gap above is unchanged and still dormant — a different subsystem,
and documented the same way for the same reason: an unwired capability a document
claims is live is worse than one it admits is dormant.

---

## Limits you should know about

Short version of [`control-plane.md`](control-plane.md)'s closing section — these
are load-bearing when you decide how much to trust the thing:

- **Verification is reproducible, not correct.** It proves *this committed
  artifact, in this pinned environment, makes this frozen procedure report
  success*. An agent that writes weak product code its own committed tests still
  pass is a limit tests share.
- **The wall-clock budget is not a kill.** Bookkeeping plus a cancellation
  *request*; the container can outlive the verdict. Backlog #69.
- **Admission is not a durable scheduler.** The queue is in-memory and dispatch is
  driven by requests, so a daemon restart erases queue order. Backlog #70.
- **Liveness is partly heuristic.** Where no per-Job observer exists, a Job past
  its budget with a vanished worktree is *guessed* dead — labelled as a heuristic
  in the code, and "I don't know" leaves the Job alone.
- **Caller identity is a class, not an identity.** The plane knows "human" or
  "agent" from the transport; it does not know *which* human.
- **The event log is immutable through the API**, not cryptographically
  tamper-proof.
- **Steering delivers nothing on the shipped runner** (above).

## Where to read more

| Document | What it is |
|---|---|
| [`control-plane.md`](control-plane.md) | As-built reference: data model, state machine, verification, governance, integration, scheduler, graph, steering. The authority when documents disagree |
| [`guild-master-plan.md`](guild-master-plan.md) | The design, with the graded responses to two external critiques |
| [`guild-master-control.md`](guild-master-control.md) | The research base — control taxonomy, what is imposable on an autonomous agent and what is not |
| `daedalus task --help` | The complete, always-current command surface |
