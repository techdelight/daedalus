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

### Milestone 7: Agent-Governed Project Management (In Progress)

For projects that opt into Project Management (PM), the daedalus **agent** —
not a human CLI — becomes the gatekeeper of the milestone/sprint lifecycle:
it starts and closes milestones and sprints through validated transitions, so
the roadmap can't drift into an inconsistent state. PM is **opt-in per project**
(most projects want it; some don't). The project's own files stay the single
source of truth — the read side of the same idea.

- **File-derived state (read side, #52)** — derive vision (`VISION.md`), version (`VERSION`), and progress (the current sprint's item statuses) host-side, and retire the unreliable agent self-report write tools (`report_progress` / `set_vision` / `set_version`).
- **Agent-governed lifecycle gates (write side)** — the in-container agent performs guarded transitions (open/close milestone, open/close sprint) that enforce the invariants by construction: exactly one milestone In Progress; the current sprint links to it; a sprint closes only when its items are Done and a version is cut; closing a milestone opens the next. This replaces free-form status edits (and the deprecated self-report writes) with a small, validated write surface — and is the *enforcement* of the "Ready → Shipped" gate M6 made visible. The agent is instructed to route all lifecycle changes through the gates; the gates validate and refuse bad transitions.
- **Per-project PM opt-in** — a `pm-enabled` flag on the registry `ProjectEntry` (default **true**), added via a registry migration. A PM-enabled project is governed/gated by the agent; a PM-disabled project is left entirely alone.
- **Upgrade migration** — installing the PM-introducing version (from a version without it) prompts the user to choose which existing projects have PM enabled — default true, with an all/none bulk toggle.
- **Coordinator staleness visibility (#47/#48)** — warn when the running daemon is older than the on-disk binary after a rebuild.

## Phasing

```
M1 (Done) ─► … ─► M5 (Done) ─► M6 (Done) ─► M7 (In Progress)
Container         Self-sust.    Roadmap       Agent-governed
Runtime           Operations    Hierarchy     PM
```

## Current Focus

**Milestone 7: Agent-Governed Project Management.** Milestones M1–M6 are complete (M6 shipped in **v0.41.0** — the sidebar sprint pipeline). M7 makes the daedalus agent the gatekeeper of the milestone/sprint lifecycle for **PM-opt-in** projects: guarded open/close transitions that enforce the roadmap invariants by construction (the write side), paired with deriving vision/version/progress from files and retiring the agent self-report write tools (#52, the read side). PM is a per-project flag (default true) with an upgrade-time selection. No sprint is open yet — see `SPRINTS.md` and `BACKLOG.md`.
