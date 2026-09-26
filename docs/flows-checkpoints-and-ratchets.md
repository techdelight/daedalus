# Four flows and a ratchet

*How the Ledger's task flow compares with the way three real projects actually
shipped — flashcards' milestone 28, Snowball's MVP, and a week of
agent-delivered work on Egor — read through one idea from engineering practice
and current research: the **ratchet**. Written 2026-09-26.*

**Status: REVIEW.** Analysis, not a plan of record. The three post-mortems it
draws on are kept locally in `post-mortems/` and are not in this repository.

---

## The answer first

All four flows are trying to do the same thing: make progress stick.
Work should move forward, and once something is known-good, nothing should be
able to quietly make it bad again.

- **The Ledger** is the most correct *design* of the four — it matches, almost
  clause for clause, what 2026 research says a safe agent pipeline should look
  like. It is also the least effective in practice, because its verification
  step usually checks documents instead of code, its unit of progress is too
  big, and its notion of "landed" can drift away from the repository people
  actually work in.
- **Flashcards** has the best working rhythm — small, complete, demonstrable
  steps — but everything that caught a defect was a human paying attention.
  Its own post-mortem says the automated checks caught **zero of six** real
  defects.
- **Snowball** is closest to the industry-standard shape (small pull requests,
  a real test pipeline, trunk stays green) and adds one thing the others
  lack in working form: every merged change produces a runnable, checksummed
  release. Its failures were about checking the *intent* of work too late, and
  about assuming things about its environment instead of testing them.
- **Egor** is the same shape as Snowball run at twice the speed — twenty PRs
  and five releases in seven days — and it is the cautionary result. The
  measures that were mechanically enforced held perfectly (zero banned
  constructs across 9,295 added lines). The measure that mattered — does the
  shipped thing work when you run it — had no mechanism at all, and a broken
  dashboard shipped, stayed shipped, and was found by a person days later.
  Its one human checkpoint was lowered "temporarily" to unblock a merge and
  never restored.

Each flow owns a piece the others are missing. The conclusion at the end is
that the Ledger should stop competing with a project's native flow and instead
wrap around it — supplying the pieces only it has (frozen intent, an
independent judge, a serialized landing, a permanent record) while borrowing
the projects' small steps and real checks.

---

## 1 · What a ratchet is, and why everyone is talking about them

A mechanical ratchet is a toothed wheel with a small catch — the **pawl** —
resting against it. The wheel turns freely one way; the pawl stops it turning
back. Tension a strap, click, click, click — let go, and it holds.

In software the metaphor means: **a mechanism that lets quality move forward
and makes moving backward impossible** — not discouraged, not frowned upon in
review, *impossible*, because a machine refuses it.

The practitioner literature converges on five properties of a good one:

