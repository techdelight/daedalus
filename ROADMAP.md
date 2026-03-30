# Roadmap

## Backlog

| # | Item |
|---|------|
| 30 | Skill install path — install skills to `.claude/skills/{skill-name}/SKILL.md` (directory per skill) instead of flat `.claude/skills/{skill-name}.md` |
| 31 | Rename skill store — rename "skill catalog" / "skill" terminology to avoid confusion with Claude Code's built-in skill repository. Candidate: "focus" (focus catalog, focus files). Open to alternatives |
| ~~1~~ | ~~Agent mode (`--agent`) — start Claude Code as a specific agent by passing a named agent configuration, enabling purpose-built personas and tool sets per project~~ |
| 2 | Authentication for Web UI — add token-based login to protect the dashboard when exposed on a network |
| 3 | Session cookie with configurable expiry |
| 4 | `--auth` / `--no-auth` flag for `daedalus web` (default: auth enabled) |
| 5 | Generate and display access token on first `daedalus web` launch |
| 6 | Shell toggle — switch between Claude Code and a regular project shell inside the container |
| 7 | Switch target for existing project — change build target from TUI (e.g. `F3`) and CLI (`daedalus config <name> --set target=<stage>`) without re-registering |
| 8 | Bundle release assets — package runtime files into a single tarball on the GitHub Release page instead of individual files |
| 9 | Side-by-side versions — install a new version alongside the existing one, allowing rollback or A/B comparison before switching |
| ~~10~~ | ~~Shared skills/MCP repository — a central directory of skills and MCP server configs that can be mounted or linked into any project, avoiding per-project duplication~~ |
| 11 | Homebrew installation (`brew install daedalus`) — add Homebrew tap, formula generator, and CI automation. See [docs/homebrew-plan.md](docs/homebrew-plan.md) for full plan |
| 12 | WSL2 Web UI access — enable `daedalus web` to be reachable from the Windows host when running inside WSL2 (bind to `0.0.0.0` or WSL2 IP, port-forwarding guidance, auto-detect WSL2 environment) |
| ~~13~~ | ~~Project management view in Web UI — per-project dashboard showing vision, version, time spent, and percentage complete~~ |
| ~~14~~ | ~~Project management MCP server — provide an MCP server inside each project container so Claude Code can report progress (vision, version, percentage complete, time spent) back to Daedalus~~ |
| ~~15~~ | ~~Skill catalog — a browsable catalog of available skills that projects can select from and mount into their containers~~ |
| 16 | ACP integration — use the Agent Client Protocol to communicate with the Claude Code CLI, enabling Daedalus to observe agent state (thinking, tool use, idle, error) in real time |
| ~~17~~ | ~~Roadmap in Web UI — display the project roadmap as a collapsible side panel on the right of the dashboard~~ |
| ~~18~~ | ~~Daedalus as MCP client — have Daedalus consume the Project Management MCP server to read roadmaps, construct and manage sprints, and trigger the agent to execute sprint items~~ |
| 19 | GitHub repo projects — start a project from a GitHub repo URL, cloning into a default project root directory |
| ~~20~~ | ~~Browser tab title — set the Web UI tab title to include the name of the active project~~ |
| 21 | Shared Maven `.m2` repository — mount a host-side `.m2/repository` into containers so dependencies are shared across projects. Investigate overlay/merge strategy: a stable global repo (read-only base) combined with a per-container local repo for builds/downloads/installs, so containers benefit from cached artifacts without polluting the shared cache |
| 22 | Favicon — add a Daedalus favicon to the Web UI so the browser tab shows a recognizable icon |
| ~~23~~ | ~~Display sharing (`--display`) — forward the host X11/Wayland display into Docker containers so GUI applications can render on the host screen. Support WSL2 (via `DISPLAY` + `/tmp/.X11-unix` or Wayland socket) and native Linux. Stored as a per-project `display` flag in `projects.json`, off by default. Prompted during `daedalus <name> <dir>` first registration and configurable via `daedalus config <name> --set display=true`~~ |
| ~~24~~ | ~~Copilot CLI support — add GitHub Copilot CLI as an alternative coding agent alongside Claude Code. Allow selecting the agent per project via `--agent copilot` or `daedalus config <name> --set agent=copilot`. Install Copilot CLI in the container, configure entrypoint to launch the selected agent, and adapt session management for Copilot's CLI interface~~ |
| 25 | Webdev container — move Node.js out of the regular `dev` stage into a dedicated `webdev` build target for web/frontend projects. Keeps the default dev image lean |
| ~~26~~ | ~~Mobile-friendly web UI — scrollable terminal output (replace tmux Ctrl+B PgUp/PgDown with native scroll), multi-line input (Enter inserts newline, separate submit button/shortcut), simplified project overview (name, online status, attach/kill/start action buttons)~~ |
| 27 | Decouple tooling from agent runner images — keep base agent containers minimal and let the agent install additional tools at runtime. Provide container snapshotting so customized environments persist across restarts. Key challenge: when the base image is upgraded, how do we replay tool installations? Options: (a) maintain a declarative tool registry (tool name + version + install method) that a provisioner re-applies on new base images — portable but subjective per tool; (b) record raw install commands as a replayable script — simple but fragile across base image changes; (c) hybrid approach with a registry of well-known tools (apt, pip, npm) plus an escape hatch for arbitrary commands. Needs design spike to evaluate trade-offs |
| ~~28~~ | ~~Active project filter — add a toggle/filter to the Web UI and TUI that shows only running projects. Useful when the project list grows large and the user wants to focus on what is currently active~~ |
| 29 | Mobile WebSocket stability — investigate and fix regular disconnects on mobile web clients (possible causes: browser background tab throttling, network switches between Wi-Fi and cellular, WebSocket ping/pong timeout tuning, reconnect logic) |

