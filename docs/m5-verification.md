# Milestone 5 — Host Verification Checklist

Milestone 5 was implemented in an environment without a Docker daemon or a
browser, so every image/container/volume/mobile behaviour is code-complete but
**unverified**. This checklist maps each assumption from the execution report
to a concrete build/run step to tick off **on a Docker-capable host**. Full
context: [milestone-5-plan.md](milestone-5-plan.md); the `[Unreleased]`
CHANGELOG records the same assumptions.

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

- [ ] On an **older project cache** whose `.claude.json` predates the trust
      keys, start the project and confirm the "trust this folder?" dialog does
      **not** fire (the entrypoint force-set patched it in).

## 4. Mobile WebSocket resilience (#29)

- [ ] On a phone, open the Web UI terminal; **background the tab** for >1 min →
      returning reconnects and repaints (not `[Connection closed]`).
- [ ] **Switch Wi-Fi ↔ cellular** mid-session → auto-reconnect within the
      backoff window; the screen repaints via replay.
- [ ] Desktop: kill the network briefly → `[Connection lost — reconnecting…]`
      then recovery; a deliberate navigation still shows `[Connection closed]`
      (intentional-close flag works).

## 5. Deferred — close after the above pass

- [ ] **#51 installer pinning** — pin the Claude and Copilot installers to a
      known version + checksum (the `TODO(#51)` markers), removing the unpinned
      `curl | bash` supply-chain risk.
- [ ] **#21 Maven overlay** — if the single shared writable `.m2` shows
      cross-project pollution problems, move to a read-only shared base + a
      per-project writable overlay.

## Verdict

- [ ] All build steps green and all runtime checks pass → M5 verified; flip the
      CHANGELOG note from "implemented, not yet verified" to verified.
- [ ] Any failure → capture it against the assumption above; the uid/permission
      and nested-mount items are the most likely to bite.