1. **The teeth are made of CI, not willpower.** "Quality is a ratchet, not a
   pendulum. The teeth are made of CI" — the
   [Ratchet Principle](https://insanelygreat.com/ratchet-principle.html).
   A rule enforced by social pressure decays as people tire; a rule enforced by
   a machine holds forever at no ongoing cost.
   [LeadDev's taxonomy](https://leaddev.com/software-quality/introducing-quality-ratchets-tool-managing-complex-systems)
   sorts ratchets by how much human maintenance they need — fully automated
   (type checkers, linters), semi-automated (tests), and process-based
   (incident reviews) — and warns that stacking up too many process-based ones
   paralyzes a team.

2. **Small teeth beat big teeth.** The canonical software ratchet is the
   [Not Rocket Science Rule](https://github.com/carbon-language/carbon-lang/issues/1552),
   popularized by Rust's bors bot: the main branch only ever advances to a
   commit that has passed every test. The increment is one commit. A ratchet
   with fine teeth engages constantly and can never slip far; one with coarse
   teeth slips a long way when it slips.

3. **The pawl must sit outside the wheel.** Whatever does the checking must
   not be editable by the thing being checked. The sharpest recent result here
   is from agent research:
   [tests written by the same agent that wrote the code cannot be trusted](https://arxiv.org/pdf/2602.07900)
   — agents systematically produce tests their own code passes while real
   defects sail through, and independent or pre-frozen test suites
   substantially outperform. The graded party must not hold the marking
   scheme.

4. **A ratchet needs a release valve.** In practice you cannot jump from
   "3,000 lint violations" to "zero" in one move.
   [Dusty Burwell's survey of working ratchets](https://www.dustyburwell.com/2019/05/29/ratchets)
   shows the pattern that survives contact with reality: record a baseline,
   block only *new* violations, tighten gradually, and keep an allowlist for
   deliberate exceptions. Ratchets without slack get routed around — but the
   valve must spring back. A relaxation with no mechanism to re-engage is not
   slack; it is the ratchet quietly becoming a freewheel.

5. **Layer the checkpoints; save the human for last.** As agents produce more
   code, having a person read every change stops working.
   [Layered supervision](https://arxiv.org/pdf/2608.26316) argues for a
   funnel: cheap automated checks first, machine-assisted screening second, and
   focused human judgment only on what filters through — redistributing human
   oversight rather than removing it. Related current work treats
   [independent verification of an agent's patches](https://arxiv.org/pdf/2608.08950)
   and [governance of many interacting pull requests as a queue](https://arxiv.org/pdf/2608.02685)
   as their own research problems.

And four failure modes, which matter more than the properties:

- **The soft pawl.** A check that passes without gripping — a test that
  asserts a constant equals itself, a green pipeline that never executed the
  thing that shipped. Worse than no ratchet, because the clicking sound
  creates confidence the mechanism doesn't back.
- **The jammed wheel.** The ratchet holds its position while the world moves
  on around it, so "held" quietly becomes "stale".
- **The wrong axis.** Monotonic progress on a measure nobody's outcome depends
  on, while the axis that matters regresses freely.
- **The lifted pawl.** A checkpoint relaxed by hand under pressure, with
  nothing that puts it back.

Keep those four in mind. Each of the flows below fails as a different one —
and one of them fails as two at once.

---

## 2 · Flow one: what the Ledger forces

The Ledger is the browser surface of `daedalus-control`, the host-side daemon
that owns Tasks, Jobs and Artifacts. The flow it imposes, end to end:

1. **Create.** A Task states its objective, its deliverables, optionally a
   programme and a rationale, extra acceptance checks and a budget. At this
   moment three things are **frozen**: the base commit (pinned to a target
   only the daemon itself can move), the acceptance policy (hashed, read from
   that commit, immune to later edits), and the container image (by digest).
2. **Dispatch.** An agent runs headless, once, in an isolated git worktree
   checked out clean at the frozen base. Process exit ends the attempt. The
   agent can drive the Task no further than *candidate* — "I think it's done."
3. **Verify.** The daemon — never the worker — grades the committed result in
   a fresh container with the network off, after first restoring every frozen
   acceptance file to its original state, so a Job that edited its own exam
   graded nothing.
4. **Review.** A second, separate agent reads the diff against the objective
   and rationale and writes a judgement. It is advisory by design: it moves
   no state, because a model reading untrusted input must not hold authority.
5. **Approve.** A human seals or rejects, per project policy.
6. **Integrate.** A transaction: serialize per repository → rebase onto the
   current target → **re-verify the merged result** → compare-and-swap the
   target forward. Landing is atomic and raced safely.

Alongside: budgets on attempts, wall-clock and review cycles; every refusal
typed and carrying its remedies; every event in an append-only log; agents on
a separate socket whose consequential requests become proposals a human must
confirm.

### Read as a ratchet

This is a ratchet *machine* — several interlocking ones:

- The **integration target** is a textbook pawl. Only a completed, re-verified
  integration advances it; nothing can move it backward; workers cannot move
  it at all — it is a database row, not a git ref, so there is no ref an agent
  could rewrite that the mechanism reads. That design decision is directly
  validated by the 2026 finding above about agents gaming their own tests.
- The **frozen oracle** is property 3 done structurally rather than by
  convention.
- The **append-only event log** and the monotonic attempt counters are
  ratchets on record and spend.
- The **one operations table** driving CLI, daemon and browser alike is a
  consistency ratchet: no surface can disagree about what a state allows.

### Where it jams

- **Soft pawl.** For any project that doesn't vendor its dependencies, the
  hermetic verifier cannot run a real build (network off, no cache — backlog
  #74), so it falls back to the built-in check: a documentation linter. The
  daemon's own honest tally: of the machine oracle's first seven verdicts,
  **one** was a statement about the work being graded. The ratchet clicks; the
  pawl is gripping butter.
- **Coarse teeth.** The unit of advance is an entire Task — an objective, an
  attempt, a grading, a seal, a landing. Compare one commit (Not Rocket
  Science Rule) or one slice (flashcards, below). When a big tooth slips — a
  rejected attempt — the whole objective's work is at stake, and the ladder of
  retry/replan/refine exists precisely to manage how much slips.
- **Jammed wheel.** The target moves only when work integrates or someone
  syncs it by hand. Measured on a real host: it sat four days and seven
  commits behind the branch, and a Task was dispatched against a tree in which
  the feature it touched didn't yet exist. And because landing moves no
  branch, "integrated" is not "on the branch I'm looking at" — a landing can
  be complete and invisible.

One more, about the human tooth specifically: the person holding the seal is
shown the objective, the programme and the rationale — **but not the diff**.
There is no change view in the Ledger and no `task diff` command. The
reviewing agent sees more of the change than the person with the authority.

---

## 3 · Flow two: flashcards M28

Flashcards is a browser-only flashcard app — no server, no backend. Milestone
28 replaced a core concept across 74 files in about 40 hours, and its
post-mortem is a defect study: thirteen findings, six of them real defects in
the app.

The flow: work happens in **vertical slices** under a walking-skeleton rule —
at every commit the app must have a complete working path from input to
output. Each slice ends with the full unit suite (~2,100 tests) and a real
browser pass (~890 checks in Chromium), whose summary line a human reads by
rule: *"if you did not read the summary line, you did not run the check."*
Slices merge to master per release, through a pull request with two CI jobs.

### Read as a ratchet

- The **walking skeleton is a genuine ratchet with fine teeth**: "the app
  works end to end" is re-established every slice, hours apart. Every defect
  the milestone injected was found and fixed *inside the slice that introduced
  it* — the slipping tooth never slipped further than one slice. This is the
  best tooth size of the four flows.
- But the **pawl is a person**. All six real defects were found by importing a
  deck and looking, by renaming a thing and watching, by reading a constant
  and asking what an older reader would do. The post-mortem is unflinching:
  the automated checks were green on every slice — 22 CI runs, zero failures —
  and caught none of the six, because they were structurally unable to see
  those defect classes. Teeth made of attention, not CI: the exact thing the
  Ratchet Principle warns about, since attention is finite and has bad days.
- Two named findings are pure ratchet failures. A test asserted a version
  constant against itself — true at any value — a **tooth that doesn't
  grip**. And a decision recorded on day one ("this feature is retired")
  shipped fully intact 37 hours later, because **no mechanism connected a
  recorded decision to its execution**; a human habit caught it.

The milestone's action items, read as a set, are one project migrating its
ratchets from process to tooling: pin version literals, sweep for vacuous
assertions, fingerprint stored shapes, cap fixed waits, forbid harness helpers
that swallow their own timeouts, and require every "decided" to name a shipped
version or an open ticket. That last one — the post-mortem calls it A6 — is
the cheapest and most portable idea in any of the three documents.

---

## 4 · Flow three: Snowball MVP

Snowball is a Go chat runtime for enterprise content management, taken from
first vertical slice to a packaged MVP in ten days: 12 merged PRs, one PR
deliberately discarded, 42 commits on master.

The flow: **one small pull request per change**, written test-first
(red→green), through Forgejo CI running Go and Node tests directly, merged by
a human. A deterministic scenario runner replays multi-turn conversations
offline — real runtime, scripted model and tool boundaries — so behaviour
checks are reproducible without credentials. And distinctively: **every
successful same-repository PR build publishes a runnable development
prerelease** — a Linux tarball with checksums, tied to its exact commit.

### Read as a ratchet

- This is the closest of the four to the Not Rocket Science Rule: small
  teeth, real checks, trunk stays green.
- The **per-PR runnable release is a working delivery tooth.** The Ledger's
  "integrated" produces no running artifact; flashcards' "released" is a
  version number, with deployment a manual step whose execution nobody
  recorded (its post-mortem literally cannot say whether an affected version
  ever reached a device). Snowball's flow makes *delivery itself* click
  forward one runnable artifact at a time.
- Its three failures are all checkpoint failures rather than tooth failures:
  - **The intent had no checkpoint.** A request for install instructions grew
    into a token-authenticated download script; the mismatch was caught only
    after a complete implementation and PR existed. The post-mortem's fix
    (PM-09) is a checklist naming intended audience, shortest path, and
    explicit non-goals — an intent checkpoint *before* implementation. This is
    the one thing the Ledger already does that the projects lack: a Task
    states objective and deliverables at create, before any agent runs.
  - **The environment was assumed, not tested.** CI treated the presence of a
    Docker client as evidence of a Docker daemon; they are separate
    dependencies, and the pipeline failed before any test ran. The packaged
    app's storage path was likewise never exercised by any test until a person
    ran a release by hand. "Packaging success was treated as stronger evidence
    of runtime readiness than it was."
  - **The pipeline lives inside the diff it judges.** Workflow files sit in
    the repository, editable by any change — the independence property the
    Ledger's frozen oracle gets structurally, a plain PR flow does not have.

---

## 5 · Flow four: a week on Egor

Egor is a Kotlin/JVM mail-handling service — nine Maven modules, PRINCE2
governance, XP practices, conventions encoded in `CLAUDE.md`. Its post-mortem
covers one week of agent-delivered work: **82 commits, 20 pull requests, 18
merged, five releases**, the test suite up 39% to 497, directed throughout by
the maintainer in a deliberate cadence: *"TDD red pattern, separate branch,
deliver a PR"*, then *"merge PR N and continue with the next item."*

The same shape as Snowball — small PRs, forge CI, red-first — run at roughly
twice the speed, with agents doing the writing and the human checkpoint thinned
to minutes per merge. It is the stress test of the PR flow at agent cadence,
and the result is precise: **everything a mechanism enforced held, and
everything no mechanism covered shipped wrong.**

### What held

The enforced axes ratcheted beautifully. Zero `!!` assertions, zero
`lateinit var`, zero hand-rolled JSON across 9,295 added lines; 42 of 42 new
files carrying the required header; a 1.31:1 test-to-main churn ratio.
Conventions written into `CLAUDE.md` propagated to every cold agent session —
a genuinely automated ratchet, and proof that this class of tooth works on
agents at full cadence. Branch protection gripped once, refusing a PR opened
against the wrong base. And PR bodies honestly recording *what had not been
verified* turned the final day's defect hunt from a search into a checklist.

### What escaped, read as a ratchet

- **The wrong axis.** The week's monotonic measures — test count, standards
  compliance, changelog discipline — all climbed. The axis that mattered,
  *does the shipped artifact work when you run it*, had no teeth at all: the
  dashboard is a static resource, so a page rendering its own source code was
  still valid HTML and 475 tests passed around it; README commands and
  `.env.example` values were text, written from intention, wrong until a
  person ran them — which was always the maintainer, always after release.
  More lines changed in markdown than in main source, and eight commits exist
  purely to correct earlier claims. The post-mortem's own summary: *"the
  safeguards that existed worked… none of them were designed to notice that a
  page renders wrong or that a README command does not run."* A perfect
  ratchet, clicking on the wrong wheel.
- **The lifted pawl.** `required_approvals: 1` on the development branch was
  lowered on the final day to unblock one merge — and never restored (action
  item C7). A checkpoint that can be relaxed under pressure with nothing that
  re-engages it is not a ratchet; it is a freewheel that remembers having been
  one. Notably, this is the failure mode the Ledger's design specifically
  refuses: its waiver (`--ignore-result`) waives one verdict on one artifact,
  recorded, with the verdict itself left standing — slack with a spring.
- **Merging unexecuted work, knowingly.** PR #17 stated plainly that its own
  test had never been run and asked the reviewer to build and open the
  dashboard before merging — *"a request the process had no step to honour."*
  The checkpoint existed as a sentence in a PR body, not as a mechanism.
  Honest text is a record; it is not a pawl.
- **The release ratchet clicked on the wrong trigger.** Five releases in six
  days while features landed; zero releases since — while the P1 fix sat on
  `development` with sixteen other commits and both README download commands
  kept serving the broken build. Delivery advanced on feature cadence and
  froze exactly when severity demanded a click. And unlike Snowball, Egor
  *had* per-push builds, per-PR builds and a single-archive release — the
  artifacts existed; nothing ever started one. **A delivery tooth without an
  execution pawl is decoration.**
- **Concurrency without a queue.** Three agent sessions on the repository in
  one day produced two incompatible fixes for the same defect 35 minutes
  apart, one PR against the wrong base, and 10% of the week's PRs discarded.
  This is [queue-level PR governance](https://arxiv.org/pdf/2608.02685) as a
  live incident rather than a benchmark — and it is the problem the Ledger's
  serialized landing and per-project claims already solve.

---

## 6 · Side by side

| | **Ledger** | **Flashcards** | **Snowball** | **Egor** |
|---|---|---|---|---|
| Unit of advance (tooth size) | one Task — coarse | one slice — fine | one PR — fine | one PR — fine, fast |
| What the pawl is | frozen policy in a hermetic container | a person reading | Forgejo CI, real tests | CI compile + unit tests; a minutes-long human merge |
| Pawl actually grips? | **usually no** — falls back to a docs linter (#74) | yes, but made of attention | yes, for what tests cover | for domain code yes; **for the shipped artifact there were no teeth** |
| Pawl independent of the wheel? | **yes, structurally** — frozen at a target workers can't write | n/a — same person both sides | no — workflow files are in the diff | no — CI took three attempts at itself, one reverted |
| Intent checked before work? | **yes** — objective, deliverables, rationale at create | design doc, unenforced | no — the PR #12 failure | a one-line brief per item; assumptions recorded in the PR, not checked before it |
| Decision → execution linked? | partly — states are machine-held; docs aren't | no — the retired-feature failure | no — action items untracked | no — the lowered approval was never restored; items "to file" |
| Delivery ratchet | none — integrate produces no artifact | none — manual deploy, unrecorded | **runnable prerelease per PR** | artifacts per push/PR/release — **never executed** |
| Record of what happened | **append-only event log, typed refusals** | commit messages + lessons list | PR history | PR bodies honestly stating what was not verified |
| Cost per click | high — create, dispatch, verify, review, seal, land | low | low | very low — minutes |
| Failure mode exhibited | soft pawl · coarse teeth · jammed wheel | human pawl · a tooth that didn't grip | late intent checkpoint · assumed environment | **wrong axis · lifted pawl** |

---

## 7 · What the research adds

Three results from the 2025–26 literature bear directly on this comparison.

**The Ledger's architecture is where the field says things are heading — and
Egor's week is the argument for it.**
[Layered supervision](https://arxiv.org/pdf/2608.26316) — automated checks
first, machine-assisted screening second, focused human judgment last, because
per-change human review doesn't survive agent-scale volume — describes the
Ledger's verify → advisory review → human seal funnel almost exactly. Egor
shows what the paper predicts: at twenty agent PRs a week, the human merge
step compressed to minutes, then to a checkpoint someone lowered to keep the
queue moving. Review alone did not scale, live, in seven days.

**The frozen oracle is the piece a plain PR flow cannot replicate.**
[Rethinking the Value of Agent-Generated Tests](https://arxiv.org/pdf/2602.07900)
finds agents reliably game tests they control, and recommends precisely what
`daedalus-control` built: oracles fixed before the work exists, held outside
the worker's reach. On any forge, the workflow files are part of the diff —
Egor's CI was edited, broken and reverted by the same stream of changes it was
judging. This is the Ledger's genuine, hard-to-copy advantage — currently
spent on a linter.

**Coordinating many small changes is its own problem.**
[BulkPR-Bench](https://arxiv.org/pdf/2608.02685) treats queue-level governance
of interacting PRs as a benchmark task, and
[Agent Capsules](https://arxiv.org/html/2605.00410v1) studies when merging
parallel agent work is safe. The Ledger's serialized, re-verify-merged,
compare-and-swap landing already answers the question these papers pose — it
is a merge queue, built before the term arrived here. Snowball merged 12 PRs
without one and was fine; Egor ran three uncoordinated sessions in a day and
produced colliding fixes and discarded work. The difference is not the
mechanism's presence — it is the cadence that makes its absence bite.

---

## 8 · The synthesis

Each flow owns something the others need:

- **The Ledger owns intent and independence.** Frozen objective and oracle, a
  judge the worker can't influence, a landing that re-verifies the merged
  result, an append-only record, authority derived from a socket rather than a
  claim, and a waiver that springs back instead of staying lifted. Machine
  consistency, best in class.
- **Flashcards owns tooth size.** Hours-scale increments, each leaving a
  complete working path, so no defect outlives its slice.
- **Snowball owns the delivery tooth in working form.** Every advance ends in
  a runnable, checksummed artifact.
- **Egor owns two proofs.** Positive: convention ratchets (`CLAUDE.md`
  standards) hold perfectly on agents at full cadence — that class of tooth
  transfers. Negative: at agent speed, every axis without a mechanism fails,
  no matter how disciplined the axes with one — and artifacts that are
  produced but never started protect nothing.

The failure analysis is symmetrical. The Ledger's weaknesses — soft pawl,
coarse teeth, jammed wheel — are exactly the projects' strengths; the
projects' weaknesses — editable checks, unchecked intent, decisions that don't
execute, checkpoints that stay lifted, attention as the last line — are
exactly the Ledger's strengths. These are not four competitors; they are one
mechanism drawn in four incomplete sketches.

**The conclusion:** the Ledger should wrap a project's native flow rather than
replace it. Concretely, in the order the evidence supports:

1. **Harden the pawl** — let the landing step run the project's own real
   pipeline against the merged commit (the direction the earlier
   `reviewing-and-landing.md` design already worked out, arrived at here
   independently from the ratchet side), or warm dependency caches into the
   pinned image so the hermetic verifier can run real builds (#74). Either
   way, stop clicking on a linter. Egor's C3 and C5 — execute the README in
   CI; make "built and run" a merge requirement — are the same item written
   from the project side.
2. **Add the missing decision ratchet** — flashcards' A6, as a `docs lint`
   rule: every *settled / decided / retired / FIXED* names a shipped version,
   a commit, a test, or an open item. Cheapest item on the list, and this
   repository fails it today in about a dozen places. Egor's never-restored
   approval setting is the same defect in configuration rather than
   documents: a relaxation nothing re-engages.
3. **Add the delivery tooth — with an execution pawl.** A landing should be
   able to produce a runnable artifact, Snowball-style, and something must
   *start* it — Egor had per-push builds nobody ever ran, and a broken
   dashboard shipped straight through them. Producing the artifact and
   executing the artifact are two teeth, not one. And couple release cadence
   to severity: a P1 fix triggers a click; it does not wait for the next
   feature batch (Egor's C6).
4. **Un-jam the wheel** — surface target lag where the operator already looks
   (partly done), and treat adoption as part of the flow rather than an
   afterthought, so the ratchet's held position and the branch people work on
   cannot silently diverge for days.
5. **Show the diff at the seal** — a human tooth engaged blind is a soft pawl
   with a conscience. The reviewer sees the change; the person with authority
   must too. At Egor's cadence the human checkpoint thinned to rubber-stamping
   in minutes; the answer is not more minutes, it is layering (research §7) —
   machines filter, the human decides the filtered few, and what reaches the
   human includes the change itself.

The tooth-size question — whether a Task could ever click forward in
slice-sized increments instead of objective-sized ones — is real but larger,
and nothing above depends on answering it first.

---

## Sources and references

**Practitioner writing on ratchets**

- [Introducing quality ratchets: a tool for managing complex systems — LeadDev](https://leaddev.com/software-quality/introducing-quality-ratchets-tool-managing-complex-systems) — the three-type taxonomy; the warning about process-heavy ratchets
- [The Ratchet Principle — InsanelyGreat](https://insanelygreat.com/ratchet-principle.html) — "the teeth are made of CI"; forbidden backslides; the rejection of "later"
- [Ratchets: improving systems incrementally — Dusty Burwell](https://www.dustyburwell.com/2019/05/29/ratchets) — baselines, allowlists, warn-then-block, gradual tightening
- [ratchet — language-agnostic code-quality ratchet](https://github.com/leonkacowicz/ratchet) — snapshot metrics, block regression, always allow improvement
- [Consider "not rocket science" over "revert to green" — carbon-lang #1552](https://github.com/carbon-language/carbon-lang/issues/1552) — the Not Rocket Science Rule and per-commit green trunks

**Research (2025–2026)**

- [When Review Alone No Longer Scales: Layered Supervision in AI-Assisted Software Engineering](https://arxiv.org/pdf/2608.26316) — the checkpoint funnel; human judgment last and focused
- [Rethinking the Value of Agent-Generated Tests for LLM-Based Software Engineering Agents](https://arxiv.org/pdf/2602.07900) — agents game their own tests; frozen/independent oracles win
- [Independent Patch Verification for Coding Agents with a Bidirectional Reconstruct-and-Verify Framework](https://arxiv.org/pdf/2608.08950) — verification separated from production
- [BulkPR-Bench: Benchmarking Queue-Level Governance of Interacting Pull Requests](https://arxiv.org/pdf/2608.02685) — many interacting PRs as a governance problem
- [Agent Capsules: Quality-Gated Granularity Control for Multi-Agent LLM Pipelines](https://arxiv.org/html/2605.00410v1) — when merging parallel agent work is safe
- [Automated Self-Testing as a Quality Gate: Evidence-Driven Release Management for LLM Applications](https://arxiv.org/pdf/2603.15676) — release checks as evidence, not ritual

**This repository and the three projects**

- `post-mortems/flashcards-m28-postmortem.md` (local, not committed) — the six-defects-zero-caught study; the vacuous assertion; the unexecuted decision; action item A6
- `post-mortems/snowballv2-POST_MORTEM.md` (local, not committed) — the Docker client/daemon inference; the packaged-release startup failure; the discarded PR #12; per-PR development releases
- `post-mortems/egor-postmortem-week-2026-09-19.md` (local, not committed) — 82 commits and 20 PRs in seven days; zero banned constructs beside a shipped broken dashboard; the merge of knowingly unexecuted work; the approval requirement lowered and never restored; five releases on feature cadence and none for the P1 fix
- `docs/control-plane.md` — the task lifecycle, frozen oracle, budgets, integration transaction, and the honest one-verdict-in-seven tally
- `docs/reviewing-and-landing.md` — the earlier design for landing through a project's own pipeline, whose conclusion this analysis reaches independently
- `docs/post-mortem-lessons-for-daedalus.md` — the companion report: the flashcards and Snowball post-mortems read for specific defects and mechanisms
- `BACKLOG.md` #74 (the hermetic verifier cannot run real builds), #79 (landed ≠ on your branch), #89 (target lag)

**Method note.** The descriptions of the Ledger's flow are from this
repository's code and documents, largely verified in the companion report. The
descriptions of the three projects' flows are from their post-mortems alone —
self-reports, with the numbers taken on trust. The research summaries are from
the cited papers as read on 2026-09-26; the layered-supervision and
agent-tests papers were read in full, the others at abstract depth.
