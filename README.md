<p align="center">
  <img src="assets/banner.png" alt="Daedalus — A TechDelight Project" >
</p>

# Daedalus

A Docker environment for running AI coding agents ([Claude Code](https://claude.ai/code), [GitHub Copilot CLI](https://github.com/features/copilot)) autonomously without permission prompts. The container isolates the agent with write access only to the mounted project directory.

## Why

Claude Code is powerful, but using it day-to-day has real friction:

- **Permission fatigue.** Claude asks for confirmation on every file edit, shell command, and tool call. You end up babysitting instead of building. Daedalus gives Claude 100% green light inside a locked-down Docker container, so it works autonomously while your system stays safe.

- **Fragile connections kill sessions.** Working from a phone, a train, or anywhere with spotty wifi means your Claude Code session dies the moment the connection drops — and all context is lost. Daedalus wraps every session in tmux, so you can disconnect (or get disconnected) and pick up exactly where you left off.

- **Switching between projects is painful.** Juggling multiple Claude sessions across different codebases means manually managing terminals. Daedalus gives you a TUI and web dashboard to start, stop, attach, and switch between projects in seconds.

## Quick Start

```bash
# Install Daedalus
curl -fsSL https://raw.githubusercontent.com/techdelight/daedalus/master/install.sh | bash

# Start a project
daedalus my-awesome-app /path/to/project
```

## Basic Usage

Daedalus wraps each container session in tmux. A few essentials:

| Action | Keys |
|---|---|
| Detach (leave running in background) | `Ctrl-b` then `d` |
| Scroll up (copy mode) | `Ctrl-b` then `[`, arrows or `PgUp`/`PgDn`, `q` to exit |
| Reattach | `daedalus <project-name>` — auto-attaches to existing session |

Full tmux reference: [tmuxcheatsheet.com](https://tmuxcheatsheet.com/)

## Installation

The install script downloads a pre-built binary and runtime files from the latest GitHub Release, copies them to a prefix directory, and symlinks `daedalus` into `~/.local/bin`. No build step required.

**Prerequisites:** curl. Docker is required at runtime but not for installation.

```bash
# Install to ~/.local/share/daedalus (default)
curl -fsSL https://raw.githubusercontent.com/techdelight/daedalus/master/install.sh | bash

# Install to a custom directory
curl -fsSL https://raw.githubusercontent.com/techdelight/daedalus/master/install.sh | bash -s -- --prefix ~/daedalus
```

**Uninstall:**

```bash
# Uninstall (keeps project data by default)
~/.local/share/daedalus/setup.sh --uninstall

# Uninstall from a custom prefix
~/.local/share/daedalus/setup.sh --uninstall --prefix ~/daedalus
```

**Options:**

| Flag | Description |
|---|---|
| `--prefix <dir>` | Installation directory (default: `~/.local/share/daedalus`) |
| `--no-link` | Skip creating a symlink in PATH |
| `--uninstall` | Remove Daedalus installation (prompts before deleting project data) |
| `--verbose` | Enable shell tracing (`set -x`) for debugging |

The symlink is created in `~/.local/bin`. If this directory is not on your PATH, the script prints a hint.

> **Note:** zsh users (the default shell on macOS) may need to run `source ~/.zshrc` or open a new terminal before the `daedalus` command is available.

## Usage

```
daedalus [flags] <project-name> [project-dir]
daedalus list
daedalus prune
daedalus remove <name> [name...]
daedalus config <name> [--set key=value] [--unset key]
daedalus skills [add <file> | remove <name> | show <name>]
daedalus runners [list | show <name>]
daedalus personas [list | show <name> | create <name> | remove <name>]
daedalus programmes [list | show <name> | create <name> | add-project <prog> <proj> | add-dep <prog> <up> <down> | remove <name>]
daedalus foreman [start | stop | status]
daedalus tui
daedalus web [--port PORT] [--host HOST] [--auth|--no-auth]
daedalus completion <bash|zsh|fish>
daedalus --help
```

**Commands:**

| Command | Description |
|---|---|
| `<project-name>` | Open a registered project (uses stored directory) |
| `<project-name> <project-dir>` | Register and open a new project |
| `list` | List all registered projects |
| `prune` | Remove registry entries with missing directories |
| `remove <name> [name...]` | Remove named projects from the registry |
| `config <name>` | View or edit per-project default flags |
| `skills` | List, add, remove, or show skills in the shared catalog |
| `runners` | List or show built-in runner profiles (`claude`, `copilot`) |
| `personas` | List, show, create, or remove named persona configurations |
| `programmes` | List, show, create, or remove multi-project programmes with dependencies |
| `foreman` | Start, stop, or check the status of the Foreman agent (runs inside `daedalus web`) |
| `tui` | Interactive dashboard for managing projects |
| `web` | Web UI dashboard (default: `localhost:3000`) |
| `completion <shell>` | Print shell completion script (bash, zsh, fish) |
| `--help`, `-h` | Show usage message |

**Flags:**

| Flag | Description |
|---|---|
| `--build` | Force rebuild the Docker image (standalone: rebuild all registered projects) |
| `--target <stage>` | Build target: `dev` (default), `godot`, `base`, `utils` |
| `--resume <id>` | Resume a previous Claude session |
| `-p <prompt>` | Run a headless single-prompt task |
| `--no-tmux` | Run without tmux session wrapping |
| `--debug` | Enable Claude Code debug mode |
| `--dind` | Mount Docker socket (WARNING: grants host Docker access) |
| `--runner <name>` | AI runner: `claude` (default) or `copilot` |
| `--persona <name>` | Named persona configuration to use |
| `--display` | Forward host X11/Wayland display into the container for GUI apps |
| `--force` | Force deletion in non-interactive mode (e.g. prune, remove) |
| `--no-color` | Disable colored output (also honors `NO_COLOR` env var) |
| `--port <port>` | Port for web UI (default: `3000`) |
| `--host <host>` | Host for web UI (default: `127.0.0.1`) |
| `--auth` | Enable token-based authentication for web UI (default for `web`) |
| `--no-auth` | Disable authentication for web UI |

**Examples:**

```bash
# Open an existing project from the registry
daedalus my-awesome-app

# Register a new project with a directory
daedalus my-awesome-app /path/to/project

# Force rebuild the Docker image
daedalus --build my-awesome-app /path/to/project

# Rebuild all registered projects
daedalus --build

# Rebuild only the godot target image
daedalus --build --target godot

# Build a specific target (default: dev)
daedalus --build --target godot my-awesome-app /path/to/project

# Resume a previous session
daedalus --resume <session-id> my-awesome-app

# Run without tmux session wrapping
daedalus --no-tmux my-awesome-app /path/to/project

# Interactive TUI dashboard
daedalus tui

# Web UI dashboard (opens at http://localhost:3000)
daedalus web

# Web UI on a custom port
daedalus web --port 8080

# List all registered projects
daedalus list

# Enable display forwarding for GUI apps
daedalus --display my-app /path/to/project

# Use Copilot CLI instead of Claude Code
daedalus --runner copilot my-app /path/to/project

# Create a custom persona configuration (interactive)
daedalus personas create reviewer

# Use a custom persona
daedalus --persona reviewer my-app /path/to/project

# Start a project from a GitHub URL
daedalus https://github.com/user/repo

# Or use owner/repo shorthand
daedalus user/repo

# Switch a project's build target
daedalus config my-app --set target=godot

# Set Copilot as the default runner for a project
daedalus config my-app --set runner=copilot

# Per-project configuration
daedalus config my-app --set display=true

# Shell completions
daedalus completion bash

# Show help
daedalus --help
```

## TUI Dashboard

An interactive terminal dashboard for managing all registered projects.

```bash
daedalus tui
```

<video src="assets/tui-demo.mp4" width="100%" autoplay loop muted></video>

**Key bindings:** `j`/`↓` move down, `k`/`↑` move up, `s` start (auto-attaches to tmux), `a` attach to running session, `Del` stop container, `n` create new project, `F2` rename, `r` refresh, `q` quit.

The dashboard shows each project's name, running status, build target, session count, and last-used time. Status refreshes automatically every 5 seconds.

**Creating a project:** Press `n` to enter create mode. Type a project name and press Enter, then browse the filesystem to select a directory (`j`/`k` to navigate, `Enter` to open, `Backspace` to go up, `s` to select, `c` to create a new subdirectory). Press `Esc` to cancel at any step.

## Web UI Dashboard

A browser-based dashboard for managing projects with an embedded terminal.

```bash
daedalus web                    # Start on localhost:3000
daedalus web --port 8080        # Custom port
daedalus web --host 0.0.0.0    # Bind to all interfaces
```

<video src="assets/web-demo.mp4" width="100%" autoplay loop muted></video>

The web UI provides:
- **Project list** with live status (running/stopped), target, and last-used time. Auto-refreshes every 5 seconds.
- **Start/Stop** buttons for each project (launches container in a tmux session).
- **Attach** button that opens an xterm.js terminal in the browser, connected to the tmux session via WebSocket.

**Security:** Authentication is enabled by default. On first launch, a random access token is generated, saved to `config.json`, and printed to the terminal. Enter the token in the login page to start a session (cookie-based, default 24h expiry). Use `--no-auth` to disable authentication. Binds to `127.0.0.1` by default (localhost only); use `--host 0.0.0.0` for remote access.

### WSL2

When running inside WSL2, `daedalus web` auto-detects the environment and binds to `0.0.0.0` so the Windows host can reach it. The startup output prints the VM IP for easy access.

To make the Web UI reachable from **other machines on your LAN**, Windows needs a port proxy and firewall rule. A helper script is included:

```bat
rem Run in an elevated Command Prompt on the Windows host

rem Enable LAN access (default port 3000)
wsl2-network.bat enable

rem Enable on a custom port
wsl2-network.bat enable 8080

rem Disable
wsl2-network.bat disable
```

> **Note:** WSL2's internal IP changes on reboot. Re-run `enable` after restarting WSL2 to update the forwarding rule.

## Build Targets

The single `Dockerfile` uses a multi-stage build with six stages:

| Target | Description |
|---|---|
| `base` | Minimal Debian bookworm-slim with Claude CLI, git, and networking tools |
| `utils` | Extends base with unzip, wget, build-essential |
| `dev` (default) | Full dev environment: Go, Python 3, OpenJDK 17, Maven, Kotlin |
| `godot` | Godot 4.x engine for headless game CI, exports, and tests |
| `copilot-base` | Minimal base with Copilot CLI (via [gh.io installer](https://gh.io/copilot-install)) instead of Claude CLI |
| `copilot-dev` | Copilot CLI with full dev environment (Go, Python 3, OpenJDK 17, Maven, Kotlin) |

Select a target with `--target`:

```bash
daedalus --build --target godot my-game /path/to/game-project
```

### Creating a New Target

Add a new `FROM … AS <name>` stage to the `Dockerfile`. Extend `base` (minimal) or `utils` (adds build tools) depending on what you need.

```dockerfile
# ── Stage: rust ─────────────────────────────────────────────────────────────
# Rust development environment.
FROM utils AS rust

USER root

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      pkg-config libssl-dev && \
    rm -rf /var/lib/apt/lists/*

USER claude
RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y
ENV PATH="/home/claude/.cargo/bin:$PATH"
```

Then build and run with the new target:

```bash
daedalus --build --target rust my-rust-app /path/to/project
```

**Guidelines:**

- Always switch back to `USER claude` at the end of the stage.
- Clean up apt lists (`rm -rf /var/lib/apt/lists/*`) to keep images small.
- The stage name becomes the `--target` value — keep it short and lowercase.

## Authentication

On first use, run `claude /login` inside the container to authenticate. Credentials are stored in the per-project cache directory (`.cache/<project>/`) and persist across container restarts — no host-side credential setup required.

```bash
# Start a project, then log in inside the container
daedalus my-app /path/to/project
claude /login
```

## Project Registry

Projects are tracked in `.cache/projects.json` with metadata (directory, target, timestamps). On first run, existing `.cache/*/` directories are auto-migrated into the registry.

When starting with a project name that isn't registered:
- **Interactive mode** — prompts to create a new project or abort
- **Headless mode** (piped stdin or `-p` flag) — auto-registers the project silently

Use `daedalus list` to see all registered projects with their directories, targets, and last-used timestamps.

Each container is named `claude-run-<project-name>`. If a container with that name is already running, `daedalus` exits with an error instead of starting a second instance.

## Home Directory Persistence

Container home directories are persisted across runs via `.cache/<project-name>/` on the host, bind-mounted as `/home/claude`. This preserves shell history, tool caches, and session state between container restarts.

Session transcripts survive container removal, enabling `--resume` to work across runs:

```bash
# Run a session, note the session ID, then exit
daedalus my-app /path/to/project

# Resume that session later
daedalus --resume <session-id> my-app
```

## Configuration File

A default `config.json` is installed next to the binary. The installer automatically sets `data-dir` to `<install-dir>/.cache`. Edit the file to customize settings that would otherwise require CLI flags or environment variables.

**Location:** `<install-dir>/config.json` (default: `~/.local/share/daedalus/config.json`)

**Precedence:** CLI flags > environment variables > `config.json` > built-in defaults

```json
{
  "data-dir": "/mnt/data/daedalus",
  "debug": true,
  "no-tmux": false,
  "image-prefix": "custom/claude-runner",
  "log-file": "/mnt/data/daedalus/daedalus.log",
  "runner": "claude"
}
```

All fields are optional. The file itself is optional — Daedalus works without it. An empty `{}` is valid.

| Key | Type | Description |
|---|---|---|
| `data-dir` | string | Base directory for registry and per-project caches. Must be an absolute path. Set during installation to `<install-dir>/.cache`. |
| `debug` | bool | Enable Claude Code debug mode |
| `no-tmux` | bool | Run without tmux session wrapping |
| `image-prefix` | string | Docker image prefix (default: `techdelight/claude-runner`). For copilot agent, `claude-runner` is automatically replaced with `copilot-runner`. |
| `log-file` | string | Path to the runtime log file (default: `<data-dir>/daedalus.log`) |
| `runner` | string | Default AI runner: `claude` (default), `copilot`, or a user-defined persona name |

## MCP Servers

Daedalus supports [MCP servers](https://modelcontextprotocol.io/) configured in `claude.json` at the repo root. The file is copied as `.claude.json` into the Docker image at build time, so **rebuild after any changes** (`daedalus --build`).

**Transport types:**

| Type | Example |
|---|---|
| `stdio` | Spawns a subprocess inside the container |
| `http` | Connects to a remote URL |

```json
{
  "mcpServers": {
    "my-stdio-server": {
      "type": "stdio",
      "command": "docker",
      "args": ["run", "-i", "--rm", "my-mcp-image:latest"]
    },
    "my-http-server": {
      "type": "http",
      "url": "https://mcp.example.com/mcp"
    }
  }
}
```

**Container restrictions:**

- **All Linux capabilities are dropped** — servers cannot escalate privileges.
- **stdio servers that spawn Docker** require the `--dind` flag (mounts the host Docker socket).
- **HTTP servers running on the host** must use `host.docker.internal` instead of `localhost` (e.g. `http://host.docker.internal:3000/mcp`).
- The container filesystem is limited to `/workspace` (project mount) and `/home/claude` (persistent home).

**Per-project override:** edit `.cache/<project>/.claude-config/.claude.json` directly — this file is persisted in the project's home directory and is not overwritten after the first run.

## Skill Catalog

Daedalus includes a shared skill catalog — a directory of Claude Code skill files (`.md`) that are available to all projects. Skills are stored at `<data-dir>/skills/` on the host and mounted read-write into every container at `/opt/skills`.

**From the host CLI:**

```bash
daedalus skills                    # List all skills in the catalog
daedalus skills add my-skill.md    # Add a skill file to the catalog
daedalus skills show commit        # Print a skill's content
daedalus skills remove commit      # Remove a skill from the catalog
```

**From inside a container**, Claude Code automatically discovers the `skill-catalog` MCP server and can use these tools:

| Tool | Description |
|---|---|
| `list_skills` | List all skills in the shared catalog |
| `read_skill` | Read the full content of a skill |
| `install_skill` | Install a skill into `.claude/skills/` |
| `uninstall_skill` | Remove a skill from `.claude/skills/` |
| `create_skill` | Publish a new skill to the shared catalog |
| `update_skill` | Update an existing skill in the catalog |
| `remove_skill` | Delete a skill from the catalog |
| `list_installed` | List skills installed in the current project |

Installing a skill copies it from the catalog to the project's `.claude/skills/` directory, where Claude Code automatically discovers it. The catalog is seeded with starter skills (`commit.md`, `review.md`) on first run.

## Project Management

Each container includes a `project-mgmt` MCP server that Claude Code can use to report project progress back to Daedalus.

**Available MCP tools (inside the container):**

| Tool | Description |
|---|---|
| `report_progress` | Set completion percentage (0-100) with optional status message |
| `set_vision` | Set the project vision statement |
| `set_version` | Set the project version string |
| `get_progress` | Read current progress data |

Progress data is stored in `.daedalus/progress.json` in the project directory, visible to both the container and the host. The Web UI dashboard reads this file to display real-time progress.

Click any project name in the Web UI to see the project dashboard with progress bar, version, total session time, and vision. The dashboard also includes a "Show Roadmap" button to view parsed sprint data from the project's `ROADMAP.md`.

**MCP roadmap tools (inside the container):**

| Tool | Description |
|---|---|
| `get_roadmap` | Parse and return all sprints from the project's ROADMAP.md |
| `get_current_sprint` | Return the current sprint only |

## Programmes

Programmes group multiple projects with dependency relationships for coordinated orchestration.

```bash
daedalus programmes                                    # List all programmes
daedalus programmes create my-platform                 # Create a programme
daedalus programmes add-project my-platform api        # Add a project
daedalus programmes add-project my-platform frontend   # Add another project
daedalus programmes add-dep my-platform api frontend   # Declare dependency
daedalus programmes show my-platform                   # Show with status
daedalus programmes cascade my-platform --dry-run      # Preview cascade
daedalus programmes remove my-platform                 # Delete
```

Dependencies have cascade strategies: `auto` (Foreman acts), `notify` (human approves), `manual` (skip). Default: `notify`.

## The Foreman

The Foreman is an AI-driven project manager that runs inside `daedalus web`. It monitors a programme, reads roadmaps from member projects, tracks agent state, and reports through the Web UI.

```bash
# Start the web server (Foreman runs inside it)
daedalus web

# Manage via REST API
# POST /api/foreman/start   — body: {"programme": "my-platform"}
# POST /api/foreman/stop
# GET  /api/foreman/status   — returns state, plan, cascade log
```

The Foreman status indicator appears in the Web UI header when active.

## Persona Configurations

Named persona configurations let you define custom personas that layer system prompts, tool permissions, and environment variables on top of a built-in runner (`claude` or `copilot`). Configs are stored as JSON in `<data-dir>/personas/`.

**Managing personas:**

```bash
daedalus personas                    # List all user-defined personas
daedalus personas create reviewer    # Create a new persona config (interactive)
daedalus personas show reviewer      # Print the full JSON config
daedalus personas remove reviewer    # Delete a persona config
```

**Using a custom persona:**

```bash
# One-time use
daedalus --persona reviewer my-app /path/to/project

# Set as project default
daedalus config my-app --set persona=reviewer
```

**File layout:**

Each persona is stored as a pair of files in `<data-dir>/personas/`:

```
personas/
  reviewer.json    # config (name, baseRunner, settings, env)
  reviewer.md      # CLAUDE.md content injected into the container
```

**JSON config (`reviewer.json`):**

```json
{
  "name": "reviewer",
  "description": "Code review specialist",
  "baseRunner": "claude",
  "settings": { "permissions": { "allow": ["Read", "Glob", "Grep"] } },
  "env": { "REVIEW_MODE": "strict" }
}
```

**Markdown file (`reviewer.md`):**

```markdown
You are a code reviewer. Focus on bugs, security, and readability.
```

| Field | Location | Description |
|---|---|---|
| `name` | `.json` | Unique name (must not collide with built-in runners) |
| `baseRunner` | `.json` | Built-in runner binary to use: `claude` or `copilot` |
| `settings` | `.json` | Override for `.claude/settings.json` tool permissions (optional) |
| `env` | `.json` | Extra environment variables passed to the container (optional) |
| CLAUDE.md content | `.md` | Custom system prompt injected into the container (optional) |

When a user-defined persona is selected, Daedalus writes the overlay files to a temp directory and mounts them read-only into the container. The base runner binary runs as normal — the persona is purely file-based.

## Sharing Skills Across Projects

Claude Code reads instructions from several locations inside the container. Use these to share skills, conventions, and workflows across projects.

| Source | Scope | How it works |
|---|---|---|
| Skill Catalog | All projects | Shared `.md` files in `<data-dir>/skills/`, mounted at `/opt/skills`. Managed via `daedalus skills` or MCP tools |
| Project `CLAUDE.md` | Per-project | Mounted automatically at `/workspace/CLAUDE.md` from the project directory |
| Project `.claude/` dir | Per-project | Mounted at `/workspace/.claude/` — place commands, settings, or memory files here |
| `claude.json` | All projects (same image) | Copied as `.claude.json` into the image; seeds `/home/claude/.claude-config/.claude.json` on first run |
| `settings.json` | All projects (same image) | Baked into the image; seeds `/home/claude/.claude-config/settings.json` on first run |

**Per-project override:** the persistent home directory at `.cache/<project>/.claude-config/` stores the runtime copies of `.claude.json` and `settings.json`. Edit them directly to override image-level defaults for a single project without rebuilding.

## Man Page

A man page is included for offline reference:

```bash
# Install the man page
sudo cp daedalus.1 /usr/local/share/man/man1/
sudo mandb
man daedalus

# Or generate a fresh copy
go run ./cmd/generate-manpage > daedalus.1
```

## Logging

Daedalus writes runtime logs to a persistent file for post-mortem debugging. Log entries include timestamps, levels (`INFO`, `DEBUG`, `ERROR`), and key events.

**Default location:** `<data-dir>/daedalus.log`

Customize via `log-file` in `config.json`. Debug-level messages are only written when `--debug` is enabled.

## Auto-Rebuild

Daedalus tracks a SHA-256 checksum of build-relevant files (Dockerfile, entrypoint.sh, docker-compose.yml, settings.json, claude.json). After an install or upgrade that changes these files, the Docker image is automatically rebuilt on next project start. No manual `--build` needed.

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `DAEDALUS_DATA_DIR` | `.cache` next to binary | Base directory for registry and per-project caches |
| `NO_COLOR` | (unset) | Disable colored output when set |

## Security Model

- Runs as non-root `claude` user (UID matched to caller)
- All Linux capabilities dropped
- `no-new-privileges` prevents privilege escalation

## Requirements

- Docker and Docker Compose
- (Optional) `tmux` for detach/reattach support

## Dev Builds

Rolling pre-release builds are published automatically from the latest `development` branch. These are useful for testing in-progress work before a stable release.

```bash
# Install the latest dev build (binary + runtime files + symlink)
curl -fsSL https://github.com/techdelight/daedalus/releases/download/dev/install.sh | bash
```

Dev builds are marked as pre-release on the [Releases page](https://github.com/techdelight/daedalus/releases/tag/dev) and report a version like `0.8.1-dev+abc1234`. They are overwritten on every push to `development`.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for build instructions, development workflow, and technical details. See [ARCHITECTURE.md](ARCHITECTURE.md) for system design.

CI runs automatically on pull requests via GitHub Actions (vet, test, build).

## License

Apache-2.0 — see [LICENSE](LICENSE) for details.
