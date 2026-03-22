# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

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