## Current Sprint

### Sprint 30: Programme-Level Cascade Orchestration (v0.25.0)

Goal: when an upstream project completes a sprint item, the Foreman propagates work to downstream projects via the dependency graph. Configurable cascade strategies per dependency edge.

| # | Item | Status |
|---|------|--------|
| 1 | `internal/foreman/cascade.go` — cascade logic via `DependencyGraph.Downstreams()`, cascade strategies (`auto`, `notify`, `manual`) | Done |
| 2 | `core/programme.go` — add `Strategy` field to `DependencyEdge` | Done |
| 3 | `internal/web/` — cascade event log in Foreman status API response | Done |
| 4 | `daedalus programmes cascade <name> --dry-run` — preview cascade actions | Done |
| 5 | Documentation — ARCHITECTURE, CHANGELOG, VERSION, README | Done |

### Sprint 29: The Foreman Agent — Core Loop (v0.24.0)

Goal: Daedalus itself becomes an AI-driven project manager. The Foreman reads roadmaps, maintains a plan, monitors worker agents, and reports through the Web UI. Runs as a goroutine inside `daedalus web`.

| # | Item | Status |
|---|------|--------|
| 1 | `core/foreman.go` — `ForemanConfig`, `ForemanState`, `ForemanPlan` pure types | Done |
| 2 | `internal/foreman/foreman.go` — Foreman main loop: read programme, read roadmaps, build plan, monitor agents, report status | Done |
| 3 | `internal/foreman/planner.go` — sprint planning logic (reads roadmaps, proposes next actions) | Done |
| 4 | `internal/foreman/monitor.go` — monitoring loop: poll MCP client and agent observer for worker state | Done |
| 5 | `cmd/daedalus/main.go` — `daedalus foreman start/stop/status` subcommands | Done |
| 6 | `internal/web/` — `/api/foreman/status` endpoint, Foreman console panel in Web UI | Done |
| 7 | Documentation — ARCHITECTURE, CHANGELOG, VERSION, README | Done |

### Sprint 28: Agent Observability (v0.23.0)

