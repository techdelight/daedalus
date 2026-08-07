# Roadmap

## End Goal

Daedalus orchestrates multiple coding agents (Claude Code, Copilot CLI, ...) across a programme of projects through a layered runtime: a uniform runner-adapter shim, a `daedalus-runner` PID-1 process inside each container, a host-side `daedalus-coordinator` daemon that owns session lifecycles, and thin CLI/TUI/Web UIs that discover sessions through the daemon's HTTP-over-UDS API.

## Milestones

### Milestone 1: Autonomous Container Runtime (Done)

Single-command project launch with Docker isolation, tmux session management, and three UI surfaces (CLI, TUI, Web). Claude Code runs with `--dangerously-skip-permissions` inside a hardened container.

### Milestone 2: Programme Topology (Done)

Programme definitions with dependency graphs, MCP-based progress reporting, and agent observability.

### Milestone 3: Terminal Fidelity (Done)

tmux control mode (`-C`) for structured terminal I/O: native scrollback, resize handling, live-capture, history mode. Eliminates raw PTY quirks and enables machine-parseable agent events.

### Milestone 4: Layered Runner/Coordinator Architecture (Done)

Introduce daedalus-runner (in-container PID-1 process wrapping a runner via per-runner adapter), a central coordinator daemon with an API, and thin CLI/TUI/Web clients of that API. Replaces the current pattern where each UI talks to tmux directly.

### Milestone 5: Self-Sustaining Operations (Done)

- Shared Docker volumes (Claude versions, Maven `.m2`) to reduce disk usage
- Persistent per-project tools volume (`/opt/tools`) for runtime-installed tools
- Automatic trust prompt handling
- Mobile WebSocket stability

### Milestone 6: Roadmap Hierarchy, Made Visible (Done)

From any project, the milestone → sprint hierarchy is visible at a glance,
with sprints framed for how the tool actually works — verified batches that
cut a release, not calendar timeboxes.

- The active milestone and its sprints surface in the session sidebar
- Sprints are shown by ship-pipeline state — Building → Ready → Shipped (+ optional Proposed) — not by calendar time
- The verify/ship gate ("Ready": built but not yet released) is first-class; "active milestone, no sprints yet" is a valid, non-empty view

### Milestone 7: Project-Management Tools & File-Derived State (Done)

