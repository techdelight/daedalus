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

### Milestone 5: Self-Sustaining Operations (In Progress)

- Shared Docker volumes (Claude versions, Maven `.m2`) to reduce disk usage
- Persistent per-project tools volume (`/opt/tools`) for runtime-installed tools
- Automatic trust prompt handling
- Mobile WebSocket stability

## Phasing

```
M1 (Done) ──► M2 (Done) ──► M3 (Done) ──► M4 (Done) ──► M5 (In Progress)
Container      Programme      Terminal      Layered         Self-sustaining
Runtime        Topology       Fidelity      Stack           Operations
```

## Current Focus

Milestone 5: Self-Sustaining Operations. The layered runner/coordinator stack (M4) is complete — the runner path is the **default**, all three UIs (CLI, TUI, Web) go through the coordinator, and the trust-prompt gap (#38) is fixed and parity-verified on real Docker.

M5's first slice is implemented on `development` — shared Claude/Maven caches (#37/#21), a per-project tools volume (#27), mobile-WebSocket resilience (#29), Dockerfile layer efficiency (#51), the coordinator-mount fix (#55), and idempotent trust handling — but **not yet verified on real Docker or a device** (it was built in a container without a daemon). Sprint 43 verifies it end-to-end and closes the deferred pieces (installer pinning, Maven overlay); see `docs/m5-verification.md`.

The one Milestone 4 cleanup tail — retiring the classic tmux launch path — rides Sprint 43, once the runner default has proven out.

See `BACKLOG.md` for work items and `SPRINTS.md` for sprint execution.
