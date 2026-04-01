# What's New in Daedalus (March 11 – April 1, 2026)

Three weeks, 17 releases (v0.8.2 → v0.29.0), and over 100 commits. Here's what changed.

---

## The Foreman — AI-Driven Project Management

Daedalus now includes an AI project manager called **the Foreman** that runs inside `daedalus web`.

- **Programmes** — group multiple projects with dependency relationships. Declare which projects depend on each other and how work should cascade between them.
- **Cascade orchestration** — when an upstream project completes, the Foreman evaluates downstream projects using configurable strategies: `auto` (act immediately), `notify` (flag for human approval), or `manual` (skip).
- **Sprint planning** — the Foreman reads each project's `ROADMAP.md`, parses it into structured sprint data, and builds a plan across the programme.
- **Agent monitoring** — tracks whether each project's agent is running, idle, stopped, or in error state via Docker container inspection.
- **Web UI** — a dedicated Foreman view with live status indicator, programme selector, active plan display with progress bars, cascade event log, and full programme CRUD (create, edit, delete).
- **CLI** — `daedalus foreman start|stop|status` and `daedalus programmes list|show|create|add-project|add-dep|cascade|remove`.

## Web UI Authentication

The Web UI is now protected by default.

- A random access token is generated on first `daedalus web` launch and saved to `config.json`.
- Login page with token input, styled to match the Daedalus dark theme.
- Session cookie (`daedalus_session`) with configurable expiry (default 24 hours).
- WebSocket connections authenticate via the session cookie automatically, or via `?token=` query parameter.
- Use `--no-auth` to disable for local-only development.

## Project Management Inside Containers

Agents can now report their progress back to Daedalus in real time.

- **Project management MCP server** (`project-mgmt-mcp`) runs inside each container with tools: `report_progress`, `set_vision`, `set_version`, `get_progress`.
- **Roadmap tools** — `get_roadmap` and `get_current_sprint` parse the project's `ROADMAP.md` and return structured sprint data.
- **Dashboard** — click any project name in the Web UI to see progress bar, version, total session time, vision statement, and a "Show Roadmap" panel.
- **Host-side MCP client** — Daedalus reads project state from bind-mounted `.daedalus/progress.json` files, enabling aggregated programme views.

## GitHub Repo Projects

Start a project directly from a GitHub URL — Daedalus clones the repo and registers it in one step.

```bash
daedalus https://github.com/user/repo
daedalus user/repo                        # shorthand
```

## Personas and Runner/Persona Split

The overloaded "agent" concept is now cleanly split into **runners** (claude/copilot binary) and **personas** (user-defined configuration overlays).

- Create named personas with custom system prompts, tool permissions, and environment variables.
- Personas layer on top of a built-in runner — select with `--persona reviewer`.
- `daedalus personas create|list|show|remove` and `daedalus runners list|show`.
- `--runner` and `--persona` flags replace the old `--agent` flag (still accepted as alias).

## Skill Catalog

A shared skill repository mounted into every container.

- Skills stored as `{name}/SKILL.md` directories (changed from flat `.md` files in v0.27.0).
- `skill-catalog-mcp` MCP server with 8 tools for browsing, installing, and publishing skills.
- `daedalus skills list|add|remove|show` from the host CLI.
- Seeded with starter skills (`commit`, `review`) on first run.

## Mobile Web UI

The web dashboard is now usable on phones and tablets.

- Scrollable terminal with 10,000-line buffer.
- Multi-line mobile input area — textarea with Send button below the terminal.
- Select mode for native text selection via long-press.
- Card-based project list on mobile with larger touch targets.

## Agent Observability

Real-time agent state tracking in the Web UI.

- Pulsing status dot on each project card (running, stopped, idle, error, unknown).
- `GET /api/projects/{name}/state` endpoint.
- Used by the Foreman to monitor agent health across a programme.

## Active Project Filter

Filter the project list to show only running projects — helpful when the list grows.

- Web UI "Active Only" toggle button (state persisted in localStorage).
- TUI `[f]` keybinding.

## Configuration Improvements

- **Switch build target** — `daedalus config my-app --set target=godot` changes the Docker build target without re-registering.
- **Config.json auth fields** — `auth-token` and `auth-expiry` for persisting Web UI authentication settings.
- **MCP server reconciliation on startup** — the entrypoint ensures daedalus-specific MCP servers are present in the runner's config, adding missing entries without overwriting user customizations.

## Display Sharing

Forward the host X11/Wayland display into containers so GUI apps render on your screen.

- `--display` flag, prompted during first-run project registration.
- Supports WSL2 (`DISPLAY` + X socket) and native Linux (Wayland socket forwarding).

## Copilot CLI Support

GitHub Copilot CLI as an alternative coding agent alongside Claude Code.

- `--runner copilot` or `daedalus config my-app --set runner=copilot`.
- Dedicated `copilot-base` and `copilot-dev` Dockerfile stages.
- Agent dispatch in entrypoint based on `$RUNNER` env var.

## Build & DevOps

- **Auto-rebuild** — SHA-256 checksums of build files; Docker image rebuilds automatically after install/upgrade.
- **Persistent logging** — runtime logs to `<data-dir>/daedalus.log`.
- **Container logging** — `--container-log` tees container output to a file.
- **Dev releases** — rolling pre-releases from the development branch.
- **Installer split** — `install.sh` (downloader) + `setup.sh` (installer) for cleaner upgrades.
- **macOS fixes** — portable `sed -i`, symlink resolution, bash 3.2 compat.
- **Go 1.25** — build toolchain upgraded from Go 1.19 to 1.25.

## Web UI Polish

- **Favicon** — SVG labyrinth motif visible in browser tabs.
- **Foreman project navigation** — clicking a project card in the Foreman view navigates to its dashboard.
- **Browser tab title** — shows the active project name.
- **WSL2 auto-detect** — binds to `0.0.0.0` on WSL2 so the Windows host can reach the dashboard.

## Test Coverage

- **Go tests** — `core` at 98%, `web` at 60.6%, 24 packages tested.
- **Playwright E2E** — 34 API-level tests covering static assets, HTML structure, all REST endpoints, programme CRUD lifecycle, and auth modes. Plus pre-existing browser-based tests for terminal, mobile, project list, dashboard, and Foreman views.

## By the Numbers

| Metric | Value |
|---|---|
| Releases | 17 (v0.8.2 → v0.29.0) |
| New packages | 8 (`auth`, `agentstate`, `catalog`, `foreman`, `mcpclient`, `personas`, `programme`, `progress`) |
| New binaries | 2 (`skill-catalog-mcp`, `project-mgmt-mcp`) |
| New CLI subcommands | 5 (`skills`, `runners`, `personas`, `programmes`, `foreman`) |
| New CLI flags | 8 (`--display`, `--runner`, `--persona`, `--container-log`, `--auth`, `--no-auth`, plus legacy `--agent`) |
| New API endpoints | 14 (dashboard, roadmap, state, foreman ×3, programmes ×5, terminal, login) |
| Sprints completed | 14 (Sprint 14 through Sprint 33, with some numbered as fix/polish sprints) |
