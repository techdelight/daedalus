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

Milestone 4: Layered Runner / Coordinator Architecture, mid-flight.

- **v0.38.0** shipped the runner foundation — `daedalus-runner` PID-1 binary, `runproto` wire protocol, host-side `runclient`, per-runner adapters, and an in-process `coordinator`. CLI and Web could attach through the runner socket, opt-in at the time.
- **v0.39.0** (Sprint 40, complete on `master` awaiting release) promoted the coordinator to a real daemon: `daedalus-coordinator` binary with an HTTP-over-Unix-socket API, a Go client, ssh-agent-style auto-spawn, and `sessions.json` persistence with `docker ps` reconciliation across restarts. The CLI and Web both go through the daemon; sessions are now host-wide discoverable.

Remaining for M4: migrate the TUI list to the coordinator client, and retire the tmux launch path once feature parity is confirmed. The runner path is now the **default** (opt out with `DAEDALUS_USE_TMUX=1`), and the trust-prompt gap under the runner path (Backlog #38) is fixed via smart replay-on-attach.

See `BACKLOG.md` for work items and `SPRINTS.md` for sprint execution.