Goal: define the agent observation interface and implement a container-status-based observer. Adds real-time agent state indicators to the Web UI. Partial implementation of backlog item 16 — full ACP integration deferred until the protocol is publicly stable.

| # | Item | Status |
|---|------|--------|
| 1 | `internal/agentstate/` — `AgentState` enum, `Observer` interface, `ContainerObserver` implementation | Done |
| 2 | `internal/web/` — `GET /api/projects/{name}/state` endpoint returning agent state | Done |
| 3 | Web UI — agent state indicator (colored dot) on project cards in the list view | Done |
| 4 | `internal/foreman/` — `AgentObserver` interface matching `agentstate.Observer` | Done |
| 5 | Documentation — ARCHITECTURE, CHANGELOG, VERSION | Done |

### Sprint 27: Daedalus as MCP Client (v0.22.0)

Goal: Daedalus consumes the project-mgmt-mcp server from the host side via `docker exec` + stdio transport. Enables programmatic reading of project state and aggregated programme views. Implements backlog item 18.

| # | Item | Status |
|---|------|--------|
| 1 | `internal/mcpclient/` — MCP client package using go-sdk, transport via `docker exec` + stdio | Done |
| 2 | High-level methods: `ReadProgress()`, `ReadRoadmap()`, `GetCurrentSprint()` | Done |
| 3 | `daedalus programmes show <name>` — aggregate progress from all member projects via MCP client | Done |
| 4 | Documentation — ARCHITECTURE, CHANGELOG, VERSION, README | Done |

### Sprint 26: Roadmap Parsing and Sprint Decomposition (v0.21.0)

Goal: Daedalus can read a ROADMAP.md file and parse it into structured sprint data. Adds a roadmap API endpoint and MCP tools for agents to query sprint status. Implements backlog item 17.

| # | Item | Status |
|---|------|--------|
| 1 | `core/sprint.go` — `Sprint`, `SprintItem`, `SprintStatus` types (pure, zero I/O) | Done |
| 2 | `core/roadmap.go` — `ParseRoadmap(markdown) ([]Sprint, error)` parser for Daedalus-native ROADMAP.md format | Done |
| 3 | `internal/web/` — `GET /api/projects/{name}/roadmap` endpoint, collapsible side panel in Web UI | Done |
| 4 | `cmd/project-mgmt-mcp/` — `get_roadmap` and `get_current_sprint` tools | Done |
| 5 | Documentation — ARCHITECTURE, CHANGELOG, VERSION, README | Done |

### Sprint 25: Programme Data Model and CLI (v0.20.0)

Goal: declare multi-project programmes with dependency relationships. Users can model project topology even without the Foreman. Pure data model sprint — no orchestration yet.

| # | Item | Status |
|---|------|--------|
| 1 | `core/programme.go` — `Programme`, `DependencyEdge`, `DependencyGraph` types; `TopologicalSort()`, `DetectCycles()`, `Downstreams()`, `Upstreams()` pure functions with tests | Done |
| 2 | `internal/programme/` — `Store` with `List`, `Read`, `Create`, `Update`, `Remove`, persisted to `programmes.json` with tests | Done |
| 3 | `core/config.go` — add `Programme` field and `ProgrammesArgs` to Config; `ProgrammesDir()` method | Done |
| 4 | `cmd/daedalus/main.go` — `daedalus programmes` subcommand: list, show, create, add-project, add-dep, remove | Done |
| 5 | Shell completions for `programmes` subcommand in bash, zsh, fish | Done |
| 6 | Documentation — update ARCHITECTURE.md, CHANGELOG.md, VERSION, README.md | Done |

### Sprint 24: Project Management MCP Server (v0.19.0)

Goal: add a second MCP server (`project-mgmt-mcp`) inside each container so Claude Code can report progress, set vision/version, and read sprint items. Daedalus reads progress via bind-mounted `.daedalus/progress.json`. Implements backlog item 14.

