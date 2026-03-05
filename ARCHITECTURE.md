# Architecture

## Overview

Daedalus is a Go CLI tool that wraps Claude Code in a Docker container for autonomous operation. It provides three UI surfaces (CLI, TUI, Web) over a shared core.

## Modules

### `core/` — Pure Logic (zero I/O imports)

Contains types, command builders, and helpers with no side effects.

| File | Contents |
|---|---|
| `config.go` | `Config` struct, `Image()`, `ContainerName()`, `TmuxSession()`, `CacheDir()`, `UseTmux()`, `ApplyRegistryEntry()` |
| `project.go` | `RegistryData`, `ProjectEntry`, `SessionRecord`, `ProjectInfo` types |
| `command.go` | `BuildClaudeArgs()`, `BuildTmuxCommand()`, `BuildEnvExports()`, `ShellQuote()` |
| `time.go` | `NowUTC()`, `ParseUTC()`, `RelativeTime()` |

### Main Package — I/O Boundary

All side effects (filesystem, shell, network) live here behind interfaces.

| File | Component | Responsibility |
|---|---|---|
| `main.go` | CLI entry point | Argument dispatch, project resolution, container launch |
| `config.go` | `parseArgs()` | CLI argument parsing into `core.Config` |
| `executor.go` | `Executor` interface | Abstracts `os/exec` calls; `RealExecutor` + `MockExecutor` |
| `registry.go` | `Registry` | JSON file read/write for project metadata, migrations |
| `docker.go` | `Docker` | Container lifecycle: build, run, compose, status checks |
| `session.go` | `Session` | tmux session create/attach/send-keys |
| `tui.go` | `tuiModel` | Interactive TUI dashboard (bubbletea + lipgloss) |
| `web.go` | `WebServer` | REST API + WebSocket terminal relay |
| `web_embed.go` | `staticFiles` | `go:embed` for static assets |
| `completions.go` | Shell completions | bash/zsh/fish completion scripts |
| `color.go` | Terminal colors | ANSI color helpers, `NO_COLOR` support |

## Components

```
┌─────────────────────────────────────────────────┐
│                   Daedalus CLI                   │
│                                                  │
│  ┌──────────┐  ┌──────────┐  ┌──────────────┐   │
│  │   CLI    │  │   TUI    │  │   Web UI     │   │
│  │ (main)   │  │(bubbletea│  │ (net/http +  │   │
│  │          │  │+ lipgloss│  │  WebSocket)  │   │
│  └────┬─────┘  └────┬─────┘  └──────┬───────┘   │
│       │              │               │           │
│  ┌────┴──────────────┴───────────────┴────────┐  │
│  │              Shared Services                │  │
│  │  Registry · Docker · Session · Executor     │  │
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
~/.claude/.credentials ─(ro)► /opt/claude/credentials/
.cache/<name>/ ──(rw)──►     /home/claude (persistent)
```

Security: non-root user, all capabilities dropped, `no-new-privileges`.
