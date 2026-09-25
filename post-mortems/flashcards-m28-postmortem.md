# Postmortem — Milestone 28, *A Study Is The Thing You Are Working Through*

**A note on the form.** The template this follows
(`docs/POST_MORTEM.md`, adapted from Google's *Postmortem Culture: Learning from
Failure*) is written for incidents — an outage, a failed deploy, a service
degradation. M28 was none of those. It was a planned milestone that shipped, and
nothing broke for any learner that anyone knows of. It is written up in this form
anyway because the interesting material is the same material an incident review
looks for: defects injected, how each was detected, and which safeguards were
present and silent. Where a section of the template has no analogue here
(latency, failed requests), it says so rather than inventing one.

Blameless throughout. This is a single-maintainer project working with an
implementation agent; "who" is never the useful answer, and in a two-participant
system it is never even an interesting one.

---

## 1 · Incident details

| | |
|---|---|
| **Title** | M28 — *A Study Is The Thing You Are Working Through*: development postmortem |
| **Period** | 2026-09-22 04:12 UTC → 2026-09-23 20:16 UTC (design commit → v0.105.0 on `master`) |
| **Affected software** | `flashcardsv2` — a browser-only, client-side flashcard app. **There is no server, no backend and no listening port**, so there is no service to be down. The surfaces changed: what a lesson draws from, the day's new-card allowance, extra practice, the study editor, the stored settings shape, the export format, and the *Prioritise new cards* feature (removed) |
| **Releases** | v0.104.0 (seven slices, merged 2026-09-23 17:25 UTC) and v0.105.0 (backlog #56, merged 2026-09-23 20:16 UTC) |
| **Document status** | **Published** 2026-09-25, at the accountable owner's direction. The action items in §8 are not yet tracked in `BACKLOG.md` |
| **Accountable owner** | dstibbe (operator and maintainer) |
| **Contributors** | Claude Code (implementation agent); Forgejo Actions runner (automated checks) |

---

## 2 · Executive summary

M28 replaced four half-modelled concepts — a *Mix*, a `SessionScope`, an
`activeMixId` and the open deck — with one the learner names and selects: a
**study**. It shipped in seven slices over about 40 hours, then a follow-up
release removed *priority*, a concept M28 had decided to retire. Both releases
are on `master`, both fully green in CI, and no learner-visible failure is known.

**Thirteen defects and process faults were recorded during those two days.
Six of them were defects in the app. Not one of those six was detected by a
pre-existing automated check.** They were found by reading code, by importing a
deck and looking at the screen, and by reasoning about when a control is read.
Meanwhile the automated checks were green on every slice: 22 CI runs across the
eight commits, both jobs, every one a success (measured — see §9).

Two findings carry most of the value:

- **A format version spent an entire slice describing a shape that had already
  changed.** `BUNDLE_VERSION` stayed at 9 through slice 6, which altered the
  stored settings shape. An older reader would have read such a file perfectly,
  dropped every study a learner had made by hand along with its daily rate, and
  destroyed them on the next export. The tests in place asserted the constant
  *against itself*, which is true at any value. Caught by reading, before
  release.
- **A decision was recorded and not executed.** Priority was settled as retired
  on 2026-09-22 and was still fully live in v0.104.0 — panel, tags, export file
  and all. v0.105.0 removed it 2h17m later. The project's own rule about writing
  decisions down is what made the gap visible; nothing mechanical connected the
  decision to its execution.

---

## 3 · Impact

**No known learner-visible failure.** All seven slices were developed on a
branch. `master` received two merges, each with both automated jobs passing.

### Quantified scope

| Measure | Value | Source |
|---|---|---|
| Elapsed development | ~40 hours over 2 calendar days | commit timestamps |
| Code changed, whole milestone | 74 files, +6,929 / −8,265 lines | `git diff --shortstat 56f41cb da853e0` |
| Largest single slice | slice 3 — 39 files, +2,314 / −2,601 | `git diff --shortstat` |
| Findings recorded | 13 | `git diff 56f41cb da853e0 -- CLAUDE.md` |
| App defects among them | 6 | classification, §6 |
| App defects caught by a pre-existing automated check | **0** | §6 table |
| CI runs over the period | 22 runs, 0 failures, 2 cancellations | Forgejo Actions API |
| Unit tests at milestone close | 2,082 | slice 7 commit message |
| Browser checks at milestone close | 894/894 | slice 7 commit message |

Latency, error rates and failed-request counts have **no analogue** in this
system: it is a local browser application with no requests to fail.

### The one user-facing exposure

v0.104.0 shipped the *Prioritise new cards* control after the decision to remove
it had been recorded. A learner who set a priority on v0.104.0 has it **silently
dropped** when they reach v0.105.0 — read and discarded, deliberately not
converted into a study, because a priority *ordered* the day's draw where a
study's rule *narrows* it, and converting would have quietly cut their lesson
down to those tags instead of leading with them.

- Window on `master`: **2h17m** (17:25 → 19:42 UTC, 2026-09-23).
- Real-world exposure: **unknown.** Deployment to the static host is a manual
  procedure documented in `DISTRIBUTION.md` §2, not automated, so whether
  v0.104.0 ever reached a device is not recorded anywhere this review can read.
  If it was never deployed, exposure is zero.
- No cards, decks, schedules or review history were at risk in either case.

### Harm avoided rather than suffered

The `BUNDLE_VERSION` miss is the one that would have cost a learner real data —
every hand-made study, with its daily rate, silently dropped by an older reader
and then destroyed on the next export. It never shipped. It was caught by reading
the constant and asking what an older reader would do, not by anything that ran.

### Rework

- The published slice plan was **wrong about a dependency**: the storage change
  scheduled for slice 7 had to move into slice 6, because an editor that writes
  tags is a requirement on where tags are stored, and a study derived from a mix
  on every load had nowhere to put them.
- Slice 4 ran the full browser pass **twice** — once for the slice, once to
  confirm a repair to the test harness itself.
- Backlog #56 required two full browser passes for the same reason.

---

## 4 · Background

Terms a reader outside this project will need.

**The app.** `flashcardsv2` is a spaced-repetition flashcard application that
runs entirely in a browser tab. Data lives in the browser's own IndexedDB. There
is no account, no sync and no server component. "Release" means a version number,
a changelog entry and a merge to `master`; getting the bundle onto a device is a
separate manual step.

**Architecture.** A pure core (`src/core/*`) that may not touch the DOM, storage,
Three.js or the clock; adapters at the edges for persistence and time; and a
composition root, `src/main.ts`, which wires elements to behaviour. The core is
unit-testable in isolation. **The composition root is not**: importing it runs it,
and it wants a live document. So many of its assertions read it *as text* and
check that a function contains a phrase. That structural limit explains several
of the detection failures below and is not new — it is recorded in the project's
own lessons list as far back as v0.34.0.

**A slice.** This project works in vertical slices under a walking-skeleton rule:
at every commit the app has a complete working path from user input through
processing to output. Seven slices, each demoable, rather than one large merge.

**The two instruments.**

1. The **unit suite** — Vitest, ~2,100 tests at the time, of which a substantial
   number are source-text readings of the composition root rather than executions
   of it.
2. The **browser pass** — `smoke/run.mjs`, driving real Chromium through
   Playwright against the Vite dev server, ~890 checks. It prints a summary line
   (`N/M checks passed`) at the end. It exits non-zero for a **failure** and also
   for a **crash**, so an exit code cannot distinguish the two; the summary line
   is the only thing that can. This project learned that the hard way and the
   rule "if you did not read the summary line, you did not run the check" is
   written into its guidance.

**CI.** A Forgejo Actions workflow added nine days before M28: a fast `check` job
(typecheck, unit tests, production build) and a slow `browser` job that runs the
browser pass and asserts the summary line exists. `browser` depends on `check`.
Both run on push and on pull request.

**A study** (the thing M28 built). A named rule over a subject's cards — which
decks, and which tags within them — with a daily new-card allowance of its own.
It holds no cards; it is resolved against the card pool when a lesson is built.

**A priority** (the thing M28 retired). A name plus tags, deciding which unseen
cards the day's allowance was spent on *first* — an ordering, never a filter.
Everything due stayed due regardless.

**`BUNDLE_VERSION`.** A number stamped into every export file. A reader refuses a
file whose version is higher than its own. It exists to make an older build
*refuse* a file it would otherwise misread, and the rule for bumping it is
written at the constant: bump when an older reader would misread the file, where
"misread" includes reading it perfectly and then doing harm with what it dropped.

---

## 5 · Timeline and recovery

All times **UTC**. Sources: commit timestamps, commit message bodies, and the
Forgejo Actions API.

| Time | Event |
|---|---|
| **2026-09-22 04:12** | Design committed: `flashcards-study-design.md`, with the operator's three decisions and their stated costs |
| 04:28 | §7a written: **priority is retired**. The decision is recorded, the code is left in place, and that gap is filed as **backlog #56** rather than implied |
| 04:55 | **Slice 1** — `core/study`: the type, the rule, the query. Pure, wired to nothing. Written test-first. 2,092 unit tests, 863/863 browser checks. *The tests corrected the design*: the sketch had an empty deck list mean "every deck", which collides with how an empty tag list reads |
| 09:27 | **Slice 2** — subjects carry studies, derived from their mixes on load. Nothing on screen changes yet. 2,103 unit tests, 863/863 |
| 14:36 | **Slice 3** — Home selects a study; the open deck stops deciding the lesson. Largest slice (39 files). 2,078 unit tests, 868/868. **Three defects found and fixed inside the slice**, two of them real: a derivation placed at the read rather than the write, and a derived field that preserved a name the learner could not edit |
| 18:50 | **Slice 4** — the day's allowance belongs to the study. 2,093 unit tests, **878/878 twice**. Two defects: a control left stale on a shut panel, and a fixed 350 ms wait in the test harness that killed an unrelated group half a minute later |
| 22:35 | **Slice 5** — extra practice follows the study; backlog #55 closes as a consequence. 2,094 unit tests, 884/884. Found by reading: two functions walking the same structure in two loops, one of which had lost its only production caller |
| **2026-09-23 12:23** | **Slice 6** — the study editor writes a rule, with tags. 2,110 unit tests, 894/894. The slice absorbed work planned for slice 7 (see §6, plan faults). A browser check reported *0 cards* and looked like a defect; the app's own newly written warning, printed in the failure's detail line, showed the check had ticked the wrong deck |
| 15:42 | **Slice 7** — `core/mix` deleted; milestone closes. 2,082 unit tests, 894/894. **`BUNDLE_VERSION` moved 9 → 10 here**, one slice after the shape it describes changed. The first browser pass of this slice **crashed with no summary line**; the cause was a helper that had been swallowing its own timeout for twenty sprints |
| 16:23 | Release commit: changelog and architecture document brought up to date. The 0.104.0 changelog section was found to have duplicated text, a lost slice-4 entry, and the version bump filed under *Removed*; rewritten |
| 17:25 | **v0.104.0 merged to `master`** (PR #11). CI run 69: `check` and `browser` both pass. Priority is still fully present in the app |
| 19:42 | **Backlog #56 committed** — priority retired in code, red-first. 42 files, +689 / −3,556. 1,992 unit tests, 859/859 browser checks. Two faults in the *replacement checks* surfaced and were fixed (§6) |
| 20:16 | **v0.105.0 merged to `master`** (PR #12). CI run 72: `check` and `browser` both pass |
| 2026-09-24 | v0.105.0 tagged. Eight earlier releases (v0.103.11 – v0.104.0) found untagged and backfilled; the tag push triggered CI on eight historical commits, one of which reported a `browser` failure on months-old code. Noise, self-inflicted, and recorded here so it is not read later as a regression |

**Detection and recovery pattern.** There was no single detection moment and no
restoration, because nothing was ever broken in front of a learner. Every defect
was found and repaired inside the slice that introduced it, which is the
slicing discipline working as designed. What varies — and what §6 is about — is
*how* each was found.

---

## 6 · Causes and trigger

### Trigger

There is no single initiating event. The initiating **condition** is the shape of
the work: M28 replaced a concept that had four partial representations spread
across two levels of a hierarchy, touching 74 files. A change of that shape
injects defects in proportion to the number of boundaries the data crosses — a
fact this project had already paid for once, in Sprint 77, and written down as
*adding a field is not one edit, it is one edit per boundary the data crosses*.

### The defects, and what found each one

| # | Defect | Slice | Real app defect? | Found by |
|---|---|---|---|---|
| 1 | Studies derived only where settings are *read from storage*, so a mix saved in-session never became a study until the next reload. Home was missing the study the learner had just made | 3 | **Yes** | Importing a deck and looking at the screen |
| 2 | A derived field preserved a study's `name`, so renaming a mix left Home showing the old name — the learner could only edit the mix's name | 3 | **Yes** | Renaming a mix and watching the screen |
| 3 | Home's study chooser refreshed by the acts that happened to change it — a list that cannot be kept complete | 3 | **Yes** | Reasoning about where the control is *read* |
| 4 | *New cards a day* left stale on a shut Settings panel while the study in force moved underneath it | 4 | **Yes** | Same reasoning, applied to a panel |
| 5 | `BUNDLE_VERSION` left at 9 through a slice that changed the stored settings shape | 6→7 | **Yes** (unshipped; data-loss class) | Reading the constant and asking what an older reader would do |
| 6 | A standing sentence in the editor told every learner the opposite of what the app did, for two slices — it promised a mix would not change the daily new-card count, and slice 4 gave each study its own rate | 6 | **Yes** | Reading the surface's standing text |
| 7 | `everythingInDeck` and `everythingInDecks` walked the same structure in two loops; one lost its last production caller and survived only in tests | 5 | Dead code | Reading |
| 8 | `enterStudy` waited a fixed 350 ms; under load an unrelated group died 30 seconds later on a perfectly healthy lesson | 4 | Test harness | The harness accusing the app in the wrong place |
| 9 | `leaveAnyPanel` returned quietly when a press changed nothing, converting a stuck panel into a 30-second timeout elsewhere and a **crash with no summary line** | 7 | Test harness | The crash — noticed only because the summary line was absent |
| 10 | A browser check ticked one named deck and reported 0 cards, which read as an app defect | 6 | Check error | The app's own new diagnostic, printed in the failure detail |
| 11 | Changelog section for 0.104.0: duplicated text, a lost slice-4 entry, version bump under the wrong heading | release | Document | Reading before release |
| 12 | A source sweep over `index.html` read only half the file — it stripped HTML comments but not the stylesheet's, and the file is two-thirds stylesheet | #56 | Sweep design | The sweep firing on stylesheet comments |
| 13 | Two replacement checks were wrong in the shape of the checks they replaced: one asserted *visibility* of a section that is legitimately hidden under the default scheduler; one demanded a property only the deleted half had ever satisfied | #56 | Check design | The unit suite and the browser pass failing |

**Six real app defects (1–6). None was detected by a pre-existing automated
check.** Three automated detections occurred (8, 9, 13) and all three were about
the *instruments*, not the app: two cases of the harness misreporting where a
problem was, and one case of freshly written checks being wrong.

### Why the safeguards were silent

Each of these is structural, not a matter of attention.

- **The composition root cannot be imported, so its tests read text.** A text
  assertion is satisfied by the presence of a phrase. Defects 1–4 are all about
  *when* code runs, which no amount of reading the source as a string can settle.
  For defect 1 the commit record is explicit: the unit suite passed because the
  normalizer does derive correctly, and the browser pass had never reached the
  group that would have noticed.
- **A constant compared to itself is true at any value.** The tests around
  `BUNDLE_VERSION` asserted `toGoalBundle(x).version === BUNDLE_VERSION` — a
  tautology. Nothing asserted the **literal**, so nothing could fail when the
  shape changed and the number did not.
- **Nothing asserts that a standing sentence is *true*.** Defect 6 is a string
  constant. It was correct when written, falsified by a later slice, and no
  mechanism exists that could notice.
- **A staleness defect has no failing state to observe.** Defects 3 and 4 show
  correct-looking values; they are only wrong relative to an event that happened
  while nobody was looking at them.
- **The browser pass cannot distinguish a failure from a crash by exit code**,
  and helpers that swallowed their own timeouts turned a failure at the real
  subject into a crash somewhere unrelated. Defect 9 had been latent for twenty
  sprints, in a helper almost every check calls.
- **CI runs the same two instruments.** Its green is evidence about regression
  against what those instruments already cover. It is not independent evidence,
  and treating 22 green runs as reassurance about defect classes neither
  instrument can see would be the mistake this document most wants to prevent.

### Plan faults, which are a separate class

- **A slice plan did not ask where a new control's value is stored.** Slice 6 was
  specified as "the editor becomes the study editor" and slice 7 as "`Mix` is
  deleted". Slice 6 turned out to be impossible alone: a study derived from a mix
  loses its tags the moment they are set, because a mix has nowhere to keep them.
  The storage change had to move forward a slice, bringing deck-deletion pruning,
  import re-keying and a module's list operations with it. **The prerequisite was
  discoverable before any code was written**, by asking of each new control:
  *where does what this writes get stored, and is that what the app reads?*
- **Nothing connected a recorded decision to its execution.** Priority was
  settled retired at 04:28 on day one and shipped intact 37 hours later. What
  caught it was a human habit — this project's rule that decisions must be
  written down where they can be seen — plus a backlog entry. No check exists
  that could have failed.

---

## 7 · Lessons learned

### What worked

- **Slicing.** Seven slices, each leaving a complete working path, each with its
  own browser pass. Every defect was found and fixed inside the slice that
  created it. The alternative shape — build the core, then storage, then the UI,
  then integrate — would have surfaced defects 1 and 2 at the end, together,
  with two days of work stacked on top of them.
- **Red-first.** Every slice's commit message records the specific failure it
  started from (`studyInForce is not a function`, then a behavioural failure,
  then green). In slice 1 the tests corrected the design before any caller
  existed: an empty deck list meaning "every deck" collides with an empty tag
  list meaning "no narrowing", and writing the test is what exposed it.
- **Writing the app's own diagnostics.** Defect 10 was legible in one reading
  because the slice had just added a warning for learners — *no card in those
  decks carries any of these tags yet* — and it printed in the failing check's
  detail line. A message written for a learner diagnosed a fault in the test
  harness. That is a strong argument for writing the sentence rather than leaving
  a state silent.
- **The summary-line rule.** Defect 9 produced zero failures and no summary line.
  Exit code alone would have read as an ordinary failure. The habit of reading
  the summary is the only reason six slices of work were not reported against a
  run that never finished.
- **Separating a concept's code from its argument.** When `core/lessonSource` and
  `core/mix` were deleted, their reasoning moved into `core/study`'s header and
  into the design document — including the argument the milestone *reversed*,
  which is exactly when it is most worth keeping. A reversal with its original
  argument beside it can be re-examined; one without it gets re-litigated.

### What failed

- **The automated checks as a detector for this class of change.** Measured: zero
  of six real defects. They are good regression checks and were not the thing
  that found anything here.
- **The slice plan's dependency ordering**, in one place, discoverably.
- **The link from decision to execution.** A decision recorded in a design
  document has no mechanical consequence.
- **Assertions that could not fail.** Two distinct vacuous-assertion shapes
  appeared in two days: a constant compared to itself, and a function arity check
  over parameters with defaults (which do not count toward arity, so the
  assertion was satisfied by a number nobody had verified).

### Where luck limited the damage

- **Defect 5 is the one to sit with.** Nothing in the system was going to catch
  it. It was caught because someone read a constant and asked a question about a
  hypothetical older reader. Had slice 6 shipped, the consequence was a learner
  opening their own backup on an older build and losing every partition they had
  made by hand — then having it destroyed on the next export. The distance
  between "caught by reading" and "shipped" here is one lapse of attention.
- **Defect 6 shipped and was live for two slices.** It was text rather than
  behaviour, so the cost was a learner being told the opposite of the truth about
  their daily new-card count. It was found by reading the surface, not by use.
- **Defect 9 had been latent for twenty sprints.** It surfaced during M28 by
  coincidence of which panel got stuck. It could have surfaced during any release
  in that span, and on a day when nobody checked for a summary line it would have
  been read as an ordinary failure in the wrong place.

---

## 8 · Action items

Typed as **prevent** (make the defect impossible), **detect** (make it fail
loudly), **mitigate** (reduce blast radius) or **process**. Each has a measurable
completion condition. Owners are the two participants in this project, named
honestly rather than distributed for appearances.

| # | Type | Action | Measurable condition | Owner | Priority | Tracking |
|---|---|---|---|---|---|---|
| A1 | detect | **Pin every format and schema version to a literal.** `BUNDLE_VERSION` and `DECK_BUNDLE_VERSION` are pinned as of #56. `DB_VERSION` (11) and `POOL_DB_VERSION` (10) in `adapters/persistence.ts` are **not, and cannot be** — they are module-private. Export them and assert the literals beside the argument for each number | `grep 'DB_VERSION).toBe('` returns 2 matches; the constants are exported | dstibbe | **High** | new backlog item |
| A2 | prevent | **Fail on vacuous assertions.** A sweep over `src/**/*.test.ts` rejecting self-comparisons (`expect(f(x).v).toBe(V)` where `V` is the constant `f` stamps) and arity assertions over defaulted parameters | The sweep exists, fails on a seeded example of each shape, and passes on the current suite | Claude Code, next session | **High** | new backlog item |
| A3 | detect | **Make a shape change require a version decision.** A test holding a structural fingerprint of the stored settings and bundle shapes, so altering either fails until the fingerprint and the version literal are both revisited | Adding or removing a settings field fails one named test | dstibbe | **High** | new backlog item |
| A4 | prevent | **Retire fixed waits in the browser harness.** Measured baseline: **543** `waitForTimeout` calls against **85** event-based waits in `smoke/`. Five instances of the fixed-wait fault are already recorded in the project's own lessons list. Convert the waits that stand in front of an assertion, then add a sweep capping the count so it cannot grow | `waitForTimeout` count strictly decreasing, with a sweep asserting the ceiling | dstibbe | **Medium** | new backlog item |
| A5 | detect | **No harness helper may swallow its own timeout.** A source sweep over `smoke/*.mjs` failing on a `catch` that returns without re-throwing inside a wait helper. Baseline: 36 `catch` occurrences in `run.mjs`, 5 in `app.mjs`, 9 in `shots.mjs` — each needs reading once | The sweep exists and every surviving `catch` is either re-throwing or annotated with why not | Claude Code, next session | **High** | new backlog item |
| A6 | process | **A decision recorded must name its execution.** Every line in `ROADMAP.md`, `SPRINTS.md` or a design document that says *settled* / *decided* / *retired* must name either a shipped version or an open backlog id. Checkable by a document sweep | The sweep exists and passes; it would have flagged §7a on 2026-09-22 | dstibbe | **High** | new backlog item |
| A7 | process | **A slice plan must answer, per new control: where is what this writes stored, and is that what the app reads?** One line per control in the slice specification | The next milestone's slice plan contains the answers | dstibbe | **Medium** | add to the slice template |
| A8 | detect | **Standing-sentence review as a step, not a habit.** For every surface a slice touches, read its standing text and ask whether each claim is still true. Defect 6 is the second instance of this class (the first was four stale directions in Sprints 58–60) | The step is in the pre-commit checklist in `CLAUDE.md`; the next slice records having run it | dstibbe | **Medium** | `CLAUDE.md` checklist |
| A9 | prevent | **A sweep over a file with two comment syntaxes must strip both.** `index.html` is two-thirds stylesheet. Audit existing markup sweeps for the same half-blindness | Every sweep reading `index.html` strips `<!-- -->` and `/* */`; verified by a seeded comment in each | Claude Code, next session | **Low** | new backlog item |
| A10 | process | **Tag at release time, and one tag per push.** Backfilling eight tags in one push started CI on eight historical commits and produced a `browser` failure on months-old code that means nothing | The next release is tagged in the same session it merges | dstibbe | **Low** | `CLAUDE.md` release steps |
| A11 | mitigate | **Decide whether v0.104.0 reached a device.** If it did, a learner's priority is silently dropped on update; a one-line note in the v0.105.0 changelog already explains the removal, but nobody has confirmed whether the intermediate release was ever deployed | The deployment status of v0.104.0 is recorded, and the changelog note is judged sufficient or not | dstibbe | **Medium** | this document, §3 |

**Deliberately not an action item: "be more careful."** Five of the six real
defects were found by careful reading, which means care was present and working.
Asking for more of it adds nothing. Every item above is a mechanism.

---

## 9 · References and evidence

**Commits** (all on `master`)

- `d6ed120` design · `56f41cb` §7a, priority retired by decision
- `2744977` slice 1 · `9f4e864` slice 2 · `1d6a924` slice 3 · `f9c5e2e` slice 4 ·
  `bea2f3f` slice 5 · `9745a94` + `86875c1` slice 6 · `54f1730` slice 7
- `9681d5b` release preparation · `e621aee` v0.104.0 merge (PR #11)
- `a8e5c67` backlog #56 · `da853e0` v0.105.0 merge (PR #12)

**Measurements**

- Code volume: `git diff --shortstat 56f41cb da853e0` → 74 files, +6,929 / −8,265
- Findings count: `git diff 56f41cb da853e0 -- CLAUDE.md` → 13 new entries
- CI: Forgejo Actions API, `/repos/dstibbe/flashcardsv2/actions/tasks`. Runs
  41–62 and 65–72 cover the M28 commits: **0 failures, 2 cancellations**
  (cancellations were superseded pushes, not results)
- Test and check counts per slice: taken from each slice's own commit message,
  which records the numbers that were read at the time
- Harness baselines: `grep -o 'waitForTimeout' smoke/*.mjs | wc -l` → 543;
  event-based waits → 85; version literals pinned → `BUNDLE_VERSION`,
  `DECK_BUNDLE_VERSION` only

**Documents**

- `flashcards-study-design.md` — the design, the operator's three decisions, and
  §7a where priority's retirement and its preserved evidence live
- `ROADMAP.md` § Milestone 28 — the milestone record, including what it did not do
- `CLAUDE.md` § Lessons Learned — all 13 findings, in the form the project keeps
  them
- `CHANGELOG.md` — v0.104.0 and v0.105.0, learner-facing
- `docs/POST_MORTEM.md` — the template this follows

**Marked unknown**

- Whether v0.104.0 was deployed to a device, and therefore whether the priority
  exposure window had any real subject.
- Why run 73's `browser` job failed on v0.103.11 (a commit from several sprints
  earlier, replayed by today's tag push). Job logs are a web-UI route requiring a
  session cookie and are not reachable with the API token available here, so a red
  run can be *seen* from this environment but not always diagnosed from it.

**Estimates, distinguished from measurements.** "~40 hours elapsed" is derived
from first and last commit timestamps and includes time not spent on this work.
The classification of each finding as app defect / harness / document is a
judgement made in writing this document, not a recorded field; the underlying
findings and their detection stories are quoted from contemporaneous commit
messages and `CLAUDE.md` entries.