| # | Item | Status |
|---|------|--------|
| 1 | `internal/progress/` package — pure progress file read/write operations with tests | Done |
| 2 | `cmd/project-mgmt-mcp/main.go` — new MCP server binary with `report_progress`, `set_vision`, `set_version`, `get_progress` tools | Done |
| 3 | `core/command.go` — mount `.daedalus/` directory into containers via `BuildExtraArgs` | Done |
| 4 | `claude.json` — register `project-mgmt-mcp` MCP server entry | Done |
| 5 | `Dockerfile` — copy `project-mgmt-mcp` binary into image, `entrypoint.sh` — ensure `.daedalus/` directory exists | Done |
| 6 | `build.sh` — build `project-mgmt-mcp` binary alongside existing binaries | Done |
| 7 | `internal/web/` — poll `.daedalus/progress.json` from host and feed into dashboard endpoint | Done |
| 8 | Documentation — update ARCHITECTURE.md, CHANGELOG.md, VERSION, README.md | Done |

### Sprint 23: Project Management View in Web UI (v0.18.0)

Goal: per-project dashboard showing vision, version, time spent, and progress percentage — the foundation for the Foreman agent's reporting layer. Implements backlog item 13.

| # | Item | Status |
|---|------|--------|
| 1 | `core/project.go` — add `ProgressPct`, `Vision`, `ProjectVersion` fields to `ProjectEntry` with tests | Done |
| 2 | `internal/registry/` — v2-to-v3 migration (new fields default to zero values) with migration test | Done |
| 3 | `internal/registry/` — `UpdateProjectProgress(name, pct, vision, version)` method with tests | Done |
| 4 | `internal/web/` — `GET /api/projects/{name}/dashboard` endpoint returning progress data with tests | Done |
| 5 | `internal/web/static/` — project detail panel (click project row to see vision, version, total session time, progress bar) | Done |
| 6 | Documentation — update ARCHITECTURE.md, CHANGELOG.md, VERSION, README.md | Done |

### Sprint 22: Runner/Persona Polish & Skill Fix (v0.17.0)

Goal: clean up the runner/persona split — add `daedalus runners` subcommand, separate `personas list` from runners, store persona details in companion `.md` files, fix skill installation path, and harden validation and test coverage.

| # | Item | Status |
|---|------|--------|
| 1 | `daedalus runners` subcommand — list and show built-in runner profiles with shell completions | Done |
| 2 | `personas list` shows only user-defined personas, `personas show` rejects built-in names | Done |
| 3 | Persona `.md` companion file — store CLAUDE.md content alongside `.json` config | Done |
| 4 | Fix `resolvePersonaOverlay` — use `cfg.Persona`, set `cfg.Runner` from `BaseRunner` | Done |
| 5 | `--runner` strict validation (builtins only), `--persona` validation (rejects builtins, checks store) | Done |
| 6 | Skill install target: `~/.claude/commands/` → `/workspace/.claude/skills/` | Done |
| 7 | Dev release workflow fix — replace `softprops/action-gh-release` with `gh release create` | Done |

### Sprint 21: Personas & Runner/Persona Split (v0.16.0)

Goal: allow users to define named persona configurations that layer custom system prompts and tool-permission overrides on top of a built-in runner, selectable via `--persona <name>`. Split the overloaded "agent" concept into **runner** (claude/copilot binary) and **persona** (user-defined overlay).

| # | Item | Status |
|---|------|--------|
| 1 | `core/persona.go` — `PersonaConfig` type, `PersonasDir()`, `ValidatePersonaName()` with tests | Done |
| 2 | `internal/personas` package — Store with List/Read/Create/Update/Remove, unit tests | Done |
| 3 | `core/runner.go` — `LookupRunner` resolves personas to base runner, `ValidRunnerNames` for builtins, update all callers | Done |
| 4 | `core/command.go` — `BuildExtraArgs` injects custom CLAUDE.md and settings mounts for persona overlays | Done |
| 5 | `internal/config` — `--runner` and `--persona` flags with independent validation, legacy `--agent` alias | Done |
| 6 | `daedalus personas` CLI subcommand — list, show, create, remove with help text and shell completions | Done |
| 7 | Rename across codebase — `AGENT` env → `RUNNER`, docker-compose, entrypoint, Dockerfile, all docs | Done |

