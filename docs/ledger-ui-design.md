# Two Focuses for the Ledger

**A design pass on the Ledger's shape, written 2026-09-05 after the question
"what is happening in *this* project?" turned out to have no answer on the page.**

Status: **DESIGNED, NOT BUILT.** What exists is this document and two working
mockups that render the real CSS against faked data:

- `internal/web/static/ledger-project-preview.html` — the per-project focus
- `internal/web/static/ledger-programme-preview.html` — the programme focus
- `internal/web/static/ledger-next.css` — the real stylesheet both load

Open them in a browser. They are the same kind of artifact
`control-preview.html` already is, and they exist for the same stated reason:
the visual result cannot be checked from a terminal.

**And they have not been looked at yet, which this document has to say rather
than imply.** They were authored in a headless container with no browser, no
Node and no Playwright, so what has actually been checked is structural: the
stylesheets balance, the markup nests, every class the pages use resolves in one
of the three sheets, and the scripts' strings and brackets close. None of that is
the check that matters. #95's fix passed every assertion somebody had thought to
write and was caught by **looking at a screenshot** — so the first thing anybody
does with this design is open the two files, and the design is unverified until
they have.

---

## The complaint, in one sentence

*The Ledger is one flat list of everything, sectioned by status, and a list
sectioned by status can answer "what needs me right now" and cannot answer
"where is this project up to" or "what shape is this programme in".*

---

## What the Ledger is today, and the two questions it cannot answer

The page is a two-pane menu: a list on the left grouped into `Running`,
`Blocked`, `Awaiting you`, `Proposed`, `Programmes`, `Landed`, and a description
window on the right holding whichever row the cursor is on
(`internal/web/static/control.css:133`, the split; `control-preview.html` for
every state it renders). It is a good interrupt queue. It was built to answer
"is anything waiting on me", and `BoardView.PendingApprovals` and
`PendingProposals` exist because that is the question it was built around
(`internal/control/board.go:142-146`).

Two other questions arrive at the same page and leave without an answer.

**"What is happening in `flashcards`?"** The project is not a heading, not a
filter and not a column. It appears in exactly one place: the description window,
as a single word beside the id (`.ledger-desc-project`, `control.css:166`). So a
project's work is scattered across six status sections, and reading it means
scanning every section and filtering by eye. `BoardView.Projects` — "every
project with a Task on the board, sorted" (`board.go:148`) — is already computed,
already on the wire, and drives nothing on the page.

**"What shape is this programme in?"** A programme is one row in the same list,
and its whole structure is flattened into the description window as prose:
declared edges as rows, undeclared edges as rows, external dependencies as rows,
member tasks as rows (`control-preview.html:331-367`). The plane computes a
genuinely graph-shaped answer — `Declared` with its `Enforced` flag and
`EnforcedBy` task edges, `Undeclared` for the edges the plan never mentioned,
`External` for the work it waits on from outside — and the page renders it as
three lists under three subheadings. **`ProgrammeStatus` is a graph and the
Ledger prints it as a table of contents.**

Neither of these is a missing-data problem. Every field named below is already
served. It is a focus problem: the page has one frame, the interrupt queue, and
two other jobs are being done inside it badly.

---

## What the research says

Three literatures and one video game converge on the same mechanic, which is
why the design below is built on it.

### Statecharts: show the machine, and gray out what it will not do

