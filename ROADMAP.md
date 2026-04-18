# Roadmap

## End Goal

Daedalus is a fully autonomous project manager that governs multiple coding agents across a programme of projects. It reads strategic direction, plans sprints, delegates work to containerised AI agents, monitors progress, and cascades changes through dependency graphs — with zero human intervention for routine work.

## Milestones

### Milestone 1: Autonomous Container Runtime (Done)

Single-command project launch with Docker isolation, tmux session management, and three UI surfaces (CLI, TUI, Web). Claude Code runs with `--dangerously-skip-permissions` inside a hardened container.

### Milestone 2: Multi-Agent Governance (Done)

Programme-level orchestration: dependency graphs, cascade propagation, Foreman agent with planning/monitoring loop, MCP-based progress reporting, and agent observability.

### Milestone 3: Terminal Fidelity (In Progress)

tmux control mode (`-C`) for structured terminal I/O: native scrollback, resize handling, live-capture, history mode. Eliminates raw PTY quirks and enables machine-parseable agent events.

### Milestone 4: Agent Protocol Integration

Replace container-status-based observation with ACP (Agent Client Protocol) for real-time agent state: thinking, tool use, idle, error. Enables the Foreman to make informed decisions about when to intervene.

### Milestone 5: Self-Sustaining Operations

- Shared Docker volumes (Claude versions, Maven `.m2`) to reduce disk usage
- Container snapshotting for tool persistence across restarts
- Automatic trust prompt handling
- Mobile WebSocket stability
- Homebrew distribution

## Phasing

```
M1 (Done) ──► M2 (Done) ──► M3 (In Progress) ──► M4 ──► M5
Container      Governance     Terminal              ACP     Self-sustaining
Runtime        & Foreman      Fidelity              Obs     Operations
```

## Current Focus

Milestone 3: Terminal Fidelity — tmux control mode is functional with scrollback, resize, history mode, and live-capture. Document structure split complete (ROADMAP/BACKLOG/SPRINTS). Remaining: final polish and stabilisation.

See `BACKLOG.md` for work items and `SPRINTS.md` for sprint execution.
