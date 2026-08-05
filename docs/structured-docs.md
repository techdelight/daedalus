# Structured project documents

Daedalus reads a project's own markdown as its source of truth. The web
dashboard's "project journey" — Purpose → Arc → Backlog — is not stored
anywhere; it is *derived*, on every request, from four hand-authored files in
the project root:

| File         | Supplies                              | Parser                 |
|--------------|---------------------------------------|------------------------|
| `VISION.md`  | Purpose (raw markdown)                | — (served verbatim)    |
| `ROADMAP.md` | The milestone arc                     | `core.ParseMilestones` |
| `SPRINTS.md` | Sprints, the current one, its items   | `core.ParseSprints`    |
| `BACKLOG.md` | The backlog pool                      | `core.ParseBacklog`    |

The documents stay human-authored markdown. There is **no** YAML frontmatter,
no sidecar `.json`, no generated data file — nothing a tool writes and a human
must not touch. What follows is a *contract*: the small set of conventions the
parsers rely on. Keep to them and the dashboard stays accurate; a document that
strays still renders (the parsers are total — see below), it just carries less.

## Guarantees the parsers make

Every parser is **pure and total**: it takes a string, returns data, performs
no I/O, and never errors. A half-written or malformed document is a normal
state for a live project, so a parser must keep rendering whatever it *can*
read rather than fail the view. Two consequences:

- **Absence is not an error.** A missing file yields an empty result, never a
  failure. A project that has not written a `ROADMAP.md` has no milestones —
  that is a fact about the project, not a fault to report.
- **The unparseable is skipped, not fatal.** A heading that doesn't match the
  convention is passed over; the rest of the document still parses.

