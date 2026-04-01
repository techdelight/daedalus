# tmux Control Mode Integration — Design Document

## Status: Approved for phased implementation

## Problem

The Web UI terminal relay uses a raw PTY attached to `tmux attach-session`. This works for live interaction but has fundamental limitations:

1. **No scrollback** — users must enter tmux copy mode (`Ctrl+B [`) to see prior output. The Web UI has no way to request historical content.
2. **No command introspection** — no structured way to detect command completion or exit codes.
3. **Race conditions** — two goroutines reading/writing the same PTY can collide.
4. **No state queries** — cannot programmatically ask "what's in the pane right now?"

## Solution

Replace the raw PTY relay with tmux control mode (`tmux -C`) for the Web UI terminal. Control mode is a machine-friendly line protocol that provides structured output notifications, command responses, and scrollback access.

**Scope**: Web UI terminal only. CLI attach and TUI are unaffected — they continue using normal `tmux attach-session`.

## Current Architecture

```
Browser ──WebSocket──► relayWebSocketToPTY ──► PTY ──► tmux attach-session
                         ▲                                      │
                         └──────── relayPTYToWebSocket ────────┘
```

- `handleTerminal()` in `internal/web/web.go` upgrades HTTP to WebSocket
- `startPTY()` spawns `tmux attach-session -t <name>` wrapped in a pseudo-terminal
- Two goroutines relay bytes bidirectionally between PTY and WebSocket
- Terminal resize is handled via JSON messages on the WebSocket

## Proposed Architecture

```
Browser ──WebSocket──► writeControlMessages ──► stdin ──► tmux -C attach -t <name>
                         ▲                                         │
                         └──────── readControlMessages ───── stdout┘
                                        │
                                  parseControlLine()
                                        │
                              ┌─────────┴──────────┐
                              │                    │
                         %output → WS binary    %end → WS JSON
                         (pane data)            (command result)
```

- `startControlSession()` spawns `tmux -C attach-session -t <name>` (no PTY needed)
- stdin/stdout pipes replace the PTY
- A parser converts `%output`, `%begin`, `%end`, `%error` lines into typed messages
- The WebSocket carries two message types:
  - **Binary**: raw pane output (from `%output` notifications)
  - **JSON**: structured events (scrollback responses, resize notifications, errors)

## Control Mode Protocol

tmux control mode (requires tmux 2.4+) communicates via `%`-prefixed line messages.

### Server → Client (notifications)

| Message | Format | Purpose |
|---|---|---|
| `%output` | `%output <pane-id> <data>` | Pane content was added |
| `%begin` | `%begin <cmd-id> <flags>` | Command response starting |
| `%end` | `%end <cmd-id> <code>` | Command response complete (code 0 = success) |
| `%error` | `%error <cmd-id> <message>` | Command failed |
| `%layout-change` | `%layout-change <win-id> <layout> <w> <h>` | Window resized |

### Client → Server (commands)

| Command | Purpose |
|---|---|
| `send-keys -t <pane> <keys>` | Type into the pane |
| `capture-pane -t <pane> -p -S -<n>` | Get last *n* lines of scrollback |
| `capture-pane -t <pane> -p -e` | Get pane content with ANSI escapes |
| `resize-window -t <win> -x <w> -y <h>` | Resize the window |

### Scrollback Example

```
Client:  capture-pane -t mysession -p -S -500
Server:  %begin 1234 0
Server:  (500 lines of pane content)
Server:  %end 1234 0
```

## New WebSocket Message Types

The WebSocket protocol gains new JSON message types alongside the existing `resize`:

```json
// Client → Server: request scrollback
{"type": "scrollback", "lines": 500}

// Server → Client: scrollback response
{"type": "scrollback-response", "content": "...500 lines..."}

// Server → Client: pane resized (from %layout-change)
{"type": "pane-resized", "cols": 120, "rows": 40}
```

Binary messages remain unchanged — raw terminal output forwarded to xterm.js.

## Component Impact

| Component | Change | Complexity |
|---|---|---|
| `internal/session/` | New `ControlSession` struct + `CapturePane()` method, control message parser | M |
| `internal/web/web.go` | New `handleTerminalControl()` handler alongside existing PTY relay | L |
| `internal/web/static/terminal.js` | Add scrollback request WS message type | S |
| `core/config.go` | No change (web-only, no config flag needed) | — |
| `cmd/daedalus/main.go` | No change (CLI/TUI unaffected) | — |
| `internal/tui/` | No change | — |
| `entrypoint.sh` | No change (container-side, tmux is host-side) | — |

## Implementation Plan

### Phase 1: Control Session Package

New files in `internal/session/`:

```go
// control.go
type ControlSession struct {
    name   string
    cmd    *exec.Cmd
    stdin  io.Writer
    stdout *bufio.Scanner
}

func NewControlSession(exec executor.Executor, name string) *ControlSession
func (cs *ControlSession) Start() error          // spawn tmux -C attach-session
func (cs *ControlSession) SendKeys(keys string) error
func (cs *ControlSession) CapturePane(lines int) (string, error)
func (cs *ControlSession) ResizeWindow(cols, rows int) error
func (cs *ControlSession) Close() error

// controlparser.go
type ControlMessage struct {
    Type    string // "output", "begin", "end", "error", "layout-change"
    PaneID  string
    CmdID   string
    Code    int
    Content string
}

func ParseControlLine(line string) *ControlMessage
```

**Tests**: parser unit tests with all message types, session integration tests with `MockExecutor`.

### Phase 2: Web Terminal Control Relay

Modify `internal/web/web.go`:

- Add `handleTerminalControl()` that uses `ControlSession` instead of PTY
- Route decision: use control mode when available, fall back to PTY relay
- Add scrollback request handler
- Keep `handleTerminal()` as-is for backward compatibility

### Phase 3: Frontend Scrollback

Modify `internal/web/static/terminal.js`:

- Add "Scroll to top" / "Load more" button above terminal
- Send `{"type": "scrollback", "lines": 500}` WebSocket message
- Render scrollback content above live terminal output
- Progressive loading (request 500 lines at a time)

### Phase 4: Cutover

- Make control mode the default for web terminal
- Remove PTY relay (or keep as fallback behind flag)
- Document in README

## Requirements

- tmux 2.4+ (released 2016, standard on Ubuntu 20.04+, RHEL 8+, macOS Homebrew)
- No container-side changes
- No new dependencies

## Migration Strategy

Both modes coexist during transition:

1. **Phase 1-2**: Control mode available but PTY relay remains default
2. **Phase 3**: Control mode becomes default for web; PTY relay available as fallback
3. **Phase 4**: PTY relay removed (optional — can keep indefinitely at low maintenance cost)

The CLI and TUI are never affected. They continue using `tmux attach-session` directly.

## Risks

| Risk | Severity | Mitigation |
|---|---|---|
| Control message parsing bugs | Medium | Comprehensive unit tests for all `%` message types |
| Binary data in `%output` | Medium | Handle embedded newlines and special characters carefully |
| tmux version too old | Low | Check version at startup, fall back to PTY |
| Performance regression | Low | Control mode sends less data than raw PTY (no cursor positioning noise) |

## Estimated Effort

| Phase | Scope | Estimate |
|---|---|---|
| 1. Control session + parser | ~350 lines Go + tests | 1 sprint |
| 2. Web relay refactor | ~250 lines Go + tests | 1 sprint |
| 3. Frontend scrollback | ~150 lines JS | 0.5 sprint |
| 4. Cutover + docs | ~100 lines | 0.5 sprint |
| **Total** | **~850 lines** | **3 sprints** |
