# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Process Model

We use a hybrid of **PRINCE2** (governance + documentation), **Scrum** (sprints + backlog), and **XP** (engineering practices).

Default expectations: small incremental changes, frequent integration, automated tests, refactoring, pairing/mob reviews when useful.

## Project Documents

Maintain the following documents at the repo root (Markdown unless specified):

| Document | Purpose |
|---|---|
| `VISION.md` | Why we are building this — problem statement, target users, success metrics, non-goals, constraints, guiding principles |
| `ROADMAP.md` | The plan — current sprint (detailed goals, scope, acceptance criteria, demo plan), future sprints (feature themes, sequencing), version milestones |
| `CHANGELOG.md` | Changes per version (Added/Changed/Fixed/Removed). Include an `[Unreleased]` section |
| `VERSION` | Single-line semantic version (e.g., `0.6.0`). Any release increments VERSION and adds a CHANGELOG entry |
| `README.md` | End-user usage only — quick start, run info. No build instructions. Refer to `CONTRIBUTION.md` for technical details |
| `ARCHITECTURE.md` | Architecture — modules, components, protocols/ports, connections |
| `CONTRIBUTION.md` | Development guide — branching, TDD, code quality, Definition of Done, build instructions |

## Commands

```bash
# Build the binary (requires Docker)
make build

# Run tests
make test

# Interactive session (default target: dev)
./daedalus my-app /path/to/project

# Headless single-prompt task
./daedalus my-app -p "Fix all linting errors"

# Force rebuild the image
./daedalus --build my-app /path/to/project
```

## Development Workflow

- Develop per feature on a branch. When the feature is Done, merge to master and push.
- Follow Test-Driven Development (red-green-refactor).
- Apply CQS: functions are either Queries (return data, no side effects) or Commands (perform action, return void). Split functions that do both.
- Keep I/O in a separate package/library from core logic (`core/` has zero I/O imports).

## Code Quality

- Write intention-revealing names; avoid util/helper/manager.
- Keep functions small, single-purpose, one abstraction level.
- Prefer pure logic; push IO to the edges.
- Enforce SRP; high cohesion, low coupling; explicit dependencies.
- Remove duplication with judgment; avoid premature abstraction.
- Validate inputs at boundaries; fail fast; no swallowed errors.
- Refactor opportunistically (Boy Scout Rule) without expanding scope.
- If code becomes hard to read, stop and refactor before adding more.

## Copyright Headers

Every source file must start with a copyright comment:

| File type | Format |
|---|---|
| `.go` | `// Copyright (C) 2026 Techdelight BV` |
| `.html` | `<!-- Copyright (C) 2026 Techdelight BV -->` |
| `.css` | `/* Copyright (C) 2026 Techdelight BV */` |
| `.js` | `// Copyright (C) 2026 Techdelight BV` |

## Web Technology

- Use HTML, CSS, and JavaScript.
- For JavaScript UI, use a small library: **Alpine.js**.
- For graphics, if needed, use: **Three.js**.

## Services

- Every service should start by displaying the VERSION, build-timestamp, and the Techdelight logo (from `logo.txt`).
- Every service has a debug mode that logs incoming and outgoing messages.

## Build

- For fat jars, use the `maven-assembly-plugin`. Never use `maven-shade-plugin`.

## Committing

Each commit starts with the branch/plan name and a clean summary of what changed and why:

```
feature/web-ui: add REST API for project management.
Implements GET/POST endpoints for listing, starting, and stopping projects.
```

## Session Summary

At the end of each session, write a summary in `{this project name}.md` in `/mnt/c/agent-workspace/project-logs/`. Start with the date, note runtime duration, and describe key achievements, goals, objectives, and issues.

## Setup

1. Log in to Claude Code on the host (`claude` CLI)
2. Build: `make build`
3. Run `./daedalus <project-name> <project-dir>`
