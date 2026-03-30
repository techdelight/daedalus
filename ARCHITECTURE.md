# Architecture

## Overview

Daedalus is a Go CLI tool that wraps Claude Code in a Docker container for autonomous operation. It provides three UI surfaces (CLI, TUI, Web) over a shared core.

## Modules

### `core/` — Pure Logic (zero I/O imports)

Contains types, command builders, and helpers with no side effects.

| File | Contents |
|---|---|
| `config.go` | `Config` struct, `Image()`, `ContainerName()`, `TmuxSession()`, `CacheDir()`, `SkillsDir()`, `UseTmux()`, `ApplyRegistryEntry()` |
| `appconfig.go` | `AppConfig` struct, `ApplyAppConfig()` |
| `runner.go` | `RunnerProfile` struct, `LookupRunner()`, `LookupBuiltinRunner()`, `ValidRunnerNames()`, `ResolveRunnerName()` |
| `persona.go` | `PersonaConfig`, `PersonaOverlay` structs, `PersonasDir()`, `ValidatePersonaName()`, `IsBuiltinRunner()`, `BuiltinRunnerNames()` |
| `project.go` | `RegistryData`, `ProjectEntry` (with `ProgressPct`, `Vision`, `ProjectVersion`), `SessionRecord`, `ProjectInfo` types |
| `command.go` | `BuildRunnerArgs()`, `BuildClaudeArgs()` (deprecated alias), `BuildTmuxCommand()`, `BuildEnvExports()`, `ShellQuote()`, `BuildExtraArgs()`, `OverlayPaths` |
| `skills.go` | `StarterSkills()` — embedded starter skill files via `go:embed` |
| `programme.go` | `Programme`, `DependencyEdge`, `DependencyGraph` types; `NewDependencyGraph()`, `TopologicalSort()`, `DetectCycles()`, `Downstreams()`, `Upstreams()`, `ValidateProgrammeName()` |
| `sprint.go` | `Sprint`, `SprintItem`, `SprintStatus` types |
| `roadmap.go` | `ParseRoadmap()` — parses Daedalus-native ROADMAP.md into `[]Sprint` |
| `time.go` | `NowUTC()`, `ParseUTC()`, `RelativeTime()` |

### `cmd/daedalus/` — CLI Entry Point

| File | Responsibility |
|---|---|
| `main.go` | `main()`, `run()` dispatcher, project resolution, subcommand handlers (`list`, `prune`, `remove`, `config`, `skills`, `runners`, `personas`, `programmes`) |

### `cmd/skill-catalog-mcp/` — Skill Catalog MCP Server

| File | Responsibility |
|---|---|
| `main.go` | MCP server over stdio with 8 tools for skill catalog operations (list, read, install, uninstall, create, update, remove, list_installed) |

### `cmd/project-mgmt-mcp/` — Project Management MCP Server

| File | Responsibility |
|---|---|
| `main.go` | MCP server over stdio with 4 tools for project progress reporting (report_progress, set_vision, set_version, get_progress) |

### `cmd/generate-manpage/` — Man Page Generator

| File | Responsibility |
|---|---|
| `main.go` | Generates `daedalus.1` roff man page from embedded content and VERSION file |

### `internal/` — I/O Boundary Packages

All side effects (filesystem, shell, network) live here behind interfaces.

| Package | Key Types/Functions | Responsibility |
|---|---|---|
| `executor` | `Executor` interface, `RealExecutor`, `MockExecutor` | Abstracts `os/exec` and `syscall.Exec` calls |
| `color` | `Init()`, `Disable()`, `Red()`, `Green()`, `Yellow()`, `Cyan()`, `Bold()`, `Dim()` | ANSI color helpers, `NO_COLOR` support |
| `config` | `ParseArgs()`, `IsHeadless()`, `LoadAppConfig()` | CLI argument parsing into `core.Config` |
| `registry` | `Registry` | JSON file read/write for project metadata, schema migrations (v3), progress tracking |
| `docker` | `Docker`, `SetupCacheDir()` | Container lifecycle: build, run, compose, status checks |
| `session` | `Session`, `TmuxAvailable()` | tmux session create/attach/send-keys |
| `tui` | `Run()` | Interactive TUI dashboard (bubbletea + lipgloss) |
| `web` | `Run()`, `WebServer` | REST API + WebSocket terminal relay, embedded static assets |
| `logging` | `Init()`, `Close()`, `Info()`, `Error()`, `Debug()` | Thread-safe file logging with timestamp and level prefixes |
| `completions` | `Generate()` | bash/zsh/fish shell completion scripts |
| `personas` | `Store`, `New()`, `List()`, `Read()`, `Create()`, `Update()`, `Remove()` | User-defined persona configuration CRUD (JSON files) |
| `catalog` | `Catalog`, `New()`, `List()`, `Read()`, `Install()`, `Uninstall()`, `Create()`, `Update()`, `Remove()`, `ListInstalled()` | Shared skill catalog operations (filesystem I/O) |
| `progress` | `Data`, `Read()`, `Write()`, `Update()` | Project progress file I/O (`.daedalus/progress.json`) |
| `programme` | `Store`, `New()`, `List()`, `Read()`, `Create()`, `Update()`, `Remove()`, `AddProject()`, `AddDep()` | Programme definition CRUD (JSON files) |
| `platform` | `IsWSL2()`, `WSL2IPAddress()`, `DisplayArgs()` | Platform detection (WSL2) and display forwarding argument resolution |