Because each parser sees only one file, none can catch a contradiction *between*
files. That is what [validation](#validation) is for.

## ROADMAP.md — the milestone arc

Milestones are `###` headings anywhere in the file:

```
### Milestone 4: Layered Runner/Coordinator Architecture (In Progress)

Prose under the heading, verbatim, until the next heading. A bullet list
survives as a bullet list.
```

- **Number** — the integer after `Milestone`. Numbered from 1.
- **Title** — the text after the colon, up to an optional trailing status.
- **Status** — the parenthetical, **pinned** to exactly `(Done)`,
  `(In Progress)` or `(Paused)`. No parenthetical means **Planned** — the
  deliberate-future default. The pin is load-bearing: a permissive parenthetical
  would swallow a title like `Rework (Phase 2)` as status "Phase 2". An
  unrecognised parenthetical therefore stays part of the title, which is what it
  is. A **Paused** milestone is one that was started (or planned) and then
  parked — see [Paused](#the-paused-state).
- **Description** — every line under the heading until the next heading (of any
  level), trimmed and newline-joined.

Milestones are deliberately **tri-state with no per-milestone percentage**.
Progress is itemised at the sprint level, where the work actually lives; a
milestone-level number could only ever be invented.

## SPRINTS.md — sprints and the current one

Sprints are `###` headings; the current one sits under a `## Current Sprint`
section, history under `## Sprint History`.

```
### Sprint 41: Trust-Prompt & Runner Terminal Fidelity (v0.40.0)

Goal: one-line statement of the sprint's aim.

Milestone: 4

| # | Item                          | Status      |
|---|-------------------------------|-------------|
| 1 | Pre-seed workspace trust      | Done        |
| 2 | Initial PTY sizing            | In Progress |
| 3 | Repaint-on-attach             |             |
```

- **Number / Title** — as for milestones.
- **Version** — an optional trailing `(vX.Y.Z)`. The `v` is required inside the
  parens; it is stripped from the stored value.
- **`Goal:`** — an optional single line.
- **`Milestone: N`** — an optional link to the milestone this sprint advances.
  It is a **line of its own**, never a `[M4]` tag in the heading — a heading tag
  would be absorbed into the lazy title group and silently drop the version.
  A missing or non-numeric value is `0`, meaning *unlinked*. Sprint history
  predating the convention is expected to be unlinked; that is not a fault.
- **`Status: Paused`** — an optional line that parks the sprint. Like
  `Milestone:` it is a header-block line of its own. It is normally **absent** —
  a sprint's state is derived from its version and items (see below), not
  stored — and set only to take the sprint out of that flow. See
  [Paused](#the-paused-state).
- **Item table** — `| # | Item | Status |` rows. An empty status cell is
  **Pending**.

> The `Milestone:` line has its own parse guard, independent of the `Goal:`
> line, so a sprint whose `Goal:` precedes its `Milestone:` links correctly
> regardless of order. `SPRINTS.md` places them `Goal:` then `Milestone:` on
> purpose, so the real document exercises that ordering.

### Sprint phase — the ship pipeline

A sprint's **phase** is where it sits in the ship pipeline. It is *derived*, not
stored: `core.PhaseOf` reads it off a sprint's `Version` and its item statuses,
so a phase is always consistent with the document and never drifts from it.

| Phase        | Derived when                                        | Meaning                                   |
|--------------|-----------------------------------------------------|-------------------------------------------|
| `Paused`     | a `Status: Paused` line is present                  | parked — the override wins outright       |
| `Shipped`    | `Version` is set                                    | cut a release — the version wins outright |
| `Ready`      | no version, and **every** item is `Done`            | built, awaiting the verify/ship gate      |
| `Building`   | no version, some items done or `In Progress`        | work in flight                            |
| `Proposed`   | no version, and **no** items (or all `Pending`)     | declared, not started                     |

The order of the checks is what makes them unambiguous: `Status: Paused` is
tested first, so a parked sprint reads as `Paused` whatever its version or
items; then `Version`, so a released sprint reads as `Shipped` even if its table
looks incomplete; the itemless / all-pending case reads as `Proposed`; all-done
reads as `Ready`; anything else is `Building`. The active-milestone sidebar groups a
milestone's sprints by these phases (Building → Ready → Proposed → Shipped),
with `Ready` — the verify/ship gate — the most prominent.

### Planned sprints — declaring the Proposed ones

Future sprints are declared under a `## Planned Sprints` section, the same shape
as `## Current Sprint` / `## Sprint History`. A planned sprint is just a header
and a `Milestone: N` link, with **no item table**:

```
## Planned Sprints

### Sprint 46: Mobile Sprint Overlay

Milestone: 6
```

An itemless sprint is not dropped: it parses to a `Sprint` with its milestone
link and empty items, which `PhaseOf` reports as `Proposed`. That is how a
milestone shows the work still ahead of it — a real Proposed bucket in the
pipeline — before anyone has written its table.

## BACKLOG.md — the pool

Numbered rows in a table: `| # | Item |`. Everything after the number, up to the
trailing pipe, is the item description.

## Status vocabulary

One shared, case-sensitive vocabulary spans milestones and sprint items
(`core.Status`):

| Text          | Meaning                        | Constant            |
|---------------|--------------------------------|---------------------|
| `Done`        | finished                       | `StatusDone`        |
| `In Progress` | under way                      | `StatusInProgress`  |
| `Paused`      | started, then parked           | `StatusPaused`      |
| *(empty)*     | sprint item, untouched         | `StatusPending`     |
| *(no marker)* | milestone, not started         | `StatusPlanned`     |

The casing is exact. `In progress` (lowercase *p*) is **not** `In Progress`: it
parses fine, is carried through verbatim, and then compares unequal everywhere —
the item silently stops counting as in progress rather than failing loudly.
Validation catches this; a parser will not.

## The Paused state

`Paused` is for a milestone or sprint that was started (or planned) and then
deliberately put on hold — distinct from `Done` (finished), `In Progress`
(under way) and the not-started defaults (`Planned` / Pending). It is written
differently on each, matching where each already carries its state:

- **A milestone** is paused with a heading parenthetical, exactly like its other
  statuses: `### Milestone 8: Later Work (Paused)`.
- **A sprint** is paused with a `Status: Paused` line in its header block, beside
  `Goal:` and `Milestone:`. A sprint's phase is normally *derived* (from its
  version and items), so this line is the one way to override that flow;
  `core.PhaseOf` reports `Paused` ahead of every other phase. Resuming a sprint
  is simply removing the line.

Paused does not disturb the invariants: it is not `In Progress`, so a paused
milestone does not count toward the one-current-focus rule, and a paused current
sprint sitting on a paused milestone is a coherent state, not a drift the
validator flags.

## Lifecycle tools

The documents above are hand-authored, but the milestone/sprint **lifecycle** —
adding, removing, moving, and starting/finishing/pausing them — is also exposed
as MCP tools on the in-container `project-mgmt` server, so an agent can manage
the roadmap without hand-editing the markdown. Prefer the tools: each makes a
**surgical, prose-preserving** edit (via `core`'s document writer, which changes
only the lines it must and leaves every other byte identical), then **validates
the whole result** before writing — a change that would leave the docs
inconsistent (a second milestone `In Progress`, a dangling milestone link,
finishing a milestone whose sprint is still open) is refused with a clear
message and nothing is written.

| Tool                                          | Effect                                                        |
|-----------------------------------------------|---------------------------------------------------------------|
| `add_milestone{title, description}`           | append a new milestone (auto-numbered, Planned)               |
| `remove_milestone{number}`                    | delete a milestone                                            |
| `start_milestone{number}`                     | mark it `In Progress` (refused if another already is)         |
| `finish_milestone{number}`                    | mark it `Done` (refused while a current sprint links to it)   |
| `pause_milestone{number}`                     | mark it `(Paused)`                                            |
| `add_sprint{title, milestone, items[]}`       | add a sprint at the top of Current Sprint (items all Pending) |
| `remove_sprint{number}`                       | delete a sprint                                               |
| `move_sprint{number, to_milestone}`           | re-link a sprint to a different milestone                     |
| `start_sprint{number}`                        | resume a paused sprint (remove its `Status: Paused` line)     |
| `finish_sprint{number, version, force?}`      | roll it into history with `(vX)` (refused if items unfinished unless `force`) |
| `pause_sprint{number}`                         | add a `Status: Paused` line                                  |

The read side is **derived from files** too (Backlog #52): `get_progress`
reports the version (`VERSION`), vision (`VISION.md`), and the current sprint's
completion (its Done/total ratio) — there is no self-reported state to keep in
sync.

## Validation

`core.ValidateDocs(milestones, sprints)` is the one thing that holds every
document at once and so can see what no single-file parser can. It is itself
pure and zero-I/O — the caller reads the files and hands over the parsed
results. It returns `[]Finding`, each an `error` or a `warning`:

- **error** — documents that *contradict* each other: a sprint linked to a
  milestone that does not exist; a current sprint whose milestone is not the one
  the roadmap marks in progress; duplicate milestone or sprint numbers; two
  current sprints; none or several milestones in progress.
- **warning** — documents that parse but *lose information*: a current sprint
  with no milestone link; an item status outside the vocabulary (the casing
  trap above).

Absence is never a finding. Checks that need milestones are skipped when there
is no `ROADMAP.md` rather than failing — the docs badge already reports which
documents are missing, and saying it twice would bury the findings that matter.

`ValidateDocs` reasons over the *parsed* structs, so there is one thing it
structurally cannot see: a heading the parser *rejected* never becomes a struct.
A mistyped `## Milestone 4:` (wrong level) or `### Milestone 4 Foo` (no colon)
is silently dropped from the arc, and no consistency check over the parsed
result can mention what was never parsed. `core.LintHeadings(doc, content)`
catches this in the raw text: it flags any heading that *looks* like a milestone
or sprint entry (`### Milestone N`, `### Sprint N`) but does not match the strict
format, as an **error**. It skips fenced code blocks, so a phasing diagram that
draws `M4 (In Progress)` inside a fence is left alone.

### The gate

`daedalus docs lint [--ci] [dir]` runs both — the heading checks, then the
cross-file checks — over a project's `ROADMAP.md` and `SPRINTS.md` and reports
them as one list. It exits **non-zero on any error**, and with `--ci` on any
warning too, so it can gate a commit, a session, or CI. A test holds this repo's
own documents clean, so a future drift in Daedalus's own docs fails there rather
than shipping.

## Retrieval

The dashboard fetches the whole journey in **one** request:

```
GET /api/projects/{name}/overview
  → { vision, milestones[], currentSprint, backlog[] }
```

Painting the view spanned four documents and once cost four round-trips; this
is the one. The granular endpoints (`/milestones`, `/vision`, `/sprints`,
`/backlog`, `/strategic-roadmap`) remain for their other consumers. On the host
side, `mcpclient.Client` exposes the same reads (`ReadMilestones`,
`ReadSprints`, `ReadBacklog`) against the project directory directly.

A missing document is an **empty section** in the response, never a 404: the
dashboard's job is to show a project the shape of its own gaps, so failing the
whole view because one file is absent would hide exactly what it exists to
reveal. (`/vision` alone 404s an absent `VISION.md` — that endpoint is *about*
one document, so its absence is the answer.)
