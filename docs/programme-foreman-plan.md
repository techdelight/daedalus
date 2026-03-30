# Programme-Level Orchestration + Foreman Agent: Combined Implementation Plan

**Status: DELIVERED** — All 8 sprints implemented (v0.18.0–v0.25.1), 2026-03-30.

## Overview

This plan combines Musing 1 (Programme-Level Orchestration) and Musing 3 (The Foreman Agent) into a single phased roadmap. The core insight is that these two capabilities are symbiotic: the Foreman needs a dependency graph to make intelligent orchestration decisions, and the dependency graph needs an intelligent coordinator (the Foreman) to act on cascading changes. Early sprints deliver standalone value (project dashboards, MCP communication) while later sprints compose them into the full vision.

## Relationship to Existing Backlog Items

| Backlog # | Item | Relationship |
|-----------|------|-------------|
| 13 | Project management view in Web UI | **Prerequisite.** The Foreman reports through the Web UI. Sprint A delivers this. |
| 14 | Project management MCP server | **Prerequisite.** The communication channel between worker agents and Daedalus. Sprint B delivers this. |
| 16 | ACP integration | **Folded in.** Required for the Foreman to observe real-time agent state. Sprint F delivers this. |
| 17 | Roadmap in Web UI | **Folded in.** The Foreman's sprint view subsumes and extends this. Sprint D delivers a basic version; Sprint H extends it. |
| 18 | Daedalus as MCP client | **Folded in.** This is the Foreman's mechanism for reading project state and issuing work. Sprint E delivers this. |

## Architectural Changes Required

### 1. New Core Types: Programme and Dependency Graph

The `core/` package needs new pure types for multi-project relationships. Following the existing zero-I/O invariant:

- **`core/programme.go`** -- `Programme`, `DependencyEdge`, `DependencyGraph` types; `TopologicalSort()`, `DetectCycles()`, `Downstreams(project)`, `Upstreams(project)` pure functions.
- **`core/sprint.go`** -- `Sprint`, `SprintItem` structs representing decomposed work.
- **`core/foreman.go`** -- `ForemanState` enum, `ForemanConfig` struct. Pure configuration types only.

### 2. New Internal Packages

Following the existing `internal/` pattern where all I/O lives behind interfaces:

- **`internal/programme/`** -- CRUD for programme definitions, persisted as `programmes.json` alongside `projects.json`. Mirrors `internal/personas/` Store pattern.
- **`internal/foreman/`** -- The Foreman agent loop: reads roadmaps, decomposes sprints, issues commands to worker agents via MCP, monitors progress via ACP.
- **`internal/mcpclient/`** -- Daedalus as an MCP client (backlog 18). Consumes project management MCP servers running inside containers.

### 3. New MCP Server: `cmd/project-mgmt-mcp/`

A second MCP server binary (like `cmd/skill-catalog-mcp/`) that runs inside each container and exposes tools for reporting progress, reading sprint items, and signaling completion or blockers.

### 4. Registry Schema Evolution

`ProjectEntry` gains new optional fields: `Programme`, `DependsOn`, `ProgressPct`, `CurrentSprint`. Requires a registry migration from v2 to v3.

### 5. Web UI Extensions

New API endpoints and frontend views: programme dashboard (dependency graph), sprint board, Foreman console.

### 6. Config Extensions

New fields: `ForemanMode bool`, `Programme string`. New subcommands: `daedalus programme`, `daedalus foreman`.

---

## Sprint Plan

### Sprint A: Project Management View in Web UI (Backlog 13)

**Goal:** Per-project dashboard showing vision, version, time spent, and percentage complete.

| # | Item |
|---|------|
| 1 | `core/project.go` -- add `ProgressPct`, `Vision`, `Version` fields to `ProjectEntry` |
| 2 | `internal/registry/` -- v2-to-v3 migration |
| 3 | `internal/registry/` -- `UpdateProjectProgress()` method |
| 4 | `internal/web/` -- `GET /api/projects/{name}/dashboard` endpoint |
| 5 | `internal/web/static/` -- project detail panel (vision, version, time, progress bar) |
| 6 | Tests |

**Dependencies:** None.

### Sprint B: Project Management MCP Server (Backlog 14)

**Goal:** An MCP server inside each container that Claude Code can use to report progress.

| # | Item |
|---|------|
| 1 | `cmd/project-mgmt-mcp/main.go` -- new MCP server binary, stdio transport |
| 2 | MCP tools: `report_progress`, `set_vision`, `set_version`, `get_sprint_items`, `complete_sprint_item` |
| 3 | Progress file: writes to `/workspace/.daedalus/progress.json`; Daedalus reads via bind mount |
| 4 | `core/command.go` -- mount `.daedalus/`, add `project-mgmt-mcp` to `claude.json` |
| 5 | `Dockerfile` -- build and install `project-mgmt-mcp` binary |
| 6 | `internal/web/` -- poll/watch `progress.json` and update dashboard |
| 7 | Tests |

**Dependencies:** Sprint A.

### Sprint C: Programme Data Model and CLI

**Goal:** Declare multi-project programmes with dependency relationships.

| # | Item |
|---|------|
| 1 | `core/programme.go` -- types, `TopologicalSort()`, `DetectCycles()`, `Downstreams()`, `Upstreams()` |
| 2 | `internal/programme/` -- `Store` CRUD, persisted to `programmes.json` |
| 3 | `core/config.go` -- add `Programme` field |
| 4 | `cmd/daedalus/main.go` -- `daedalus programme` subcommand |
| 5 | Shell completions |
| 6 | Tests |