### Sprint 20: Active Project Filter (v0.15.0)

Goal: add a toggle/filter to the Web UI and TUI that shows only running projects, helping users focus when the project list grows large.

| # | Item | Status |
|---|------|--------|
| 1 | Web UI — filter toggle button in the project list header, filters table to running projects only | Done |
| 2 | Web UI — persist filter state in `localStorage` so it survives page reloads | Done |
| 3 | TUI — keybinding to toggle active-only filter, update project list rendering | Done |

### Sprint 19: Mobile Select Mode (v0.14.0)

Goal: enable native text selection on mobile terminals by overlaying the xterm.js buffer as plain selectable HTML.

| # | Item | Status |
|---|------|--------|
| 1 | Replace Copy button with Select toggle — overlay terminal buffer as selectable `<pre>` text, Done button to dismiss | Done |
| 2 | Force `user-select` and `touch-callout` for real mobile browser compatibility | Done |

### Sprint 17: Mobile-Friendly Web UI (v0.13.0)

Goal: make the web dashboard usable on phones and tablets — scrollable terminal, mobile input area, card-based project list.

| # | Item | Status |
|---|------|--------|
| 1 | Scrollable terminal — increase xterm.js scrollback to 10 000 lines | Done |
| 2 | Multi-line mobile input — textarea + Send button below terminal, Ctrl+Enter submits, xterm.js stdin disabled on mobile | Done |
| 3 | Card-based project list on mobile — hide Target/Last Used columns, flex card layout, larger touch targets | Done |
| 4 | Playwright test suite for the web frontend | |

### Sprint 16: Copilot CLI Support (v0.11.0)

Goal: agent abstraction so projects can use either Claude Code or Copilot CLI, selectable via `--agent copilot` or per-project default.

| # | Item | Status |
|---|------|--------|
| 1 | `core/agent.go` — `AgentProfile` struct, `LookupAgent()`, `ValidAgentNames()`, `ResolveAgentName()` with tests | Done |
| 2 | `Agent` field in `Config`, `AppConfig`, and `applyDefaultFlags` with tests | Done |
| 3 | `BuildAgentArgs()` — agent-aware argument builder, `BuildClaudeArgs()` kept as deprecated alias, `AGENT` in tmux exports, with tests | Done |
| 4 | `--agent` flag parsing with validation in `internal/config` with tests | Done |
| 5 | Wire up in `cmd/daedalus/main.go`, `internal/tui/tui.go`, `internal/web/web.go` — use `BuildAgentArgs`, pass `AGENT` env, update help text and `collectDefaultFlags` | Done |
| 6 | Shell completions for `--agent` in bash, zsh, and fish | Done |
| 7 | `docker-compose.yml` — `AGENT` environment variable | Done |
| 8 | `entrypoint.sh` — agent-aware dispatch (claude/copilot) | Done |
| 9 | `Dockerfile` — `copilot-base` and `copilot-dev` stages with Copilot CLI via gh.io installer | Done |

### Sprint 15: Skill Catalog (v0.10.0)

Goal: shared skill catalog with MCP server for browsing, installing, and publishing skills across projects.

| # | Item | Status |
|---|------|--------|
| 1 | `internal/catalog` package — pure catalog operations (list, read, install, uninstall, create, update, remove) with 21 unit tests | Done |
| 2 | `skill-catalog-mcp` MCP server — 8 tools over stdio using official `github.com/modelcontextprotocol/go-sdk` | Done |
| 3 | Docker integration — skills volume mount in `BuildExtraArgs`, MCP server entry in `claude.json`, binary in Dockerfile | Done |
| 4 | `daedalus skills` CLI subcommand — list, add, remove, show skills from the host | Done |
| 5 | Starter skills — `commit.md` and `review.md` seeded via `go:embed` on first run | Done |
| 6 | Build & install — `build.sh` builds both binaries, `install.sh` includes `skill-catalog-mcp` in runtime files | Done |

