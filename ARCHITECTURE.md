# Architecture

## Overview

Daedalus is a Go CLI tool that wraps Claude Code in a Docker container for autonomous operation. It provides three UI surfaces (CLI, TUI, Web) over a shared core.

## Modules

### `core/` — Pure Logic (zero I/O imports)

Contains types, command builders, and helpers with no side effects.

| File | Contents |
|---|---|
| `config.go` | `Config` struct, `Image()`, `ContainerName()`, `TmuxSession()`, `CacheDir()`, `UseTmux()`, `ApplyRegistryEntry()` |
| `appconfig.go` | `AppConfig` struct, `ApplyAppConfig()` |
| `project.go` | `RegistryData`, `ProjectEntry`, `SessionRecord`, `ProjectInfo` types |
| `command.go` | `BuildClaudeArgs()`, `BuildTmuxCommand()`, `BuildEnvExports()`, `ShellQuote()` |
| `time.go` | `NowUTC()`, `ParseUTC()`, `RelativeTime()` |

### `cmd/daedalus/` — CLI Entry Point

| File | Responsibility |
|---|---|
| `main.go` | `main()`, `run()` dispatcher, project resolution, subcommand handlers (`list`, `prune`, `remove`, `config`) |

### `internal/` — I/O Boundary Packages

All side effects (filesystem, shell, network) live here behind interfaces.

| Package | Key Types/Functions | Responsibility |
|---|---|---|
| `executor` | `Executor` interface, `RealExecutor`, `MockExecutor` | Abstracts `os/exec` and `syscall.Exec` calls |
| `color` | `Init()`, `Disable()`, `Red()`, `Green()`, `Yellow()`, `Cyan()`, `Bold()`, `Dim()` | ANSI color helpers, `NO_COLOR` support |
| `config` | `ParseArgs()`, `IsHeadless()`, `LoadAppConfig()` | CLI argument parsing into `core.Config` |
| `registry` | `Registry` | JSON file read/write for project metadata, schema migrations |
| `docker` | `Docker`, `SetupCacheDir()` | Container lifecycle: build, run, compose, status checks |
| `session` | `Session`, `TmuxAvailable()` | tmux session create/attach/send-keys |
| `tui` | `Run()` | Interactive TUI dashboard (bubbletea + lipgloss) |
| `web` | `Run()`, `WebServer` | REST API + WebSocket terminal relay, embedded static assets |
| `completions` | `Generate()` | bash/zsh/fish shell completion scripts |

### Dependency Graph (no cycles)

```
executor  (leaf)
color     (leaf)
  ↑
config    → core, color
registry  → core
docker    → core, executor
session   → executor
completions → core
tui       → core, executor, registry, docker, session
web       → core, executor, registry, docker, session
  ↑
cmd/daedalus → all of the above
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
| HTTP | 3000 (default) | Web UI | REST API (`/api/projects/*`) and static file serving |
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
```

Security: non-root user, all capabilities dropped, `no-new-privileges`.
