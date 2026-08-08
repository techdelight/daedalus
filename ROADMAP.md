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
> two rounds of evaluation (`daedalus-control-plane-report.md` and
> `guild-master-plan-critical-evaluation.md`, both pressure-tested against the
> literature), the arc below adopts a control-plane architecture: the **Guild
> Master has initiative, the control plane has authority** — it *proposes*
> actions; the host-side control plane adjudicates against policy and *executes*
> via the coordinator. Authoritative state (Tasks, Jobs, Artifacts, verification,
> approvals, events) lives host-side in a Daedalus-owned SQLite store — never in an
> agent workspace — and is **reconciled** against reality, not merely stored. The
> unit of orchestration is the **Job** (one attempt in an isolated Git worktree),
> not the session. Orchestration is **Git-native** (a managed project must be a Git
> repo). V1 is **human-CLI-first** — the deterministic reference path — with the
> Guild Master joining as a *gated* client only in V2. Full design (with the graded
> response to the critique) in `docs/guild-master-plan.md`; evidence in
> `docs/guild-master-control.md`. Sequenced **V1 → V2 → V3**.

### Milestone 13: Control Plane Foundation + the deterministic CLI path (V1) (Planned)

Stand up the host-side control plane (`daedalus-control`) and its core data model — **Task** (what to accomplish) → **Job** (one attempt) → **Artifact** (a committed result + status) — with authoritative *desired* state in a Daedalus-owned SQLite store. Each Job runs in a **dedicated, isolated Git worktree** checked out clean at `base_sha` (isolation as artifact-provenance, so the captured commit holds only the Job's changes — never a developer's dirty edits); a Job ends at **process exit**, and only a `success` execution promotes its `output_snapshot` to a candidate Artifact (commit-exists ≠ succeeded). The **only client is a human CLI** — `daedalus task create|dispatch|status|cancel|verify` — the deterministic reference path that makes the plane useful at N=1 and isolates bugs before any agent drives it. **Git-native** (a managed project must be a Git repo).

- `daedalus-control` daemon + SQLite (durable desired-state) + Task/Job/Artifact model + `control.sock`
- Isolated Git worktree per Job; headless Job (process-exit boundary); `execution_result` vs `output_snapshot` (only success → candidate)
- **Reconcile-on-boot + periodic loop** with idempotent, deterministically-named side-effects (the dual-write fix — SQLite holds desired state, containers/worktrees are reconstructible), so state survives daemon/agent crashes
- Human `daedalus task …` CLI as the sole client; one active Job per project; **no Guild Master client yet**

### Milestone 14: Independent Verification (V1) (Planned)

Make "done" structural, not conversational — the highest-leverage step. The worker may only move a Job `working → candidate` ("I think it's done"); **only the control plane** performs `candidate → verified`, by checking out the Artifact's commit into a **clean verifier container** and running the project's verify policy (build + tests + `daedalus docs lint` + acceptance). Honest scope: this yields an *independently reproducible verification result*, **not** a proof of correctness — frontier agents game tests in 30–100% of adversarial runs, so the acceptance oracle must sit **outside the agent's write scope**.

- `daedalus verify` contract + clean verifier container verifying the committed Artifact independently of the worker's environment; a null-agent floor check
- **Verifier image pinned by `sha256:` digest** (not a mutable tag) + an explicit network/creds/`/opt/tools` policy
- **Frozen `acceptance_policy@base_sha`** (hashed) **plus a test-integrity gate that rejects any Job whose diff touches the frozen test/acceptance files**; the ladder toward control-plane-supplied held-out tests
- The structural `candidate → verified | rejected → retry/replan` transition owned solely by the control plane

### Milestone 15: Governance, Integration & the Guild Master client (V2) (Planned)

Turn the control plane into a **governed** orchestrator that an agent can safely drive. It enforces **budgets** (wall-clock/concurrency/max-attempts/review-cycles strongly enforceable; turn/token/cost policy-in-plane) and can **reject** requests (over-budget; stale base). **Integration is a race-safe transaction** (serialize → rebase onto the current tip → re-verify the *merged* result → compare-and-swap the target ref — the merge-queue fix for semantic conflicts), gated by a human `verified → approval_required → approved → integrated` state machine in the Web UI/TUI. **The Guild Master joins here** via `guild-control-mcp`, reusing the CLI capabilities but with **tiered, injection-safe authority**: it reads untrusted project docs, so consequential ops (cancel a Job, raise a budget, request integration) are **human-confirmed proposals**, never direct execution.

