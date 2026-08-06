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

### Milestone 10: Homebrew Distribution (In Progress)

`brew install daedalus` for macOS users — a Homebrew tap, a formula generator, and CI automation that publishes and updates the formula on each release, so install and upgrade both run through Homebrew. Builds on the single bundled release archive from M9 (#8), which a formula downloads and checksums. See `docs/homebrew-plan.md`.

- Homebrew tap + formula generator + release CI automation (#11)

## Phasing

```
M1 (Done) ─► … ─► M9 (Done) ─► M10 (In Progress)
Container         Release        Homebrew
Runtime           Bundling       Distribution
                  & Upgrades
```

## Current Focus

**Milestone 10: Homebrew Distribution.** Milestones M1–M9 are complete (M9 shipped in **v0.44.0** — the single checksum-verified release archive (#8) and side-by-side versioned installs with `daedalus version use/rollback/prune` (#9)). M10 brings `brew install daedalus`: a Homebrew tap, a formula generator, and CI automation that publishes/updates the formula on each release — building on M9's bundled archive, which a formula downloads and checksums (#11). No sprint is open yet — see `SPRINTS.md` and `BACKLOG.md`.
