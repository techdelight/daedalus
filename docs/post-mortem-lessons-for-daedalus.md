# What two post-mortems ask of Daedalus, the Guild and the Ledger

*Reading `post-mortems/flashcards-m28-postmortem.md` (a defect study of one
milestone) and `post-mortems/snowballv2-POST_MORTEM.md` (a delivery review of one
MVP) for what they say about this tool. Both post-mortems are kept locally and
are not in this repository.*

**Status: REVIEW.** Written 2026-09-25. Nothing here is filed work.

---

## The thesis

Read as a pair, the two documents describe one failure with two faces:
**evidence that proves less than it appears to.**

- Snowball's CI read the presence of a Docker *client* as proof of a Docker
  *daemon*. Two dependencies, one check.
- Snowball's packaging success proved a tarball existed, not that the service
  inside it could open its database. *"Packaging success was treated as stronger
  evidence of runtime readiness than it was."*
- Flashcards' `BUNDLE_VERSION` test asserted `toGoalBundle(x).version ===
  BUNDLE_VERSION` — a constant against itself, true at any value.
- Flashcards ran **22 green CI runs** across the milestone and caught **0 of 6**
  real app defects, because CI runs the same two instruments the developer runs.
  *"Its green is evidence about regression against what those instruments
  already cover. It is not independent evidence."*

And flashcards supplies the rule that decides what to do about it:

> **Deliberately not an action item: "be more careful."** Five of the six real
> defects were found by careful reading, which means care was present and
> working. Asking for more of it adds nothing. Every item above is a mechanism.

Measured against that standard, Daedalus carries the same defect in four places
and has already built the cure for it in one.

---

## Method note

Claims below are marked:

- **[verified]** — checked first-hand against the tree at `development` @
  `c752fe6`, with the command or file:line given.
- **[relayed]** — reported by a search agent over this repository and *not*
  independently re-checked. Treated as a lead, not a fact. Given the subject of
  this report, the distinction is not decoration.

Both post-mortems' own figures (test counts, defect counts, CI runs) are taken
from those documents and were not reproduced.

---

## 1 · Evidence that proves a permission bit

**The lesson.** Snowball **PM-01**: add a release smoke test that starts the
packaged binary, initialises real storage, restarts, and proves persistence —
and gate publication on it. Flashcards **A2**: fail on vacuous assertions, a
sweep rejecting self-comparisons and arity checks over defaulted parameters.

### Daedalus

**The release archive is never executed.** **[verified]**
`scripts/test-release-bundle.sh` (205 lines, 30 assertions) and
`scripts/test-install.sh` (505 lines) build the archive, run `install.sh`, and
then assert:

```sh
check_exec() { if [ -x "$2" ]; then pass "$1"; else fail "$1 (not executable: $2)"; fi; }
```

`[ -x ]` is a permission bit. Neither script ever invokes the installed
`daedalus` — not `--version`, not anything. A wrong-architecture build, a
truncated archive or a corrupt binary passes all 710 lines. This is Snowball's
gap exactly, and Snowball learned it by shipping a release that died at startup
with an error message that named the wrong cause.

**`core.BuildExtraArgs` has eight passing tests and no production callers.**
**[verified]** Every reference in the tree is the definition
(`core/command.go:82`), two comments (`core/command.go:32`,
`internal/coordinator/coordinator.go:217`), and eight tests in
`core/command_test.go`. `TestBuildExtraArgs_WithDinD` asserts the Docker socket
is mounted; nothing on a live launch calls the function, so the DinD mount
(`core/command.go:86`), the display args (`:89`) and the persona overlay mounts
(`:93-101`) are never constructed. `StartRequest`
(`internal/coordinator/daemon.go:95-107`) carries no `DinD` field, so
`toConfig` leaves it false unconditionally. The suite is evidence about dead
code. **[relayed]** the same agent reports `resolvePersonaOverlay`
(`cmd/daedalus/persona.go:23`) is test-only, and that `core.ResourceLimitEnv` is
called from `composeEnv` with a config that structurally cannot carry
`MemLimit`/`CPUs`/`PidsLimit` — so `docker-compose.yml`'s `4g` / `2.0` / `512`
defaults are always in force.

**The verify scripts cannot tell a skipped run from a clean one.**
**[verified]** `scripts/verify-guild-control.sh` and `scripts/verify-m20.sh`
count `PASS`/`FAIL`, print `N passed, M failed`, and exit on `FAIL -eq 0`.
Nothing asserts *how many* assertions should have run, so a silently skipped
block reports "12 passed, 0 failed" and exits 0. The expected counts live only
in text, and already disagree with each other:

| Source | Claimed |
|---|---|
| `docs/control-plane.md:852` | 19 assertions |
| `docs/m20-verification.md:19` | 19 assertions |
| `docs/m21-m22-verification.md:19` | 29 assertions |
| `docs/using-daedalus-control.md:1106` | 15 assertions |
| `BACKLOG.md` #82 / #88 | 20/20 · 35/35 |

This is flashcards' summary-line rule — *"if you did not read the summary line,
you did not run the check"* — with the number that would make the summary line
mean something kept somewhere the script cannot see.

### Proposed

1. **PM-01 for Daedalus.** The release test runs the installed binary:
   `daedalus --version` matching the packaged version, `daedalus list` against a
   throwaway data dir, `daedalus docs lint` over a scaffolded project. Gate
   `package-release.sh` on it.
2. **A2 for Daedalus.** A sweep that fails on an existence-only assertion where
   an invocation is available, and on an exported `core` symbol with no
   production caller. The second catches `BuildExtraArgs` mechanically rather
   than waiting for somebody to notice.
3. Every verify script asserts its own expected assertion count, read from the
   script rather than from a document.

---

## 2 · A claim recorded is not a claim executed

**The lesson.** Flashcards **A6**: every line in a roadmap, sprint record or
design document saying *settled / decided / retired* must name either a shipped
version or an open backlog id, checkable by a document sweep. Measured
motivation: priority was settled retired on day one and shipped intact 37 hours
later, because *"nothing mechanical connected the decision to its execution."*

Snowball **PM-10** is the same item from the other end: create issues for PM-01
through PM-09 and replace the local references with links.

**Both documents carry this defect about themselves.** Flashcards §1: *"The
action items in §8 are not yet tracked in `BACKLOG.md`."* Snowball: *"Tracking
references are local postmortem IDs until corresponding Forgejo issues are
created"* — and every row reads `Open`.

### Daedalus

**Resolution claims mostly cite nothing checkable.** **[verified]** 69 backlog
entries; 19 open with a bold **FIXED** / **DECIDED** / **BUILT** / **PARTIALLY
FIXED**; roughly 8 of those name a commit SHA, a `Test…` name, or a
`scripts/verify-*.sh`. (Indicative — the count is a regex over heterogeneous
text, not an exact audit.)

**Four live cases of text claiming a capability that is not there:**

| Claim | Reality | |
|---|---|---|
| `docs/control-plane.md:1444` lists `request_review` in the agent's "bounded write — executes" row | No such tool in `guild-control-mcp`. `OpReview` is `TierAllowed`, `callerScope.ReviewTask` is live, `executeProposal` has the case — nothing reaches any of it from an agent. Same for `list_pending_approvals`, `list_targets`, `job_steering` | **[relayed]** |
| `internal/web/control.go:324-327` — *"The Ledger shows it because this is the failure nobody can deduce"* | `control.js` never fetches `/targets/lag` | **[verified]** |
| `internal/web/static/control.css:1064-1068` — *"The list keeps its scroll position while an entry is open"* | `control.js:813` empties the list every 15 s and nothing saves `scrollTop` | **[relayed]** |
| `ARCHITECTURE.md:337` — the Guild Master is "the read-only programme overseer" | False whenever the agent socket is mounted, which `daedalus guild-master` arranges by default | **[relayed]** |

The first is **#82's shape recurring inside the document that recorded #82's
lesson**: an operation tiered, written into a deliverable list, and marked done
while no agent surface could reach it.

### Proposed

Extend `core.ValidateDocs` (`core/validate.go:49`) — it already returns
`[]Finding{Severity, Doc, Message}` and is already run by `daedalus docs lint` —
with a rule that a resolution claim must name evidence: a 7+ hex SHA, a
`Test[A-Z]\w+`, or a `scripts/verify-*.sh` path. That is flashcards' A6 with a
home that exists, a severity model that exists, and a command that already runs
in CI.

---

## 3 · The instrument accusing the wrong place

**The lesson.** Flashcards defect 8: a fixed 350 ms wait in the harness killed
an unrelated group thirty seconds later. Defect 9: `leaveAnyPanel` returned
quietly when a press changed nothing, converting a stuck panel into a timeout
elsewhere and **a crash with no summary line** — latent for twenty sprints.
Defect 10: a check ticked the wrong deck, reported *0 cards*, and read as an app
defect. **A5**: no harness helper may swallow its own timeout. **A4**: 543
`waitForTimeout` calls against 85 event-based waits, to be driven down under a
sweep.

Snowball's version: SQLite reported `out of memory (14)` for a file-open
failure. *"The SQLite error text included `out of memory`, which could misdirect
diagnosis."*

### Daedalus

- **`e2e/ledger.py` runs 26 fixed waits against 18 event-based waits.**
  **[verified]** `e2e/web-ui.spec.ts` is 0 against 53 — that one is already
  right, which makes the Python harness the outlier rather than the norm.
- **A late response paints into the wrong entry, and the failure is swallowed.**
  **[verified]** `loadDetail` guards correctly:
  ```js
  if (!current || current.kind !== 'task' || current.id !== id) return; // moved on
  ```
  (`control.js:1234`). `paintRecord`'s events fetch (`control.js:1579`) has no
  such re-check after its response arrives, and **[relayed]** `paintSteering`
  (`control.js:1720`) is reported to append via `insertBefore` against a
  possibly-detached anchor, throwing into its own `.catch` so the rows vanish
  silently. That is flashcards' defect 9 in a different language: a helper that
  absorbs its own failure so the symptom surfaces somewhere else, or nowhere.
- **The sentences that explain a dead end render unstyled.** **[verified]**
  `ff-cmd-note` is used four times in `control.js` and has **no CSS rule in any
  stylesheet in the repository**. Those four strings are the ones that say why a
  task offers no commands.

### Proposed

1. A sweep over `e2e/*.py` forbidding a `catch`/`except` inside a wait helper
   that does not re-raise, plus a strictly-declining ceiling on fixed waits —
   flashcards' A4 and A5, which are cheap because the baseline is already 26
   rather than 543.
2. Add the generation guard to `paintRecord` and `paintSteering`. The pattern is
   three hundred lines away in the same file.
3. Give `ff-cmd-note` a rule, or delete the class and use one that exists.

**The positive lesson, which is the best line in either document.** Flashcards
defect 10 was diagnosed in one reading because the slice had just added a
*learner-facing* warning — *no card in those decks carries any of these tags
yet* — and it printed in the failing check's detail line. A message written for
a user diagnosed a fault in the harness.

Daedalus's analogue already exists: `jobEnvironmentNote` (#83), one paragraph
telling a Job it has no git remote, which bought a refusal in the first minute
instead of a wasted attempt. Snowball's Docker failure is the argument for
generalising it — **the environment note should state what the container
actually has, not what the image intended.**

---

## 4 · The Guild cannot read the documents it exists to read

This only appears when the two post-mortems are read as a pair, which is
precisely the Guild Master's stated job.

- **`POST_MORTEM.md` has zero references in any Go file.** **[verified]** It is
  a template nothing parses, lints, or surfaces.
- **`guild-mcp` exposes three tools** — `list_guild_projects`,
  `read_project_doc`, `guild_overview` — and `read_project_doc`'s allow-list is
  derived from `core.RequiredDocs()` (the eight standard documents) plus
  `VERSION` and `CLAUDE.md`. **[verified]** Post-mortems are not in it.

So two projects independently produced a post-mortem; both recorded action items
with no mechanical link to execution; both said so in the document — and the
component whose entire purpose is noticing what projects have in common is
structurally blind to the document class where they wrote it down.

### Proposed

Make post-mortems a structured document class:

1. `core.ParsePostMortem` over the action-item table — reference, type,
   priority, owner, action, status/tracking — in the shape `core.ParseBacklog`
   and `core.ParseSprints` already use.
2. Add it to `guild-mcp`'s allow-list and to `guild_overview`.
3. Then the Guild Master can answer what neither project could answer for
   itself: **which action items across every project are still open, and for how
   long.** Flashcards' A6 becomes checkable across the guild rather than per
   repository.

Noticing is the remit; filing stays a human act, at the existing proposal tier.

### Two smaller Guild findings

- **Five proposal tools are named nowhere in the role doc.** **[relayed]**
  `request_dispatch`, `request_retry`, `request_replan`, `request_cancel` and
  `request_integration` are registered in `guild-control-mcp` and absent from
  `GuildMasterRoleDoc`'s "What you can only propose" list — against that file's
  own argument that a tool nobody is told about is a tool nobody reaches for.
- **Two proposals can be recorded and never confirmed.** **[verified]**
  `OpReverify` and `OpRefine` are `TierProposal` (`authority.go:157`, `:162`)
  with live propose paths (`scope.go:229`, `:95`), and `executeProposal`
  (`proposal.go:104-186`) has **no case for either** — seventeen cases, neither
  of these. A human confirming one falls through to `default:` and it fails with
  *"names an operation this plane cannot execute"*. `OpRefine` is additionally
  absent from `mutatingOps` (`authority.go:216`), so
  `TestAuthority_EveryMutatingOpIsTiered` does not cover it. A refusal that
  leads nowhere, which is the class #95 set out to end. It is currently masked
  only because no MCP tool calls either — one unfinished surface hiding another.

---

## 5 · The Ledger, and Snowball's scope defect

**The lesson.** Snowball's third failure: a request for CLI install instructions
became a token-authenticated release-discovery and download script. *"The
intended abstraction level of Quick Start was not made explicit before
implementation. A technically complete solution was not the desired user
experience."* Review happened only after a full implementation and a PR existed;
PR #12 was closed unmerged and its release and refs deleted. **PM-09** asks for
a checklist naming the intended audience, the shortest successful path, and
**explicit non-goals**.

That lands on the Ledger twice.

**The task never states its non-goals.** **[relayed]** The New entry window asks
for `Objective` and `Deliverables — one per line: what will exist when this is
done`, and the entry says so when they are missing: *"No deliverables — nothing
on this task says what will exist when it is done."* There is no field for what
the task must **not** do. That is one field, and it is the half Snowball lost a
pull request to.

**The person deciding cannot see the change.** **[verified]** There is no `diff`
subcommand in `cmd/daedalus/task.go`'s dispatch (25 subcommands, none of them
diff). **[relayed]** there is no diff route on `TaskAPI` and none in the Ledger;
the diff is computed only inside the reviewer as prompt input
(`internal/control/reviewer.go:123-128`) and never returned to any client.
Meanwhile **[verified]** the approvals payload was deliberately widened in M21 to
carry objective, base SHA, state, created-at, programme, rationale and its
author — with a comment at `internal/web/control.go:203-208` arguing that this
*is* the milestone — and `renderApprovals` (`control.js:2451-2463`) reads **only
`t.id`**, every five seconds, forever.

Snowball caught its scope mismatch by reading the implementation. The cost was
that the implementation already existed.

---

## What is already right, and should be the template

The Ledger's command plates name a control-daemon **operation**, and their
legality comes from `GET /api/control/operations` — fetched once, derived, never
hardcoded. `internal/web/control_test.go:1160-1206` parses `control.js` and
fails in **both** directions: a plate naming an operation the daemon does not
have, *and* an operation with no plate. It fails again if a plate regrows its own
`states: [...]` list.

That single pattern is the answer to §1, §2 and §4. The recommendation is to
apply its shape to the three surfaces that still hand-maintain a list:

| Surface | Derive from |
|---|---|
| `guild-control-mcp`'s registered tools | `agentAuthority`'s tiered operations |
| the release test's assertions | the archive manifest, plus one real invocation |
| a verify script's summary line | the script's own assertion count |

**[relayed]** `scripts/verify-guild-control.sh:119-130` checks eight tool names
against the source; nothing asserts parity between `agentAuthority` and the
registered tool set — which is the gap that leaves `request_review` documented
and unreachable.

---

## Consolidated actions

| # | Action | Type | Source | Size |
|---|---|---|---|---|
| L1 | Release test runs the installed binary; packaging gated on it | prevent | PM-01 | small |
| L2 | Post-mortems become a parsed document class, readable by `guild-mcp` | detect | A6 + PM-10 | medium |
| L3 | `ValidateDocs` requires evidence for a resolution claim | detect | A6 | small |
| L4 | Derive `guild-control-mcp`'s tool set from `agentAuthority`, both directions | prevent | §2, §4 | small |
| L5 | `executeProposal` cases for `OpReverify` / `OpRefine`; `OpRefine` into `mutatingOps` | fix | §4 | trivial |
| L6 | Verify scripts assert their own assertion count | detect | flashcards' summary-line rule | trivial |
| L7 | Generation guard on `paintRecord` / `paintSteering`; a rule for `ff-cmd-note` | fix | defect 9 | trivial |
| L8 | Sweep: no swallowed timeout in a wait helper; declining fixed-wait ceiling | prevent | A5, A4 | small |
| L9 | Sweep: no exported `core` symbol without a production caller | prevent | A2 | small |
| L10 | A non-goals field on a task, shown at the approval checkpoint | prevent | PM-09 | small |
| L11 | Show the artifact's diff to the person deciding | detect | §5 | medium |
| L12 | Generalise `jobEnvironmentNote` — state what the container has, not what the image intended | detect | Snowball's Docker failure | small |

L5, L6 and L7 are hours. L1, L3 and L4 are the highest ratio of value to size.
L2 is the one that changes what the Guild is for.

---

## Questions

1. **Is a post-mortem a first-class project document?** L2 assumes yes — that it
   joins ROADMAP/SPRINTS/BACKLOG as something the tool parses rather than
   something a human reads. If no, the Guild half of this report collapses to
   "add it to the read allow-list", which is one line and much less useful.
2. **Should `daedalus docs lint` enforce claim-evidence (L3)?** It would fail on
   this repository today, in eleven-ish places. That is the point, and it is
   also a decision about whether the linter is advisory or blocking.
3. **Does the non-goals field (L10) belong on the task or on the objective?**
   Snowball's failure was a *documentation* task; Daedalus's tasks are mostly
   code. The lesson may generalise less well than it reads.
4. **L4 changes the Guild Master's reach.** Deriving tools from `agentAuthority`
   would give it `request_review`, `list_pending_approvals`, `list_targets`,
   `job_steering`, `add_dependency`, `sync_target`, `cancel_steering`,
   `reverify` and `refine` — because all nine are already tiered for it. That is
   the correct reading of the existing authority table, and it is still a real
   widening that somebody should choose deliberately rather than inherit from a
   test.

---

## Assumptions and limits

1. **Two projects, two periods.** Both single-maintainer, agent-assisted,
   pre-1.0; one a Go modular monolith with a forge pipeline, one a browser-only
   app with no server. Conclusions about *Daedalus* rest on code in this
   repository; conclusions about *what matters* rest on a sample of two.
2. **Neither post-mortem names Daedalus, and neither describes using
   `daedalus-control`.** No Tasks, Jobs, verification or approval appear in
   either document. Every "Daedalus could have helped" reading here is therefore
   an argument, not an observation.
3. **Whether either project ran inside a Daedalus container is not established
   by these two documents.** Snowball's CI ran on a Forgejo runner; flashcards'
   browser pass ran Chromium somewhere. The container is not load-bearing for
   any finding above, which are all about this repository's own code.
4. **The [relayed] claims were produced by search agents and not re-checked.**
   They are the weaker half of every section they appear in, and §5's
   non-goals reading and §4's role-doc finding rest entirely on them.
5. **Sizes are judgement.** Nothing here has been costed.
6. **The backlog evidence ratio (19 claims, ~8 with evidence) is a regex over
   heterogeneous text.** It is directionally right and not an audit.
