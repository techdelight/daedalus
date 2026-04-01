# Contribution Guide

## Branch Workflow

Develop per feature on a branch. When the feature is Done, merge to `master` and push.

**Branch naming**: `<type>/<short-description>` where type is one of:
- `feature/` — new functionality
- `fix/` — bug fixes
- `refactor/` — restructuring without behavior change
- `chore/` — docs, config, tooling

**Commit messages** start with the branch name and a clean summary:

```
feature/web-ui: add REST API for project management.
Implements GET/POST endpoints for listing, starting, and stopping projects.
```

**Merge to master**: squash or merge commit, no rebasing published branches.

## Building

```bash
# Build the Go binary (requires Docker)
./build.sh

# Run all tests
./test.sh

# Run tests locally (requires Go 1.25+)
go test -v ./...
```

## Test-Driven Development

All changes follow red-green-refactor:

1. **Red** — Write a failing test that defines the expected behavior
2. **Green** — Write the minimum code to make the test pass
3. **Refactor** — Clean up while keeping tests green

### Test Structure

Tests use **Arrange / Act / Assert** with table-driven cases:

```go
func TestConfig_UseTmux(t *testing.T) {
    tests := []struct {
        name   string
        cfg    Config
        expect bool
    }{
        {"default", Config{}, true},
        {"with prompt", Config{Prompt: "do stuff"}, false},
        {"no tmux flag", Config{NoTmux: true}, false},
        {"both", Config{Prompt: "x", NoTmux: true}, false},
    }
    for _, tc := range tests {
        t.Run(tc.name, func(t *testing.T) {
            got := tc.cfg.UseTmux()
            if got != tc.expect {
                t.Errorf("UseTmux() = %v, want %v", got, tc.expect)
            }
        })
    }
}
```

### Integration Tests

Add a new integration test if applicable. Integration tests use real file I/O with temporary directories:

```go
func setupWebTest(t *testing.T) (*WebServer, *MockExecutor) {
    t.Helper()
    tmpDir := t.TempDir()
    reg, _ := NewRegistry(filepath.Join(tmpDir, "projects.json"))
    mock := &MockExecutor{}
    cfg := core.Config{ScriptDir: tmpDir}
    ws := &WebServer{cfg: cfg, registry: reg, executor: mock}
    return ws, mock
}
```

Key patterns:
- Use `t.TempDir()` for filesystem tests (auto-cleaned)
- Use `httptest.NewRequest` / `httptest.NewRecorder` for HTTP handler tests
- Use `MockExecutor` to stub out shell commands

### Running Tests

```bash
# All tests
go test ./...

# Verbose with race detection
go test -v -race ./...

# Single package
go test ./core/

# Single test
go test -run TestConfig_UseTmux ./core/

# Integration tests only
go test -v ./cmd/daedalus/ -run Integration

# Changelog extraction tests
bash scripts/extract-changelog_test.sh

# Install script tests (mocked downloads, no network)
bash scripts/test-install.sh
```

## Continuous Integration

GitHub Actions runs on every push to `master` and on pull requests:

- `go vet ./...` — static analysis
- `go test -v -race ./...` — tests with race detector
- `go build -o daedalus ./cmd/daedalus` — compilation check

Releases are built automatically when a version tag (`v*`) is pushed.

## Code Quality

### Naming

- **Intention-revealing names** — `IsContainerRunning` not `checkContainer`
- **Avoid** `util`, `helper`, `manager` — name by what it does
- **Package-qualified context** — `core.Config` not `core.CoreConfig`
- **Test names describe behavior** — `TestShellQuote_WithSpaces` not `TestShellQuote2`

### Functions

- Small, single-purpose, one abstraction level
- Prefer pure logic; push IO to the edges
- Enforce SRP; high cohesion, low coupling; explicit dependencies
- Return `error` as the last return value
- Handle errors immediately — no silent discards:

```go
// Good
if err := reg.TouchProject(name); err != nil {
    return fmt.Errorf("touch project %q: %w", name, err)
}

// Bad — silent discard
reg.TouchProject(name)
```

### Command-Query Separation (CQS)

- **Query** — returns data, has no side effects
- **Command** — performs an action, has side effects, returns void/error only
- If a function both returns meaningful data and changes state, split it into two operations

### Error Handling

- Wrap errors with context: `fmt.Errorf("start container %q: %w", name, err)`
- Validate inputs at boundaries; fail fast; no swallowed errors
- Return errors to the caller; let the top-level decide how to present them
- Log-and-continue only for non-fatal operations (e.g., Docker status check in TUI)

### Refactoring

- Remove duplication with judgment; avoid premature abstraction
- Refactor opportunistically (Boy Scout Rule) without expanding scope
- If code becomes hard to read, stop and refactor before adding more

## Architecture

### I/O Separation

Input/Output must always be in a separate package/library from core logic.

### `core/` Package (Pure Logic)

The `core/` package has **zero I/O imports** — no `os`, `exec`, `net`, `http`, or `syscall`.

It contains:
- `Config` struct and helper methods (`UseTmux`, `ContainerName`, `SessionName`)
- Registry data types (`Project`, timestamps)
- Command builders (`ComposeRunArgs`, `ShellQuote`, `BuildTmuxCommand`)
- Time helpers (`RelativeTime`)

### Main Package (I/O Boundary)

All side effects live in the main package:
- `Executor` interface abstracts shell commands (with `MockExecutor` for tests)
- `Registry` reads/writes JSON files
- `DockerClient` runs `docker` commands
- `SessionManager` manages tmux sessions
- `WebServer` handles HTTP/WebSocket

**Rule**: If a function needs `os`, `exec`, or `net`, it belongs in the main package, not `core/`.

## Web Technology

- Use HTML, CSS, and JavaScript
- For JavaScript UI, use a small library: **Alpine.js**
- For graphics, if needed, use: **Three.js**

## Services

- Every service should start by displaying the VERSION, build-timestamp, and the Techdelight logo (from `logo.txt`)
- Every service has a debug mode that logs incoming and outgoing messages

## Build Tooling

- For fat jars, use the `maven-assembly-plugin`. Never use `maven-shade-plugin`.

## Copyright Headers

Every source file must have a copyright header as the first line.

| File type | Format |
|-----------|--------|
| `.go`     | `// Copyright (C) 2026 Techdelight BV` |
| `.html`   | `<!-- Copyright (C) 2026 Techdelight BV -->` |
| `.css`    | `/* Copyright (C) 2026 Techdelight BV */` |
| `.js`     | `// Copyright (C) 2026 Techdelight BV` |

## Definition of Done

A change is done when **all** of the following are true:

- [ ] Feature requirements are met
- [ ] Code quality is up to standards (naming, CQS, SRP, error handling)
- [ ] `go build` succeeds with no warnings
- [ ] `go test ./...` passes (zero failures)
- [ ] `go vet ./...` reports no issues
- [ ] New code has tests (table-driven where applicable)
- [ ] `core/` package has no I/O imports
- [ ] Copyright headers present on new files
- [ ] All documentation is up-to-date (README, CHANGELOG, VERSION, ARCHITECTURE)
- [ ] VERSION is semantically updated
- [ ] CHANGELOG.md updated under `[Unreleased]`
- [ ] Software still runs
- [ ] All changes are committed with branch-name-prefixed message
