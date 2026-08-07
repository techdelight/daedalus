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

> **The controlling Guild Master is built as a host-side _control plane_.** After
> evaluation (see `daedalus-control-plane-report.md`), the arc below adopts a
> control-plane architecture: the **Guild Master has initiative, the control plane
> has authority** — it *proposes* privileged actions through a constrained
> `guild-control-mcp`/`control.sock`; the host-side control plane adjudicates
> against policy and *executes* via the coordinator. Authoritative state (Tasks,
> Jobs, Artifacts, verification, approvals, audit) lives host-side in a Daedalus-
> owned SQLite store — never in an agent workspace. The unit of orchestration is
> the **Job** (one attempt), not the session. Full design in
> `docs/guild-master-plan.md`; evidence base in `docs/guild-master-control.md`.
> Sequenced as the report's **V1 → V2 → V3**.

### Milestone 13: Control Plane Foundation — the Job model (V1) (Planned)

Stand up the host-side control plane (`daedalus-control`) and its core data model — **Task** (what to accomplish) → **Job** (one attempt) → **Artifact** (a durable commit/branch + status) — with authoritative state in a Daedalus-owned SQLite store. The Guild Master drives it only through a constrained `guild-control-mcp` over `control.sock` (intent-level ops: `create_task`/`dispatch_task`/`get_task`/`cancel_task`); it never receives `coordinator.sock`. A **Job wrapper** pins `base_sha`, runs the agent via the coordinator, and captures the resulting commit as the Artifact. GM workspace docs (`TASKS.md`/`STATUS.md`) become read-only *projections* of control-plane state.

- `internal/control` service + `cmd/daedalus-control` + `cmd/guild-control-mcp`; `control.sock` as the security boundary (constrained, policy-checked ops only)
- Task/Job/Artifact model + SQLite store + the early state machine (planned → queued → working → candidate); the Job (not the session) is the unit of orchestration
- Job wrapper: pin `base_sha`, dispatch via the coordinator, capture `head_sha`/branch as the Artifact; one active job per project to start

### Milestone 14: Independent Verification & Frozen Acceptance (V1) (Planned)

Make "done" structural, not conversational — the highest-leverage step (MAST: ~24% of failures are "declared done ≠ verified done"). The worker may only move a Job `working → candidate` ("I think it's done"); **only the control plane** performs `candidate → verified`, by checking out the Artifact's commit into a **clean verifier container** (the project's image, no worker mutable state) and running the project's `verify` policy (build + tests + `daedalus docs lint` + acceptance checks). The acceptance policy is **frozen at the task's `base_sha`** (captured + hashed) so a worker cannot weaken the check it must pass. Runner-agnostic — it verifies a git artifact, not a Claude session (an injected Stop-hook is an optional secondary nudge, not the authority).

- `daedalus verify` contract + a clean-worktree verifier container that verifies the committed Artifact independently of the worker's environment
- Frozen `acceptance_policy@base_sha` (hashed); policy changes affect only future tasks, never the current one
- The structural `candidate → verified | rejected → retry/replan` transition owned solely by the control plane

### Milestone 15: Governance — budgets, approval & integration (V2) (Planned)

Turn the control plane into a **governed** orchestrator. It enforces **budgets** (wall-clock / concurrency / max-attempts / review-cycles are strongly enforceable; turn/token/cost are policy-in-plane, measurement runner-dependent) and can **reject** Guild Master requests (budget too high; Artifact produced from a stale base → must rebase + re-verify). It adds **human approval** as a first-class state (`verified → approval_required → approved → integrated`), surfaced as an approve/reject control in the Web UI/TUI — so the Guild Master can never approve its own work when policy requires a human. Plus retry/replan, an independent **reviewer** pass, and an append-only **audit log** of every event.

- Budget enforcement + request rejection ("GM proposes, the plane adjudicates and executes")
- Human integration-approval state machine + Web/TUI approve/reject; independent reviewer pass; audit/event log
- (Optional add-on) roadmap-transition governance for PM-opt-in projects, reusing the same approval machinery

### Milestone 16: Parallel Programme Execution (V3) (Planned)

Scale from one-job-at-a-time to a real programme scheduler: **multiple concurrent Jobs**, each in an **isolated git worktree/branch** (one-owner isolation per attempt, so parallel jobs never collide), with **dependency scheduling** across a **cross-project task graph** (composing with the existing `programmes` feature). This is where Daedalus becomes a genuine multi-agent programme-execution platform.

- Concurrent Jobs per project via isolated worktrees/branches; a job scheduler with concurrency limits
- Cross-project task graph + dependency scheduling (blocked/ready transitions), integrated with `programmes`

### Milestone 17: Typed Steering & Coordination Polish (V3) (Planned)

Represent steering as a typed, audited control-plane operation — `steer_job(job, instruction)`, recorded as a `SteeringEvent` with issuer, timestamp, and delivery state, delivered by the runner/hook layer at the next supported boundary — rather than an ad-hoc terminal injection. Round out the coordination surface (task-board views, provenance, cancellation) so the whole orchestration model is uniform and auditable.

- Typed `steer_job` with provenance / delivery-state / cancellation, delivered at a supported boundary
- Coordination polish: cross-project task-board views over control-plane state; uniform provenance + audit across tasks, jobs, steering, and approvals

## Phasing

```
M1..M12 (Done, except M10) ─► ( no active milestone )

Planned — the "controlling Guild Master" control-plane arc
(design: docs/guild-master-plan.md; evidence: docs/guild-master-control.md):
  V1  M13 Control Plane Foundation (Task/Job/Artifact) ─► M14 Independent Verification
  V2  M15 Governance (budgets · approval · integration · audit)
  V3  M16 Parallel Execution (worktrees · dependency graph) ─► M17 Typed Steering
Also Planned: M10 Homebrew Distribution.
```

## Current Focus

**No active milestone.** Milestones M1–M9, M11 and M12 are complete (M12 shipped in **v0.47.0** — the embedded, un-removable `guild-master` project with read visibility across every project's docs). Planned next: the **"controlling Guild Master" control-plane arc** (M13–M17), which evolves the Guild Master from a read-only overseer into a controlling entity via a host-side **control plane** — the *Guild Master has initiative, the control plane has authority*. It follows the report's **V1 → V2 → V3**: V1 = the control-plane foundation (Task/Job/Artifact + SQLite) and independent artifact verification (M13–M14); V2 = governance — budgets, human approval, integration, audit (M15); V3 = parallel execution + typed steering (M16–M17). Design in `docs/guild-master-plan.md` (revised after evaluation, `daedalus-control-plane-report.md`); evidence in `docs/guild-master-control.md`. The natural first is **M13 (Control Plane Foundation)** — fully host-testable. Also Planned: M10 (Homebrew). No milestone or sprint is in progress yet — a deliberate between-milestones state, so `daedalus docs lint` noting "no milestone is marked (In Progress)" is expected here, not a defect.
