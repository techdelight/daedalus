# Vision

## Problem Statement

Claude Code's interactive permission prompts interrupt autonomous workflows. Every file write, shell command, or network call requires manual approval — making unattended operation impossible. Developers who want Claude Code to work autonomously on a project (overnight runs, CI pipelines, batch refactors) are blocked by this design.

**Daedalus** solves this by wrapping Claude Code in a Docker container with `--dangerously-skip-permissions`, providing isolation instead of prompts. The container boundary replaces per-action approval with a single trust decision: "Claude can do anything *inside this container*."

## Target Users

- **Solo developers** running Claude Code on personal projects who want hands-off autonomy
- **Small teams** using Claude Code for batch tasks (migrations, refactors, test generation)
- **CI/CD pipelines** where Claude Code runs as an automated step

## Success Metrics

- Zero permission prompts during autonomous operation
- Single-command project launch (`daedalus <name> <dir>`)
- Session survives terminal disconnect (tmux detach/reattach)
- Container isolation prevents unintended host modifications
- Three UI surfaces (CLI, TUI, Web) sharing one core

## Non-Goals

- **Multi-user authentication** — daedalus is a single-user tool; access control is the host's responsibility
- **Cloud/SaaS hosting** — no hosted service, no remote API, no multi-tenant deployment
- **IDE integration** — daedalus is a standalone CLI tool, not a VS Code extension or LSP server
- **Non-Claude LLMs** — purpose-built for Claude Code's specific permission model and CLI interface
- **Container orchestration** — one container per project, no Kubernetes, no Swarm, no scaling

## Constraints

- Must run on any Linux or macOS host with Docker installed
- Must ship as a single static binary (no runtime dependencies beyond Docker and tmux)
- Must not require root privileges on the host (except Docker group membership)
- Must not modify the host filesystem outside the mounted project directory
- Container must run as non-root with all capabilities dropped

## Guiding Principles

1. **Isolation first** — The container is the security boundary. Every design decision should strengthen isolation, not weaken it for convenience.
2. **Single binary** — `daedalus` is one executable with zero host dependencies beyond Docker and tmux. No installers, no package managers, no runtime downloads.
3. **Pure core, I/O at edges** — The `core/` package contains zero I/O imports (`os`, `exec`, `net`, `http`, `syscall`). All side effects live in the main package behind interfaces.
4. **Three UIs, one core** — CLI, TUI, and Web UI are thin layers over the same session/docker/registry logic. Adding a fourth UI should require zero core changes.
5. **Explicit over implicit** — Flags like `--dind` and `--no-tmux` exist because dangerous or surprising behavior must be opt-in. Defaults are safe.
