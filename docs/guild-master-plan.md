# The Guild Master: Total Plan & Proposal

*A self-contained document of the idea, the plan, and the milestones — written
for evaluation. The evidence base (research across academic / platform /
practitioner sources) lives in [`docs/guild-master-control.md`](guild-master-control.md);
this document is the decision-ready proposal that stands on its own.*

**Status:** proposal for review. Milestones M13–M17 are **Planned**, not started.
Nothing here is committed to build until this plan is evaluated and a milestone
is explicitly opened.

---

## 1. The idea, in one paragraph

Daedalus runs a *guild* of projects, each an autonomous coding agent in a
hardened container. **The Guild Master is the guild's leader** — an embedded,
un-removable project that today (as shipped in Milestone 12, v0.47.0) can *see*
every project's documents but cannot *act* on them. This plan evolves the Guild
Master from a **read-only programme overseer** into a **controlling entity**: one
that can verify, command lifecycles, dispatch work, gate transitions, and
coordinate a whole programme of projects — using only the levers Daedalus can
actually enforce, and refusing to pretend it has levers it doesn't.

## 2. Where we are today (the baseline to build on)

Milestone 12 shipped the Guild Master as a **supervisor by visibility**:

- An always-present, **un-removable** `guild-master` project (its own scaffolded
  workspace; refuses remove/prune/rename).
- **Read-only** cross-project document access — every project's directory mounted
  read-only at `/guild/<name>`, plus a `guild-mcp` server
  (`list_guild_projects` / `read_project_doc` / `guild_overview`).
- A distinguished, crowned hero in the Guild view.

It advises and plans; it does not command. This plan is about closing that gap
**honestly**.

## 3. The core thesis (the design principles this plan is built on)

Four principles, each drawn from the research and from Daedalus's own
architecture. They are the lens for evaluating every milestone below.

1. **Control must be *externally imposable*, not *cooperative*.** The research's
   sharpest distinction: cooperative control (in-process handoffs, shared graph
   state, `interrupt()` checkpoints) requires the worker to be *built into* an
   orchestration framework — impossible to impose on an autonomous CLI. Imposable
   control (session lifecycle over a sandbox, resource caps, verification via CI
   on the artifact, the merge gate, host-mediated permissions) works regardless
   of the worker's internals. **The Guild Master's hard guarantees must sit on
   the imposable column.**

2. **Verification is the crown jewel, not orchestration.** The MAST failure study
   found ~24% of multi-agent failures are "declared done ≠ verified done", 44%
   are the controller's own fault, and *structural verify gates beat prompt
   engineering*. Practitioners are blunt: "a lone agent grades its own homework; a
   fleet with a gate cannot." So the **first** capability is a machine-checked
   definition of done — everything else is worth less without it.

3. **Daedalus can gate at boundaries, never mid-turn — and that is enough.**
   Daedalus cannot pause an agent between tokens. But it *writes the container's
   Claude Code configuration*, and Claude's permission chain is
   `hooks → deny → ask → mode → allow`, where **`deny` beats even bypass mode**.
   So Daedalus can inject `PreToolUse` / `Stop` **hooks** — imposable gates at
   tool-call and stop **boundaries**. This revives the "gatekeeper" idea shelved
   earlier (when the conclusion was "we can only offer MCPs"): we gate at seams,
   not tokens.

4. **The Guild Master is a supervisor, never a puppeteer.** It orchestrates only
   genuinely parallel, well-specified work; its real product is *decomposition,
   specs, and gates*, not spawning agents. It stays authoritative
   ("return" semantics: dispatch → collect → decide), and every project agent
   keeps its autonomy. Multi-agent is *not* automatically better — the plan
   optimises for control quality, not agent count.

## 4. Targets — the capabilities that make control real

