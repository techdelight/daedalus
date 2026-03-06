# Roadmap

## Backlog

(empty)

## Current Sprint

(none)

---

## Sprint History

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

### Sprint 9: 1.0 Preparation

- Stability audit — no breaking changes after 1.0
- End-to-end integration test suite
- Binary releases via GitHub Actions (Linux amd64/arm64, macOS amd64/arm64)
- Man page generation
- Final documentation pass

---

## Open Code Review Issues

| # | Issue | Severity | Sprint |
|---|-------|----------|--------|
| ~~26~~ | ~~`claude /login` replaces `.credentials.json` (new inode), breaking bind-mount into running containers~~ | ~~Major~~ | Closed (credentials no longer bind-mounted) |
| 27 | TUI kill (`K`) does not stop the container | Major | — |