### Sprint 14: Display Sharing (v0.9.0)

Delivered 2026-03-21. GUI application rendering from Docker containers on the host screen via X11/Wayland forwarding.

| # | Item | Status |
|---|------|--------|
| 1 | `--display` flag plumbing — Config field, CLI parsing, per-project defaults, help text, shell completions, man page | Done |
| 2 | Display forwarding logic — `DisplayArgs()` in `internal/platform/display.go` for X11 + Wayland, wire into `launchProject()` | Done |
| 3 | First-run prompt — ask during interactive project registration whether to enable display forwarding (default: no) | Done |

### Sprint 13: Platform & Accessibility (v0.8.3)

Delivered 2026-03-20. WSL2 web access, dev releases, browser tab title, code quality improvements.

| # | Item | Status |
|---|------|--------|
| 1 | WSL2 Web UI access — auto-detect WSL2, bind to `0.0.0.0`, print VM IP for Windows browser access | Done |
| 2 | Dev release workflow — rolling `dev` pre-release on push to master with `VERSION-dev+SHA` binaries | Done |
| 3 | Browser tab title — set the Web UI tab title to include the name of the active project | Done |
| 4 | Core package purity — move `PrintBanner()` from `core/banner.go` to `cmd/daedalus/`, keeping `ReadVersion()` in core. Restores the zero-I/O invariant for the `core/` package | Done |
| 5 | Executor test coverage — add `internal/executor/executor_test.go` with tests for `MockExecutor` (call recording, result lookup, `HasCall`/`FindCall`/`FindCalls` queries) | Done |
| 6 | Fix stale test fixture — update 13 hardcoded `"0.8.1"` version strings to `"0.8.2"` in `cmd/generate-manpage/main_test.go` | Done |
| 7 | Refactor `run()` — extract `ensureImageBuilt()`, `launchProject()`, and `resolveProject()` from the 197-line `run()` function in `cmd/daedalus/main.go` to bring it under ~60 lines | Done |

### Sprint 12: Build, Debug & Logging Improvements (v0.8.0)

Goal: improve the build workflow, add diagnostic tooling, and set up release documentation.

| # | Item | Status |
|---|------|--------|
| 1 | Standalone `--build` — allow `daedalus --build` without requiring a project name or path, rebuilding the image for the current directory or all registered projects | Done |
| 2 | Verbose `--debug --build` output — when `--debug` is combined with `--build`, log all environment variables and the resolved paths for Dockerfile and docker-compose.yml | Done |
| 3 | File logging — write runtime logs to a persistent log file (e.g. `~/.local/share/daedalus/daedalus.log` or configurable path) for post-mortem debugging | Done |
| 4 | Release changelog — show a curated changelog / new features summary on the GitHub Release page | Done |
| 5 | Auto-rebuild after install/upgrade — detect when runtime files (Dockerfile, entrypoint, etc.) have changed and rebuild the Docker image on next project start | Done |
| 6 | Install script test harness — run the installer in a chroot or lightweight container to validate install/upgrade/uninstall flows without affecting the host | Done |

---

## Sprint History

### Sprint 18: Fix macOS Installation (v0.12.1)

Delivered 2026-03-24. Portable macOS install support for bash 3.2.

| # | Item | Status |
|---|------|--------|
| 1 | Fix `sed -i` in `install.sh` — use cross-platform `sed_inplace` wrapper for BSD/GNU compatibility | Done |
| 2 | Fix `sed -i` in `scripts/test-install.sh` — same `sed_inplace` wrapper for all 9 `sed -i` calls | Done |
| 3 | Add macOS (`macos-latest`) job to CI workflow — run install tests on both Ubuntu and macOS | Done |
| 4 | Fix symlink resolution in `ScriptDir` — `os.Executable()` returns the symlink path on macOS, so `filepath.EvalSymlinks` is needed to find the real binary directory containing Dockerfile and runtime files | Done |
| 5 | Fix empty array expansion in `install.sh` — `"${FORWARD_ARGS[@]}"` fails with `set -u` on macOS bash 3.2 when no flags are passed; use `${FORWARD_ARGS[@]+"${FORWARD_ARGS[@]}"}` | Done |