| ID | Capability | Externally imposable? |
|----|------------|-----------------------|
| **T1** | Machine-checked **verification gate** — `daedalus verify` (build + tests + `docs lint`), exit code is the gate, enforced by an injected Stop-hook | **Yes** |
| **T2** | **Lifecycle command + budgets** — GM start/stop/pause of any session via the coordinator; concurrency/turn/wall-clock/cost caps; auto-pause | **Yes** |
| **T3** | **Task dispatch → artifact** — hand a spec'd task to a project; collect a durable artifact (branch/commit + status), async, "return" semantics | Dispatch: yes; quality: no |
| **T4** | **Programme Task Ledger + Progress Ledger** — persistent plan/facts + per-cycle progress check with stall→replan and termination | **Yes** (GM-side) |
| **T5** | **Boundary approval gates** — approve milestone/sprint transitions + merges via `ValidateWrite` + a `PreToolUse` hook (PM-opt-in) | **Yes** at boundaries |
| **T6** | **Independent review pass** — a review agent / LLM-judge over the diff, gating integration | Partly |
| **T7** | **Non-destructive steering** — priority message to a running agent at a tool-call boundary, not `Ctrl-C` | **Yes** (runner + hook) |
| **T8** | **Cross-project coordination contract** — internal A2A-style task/status/artifact + a shared task board for dependencies | Partly |

## 5. The milestone plan (M13–M17)

Each milestone is a ~2-sprint unit in Daedalus's established rhythm, host-side
testable with the usual "the actual container run is host-only" caveat. Sequenced
by **leverage × dependency**: verification first (it is both the highest-leverage
capability and the thing the rest gate on), then the imposable lifecycle layer,
then dispatch, then the softer approval and coordination layers.

### M13 · The Verify Gate — *"done" means a machine checked it*
- **Targets:** T1, T6.
- **Goal:** a project's work is never "done" by self-report. A project declares a
  `daedalus verify` check (build + tests + `daedalus docs lint`); its **exit
  code** is the definition of done. Daedalus injects it as a Claude Code
  **Stop-hook** so the project agent cannot stop on red. The Guild Master reads
  each project's verify status.
- **Deliverables:** the `daedalus verify` contract + runner; the injected
  Stop-hook (runner-specific; graceful degradation for hook-less runners);
  verify status surfaced via `guild-mcp`/coordinator; an optional independent
  review-agent pass.
- **How we verify it (host-side):** unit tests for the verify runner + exit-code
  gate; the hook JSON injection tested against sample configs (`bash -n`
  entrypoint); a smoke that a red check blocks "done". The in-container hook
  firing is host-only.
- **Why first:** highest leverage (MAST), mostly imposable, and the foundation
  M15/M16 build on.
- **Open questions for evaluation:** How does a project *declare* its check —
  a `verify:` line in config, a `Makefile`/script convention, or a doc field?
  What is the default when a project declares none (skip vs. fail-open)?

### M14 · Lifecycle Command & Budgets — *the crowned hero commands the party*
- **Targets:** T2.
- **Goal:** the Guild Master can `start` / `stop` / `pause` any project's session
  through the coordinator (which already owns session lifecycles — the
  Devin/OpenHands "session over a sandbox" model), bounded by explicit budgets.
- **Deliverables:** GM control tools (`start`/`stop`/`pause_project`) over the
  coordinator, surfaced in the Guild view; concurrency caps + wall-clock/turn/cost
  budgets + auto-pause of stale sessions.
- **How we verify it (host-side):** coordinator command tests (start/stop/pause
  route correctly, budgets enforced, non-GM callers rejected); the actual
  container start/stop is host-only.
- **Why here:** pure externally-imposable control that needs *no* cooperation
  from the project agent, built on infrastructure Daedalus already has.
- **Open questions:** Who may command — the Guild Master's agent (via a control
  MCP), the human, or both? Is a stopped project's in-flight work checkpointed or
  discarded?

