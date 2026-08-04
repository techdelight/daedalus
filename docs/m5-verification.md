# Milestone 5 — Host Verification Checklist

Milestone 5 was implemented in an environment without a Docker daemon or a
browser, so every image/container/volume behaviour is code-complete but
**unverified**. This checklist maps each assumption from the execution report
to a concrete build/run step to tick off **on a Docker-capable host**. Full
context: [milestone-5-plan.md](milestone-5-plan.md); the `[Unreleased]`
CHANGELOG records the same assumptions.

**These are the steps that cannot run in the container-only dev environment** —
they need a machine with a Docker daemon and (for the runtime checks) Claude /
Copilot credentials. Everything that *could* be verified statically or with a
unit/shell test already has been.

## Status (Sprint 43)

| Sprint 43 item | State |
|---|---|
| 4 — Mobile #29 | ✅ Done — verified on a real phone |
| 7 — Retire tmux | ✅ Done — code-only, landed |
| 3 — Trust idempotency | 🔶 Logic regression-tested + entrypoint hardened; **on-image confirmation below** is the remaining step |
| 5 — Installer pins | 🔶 Implemented (pinned + checksum-verified by the installers); **on-build confirmation below** |
| 1 — Dockerfile build/cache | ⬜ Pending — needs a host build |
| 2 — Runtime mounts (uid risk) | ⬜ Pending — needs a host run |
| 6 — Maven overlay | ⬜ Deferred — only if the shared `.m2` shows pollution |

## Preconditions

- [ ] `./build.sh` — produces the five Go binaries the image `COPY`s (needs Docker).
- [ ] `./test.sh` green, `./e2e/run-repaint.sh` green.

## 1. Dockerfile layer restructure (#51)

Statically checked (stage graph resolves, hadolint-clean) but **not built**.

- [ ] `docker build --target base        -t daedalus:m5 .`
- [ ] `docker build --target utils       -t daedalus:m5 .`
- [ ] `docker build --target dev         -t daedalus:m5 .`
- [ ] `docker build --target godot       -t daedalus:m5 .`
- [ ] `docker build --target copilot-base -t daedalus:m5 .`
- [ ] `docker build --target copilot-dev -t daedalus:m5 .`
- [ ] **Cache win:** rebuild `dev` after touching a Daedalus binary (`touch daedalus-runner`) → the Go/SDKMAN layers are `CACHED`, only the final COPY re-runs.

**Assumption checked:** `COPY --chown` under `USER claude`, ENV/ENTRYPOINT/USER
inheritance across the new parent/leaf graph.

## 2. Coordinator mounts (#55) + shared caches (#37/#21) + tools (#27)

Run a real project on the runner path and inspect the mounts.

- [ ] `daedalus <project>` starts on the runner path (default).
- [ ] **#55** — skills work: the skill catalog is available in-session, and
      project-mgmt MCP progress reporting works (`.daedalus/` written).
      Confirm the container has `/opt/skills` and `/workspace/.daedalus` mounts
      (`docker inspect <container> --format '{{json .Mounts}}' | jq`).
- [ ] **#37** — Claude versions land in `<DataDir>/shared/claude-versions/` on
      the host (not the per-project cache); a second project reuses them.
- [ ] **#21** — Maven artifacts land in `<DataDir>/shared/m2/`; shared across
      projects.
- [ ] **#27** — put a binary in `/opt/tools/bin/` inside the container, stop +
      restart the project, confirm it's still on `PATH`. Verify it lives at
      `<DataDir>/tools/<project>/bin/` on the host.
- [ ] **⚠ uid assumption (top risk):** confirm the container's `claude` user can
      write the shared/tools dirs — no `Permission denied` in the session or the
      coordinator log (`<DataDir>/.daedalus/coordinator.log`). If the host user
      isn't uid 1000, expect failures here; that's the fix-first signal.
- [ ] **Nested mounts:** confirm the shared caches at subpaths under
      `/home/claude` are not masked by the home mount (they appear populated).

## 3. Trust-prompt idempotency

The config transform is already regression-tested off-host — the entrypoint's
jq force-set is exercised against old-cache fixtures by
`scripts/test-trust-idempotency.sh` (in CI), and the patch is hardened to be
non-fatal so a malformed cache can't crash startup. What's left is confirming
the *behaviour* against real Claude:

- [ ] `bash scripts/test-trust-idempotency.sh` is green (fast, no Docker — run
      it first as a smoke check of the filter itself).
- [ ] On an **older project cache** whose `.claude.json` predates the trust
      keys (or carries `hasTrustDialogAccepted: false`), start the project and
      confirm the "trust this folder?" dialog does **not** fire (the entrypoint
      force-set patched it in). Simulate an old cache by editing
      `<DataDir>/<project>/.claude-config/.claude.json` to drop the
      `projects["/workspace"]` trust keys, then start the project.

## 4. Mobile WebSocket resilience (#29) — ✅ DONE

Verified on a real phone (Sprint 43 item 4): backgrounding the tab and
switching Wi-Fi ↔ cellular both reconnect and repaint; a deliberate navigation
still closes cleanly. No further host steps.

## 5. Installer pins (#51) — implemented, confirm on build

The Claude and Copilot installers are now version-pinned via Dockerfile build
args (`CLAUDE_VERSION`, `COPILOT_VERSION`), replacing the unpinned `latest`
`curl | bash`. Both installers verify the downloaded binary's SHA-256 (Claude
against Anthropic's release `manifest.json`; Copilot against the release's
`SHA256SUMS.txt`), so the binaries are checksum-verified. Confirm on a build:

- [ ] `docker build --target dev -t daedalus:m5 .` succeeds with the pinned
      `CLAUDE_VERSION`; inside the image, `claude --version` reports it (or a
      newer runtime auto-update — the pin is the install floor).
- [ ] `docker build --target copilot-dev -t daedalus:m5 .` succeeds; inside the
      image, `copilot --version` reports the pinned `COPILOT_VERSION`.
- [ ] Override works: `docker build --target dev --build-arg CLAUDE_VERSION=stable .`
      builds (proves the arg is wired), and a bad pin fails fast (expected).
- [ ] Bump procedure: update the `ARG CLAUDE_VERSION` / `ARG COPILOT_VERSION`
      defaults in the `Dockerfile` to a newer released version when refreshing
      the floor.

## 6. Deferred — Maven overlay (#21)

- [ ] If the single shared writable `.m2` shows cross-project pollution
      problems, move to a read-only shared base + a per-project writable
      overlay.

## Verdict

- [ ] All build steps green and all runtime checks pass → M5 verified; flip the
      CHANGELOG note from "implemented, not yet verified" to verified.
- [ ] Any failure → capture it against the assumption above; the uid/permission
      and nested-mount items are the most likely to bite.
