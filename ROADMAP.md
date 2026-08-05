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

### Milestone 8: Onboarding & Adoption (In Progress)

Make Daedalus approachable for a new user and a new project — from install to first productive session.

- Post-install onboarding / first-run guidance (#45)
- `daedalus docs scaffold` to bootstrap conformant project docs (#54)
- Sharpen the value proposition in README + first-run messaging (#46)

## Phasing

```
M1 (Done) ─► … ─► M6 (Done) ─► M7 (Done) ─► M8 (In Progress)
Container         Roadmap       PM tools &    Onboarding
Runtime           Hierarchy     file state    & Adoption
```

## Current Focus

**Milestone 8: Onboarding & Adoption.** Milestones M1–M7 are complete (M7 shipped in **v0.42.0** — the agent-facing lifecycle MCP tools + file-derived state). M8 turns outward: make Daedalus approachable for a new user and a new project — post-install onboarding (#45), `daedalus docs scaffold` for conformant project docs (#54), and a sharper value proposition (#46). No sprint is open yet — see `SPRINTS.md` and `BACKLOG.md`.
