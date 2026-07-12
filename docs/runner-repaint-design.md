# Runner Repaint-on-Attach — Design

**Backlog #38 · Sprint 41 (Trust-Prompt & Runner Terminal Fidelity) · Layer 2b**

## Problem

The runner path fans one PTY out to many socket clients (CLI, Web) via the
hub in `cmd/daedalus-runner`. When a client attaches, the hub replays a **raw
byte scrollback ring** (`ringbuffer.go` → `Hello`) and then live output. It
never reconstructs the agent's *current screen*. For a one-shot full-screen UI
that is drawn once and then idles — Claude's "trust this folder?" dialog, the
`--resume` picker — a late or same-size client can see a stale or blank screen
instead of the live prompt. This is the mechanism behind #38 (Web UI hangs on
the trust prompt under the runner path).

Contrast: the classic tmux path repaints on attach for free —
`internal/web/control_relay.go` auto-issues a `capture-pane` after resize. The
runner path re-implements a thinner version (a byte ring) that does not
reconstruct screen state.

## What Layers 1 and 2a already do

- **Layer 1** (`claude.json`): pre-seeds
  `projects["/workspace"].hasTrustDialogAccepted`, so the trust dialog should
  never fire inside the container. Caveat: upstream
  (anthropics/claude-code#9113) reports pre-seeded trust is not always honoured
  across Claude versions.
- **Layer 2a** (`hub.applyInitialSize`): sizes the PTY to 80×24 at startup,
  before the agent renders, instead of creack/pty's 0×0 default.

### Key insight: 2a likely covers the *urgent* first-attach case

With 2a the agent renders at 80×24. When the first real client attaches at its
own size (e.g. 120×30), `hub.recomputeSize` sees a delta (80×24 → 120×30) and
calls `setSize`, delivering a **SIGWINCH with a real size change** — which makes
a well-behaved TUI (Claude/Ink) repaint. So the first-attach trust-prompt path
(the #38 scenario) is *probably* resolved by Layers 1 + 2a together.

The residual gap that Layer 2b addresses is narrower:

1. **Same-size (re)attach** — a client reconnects at the identical size ⇒ no
   delta ⇒ no SIGWINCH ⇒ no repaint.
2. **Second simultaneous client** at the same negotiated size — same problem.
3. **Alt-screen state fidelity** — if the dialog uses the alternate screen
   buffer, a raw-byte ring replay may not perfectly reconstruct it.

## Recommendation: verify before building

Because 2a plausibly closes the urgent case, **run the e2e verification
(Sprint 41 item 4) before implementing Layer 2b.** A full terminal emulator is
a real dependency and a real chunk of work; it should be justified by an
observed residual failure, not built speculatively. Concretely, swap the
current sprint item order: do item 4 (e2e on real Docker + Claude) first, and
build Layer 2b only if the residual cases actually break.

## Options (if Layer 2b is needed)

| | Approach | Cost | Verdict |
|---|---|---|---|
| A | Resize-nudge on attach (toggle size to force SIGWINCH) | ~10 lines | Disturbs already-attached clients (shared PTY); app must repaint on SIGWINCH |
| B | Inject redraw keystroke (Ctrl-L) on attach | trivial | App-specific, unreliable; also pokes the shared PTY |
| C | Server-side VT emulator + screen snapshot on attach | ~1 slice | Correct for all cases; tmux parity; per-client repaint without disturbing others |

**Option C is the only one correct for the shared-PTY fan-out** — the snapshot
is synthesized per-attach from shared emulator state, so a new viewer repaints
independently. A and B poke the single shared PTY and disturb existing viewers.

## Option C — implementation sketch

1. A `screen` type (in `cmd/daedalus-runner`, or a new `internal/vt`) wrapping a
   headless VT emulator: `Write([]byte)` feeds PTY output; `Snapshot() []byte`
   emits a byte sequence reproducing the current screen (clear, cells + SGR,
   cursor position, alt-screen / mode state).
2. Hub `fromPty` case: feed `screen.Write(data)` alongside the existing
   `scroll.Append` + `broadcast`.
3. On `add`: send `Hello` with `screen.Snapshot()` (the reconstructed live
   screen). Each new client gets a correct repaint, zero disturbance to others.
4. On `setSize`: also `screen.Resize(cols, rows)` so snapshots reflow.
5. Tests (unit, no Docker): feed an alt-screen "draw box + wait" sequence and
   assert `Snapshot()` reproduces it; a hub test attaches a client after such
   output and asserts the `Hello` payload contains the box. This makes #38's
   core mechanism testable in-package.

### Dependency

A VT emulator is a deliberate exception to the five-direct-dependency discipline
(a terminal multiplexer fundamentally needs a terminal model). Candidates, in
order of preference:

- `github.com/charmbracelet/x/vt` — coherent with the existing charm stack
  (bubbletea/lipgloss); verify its snapshot / serialize API first.
- `github.com/hinshun/vt10x` — mature, standalone.
- In-house minimal parser — avoids the dependency but is more code to own; only
  if the above don't fit.

## Sequencing

```
Sprint 41:
  [1 Layer 1 ✓] [2 Layer 2a ✓]
        → [4 e2e verify on real Docker + Claude]
        → (only if residual breakage) [3 Layer 2b / Option C]
        → [5 flip runner default, retire tmux]
```

Layer 2b, if built, gives the runner path tmux-equivalent attach fidelity — the
precondition for retiring the tmux launch path (item 5).

## Decision (Sprint 41)

Built **smart replay-from-boundary** — a lightweight middle ground between the
raw byte ring and a full VT emulator. On attach, the hub replays scrollback
from the last screen-establishing boundary (alt-screen enter, `\e[2J`/`\e[3J`,
RIS) instead of from the ring's oldest byte, so a one-shot dialog reconstructs
for *every* viewer (first, second, same-size, reattach) with no SIGWINCH trick,
no shared-PTY poke, and no dependence on the app repainting. It falls back to
the raw snapshot when no boundary is retained, so it is never worse than
before. Code: `cmd/daedalus-runner/screen.go` (`ScreenSnapshot`), wired into the
hub's `Hello`; unit tests in `screen_test.go`; end-to-end proof in
`repaint_e2e_test.go` (run via `e2e/run-repaint.sh`).

It does not reconstruct SGR/cursor state set *before* the boundary — the full
Option C emulator would. That is deferred until the real-Docker+Claude parity
pass (`e2e/runner-parity-runbook.md`) shows it is actually needed.

Two cheaper hedges were considered and rejected:

- **Fixed non-80×24 startup size** (e.g. 81×24) so a stock terminal's attach
  always deltas → SIGWINCH → repaint. Reliable but a magic constant, and only
  helps the *first* attach (not a second same-size client or a same-size
  reattach).
- **Size-toggle on first attach** to force a SIGWINCH. Empirically a same-size
  `TIOCSWINSZ` delivers **no** SIGWINCH (verified on Linux: same-size ×3 → 0
  signals; a real change → 1), so it requires a visible shrink-and-restore
  toggle that Ink may debounce away — fragile, and it disturbs the shared PTY.

Smart replay avoids both failure modes and serves all viewer cases, so it was
chosen over either hedge.
