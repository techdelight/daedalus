# Roadmap

## End Goal

Daedalus orchestrates multiple coding agents (Claude Code, Copilot CLI, ...) across a programme of projects through a layered runtime: a uniform runner-adapter shim, a daedalus-runner process inside each tmux session, a central tmux coordinator, and thin CLI/TUI/Web UIs.

## Milestones

### Milestone 1: Autonomous Container Runtime (Done)

Single-command project launch with Docker isolation, tmux session management, and three UI surfaces (CLI, TUI, Web). Claude Code runs with `--dangerously-skip-permissions` inside a hardened container.

### Milestone 2: Programme Topology (Done)

Programme definitions with dependency graphs, MCP-based progress reporting, and agent observability.

### Milestone 3: Terminal Fidelity (Done)

tmux control mode (`-C`) for structured terminal I/O: native scrollback, resize handling, live-capture, history mode. Eliminates raw PTY quirks and enables machine-parseable agent events.

### Milestone 4: Layered Runner/Coordinator Architecture (In Progress)

Introduce daedalus-runner (in-container PID-1 process wrapping a runner via per-runner adapter), a central coordinator daemon with an API, and thin CLI/TUI/Web clients of that API. Replaces the current pattern where each UI talks to tmux directly.

### Milestone 5: Self-Sustaining Operations

- Shared Docker volumes (Claude versions, Maven `.m2`) to reduce disk usage
- Container snapshotting for tool persistence across restarts
- Automatic trust prompt handling
- Mobile WebSocket stability
- Homebrew distribution

## Phasing

```
M1 (Done) ──► M2 (Done) ──► M3 (Done) ──► M4 (In Progress) ──► M5
Container      Programme      Terminal      Layered              Self-sustaining
Runtime        Topology       Fidelity      Stack                Operations
```

## Current Focus

Milestone 4: Layered Runner / Coordinator Architecture. v0.38.0 shipped the foundation — `daedalus-runner` PID-1 binary inside the container, `runproto` wire protocol, host-side `runclient`, per-runner adapters, and an in-process `coordinator`. CLI (`DAEDALUS_USE_RUNNER=1`) and Web (`?mode=runner`) can both attach through the runner socket today, opt-in.

Remaining for M4: promote the coordinator from in-process to a daemon with a local API and session persistence, migrate the TUI onto the same API, make the runner path the default, and retire the tmux launch path once feature parity is reached.

See `BACKLOG.md` for work items and `SPRINTS.md` for sprint execution.
