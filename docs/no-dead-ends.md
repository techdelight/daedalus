# No Dead Ends

**A design pass on refusals, written 2026-08-25 after a day in which one
operator hit five states they could not leave.**

Status: **BUILT, 2026-08-31**, items 1–4. Item 5 is still a question. The design
below is unchanged from the proposal; what follows immediately is what shipping
it actually taught, which is the part worth reading twice.

---

## What building it found

**One table, and it found things the moment it existed.** The exhaustive
guard/table cross-check (`TestOperationTable_MatchesGuards_Exhaustive`, 15
operations × 15 states) failed on its first run in three places:

- **`CancelTask` on a finished task** fell through to the transition table and
  surfaced as `illegal state transition: cancelled → cancelled` — the plane's
  internals, for what is usually an operator repeating themselves. A real defect,
  and now a refusal in the plane's own words.
- **`review` checked *is a reviewer configured* before *is this state
  reviewable*,** so reviewing a `planned` task said "no reviewer configured" and
  sent the operator to look at their installation for a mistake they had made in
  the command. Ordering, but the wrong order.
- **`AddDependency` looked unguarded and was not.** The rule is already there,
  inside the transaction that inserts the edge — which is where it has to be,
  since a check made outside can be true when it runs and false when the row
  lands. What was wrong was the FIXTURE: it passed a nonexistent upstream id, and
  the existence check runs before the state check, so the call failed with "not
  found" and the rule was never reached. A guard was added out of that reading and
  then taken out again; it duplicated a better-placed check and downgraded
  `ErrDependencyInvalid` to a generic state refusal. Recorded here because the
  test was RIGHT to complain and the first reading of it was wrong — a fixture
  without the property that makes the rule reachable proves nothing about the
  rule, in either direction.

**And the fix reproduced the original defect.** The first version of the
exhausted-attempts refusal dropped `dispatch`, `retry` and `replan` from its
remedy list — and left `refine` in. Refine calls `checkDispatchBudget` like the
other three, so the Ledger told an operator to refine a task whose refine had
just been refused: exactly the `daedalus task retry` advice that started this
document, in the code written to abolish it.

Every assertion passed. It was caught by **looking at a screenshot of the page**
(`e2e/ledger.py`, `LEDGER_DEADEND_SHOT`), because the assertions checked the
three names somebody had thought of. The list is now held to account by
`TestExhaustedBudgetRefusesEverythingItDoesNotOffer`, which exercises every
operation against an exhausted task in both directions rather than trusting the
list — and by `TestExhaustedRefusalOffersNothingItWouldRefuse`, which takes the
sentence an operator actually reads and tries every operation it names.

The lesson is the document's own: **a hand-written list of anything is where this
goes wrong**, and a test that enumerates the same list proves nothing.

---

## The complaint, in one sentence

*A refusal that names no action the operator can actually take is a dead end,
and the plane produced five of them in a single evening.*

Each refusal was individually correct. The plane was right to decline every
time. What was missing in each case was the other half of the sentence: **and
here is the way forward.**

## What actually happened

One task, T-28, in the `snowball` project. The root cause was a single defect —
`git add -A -- :(exclude)…` is refused outright by git when the named path sits
inside a directory `.gitignore` ignores, so `Capture` returned no commit and the
agent's work was discarded after it had been done. Everything below is the plane
*handling that badly*, and each one was found by the operator running a command
and being told no.

| # | The state | Why nothing worked | Fixed in |
|---|---|---|---|
| 1 | `candidate` with an artifact naming no commit | review, verify and integrate all refused it (in git's words, not the plane's); retry and replan want `rejected`; only cancel escaped | `cdbaf7b`, `2289221` |
| 2 | review budget spent by passes that never ran | two harness failures charged the envelope; the artifact had never been read | `9511d78` |
| 3 | `verified` on a pass from the wrong oracle | `reverify` accepts `rejected` only, so a wrong **no** is correctable and a wrong **yes** is not | not fixed — routed around |
| 4 | attempts exhausted after a merge conflict | the envelope is frozen at create; `budgets.json` reaches only new tasks; `refine` cannot resolve a conflict | not fixed |
| 5 | review budget refusing the operator | a limit written to stop an *agent* looping, applied to the human asking for a second opinion | `62e9b58` |

Five states. Three of them I fixed one at a time while the operator waited,
which is the right thing to do at the time and the wrong thing to keep doing.

## The shape