### Dependency Graph (no cycles)

```
executor  (leaf)
color     (leaf)
logging   (leaf)
personas  → core
catalog   (leaf)
  ↑
programme → core
config    → core, color, personas
registry  → core
docker    → core, executor
session   → executor
completions → core
tui       → core, executor, registry, docker, session
web       → core, executor, registry, docker, session, progress
  ↑
cmd/daedalus → all of the above + catalog + personas + programme
cmd/skill-catalog-mcp → catalog (standalone MCP server, uses modelcontextprotocol/go-sdk)
cmd/project-mgmt-mcp → progress (standalone MCP server, uses modelcontextprotocol/go-sdk)
cmd/generate-manpage → (standalone, reads VERSION file only)
```

## Components

```
┌─────────────────────────────────────────────────┐
│                   Daedalus CLI                   │
│                cmd/daedalus/                     │
│  ┌──────────┐  ┌──────────┐  ┌──────────────┐   │
│  │   CLI    │  │   TUI    │  │   Web UI     │   │
│  │ (main)   │  │(bubbletea│  │ (net/http +  │   │
│  │          │  │+ lipgloss│  │  WebSocket)  │   │
│  └────┬─────┘  └────┬─────┘  └──────┬───────┘   │
│       │              │               │           │
│  ┌────┴──────────────┴───────────────┴────────┐  │
│  │          internal/ Shared Services         │  │
│  │  Registry · Docker · Session · Executor    │  │
│  └────────────────────┬───────────────────────┘  │
│                       │                          │
│  ┌────────────────────┴───────────────────────┐  │
│  │            core/ (pure logic)              │  │
│  │  Config · Project types · Command builders │  │
│  └────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────┘
```

## Protocols and Ports

| Protocol | Port | Component | Description |
|---|---|---|---|
| HTTP | 3000 (default) | Web UI | REST API (`/api/projects/*`, `/api/projects/{name}/dashboard`) and static file serving |
| WebSocket | 3000 (default) | Web UI | Terminal relay at `/api/projects/{name}/terminal` |
| Docker API | Unix socket | Docker client | Container lifecycle via `docker` CLI |
| tmux | — | Session manager | IPC via `tmux` CLI commands |

## Connections

```
User ──► daedalus CLI ──► Docker Engine ──► Container (Claude Code)
                │                              │
                ├──► tmux ◄────────────────────┘
                │     (session management)
                │
                ├──► .cache/projects.json (registry)
                │
                └──► .cache/<project>/ (persistent home)

Browser ──► Web UI (HTTP/WS) ──► tmux attach (PTY relay) ──► Container
```

### Data Flow

1. **CLI/TUI/Web** parse user intent into `core.Config`
2. **Registry** resolves project name to directory/target/flags
3. **Docker** builds image if missing, launches container via `docker compose run`
4. **Session** wraps container in tmux for detach/reattach
5. **Web UI** bridges browser to tmux session via WebSocket + PTY

### Docker Container

```
Host                          Container (claude-run-<name>)
─────                         ────────────────────────────
/path/to/project ──(rw)──►   /workspace
.cache/<name>/ ──(rw)──►     /home/claude (persistent)
.cache/skills/ ──(rw)──►     /opt/skills (shared skill catalog)
<project>/.daedalus/ ──(rw)──► /workspace/.daedalus (progress data)
                              /usr/local/bin/skill-catalog-mcp (MCP server)
                              /usr/local/bin/project-mgmt-mcp (MCP server)
                              Claude Code ⟷ MCP stdio ⟷ skill-catalog-mcp
                              Claude Code ⟷ MCP stdio ⟷ project-mgmt-mcp
```

Security: non-root user, all capabilities dropped, `no-new-privileges`.