- Budget enforcement + request rejection; the integration transaction (rebase → re-verify merged → CAS); human approval state machine + Web/TUI; retry/replan; independent reviewer pass; control-plane-managed event log
- **Guild Master as a gated client** (`guild-control-mcp` over `control.sock`; never `coordinator.sock`; project docs = untrusted; tiered authority breaks the lethal trifecta)
- (Optional add-on) roadmap-transition governance for PM-opt-in projects, reusing the approval machinery

### Milestone 16: Parallel Programme Execution (V3) (Planned)

Scale from one Job at a time to a real programme scheduler. The **worktrees already exist from M13**, so this *adds only concurrency and scheduling*: multiple concurrent Jobs, a scheduler with concurrency limits, and **dependency scheduling** across a **cross-project task graph** (composing with the existing `programmes` feature). This is where Daedalus becomes a genuine multi-agent programme-execution platform.

- Concurrent Jobs (each already isolated in its own worktree); a job scheduler with concurrency limits
- Cross-project task graph + dependency scheduling (blocked/ready transitions), integrated with `programmes`

### Milestone 17: Typed Steering (V3, demoted) (Planned)

Represent steering as a typed, audited control-plane operation — `steer_job(job, instruction)`, recorded as a `SteeringEvent` with issuer, timestamp, and delivery state, delivered by the runner/hook layer at the next supported boundary — rather than an ad-hoc terminal injection. Round out the coordination surface (task-board views, provenance, cancellation) so the whole orchestration model is uniform and auditable.

Kept Planned but **low-priority / demotion candidate to BACKLOG**: for short Jobs, **cancel + redispatch with corrected instructions** may suffice, so live steering should prove its value in real use before it earns a milestone.

- Typed `steer_job` with provenance / delivery-state / cancellation, delivered at a supported boundary
- Coordination polish: cross-project task-board views over control-plane state; uniform provenance across tasks, jobs, steering, and approvals

## Phasing

```
M1..M12 (Done, except M10) ─► ( no active milestone )

Planned — the "controlling Guild Master" control-plane arc
(design: docs/guild-master-plan.md; evidence: docs/guild-master-control.md):
  V1  M13 Control-Plane Foundation + CLI path (worktrees · reconcile) ─► M14 Independent Verification
  V2  M15 Governance · Integration txn · Guild Master (gated) client
  V3  M16 Parallel Execution (dependency graph) ─► M17 Typed Steering (demoted)
Also Planned: M10 Homebrew Distribution.
```

## Current Focus

**No active milestone.** Milestones M1–M9, M11 and M12 are complete (M12 shipped in **v0.47.0** — the embedded, un-removable `guild-master` project with read visibility across every project's docs). Planned next: the **"controlling Guild Master" control-plane arc** (M13–M17), which evolves the Guild Master from a read-only overseer into a controlling entity via a host-side **control plane** — the *Guild Master has initiative, the control plane has authority*. Revised after **two rounds of evaluation** (`daedalus-control-plane-report.md`, then `guild-master-plan-critical-evaluation.md`, both literature-checked): **V1** = a **human-CLI-first** control-plane foundation with isolated Git worktrees + crash reconciliation (M13) and independent, digest-pinned artifact verification with a test-integrity gate (M14); **V2** = governance, a race-safe integration transaction, and the Guild Master joining as a *gated, injection-safe* client (M15); **V3** = parallel execution (M16) and typed steering (M17, demoted). Full design + the graded response to the critique in `docs/guild-master-plan.md`; evidence in `docs/guild-master-control.md`. The natural first is **M13** — fully host-testable, and useful at N=1 before any agent orchestration exists. Also Planned: M10 (Homebrew). No milestone or sprint is in progress yet — a deliberate between-milestones state, so `daedalus docs lint` noting "no milestone is marked (In Progress)" is expected here, not a defect.
