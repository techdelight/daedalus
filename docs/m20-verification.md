# Verifying Milestone 20

A playbook for examining what M20 actually built — *Programmes in the Plane* and
*a Reviewer at the Gate* — and for telling the parts that are proven from the
parts that are only argued for.

It is written in the order you should distrust things. The control-plane logic is
the cheapest to check and the least likely to be wrong; the container seam is the
opposite, and this repository's history is unambiguous about which one bites.
Every host-only seam here — the runner (`Not logged in`), the verifier's
entrypoint (checks that never ran), the git mount (`fatal: not a git repository`)
— was green in tests and broken on a host.

---

## 0. The 60-second version

```bash
bash scripts/verify-m20.sh fake        # 19 assertions, no Docker, isolated data dir
```

It builds the binaries, spins a daemon in a temp data dir, and proves the whole
of part one plus the recording half of part two. It never touches your registry,
your `control.db`, or a running daemon. If that is green, everything below is
about the one thing it cannot reach: a real agent in a real container.

---

## 1. Part one — is intent actually structural?

The claim: a programme is plane state a Task can point at, and a Task records
what it is *for* and **who said so**.

```bash
daedalus programmes list
```

**What to look for.** Every programme has a `PR-n` id. That is the whole
argument: the file-backed store keyed a programme by its filename, so a rename
broke every reference silently. If your pre-existing programmes are listed here,
the daemon adopted them on start — once, idempotently by name, which is why there
is no migration flag to have forgotten.

> The files under `<data-dir>/programmes` are **kept and no longer read**. Editing
> one now edits a copy nobody consults. That is deliberate and documented; if you
> want it gone, that is a decision to take on purpose.

```bash
daedalus task create --project my-app \
  --objective "Add cursor pagination to GET /items" \
  --programme fluency \
  --rationale "the review queue is the habit everything else hangs off"

daedalus task status T-n
```

**The line that matters** is the one ending `(human)`:

```
For: the review queue is the habit everything else hangs off (human)
```

The author is derived from the socket the request arrived on and is never in the
request. Create a Task through the Guild Master instead and the same line reads
`(agent)`. That is what makes "the rationale is your own words" checkable rather
than hopeful — and it is the property to check first if you ever wonder whether
the record still means what it says.

**Then the roll-up:**

```bash
daedalus programmes status fluency
```

Look for the section headed *Waiting on work outside this programme*. That is the
one fact no per-project view can show, because the two Tasks are in different
projects — a programme that looks fully staffed while blocked on something nobody
put in it. If you have no such edge, make one (`daedalus task depends A --on B`
across two projects) and check it appears.

**Two refusals worth provoking**, because each replaces a silent failure:

```bash
daedalus task create --project my-app --objective x --programme nonsense
#   → refused: no programme "nonsense"       (the file store stored the dangle)
daedalus programmes remove fluency
#   → refused: still has N task(s)           (dissolving it would erase their reason)
```

---

## 2. Part two — does the reviewer report without acting?

The claim: a separate agent reads the diff against the promise, and its verdict
is **evidence**, not a gate.

The property to check first is the safety one, and you can check it without
Docker at all — this is what the script's second half does:

```bash
daedalus task status T-n | grep State     # before
daedalus task review T-n
daedalus task status T-n | grep State     # after — MUST be identical
```

**The state must not change.** Before M20 a failed review drove the Task to
`rejected` and reclaimed its worktree. If you see it move, the safety property is
broken and nothing else in this section matters. The command says so itself:

```
This is advisory. Nothing moved — T-7 is still verified, and the decision is
yours at the approval gate.
```

Then look at what was recorded — `daedalus task status T-n`, or the Ledger's
**record** page (`daedalus web` → Ledger → the entry → *record*):

```
Reviews (1)
  RV-1  had concerns  by agent:daedalus-review-J-9  2026-08-20T…
      <the reviewer's reasoning>
      [blocking] internal/api/items.go:88  cursor is not validated
        why: a malformed cursor reaches the query builder
```

**What good looks like.** Findings carry a *location* and a *why*. A finding with
neither is an opinion — which is what the old `{Passed, Detail}` pair could
already express, and the reason the shape changed.