The XState Visualizer is the most-used interface in this space, and the thing it
does is small and specific. Kyle Shevlin's account of it
([kyleshevlin.com](https://kyleshevlin.com/xstate-visualizer/)) names three
affordances: **"the highlighted state (indicated by the blue color), is the state
the machine is currently in"**; **"the buttons inside each box will trigger the
event with that name, and move it into the state it points to"**; and, when an
event is not available, **"the state and buttons that are not enabled become
gray."**

The third is the one this design takes. The Ledger today renders **only** the
admitted commands — a `rejected` task shows nine plates, a `working` task shows
three, and nothing on screen says why the set changed or what the others need.
An operator cannot learn the machine by using it; they watch a toolbar mutate.
The visualizer's answer is to draw the whole vocabulary and dim what is out of
reach, which turns the command menu from a list of buttons into a picture of
the state.

Daedalus is unusually well placed to do this, because the admit-set is already
one table and already on the wire. `internal/control/operations.go` is "THE ONE
PLACE THAT ANSWERS *WHICH OPERATIONS DOES THIS STATE ADMIT?*", `GET /operations`
serves it as `OperationView{Key, Target, States, Summary}`
(`operations.go:376-396`), and `Summary` is a written one-line answer to "what
does this do" for every operation in the table. Everything a dimmed plate needs
to explain itself exists.

### Invariants: a state name is not a claim you can check

Morazán and Antunez's FSM visualization tool (arXiv
[2008.09254](https://arxiv.org/abs/2008.09254)) makes one design move worth
stealing: each state is associated with **an invariant predicate**, and during
execution the tool "indicates if the proposed invariant holds or does not hold
after each transition". The state label alone is not trusted to carry the
meaning; the thing that is supposed to be true in that state is checked and
displayed beside it.

The Ledger has invariants of exactly this kind and shows none of them. A
`verified` task is supposed to name a commit somebody can act on — and the case
where it does not is real enough that `usableArtifact` exists to say so in the
plane's own words (`internal/control/model.go:485-497`). A `blocked` task is
supposed to be waiting on something satisfiable; `BoardCard.Unsatisfiable`
already distinguishes the case where it is not (`board.go:120`). A `queued` task
is supposed to be waiting for a dependency, and `QueuedForCapacity` exists
precisely because "waiting for a slot" is the other thing that looks identical
and has a different remedy (`board.go:124`). Three invariants, three fields,
none of them a mark on the state.

### Process mining: a stage's mass, and the collection problem

Directly-follows graphs are the standard process-mining visualization: nodes are
activities, arcs are the observed follows-relation, and **each arc is annotated
with the frequency of that transition in the log**, so the picture carries where
the cases actually went rather than where the model says they could go. The
identified interface problem is not drawing one graph — it is that analysis is
"a laborious process that involves multiple data manipulation operations and
comparisons between the resulting DFGs", and tools "lack the ability to
uniformly manipulate and manage multiple DFGs in a consistent manner"
([Designing a User Interface to Explore Collections of Directly-Follows
Graphs](https://link.springer.com/chapter/10.1007/978-3-031-61007-3_4)).

That is this repository's situation almost exactly. Daedalus has **one** state
machine and **many** cohorts running through it — one per project — and the
useful comparison is between cohorts, not within one. It also has the event log
to do it honestly: `Event` carries `From` and `To` states with a `Reason`
(`store.go:1738-1749`), so the follows-relation is recorded, not inferred.

The design does **not** propose drawing a DFG. A fifteen-node graph with
frequency-weighted arcs is a research instrument, and an operator with four
tasks in a project needs to see four tasks. What it takes from this literature
is the discipline: *the interesting object is the cohort moving through the
machine, and the interesting comparison is between cohorts.*

### Final Fantasy IX: the ability chart

The Ledger is already a Final Fantasy menu — `control.css` opens by saying so,
and argues the item/magic menu is the right reference because it is "a list of
terse rows, a cursor, and a DESCRIPTION WINDOW". That reference has been fully
spent. FF9's *other* screen is the one this design needs.

FF9 does not have a sphere grid; its progression is the **equipment ability
chart**, and it works like this
([rpgclassics](http://shrines.rpgclassics.com/psx/ffix/abilities.shtml),
[Final Fantasy Wiki](https://finalfantasy.fandom.com/wiki/Final_Fantasy_IX_abilities)):

- **It is a matrix.** Rows are abilities; columns are the eight characters; each
  cell is the AP that character needs for that ability. A further column names
  the equipment that teaches it. One screen, two axes, and the cell is a number
  you can compare across the row.
- **Progress is partial and visible.** AP accrues into a per-ability gauge, the
  same way experience accrues toward a level. You are never told only "not
  learned" — you are shown how far in you are.
- **Progress is permanent.** A full gauge is marked with stars, and the ability
  is kept once mastered, whatever you equip afterwards.
- **The affordance list is derived from the current loadout, and unavailable
  entries are drawn dim.** In the equip menu the abilities a piece of gear would
  teach are listed under it, and — the detail that matters — an ability the
  character can learn appears in white while one they cannot appears in gray.

The last point is the XState visualizer's third affordance, in a 2000 PlayStation
menu, and it is the same mechanic Daedalus's `operations` table already
computes. **Three independent sources, one instruction: draw the whole
vocabulary, light what is reachable now, dim the rest, and say what the dim ones
need.** That is the strongest result in this document and it is the spine of both
focuses below.

The other three points give the per-project view its centrepiece. A task's status
word is exactly the "not learned" the AP gauge exists to replace: `rejected` does
not say the task reached `verifying` twice, and `planned` does not distinguish a
task nobody has started from one that was sent back from the approval gate for a
third pass. The event log knows the difference. The row does not show it.

---

## The design

Two focuses, each with its own frame, each answering one question. The existing
board **stays exactly as it is** and remains the landing screen — see
[What is deliberately not changed](#what-is-deliberately-not-changed).

### Focus 1 — the Project Ledger

*Frame: one project. Question: where is this project up to, and what can I do
next?*

**The header states the project's condition, not the plane's.** Four facts, all
already computed and none currently visible together: runner slots in use
against the project's limit (`PlaneStatus.ProjectRunning` against
`SchedulerLimits`), the integration target's lag with its divergence case
distinguished from its count (`TargetLag.Summary()`, the single rendering the
CLI and daemon already share), the merge queue this project serialises on
(`BoardCard.QueueID`), and the adoption row when the checkout branch does not
have the landed commit. Adoption is **per project already** — #79b settled that,
"because a branch lags by a COMMIT" — so a project frame is the frame it always
wanted.

**The centrepiece is the progression chart.** Rows are tasks. Columns are the
lifecycle stages, and each cell is a pip on a track drawn left to right:

```
            plan   run   cand  grade  gate  land        attempts
  T-14      ●──────●─────○─────○──────○─────○           ▓▓░ 2/3
  T-13      ●──────●─────●─────◆─────╴○─────○           ▓▓▓ 3/3  ← spent
  T-12      ●──────●─────●─────●──────◉─────○           ▓░░ 1/3
  T-10      ●──────●─────●─────●──────●─────★           ▓░░ 1/3
```

- **Filled `●` — reached and left.** Read straight off the event log's
  `From`/`To` pairs. This is the AP gauge: partial progress, made visible.
- **Lit `◉` — where it is now.** The visualizer's highlighted state.
- **Hollow `○` — not reached.**
- **`◆` — reached, and came back.** A stage entered and exited downward is the
  fact the status word destroys. `T-13` above is `rejected`; the chart says it
  got graded and was sent back, which is a different task from one that never
  compiled. The `Reason` on the event is the tooltip.
- **`★` — landed.** FF9's mastery star, and for the same reason: it is permanent
  and it should not look like just another filled pip.
- **The attempts gauge is the AP bar, literally.** `Budget` is authoritative on
  the Task (`model.go:366`), attempts spent are countable, and an exhausted
  envelope is the single most common way work stops. Today it is a number in a
  refusal you have to provoke.

Fifteen states collapse to six columns because six is what a person narrates.
The mapping is stated once, in the UI and in the code, and is not a new
authority: `planned`/`blocked` → **plan**, `queued`/`working`/`input_required` →
**run**, `candidate` → **cand**, `verifying`/`verified`/`rejected` → **grade**,
`approval_required`/`approved` → **gate**, `integrated` → **land**. The three
other terminals (`failed`, `cancelled`, `expired`) do not get a column; they get
a separate **Closed** section below the chart, because a cancelled task is not
at a stage.

**The invariant marks ride on the pip, not in a separate column.** A `blocked`
task whose dependency is in `Unsatisfiable` gets the refused colour on its plan
pip and the words "can never complete"; a `queued` task with
`QueuedForCapacity` gets "waiting for a slot, not for work" — the distinction
that field was added to make. A `verified` task whose artifact names no commit
gets the mark `usableArtifact` already has the sentence for. This is the FSM
paper's move: check the thing that is supposed to be true in the state, and say
so beside it.

**The command menu draws the whole vocabulary.** Every operation in
`AllOperations()` is on screen in the stable render order the table already
defines. Admitted operations are lit plates, exactly as now. The rest are dimmed
and carry their `Summary` plus the states they need — "verify · grade the
artifact against the frozen oracle · needs candidate". Nothing new is computable
here and nothing new is permitted: this is `GET /operations` rendered in full
instead of filtered down. The dangerous plates keep the styling they have
(`.ff-cmd.is-refuse`, `.ff-cmd.is-seal`) and waive keeps its confirmation.

This is where the design has to be careful about a rule it must not break. **A
dimmed plate is not an offer and must never become clickable-to-explain-then-do.**
The admit-set is enforced in the plane, and the page showing an operation is not
the page being allowed to call it. Drawing it is a claim about the machine, not
about this operator's authority — which is a separate table (`authority.go`) and
deliberately not merged with this one.

**What the backend still owes.** One thing, and it should be named rather than
hand-waved. The chart's reached-stages need the event log, and the log is
per-entity: rendering twenty rows means twenty calls to
`GET /tasks/{id}/events`. The honest fix is a `stagesReached` summary on
`BoardCard`, computed once where the board is already assembled — a fold over
`From`/`To` per task, no new authority and no new state. Everything else in both
focuses is served today.

### Focus 2 — the Programme Map

*Frame: one programme. Question: what shape is this, and does the plan match the
work?*

Projects are **nodes**, laid out in declared-dependency order, upstream on the
left. Each node carries its own roll-up: a stacked bar of that project's tasks
by stage (from `ByState`), its open and landed counts, and what is running now.
The node is the per-project view in miniature, and selecting it is how you get
there — which is the overview-to-detail move, and the reason the two focuses are
one design and not two.

**The edges are the point, and there are three kinds because the plane already
distinguishes three.** `programme.go` is unusually explicit that the two graphs
are not a mistake to merge away — the declared project→project edges "order a
plan" and the Task→Task edges "gate landings", and merging them "would give the
agent that can draft a plan the power to gate work". The document then names the
real defect: **"The defect was never the two graphs. It was that NOTHING EVER
COMPARED THEM."** `Declared`, `Undeclared` and `External` are that comparison.
Drawing them as one map with three edge styles is the comparison made visible.

- **Declared and enforced** — solid gold, labelled with the task edge doing the
  work (`EnforcedBy`, "T-12 → T-8"). The plan and the work agree.
- **Declared and not enforced** — dashed and dim, and it carries *why*, which is
  the part the current prose gets right and should keep. `UpstreamTasks` and
  `DownstreamTasks` are populated only in this case, and their emptiness is
  itself the answer: an edge with no candidate on one side "is not waiting for
  someone to declare it, it is waiting for the work to exist"
  (`programme.go:355-362`). Where there *are* candidates on both sides, the edge
  offers the dependency as an action. Where there are not, it says so and offers
  nothing.
- **Enforced and never declared** — drawn in the surprise colour, because this
  is the plan being wrong rather than incomplete. `UndeclaredEdge` names both
  tasks and both projects, so the edge can say "T-16 waits for T-11" on a
  `daedalus ← flashcards` arc the plan does not mention.

**External dependencies leave the frame.** A programme's edges to work outside it
are drawn crossing the boundary to a stub node, marked satisfied or unmet, and
labelled with the outside task's own programme when it has one — because
`ExternalDependency.Programme` exists precisely to distinguish "waiting on
another programme" from "waiting on unattached work", "and the difference is
what an operator acts on" (`programme.go:388-391`). `ProgrammeStatus.External`
is documented as "the reason the roll-up exists at all", and it is the one thing
a per-project view structurally cannot show. On the map it is the only edge that
crosses the border, which is the right amount of emphasis.

**A legend states the rule the map is drawing**, in the words `programme.go`
already uses: declared order plans, and it gates nothing. A map with two edge
kinds and no legend would read as one graph with cosmetic variation, which is the
exact misreading the two-graph split exists to prevent.

**Drawing them together found something while the mockup was being built, which
is the argument for drawing them together.** The fixture has `daedalus →
flashcards → langlearn` declared and an undeclared task edge `langlearn →
daedalus`, because those were the two most ordinary cases to demonstrate. Put on
one set of nodes they close a **loop** — the plan says langlearn is last and the
work says daedalus waits for it, and both cannot be right. Nothing was added to
the data to produce that; it fell out of drawing two graphs over the same nodes.
Both halves are already on the board today, under two different subheadings, and
nothing puts them next to each other. The map carries it as a finding above the
edge lists (`ledger-programme-preview.html`), and it is the clearest evidence
that the current surface is not merely less pretty than a map but less
informative than one. **Detecting the cycle is not proposed as a plane feature
here** — that would be a claim the plane owns and can be held to, and this
document is about a rendering.

---

## What is deliberately not changed

**The board stays, and stays the landing screen.** "Is anything waiting on me"
is a cross-project question and a cross-project list is the right answer to it.
Replacing it with a project picker would put a navigation step in front of the
one question the page currently answers well. The two focuses are reached *from*
the board — a project name on a row, a programme row — and the board keeps its
counts.

**No new authority, no new state, no new edges in `legalTransitions`.** Every
number, edge and mark above is a projection of a field the plane already
computes and serves. This is a rendering change with one additive backend
summary (`stagesReached`), and that is the whole intended scope. In particular
the dimmed command plates change nothing about what is permitted: the admit-set
is enforced in `requireOperable`, and a page that draws an operation is not a
page that may call it.

**Programmes are still formed, amended and dissolved from the CLI or by
confirming a proposal.** The map is a reading surface. `control-preview.html`
already records the reason — the programme entry carries no command plates — and
a map that offered "delete programme" would be a new authority arriving as a
side effect of a layout change. The one action the map does offer is *declaring a
dependency the plan already claims*, which is an existing operation
(`add_dependency`) applied to a pair the plane itself computed and suggested.

**The chart is not a DFG and does not aggregate.** No frequency-weighted arcs, no
"73% of tasks reach verified". With four tasks in a project that is noise
dressed as analysis, and the moment it stops being noise it becomes a claim
about the plane's own performance that nothing on the page can be held to.

**Sizes stay in `px` for chrome.** Backlog #96 is specific and its reasoning
holds here: prose scales in `rem`, chrome is sized to a grid and must not. The
progression chart is a grid, so its columns are `px`; the objective text and the
programme description are prose, so they inherit the prose tier.

---

## Open questions

**Where does a task with no programme appear on a map?** Nowhere, currently, and
that is probably right — but a project can hold work in three programmes and
work in none, and the per-project chart is the only place the unattached work is
visible. Whether the chart should mark which programme each row serves, or
whether that clutters the one view that is deliberately about a single project,
is not settled here.

**Does the chart survive twenty rows?** Six columns and a gauge is compact, but a
project with forty open tasks is a scroll, and the columns stop being scannable
once the eye has to travel. Sectioning by stage inside the chart would fix the
scan and destroy the left-to-right reading that makes it a progression chart at
all. Untested, and it needs a real project with real volume rather than a
mockup's twelve rows.

**Should the dimmed plates show the path, not just the requirement?** "needs
`rejected`" is honest and slightly unkind: from `candidate`, reaching `rejected`
is one legal move away and the page knows it, because `legalTransitions` is
right there. Computing "two moves away, via reject" is possible and is a second
answer derived from the most safety-relevant table in the package, which is a
good reason to leave it alone until somebody wants it.

---

## Sources

- Kyle Shevlin, [*State Machines: The XState Visualizer*](https://kyleshevlin.com/xstate-visualizer/) — current-state highlighting, events as buttons, and graying out what is not enabled.
- [XState Visualizer](https://stately.ai/viz) and [statelyai/xstate](https://github.com/statelyai/xstate) — the tool the above describes.
- Marco T. Morazán and Joshua M. Antunez, [*Visual Designing and Debugging of Deterministic Finite-State Machines in FSM*](https://arxiv.org/abs/2008.09254), arXiv:2008.09254 — per-state invariant predicates, checked and reported after each transition.
- [*Designing a User Interface to Explore Collections of Directly-Follows Graphs for Process Mining Analysis*](https://link.springer.com/chapter/10.1007/978-3-031-61007-3_4) — the collection-of-graphs problem, and frequency-annotated follows-relations.
- [*A Graphical Workflow Exploration Environment for Visual Analytics*](https://arxiv.org/pdf/2204.10221), arXiv:2204.10221 — overview-to-detail navigation over workflow structure.
- [*Final Fantasy IX* abilities](http://shrines.rpgclassics.com/psx/ffix/abilities.shtml) (RPGClassics) — the ability chart as an ability × character matrix with per-cell AP and an equipment column.
- [*Final Fantasy IX* abilities](https://finalfantasy.fandom.com/wiki/Final_Fantasy_IX_abilities) and [Ability Points](https://finalfantasy.fandom.com/wiki/Ability_Points) (Final Fantasy Wiki) — AP gauges, mastery marks, and learnable-versus-unavailable rendering in the equip menu.

## In this repository

- `internal/control/operations.go` — the one table of operation → admitted states, and `GET /operations` that serves it.
- `internal/control/programme.go` — `ProgrammeStatus`, and the two-graph argument the map draws.
- `internal/control/board.go` — `BoardView`, `BoardCard`, and the fields the invariant marks read.
- `internal/control/store.go:1738` — `Event`, with the `From`/`To` pairs the progression chart folds.
- `docs/no-dead-ends.md` — why the admit-set became one table, and what it cost when it was three.