Sort them and four distinct faults fall out. They are worth naming separately,
because they have different cures.

**A. Charged for our own fault.** (#2, and #4's first attempt.) The budget is
spent by a defect in the plane rather than by work. `CountReviewCycles` has
discounted harness-fault re-verifications since Sprint 62 and says why in as
many words — *"the budget exists to bound how many times an artifact may be
graded, not how many times we may get the grading wrong"* — and that rule was
never carried anywhere else.

**B. A frozen envelope with no amendment.** (#4.) `MaxAttempts` is captured at
create and stored authoritatively, so nothing can widen the bound on itself.
Correct against an agent. But the operator, who owns the money, also cannot
raise it — not even by a recorded, deliberate act. The only route is to cancel
and recreate, which destroys the task's history, its reviews, and its lineage.
Note the inconsistency: `task checks` amends a Task's acceptance commands in
place, with an event and a caller, and is human-only. The precedent for a safe
amendment exists and was not applied to the budget.

**C. The wrong party bounded.** (#5.) Governance written for agents applied to
humans. The plane derives caller class from which socket a request arrived on —
that is the whole mechanism behind tiered authority — and simply was not using
it here. Fixed for review. **Unaudited everywhere else.**

**D. A state that can be entered and not left.** (#1, #3.) The state machine has
no rule that every non-terminal state offers the operator a non-destructive exit,
and nothing checks for one.

## The structural reason nothing could name a remedy

**"Which operations are available from state S" is not answerable by any single
piece of code.** It is spread across three representations that no test compares:

- **Four tables** — `amendableStates`, `steerableStates`, `refinableStates`,
  `reviewableStates`.
- **Eight inline guards** — `if task.State != StateRejected { … }` and friends,
  in `reverify.go`, `approval.go`, `graph.go` and four places in `service.go`.
- **A third copy in the Ledger**, as `states: [...]` on seventeen entries of the
  `COMMANDS` table in `control.js`.

So a refusal cannot compute what to suggest, and every refusal message is
hand-written prose about what to do next. Hand-written prose goes stale exactly
the way a hand-written list of subcommands does — which is this repository's own
recurring defect, recorded as *derive the check, never enumerate it*.

It also produced a worse failure than staleness. On 2026-08-24 the guard I added
for #1 told the operator to run `daedalus task retry`, and `retry` refuses a
`candidate`. **The remedy named in a refusal was itself refused.** Nothing could
have caught that, because nothing knows which operations a state admits.

## The principle

> **Every refusal names at least one operation the caller can actually perform
> from the state they are in — and if there is none, that is a bug in the state
> machine, not a message to be worded better.**

With one corollary, which is the part that matters to somebody using this:

> **No operator action should require destroying a task's history.**

## What to build

Four pieces, in dependency order. The first is the one that makes the rest
cheap, and is worth doing even if nothing else here is.

### 1. One table of which operations a state admits

A single `map[Operation]map[State]bool` — or better, one method per operation
already declared in one place — that the service guards, the refusal messages
and the Ledger all read.

- The four existing tables move into it unchanged.
- The eight inline guards are replaced by a lookup.
- The Ledger's `COMMANDS[].states` is **derived** from it, served over the
  existing control API rather than restated in JavaScript. That removes the
  third copy, which is the one most likely to drift, because nothing in Go can
  see it.

The test that makes it load-bearing: for every operation and every state, the
guard's answer and the table's answer agree. That is `TestCanTransition_Exhaustive`'s
shape — a second, independent statement of the same rule — which is the one kind
of test in this repository that has repeatedly caught real mistakes.

### 2. A refusal carries its remedies, and they are checked

`RejectionError` gains a machine-readable list of the operations available from
the current state, derived from (1) rather than written by hand. The CLI and the
Ledger render it; the prose stays, because *why* an operation is the right one is
a human sentence, but the *availability* of it stops being a guess.

The test writes itself, and it is the one that would have caught my `retry`
advice: **every operation named in a refusal must be legal from the state the
refusal was issued in.**

### 3. Stop charging for the plane's own faults

Extend A's rule beyond re-verification. An attempt that failed for a
**plane-side** reason should be recorded and not charged:

- `Capture` produced no commit (provably ours: the agent exited 0).
- The worktree was missing, the image was absent, the container never started.
- A review that produced no judgement — already done in `9511d78`.

The distinction is not always available, and where it is not, **charge**. The
existing comment on the failure path is right that the plane cannot tell a broken
environment from a genuinely bad run, and refunding on an unsure reading would
make a Job that fails instantly free to repeat forever. But a capture failure is
not an unsure reading. We know whose fault it is.

### 4. `task budget` — an operator amendment, recorded

```
daedalus task budget T-29 --attempts 5 --review-cycles 4
```

Modelled on `task checks`, which is the existing precedent for amending
something frozen: one transaction, an event with the caller and the lineage, and
**human callers only** — an agent is refused outright rather than proposing, on
the same reasoning that keeps `budgets.json` host-side.

Two guards:

- It may never exceed the **project ceiling**. Raising that is still an edit to
  the host-side policy file, which is where the operator's authority over money
  belongs.
- The Task's budget lineage is visible in `task show`, so "this task got two
  extra attempts" is a fact in the record and not a mystery.

This is what closes #4 without recreating the task.

### 5. And the one I am least sure about: `reverify` from `verified`

#3 is real — a wrong **yes** should be as correctable as a wrong **no**, and a
vacuous pass is the more dangerous of the two because it can be integrated. The
route exists today (reject at the gate, then re-verify) and is pinned by a test,
but it is not discoverable and it makes the operator record a judgement about the
*work* in order to correct a judgement about the *oracle*.

Widening it means a new edge in `legalTransitions`, which is the most
safety-relevant table in the package. **I would rather this be argued about than
designed here.** The cheap alternative — the refusal from `verified` naming the
reject-then-reverify route, courtesy of (2) — may be enough.

## What NOT to do

Recording these because each is tempting and each would make things worse.

- **Do not make budgets advisory.** The envelope exists so that nothing an agent
  does can widen the bound on its own work. Every proposal above keeps that.
- **Do not let an agent amend its own envelope**, by proposal or otherwise.
- **Do not auto-widen on failure.** A bound that grows when it is hit is
  decoration.
- **Do not add `--force` to everything.** A force flag moves the decision to the
  operator without telling them what they are overriding, which is how #3's
  vacuous pass would have been "solved" — by integrating it.
- **Do not fix the messages alone.** Better prose in a refusal that still names
  no reachable action is the same dead end, more politely worded.

## Scope

Items 1–3 are one sprint and are the ones with evidence behind them. Item 4 is
small but touches governance and deserves its own review. Item 5 is a question,
not a task.

**As built (2026-08-31).** Item 4 shipped early, on 2026-08-25, because T-29 hit
T-28's wall an hour after this was written. Items 1–3 shipped together:

| Item | Where it lives |
|---|---|
| 1. One table | `internal/control/operations.go`. Four tables and a dozen inline guards now read it; `GET /operations` serves it; the Ledger's `COMMANDS` entries name an operation and carry no `states` |
| 2. Refusals carry remedies | `StateError` (409) and `RejectionError` (422) both carry `Remedies`, computed from (1). The wire envelope gains `remedies`; the CLI prints one per line; the Ledger renders them in its own labels |
| 3. Plane faults not charged | `ReasonCaptureFailed` + `CountJobsForTask`. **Only** a capture failure — the agent exited 0 and our capture produced nothing. Everything else still charges, for the reason `reapJob` already gives |

Two things were deliberately NOT built, and both are the scope discipline the
migration-plan review asked for: the table is operation → states and nothing
more — no nine-field `OperationDescriptor`, no surface generation — and naming
`amend_checks` / `amend_budget` for the first time does **not** tier them
(`TestChecksAndBudgetAreNeverTiered` pins the absence).

**Out of scope, deliberately:** the merge-conflict path (#4's proximate cause).
An artifact that will not rebase needs a human to resolve it, and no refusal
design changes that. The remedy — cherry-pick the artifact's branch, which
survives worktree removal by design — should be *named by the refusal*, which is
item 2's job, and nothing more.

## The measurement

If this works, one thing should be true a month from now: **no operator hits a
state whose only exit is `cancel`.** That is checkable from the event log —
every `cancel` on a non-terminal task, paired with the refusal that preceded it,
is a candidate dead end. If the list is empty, this was worth doing.

**The structural half of that is now asserted rather than waited for.**
`TestNoStateIsADeadEnd` walks every non-terminal state and fails if the only
operation it admits is `cancel`. That does not replace the measurement — it
catches the state machine having no way out, not a REFUSAL having no way out for
some other reason (a budget, a missing artifact) — so the event-log check is
still the thing to run in late September.