Give the in-container agent (Claude / Copilot) proper MCP tools to manage the
project's roadmap, and derive the rest of the project's state from its files —
replacing unreliable agent self-reporting with structured reads and writes.
daedalus **offers** these tools; it can't gate the agent, which runs as its own
autonomous CLI (daedalus launches it and provides MCP servers, it is not the
agent's harness). So this is capability, not enforcement.

- **Lifecycle MCP tools (write side)** — `project-mgmt-mcp` gains tools to **add / remove / move** milestones and sprints and **start / finish / pause** them, editing `ROADMAP.md` / `SPRINTS.md` structurally and keeping them internally consistent (the tool validates its own writes — e.g. one milestone In Progress, a current sprint linked to it). Replaces the free-form self-report write tools (`report_progress` / `set_vision` / `set_version`).
- **File-derived state (read side, #52)** — derive vision (`VISION.md`), version (`VERSION`), and progress (the current sprint's item statuses) host-side, so read state never depends on the agent reporting it.
- **A `Paused` lifecycle state** — for a milestone or sprint put on hold, distinct from Done / In Progress / Planned.

### Milestone 8: Onboarding & Adoption (Done)

Make Daedalus approachable for a new user and a new project — from install to first productive session.

- `daedalus docs scaffold` to bootstrap conformant project docs (#54)
- Post-install onboarding / first-run guidance — delivered as `daedalus init` (scaffold + getting-started guide) plus a post-install next-steps stanza (#45)
- Sharpen the value proposition in README + first-run / `--help` messaging (#46)

### Milestone 9: Release Bundling & Safe Upgrades (Done)

Make upgrading Daedalus effortless and reversible: one self-contained release artifact instead of a scatter of files, and the ability to run a new version alongside the current one before committing to it.

- Bundle release assets into a single checksum-verified per-platform archive rather than ~27 individual files (#8)
- Side-by-side versions — versioned install layout + `daedalus version list/use/rollback/prune` for A-B and rollback before committing (#9)

### Milestone 10: Homebrew Distribution (Planned)

`brew install daedalus` for macOS users — a Homebrew tap, a formula generator, and CI automation that publishes and updates the formula on each release, so install and upgrade both run through Homebrew. Builds on the single bundled release archive from M9 (#8), which a formula downloads and checksums. See `docs/homebrew-plan.md`.

- Homebrew tap + formula generator + release CI automation (#11)

### Milestone 11: The Guild Hall, Reforged (Done)

Turn the Web UI's Guild view into a living Secret-of-Mana-style party screen: every project is a distinct pixel-art hero whose animation reflects its real activity — working when the agent is busy, at ease when idle, resting when its container is asleep. A genuinely delightful, at-a-glance status board for a whole programme of projects. The activity signal already exists host-side (`/api/guild` derives busy/idle/sleeping from the in-container `.daedalus/activity.json` hook + container state); this milestone is the visual reforge on top of it.

- Distinct per-project avatar (deterministic hero archetype + palette from the project name), Secret-of-Mana pixel-art aesthetic and UI framing
- Activity-driven animation: busy = working/casting, idle = at ease, sleeping = resting — bound to the existing `/api/guild` activity state (and its `detail`)
- Polish: responsive/mobile layout, `prefers-reduced-motion`, empty/loading states

### Milestone 12: The Guild Master (embedded programme manager) (Done)

An always-present, un-removable project — default name **`guild-master`** (shown as "Guild Master") — that launches and behaves like any other Daedalus project, but whose agent has **read visibility across every registered project's documents**. It is the programme manager for the whole guild: a place to plan, reconcile, and report across projects, grounded in each project's own structured docs (ROADMAP / SPRINTS / VISION / BACKLOG) rather than in anyone's self-report.

**How this compares to the multi-agent orchestration people build online.** The "manager over workers" idea is well-trodden — LangGraph's *supervisor* graph, CrewAI's *hierarchical process* (a manager delegating to a crew), AutoGen's *group-chat manager*, the OpenAI *orchestrator-worker*, and research systems like AgentMesh/MetaGPT (a PM/architect/engineer chain). Those are **control** hierarchies: the manager dispatches tasks, awaits results, and hands off. Daedalus deliberately can't do that — it launches each agent as its own autonomous CLI and *offers* tools; it never gates an agent's turns (see M7). So the Guild Master is not a task-dispatcher. It is a **read-only programme overseer** — the "AI PM that automates cross-project status, planning and consistency" use case — a supervisor by *visibility*, not by command. It sees the whole board and can advise or draft programme-level plans; the individual project agents stay autonomous. This is the honest, achievable shape of "manager" in Daedalus's architecture, and it composes with the existing Guild view (the Guild Master is the guild's leader) and the `programmes` feature (multi-project topology).

- **Embedded, un-removable project (registry).** Auto-ensured on startup; `remove`/`prune`/`rename` refuse it (the "cannot remove built-in" precedent). Its own workspace is a Daedalus-owned dir scaffolded with docs (via `daedalus docs scaffold`) for programme-level planning. Appears in `list`, the TUI, the Web project list, and the Guild view as a distinguished hero.
- **Cross-project document access.** Every registered project's directory is mounted **read-only** into the Guild Master container (only there), and a dedicated `guild-mcp` server exposes tools to enumerate projects and read/parse any project's docs (`list_guild_projects`, `read_project_doc`, `guild_overview` — parsed milestones/sprints/progress per project). Read-only: it can *see* every project, never write another's files.
- **Scope discipline.** No control/dispatch of other agents (impossible by design); the Guild Master advises and plans. Cross-project mounts are resolved at launch (a project added later appears on the next launch) — documented, not hidden.

### Milestone 13: The Verify Gate (Planned)

Make "done" mean something a machine checked, not something an agent self-reported — the highest-leverage step toward a controlling Guild Master (research in `docs/guild-master-control.md`, targets T1/T6; MAST shows ~24% of multi-agent failures are "declared done ≠ verified done", and structural verify gates beat prompt-engineering). A project declares a `daedalus verify` check (build + tests + `daedalus docs lint`) whose **exit code** is the gate; Daedalus injects it as a Claude Code **Stop-hook** so the project agent cannot stop on red; the Guild Master reads each project's verify status. Foundation the rest of the control arc gates on.

- `daedalus verify` contract (per-project build/test/lint check) with an exit-code gate
- Injected Claude Code Stop-hook so a project agent can't self-declare done while checks fail (runner-specific; graceful degradation for runners without hooks)
- Per-project verify status exposed to the Guild Master (`guild-mcp`/coordinator); optional independent review-agent pass

### Milestone 14: Guild Master Lifecycle Command & Budgets (Planned)

Give the Guild Master externally-imposable control of the party: start / stop / pause any project's session through the coordinator (which already owns session lifecycles — the Devin/OpenHands "session over a sandbox" model), bounded by explicit budgets. This is pure imposable control that needs no cooperation from the project's agent (targets T2; `docs/guild-master-control.md`).

- Guild-Master control tools (`start`/`stop`/`pause_project`) over the coordinator, surfaced in the Guild view (the crowned hero commands the party)
- Concurrency caps + wall-clock / turn / cost budgets + auto-pause of stale sessions

### Milestone 15: Task Dispatch & the Programme Ledger (Planned)

Turn visibility into orchestration: the Guild Master hands a well-specified task to a project (headless run or session injection) and collects a **durable artifact** (branch / commit + structured status) via async dispatch → poll → artifact, keeping "return" semantics so the Guild Master stays authoritative. It maintains a persistent **Task Ledger + Progress Ledger** (the Magentic-One skeleton) in its workspace with a stall→replan escape and explicit budgets/termination (targets T3/T4).

- GM→project task-dispatch tool returning a durable artifact + structured status
- A programme-level Task Ledger (plan/facts) + Progress Ledger (satisfied? looping? progressing? next?) with stall→replan and termination conditions

### Milestone 16: Boundary Gates & Approval (Planned)

The gatekeeper, done at the seams Daedalus can actually gate (it cannot pause an agent mid-turn, but it can gate at tool-call and stop boundaries via injected hooks). For **PM-governed** projects only (opt-in, off by default), milestone/sprint transitions and merges require Guild-Master/human approval — an interrupt-state the controller owns (targets T5). Revives the earlier "gatekeeper" idea now that hooks make boundary gating imposable.

- Approval gate on `project-mgmt-mcp` writes (extend `ValidateWrite` + a `PreToolUse` hook) for milestone/sprint transitions; a merge/integration gate
- Opt-in per project ("PM enabled", default off); graceful for non-hook runners

### Milestone 17: Cross-Project Coordination & Steering (Planned)

The horizontal coordination layer: a non-destructive **steering channel** to redirect a running project agent at a tool-call boundary (runner injection + a `PreToolUse` hook surfacing queued steering) instead of a destructive `Ctrl-C`, and an internal A2A-style task/status/artifact contract + a cross-project task board for dependencies, composing with the `programmes` feature (targets T7/T8).

- Non-destructive steering channel (priority message delivered at a tool-call boundary)
- Internal task/status/artifact contract between the Guild Master and projects; a shared cross-project task board for dependencies

## Phasing

```
M1..M12 (Done, except M10) ─► ( no active milestone )

Planned — the "controlling Guild Master" arc (docs/guild-master-control.md):
  M13 Verify Gate ─► M14 Lifecycle Command ─► M15 Dispatch + Ledger
    ─► M16 Boundary Gates ─► M17 Coordination & Steering
Also Planned: M10 Homebrew Distribution.
```

## Current Focus

**No active milestone.** Milestones M1–M9, M11 and M12 are complete (M12 shipped in **v0.47.0** — the embedded, un-removable `guild-master` project with read visibility across every project's docs). Planned next: the **"controlling Guild Master" arc** (M13–M17) — evolving the Guild Master from a read-only overseer into a controlling entity via a verify gate, lifecycle command, task dispatch + a programme ledger, boundary approval gates, and cross-project coordination/steering (research + targets in `docs/guild-master-control.md`); plus M10 (Homebrew Distribution). The natural first is **M13 (The Verify Gate)** — highest leverage, mostly externally-imposable, fully host-testable. No milestone or sprint is in progress yet — a deliberate between-milestones state, so `daedalus docs lint` noting "no milestone is marked (In Progress)" is expected here, not a defect.
