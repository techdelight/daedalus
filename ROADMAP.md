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

## Phasing

```
M1 (Done) ──► M2 (Done) ──► M3 (Done) ──► M4 (Done) ──► M5 (Done)
Container      Programme      Terminal      Layered         Self-sustaining
Runtime        Topology       Fidelity      Stack           Operations
```

## Current Focus

Milestones M1–M5 are all complete. Milestone 5 (Self-Sustaining Operations) was verified end-to-end on real Docker in Sprint 43 and shipped in **v0.40.0** — shared Claude/Maven caches (#37/#21), a per-project tools volume (#27), mobile-WebSocket resilience (#29), Dockerfile layer efficiency + pinned/checksum-verified installers (#51), the coordinator-mount fix (#55), and idempotent trust handling. The classic tmux launch path was retired (the Milestone 4 tail); the runner/coordinator stack is now the sole architecture.

The next milestone is not yet defined — candidate work lives in `BACKLOG.md`; `SPRINTS.md` tracks sprint execution.