**Dependencies:** None. Can run in parallel with A and B.

### Sprint D: Roadmap Parsing and Sprint Decomposition

**Goal:** Daedalus can read a `ROADMAP.md` and parse it into structured sprint data.

| # | Item |
|---|------|
| 1 | `core/sprint.go` -- `Sprint`, `SprintItem`, `SprintStatus` types |
| 2 | `core/roadmap.go` -- `ParseRoadmap(markdown) ([]Sprint, error)` |
| 3 | `internal/web/` -- `GET /api/projects/{name}/roadmap` endpoint, collapsible side panel (backlog 17) |
| 4 | `cmd/project-mgmt-mcp/` -- `get_roadmap` and `get_current_sprint` tools |
| 5 | Tests |

**Dependencies:** Sprint B.

### Sprint E: Daedalus as MCP Client (Backlog 18)

**Goal:** Daedalus consumes the project management MCP server from the host side.

| # | Item |
|---|------|
| 1 | `internal/mcpclient/` -- MCP client using `go-sdk`, transport via `docker exec` + stdio |
| 2 | High-level methods: `ReadProgress()`, `ReadRoadmap()`, `GetSprintItems()`, `AssignSprintItem()` |
| 3 | Replace file-polling with MCP client calls |
| 4 | `daedalus programme show <name>` -- aggregate progress from all member projects |
| 5 | Tests |

**Dependencies:** Sprints B and C.

### Sprint F: ACP Integration (Backlog 16)

**Goal:** Observe real-time Claude Code state (thinking, tool use, idle, error) from outside the container.

| # | Item |
|---|------|
| 1 | Research spike: determine ACP transport for Claude Code CLI |
| 2 | `internal/acp/` -- ACP client, `AgentState` enum |
| 3 | `internal/web/` -- WebSocket endpoint streaming agent state |
| 4 | Web UI -- agent state indicator on project cards |
| 5 | `internal/foreman/` -- `AgentObserver` interface |
| 6 | Tests |

**Dependencies:** None for the client itself. Can run in parallel with D and E.

### Sprint G: The Foreman Agent -- Core Loop

**Goal:** Daedalus itself becomes an AI agent that reads roadmaps, decomposes sprints, and assigns work.

| # | Item |
|---|------|
| 1 | `core/foreman.go` -- `ForemanConfig`, `ForemanState`, `ForemanPlan` types |
| 2 | `internal/foreman/foreman.go` -- main loop: read graph, read roadmaps, plan sprint, assign, monitor, report |
| 3 | `internal/foreman/planner.go` -- invoke Claude for sprint decomposition |
| 4 | `internal/foreman/monitor.go` -- poll MCP/ACP, detect stuck agents |
| 5 | `cmd/daedalus/main.go` -- `daedalus foreman start/stop/status` |
| 6 | `internal/web/` -- Foreman console panel |
| 7 | Tests |

**Dependencies:** Sprints D, E, and F.

### Sprint H: Programme-Level Cascade Orchestration

**Goal:** Changes cascade automatically through the dependency graph.

| # | Item |
|---|------|
| 1 | `internal/foreman/cascade.go` -- cascade logic via `DependencyGraph.Downstreams()` |
| 2 | Cascade trigger: subscribe to MCP progress events |
| 3 | Cascade strategies: `auto`, `notify`, `manual` (configurable per edge) |
| 4 | Web UI -- cascade visualization |
| 5 | `daedalus programme cascade <name> --dry-run` |
| 6 | Tests |

**Dependencies:** Sprints G and C.

---

## Sprint Sequencing

```
Sprint A ─────────────────┐
(Project Dashboard)       │
                          v
Sprint B ────────> Sprint D ────────> Sprint E ────┐
(MCP Server)      (Roadmap Parse)    (MCP Client)  │
                                                    │
Sprint C ──────────────────────────────────────────>├──> Sprint G ──> Sprint H
(Programme Model)                                   │   (Foreman)    (Cascade)
                                                    │
Sprint F ──────────────────────────────────────────>┘
(ACP Integration)
```

Sprints A, C, and F have no mutual dependencies and can be developed in parallel. Sprint G is the capstone that composes all prior work. Sprint H is the programme-manager payoff.

---

## Risks and Open Questions

### Technical Risks

1. **ACP availability.** May not be publicly stable. Fallback: parse tmux output for state heuristics.
2. **MCP transport to containers.** Assumes `docker exec` + stdio. May need socket-based fallback.
3. **LLM cost for the Foreman.** Budget limits in `ForemanConfig` from day one. See Musing 7.
4. **Roadmap format fragility.** Start with Daedalus-native format; treat parser as pluggable.
5. **Core package purity.** All new `core/` types must have zero I/O imports (Guiding Principle 3).

### Open Questions

1. **Where does the Foreman run?** Recommendation: goroutine inside `daedalus web`, extract to separate process if complexity demands it.
2. **How does the Foreman invoke Claude?** Recommendation: shell out to `claude --print -p "..."` -- consistent with existing container pattern.
3. **Programme persistence.** Recommendation: separate `programmes.json` file, not an extension of `projects.json`.
4. **Approval workflow.** Default to "propose and wait" mode; `--auto-approve` flag for trusted setups.
5. **Cascade granularity.** Start with sprint-item completion as trigger. Git-level triggers can come later (Musing 9).
