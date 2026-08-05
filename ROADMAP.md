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

### Milestone 7: Trustworthy, File-Derived Project State (In Progress)

The project's own files — not agent self-reports — are the single source of
truth for its state. Continues the M5–M6 thread (structured docs, milestone /
sprint parsing) by deriving the remaining state host-side.

- Derive vision (VISION.md), version (VERSION file), and progress (the current sprint's item statuses) host-side, and deprecate the `project-mgmt-mcp` write tools (#52)
- Coordinator staleness visibility — warn when the running daemon is older than the on-disk binary after a rebuild (#47/#48)

## Phasing

```
M1 (Done) ─► … ─► M5 (Done) ─► M6 (Done) ─► M7 (In Progress)
Container         Self-sust.    Roadmap       File-derived
Runtime           Operations    Hierarchy     State
```

## Current Focus

**Milestone 7: Trustworthy, File-Derived Project State.** Milestones M1–M6 are complete (M6 shipped in **v0.41.0** — the sidebar sprint pipeline). M7 continues the "the project's own files are the source of truth" thread: derive vision/version/progress host-side and deprecate the agent-self-reporting write tools (#52), plus coordinator staleness visibility (#47/#48). No sprint is open yet — see `SPRINTS.md` and `BACKLOG.md`.
