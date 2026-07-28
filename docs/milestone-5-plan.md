# Milestone 5 — Self-Sustaining Operations: Plan

**Status:** planned, not started · design decisions settled 2026-07-28 · grounded in a code survey of the Dockerfile, coordinator, docker-compose, and web relay.

Milestone 5 makes Daedalus **cheap to run and hard to knock over** over time: less per-project disk, and resilience across restarts and network blips. It decomposes into five backlog items across two themes. (Homebrew distribution, #11, is deliberately out of scope for M5 — it is independent and ops-only, tracked as a standalone backlog item.)

| Theme | Items |
|---|---|
| **Storage efficiency** | #51 Dockerfile layers · #37 shared Claude versions · #21 shared Maven `.m2` |
| **Runtime resilience** | #27 tooling decouple + persistence · #29 mobile WebSocket · automatic trust handling |

## Implementation status (2026-07-28)

All five items were implemented autonomously in one pass. Go build + the full
unit suite (32 packages) + `daedalus docs lint --ci` are green. **The Docker
image, container-run, shared-volume, and mobile-device behaviours are NOT
exercised here (no Docker daemon / browser in the build environment)** — every
container/image/volume change is code-complete and statically checked but needs
a real `daedalus --build` + run + mobile drive before it is trusted. See the
execution report for the full assumptions/verification-gap list.

| Item | Status |
|---|---|
| #55 coordinator mounts | Implemented — `core.RunnerVolumeArgs` wired into the coordinator start; skills + `.daedalus` restored on the default path |
| #51 Dockerfile layers | Implemented (stage graph statically verified); installer **pinning deferred** — needs a build to confirm the installers' version interface |
| #37 Claude versions | Implemented — shared bind-mount at the versions subpath |
| #21 Maven `.m2` | Implemented as a **simple shared writable `.m2`** (standard dev-machine semantics); the read-only-base + per-project **overlay is deferred** |
| #27 tools persistence | Implemented — `/opt/tools` per-project mount + PATH + runtime dir + `docs/tool-persistence.md` |
| #29 mobile WebSocket | Implemented — server ping/deadline + client reconnect/backoff + visibility/online |
| trust handling | Implemented — idempotent force-set of trust keys in `entrypoint.sh` (jq filter exercised against a stale-cache sample) |

## The governing architectural fact

The per-project cache is bind-mounted as the container's entire home: **`${CACHE_DIR}:/home/claude`** (`docker-compose.yml:7`). This one fact drives the storage theme:

- It **shadows** anything baked into the image's `/home/claude` (e.g. the `dev` stage's SDKMAN at `/home/claude/.sdkman` is masked at runtime by the empty cache dir).
- The caches we want to *share* live under it, **per-project**: Claude versions at `~/.local/share/claude/versions` (#37), Maven's repo at `~/.m2` (#21) — so every project re-downloads them.

**Design rule (decided):** shared caches are **host bind-mounts** under `<DataDir>/shared/…`, mounted at the relevant **subpath under `/home/claude`** so they aren't masked (nested mounts), or relocated outside HOME (the `/opt/claude` pattern the Claude binary already uses). Host-visible and inspectable, consistent with how `/opt/skills` already works.

## Per-workstream plans

### A. Dockerfile layer efficiency (#51) — do first; foundational, low-risk

**Finding.** The daedalus-owned artifacts (`skill-catalog-mcp`, `project-mgmt-mcp`, `daedalus-runner`, `claude.json`, `entrypoint.sh`) are `COPY`'d in the **`base` stage (Dockerfile:28-37)** — upstream of every heavy download in `dev`/`godot`/`copilot-*`. `build.sh` rewrites them every build, so each version bump busts the cache for the Go tarball, SDKMAN's JDK/Maven/Kotlin, the Godot zip, and Copilot. The Claude and Copilot installers are unpinned `curl | bash` (no version, no checksum).

**Plan.**
1. Move the daedalus-artifact `COPY`s to the **end of each leaf stage** (or a thin final overlay layer after downloads), so toolchain layers stay cached when only our binaries change.
2. **Version-pin** the Claude and Copilot installers (see #55/supply-chain note), so a cache bust can't silently swap agent versions.
3. Optional: apt/SDKMAN `RUN --mount=type=cache` for faster cold builds.

Risk: low (build-only). Effort: small–medium.

### B. Shared Claude versions (#37) + shared Maven `.m2` (#21) — same mechanism; #37 first

**Finding.** Both caches live inside the per-project HOME mount today, so N projects = N copies. #37 is pure disk waste. #21 wants an overlay: a stable read-only shared base + a per-container writable local repo, so shared artifacts are reused without cross-project pollution.

**Plan.**
- **#37:** shared bind-mount `<DataDir>/shared/claude-versions` → `/home/claude/.local/share/claude/versions` (nested subpath under HOME).
- **#21:** shared bind-mount `<DataDir>/shared/m2` as a read-only base + per-project overlay, using Maven's native split (`settings.xml` `<localRepository>` layering / `-Dmaven.repo.local`) rather than a Docker-layer trick.
- **Both must be wired into the coordinator start command** (`coordinator.go:176-183`), not `BuildExtraArgs` — see #55.

Ship #37 first (simpler). Risk: medium (path-shadowing; concurrent-writer safety on the shared repo). Effort: medium.

### C. Tooling decouple + persistence (#27) — Option B (scoped volume, reproducible base)

**Finding.** Containers are fully ephemeral: the coordinator omits `--rm` but `docker stop && docker rm`s on stop (`coordinator.go:291,298`), destroying the writable layer. Only `/workspace` and `/home/claude` survive. System-level tool installs (`apt`, `/usr/local`, Go toolchain) are lost on restart.

**Decision: Option B — volumes, not `docker commit` snapshots.** Snapshotting the whole container would fight the vision's "isolation first / reproducible images" principle (turns each project into an un-rebuildable pet). Instead:
- Keep base images minimal.
- Add a **per-project persistent tools mount**: host `<DataDir>/<project>/tools` → container `/opt/tools`, with `/opt/tools/bin` on `PATH`.
- Document an install convention so runtime tools land in that prefix (user-space installs). **System `apt` packages remain ephemeral by design** — a project that truly needs one declares it in a build stage, not a snapshot.
- Wire the mount into the coordinator start path (see #55).

Risk: medium (convention + PATH setup; `apt` non-persistence is a documented trade-off). Effort: large; slot after the storage theme.

### D. Mobile WebSocket stability (#29) — additive; parallelizable

**Finding.** The web terminal has **no** keepalive (no ping/pong, no read/write deadlines), **no** client reconnect, and **no** mobile-lifecycle handling (`terminal.go`, `terminal.js`). A Wi-Fi↔cellular handoff or backgrounded tab silently half-opens the socket; the client prints `[Connection closed]` and stays dead. **But** the server-side session survives a dropped browser socket in all three relay modes (runner `Detach`, tmux detach, PTY SIGHUP), and replay primitives already exist (runner hello-frame scrollback; control-mode `live-capture`). So this is purely additive.

**Plan (four additive pieces).**
1. **Server ping + read deadline** on `safeConn` (gorilla `SetPongHandler` + periodic `WriteControl(PingMessage)`), so half-open sockets are detected.
2. **Client auto-reconnect** with exponential backoff on `onclose`, gated by an **intentional-close flag** so deliberate `disconnectTerminal()` navigation isn't treated as a drop.
3. **Repaint on reconnect** by reusing existing replay (re-send `live-capture` / rely on runner hello replay) — no new server work.
4. **`visibilitychange` + `online` listeners** to proactively re-dial when a backgrounded tab returns or the network changes.

Risk: low–medium (needs real-device testing — joins the interactive-UI verification pattern). Effort: medium. Highest user-visible payoff of M5.

### E. Automatic trust-prompt handling — mostly done by Sprint 41; small tail

**Finding.** Sprint 41's Layer 1 pre-seeds `hasTrustDialogAccepted` (`claude.json` → copied write-once by `entrypoint.sh:10-16`); Layer 2 repaints the prompt on attach. Two residual gaps: (1) the seed is **write-once** — an older project cache never gets the trust keys retro-added (the entrypoint jq merge only patches `mcpServers`); (2) nothing programmatically *answers* the prompt if it fires.

**Plan.** Extend the `entrypoint.sh` jq patch to **idempotently force-set** the trust/onboarding keys on every boot. Optionally detect + auto-accept the dialog in the runner adapter (the container is already the trust boundary). Risk: low. Effort: small. Fold into whichever sprint touches the container/entrypoint.

## Sequencing

```
Sprint α (storage foundation):   #51 Dockerfile layers  →  #37 Claude versions
Sprint β (storage, harder):      #21 Maven .m2 overlay   +  #55 coordinator-mount gap
Sprint γ (resilience):           #29 mobile WebSocket     +  trust-prompt tail (E)
After storage theme:             #27 tools volume (Option B)
```

Rationale: #51 first (speeds every later Docker cycle + fixes installer pinning); #37 before #21 (same mechanism, simpler); #55 rides β since the shared mounts must land in the coordinator path anyway; #29 is independent; #27 slots after the storage volume work is proven.

## Cross-cutting concerns

- **Testing is Docker-heavy.** Most of M5 (#21/#27/#37/#51, trust) can't be unit-tested — it needs real `docker build`/`run`. Apply the "green-locally ≠ green-in-container" discipline; plan manual/e2e verification. #29 needs real-device testing.
- **Vision tensions held:** "single binary, no runtime downloads" applies to the daedalus binary (unaffected); "isolation first / reproducible images" is why #27 is Option B, not snapshots; "don't modify host fs outside the project dir" is already relaxed for the data dir, so shared caches under `<DataDir>/shared` fit the precedent.

## Prerequisite finding

**#55 (coordinator missing mounts).** The coordinator (now-default runner) start path does not call `BuildExtraArgs` and mounts only `/workspace` + `/home/claude`; the `/opt/skills` (skill catalog) and `/workspace/.daedalus` (project-mgmt progress) mounts present on the legacy path are absent. Every shared-volume mount in this milestone (#37, #21, #27) must be wired into the coordinator command — and the missing skills/progress mounts likely need restoring regardless. Filed separately; a prerequisite for the storage theme.