**What "no judgement" means.** If you see:

```
Review: … had concerns … no judgement: the reviewer wrote no judgement to .daedalus/review.json
```

that is **not** the reviewer disapproving. It means the review could not be
obtained. The distinction is deliberate and is the one this whole arc has been
about: a broken harness reading as a criticism of the work is exactly what every
verify verdict in this project's history did. Treat it as "go look at the
container", never as "the change is bad".

---

## 3. The container seam — the part to actually distrust

```bash
bash scripts/verify-m20.sh real my-app     # prints this checklist, runs nothing
```

Run the real thing against your own data dir, on a project whose agent has logged
in at least once. Then, in order of how likely each is to be what broke:

| # | Check | Command | What a failure means |
|---|---|---|---|
| 1 | A container started | `docker ps -a \| grep daedalus-review` | The project must be `daedalus-review-<job>`, **not** `daedalus-job-<job>`. The job name would mean the reviewer is running as the worker — grading its own homework by proxy. |
| 2 | It had credentials | `grep -i 'not logged in' <data-dir>/.daedalus/control.log` | The seeding did not reach it. Same failure Jobs had in the `Not logged in` era, one component later. |
| 3 | It wrote a judgement | the review output | `no judgement: …` names which step failed — agent exited, file missing, or unreadable JSON. |
| 4 | The judgement is useful | `daedalus task status T-n` | Findings that restate the diff back at you mean the **prompt** needs work, not the plane. `ReviewPrompt` is pure and exported for exactly this reason. |
| 5 | Nothing moved | `daedalus task status T-n` | Covered above, but check it on the real path too. |

**Before any of this, restart both daemons.** Neither is displaced by a rebuild —
`EnsureRunning` reuses a live one and there is no version handshake:

```bash
daedalus coordinator stop
kill $(cat <data-dir>/.daedalus/control.pid)
```

---

## 4. Reading the code, if you would rather

The argument is in the source, at the places the decisions were taken:

| Question | Where |
|---|---|
| Why does the plane own programmes? | `internal/control/programme.go`, header |
| Why is the rationale's author not in the request? | `internal/control/model.go`, `Task.RationaleBy` |
| Why does a reviewer report and not act? | `internal/control/review.go`, `ReviewTask` doc |
| What does the reviewer see, and what does it cost? | `internal/control/reviewer.go`, header |
| Why is a harness failure not disapproval? | `internal/control/reviewer.go`, `reviewUnavailable` |
| Why can a waived Job land now? | `internal/control/integrate.go`, `jobToIntegrate` |

The tests worth reading are the ones that pin the safety properties:
`TestReview_ReportsAndDoesNotAct`, `TestReviewUnavailable_IsNotDisapproval`,
`TestUnwaivedJobCannotLandOnItsStateAlone`, and
`TestTaskRationale_AuthorshipComesFromTheTransport`.

---

## 5. What M20 did **not** do

Stated here so it is not discovered later:

- **The machine verify gate is still not authoritative, and cannot be** until
  backlog **#74** is closed. With the network off and no dependency cache the
  verifier cannot run a real build or test suite, so it falls back to grading
  documents. The tally — one verdict in seven was about the work being graded —
  is in `docs/control-plane.md`.
- **The Ledger has no programme surface.** You can read programmes from the CLI
  and see a Task's programme in its entry, but there is no view of a programme
  itself in the browser.
- **The Guild Master's programme path was missing and is now built (#82).** The
  first version of this document said it was "built but unexercised", which was
  false: `form_programme` was *tiered*, and tiering is not building.
  `guild-control-mcp` had no programme tool at all, and `executeProposal` had no
  case for the three operations, so a confirmed proposal failed closed on *"names
  an operation this plane cannot execute"*. Both are fixed, and the whole path is
  now asserted over the real sockets — `bash scripts/verify-guild-control.sh
  static`, checks 15–20. **The error is left recorded here rather than edited
  away**, because tiering an operation and believing it exists is the mistake
  worth remembering.
- **`AgentReviewer` has never run.** Every line of it is host-only. The `fake`
  phase proves the plane's half; the container's half is unproven until you run
  section 3.