### M15 · Task Dispatch & the Programme Ledger — *visibility becomes orchestration*
- **Targets:** T3, T4.
- **Goal:** the Guild Master hands a well-specified task to a project (headless
  run or session injection) and collects a **durable artifact** (branch/commit +
  structured status) via async dispatch → poll → artifact, keeping "return"
  semantics so it stays authoritative. It maintains a persistent **Task Ledger**
  (plan/facts) + **Progress Ledger** (satisfied? looping? progressing? next?)
  with a stall→replan escape and explicit termination/budgets — the Magentic-One
  dual-ledger skeleton, doc-driven in the GM workspace.
- **Deliverables:** a GM→project dispatch tool returning an artifact + status; the
  Task/Progress ledger held as structured docs in the GM workspace; stall
  detection + replan; termination conditions.
- **How we verify it (host-side):** dispatch/collect logic tested against a fake
  project; ledger transitions + stall→replan unit-tested; the actual agent run
  is host-only.
- **Depends on:** M13 (so a dispatched task's "done" is verified) and M14 (so
  dispatch can start/stop a session).
- **Open questions:** headless `-p` run vs. injecting into a live session — which
  default? How is the artifact represented (a branch name + commit + a JSON
  status file)? Where does the ledger live so it survives GM restarts (its
  workspace docs vs. a dedicated state file)?

### M16 · Boundary Gates & Approval — *the gatekeeper, at the seams we can gate*
- **Targets:** T5.
- **Goal:** for **PM-governed** projects only (opt-in, off by default),
  milestone/sprint transitions and merges require Guild-Master/human approval —
  an interrupt-state the controller owns. Daedalus can't pause a turn, but it can
  gate a `project-mgmt-mcp` write or a merge at the boundary.
- **Deliverables:** an approval gate on `project-mgmt-mcp` writes (extend
  `ValidateWrite` + a `PreToolUse` hook) for transitions; a merge/integration
  gate; a per-project "PM enabled" opt-in (surfaced at install, default off);
  graceful behaviour for non-hook runners.
- **How we verify it (host-side):** `ValidateWrite`/gate unit tests (a governed
  transition is refused without approval, allowed with it); the hook path tested
  against sample configs.
- **Depends on:** M13's hook-injection substrate.
- **Open questions:** what grants approval — a human action, or the Guild Master's
  own agent reasoning over the change (and if the latter, is *that* a
  self-grading loop we must guard against)? How intrusive is acceptable before
  projects turn it off?

### M17 · Cross-Project Coordination & Steering — *the horizontal layer*
- **Targets:** T7, T8.
- **Goal:** a non-destructive **steering channel** to redirect a running project
  agent at a tool-call boundary (runner injection + a `PreToolUse` hook surfacing
  queued steering) instead of a destructive `Ctrl-C`; and an internal A2A-style
  task/status/artifact contract + a **cross-project task board** for
  dependencies, composing with the existing `programmes` feature.
- **Deliverables:** the steering channel (priority message delivered at a
  tool-call boundary); a `Task` lifecycle contract (submitted → working →
  input-required → done/failed) between the GM and projects; a shared task board
  for cross-project dependencies.
- **How we verify it (host-side):** steering-message queue + boundary-surfacing
  logic tested; the task/board state machine unit-tested; live delivery is
  host-only.
- **Depends on:** M13–M15 (steering and coordination presuppose dispatch,
  lifecycle, and a definition of done).
- **Open questions:** is the coordination contract truly A2A (a public spec) or a
  lighter internal shape over the coordinator API + MCP? Does the task board live
  in the GM workspace or in a shared coordinator-owned store?

## 6. Sequencing & dependencies

```
M13 Verify Gate ──┬─► M15 Task Dispatch + Programme Ledger ──► M17 Coordination & Steering
   (hooks + done) │        (needs done + lifecycle)                (needs dispatch)
                  │
M14 Lifecycle ────┘─► (independent of M13; both feed M15)

M13 (hook substrate) ──► M16 Boundary Gates & Approval  (opt-in; can slot after M13)
```