### Sprint 11: UX & Installer Polish (v1.2.0)

Delivered 2026-03-08. Docker inspect suppression, TUI keybinding change, upgrade-aware installer.

| # | Item | Status |
|---|------|--------|
| 1 | Suppress `docker inspect` output when starting a container from the web interface | Done |
| 2 | Change TUI kill shortcut from `K` to the `Del` key | Done |
| 3 | Upgrade-aware installer — store version in `config.json`, detect existing install, replace binary and migrate config fields as needed | Done |

### Sprint 10: Container Polish (v1.1.0)

Delivered 2026-03-08. Suppress docker command echo on container startup.

| # | Item | Status |
|---|------|--------|
| 1 | Suppress docker compose command and env exports from terminal on container startup | Done |

### Sprint 9: 1.0 Preparation (v1.0.0)

Delivered 2026-03-07. Stability audit, integration tests, CI/CD, man page, final docs.

| # | Item | Status |
|---|------|--------|
| 1 | Stability audit — review and freeze public API surface (CLI, config, registry, env vars) | Done |
| 2 | End-to-end integration test suite — cross-package workflow tests | Done |
| 3 | Binary releases via GitHub Actions (CI + release workflows, Linux/macOS amd64/arm64) | Done |
| 4 | Man page generation — `daedalus(1)` roff man page from CLI help | Done |
| 5 | Final documentation pass — README, CONTRIBUTING, ARCHITECTURE, CHANGELOG, VERSION bump to 1.0.0 | Done |

### Sprint 8: Structure & Distribution (v0.8.0)

Delivered 2026-03-06. Code restructuring, installation improvements, and documentation.

| # | Item | Status |
|---|------|--------|
| 1 | Configurable `.cache` directory location | Done |
| 2 | Code structure cleanup — move `.go` files out of the root into packages | Done |
| 3 | Usage instructions in README | Done |
| 4 | Remove credential linking into the project container | Done |
| 5 | Improve installation script (`--uninstall`, `data-dir` docs, macOS support) | Done |
| 6 | Documentation for MCP servers (configuration, restrictions) | Done |
| 7 | Documentation for sharing skills across projects | Done |

### Sprint 7: Rebrand & Open Source (v0.7.0)

Delivered 2026-03-05. Rename to Daedalus, add license, restructure documentation.

| # | Item | Status |
|---|------|--------|
| 1 | Rename `agentenv` → `Daedalus` across all source, build, and docs | Done |
| 2 | Update copyright to Techdelight BV | Done |
| 3 | Add Apache-2.0 license | Done |
| 4 | Create `ARCHITECTURE.md` | Done |
| 5 | Restructure all documentation per project standards | Done |
| 6 | Application configuration file (`config.json`) | Done |
| 7 | Deployment/installation script | Done |

### Sprint 6: Developer Experience (v0.6.0)

Delivered 2026-03-02. CLI polish: colored output, input validation, error hints, config subcommand, shell completions.

| # | Item | Issue | Status |
|---|------|-------|--------|
| 1 | Colored CLI output + `--no-color` flag | — | Done |
| 2 | Validate `--port` and `--host` values | #21 | Done |
| 3 | Improved error messages with suggested fixes | — | Done |
| 4 | `daedalus config` subcommand | — | Done |
| 5 | Shell completions (bash, zsh, fish) | — | Done |

### Sprint 5: Registry Enhancements (v0.5.0)

Delivered 2026-03-02. Registry schema versioning, session tracking, default flags, remove subcommand.

