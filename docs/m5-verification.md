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

**Runnable form:** [`scripts/verify-m5.sh`](../scripts/verify-m5.sh) automates the
Docker-side checks below —
`verify-m5.sh build` (items 1 + 5),
`verify-m5.sh mounts <project>` (items 2 + 3, against a running project), and
`verify-m5.sh persist <project>` (item 2 #27, after a restart). It prints
credential/interactive steps as `MANUAL` rather than faking them; the checklist
here is the narrative behind it.

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

Run a real project on the runner path and inspect the mounts. Set `C` to the
container name (`claude-run-<project>`) and `D` to your `<DataDir>` for the
snippets below.

### ⚠ uid preflight (top risk — check FIRST)

The image runs its `claude` user at the uid it was **built** with
(`CLAUDE_UID = os.Getuid()` at build); the coordinator creates the shared/tools
host dirs at the uid it **runs** as. If those differ (image built by another
user or in CI, run here), the container can't write them → `Permission denied`.
Daedalus now records the build uid and the coordinator logs a clear warning on
mismatch — so check the log first, and confirm the uids line up:

```sh
grep -i 'built as uid' "$D/.daedalus/coordinator.log"   # expect NO mismatch warning
cat "$D/build-uid"; id -u                                 # build uid vs current uid — should match
docker exec "$C" id -u                                    # container claude uid — should equal build uid
```

- [ ] No uid-mismatch warning in the coordinator log; `build-uid` == `id -u` ==
      container `id -u`. If they differ: rebuild as the current user
      (`daedalus --build`). This is the fix-first signal.

### Mounts present and writable

- [ ] `daedalus <project>` starts on the runner path (default).
- [ ] **#55** — the expected mounts are present:
      ```sh
      docker inspect "$C" --format '{{json .Mounts}}' | jq -r '.[].Destination' | sort
      # expect: /home/claude/.local/share/claude/versions, /home/claude/.m2,
      #         /opt/skills, /opt/tools, /workspace/.daedalus (+ the home mount)
      ```
      Skills work in-session and project-mgmt MCP progress writes `.daedalus/`.
- [ ] **Writable by the container** (the uid check, proven directly):
      ```sh
      for p in /opt/skills /opt/tools /home/claude/.m2 \
               /home/claude/.local/share/claude/versions /workspace/.daedalus; do
        docker exec "$C" sh -c "touch $p/.wtest && rm $p/.wtest && echo OK $p || echo FAIL $p"
      done
      ```
- [ ] **#37** — Claude versions land in `$D/shared/claude-versions/` on the host
      (not the per-project cache); a second project reuses them.
- [ ] **#21** — Maven artifacts land in `$D/shared/m2/`; shared across projects.
- [ ] **#27** — `docker exec "$C" sh -c 'cp $(command -v jq) /opt/tools/bin/jq'`,
      stop + restart the project, then `docker exec "$C" command -v jq` still
      resolves to `/opt/tools/bin/jq`. On the host it lives at
      `$D/tools/<project>/bin/`.
- [ ] **Nested mounts:** the shared caches at subpaths under `/home/claude` are
      not masked by the home mount — the `.local/share/claude/versions` and
      `.m2` destinations above appear populated, not empty.

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