- **M13 and M14 are the independent foundations** — M13 gives a real "done" and
  the hook substrate; M14 gives imposable lifecycle control. Either could go
  first, but **M13 first** maximises leverage and unlocks M16.
- **M15 needs both** (dispatch a task whose "done" is verified, on a session it
  can start/stop) and adds the ledger.
- **M16** can follow M13 at any point (it only needs the hook substrate) and is
  independently valuable, but it is *opt-in* and intrusive, so it is scheduled
  after the universally-useful M13–M15.
- **M17** is last — steering and coordination presuppose the rest.

## 7. Cross-cutting risks & decisions to evaluate

- **Runner coupling.** Hook-based gates (M13/M16/M17) depend on Claude Code's
  hook system. Copilot CLI may lack equivalents → these become a *runner
  capability* (gated like `guild-mcp`), with graceful degradation. **Decision:**
  is claude-runner-only acceptable for control features, or must every control
  feature degrade cleanly for other runners?
- **The self-grading trap.** If the Guild Master's *own agent* approves
  transitions (M16) or judges "done" (M13's review pass), we risk the very
  "grades its own homework" failure the plan exists to prevent. **Decision:**
  which gates require a *human*, and which may be agent-adjudicated with an
  independent check?
- **Intrusiveness vs. adoption.** PM-governance (M16) and steering (M17) change
  how a project's agent behaves. Kept opt-in and off by default, but **decision:**
  how much control is welcome before users disable it?
- **Not automatically better.** The research is candid that multi-agent
  orchestration underperforms a lone strong agent on ambiguous specs and
  coupled/sequential work. **Decision:** should the Guild Master *refuse* to
  orchestrate work it judges non-parallelisable, rather than trying?
- **Scope creep toward a real orchestrator.** Each milestone must resist becoming
  an in-process controller. The invariant to hold: **boundary control + verified
  artifacts, never mid-turn puppeteering.**

## 8. Non-goals (explicit)

- No mid-turn / token-stream gating of an agent.
- No task-dispatch that bypasses a project agent's autonomy — the GM assigns and
  verifies; the agent still does the work its own way.
- No dependency on a specific external orchestration framework (LangGraph, CrewAI,
  etc.) — Daedalus *is* the substrate.
- No claim that more agents = better; the GM orchestrates only parallelisable,
  well-specified work.

## 9. How to evaluate this plan

Suggested lenses for the review:

1. **Leverage order** — is verification-first the right call, or should lifecycle
   command (M14) lead for a faster visible win?
2. **Imposability** — does each milestone's *hard guarantee* actually sit on the
   externally-imposable column, or does any secretly rely on the agent
   cooperating?
3. **Runner scope** — accept claude-runner-only control, or require graceful
   degradation everywhere?
4. **Human-in-the-loop** — where must a human be the gate, and where is
   agent-adjudication (with an independent check) acceptable?
5. **Stop points** — is there a natural place to stop the arc (e.g. after M13–M15)
   that already delivers most of the value?

## 10. Evidence base & references

The full research — a 7-category control taxonomy, the cooperative-vs-imposable
analysis, the MAST failure modes, and per-platform control primitives (LangGraph,
CrewAI, AutoGen/Magentic-One, OpenAI, Anthropic, Bedrock, A2A, Devin, OpenHands) —
is in **[`docs/guild-master-control.md`](guild-master-control.md)**, with sources.
Headline citations: MAST failure taxonomy
([arXiv:2503.13657](https://arxiv.org/abs/2503.13657)); Magentic-One dual-ledger
orchestrator ([arXiv:2411.04468](https://arxiv.org/abs/2411.04468)); Anthropic on
[effective harnesses for long-running agents](https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents)
and the [multi-agent research system](https://www.anthropic.com/engineering/multi-agent-research-system);
the practitioner consensus, "coherence through orchestration, not autonomy."

These milestones are also recorded in `ROADMAP.md` (M13–M17, Planned) and their
capabilities in `BACKLOG.md` (#57–#64).
