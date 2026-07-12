# Runner Parity Verification — Runbook

**Backlog #38 · Sprint 41 (Trust-Prompt & Runner Terminal Fidelity) · item 4**

This is the human-in-the-loop half of Sprint 41 item 4: the end-to-end
verification on **real Docker + real Claude** that cannot be automated
(it needs credentials, a network, and a real interactive agent). The
automatable half — the repaint-on-attach mechanism itself — lives in
`cmd/daedalus-runner/repaint_e2e_test.go` and runs via
[`./e2e/run-repaint.sh`](run-repaint.sh); run that first and get it green
before spending a human on this.

## What we're proving

Under the runner path, a client that attaches to a session showing a
**one-shot full-screen prompt** (Claude's "trust this folder?" dialog, the
`--resume` picker) must see that prompt — not a blank or stale pane. #38 is
that this hangs the Web UI. The fix is layered:

- **Layer 1** — pre-seeded workspace trust (`claude.json`) so the dialog
  ideally never fires inside the container.
- **Layer 2a** — the runner sizes its PTY at startup instead of leaving it
  at 0×0, so the agent renders into a real terminal.
- **Layer 2b / hedge** — repaint when a client attaches without changing
  the size (the residual gap; see `docs/runner-repaint-design.md`).

## Preconditions

- [ ] `./build.sh` succeeds (all five binaries present).
- [ ] `./test.sh` green (default suite).
- [ ] `./e2e/run-repaint.sh` green (repaint mechanism). Note which branch
      `TestRepaint_StockTerminalUserExpectation` logs — `KNOWN GAP` means
      the startup-size hedge has not landed yet.
- [ ] Docker running; a project image built.
- [ ] Claude credentials available to the container as in normal use.
- [ ] Two terminals (call them **T1**, **T2**) plus a browser for the Web UI.

> To force the trust dialog to actually appear (Layer 1 pre-seed normally
> suppresses it — and per anthropics/claude-code#9113 the pre-seed is not
> always honoured across Claude versions), temporarily blank
> `projects["/workspace"].hasTrustDialogAccepted` in the container's
> `claude.json`, or point at a fresh workspace path Claude hasn't trusted.
> Restore it afterwards. Record the Claude version under test — the
> pre-seed's reliability is version-dependent.

## Part A — Reproduce / confirm the trust prompt (the #38 scenario)

1. [ ] In **T1**: `DAEDALUS_USE_RUNNER=1 daedalus <project>` (with trust
       pre-seed disabled per the note above).
2. [ ] **Expected with Layers 1+2a:** the trust dialog renders correctly in
       T1 (readable box, not garbled, not 0×0-collapsed). Record: did it
       render? ______
3. [ ] Answer the prompt in T1; confirm Claude proceeds to its normal REPL.

If T1 shows a garbled or empty prompt, Layer 2a has regressed — capture the
pane and stop here.

## Part B — Attach fidelity (CLI second client)

With the session from Part A still on the trust prompt (relaunch fresh if
needed), and **without** answering it in T1:

4. [ ] In **T2**: `daedalus <project>` (same project → attaches to the same
       runner session; no tmux involved).
5. [ ] **The critical check.** Does T2 show the *same live trust prompt*?
   - Sizes: note T1 and T2 terminal sizes (`stty size`).
   - [ ] **T2 larger/smaller than the startup 80×24** → repaint expected
         (Layer 2a: the size delta raises SIGWINCH). Prompt visible? ______
   - [ ] **T2 exactly 80×24** (resize it: `printf '\e[8;24;80t'` in most
         emulators) → this is the residual gap. Pre-hedge: prompt likely
         **absent/stale**. Post-hedge: prompt **visible**. Result: ______
6. [ ] Answer in T2; confirm T1 reflects the same state (shared PTY).

## Part C — Web UI parity

7. [ ] With a runner session on the trust prompt, open the Web UI terminal
       for the project (it uses `?mode=runner`).
8. [ ] **Expected:** the browser terminal shows the live trust prompt, not
       a hang. This is the exact #38 symptom — verify it is resolved.
       Result: ______
9. [ ] Answer from the browser; confirm a CLI client on the same session
       reflects it.

## Part D — Other one-shot / full-screen surfaces (parity checklist)

Repeat the attach checks (fresh CLI client + Web) for each surface below.
For each: does a late/second viewer see the live screen?

| Surface | How to trigger | CLI 2nd client | Web | Notes |
|---|---|---|---|---|
| Trust dialog | Parts A–C | ☐ | ☐ | |
| `--resume` picker | launch a session that resumes; the picker is full-screen | ☐ | ☐ | |
| Credentials / login prompt | unauthenticated Claude → login flow | ☐ | ☐ | |
| Copilot adapter | `--adapter copilot` equivalent path, its startup UI | ☐ | ☐ | parity across adapters |
| Normal REPL scrollback | attach mid-session with history | ☐ | ☐ | byte-ring replay is enough here |

## Part E — Detach/reattach & size churn

10. [ ] Detach a client (`Ctrl-D` on the runner path) and reattach at the
        **same** size while a full-screen surface is up → residual-gap case;
        record whether the surface repaints.
11. [ ] Resize a terminal repeatedly while attached; confirm no corruption
        and the smallest attached size wins (min-size negotiation).

## Verdict

- [ ] **Layers 1+2a resolve the urgent first-attach case** (Parts A–C with a
      non-80×24 client). → Sprint 41 item 4 core met.
- [ ] **Residual same-size gap** observed in Part B step 5 / Part E step 10?
  - If **no** (never reproduced even at 80×24): Layer 2b may be unnecessary;
    record the environments tested.
  - If **yes**: the startup-size hedge (cheap) and/or Layer 2b / Option C
    (correct for all cases) is justified. Feed the observation back into
    `docs/runner-repaint-design.md` and Sprint 41 item 3.

Record the date, Claude version, OS, and terminal emulators used — the
pre-seed and repaint behaviour are all version- and emulator-sensitive.
