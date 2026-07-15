# Runner Path — Status

**As of 2026-07-15 · Sprint 41 (Trust-Prompt & Runner Terminal Fidelity) · Milestone 4**

A single place to see where the runner-path rollout stands. For the design of
the individual pieces see [ARCHITECTURE.md](../ARCHITECTURE.md); for the #38
repaint work see [runner-repaint-design.md](runner-repaint-design.md).

## Where we are

The runner path (host `daedalus-coordinator` daemon + in-container
`daedalus-runner` PID 1, PTY fanned over a Unix socket) is **feature-complete
and confirmed working end-to-end on both CLI and Web**. It remains **opt-in**:

- CLI: `DAEDALUS_USE_RUNNER=1 daedalus <project>`
- Web: `DAEDALUS_USE_RUNNER=1 daedalus web` → "runner mode" badge, **Open**
  launches a runner session via the coordinator and attaches (no CLI
  pre-launch). A `?mode=runner` URL query overrides per-terminal.

The trust-prompt gap (Backlog #38) that blocked making it the default is fixed
via **smart replay-on-attach**: the runner replays scrollback from the last
screen boundary, so a one-shot dialog reconstructs for late / second /
same-size viewers. Covered by `cmd/daedalus-runner/repaint_e2e_test.go`
(run `./e2e/run-repaint.sh`).

## What got fixed getting here

Dogfooding the parity pass surfaced a chain of runner-path integration bugs —
the runner path was effectively unusable end-to-end before these. All on
`development`:

| Commit | Fix |
|---|---|
| `68f4d54` | #38 smart replay-on-attach (Layer 2b) |
| `35efa84` | Web starts a runner session on attach (autostart) |
| `e18d281` | Browser `?mode=runner` opt-in |
| `6ac2be3` | Visible web start failures + running-container guard |
| `d016787` | Reap stale (stopped) container before start |
| `8adcfd4` | CLI runner re-attach no longer blocked by the tmux guard |
| `d25c985` | **Root cause** — `docker compose run --rm --detach` removed the container before the runner bound its socket |
| `9128865` | Coordinator start / timeout logging (points at `docker logs`) |
| `94df7d9` | Reap stale *sessions* whose container died out-of-band (`Get`/`Start` liveness) |
| `2218544` | Web dashboard is a first-class runner client (badge, Open) |

## What's left

1. **Parity pass (the gate)** — the manual real-Docker + Claude checklist in
   [`e2e/runner-parity-runbook.md`](../e2e/runner-parity-runbook.md): trust
   dialog, `--resume` picker, credentials, copilot adapter, CLI+Web sharing
   one session (esp. same-size 80×24 attach + second viewer). Automatable half
   is green; this human pass is what closes Sprint 41 item 4.
2. **Flip to default** — drafted, **gated**, on branch
   `feat/runner-default-flip` (`core.UseRunner()`; runner default, opt out with
   `DAEDALUS_USE_TMUX=1`). Merge once the parity pass is green. First half of
   Sprint 41 item 5.
3. **Retire the tmux launch path** — second half of item 5, after the runner
   default has proven out in the field.
4. **TUI** — migrate the TUI project list to the coordinator client (M4).

## Known follow-ups (backlog)

- **#47** — `coordinator status` should show the daemon's build/version and
  warn when the on-disk binary is newer than the running PID (the long-lived
  daemon reuses an old binary after a rebuild — a real debugging footgun).
- **#48** — auto-restart the daemon on binary change (builds on #47).

## Operating notes

- The coordinator **auto-spawns** on first runner-path use (ssh-agent style) —
  no manual start needed. `daedalus coordinator start/stop/status` exist for
  explicit control; `contrib/` has systemd/launchd units.
- **After rebuilding**, restart the daemon (`daedalus coordinator stop`, then
  any runner command re-spawns the fresh binary) — `EnsureRunning` reuses a
  running daemon and won't pick up a new binary on its own (see #47/#48).
- When a runner fails to come up, the reason is in the daemon log at
  `<DataDir>/.daedalus/coordinator.log` (and it points you at
  `docker logs <container>`).
