# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Added
- **`/sprints`, `/backlog`, `/strategic-roadmap` web endpoints** — three new REST handlers in `internal/web` matching the post doc-split frontend, which had been calling these URLs since v0.37 even though only the legacy `/roadmap` was registered. `/sprints` reads `SPRINTS.md` with fallback to `ROADMAP.md`, `/backlog` parses `BACKLOG.md` via `core.ParseBacklog`, `/strategic-roadmap` returns the raw `ROADMAP.md` content. The legacy `/roadmap` route now also uses the SPRINTS-first fallback and remains as an alias.

### Fixed
- **Project-detail roadmap panels stayed empty** — after the v0.37 doc split, the project-detail view fetched `/sprints`, `/backlog`, and `/strategic-roadmap`, all of which 404'd because only the old `/roadmap` route was wired up. Panels stayed hidden or showed "not found". Adding the three handlers restores the panels. (Backlog #34)
- **Large paste kills WebSocket** — pasting text containing newlines (or any multiline input on mobile) terminated the tmux control-mode `send-keys` command at the first `\n`, desyncing the response queue and dropping the WebSocket connection. Added `core.BuildControlSendKeys` which translates newlines to `Enter` keystrokes and uses `send-keys -l` (literal) for non-newline content, keeping the resulting command on a single line. (Backlog #47, #48)

### Changed
- **Deduplicated `ShellQuote`** — removed `internal/session.ShellQuote` (a copy of `core.ShellQuote`) and routed `internal/session` and `internal/web` through `core.ShellQuote`. Per ARCHITECTURE/CONTRIBUTING, command builders belong in `core/`.

## [0.37.0] - 2026-04-18

### Added
- **Document structure split** — separated monolithic `ROADMAP.md` into three purpose-specific files: `ROADMAP.md` (strategic milestones), `BACKLOG.md` (prioritised work items), `SPRINTS.md` (sprint execution history).
- **Backlog parser** — `core/backlog.go` with `BacklogItem` type and `ParseBacklog()` function for parsing `BACKLOG.md` tables.
- **New MCP tools** — `get_sprints` (parse sprints from `SPRINTS.md`), `get_backlog` (parse backlog items), `get_strategic_roadmap` (raw roadmap content). All sprint tools fall back to `ROADMAP.md` for backward compatibility.
- **Muse starter skill** — new `muse.md` starter skill in the skill catalog.

### Changed
- **`ParseRoadmap` renamed to `ParseSprints`** — reflects that sprint data now lives in `SPRINTS.md` rather than `ROADMAP.md`.
- **`get_roadmap` / `get_current_sprint` MCP tools** — now read from `SPRINTS.md` first, falling back to `ROADMAP.md` for projects that haven't migrated.
- **MCP client** — updated methods to use new tool names and added methods for backlog and strategic roadmap queries.

## [0.36.0] - 2026-04-12

### Added
- **JRPG Guild Hall UI** — new web view where each project is a pixel-art mage avatar with state-based animations. Avatars bounce with particles when busy, float gently when idle, and dim with floating "zzz" when sleeping. Each project gets a unique color palette derived from its name. Click an avatar to navigate to the project dashboard.
- **Runner-agnostic activity detection** — new `internal/activity/` package with `RunnerActivityDetector` interface, `DetectorRegistry` for mapping runner names to detectors, and `NullDetector` fallback. Adding a new runner requires only a detector implementation and one `Register()` call.
- **Claude Code `Stop` hook** — the definitive "finished processing" signal. Previously idle detection relied on `Notification` + 30-second staleness timeout; the `Stop` hook fires after ALL processing completes.
- **Three new Claude Code hooks** — `Stop` (idle), `PostToolUse` (sustained busy), and `UserPromptSubmit` (transition to busy), bringing total activity hooks to 6.
- **`HookConfig` in `RunnerProfile`** — each runner profile carries its activity hook definitions, connecting hook configuration to runner identity.
- **Settings generation** — `internal/hooks/` package renders runner-specific `settings.json` from `HookConfig` templates with placeholder substitution.
- **`GET /api/guild`** — REST endpoint returning all projects with unified three-state activity (busy/idle/sleeping), progress, and metadata for the guild hall view.

### Changed
- **`GET /api/projects/{name}/state`** — now returns activity-level state (`busy`/`idle`/`sleeping`) via `activityResolver` instead of raw container state. Response includes `containerState` field for backward compatibility.
- **Resolver is runner-aware** — `Resolve(containerName, projectDir, runnerName)` selects the correct detector per project based on its runner configuration.

### Fixed
- **Removed broken tests** — deleted test functions referencing unimplemented handlers (`handleSprints`, `handleBacklog`, `handleStrategicRoadmap`) that caused CI build failures.

## [0.35.0] - 2026-04-04

### Fixed
- **WebSocket resize race condition** — the tmux reader goroutine and WebSocket handler goroutine were both calling `ReadMessage()` on the same `bufio.Reader`, racing for command responses. Refactored `handleTerminalControl()` so a single reader goroutine consumes all tmux control-mode output, with a `sendTracked`/`dequeueType` queue to match `%begin/%end` responses to commands.
- **No terminal refresh after resize** — resize messages produced no visible response because `%layout-change` events were silently dropped. The reader goroutine now auto-captures visible pane content on `%layout-change` and sends a `live-capture-response` back to the client.
- **Terminal staircase formatting** — captured pane content was joined with `\n` (LF only), causing xterm.js to indent each line progressively. Changed to `\r\n` (CRLF) so the cursor returns to column 0 on each line.
- **Discarded errors in web handlers** — fixed 8 silently discarded errors in `web.go`: PTY cleanup (`Close`, `Wait`, `Signal`), PTY relay (`Setsize`, `Write`), progress read, index HTML write, and WebSocket response write. All now either log or return on error per contributing guidelines.

### Changed
- `shellQuote` exported as `ShellQuote` in `internal/session` package (required by the refactored `handleTerminalControl` which builds tmux commands directly).
- **Pipeline version in config.json** — release workflow patches `config.json` with the semantic version (e.g. `0.34.0`); dev-release workflow patches it with `dev_{timestamp}` (e.g. `dev_20260404120000`). Version patching removed from `install.sh` — now handled entirely at build time. (Backlog #43)

## [0.34.0] - 2026-04-03

### Fixed
- **Blank terminal on attach** — terminal now sends a `live-capture` request immediately after WebSocket connect and resize, so the current pane content is displayed right away instead of waiting for new tmux output. Especially impactful on mobile where the blank + disabled-input terminal appeared broken. (Backlog #42)
- **Foreman roadmap display** — `showDashboard()` now resets the roadmap panel and auto-loads the roadmap via `loadRoadmap()` when opening a project from the Foreman or project list. Previously the roadmap panel could show stale data or remain empty. (Backlog #41)

### Added
- `CaptureVisible()` and `CaptureVisible_Error` unit tests for the control session package.
- Backlog items 41–42 added to ROADMAP.md.

## [0.33.0] - 2026-04-02

### Added
- **History mode visual indicator** — blue "HISTORY MODE" banner with hint text appears between terminal header and content when viewing scrollback. History button highlights with active state. (Backlog #35)
- **History mode exit** — press Esc, any key, or click the Exit button in the banner to leave history mode. Exiting sends a `live-capture` request to restore the current visible pane content. (Backlog #36)
- **Live capture endpoint** — new `CaptureVisible()` method on `ControlSession` captures only the visible pane (no scrollback depth). New `live-capture` WebSocket message type in `handleTerminalControl()`.
- **Scroll recovery after crash/disconnect** — history mode state resets automatically on WebSocket close, error, or terminal disconnect, preventing stale scroll viewport. (Backlog #40)
- Backlog items 34–40 added to ROADMAP.md.

## [0.32.0] - 2026-04-01

### Added
- **tmux control mode web terminal** — `handleTerminalControl()` in `internal/web/web.go` uses `ControlSession` (tmux `-C`) instead of raw PTY relay. Activated via `?mode=control` query parameter on the terminal WebSocket endpoint. Both modes coexist.
- **Scrollback support** — "History" button in the terminal header requests the last 1000 lines of pane scrollback via `CapturePane()`. Client sends `{"type":"scrollback","lines":N}`, server responds with `{"type":"scrollback-response","content":"..."}`.
- Web UI terminal now defaults to control mode for scrollback access.

## [0.31.0] - 2026-04-01

### Added
- **tmux control mode session** — `ControlSession` in `internal/session/control.go` spawns `tmux -C attach-session` and provides structured message I/O: `SendKeys()`, `CapturePane()`, `ResizeWindow()`, `ReadMessage()`, `Close()`.
- **Control message parser** — `ParseControlLine()` in `internal/session/controlparser.go` parses all `%`-prefixed tmux control mode messages: `%output`, `%begin`, `%end`, `%error`, `%layout-change`, `%session-changed`, `%window-renamed`, `%pane-mode-changed`.
- 19 unit tests covering all message types, edge cases (empty input, special characters, spaces), `shellQuote()`, and `safeIndex()`.

## [0.30.0] - 2026-04-01

### Added
- **tmux control mode design document** (`docs/tmux-control-mode.md`) — research spike covering current PTY relay architecture, tmux `-C` protocol analysis, component impact assessment, phased implementation plan (4 phases, ~850 lines, 3 sprints), and migration strategy. Approved for implementation.

## [0.29.1] - 2026-04-01

### Fixed
- Playwright e2e: scoped `.btn-back` selector to `#terminal-view` to fix strict mode violation when multiple Back buttons exist.

### Added
- Playwright e2e test suite (34 tests) covering static assets, HTML structure, all REST API endpoints, programme CRUD lifecycle, and auth modes.
- Go test coverage improvements: `core` 98%, `cmd/skill-catalog-mcp` from 0% to tested, `internal/web` 60.6%.
- `new-and-improved.md` — summary of all changes from v0.8.2 to v0.29.0.

### Changed
- README and ARCHITECTURE synced with codebase: auth config fields, skill directory structure, `/login` route, `ValidTargets()`, `UpdateProjectTarget()`, GitHub URL parsing.

## [0.29.0] - 2026-04-01

### Added
- **Switch target for existing project** — `daedalus config <name> --set target=<stage>` changes the build target without re-registering. Validates against known targets (dev, godot, base, utils).
- `UpdateProjectTarget()` method in registry package.
- `ValidTargets()` and `IsValidTarget()` functions in core package.
- **GitHub repo projects** — pass a GitHub URL or `owner/repo` shorthand as the project name to clone and register in one step. E.g., `daedalus https://github.com/user/repo` or `daedalus user/repo`.
- Tests for target switching, GitHub URL parsing, and registry `UpdateProjectTarget`.

## [0.28.0] - 2026-04-01

### Added
- **Web UI authentication** — token-based login protects the dashboard when exposed on a network. A cryptographically random access token is generated on first launch and stored in `config.json`.
- `--auth` / `--no-auth` flags for `daedalus web` — authentication is enabled by default; use `--no-auth` to disable.
- Login page at `/login` with token input form, styled to match the Daedalus theme.
- Session cookie (`daedalus_session`) with configurable expiry (default 24 hours, `auth-expiry` in `config.json`).
- WebSocket authentication via session cookie (automatic) or `token` query parameter (fallback).
- `internal/auth` package with 12 unit tests covering token generation, middleware, login flow, cookie handling, and query parameter auth.
- Shell completions for `--auth` and `--no-auth` flags in bash, zsh, and fish.

## [0.27.0] - 2026-04-01

### Added
- **Favicon** — SVG favicon with labyrinth motif added to the Web UI, visible in browser tabs.
- **Foreman UI project navigation** — clicking a project card in the Foreman view now navigates to that project's detail/dashboard view.

### Changed
- **Skill catalog directory structure** — skills are now stored as `{name}/SKILL.md` directories instead of flat `{name}.md` files. Applies to both the shared catalog and per-project installed skills. Starter skills are seeded in the new format on first run.

## [0.26.2] - 2026-04-01

### Added
- Backlog item #32: Foreman UI project navigation — clicking a project opens its detail view.
- Backlog item #33: tmux control mode integration — native scrollback, clean disconnect/reconnect, event notifications.

## [0.26.1] - 2026-03-30

### Added
- **MCP server reconciliation on startup** — entrypoint now ensures daedalus-specific MCP servers (`skill-catalog`, `project-mgmt`) are present in the runner's config. Missing entries are added from defaults; existing entries and user-added servers are preserved.

### Fixed
- `project-mgmt-mcp` panic on startup caused by `google/jsonschema-go` v0.4.2 rejecting `description=` prefixed struct tags. Tags now use plain description strings.
- Added 12 tests for `project-mgmt-mcp` covering all MCP tools, error handling, and version fallback.

## [0.26.0] - 2026-03-30

### Added
- **Foreman web frontend** — dedicated view accessible from the main Daedalus page for managing the Foreman and its programmes.
  - Foreman status panel with live state indicator, programme selector, and Start/Stop controls.
  - Active plan display with project cards showing progress bars, agent state badges, and current sprint info.
  - Cascade event log with color-coded action badges (propagate/notify/skip).
  - Full programme CRUD: create, edit, and delete programmes with project lists and dependency edges.
- REST API endpoints for programme management: `GET/POST /api/programmes`, `GET/PUT/DELETE /api/programmes/{name}`.

### Changed
- Dev and copilot-dev Docker targets now install Go 1.25 from the official tarball instead of Debian's `golang-go` package (was Go 1.19).
- `build.sh` and `test.sh` updated to use `golang:1.25-bookworm` image.

## [0.25.1] - 2026-03-30

### Fixed
- Foreman `Start()` race condition — hold lock through goroutine launch to prevent TOCTOU between state check and `go f.run()`.
- Foreman `Stop()` double-close panic — added guard flag to prevent closing `stopCh` twice.
- `mcpclient.GetProjectStatus()` now propagates errors instead of silently returning partial data.
- `agentstate.ContainerObserver` returns `StateUnknown` (not `StateStopped`) when Docker is unreachable, preventing false "stopped" reports.
- Foreman planner and monitor now check registry and MCP client errors instead of silently ignoring them.
- `programme.Store.List()` returns error on corrupt JSON files instead of silently skipping them.
- Extracted shared `buildSummary()` function to eliminate duplication between planner and monitor.
- Added missing tests: `DefaultObserver` (GetState, IsActive), Foreman web handlers (status, start, stop), `programme.Store.Update()`, `agentstate` state constants and additional container states.

## [0.25.0] - 2026-03-30

### Added
- **Programme-level cascade orchestration** — when an upstream project completes, the Foreman evaluates which downstream projects need work based on the dependency graph and cascade strategy.
- `CascadeStrategy` type on `DependencyEdge` — `auto` (Foreman acts), `notify` (flag for human approval), `manual` (skip). Defaults to `notify`.
- `daedalus programmes cascade <name> [--dry-run]` — preview cascade propagation for a programme. Shows which downstream projects would be affected, with color-coded actions.
- Cascade event log in Foreman status API response (`cascadeLog` field).
- `EvaluateCascade()` function evaluates cascade actions for completed projects.

## [0.24.0] - 2026-03-30

### Added
- **The Foreman** — AI-driven project manager that monitors programmes. Reads roadmaps, builds plans, monitors agent state, and reports through the Web UI. Runs as a background goroutine inside `daedalus web`.
- `daedalus foreman` CLI subcommand — `start`, `stop`, `status` commands (delegates to Web UI API).
- Foreman REST API — `POST /api/foreman/start` (starts Foreman for a programme), `POST /api/foreman/stop`, `GET /api/foreman/status` (returns state, plan, and message).
- Foreman status indicator in Web UI header — shows "Foreman: monitoring" when active.
- `internal/foreman` package — `Foreman` (main loop), `Planner` (builds plans from programme data), `Monitor` (polls project and agent state).
- `core/foreman.go` — `ForemanConfig`, `ForemanState`, `ForemanPlan`, `ForemanProject`, `ForemanStatus` pure types.
- Shell completions for `foreman` subcommand in bash, zsh, and fish.

## [0.23.0] - 2026-03-30

### Added
- **Agent observability** — `internal/agentstate` package with `Observer` interface and `ContainerObserver` implementation that determines agent state from Docker container status.
- `GET /api/projects/{name}/state` REST endpoint returning current agent state (running, stopped, idle, error, unknown).
- Pulsing animation on running project status dots in the Web UI.
- `internal/foreman/observer.go` — `AgentObserver` interface and `DefaultObserver` wrapper for use in the Foreman loop.

## [0.22.0] - 2026-03-30

### Added
- `internal/mcpclient` package — host-side MCP client that reads project progress and roadmap data from bind-mounted files. Provides `ReadProgress()`, `ReadRoadmap()`, `GetCurrentSprint()`, and `GetProjectStatus()` methods.
- `daedalus programmes show <name>` now displays aggregated member project status — progress percentage, version, and current sprint for each project in the programme.

### Changed
- `programmes show` output changed from raw JSON dump to a formatted display with programme header, dependency graph, and per-project status table.

## [0.21.0] - 2026-03-30

### Added
- **Roadmap parsing** — `ParseRoadmap()` in `core/roadmap.go` parses Daedalus-native ROADMAP.md files into structured `Sprint` and `SprintItem` data. Detects current vs historical sprints.
- `GET /api/projects/{name}/roadmap` REST endpoint returning parsed sprint data from the project's ROADMAP.md.
- **Roadmap panel in Web UI** — click "Show Roadmap" in the project dashboard to see all sprints with items, statuses, goals, and version tags. Current sprints are highlighted.
- `get_roadmap` and `get_current_sprint` MCP tools in `project-mgmt-mcp` — agents can query the project's ROADMAP.md for sprint data.
- `core/sprint.go` — `Sprint`, `SprintItem`, `SprintStatus` pure types.

## [0.20.0] - 2026-03-30

### Added
- **Multi-project programmes** — declare named collections of related projects with dependency relationships. Foundation for programme-level orchestration.
- `daedalus programmes` CLI subcommand — `list` (shows all programmes), `show <name>` (prints full config), `create <name>` (creates empty programme), `add-project <programme> <project>` (adds project to programme), `add-dep <programme> <upstream> <downstream>` (declares dependency), `remove <name>` (deletes programme).
- `core/programme.go` — `Programme`, `DependencyEdge`, `DependencyGraph` types with topological sort, cycle detection, upstream/downstream queries (pure functions, zero I/O).
- `internal/programme` package — `Store` with `List`, `Read`, `Create`, `Update`, `Remove`, `AddProject`, `AddDep` operations, persisted as JSON files in `<data-dir>/programmes/`.
- Shell completions for `programmes` subcommand in bash, zsh, and fish.
- `ProgrammesDir()` method on `Config` for programme storage path.

## [0.19.0] - 2026-03-30

### Added
- **Project management MCP server** (`project-mgmt-mcp`) — runs inside each container, providing 4 tools via MCP stdio: `report_progress` (set completion %), `set_vision`, `set_version`, `get_progress`. Claude Code can use these tools to report project status back to Daedalus in real time.
- `internal/progress` package — read/write operations for `.daedalus/progress.json` files with partial-update semantics.
- `.daedalus/` directory mounted into containers for progress data exchange between agent and host.
- Dashboard endpoint now reads `.daedalus/progress.json` from the project directory, preferring real-time MCP-reported data over registry data.

### Changed
- `BuildExtraArgs` now mounts the project's `.daedalus/` directory into containers at `/workspace/.daedalus`.
- `build.sh` now builds three binaries: `daedalus`, `skill-catalog-mcp`, and `project-mgmt-mcp`.
- `claude.json` registers the `project-mgmt` MCP server alongside `skill-catalog`.
- `entrypoint.sh` ensures `/workspace/.daedalus/` directory exists on container startup.
- `Dockerfile` copies `project-mgmt-mcp` binary into the image.

## [0.18.0] - 2026-03-30

### Added
- **Project management dashboard** — click any project name in the Web UI to see a detail panel with progress bar, version, total session time, session count, and vision statement.
- `GET /api/projects/{name}/dashboard` REST endpoint returning full project dashboard data (progress percentage, vision, project version, total session time, session count, running status).
- `UpdateProjectProgress(name, pct, vision, projectVersion)` registry method for updating project progress metadata. Supports partial updates (only non-zero/non-empty values applied) and clamps percentage to 0-100.
- `ProgressPct`, `Vision`, and `ProjectVersion` fields on `ProjectEntry` for storing per-project progress metadata.

### Changed
- Registry schema upgraded from v2 to v3 (automatic migration on first read). New fields default to zero values — no data loss.

## [0.17.0] - 2026-03-29

### Added
- `daedalus runners` CLI subcommand — `list` (shows built-in runners with binary paths), `show <name>` (prints runner profile details).
- Shell completions for `runners` subcommand in bash, zsh, and fish.

### Changed
- Persona CLAUDE.md content is now stored in a companion `<name>.md` file alongside the `<name>.json` config, instead of being embedded in JSON. Easier to edit and version.
- Skill installation target changed from `~/.claude/commands/` to `/workspace/.claude/skills/` — the correct project-scoped location where Claude Code discovers skills.
- `daedalus personas list` now shows only user-defined personas, not built-in runners.
- `daedalus personas show <builtin>` now returns an error instead of printing runner details — use `daedalus runners show` instead.

### Fixed
- `resolvePersonaOverlay` now uses `cfg.Persona` instead of `cfg.Runner` to look up persona configurations. Previously the persona name was never read, so overlays were silently skipped.
- `resolvePersonaOverlay` now sets `cfg.Runner` from the persona's `BaseRunner` when no explicit `--runner` is given, ensuring the correct binary and Docker image are used.
- `--runner` flag now strictly accepts only built-in runner names (`claude`, `copilot`). Previously it also accepted persona names, blurring the runner/persona boundary.
- `--persona` flag now validated at parse time — rejects built-in runner names (use `--runner` instead) and nonexistent persona names.
- `collectDefaultFlags` now saves the `persona` key alongside `runner` for per-project defaults.
- Dev release workflow: replaced `softprops/action-gh-release` with `gh release create` to fix silent release creation failures.

## [0.16.0] - 2026-03-26

### Added
- **Named persona configurations** — users can define custom personas that layer system prompts, tool permissions, and environment variables on top of a built-in runner (Claude, Copilot). Configs stored as JSON in `<data-dir>/personas/`.
- `daedalus personas` CLI subcommand — `list` (shows built-in runners + user-defined personas), `show <name>` (prints full config), `create <name>` (interactive setup), `remove <name>` (deletes config).
- `--persona <name>` flag to select a user-defined persona.
- `--runner <name>` flag to select the runtime binary (`claude` or `copilot`), replacing the overloaded `--agent` flag.
- `core/persona.go` — `PersonaConfig` struct, `PersonaOverlay` struct, `PersonasDir()` method, `ValidatePersonaName()`.
- `core/runner.go` — `RunnerProfile` struct, `LookupRunner()`, `LookupBuiltinRunner()`, `ValidRunnerNames()`, `ResolveRunnerName()`, `IsBuiltinRunner()`, `BuiltinRunnerNames()`.
- `internal/personas` package — `Store` with `List`, `Read`, `Create`, `Update`, `Remove` operations for persona CRUD.
- `OverlayPaths` struct in `core/command.go` for injecting custom CLAUDE.md, settings.json, and environment variables into containers via volume mounts.
- Shell completions for `personas` subcommand and `--runner`/`--persona` flags in bash, zsh, and fish.
- Legacy `--agent` flag accepted as deprecated alias for `--runner`.
- Auto-migration of `<data-dir>/agents/` directory to `<data-dir>/personas/`.

### Changed
- **Terminology split**: "agent" is now two distinct concepts — **runner** (claude/copilot binary) and **persona** (user-defined configuration overlay).
- `BuildAgentArgs()` renamed to `BuildRunnerArgs()` (`BuildClaudeArgs()` kept as deprecated alias).
- `AGENT` environment variable renamed to `RUNNER` in docker-compose.yml, entrypoint.sh, and Dockerfile.
- `Config.Agent` field split into `Config.Runner` and `Config.Persona`.
- `config.json` field `"agent"` renamed to `"runner"` (legacy `"agent"` key still accepted).
- Help text updated with `personas` subcommand and `--runner`/`--persona` documentation.

## [0.15.0] - 2026-03-25

### Added
- Active project filter — Web UI "Active Only" button and TUI `[f]` keybinding toggle the project list to show only running projects. Helps users focus when the project list grows large.
- Web UI filter state persisted in `localStorage` so it survives page reloads.
- TUI title shows "(active only)" indicator when filter is active.
- Contextual empty-state messages: "No running projects." when filtered, "No registered projects." when unfiltered.

## [0.14.0] - 2026-03-25

### Added
- Mobile Select mode — replaces the Copy button with a Select toggle that overlays the terminal buffer as plain selectable HTML text, enabling native mobile text selection via long-press. Tap "Done" to dismiss the overlay and return to the live terminal.

### Changed
- Mobile Copy button removed in favour of Select mode, which gives users fine-grained native text selection instead of copying the entire buffer.

## [0.13.0] - 2026-03-22

### Added
- Mobile-friendly web UI — the dashboard is now usable on phones and tablets.
- Scrollable terminal output with 10 000-line scrollback buffer (touch-scroll works natively in xterm.js v5).
- Multi-line mobile input area below the terminal — textarea with Send button; Enter inserts newlines, Ctrl+Enter or Send button submits to the PTY. xterm.js stdin is disabled on mobile so the on-screen keyboard targets the textarea.
- Card-based project list on mobile — hides Target and Last Used columns, wraps each project as a card with larger touch targets for action buttons.
- JavaScript test suite for mobile terminal input (`internal/web/testdata/terminal_test.html`) — 16 tests covering send, keyboard shortcuts, `disableStdin`, focus prevention, event listener leak, and cleanup.

### Fixed
- Mobile terminal input not working — xterm.js's internal helper textarea was still focusable with `disableStdin: true`, stealing on-screen keyboard focus from the mobile input area. Tapping the terminal opened the keyboard but all typed characters were silently dropped. Fix: disable the xterm helper textarea on mobile and re-enable on resize back to desktop.
- Mobile input area hidden on real phones — `100vh` includes the browser chrome (URL bar, bottom navigation) on mobile browsers, pushing the input area off the visible viewport. Fix: override to `100dvh` (dynamic viewport height) on mobile with `-webkit-fill-available` fallback.

## [0.12.1] - 2026-03-24

### Fixed
- macOS install: portable `sed -i` for BSD/GNU compatibility.
- macOS install: resolve symlink in `ScriptDir` to find runtime files.
- macOS install: handle empty `FORWARD_ARGS` on bash 3.2.

### Added
- Install test for no-flags invocation covering bash 3.2.

## [0.12.0] - 2026-03-22

### Added
- `--container-log` CLI flag — tees all container stdout/stderr to `<data-dir>/<project>/container.log` for post-session debugging. Works in both direct and tmux modes (uses `io.MultiWriter` for direct, `tmux pipe-pane` for tmux sessions). Log path is printed at startup when enabled.
- `--verbose` flag for `install.sh` — enables shell tracing (`set -x`) for debugging installation issues.

### Fixed
- Copilot agent now uses agent-specific Docker image (`copilot-runner:dev`) and Dockerfile stage (`copilot-dev`) instead of always using `claude-runner:dev`.
- Agent-prefixed targets (e.g. `--target copilot-dev`) now auto-detect the agent and normalize the target, so `copilot-dev` becomes agent=`copilot` + target=`dev`. Fixes container launching Claude CLI instead of Copilot CLI when using composite targets.
- Auto-rebuild path (`NeedsRebuild`) now uses `BuildTarget()` instead of raw `Target`, ensuring the correct Dockerfile stage is built for non-claude agents.
- Copilot binary moved from `/home/claude/.local/bin/copilot` to `/usr/local/bin/copilot` — the `${CACHE_DIR}:/home/claude` volume mount was wiping the binary at container start.
- Copilot images now set `ENV AGENT="copilot"` so `docker run` without explicit AGENT env var launches the Copilot CLI instead of Claude.

### Changed
- Split `install.sh` into two scripts: `install.sh` (thin downloader) and `setup.sh` (installer). The downloader resolves the release tag, detects platform, downloads assets to a temp dir, and execs `setup.sh`. The installer handles file copy, config merge, symlink, and uninstall. `setup.sh` is uploaded as a release asset.
- `install.sh` now uses a `__RELEASE_TAG__` placeholder (falls back to `"latest"` when unpatched), replacing the previous `RELEASE_TAG="latest"` default. Pipelines sed-replace the placeholder before uploading.
- Dev and stable release workflows now patch `install.sh` with the correct release tag during build.
- Dev install URL points to release asset (`releases/download/dev/install.sh`) instead of raw source.
- Release workflows now build `skill-catalog-mcp` per-platform alongside `daedalus`. Install script downloads the platform-specific MCP binary.

## [0.11.0] - 2026-03-22

### Added
- **Copilot CLI support** — GitHub Copilot CLI can now be used as an alternative AI agent alongside Claude Code, selectable per-project via `--agent copilot` or `daedalus config <name> --set agent=copilot`.
- `core/agent.go` — `AgentProfile` struct and `LookupAgent()`, `ValidAgentNames()`, `ResolveAgentName()` functions for agent abstraction (pure logic, zero I/O).
- `--agent <name>` CLI flag with validation (accepts `claude` or `copilot`).
- `BuildAgentArgs()` — agent-aware argument builder that uses agent profiles to emit correct flags per agent.
- `Agent` field in `Config`, `AppConfig`, and per-project default flags (`applyDefaultFlags`).
- `AGENT` environment variable exported in `BuildTmuxCommand` and passed via `docker-compose.yml`.
- `copilot-base` and `copilot-dev` Dockerfile stages with Copilot CLI installed via the [gh.io installer](https://gh.io/copilot-install).
- Agent dispatch in `entrypoint.sh` — reads `$AGENT` env var to launch the correct binary (`claude` or `copilot`).
- Shell completions for `--agent` flag in bash, zsh, and fish with `claude copilot` value suggestions.

### Changed
- `BuildClaudeArgs()` is now a deprecated alias for `BuildAgentArgs()` — no breakage for existing callers.
- `cmd/daedalus/main.go`, `internal/tui/tui.go`, and `internal/web/web.go` now use `BuildAgentArgs()`.
- Help text and usage examples updated with `--agent` flag documentation.

## [0.10.0] - 2026-03-21

### Added
- **Skill Catalog** — a shared skill repository on the host filesystem, mounted into every container. Skills are Claude Code slash commands (`.md` files) that can be browsed, installed, created, and shared across projects.
- `skill-catalog-mcp` — an MCP server (using the official `github.com/modelcontextprotocol/go-sdk`) running inside containers, exposing 8 tools: `list_skills`, `read_skill`, `install_skill`, `uninstall_skill`, `create_skill`, `update_skill`, `remove_skill`, `list_installed`.
- `daedalus skills` CLI subcommand for host-side catalog management: `daedalus skills` (list), `daedalus skills add <file>`, `daedalus skills remove <name>`, `daedalus skills show <name>`.
- Starter skills (`commit.md`, `review.md`) seeded via `go:embed` on first run when the catalog directory does not exist.
- `SkillsDir()` method on `Config` for the shared catalog path (`<data-dir>/skills/`).
- Skills volume mount (`<data-dir>/skills:/opt/skills`) automatically added to every container via `BuildExtraArgs`.
- `internal/catalog` package with pure catalog operations and 21 unit tests.
- `skill-catalog-mcp` binary added to Dockerfile, `build.sh`, and `install.sh`.
- MCP server entry in `claude.json` for automatic discovery by Claude Code.
- `~/.claude/commands/` directory creation in `entrypoint.sh` for skill installation target.

### Changed
- Go module minimum version bumped to 1.25.0 (required by `github.com/modelcontextprotocol/go-sdk`).
- `build.sh` now builds both `daedalus` and `skill-catalog-mcp` binaries.

## [0.9.2] - 2026-03-21

### Fixed
- tmux sessions on macOS no longer become unreachable after opening a new terminal window. All tmux commands now use a stable socket path via `TMUX_TMPDIR=/tmp`.

### Added
- `ExecWithEnv` method on `Executor` interface for process replacement with extra environment variables.

## [0.9.1] - 2026-03-20

### Fixed
- Web UI and TUI now apply per-project default flags (display, dind) when starting containers. Previously only the CLI applied registry defaults.
- Display forwarding test no longer depends on host `DISPLAY` environment variable, fixing CI failures in headless environments.

### Changed
- Dev release workflow triggers on pushes to the `development` branch instead of `master`.

## [0.9.0] - 2026-03-20

### Added
- `--display` CLI flag to forward the host X11/Wayland display into Docker containers, enabling GUI application rendering on the host screen
- Per-project `display` default flag stored in `projects.json`, configurable via `daedalus config <name> --set display=true`
- Shell completions and man page documentation for `--display`
- `internal/platform/display.go` — pure `DisplayArgs()` function for resolving X11/Wayland Docker arguments
- X11 forwarding via `/tmp/.X11-unix` socket mount and `DISPLAY` environment variable
- Wayland forwarding via `$XDG_RUNTIME_DIR/$WAYLAND_DISPLAY` socket mount
- Interactive prompt during first project registration to enable display forwarding (default: no)

## [0.8.3] - 2026-03-20

### Added
- Unit tests for `MockExecutor` in `internal/executor/executor_test.go` covering call recording, result lookup, and query helpers

### Fixed
- Updated 13 stale `"0.8.1"` version strings to `"0.8.2"` in `cmd/generate-manpage/main_test.go` test fixtures

### Changed
- Moved `PrintBanner()` from `core/` to `cmd/daedalus/` to restore the zero-I/O invariant in the core package
- Refactored `run()` in `cmd/daedalus/main.go` — extracted `ensureImageBuilt()`, `buildImage()`, and `launchProject()` to reduce the function from ~197 lines to ~60 lines

## [0.8.2] - 2026-03-18

### Added
- Browser tab title reflects the active project name when attached to a terminal session, resets to "Daedalus — Web Dashboard" on return to the project list

## [0.8.1] - 2026-03-16

### Added
- Auto-detect WSL2 and bind web UI to `0.0.0.0` for Windows host accessibility
- Print WSL2 VM IP address at web UI startup for easy browser access

## [0.8.0] - 2026-03-15

### Added
- Standalone `--build` flag — run `daedalus --build` without a project name to rebuild Docker images for all registered projects. Supports `--target` to limit to a specific build target.
- Verbose `--debug --build` output — when both flags are set, prints resolved Dockerfile and docker-compose.yml paths, build target, image name, and all environment variables (sorted) before the build starts.
- File logging — runtime logs are written to a persistent log file for post-mortem debugging. Default location: `<data-dir>/daedalus.log`. Configurable via `log-file` in `config.json`. Logs include timestamps, levels (`INFO`/`DEBUG`/`ERROR`), and key events (startup, subcommands, builds, errors).
- `internal/logging` package — thread-safe file logger with `Init()`, `Close()`, `Info()`, `Debug()`, `Error()` functions.
- Auto-rebuild after install/upgrade — stores a SHA-256 checksum of build-relevant files (Dockerfile, entrypoint.sh, docker-compose.yml, settings.json, claude.json) after each build. On next project start, compares the current checksum to detect changes and triggers an automatic rebuild when runtime files have been updated.
- Curated release changelog — GitHub Releases now display the version-specific section from CHANGELOG.md instead of auto-generated commit notes. Extraction script at `scripts/extract-changelog.sh`.
- Install script test harness — `scripts/test-install.sh` validates install, upgrade, and uninstall flows using mocked downloads and temp directories (34 assertions across 7 test cases).
- `log-file` field in `config.json` and `AppConfig` struct for configurable log file path.
- `core/checksum.go` — pure `ComputeBuildChecksum()` and `BuildFiles()` functions (zero I/O).
- `internal/docker/checksum.go` — I/O functions for reading build files, storing/comparing checksums.

### Changed
- `--build` flag description in help text updated to reflect standalone rebuild capability.
- ARCHITECTURE.md updated with `logging` package in the dependency graph.
- Release workflow (`.github/workflows/release.yml`) now extracts changelog from CHANGELOG.md for the release body.

## [0.7.8] - 2026-03-10

### Fixed
- Dockerfile: fix Claude CLI symlink rewrite — use `readlink` + `sed` to resolve the actual target instead of an unresolved glob pattern.

## [0.7.7] - 2026-03-10

### Fixed
- Dockerfile: fix broken Claude CLI symlink after moving `/home/claude/.local` to `/opt/claude`. The symlink is now re-created to point to the correct `/opt/claude/share/claude/versions/*/claude` path.

## [0.7.6] - 2026-03-10

### Changed
- Renamed `.claude.json` to `claude.json` in the repo. The Dockerfile copies it as `.claude.json` into the image at build time, avoiding dotfile glob issues in releases and installs.

## [0.7.5] - 2026-03-10

### Fixed
- Release workflow glob `release-assets/*` did not match dotfiles — `.claude.json` was missing from GitHub Release assets.

## [0.7.4] - 2026-03-10

### Added
- README: zsh `source ~/.zshrc` note for macOS users after installation.
- README: "Creating a New Target" section with example and guidelines.
- ROADMAP: shell toggle and target switching backlog items.

### Fixed
- Install script and release workflow now include `.claude.json` in runtime files and release assets.

## [0.7.3] - 2026-03-08

### Fixed
- TUI delete confirmation prompt showed twice — once in the status area and once in the help area. Now only displays in the help area.

## [0.7.2] - 2026-03-08

### Added
- TUI `Del` key removes the selected project from the registry with inline Y/n confirmation prompt. Running projects cannot be removed. Help bar now shows `[del] remove`.

## [0.7.1] - 2026-03-08

### Changed
- TUI kill shortcut changed from `Del` key to `x`. Help bar now shows `[x] kill` instead of `[del]ete`.

## [0.7.0] - 2026-03-08

### Added
- TUI returns to the dashboard after tmux detach or session end, instead of exiting to the shell. Normal quit (`q`/`Ctrl-C`) still exits.
- `AttachWait()` method on `Session` — attaches to a tmux session via fork-wait (`Run`) instead of process replacement (`Exec`), allowing the caller to continue after detach.
- GitHub Pages landing page in `/docs`.

## [0.6.0] - 2026-03-08

### Added
- TUI create mode — press `n` to register a new project directly from the TUI with an interactive directory browser. Step 1: enter project name (validated, duplicate-checked). Step 2: browse filesystem with j/k navigation, Enter to descend, Backspace to go up, `s` to select directory, `c` to create a new subdirectory. Target defaults to `dev`. Esc cancels at any step.

## [0.5.7] - 2026-03-08

### Added
- TUI viewport scrolling — projects beyond the terminal height are now reachable via cursor keys. Scrollbar indicator (`█` thumb / `░` track) appears on the right when the list exceeds the viewport.
- Dark-themed scrollbar styling for the web UI project list, matching the Tokyo Night color palette (Webkit and Firefox).
- Version displayed in brackets after the title in both TUI and web UI (`Daedalus [0.5.7]`).

### Changed
- Version is now baked into the binary at compile time via `-ldflags` instead of reading a VERSION file at runtime.

### Fixed
- Release workflow was not injecting version into binaries via `-ldflags`, causing `unknown` to appear in titles.
- Web scrollbar not appearing — `#project-view` was missing flex layout, preventing `.project-list` from having a constrained height to overflow.
- Renaming a project via the web UI could corrupt the cache directory on WSL2/bind-mounted filesystems. Replaced `os.Rename` with copy+remove for directory renames.
- Cache directory copy failed on dangling symlinks (e.g. `.claude-config/debug/latest`). Symlinks are now recreated instead of followed.

## [0.5.2] - 2026-03-08

### Added
- Colored CLI output — errors in red, warnings in yellow, success in green, hints in cyan, section headers in bold. Respects `NO_COLOR` environment variable convention.
- `--no-color` flag to disable colored output.
- `daedalus config <name>` subcommand to view per-project configuration (directory, target, sessions, default flags).
- `daedalus config <name> --set key=value` and `--unset key` to modify per-project default flags.
- `UpdateDefaultFlags` registry method — single read-modify-write to merge set/unset changes.
- `daedalus completion <bash|zsh|fish>` subcommand to print shell completion scripts. Covers all subcommands, flags, and dynamic project name completion.
- Input validation for `--port` (must be 1-65535) and `--host` (must be non-empty) at parse time (#21).
- Actionable hint messages on 6 key errors: missing credentials, missing project directory, already running, image build failure, project not found, too many arguments.
- Configurable data directory via `DAEDALUS_DATA_DIR` environment variable. Allows storing registry and per-project caches on a different drive or following XDG conventions. Default remains `.cache` next to the binary (backward compatible).
- `RegistryPath()` method on `Config` to eliminate duplicated registry path construction.
- `install.sh` deployment script — builds the binary, copies runtime files to a configurable `--prefix` directory (default: `~/.local/share/daedalus`), and creates a PATH symlink. Validates Docker as a prerequisite.
- Application configuration file (`config.json`) — optional JSON config file next to the binary for persistent settings. Supports `data-dir`, `debug`, `no-tmux`, and `image-prefix`. Precedence: env vars > config file > defaults.
- `--uninstall` flag for `install.sh` — removes binary, runtime files, and symlink. Prompts before deleting project data in `.cache/`.
- Documentation for MCP server configuration and container restrictions.
- Documentation for sharing skills and instructions across projects.
- End-to-end integration test suite — 9 test functions covering full project lifecycle, config precedence, registry lifecycle, Docker command construction, Web API, headless mode detection, and shell completions.
- GitHub Actions CI workflow — runs `go vet` and `go test -race` on push/PR to master.
- GitHub Actions release workflow — cross-compiles binaries for Linux and macOS (amd64/arm64) on version tags, creates GitHub Release with all assets.
- Man page generator (`cmd/generate-manpage/`) — produces `daedalus.1` roff man page with all commands, flags, environment variables, configuration, examples, and exit codes.
- Pre-built `daedalus.1` man page.
- `NewWebServerForTest()` constructor and exported handler wrappers (`HandleListProjects`, `HandleStartProject`, `HandleStopProject`) for cross-package integration testing.
- Startup banner — `PrintBanner()` displays the Techdelight logo, version, and build timestamp when launching `daedalus web` or `daedalus tui`.
- Upgrade-aware installer — `install.sh` now detects an existing installation via the `version` field in `config.json`. On upgrade, it preserves user settings (data-dir, debug, no-tmux, image-prefix), replaces the binary and runtime files, and updates the version.
- `version` field in `config.json` and `AppConfig` struct.
- `daedalus rename <old-name> <new-name>` CLI subcommand to rename registered projects.
- `POST /api/projects/{name}/rename` web API endpoint with JSON body `{"newName": "..."}`.
- Rename button in the web dashboard for stopped projects (uses `prompt()` for new name).
- F2 key in TUI to rename the selected project via inline text input (Enter to confirm, Esc to cancel).
- `ValidateProjectName()` pure validation function — names must start with alphanumeric and contain only `[a-zA-Z0-9._-]`.
- `RenameProject()` registry method — atomic rename of registry key with best-effort cache directory rename.
- Shell completions for `rename` subcommand (bash, zsh, fish).
- Man page entry for `rename` command with synopsis and example.

### Changed
- `CacheDir()` now derives from `DataDir` instead of `ScriptDir`.
- **Rebrand**: Renamed project from `agentenv` to `Daedalus` across all source files, Go module path, binary name, shell completions, documentation, and build scripts.
- Copyright holder changed from "David Stibbe" to "Techdelight BV" in all source file headers.
- Apache-2.0 license added (`LICENSE` file).
- Documentation restructured: `README.md` is now end-user focused, `CONTRIBUTING.md` expanded with coding standards and Definition of Done, `ARCHITECTURE.md` created with module breakdown, component diagram, and data flow.
- `install.sh` now downloads pre-built binaries from the latest GitHub Release instead of downloading source and building via Docker. Docker is no longer required for installation (still required at runtime).
- TUI kill shortcut changed from `K` (shift+k) to the `Del` (Delete) key.

### Fixed
- `install.sh` `sed -i` command now portable across Linux and macOS (replaced with `sed` + temp file).
- TUI kill (`K`) and web UI stop did not stop containers — `executor.Run` attached stdout/stderr/stdin to the subprocess, conflicting with bubbletea's alt-screen terminal. Replaced with `executor.Output` which captures output without terminal interference (#27).
- Registry migration did not reject future schema versions — a registry file with a version newer than the binary could be silently accepted. Now returns an error with both the file version and the supported version.
- Docker compose command and environment exports no longer visible in the terminal when starting a container via TUI or Web UI. The tmux command now clears the screen before execution.
- `docker image inspect` output no longer leaks to the terminal when starting a container from the web interface. `ImageExists()` now uses `Output()` instead of `Run()`.

### Removed
- `--data-dir` CLI flag — data directory is now configured via `config.json` or the `DAEDALUS_DATA_DIR` environment variable.
- Host credential linking — `ClaudeConfigDir`, `CredSourcePath()`, and `CRED_PATH` env var removed from config, command builder, and compose environment. Users now run `claude /login` inside the container; credentials persist in the per-project cache directory.
- Credential prerequisite check from `install.sh` — Claude credentials are no longer required on the host.
- `/opt/claude/credentials/` directory from Dockerfile and credential symlink logic from `entrypoint.sh`.
- `.claude.json` from `install.sh` runtime files — it is baked into the Docker image and not included in release assets.
- `start.sh` from release workflow assets — it is a development helper not needed by end users.

## [0.5.0] - 2026-03-02

### Added
- Session history tracking per project — `StartSession`/`EndSession` record session IDs, timestamps, durations, and optional resume IDs. Capped at 50 entries per project. Sessions column in `list`, TUI, and web API.
- Per-project default flags — flags like `--dind`, `--debug`, `--no-tmux` are captured at first registration and automatically applied on subsequent runs. CLI flags always override. New `SetDefaultFlags` registry method.
- `daedalus remove <name> [name...]` subcommand to explicitly delete named projects from the registry with interactive confirmation (or `--force` for headless mode).
- Batch `RemoveProjects` registry method — single read-modify-write cycle for removing multiple projects, replacing N+1 pattern in `pruneProjects` (#24).
- Registry schema versioning and migration framework — `CurrentRegistryVersion` constant, `migrate()` with per-version upgrade functions, auto-migration on read with immediate persistence.
- `RemoveProject` now cleans up the per-project cache directory after registry deletion (#23).

### Changed
- `ComposeRun` now delegates to `ComposeRunCommand` internally, eliminating duplicated arg construction (#20).
- `pruneProjects` uses batch `RemoveProjects` for atomic removal instead of per-item loop.
- New registries are created at schema version 2 (up from 1).

## [0.4.1] - 2026-03-02

### Fixed
- **Critical**: `extraArgs` (e.g. `-v` for DinD socket mount) placed after service name in `ComposeRun`/`ComposeRunCommand` — flags were interpreted as container command instead of `docker compose run` flags (#15).
- `claude` user not in `docker` group — socket permission denied inside container (#16).
- `docker.io` installed in `utils` stage, bloating `godot` image — moved to `dev` stage only (#17).
- Headless `prune` auto-deleted without confirmation — now requires `--force` flag (#19, #25).

### Added
- Runtime stderr warning when `--dind` is used, documenting that the host Docker socket is mounted (#18).
- `--force` flag for non-interactive `prune` deletion.
- Unit tests for `pruneProjects` (no-stale, with-stale, headless-without-force) (#22).

## [0.4.0] - 2026-03-02

### Added
- `--dind` flag to mount the host Docker socket into the container for Docker-in-Docker workflows. Docker CLI installed in the `utils` stage. Security warning: grants host Docker access.
- `daedalus prune` subcommand to remove registry entries whose project directories no longer exist on disk. Interactive confirmation in TTY mode, auto-remove in headless mode.
- `--debug` flag to opt-in to Claude Code debug mode (previously hardcoded as always-on).
- `RemoveProject` method on the registry for programmatic project deletion.
- Container resource limits: `mem_limit: 4g`, `cpus: 2.0`, `pids_limit: 512`.

### Fixed
- `--debug` flag no longer hardcoded — now opt-in via CLI flag (#7).
- Volume paths in `docker-compose.yml` are now quoted to handle paths with spaces (#10).
- Dead `ln -sfr` symlink removed from Dockerfile — binary already on PATH via `ENV PATH` (#13).
- Redundant `mkdir -p` in `entrypoint.sh` consolidated to a single unconditional call (#14).

### Changed
- Dockerfile install script now has a supply-chain warning comment documenting the unverified curl-pipe-sh pattern (#12).

## [0.3.0] - 2026-03-02

### Added
- Web UI dashboard (`daedalus web`) — browser-based project management with REST API and embedded xterm.js terminal connected to tmux sessions via WebSocket + PTY relay. Static assets embedded in binary via `go:embed`. Binds to `localhost:3000` by default with `--port` and `--host` flags.
- Interactive TUI dashboard (`daedalus tui`) for managing projects — start, attach, kill, and monitor containers with keyboard-driven navigation (bubbletea + lipgloss)
- TUI auto-attaches to the tmux session after starting a project, matching the non-TUI flow
- `entrypoint.sh` wrapper that seeds config defaults on first run and symlinks credentials
- Project registry (`.cache/projects.json`) tracking directory, target, and timestamps per project
- Auto-migration of existing `.cache/*/` directories into registry on first run
- Project existence check — interactive prompt for unregistered projects, auto-register in headless mode
- Container naming (`claude-run-<project-name>`) for easy identification in `docker ps`
- Duplicate container detection — prevents launching a project that's already running
- `jq` dependency check at script startup with install hint
- `ROADMAP.md` with language evaluation (bash vs Go vs Zig vs C++)
- Single multi-stage `Dockerfile` with four targets: `base`, `utils`, `dev`, `godot`
- Godot 4.x stage for headless game CI, exports, and tests
- `--target` flag in `run.sh` to select build stage (default: `dev`)
- `--resume` flag in `run.sh` to resume a previous Claude Code session
- Home directory persistence via `.cache/<project-name>/` bind-mounted as `/home/claude`
- `update-login.sh` script to refresh credentials for running agents
- MCP server support via Claude Code settings
- `--debug` flag passed to Claude Code by default

### Fixed
- TUI tmux attach no longer garbles the terminal. Previously, `syscall.Exec` was called from within bubbletea, skipping alt-screen and raw-mode cleanup. Now the TUI quits cleanly first, then attaches to tmux after the terminal is restored.
- Session resume (`--resume`) now works across container runs. `CLAUDE_CONFIG_DIR` moved from `/opt/claude/config` (ephemeral) to `/home/claude/.claude-config` (persistent volume), so session transcripts survive container removal.

### Changed
- `run.sh` positional arguments (`project-name`, `project-dir`) are now optional. Zero args defaults to current directory name and path; one arg defaults project-dir to current directory.
- Makefile Go Docker image bumped from `golang:1.21-bookworm` to `golang:1.24-bookworm` to match `go.mod`
- Merged `Dockerfile.base` and `Dockerfile.dev` into a single multi-stage `Dockerfile`
- Credentials now bind-mounted read-only at runtime instead of only baked into the image
- `docker-compose.yml` uses `TARGET` env var for image tag selection
- Container auto-removed on exit since home directory is now persisted
- Container user UID matched to caller's UID via `CLAUDE_UID` build arg
- Claude CLI installed to `/opt/claude` with defaults at `/opt/claude/defaults/`; runtime config at `/home/claude/.claude-config` (persistent volume)
- Credentials mount moved from `/opt/claude/config/.credentials.json` to `/opt/claude/credentials/.credentials.json`
- `jq` added to base Dockerfile stage

### Removed
- Separate `Dockerfile.base` and `Dockerfile.dev` (replaced by single multi-stage `Dockerfile`)
- Named Docker volume for Claude config (replaced by host bind mount)

## [0.1.0] - 2026-02-15

### Added
- Dockerfile with Node 22, Python 3, git, build tools, and Claude Code CLI
- `entrypoint.sh` launching Claude Code with `--dangerously-skip-permissions`
- `docker-compose.yml` with security hardening (read-only FS, dropped capabilities, no-new-privileges)
- `.claude/settings.json` pre-approving all Claude Code tools
- `.env.example` for API key configuration
- `CLAUDE.md` with project guidance for Claude Code