| # | Item | Issue | Status |
|---|------|-------|--------|
| 1 | DRY refactor: `ComposeRun` calls `ComposeRunCommand` | #20 | Done |
| 2 | Registry schema versioning and migration framework | — | Done |
| 3 | `RemoveProject` cleans up cache directory | #23 | Done |
| 4 | Batch `RemoveProjects` method | #24 | Done |
| 5 | `daedalus remove <name>` subcommand | — | Done |
| 6 | Per-project default flags | — | Done |
| 7 | Session history tracking | — | Done |

### Sprint 4 Hotfix: DinD & Prune Fixes (v0.4.1)

Delivered 2026-03-02. Fixed critical DinD bug and addressed major review issues from v0.4.0.

| # | Item | Issue | Status |
|---|------|-------|--------|
| 1 | Fix `extraArgs` placement before service name in `ComposeRun`/`ComposeRunCommand` | #15 | Done |
| 2 | Add `claude` user to `docker` group in Dockerfile | #16 | Done |
| 3 | Move `docker.io` install from `utils` to `dev` stage | #17 | Done |
| 4 | Print runtime warning to stderr when `--dind` is used | #18 | Done |
| 5 | Require `--force` flag for headless `prune` (default to dry-run) | #19 | Done |
| 6 | Add unit tests for `pruneProjects` function | #22 | Done |
| 7 | Guard against `-p` + `prune` skipping confirmation | #25 | Done |

### Sprint 4: Hardening & Docker-in-Docker (v0.4.0)

Delivered 2026-03-02. Resolved all 6 remaining code review issues, added DinD and prune.

| # | Item | Status |
|---|------|--------|
| 1 | Fix hardcoded `--debug` flag — make opt-in (#7) | Done |
| 2 | Quote volume paths in docker-compose.yml (#10) | Done |
| 3 | Add container resource limits (`mem_limit`, `cpus`, `pids_limit`) (#11) | Done |
| 4 | Document install script risk in Dockerfile (#12) | Done |
| 5 | Remove dead `ln -sfr` symlink in Dockerfile (#13) | Done |
| 6 | Remove redundant `mkdir -p` in entrypoint.sh (#14) | Done |
| 7 | Docker-in-Docker `--dind` flag | Done |
| 8 | `daedalus prune` subcommand | Done |

### Sprint 1: Foundation (v0.1.0)

Delivered 2026-02-15. Initial Docker-only release.

- Dockerfile with Claude Code CLI, security hardening (dropped capabilities, no-new-privileges)
- `docker-compose.yml` with read-only filesystem and credential mounting
- `entrypoint.sh` launching Claude Code with `--dangerously-skip-permissions`
- Pre-approved tool settings via `.claude/settings.json`

### Sprint 2: Go Migration & Core Features (v0.2.0)

Migrated from 314-line bash script to Go binary. Added project management.

- Complete rewrite: `run.sh` → `daedalus` Go binary
- Project registry (`.cache/projects.json`) with atomic writes
- tmux session wrapping with detach/reattach
- CLI subcommands (`list`, `--help`, positional args)
- Multi-stage Dockerfile (base, utils, dev, godot)
- Resolved 10 code review issues inherited from bash era

### Sprint 3: UI Layer & Architecture (v0.3.0)

Three UI surfaces sharing one core. Clean architecture extraction.

- TUI dashboard (`daedalus tui`) — bubbletea + lipgloss
- Web UI dashboard (`daedalus web`) — REST API + xterm.js terminal via WebSocket/PTY
- `core/` package extraction — pure types and functions, zero I/O imports
- Copyright headers on all source files
- 113 tests total, zero regressions
- Resolved 6 additional code review issues

---

## Future Sprints

(see Current Sprint above)

---

## Open Code Review Issues

| # | Issue | Severity | Sprint |
|---|-------|----------|--------|
| ~~26~~ | ~~`claude /login` replaces `.credentials.json` (new inode), breaking bind-mount into running containers~~ | ~~Major~~ | Closed (credentials no longer bind-mounted) |
| ~~27~~ | ~~TUI kill (`K`) does not stop the container~~ | ~~Major~~ | Fixed (v0.8.1 — `executor.Output` instead of `executor.Run`) |
