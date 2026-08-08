# Sprints

## Current Sprint

### Sprint 58: Governance Core — Budgets, Rejection, Retry/Replan & the Event Log

Goal: make the control plane *governed* — it can now say **no**. Add a per-Task **budget** enforced host-side on the axes that are genuinely enforceable (wall-clock, max-attempts, review-cycles, concurrency), **typed rejection** with machine-readable reasons (over-budget, stale base), **retry/replan** out of `rejected` with a preserved Job chain, and the **control-plane-managed event log** (`daedalus task events`) — named honestly, immutable through the API rather than cryptographically tamper-proof. Pure Go + git, fully host-testable without Docker. Design in `docs/guild-master-plan.md` (M15, V2) + §6. Out of scope: the integration transaction + human approval (Sprint 59) and `guild-control-mcp` (Sprint 60).

Milestone: 15

| # | Item | Status |
|---|------|--------|
| 1 | **Budget model + host-side enforcement** — a per-Task budget (wall-clock seconds, max-attempts, max-review-cycles, concurrency) captured at create and stored authoritatively; the plane enforces the strongly-enforceable axes itself (wall-clock kills a running Job → `execution_result=timeout`; max-attempts blocks a further dispatch). Turn/token/cost remain *policy in the plane*, explicitly documented as runner-dependent measurement, not enforced. Defaults are per-project-overridable. Tests over the enforcement decisions, no Docker. |  |
| 2 | **Typed request rejection** — the plane can say no, with a machine-readable reason: over-budget (dispatch beyond max-attempts / budget), and **stale base** (a candidate Artifact whose `base_sha` is no longer the project's target tip → rejected, must rebase + re-verify). A `RejectionReason` enum surfaced through the API + CLI exit codes, so a client can distinguish 'refused by policy' from 'failed'. Tests against real temp repos for the stale-base detection. |  |
| 3 | **Retry / replan from `rejected`** — `daedalus task retry <id>` (rejected → queued, a fresh Job, attempt counter incremented, budget re-checked) and `daedalus task replan <id> --objective` (rejected → planned with a revised objective). Attempt history is preserved — never overwritten — so a Task carries its full Job chain. Enforced against max-attempts. Tests for both paths, including the exhausted-attempts refusal. |  |
| 4 | **Control-plane-managed event log + `daedalus task events`** — every transition, budget decision, rejection and verification outcome recorded as a typed event with actor (human/plane/worker), immutable *through the API* (no update/delete op exists); a `daedalus task events <id>` view. Named honestly in the docs: control-plane-managed, NOT cryptographically tamper-proof (hash-chaining stays an optional later property). Tests assert the API exposes no mutation path. CHANGELOG. |  |

## Sprint History

### Sprint 57: The Clean Verifier Container (v0.49.0)

Goal: replace the stub with the **real clean verifier** — the piece that makes `candidate → verified` mean something. The control plane checks out the Artifact's `head_sha` into a **fresh, digest-pinned** project container (no worker mutable state), runs the frozen policy's `checks`, and reports pass/fail; plus the **`sha256:` image-digest pin**, an explicit **network/creds/`/opt/tools` verifier policy**, and a **null-agent floor** check. Closes Milestone 14. The verifier *logic* is host-testable (checkout + run behind the existing `VerifyRunner` interface, with a fake); the actual container run is host-only, as ever. Design in `docs/guild-master-plan.md` (M14) + §6.

Milestone: 14

| # | Item | Status |
|---|------|--------|
| 1 | **Clean verifier container (real `VerifyRunner`)** — a host-only adapter that checks out the Artifact's `head_sha` into a **fresh, clean** checkout and runs the frozen `policy.checks` inside a container built from the project's image (never the worker's worktree/env); returns pass + per-check detail. Behind the Sprint-56 `VerifyRunner` interface; host-tested with a fake, container run host-only. | Done |
| 2 | **Image digest pinning** — capture the project image's `sha256:` digest (at task create or first verify) and run the verifier against **that digest**, not a mutable tag, so the artifact is verified in the same environment it was authored against. Recorded on the task/job; tests over the capture/compare logic. | Done |
| 3 | **Verifier environment policy + null-agent floor** — an explicit, documented policy for the verifier container (network off/allowlisted, no ambient credentials, no inherited `/opt/tools`); a **null-agent floor** check (an empty/no-op change must NOT verify as "done") so a vacuous pass is caught. Tests for the policy plumbing + the floor. | Done |
| 4 | **Docs + close** — `docs/control-plane.md` (verifier section: clean checkout, digest pin, env policy, "reproducible verification result, not proof of correctness"); a `verify` phase in `scripts/verify-m13.sh` (or a sibling) for the host seam; CHANGELOG; `CGO_ENABLED=0` build + suite green; close Milestone 14. | Done |

Out of scope (Milestone 15, V2): budgets + request rejection, the race-safe integration transaction (rebase → re-verify merged → CAS), human approval + integration, the independent reviewer pass, and the `guild-control-mcp` Guild Master client. Parallel Jobs are M16.


### Sprint 56: Acceptance Contract & the Test-Integrity Gate (v0.49.0)

Goal: the host-testable half of independent verification — everything except the container itself. Define the **`daedalus verify` acceptance contract** (how a project declares its check), **freeze + hash the acceptance policy at `base_sha`** when a Task is created (so a worker can't weaken the check it must pass), add the **test-integrity gate** (reject any Job whose diff touches the frozen test/acceptance files — the cheap, high-value defence against the 30–100% test-gaming rates), and wire the plane-owned `candidate → verifying → verified | rejected` transitions behind an injectable `VerifyRunner` (a stub now; the real clean verifier container is Sprint 57). Pure Go + git-diff logic, **fully host-testable without Docker**. Design in `docs/guild-master-plan.md` (M14, V1) + §6. Milestone 14.

Milestone: 14

| # | Item | Status |
|---|------|--------|
| 1 | **`daedalus verify` acceptance contract** — decide + implement how a project declares its verify policy (e.g. a `verify:` list in config / a `.daedalus/verify` script / documented convention) and which paths count as the frozen **acceptance/test files**. A pure reader that returns the policy + the acceptance-file globs from a checkout. Tests. | Done |
| 2 | **Freeze `acceptance_policy@base_sha`** — at Task create, capture the policy from the task's `base_sha` and store a **hash** on the Task; a proposed policy change affects only future tasks. Tests assert the frozen hash is stable and independent of later working-tree edits. | Done |
| 3 | **Test-integrity gate (pure)** — given a Job's `base_sha..head_sha` diff and the frozen acceptance-file globs, **reject** (→ `rejected`) any Job whose diff touches those files; otherwise allow it to proceed to verification. Host-tested against real temp repos (touches test file → rejected; touches only src → allowed). | Done |
| 4 | **Plane-owned verify transitions + CLI** — wire `candidate → verifying → verified | rejected` into the control plane behind an injectable `VerifyRunner` interface (stub returns pass/fail for tests); `daedalus task verify <id>` drives it; `rejected → queued/planned` for retry. **Only the plane** performs `candidate → verified` (already structural). Tests; CHANGELOG. | Done |

Out of scope (Sprint 57): the real **clean verifier container** (checkout the Artifact commit into a fresh container from the **digest-pinned** project image + run the policy), the network/creds/`/opt/tools` policy, the null-agent floor, and the M14 close. Governance/integration/approval + the Guild Master client are Milestone 15.

### Sprint 55: Execution — daemon, worktree, headless Jobs & reconciliation (v0.48.0)

Goal: make the control plane *run work*. Stand up the `daedalus-control` daemon over `control.sock` (the `daedalus task` CLI becomes its client); dispatch a Task as a **Job in an isolated Git worktree** checked out clean at `base_sha`; run the agent **headless** via the coordinator, take **process exit** as the boundary, classify `execution_result`, capture the commit as `output_snapshot`, and promote **only success → candidate Artifact**; and add a **reconcile-on-boot + periodic loop** so state survives crashes. Completes Milestone 13. The daemon/worktree/reconcile *logic* is host-testable (coordinator behind an interface, real temp git repos); the actual container run is host-only. Design in `docs/guild-master-plan.md` (M13, V1); heed the critique's §4 (reconciliation) + §5/§6 (job-end, capture-vs-success).

Milestone: 13

| # | Item | Status |
|---|------|--------|
| 1 | **`daedalus-control` daemon + `control.sock`** — a long-running daemon that owns the SQLite store and an HTTP-over-UDS API (create/list/status/cancel/dispatch), with `EnsureRunning`/stale-detection modelled on `internal/coordinator`. The `daedalus task` CLI (Sprint 54) becomes a **thin client** of it. Tests over the API. | Done |
| 2 | **Isolated Git worktree per Job (the Job wrapper)** — `git worktree add` a clean checkout at `base_sha` on branch `daedalus/<task>/<job>`, at a deterministic path under `<DataDir>` (never the developer's checkout); cleanup on terminal. Pure git ops, host-tested against real temp repos. | Done |
| 3 | **Headless Job execution** — `daedalus task dispatch <id>` → the daemon creates a Job + worktree, runs the agent **headless** via the coordinator with the worktree as `/workspace`, waits for **process exit**, sets `execution_result` ∈ {success,failed,timeout,cancelled}, captures `head_sha` as `output_snapshot`, and promotes **only success → candidate**. Coordinator behind an injectable interface so the logic is host-tested; the real run is host-only. | Done |
| 4 | **Reconcile-on-boot + periodic loop + close** — on daemon start (and on a tick), for every non-terminal Job compare desired (DB) vs observed (worktree/coordinator session) state and drive/repair (adopt, resume, or fail orphans) with idempotent, deterministically-named side-effects; tested with a fake coordinator. CHANGELOG; verify; close Milestone 13. | Done |

Out of scope: the clean verifier performing `candidate → verified` + digest-pinning + the test-integrity gate (Milestone 14); budgets, the integration transaction, human approval, and the `guild-control-mcp` Guild Master client (Milestone 15); parallel Jobs (Milestone 16). One active Job per project still holds.

### Sprint 54: Control-Plane Core — model, store & the `daedalus task` CLI (v0.48.0)

Goal: lay the foundation of the host-side control plane — the Task/Job/Artifact data model, a SQLite store as the durable source of *desired* state, and a human `daedalus task` CLI as the first (and, for now, only) client. **No execution yet** — Sprint 55 adds the worktree, headless Job run, and reconciliation. This half is pure Go + SQLite, **fully host-testable without Docker or Git**. Design in `docs/guild-master-plan.md` (M13, V1); the CLI-first ordering is deliberate (a deterministic reference path, useful at N=1, before any agent client). Milestone 13.

Milestone: 13

| # | Item | Status |
|---|------|--------|
| 1 | **Core model + state machine (`internal/control`)** — pure-Go `Task` (project, objective, acceptance ref, base_sha) / `Job` (one attempt: base_sha, runner, budget, `execution_result`) / `Artifact` (head_sha, branch, verify/review status) types + the state machine (states + the legal transitions from `docs/guild-master-plan.md` §5), with the worker-can-only-reach-`candidate` / plane-owns-`verified` rule encoded. Unit-tested. | Done |
| 2 | **SQLite store (pure-Go, CGO-free)** — `modernc.org/sqlite` (must build under `CGO_ENABLED=0`) at `<DataDir>/control.db`; schema `tasks`/`jobs`/`artifacts`/`events`; open+migrate; CRUD + **atomic single-row state transitions** that reject illegal moves; an append-only `events` log write on every transition. SQLite holds *desired* state only. Tests over a temp DB. | Done |
| 3 | **`daedalus task` CLI (the reference client)** — `create` / `list` / `status <id>` / `cancel <id>` driving the store in-process (the daemon + `control.sock` are Sprint 55). Git-native + one-active-Job-per-project invariants enforced at create/dispatch. Wire into dispatch + `usage.go` + `--help` + completions. Tests. | Done |
| 4 | **Docs + guardrails** — CHANGELOG `[Unreleased]`; a short `docs/control-plane.md` stub describing the model + the V1 scope boundary (no execution/agent client yet); `go build`/`vet`/suite green; `gofmt` clean. | Done |

Out of scope (Sprint 55): the `daedalus-control` daemon + `control.sock`; the isolated Git worktree; headless Job execution via the coordinator; the reconcile-on-boot/periodic loop; `task dispatch`. Verification (the clean verifier) is Milestone 14; the Guild Master client is Milestone 15.

### Sprint 53: Cross-Project Document Access (v0.47.0)

Goal: give the Guild Master's agent read visibility across every project — read-only mounts of each project's directory + a `guild-mcp` server that enumerates and reads them — then close Milestone 12. The mount-arg builder and the MCP doc logic are pure host-side Go, fully testable; the container run itself is host-only. Design in ROADMAP M12.

Milestone: 12

| # | Item | Status |
|---|------|--------|
| 1 | **Read-only cross-project mount builder (core)** — a pure function that, for the guild-master project ONLY, emits read-only bind-mount args mounting every *other* registered project's directory at `/guild/<name>` (skip guild-master itself + missing dirs; sanitise the mount name). Wire into the coordinator/launch arg-building for that project. Unit tests over a fake registry | Done |
| 2 | **`cmd/guild-mcp` server** — MCP tools over the mounted `/guild/*` tree: `list_guild_projects` (names + basic parsed state), `read_project_doc(project, doc)` (a named doc's contents), `guild_overview` (per-project parsed milestones/sprints/progress via `core.Parse*`). Robust to missing/half-written docs. Tests over a fixture `/guild` tree | Done |
| 3 | **Scope + role wiring** — build `guild-mcp` into the image and declare it in `claude.json`, **gated so it is active only for the Guild Master** (the `/guild` mount presence / an env from launch). Seed the guild-master workspace with a short role doc (its scaffolded VISION/README or a CLAUDE.md) framing it as the read-only programme overseer. README/ARCHITECTURE note | Done |
| 3b | Keep the Sprint-52 protection + distinguished-hero behaviour intact | Done |
| 4 | **Verify + close** — `go build`/`vet`/`test` green; document the launch-time-mount limitation + the no-control scope + the host-only container bits; CHANGELOG; ship Milestone 12 | Done |

Out of scope: any control/dispatch of other agents (impossible by design — read-only visibility only); a live registry watch (mounts resolve at launch); a TUI cross-project view.

### Sprint 52: The Embedded Guild Master (v0.47.0)

Goal: bring the `guild-master` project into being — always present, un-removable, and launchable like any other project. This is the registry + protection + UI half of Milestone 12; cross-project document access is Sprint 53. Host-side and fully testable without Docker (the container launch itself is host-only, as ever). Design in ROADMAP M12.

Milestone: 12

| # | Item | Status |
|---|------|--------|
| 1 | **Auto-ensured registry entry (core/registry)** — a reserved `guild-master` name + an `EnsureGuildMaster` that creates the entry if missing, with a Daedalus-owned workspace dir (`<DataDir>/guild-master`) scaffolded via `core.ScaffoldDocs` on first create. Idempotent; invoked on the startup paths (CLI launch/list, coordinator, web) so it is always present. Tests | Done |
| 2 | **Removal / rename protection** — `RemoveProject`/`RemoveProjects`/`RenameProject` refuse the guild-master with a clear error (the `persona.go` "cannot remove built-in" precedent); `prune` skips it; the CLI `remove`/`prune` + web/TUI remove paths surface the refusal cleanly. Tests | Done |
| 3 | **Launch parity** — `daedalus guild-master` resolves and launches through the normal runner path (its `<DataDir>/guild-master` workspace at `/workspace`); no special-casing beyond the ensure. Verify the resolution + launch-arg building host-side (the container run is host-only). Tests | Done |
| 4 | **UI presence + a distinguished hero** — appears in `daedalus list` (marked as the built-in manager) and in the Web Guild view as a distinguished hero (a crown / special class ribbon or badge), never offered for deletion. `go build`/`vet` + suite green | Done |

Out of scope (Sprint 53): the cross-project read-only mounts and `guild-mcp` doc-access tools; the guild-master's programme-manager role doc; the milestone close.

### Sprint 51: Activity Fidelity & Party Polish (v0.45.0)

Goal: finish the Guild reforge — surface *what* each busy hero is doing, and make the party screen production-grade (responsive, accessible, graceful edges). All frontend: `/api/guild` already carries `detail`, `lastUsed`, and `sessionCount`. Design in ROADMAP M11.

Milestone: 11

| # | Item | Status |
|---|------|--------|
| 1 | **Action label from `detail`** — when a hero is busy, surface the agent's `detail` (e.g. a tool name) as a themed JRPG action ribbon/speech ("Casting **Edit**", "Reading the runes…"); map common details to flavour, fall back to the raw string. Hidden when idle/sleeping. Reads live on the 3s poll via the diff-update | Done |
| 2 | **Responsive / mobile** — the party roster reflows cleanly on a phone (the app has a 768px breakpoint used elsewhere); heroes scale, frames/gauges stay legible, no horizontal scroll | Done |
| 3 | **Accessibility & graceful edges** — honour `prefers-reduced-motion` (freeze the working/idle loops to a static pose, keep state legible via colour/label); a JRPG-styled empty state ("The guild hall stands empty…") and a first-load state; optional light JRPG stats from `lastUsed`/`sessionCount` (e.g. "Lv" / "last seen") if they fit tastefully | Done |
| 4 | **Docs + close** — CHANGELOG; refresh `guild-preview.html` to include a busy hero with an action label + a reduced-motion note; `go build`/`vet` + `TestHandleGuild` green; ship Milestone 11 | Done |

Out of scope: any new backend activity plumbing (the signal is sufficient); a TUI equivalent; per-archetype bespoke idle poses beyond the shared set.

### Sprint 50: Heroes & Activity Animation (v0.45.0)

Goal: the heart of the Guild reforge — replace the generic colour-permuted "mage" cards with a distinct **Secret-of-Mana-style pixel-art hero per project** whose animation is driven by real activity: **working** when the agent is busy, **at ease** when idle, **resting** when the container is asleep. Rides the existing `GET /api/guild` busy/idle/sleeping signal + 3s polling — no new activity plumbing. Design in ROADMAP M11.

Milestone: 11

| # | Item | Status |
|---|------|--------|
| 1 | **Distinct per-project hero** — deterministic archetype (e.g. knight / mage / archer / sprite) **and** palette from the project name (extend the existing `nameToHue`/`avatarColors` in `guild.js`), rendered as crisp pixel art (CSS box-shadow sprite grid or inline SVG with `image-rendering: pixelated`; self-contained, no external image assets). At least ~4–6 visually distinct archetypes so a programme of projects reads as a varied party | Done |
| 2 | **Activity-driven animation states** — bind the card's animation to `member.activity`: `busy` → an active working/casting loop (tool-swing, spell shimmer), `idle` → a calm idle-breathing/at-ease loop, `sleeping` → a resting loop (the existing ZZZ, dimmed). Smooth CSS transitions between states on the 3s poll; keep the diff-update (no flicker) already in `renderGuildMembers` | Done |
| 3 | **Secret-of-Mana UI framing** — reskin `guild.css`: ornate rounded panel/border frame per hero, retro pixel/serif JRPG type, a Mana-esque palette, the "HP" bar restyled as a proper JRPG gauge (keep it bound to `progressPct`), party-roster header. Cohesive, not garish | Done |
| 4 | **Wire-up + verification aids** — keep `/api/guild` + `showGuildView` polling intact; `go build` + existing `TestHandleGuild` green (extend if the JSON shape changes). Add a self-contained `internal/web/static/guild-preview.html` that renders the three states (busy/idle/sleeping) with mock data so the look can be reviewed without a running container/browser session. `bash`/`go vet` clean | Done |

Out of scope (Sprint 51): surfacing the agent `detail` ("what it's doing") as an action label/effect; responsive/mobile overlay; `prefers-reduced-motion`; empty/loading polish; final close + any backend `detail` enrichment.

### Sprint 49: Side-by-Side Versions & Rollback (v0.44.0)

Goal: a new install lands *alongside* the current one instead of clobbering it, and switching or rolling back is one command — so a user can try a new version and fall back if it misbehaves. Builds directly on Sprint 48's single archive. Design in ROADMAP M9 (#9).

Milestone: 9

| # | Item | Status |
|---|------|--------|
| 1 | **Versioned install layout (`setup.sh`)** — install into `$PREFIX/versions/<version>/`; maintain `$PREFIX/current` → the active version and point the PATH symlink at `current/daedalus`. Upgrades keep prior versions. Transparently **migrate a legacy flat install** into `versions/<old>/` on the first versioned upgrade; uninstall handles the new layout. `bash -n`; `scripts/test-install.sh` extended | Done |
| 2 | **`daedalus version` subcommand (Go)** — `list` (installed versions, marking current), `use <version>` (repoint `current` + PATH symlink, recording the prior as previous), `rollback` (switch back to the previously-active version). Derives the install prefix from the running binary (`os.Executable`), `DAEDALUS_PREFIX` override for tests. Wire into dispatch + usage + `--help` + completions; unit tests over a fake prefix | Done |
| 3 | **Prune + safety** — `daedalus version prune [--keep N]` removes old versions keeping the last N + current; refuse to remove the current/active version; clear errors on unknown/again-current version. Tests | Done |
| 4 | **Docs + close** — README (versioned install + switch/rollback/prune), CHANGELOG; extended local simulation (install v1 → install v2 alongside → `list`/`use`/`rollback`/`prune` → uninstall, no GitHub/Docker); verify; close Milestone 9 + release | Done |

Out of scope: a web/TUI version switcher; auto-pruning on install (explicit `prune` only); cross-machine/remote version sync.

### Sprint 48: Single Bundled Release Archive (v0.44.0)

Goal: replace the ~27 individual release assets with one self-contained per-platform archive plus a checksums file, and make `install.sh` download and verify a single archive instead of curling each file. The packaging logic is factored into a script both the release workflow and a local simulation call, so the whole chain is verifiable here without GitHub or Docker. Foundation for side-by-side installs (M9 #9) and the Homebrew formula (M10 #11). Design in ROADMAP M9 (#8).

Milestone: 9

| # | Item | Status |
|---|------|--------|
| 1 | **`scripts/package-release.sh` — the single packaging source of truth** — given a staging dir of built binaries + runtime files and a version, produce per-platform `daedalus-<os>-<arch>.tar.gz` + a `SHA256SUMS.txt`. `bash -n` clean; deterministic layout | Done |
| 2 | **`release.yml` calls the script** — replace the inline asset-prep (`cp`/`sed`) with `scripts/package-release.sh`; upload the archives + `SHA256SUMS.txt` + `install.sh` (still curl'd raw from master). Keep the changelog body + install one-liner intact | Done |
| 3 | **`install.sh` pulls one archive** — detect OS/arch, download the single `daedalus-<os>-<arch>.tar.gz` + `SHA256SUMS.txt`, verify the checksum, extract into `WORK_DIR`, then exec `setup.sh` — replacing the per-file curl sequence. Preserve `--prefix`/link flags + version patching; clear error if the archive/checksum is missing or mismatched | Done |
| 4 | **Local end-to-end simulation + docs + close** — `scripts/test-release-bundle.sh`: build host binaries → `package-release.sh` → run `install`/`setup` from the archive into a temp prefix, asserting binaries + runtime files land and the symlink works (no GitHub, no Docker). `bash -n` on every touched script. CHANGELOG; README/CONTRIBUTING install notes; ship | Done |

Out of scope: side-by-side versioned installs (#9, next sprint) and the Homebrew formula (M10); keeping the old individual-file assets as a parallel/back-compat path (new installs use the archive — older `install.sh` copies pin their own tag).

### Sprint 47: First-Run Onboarding & Value Proposition (v0.43.0)

Goal: close the "installed — now what?" gap. A new user gets a clear first step after install, and the README + first-run messaging say what Daedalus is and why in one breath. The messaging half of Milestone 8 (#45, #46), building on Sprint 46's doc-bootstrap backbone (`daedalus docs scaffold`). Design in ROADMAP M8.

Milestone: 8

| # | Item | Status |
|---|------|--------|
| 1 | **`daedalus init`** — a first-step command that carries a new user from install to first session: getting-started guidance (register a project, scaffold docs, start a session) with next-step hints; optionally bootstraps the current directory's docs via `core.ScaffoldDocs`. Host-side, testable (#45) | Done |
| 2 | **Post-install next-steps** — `install.sh` prints a clear getting-started pointer on a successful install (what to run next), pointing at `daedalus init` (#45) | Done |
| 3 | **Sharpen the value proposition (#46)** — tighten the README opening + first-run / `--help` messaging to the "hands-off AI coding in a safe container" pitch; say what it is and why in one breath | Done |
| 4 | **Docs + close** — CHANGELOG; usage/help; verify; close Milestone 8 + release | Done |

Out of scope: any in-container/first-session UX that needs a running Docker daemon (host-side onboarding only); a GUI wizard.

### Sprint 46: Bootstrap Conformant Project Docs (v0.43.0)

Goal: turn "read `docs/PROJECT-INIT.md` and hope" into one command — `daedalus docs scaffold [dir]` writes the required-doc skeletons (`core.RequiredDocs()`), already conformant to the structured-docs contract so `daedalus docs lint` passes on a fresh project. The concrete first step of Milestone 8's "install → first productive session" arc: a new project starts with a valid roadmap arc instead of an empty tree. Design in ROADMAP M8 (#54).

Milestone: 8

| # | Item | Status |
|---|------|--------|
| 1 | **`core.ScaffoldDocs(dir, force)` (core)** — write the 8 `RequiredDocs()` skeletons from templates; skip existing files unless `force`; return created/skipped lists. Tests assert the output parses and `ValidateDocs` is clean (one In-Progress milestone stub + a current-sprint stub linked to it). | Done |
| 2 | **Single source of truth for templates** — factor the skeletons so `docs/PROJECT-INIT.md` and the scaffolder can't drift; the ROADMAP/SPRINTS stubs must satisfy the same strict-format checks `docs lint` enforces. Tests | Done |
| 3 | **`daedalus docs scaffold [dir] [--force]` (CLI)** — wire into `manageDocs` beside `lint`; default to cwd; print a created/skipped summary; usage + `docs help` text | Done |
| 4 | **Docs + close** — document `docs scaffold` in README + `docs/structured-docs.md` (sibling of `docs lint`); CHANGELOG; verify `docs lint` passes on freshly scaffolded output; ship | Done |

### Sprint 45: Project-Management MCP Tools & File-Derived State (v0.42.0)

Goal: give the in-container agent MCP tools to manage the roadmap lifecycle (add/move/remove/start/finish/pause milestones & sprints), and derive project *read* state from files — replacing the unreliable self-report write tools. daedalus offers and validates its own writes; it does not gate the agent (it launches the CLI, it is not the agent's harness). Design in ROADMAP M7.

Milestone: 7

| # | Item | Status |
|---|------|--------|
| 1 | **`Paused` lifecycle state (core)** — add `StatusPaused` to `core/status.go`; recognize `(Paused)` in milestone/sprint headers and a `Paused` item cell in `ParseMilestones`/`ParseSprints`; extend `PhaseOf` + `ValidateDocs`. Tests | Done |
| 2 | **Structured doc writer (core) — the crux** — surgical, prose-preserving edits to `ROADMAP.md`/`SPRINTS.md`: `SetMilestoneStatus`, `AddMilestone`, `RemoveMilestone`, `AddSprint`, `SetSprintStatus`, `MoveSprint`, `RemoveSprint`, `RollSprintToHistory`. Round-trip + prose-preservation tests | Done |
| 3 | **Invariant validation on write (core)** — refuse edits producing an inconsistent roadmap (>1 In Progress milestone; current sprint not linked to it; finish a sprint with unfinished items unless forced; finish a milestone with an open sprint). Reuses `ValidateDocs`. Tests | Done |
| 4 | **Lifecycle MCP tools (`cmd/project-mgmt-mcp`)** — `add/remove/move/start/finish/pause_milestone` + `…_sprint`, each reading `ROADMAP.md`/`SPRINTS.md`, applying the #2 mutation, writing back, returning new state or a structured error. Tests; register in the server (+ `internal/mcpclient` if consumed) | Done |
| 5 | **File-derived read state (#52)** — derive vision (`VISION.md`), version (`VERSION`), progress (sprint item statuses → %) host-side; remove the self-report write tools (`report_progress`/`set_vision`/`set_version`). Point host readers at the derived source. Tests | Done |
| 6 | **Agent guidance + docs + close** — tool descriptions as agent guidance; document tools + `Paused` in `docs/structured-docs.md` (+ README); CHANGELOG; dogfood; close M7 + release | Done |

Out of scope (deferred): any enforcement/gating of the agent (impossible — capability only); a human-facing CLI for these tools; a web reordering UI; the #47/#48 coordinator-staleness work.

### Sprint 44: Sidebar Sprint Pipeline (v0.41.0)

Goal: surface the active milestone and its sprints in the session sidebar, framed by **ship-pipeline state** (Building → Ready → Shipped, + optional Proposed) rather than calendar time — the light version of the agentic reframe (keep the word "sprint"; derive phase from existing fields; make the verify/ship gate first-class). Design in ROADMAP M6.

Milestone: 6

| # | Item | Status |
|---|------|--------|
| 1 | `core.PhaseOf(Sprint) SprintPhase` (Shipped / Ready / Building / Proposed) derived from `Version` + item statuses, plus a done/total progress helper, with tests | Done |
| 2 | `GET /api/projects/{name}/milestone-sprints` — the active (In Progress) milestone + its sprints with derived phase and progress; handler + tests | Done |
| 3 | Sidebar "Sprints" section (`#docs-sidebar`) — markup, `loadMilestoneSprints`, renderer, phase-badge CSS; wired into `loadSidebar`. Order: Building, Ready (accented), Proposed, Shipped (dimmed) | Done |
| 4 | Optional: parse a `Status: Planned` sprint / `## Planned Sprints` section so the **Proposed** bucket can be non-empty (defer-able) | Done |
| 5 | Docs: define M6 (ROADMAP, In Progress), open Sprint 44 (SPRINTS, `Milestone: 6`), note the phase model in `docs/structured-docs.md` | Done |

Out of scope (deferred): the full sprint → batch/increment vocabulary rename; a rich planned-sprint planning UI; a mobile sprints overlay.

### Sprint 43: Milestone 5 Verification & Hardening (v0.40.0)

Goal: verify the Milestone 5 implementation on real Docker + a device — it was built in a container with no daemon or browser, so every image/container/volume/mobile behaviour is code-complete but unverified — fix what breaks, close the deferred pieces, and finish the Milestone 4 tail. Step-by-step in `docs/m5-verification.md`; design in `docs/milestone-5-plan.md`.

Milestone: 5

| # | Item | Status |
|---|------|--------|
| 1 | Build every Dockerfile target on real Docker (#51 restructure) and confirm the cache win — a Daedalus-binary change no longer busts the toolchain download layers. **Done 2026-08-04**: all six targets (base/utils/dev/godot/copilot-base/copilot-dev) build on a real host; cache win confirmed (a binary touch reused 20 cached layers, only the final COPYs re-ran) via `scripts/verify-m5.sh build`. | Done |
| 2 | Runtime verification on a real project: #55 skills/`.daedalus` mounts present; #37 shared Claude versions + #21 shared `.m2` populate under `<DataDir>/shared/`; #27 `/opt/tools` binary survives a stop/restart. **Watch the uid/permission assumption** (the top risk). **In progress 2026-08-04**: the top risk is now instrumented — daedalus records the build uid (`<DataDir>/build-uid`) and the coordinator logs a clear warning when it runs as a different uid (the exact "Permission denied" cause), turning the cryptic failure into a diagnosis + fix. Runbook §2 (`docs/m5-verification.md`) made executable (copy-paste inspect/exec/writability snippets). **Done 2026-08-04**: verified on a real host via `scripts/verify-m5.sh mounts` — uid preflight clean (build=run=container=1000), all five mounts present + writable, #27 tool survived a stop/restart. | Done |
| 3 | Trust idempotency — confirm an older project cache no longer fires the "trust this folder?" dialog. **In progress 2026-08-03**: the entrypoint force-set is regression-tested (`scripts/test-trust-idempotency.sh`, wired into CI — old-cache fixtures assert the trust keys are forced true, MCP servers merged, user data preserved, idempotent) and hardened non-fatal so a malformed cache can't crash startup under `set -e`. **Done 2026-08-04**: verified on a real host — no "trust this folder?" dialog on a fresh attach or after an old-cache (trust keys dropped) restart. | Done |
| 4 | Mobile #29 on a phone — reconnect + repaint on a backgrounded tab and a Wi-Fi/cellular switch. **Done 2026-08-03**: verified on a real device; mobile web session confirmed end-to-end (terminal touch-scroll, milestones overlay). | Done |
| 5 | Close deferral: pin the Claude/Copilot installers to a version + checksum (the `TODO(#51)` markers; supply-chain). **In progress 2026-08-04**: both installers pinned via Dockerfile build args (`CLAUDE_VERSION=2.1.221`, `COPILOT_VERSION=v1.0.78`) instead of unpinned `latest`; both installers checksum-verify the downloaded binary (Claude vs. the release `manifest.json`, Copilot vs. `SHA256SUMS.txt`). **Done 2026-08-04**: confirmed on a real build — `claude --version` = 2.1.221, `copilot --version` = 1.0.78. | Done |
| 6 | Close deferral if needed: Maven read-only-base + per-project overlay (#21), should the simple shared `.m2` show cross-project pollution. **Closed 2026-08-04 — no action**: a shared writable `.m2` is standard, safe practice (immutable coordinate-keyed artifacts); no overlay built. Revisit only if pollution is observed. | Done |
| 7 | Retire the classic tmux launch path — the Milestone 4 cleanup tail — once the runner default is proven. **Done 2026-08-01**: runner path is the only launch path; `internal/session`, the `DAEDALUS_USE_TMUX`/`DAEDALUS_USE_RUNNER` toggle, the `--no-tmux` flag + `no-tmux`/`tmux-prefix` config, and the Web tmux/control relays are removed. | Done |

All items done. Items 1–5 and 7 were verified on a real Docker host + a phone (via `scripts/verify-m5.sh` and `docs/m5-verification.md`); item 6 (Maven overlay) closed with no action. Milestone 5 complete; shipped as **v0.40.0**.

### Sprint 41: Trust-Prompt & Runner Terminal Fidelity (v0.40.0)

Goal: close the Web-UI-hangs-on-trust-prompt gap (Backlog #38) that blocks making the runner path the default. Two layers — (1) eliminate the redundant workspace-trust prompt inside the container (the container is already the trust boundary); (2) make the runner relay robust to any early one-shot full-screen prompt via initial PTY sizing and repaint-on-attach — then flip the runner path to default and retire the tmux launch path. Milestone 4 endgame.

Milestone: 4

| # | Item | Status |
|---|------|--------|
| 1 | Layer 1 — pre-seed workspace trust in the default `claude.json` (`projects["/workspace"].hasTrustDialogAccepted`) so Claude's "trust this folder?" dialog never fires inside the container | Done |
| 2 | Layer 2a — initial PTY sizing in `daedalus-runner`: size the PTY at startup (default 80×24, `--cols`/`--rows`) instead of creack/pty's 0×0 default, routed through the hub, with unit tests | Done |
| 3 | Layer 2b — repaint-on-attach: reconstruct the current screen on attach so one-shot dialogs render for late/second/same-size viewers. Delivered as **smart replay-from-boundary** (`ScreenSnapshot` replays scrollback from the last screen boundary) rather than a SIGWINCH nudge; see `docs/runner-repaint-design.md#decision-sprint-41` | Done |
| 4 | End-to-end verification — automatable half (`cmd/daedalus-runner/repaint_e2e_test.go` + `e2e/run-repaint.sh`, green) plus the manual real-Docker + Claude parity pass (`e2e/runner-parity-runbook.md`). **Passed 2026-07-27:** the trust dialog reconstructs on every attach tested — primary render (T1), a second CLI client at a different size (Layer 2a), and a clean same-size detach/reattach (Layer 2b snapshot), and the **Web painted the live prompt instead of hanging (#38 resolved)**. No blank/stale in any case. Residuals below. | Done |
| 5 | Flip the runner path to default and retire the tmux launch path. **Flip landed 2026-07-27** (`669b425`, `core.UseRunner()`: runner is the default, opt out with `DAEDALUS_USE_TMUX=1`; legacy `DAEDALUS_USE_RUNNER=1` still honored). The tmux-launch-path retirement was spun out to Sprint 43 (item 7), deferred until the runner default proves out. | Done |

**Runner-path hardening (surfaced while dogfooding the parity pass).** A chain of integration bugs that made the runner path unusable end-to-end, now fixed: the Web couldn't start runner sessions (`feat/web-runner-autostart`); `docker compose run --rm --detach` removed the container before the runner bound its socket — the root cause of the "socket did not appear" failures (`fix/coordinator-drop-rm-flag`); the CLI's tmux/duplicate guard blocked runner re-attach (`fix/cli-runner-attach-guard`); stale sessions for out-of-band-killed containers were handed back to callers (`fix/coordinator-reap-stale-sessions`); plus stale-container reaping before start, running-container guards, and start/timeout logging so the next failure isn't a silent 30s hang.

**Parity-pass residuals (2026-07-27, low-risk).** The pass verified the trust-prompt surface across Parts A/B/C/E; two things were deliberately not closed and don't block the item-5 flip:
- **Other one-shot surfaces not separately exercised** — the `--resume` picker, login, and copilot startup UIs. Same repaint-on-attach path as the verified trust prompt, so low-risk. Note the picker specifically isn't reachable through daedalus (its `--resume` requires a session id → direct resume, no picker; a picker needs a container shell, Backlog #6).
- **Mobile Enter-submit not cleanly confirmed on a physical phone** — the Web painted and answered fine on desktop, but the phone `\r`-submits assumption behind the mobile-send fix was not definitively exercised on-device. Tracked separately as part of the unverified-web-UI batch.

Also fixed while running the pass: two pre-existing "`./test.sh` as root in the golang container" failures — `TouchProject` tests relied on a `chmod` that root bypasses (now skipped when euid==0), and `TestIntegration_DaemonBinary`'s inner `go build` hit git "dubious ownership" during VCS stamping (now `-buildvcs=false`, matching `build.sh`).

### Sprint 42: Structured Project Documents & Dashboard Journey

Goal: make a project's own markdown the machine-readable source of truth, and turn the per-project dashboard into a file-derived "journey" — Purpose → Arc → Backlog. Ran in parallel with Sprint 41 on `development`; landed but unreleased. Cross-cutting DX/tooling, not tied to a single milestone.

| # | Item | Status |
|---|------|--------|
| 1 | Parse ROADMAP milestones (`core.ParseMilestones`, tri-state, neutral `core.Status`) and link a sprint to its milestone via a `Milestone: N` line in `core.ParseSprints` | Done |
| 2 | Aggregate `GET /api/projects/{name}/overview` (vision + milestones + current sprint + backlog in one fetch) plus `/milestones`; host-side `mcpclient.ReadMilestones` | Done |
| 3 | Cross-file `core.ValidateDocs` (contradictions = errors, information loss = warnings) and raw-text `core.LintHeadings` (catch silently-dropped headings), surfaced as `daedalus docs lint [--ci]` | Done |
| 4 | `docs/structured-docs.md` — the parseable-markdown contract the parsers rely on | Done |
| 5 | Frontend project-journey dashboard — replace the 5-KPI grid with Purpose → Arc (current sprint nested in the in-progress milestone) → Backlog, fed by `/overview` | Done |
| 6 | Reconcile `docs/PROJECT-INIT.md` to the structured-docs model (ROADMAP = milestones; add SPRINTS/BACKLOG; parseable templates verified against `daedalus docs lint`) and relocate it under `docs/` | Done |

**Frontend not yet browser-verified.** The journey dashboard is code-complete, static-checked, and its data path is verified against this repo's real documents, but it has never been rendered in a browser (no node in the build sessions). It rides the Sprint 41 real-Docker parity pass for a real paint against the approved mockup.

### Sprint 40: Coordinator-as-Daemon (v0.39.0)

Delivered 2026-07-11. Second slice of Milestone 4. Promoted `internal/coordinator` from an in-process, per-CLI map into a long-lived host daemon (`daedalus-coordinator`) exposing an HTTP-over-Unix-socket API, with a Go client, ssh-agent-style auto-spawn, and `sessions.json` persistence reconciled against `docker ps` across restarts. CLI and Web both attach through the daemon, so runner sessions are host-wide discoverable.

| # | Item | Status |
|---|------|--------|
| 1 | `internal/coordinator/daemon.go` — HTTP-over-UDS server (`POST`/`GET`/`GET {name}`/`DELETE /sessions`), reusing the `Coordinator` type | Done |
| 2 | `sessions.json` persistence under `DataDir/.daedalus/` — write-on-change, load + `docker ps` reconcile at startup | Done |
| 3 | `internal/coordinator/client.go` — Go client mirroring the `Coordinator` method shape; swap by constructor | Done |
| 4 | `cmd/daedalus-coordinator` daemon binary + `daedalus coordinator start/stop/status`; systemd unit + launchd plist under `contrib/` | Done |
| 5 | Rewire CLI `launchProjectViaRunner` and Web `?mode=runner` to the daemon client with ssh-agent-style auto-spawn (TUI list deferred) | Done |
| 6 | Deprecate the in-process path; retain `Coordinator` as the daemon's engine (one non-test caller) | Done |
| 7 | Real Unix-socket integration test booting the daemon binary against a mock `docker` on PATH, driving Start/List/Get/dup-Start/Stop/Get | Done |

### Sprint 39: Runner Foundation & Foreman Removal (v0.38.0)

Delivered 2026-07-11. First slice of Milestone 4 (Layered Runner / Coordinator Architecture). New `daedalus-runner` PID-1 binary inside the container, `runproto` wire protocol, host-side `runclient` and `coordinator`, and CLI + Web attach paths (`DAEDALUS_USE_RUNNER=1`, `?mode=runner`). Foreman removed wholesale. Large god-object refactors of `main.go`, `web.go`, `tui/`, and `registry.go`. Parallel test installs. `install.sh` version-recording fix.

| # | Item | Status |
|---|------|--------|
| 1 | `internal/runproto` — host ↔ runner wire protocol (`Hello`, `Output`, `Input`, `Resize`) | Done |
| 2 | `cmd/daedalus-runner` — PID-1 binary owning the container PTY with socket fanout | Done |
| 3 | `internal/runclient` — host-side socket client with scrollback replay and detach | Done |
| 4 | `internal/runner` — per-runner `Adapter` interface plus `claude` and `copilot` implementations | Done |
| 5 | `internal/coordinator` — host-side lifecycle owner: `Start` / `Get` / `List` / `Stop` with docker-compose-run and socket-readiness wait | Done |
| 6 | CLI runner path — `DAEDALUS_USE_RUNNER=1` short-circuits `launchProject` to `launchProjectViaRunner`, attaching via runclient | Done |
| 7 | Web terminal `?mode=runner` — `runnerRelay` bridges the runner socket to the browser WebSocket | Done |
| 8 | Foreman deprecated wholesale — CLI subcommand, HTTP routes, cascade machinery, Web view all removed | Done |
| 9 | Refactor: split `cmd/daedalus/main.go` (1674 → 171 line dispatcher + 12 topic files) (Backlog #50) | Done |
| 10 | Refactor: split `internal/web/web.go` god-object (1196 lines / 31 methods → topic files) (Backlog #49) | Done |
| 11 | Refactor: extract `controlRelay` from `handleTerminalControl` | Done |
| 12 | Refactor: split `internal/tui/tui.go` and `core/registry.go` into topic files | Done |
| 13 | Refactor: deduplicate `ShellQuote` — route `session` and `web` through `core` | Done |
| 14 | Parallel test installs — `--link-name`, `--container-prefix`, `--tmux-prefix`, `--image-prefix` on `install.sh`/`setup.sh` with matching `core.Config` support | Done |
| 15 | Web endpoints `/sprints`, `/backlog`, `/strategic-roadmap` to match post doc-split frontend (Backlog #34) | Done |
| 16 | Fix: runner-attach race — coordinator waits for socket before returning | Done |
| 17 | Fix: large paste kills WebSocket in tmux control mode (Backlog #47, #48) | Done |
| 18 | Fix: `install.sh` recorded `"version": "unknown"` — patch release tag into `config.json` before `setup.sh` | Done |

### Sprint 38: Document Structure Split (v0.37.0)

Delivered 2026-04-18. Separated the monolithic ROADMAP.md into three purpose-specific files — ROADMAP.md (strategic milestones), BACKLOG.md (prioritised work items), SPRINTS.md (sprint execution) — and updated all parsers, MCP tools, and the MCP client to support the new structure.

| # | Item | Status |
|---|------|--------|
| 1 | Split ROADMAP.md — extract backlog items to BACKLOG.md and sprint data to SPRINTS.md, keep only strategic milestones in ROADMAP.md | Done |
| 2 | `core/backlog.go` — `BacklogItem` type and `ParseBacklog()` parser with tests | Done |
| 3 | `core/roadmap.go` — rename `ParseRoadmap` to `ParseSprints` to reflect new file structure, add backward-compatible alias, with tests | Done |
| 4 | `cmd/project-mgmt-mcp/` — new `get_sprints`, `get_backlog`, `get_strategic_roadmap` MCP tools with `readSprintsFile()` fallback (SPRINTS.md → ROADMAP.md), with tests | Done |
| 5 | `internal/mcpclient/` — update MCP client methods for new tool names and add new methods for backlog and strategic roadmap, with tests | Done |
| 6 | Documentation — SPRINTS.md structure cleanup, VERSION, CHANGELOG | Done |

### Sprint 37: History Mode UX & Bug Fixes (v0.33.0)

Delivered 2026-04-18. History/scroll mode UX, crash recovery, Foreman roadmap fix, blank terminal fix.

| # | Item | Status |
|---|------|--------|
| 1 | `inHistoryMode` state tracking in `terminal.js` with `enterHistoryMode()` / `exitHistoryMode()` functions | Done |
| 2 | Visual banner (`#history-banner`) with "HISTORY MODE" label, hint text, and Exit button in `index.html` + `style.css` | Done |
| 3 | Exit via Esc key, any keystroke, or Exit button — sends `live-capture` to restore live viewport | Done |
| 4 | `CaptureVisible()` method on `ControlSession` and `live-capture` WebSocket message handler in `web.go` | Done |
| 5 | History mode state reset on WebSocket close, error, and `disconnectTerminal()` | Done |
| 6 | Foreman roadmap display — `showDashboard()` now resets roadmap panel and auto-loads via `loadRoadmap()`. Roadmap visible immediately when opening a project from Foreman or project list (backlog #41) | Done |
| 7 | Web UI blank terminal on attach — `ws.onopen` now sends `live-capture` request after resize to populate terminal immediately on connect (backlog #42) | Done |

### Sprint 36: tmux Control Mode — Web Terminal Relay (v0.32.0)

Goal: wire the ControlSession into the Web UI terminal as an alternative to the PTY relay. Add scrollback request support via WebSocket. Both modes coexist — control mode activates via `?mode=control` query parameter.

| # | Item | Status |
|---|------|--------|
| 1 | `internal/web/web.go` — `handleTerminalControl()` handler using `ControlSession` instead of PTY, with `readControlMessages` / `writeControlMessages` relay goroutines | Done |
| 2 | WebSocket scrollback — client sends `{"type":"scrollback","lines":N}`, server calls `CapturePane()` and returns `{"type":"scrollback-response","content":"..."}` | Done |
| 3 | `internal/web/static/terminal.js` — add scrollback request message type and render response | Done |
| 4 | Tests and documentation | Done |

### Sprint 35: tmux Control Mode — Control Session & Parser (v0.31.0)

Goal: implement the control session package and message parser (Phase 1 of the tmux control mode plan). New `ControlSession` type that spawns `tmux -C attach-session`, parses `%output/%begin/%end/%error` messages, and provides `SendKeys()`, `CapturePane()`, and `ResizeWindow()` methods.

| # | Item | Status |
|---|------|--------|
| 1 | `internal/session/controlparser.go` — `ControlMessage` type and `ParseControlLine()` parser for all `%`-prefixed message types, with tests | Done |
| 2 | `internal/session/control.go` — `ControlSession` struct with `Start()`, `SendKeys()`, `CapturePane()`, `ResizeWindow()`, `ReadMessage()`, `Close()` methods | Done |
| 3 | `internal/session/control_test.go` — unit tests for parser (all message types, edge cases) and integration tests for ControlSession with MockExecutor | Done |
| 4 | Documentation — ARCHITECTURE, CHANGELOG, VERSION | Done |

### Sprint 29: The Foreman Agent — Core Loop (v0.24.0)

Goal: Daedalus itself becomes an AI-driven project manager. The Foreman reads roadmaps, maintains a plan, monitors worker agents, and reports through the Web UI. Runs as a goroutine inside `daedalus web`.

| # | Item | Status |
|---|------|--------|
| 1 | `core/foreman.go` — `ForemanConfig`, `ForemanState`, `ForemanPlan` pure types | Done |
| 2 | `internal/foreman/foreman.go` — Foreman main loop: read programme, read roadmaps, build plan, monitor agents, report status | Done |
| 3 | `internal/foreman/planner.go` — sprint planning logic (reads roadmaps, proposes next actions) | Done |
| 4 | `internal/foreman/monitor.go` — monitoring loop: poll MCP client and agent observer for worker state | Done |
| 5 | `cmd/daedalus/main.go` — `daedalus foreman start/stop/status` subcommands | Done |
| 6 | `internal/web/` — `/api/foreman/status` endpoint, Foreman console panel in Web UI | Done |
| 7 | Documentation — ARCHITECTURE, CHANGELOG, VERSION, README | Done |

### Sprint 28: Agent Observability (v0.23.0)

Goal: define the agent observation interface and implement a container-status-based observer. Adds real-time agent state indicators to the Web UI. Partial implementation of backlog item 16 — full ACP integration deferred until the protocol is publicly stable.

| # | Item | Status |
|---|------|--------|
| 1 | `internal/agentstate/` — `AgentState` enum, `Observer` interface, `ContainerObserver` implementation | Done |
| 2 | `internal/web/` — `GET /api/projects/{name}/state` endpoint returning agent state | Done |
| 3 | Web UI — agent state indicator (colored dot) on project cards in the list view | Done |
| 4 | `internal/foreman/` — `AgentObserver` interface matching `agentstate.Observer` | Done |
| 5 | Documentation — ARCHITECTURE, CHANGELOG, VERSION | Done |

### Sprint 27: Daedalus as MCP Client (v0.22.0)

Goal: Daedalus consumes the project-mgmt-mcp server from the host side via `docker exec` + stdio transport. Enables programmatic reading of project state and aggregated programme views. Implements backlog item 18.

| # | Item | Status |
|---|------|--------|
| 1 | `internal/mcpclient/` — MCP client package using go-sdk, transport via `docker exec` + stdio | Done |
| 2 | High-level methods: `ReadProgress()`, `ReadRoadmap()`, `GetCurrentSprint()` | Done |
| 3 | `daedalus programmes show <name>` — aggregate progress from all member projects via MCP client | Done |
| 4 | Documentation — ARCHITECTURE, CHANGELOG, VERSION, README | Done |

### Sprint 26: Roadmap Parsing and Sprint Decomposition (v0.21.0)

Goal: Daedalus can read a ROADMAP.md file and parse it into structured sprint data. Adds a roadmap API endpoint and MCP tools for agents to query sprint status. Implements backlog item 17.

| # | Item | Status |
|---|------|--------|
| 1 | `core/sprint.go` — `Sprint`, `SprintItem`, `SprintStatus` types (pure, zero I/O) | Done |
| 2 | `core/roadmap.go` — `ParseRoadmap(markdown) ([]Sprint, error)` parser for Daedalus-native ROADMAP.md format | Done |
| 3 | `internal/web/` — `GET /api/projects/{name}/roadmap` endpoint, collapsible side panel in Web UI | Done |
| 4 | `cmd/project-mgmt-mcp/` — `get_roadmap` and `get_current_sprint` tools | Done |
| 5 | Documentation — ARCHITECTURE, CHANGELOG, VERSION, README | Done |

### Sprint 25: Programme Data Model and CLI (v0.20.0)

Goal: declare multi-project programmes with dependency relationships. Users can model project topology even without the Foreman. Pure data model sprint — no orchestration yet.

| # | Item | Status |
|---|------|--------|
| 1 | `core/programme.go` — `Programme`, `DependencyEdge`, `DependencyGraph` types; `TopologicalSort()`, `DetectCycles()`, `Downstreams()`, `Upstreams()` pure functions with tests | Done |
| 2 | `internal/programme/` — `Store` with `List`, `Read`, `Create`, `Update`, `Remove`, persisted to `programmes.json` with tests | Done |
| 3 | `core/config.go` — add `Programme` field and `ProgrammesArgs` to Config; `ProgrammesDir()` method | Done |
| 4 | `cmd/daedalus/main.go` — `daedalus programmes` subcommand: list, show, create, add-project, add-dep, remove | Done |
| 5 | Shell completions for `programmes` subcommand in bash, zsh, fish | Done |
| 6 | Documentation — update ARCHITECTURE.md, CHANGELOG.md, VERSION, README.md | Done |

### Sprint 24: Project Management MCP Server (v0.19.0)

Goal: add a second MCP server (`project-mgmt-mcp`) inside each container so Claude Code can report progress, set vision/version, and read sprint items. Daedalus reads progress via bind-mounted `.daedalus/progress.json`. Implements backlog item 14.

| # | Item | Status |
|---|------|--------|
| 1 | `internal/progress/` package — pure progress file read/write operations with tests | Done |
| 2 | `cmd/project-mgmt-mcp/main.go` — new MCP server binary with `report_progress`, `set_vision`, `set_version`, `get_progress` tools | Done |
| 3 | `core/command.go` — mount `.daedalus/` directory into containers via `BuildExtraArgs` | Done |
| 4 | `claude.json` — register `project-mgmt-mcp` MCP server entry | Done |
| 5 | `Dockerfile` — copy `project-mgmt-mcp` binary into image, `entrypoint.sh` — ensure `.daedalus/` directory exists | Done |
| 6 | `build.sh` — build `project-mgmt-mcp` binary alongside existing binaries | Done |
| 7 | `internal/web/` — poll `.daedalus/progress.json` from host and feed into dashboard endpoint | Done |
| 8 | Documentation — update ARCHITECTURE.md, CHANGELOG.md, VERSION, README.md | Done |

### Sprint 23: Project Management View in Web UI (v0.18.0)

Goal: per-project dashboard showing vision, version, time spent, and progress percentage — the foundation for the Foreman agent's reporting layer. Implements backlog item 13.

| # | Item | Status |
|---|------|--------|
| 1 | `core/project.go` — add `ProgressPct`, `Vision`, `ProjectVersion` fields to `ProjectEntry` with tests | Done |
| 2 | `internal/registry/` — v2-to-v3 migration (new fields default to zero values) with migration test | Done |
| 3 | `internal/registry/` — `UpdateProjectProgress(name, pct, vision, version)` method with tests | Done |
| 4 | `internal/web/` — `GET /api/projects/{name}/dashboard` endpoint returning progress data with tests | Done |
| 5 | `internal/web/static/` — project detail panel (click project row to see vision, version, total session time, progress bar) | Done |
| 6 | Documentation — update ARCHITECTURE.md, CHANGELOG.md, VERSION, README.md | Done |

### Sprint 22: Runner/Persona Polish & Skill Fix (v0.17.0)

Goal: clean up the runner/persona split — add `daedalus runners` subcommand, separate `personas list` from runners, store persona details in companion `.md` files, fix skill installation path, and harden validation and test coverage.

| # | Item | Status |
|---|------|--------|
| 1 | `daedalus runners` subcommand — list and show built-in runner profiles with shell completions | Done |
| 2 | `personas list` shows only user-defined personas, `personas show` rejects built-in names | Done |
| 3 | Persona `.md` companion file — store CLAUDE.md content alongside `.json` config | Done |
| 4 | Fix `resolvePersonaOverlay` — use `cfg.Persona`, set `cfg.Runner` from `BaseRunner` | Done |
| 5 | `--runner` strict validation (builtins only), `--persona` validation (rejects builtins, checks store) | Done |
| 6 | Skill install target: `~/.claude/commands/` → `/workspace/.claude/skills/` | Done |
| 7 | Dev release workflow fix — replace `softprops/action-gh-release` with `gh release create` | Done |

### Sprint 21: Personas & Runner/Persona Split (v0.16.0)

Goal: allow users to define named persona configurations that layer custom system prompts and tool-permission overrides on top of a built-in runner, selectable via `--persona <name>`. Split the overloaded "agent" concept into **runner** (claude/copilot binary) and **persona** (user-defined overlay).

| # | Item | Status |
|---|------|--------|
| 1 | `core/persona.go` — `PersonaConfig` type, `PersonasDir()`, `ValidatePersonaName()` with tests | Done |
| 2 | `internal/personas` package — Store with List/Read/Create/Update/Remove, unit tests | Done |
| 3 | `core/runner.go` — `LookupRunner` resolves personas to base runner, `ValidRunnerNames` for builtins, update all callers | Done |
| 4 | `core/command.go` — `BuildExtraArgs` injects custom CLAUDE.md and settings mounts for persona overlays | Done |
| 5 | `internal/config` — `--runner` and `--persona` flags with independent validation, legacy `--agent` alias | Done |
| 6 | `daedalus personas` CLI subcommand — list, show, create, remove with help text and shell completions | Done |
| 7 | Rename across codebase — `AGENT` env → `RUNNER`, docker-compose, entrypoint, Dockerfile, all docs | Done |

### Sprint 20: Active Project Filter (v0.15.0)

Goal: add a toggle/filter to the Web UI and TUI that shows only running projects, helping users focus when the project list grows large.

| # | Item | Status |
|---|------|--------|
| 1 | Web UI — filter toggle button in the project list header, filters table to running projects only | Done |
| 2 | Web UI — persist filter state in `localStorage` so it survives page reloads | Done |
| 3 | TUI — keybinding to toggle active-only filter, update project list rendering | Done |

### Sprint 19: Mobile Select Mode (v0.14.0)

Goal: enable native text selection on mobile terminals by overlaying the xterm.js buffer as plain selectable HTML.

| # | Item | Status |
|---|------|--------|
| 1 | Replace Copy button with Select toggle — overlay terminal buffer as selectable `<pre>` text, Done button to dismiss | Done |
| 2 | Force `user-select` and `touch-callout` for real mobile browser compatibility | Done |

### Sprint 17: Mobile-Friendly Web UI (v0.13.0)

Goal: make the web dashboard usable on phones and tablets — scrollable terminal, mobile input area, card-based project list.

| # | Item | Status |
|---|------|--------|
| 1 | Scrollable terminal — increase xterm.js scrollback to 10 000 lines | Done |
| 2 | Multi-line mobile input — textarea + Send button below terminal, Ctrl+Enter submits, xterm.js stdin disabled on mobile | Done |
| 3 | Card-based project list on mobile — hide Target/Last Used columns, flex card layout, larger touch targets | Done |
| 4 | Playwright test suite for the web frontend | |

### Sprint 16: Copilot CLI Support (v0.11.0)

Goal: agent abstraction so projects can use either Claude Code or Copilot CLI, selectable via `--agent copilot` or per-project default.

| # | Item | Status |
|---|------|--------|
| 1 | `core/agent.go` — `AgentProfile` struct, `LookupAgent()`, `ValidAgentNames()`, `ResolveAgentName()` with tests | Done |
| 2 | `Agent` field in `Config`, `AppConfig`, and `applyDefaultFlags` with tests | Done |
| 3 | `BuildAgentArgs()` — agent-aware argument builder, `BuildClaudeArgs()` kept as deprecated alias, `AGENT` in tmux exports, with tests | Done |
| 4 | `--agent` flag parsing with validation in `internal/config` with tests | Done |
| 5 | Wire up in `cmd/daedalus/main.go`, `internal/tui/tui.go`, `internal/web/web.go` — use `BuildAgentArgs`, pass `AGENT` env, update help text and `collectDefaultFlags` | Done |
| 6 | Shell completions for `--agent` in bash, zsh, and fish | Done |
| 7 | `docker-compose.yml` — `AGENT` environment variable | Done |
| 8 | `entrypoint.sh` — agent-aware dispatch (claude/copilot) | Done |
| 9 | `Dockerfile` — `copilot-base` and `copilot-dev` stages with Copilot CLI via gh.io installer | Done |

### Sprint 15: Skill Catalog (v0.10.0)

Goal: shared skill catalog with MCP server for browsing, installing, and publishing skills across projects.

| # | Item | Status |
|---|------|--------|
| 1 | `internal/catalog` package — pure catalog operations (list, read, install, uninstall, create, update, remove) with 21 unit tests | Done |
| 2 | `skill-catalog-mcp` MCP server — 8 tools over stdio using official `github.com/modelcontextprotocol/go-sdk` | Done |
| 3 | Docker integration — skills volume mount in `BuildExtraArgs`, MCP server entry in `claude.json`, binary in Dockerfile | Done |
| 4 | `daedalus skills` CLI subcommand — list, add, remove, show skills from the host | Done |
| 5 | Starter skills — `commit.md` and `review.md` seeded via `go:embed` on first run | Done |
| 6 | Build & install — `build.sh` builds both binaries, `install.sh` includes `skill-catalog-mcp` in runtime files | Done |

### Sprint 14: Display Sharing (v0.9.0)

Delivered 2026-03-21. GUI application rendering from Docker containers on the host screen via X11/Wayland forwarding.

| # | Item | Status |
|---|------|--------|
| 1 | `--display` flag plumbing — Config field, CLI parsing, per-project defaults, help text, shell completions, man page | Done |
| 2 | Display forwarding logic — `DisplayArgs()` in `internal/platform/display.go` for X11 + Wayland, wire into `launchProject()` | Done |
| 3 | First-run prompt — ask during interactive project registration whether to enable display forwarding (default: no) | Done |

### Sprint 13: Platform & Accessibility (v0.8.3)

Delivered 2026-03-20. WSL2 web access, dev releases, browser tab title, code quality improvements.

| # | Item | Status |
|---|------|--------|
| 1 | WSL2 Web UI access — auto-detect WSL2, bind to `0.0.0.0`, print VM IP for Windows browser access | Done |
| 2 | Dev release workflow — rolling `dev` pre-release on push to master with `VERSION-dev+SHA` binaries | Done |
| 3 | Browser tab title — set the Web UI tab title to include the name of the active project | Done |
| 4 | Core package purity — move `PrintBanner()` from `core/banner.go` to `cmd/daedalus/`, keeping `ReadVersion()` in core. Restores the zero-I/O invariant for the `core/` package | Done |
| 5 | Executor test coverage — add `internal/executor/executor_test.go` with tests for `MockExecutor` (call recording, result lookup, `HasCall`/`FindCall`/`FindCalls` queries) | Done |
| 6 | Fix stale test fixture — update 13 hardcoded `"0.8.1"` version strings to `"0.8.2"` in `cmd/generate-manpage/main_test.go` | Done |
| 7 | Refactor `run()` — extract `ensureImageBuilt()`, `launchProject()`, and `resolveProject()` from the 197-line `run()` function in `cmd/daedalus/main.go` to bring it under ~60 lines | Done |

### Sprint 12: Build, Debug & Logging Improvements (v0.8.0)

Goal: improve the build workflow, add diagnostic tooling, and set up release documentation.

| # | Item | Status |
|---|------|--------|
| 1 | Standalone `--build` — allow `daedalus --build` without requiring a project name or path, rebuilding the image for the current directory or all registered projects | Done |
| 2 | Verbose `--debug --build` output — when `--debug` is combined with `--build`, log all environment variables and the resolved paths for Dockerfile and docker-compose.yml | Done |
| 3 | File logging — write runtime logs to a persistent log file (e.g. `~/.local/share/daedalus/daedalus.log` or configurable path) for post-mortem debugging | Done |
| 4 | Release changelog — show a curated changelog / new features summary on the GitHub Release page | Done |
| 5 | Auto-rebuild after install/upgrade — detect when runtime files (Dockerfile, entrypoint, etc.) have changed and rebuild the Docker image on next project start | Done |
| 6 | Install script test harness — run the installer in a chroot or lightweight container to validate install/upgrade/uninstall flows without affecting the host | Done |

### Sprint 33: Project Workflow Improvements (v0.29.0)

Delivered 2026-04-01. Target switching via config --set, GitHub repo URL/shorthand project creation.

| # | Item | Status |
|---|------|--------|
| 1 | Switch target — add `UpdateProjectTarget()` to registry, handle `target=<stage>` in `daedalus config --set`, validate against known targets | Done |
| 2 | GitHub repo projects — detect GitHub URLs in positional args, clone repo, register as project | Done |
| 3 | Documentation — ARCHITECTURE, CHANGELOG, VERSION | Done |

### Sprint 32: Web UI Authentication (v0.28.0)

Delivered 2026-04-01. Token-based auth for Web UI with login page, session cookies, and --auth/--no-auth flags.

| # | Item | Status |
|---|------|--------|
| 1 | Auth token generation — `AuthToken`/`AuthExpiry` in `AppConfig`, token persisted to `config.json` | Done |
| 2 | `--auth` / `--no-auth` flags — default auth enabled for `web` subcommand | Done |
| 3 | Auth middleware — login page, session cookie, exempt paths | Done |
| 4 | WebSocket auth — cookie or `token` query parameter | Done |
| 5 | Documentation — ARCHITECTURE, CHANGELOG, VERSION, README | Done |

### Sprint 31: Web UI Polish & Skill Paths (v0.27.0)

Delivered 2026-04-01. Favicon, Foreman project navigation, directory-per-skill catalog structure.

| # | Item | Status |
|---|------|--------|
| 1 | Favicon — add an SVG favicon to `internal/web/static/`, link in `index.html` `<head>` | Done |
| 2 | Foreman UI project navigation — make project cards in the Foreman view clickable, navigating to the project detail/dashboard view | Done |
| 3 | Skill install path — change catalog install/read/list to use `{name}/SKILL.md` directory structure instead of flat `{name}.md` files. Update starter skills, MCP server, and all tests | Done |
| 4 | Documentation — ARCHITECTURE, CHANGELOG, VERSION | Done |

### Sprint 30: Programme-Level Cascade Orchestration (v0.25.0)

Delivered 2026-04-01. Cascade propagation through programme dependency graphs with configurable strategies.

| # | Item | Status |
|---|------|--------|
| 1 | `internal/foreman/cascade.go` — cascade logic via `DependencyGraph.Downstreams()`, cascade strategies (`auto`, `notify`, `manual`) | Done |
| 2 | `core/programme.go` — add `Strategy` field to `DependencyEdge` | Done |
| 3 | `internal/web/` — cascade event log in Foreman status API response | Done |
| 4 | `daedalus programmes cascade <name> --dry-run` — preview cascade actions | Done |
| 5 | Documentation — ARCHITECTURE, CHANGELOG, VERSION, README | Done |

### Sprint 18: Fix macOS Installation (v0.12.1)

Delivered 2026-03-24. Portable macOS install support for bash 3.2.

| # | Item | Status |
|---|------|--------|
| 1 | Fix `sed -i` in `install.sh` — use cross-platform `sed_inplace` wrapper for BSD/GNU compatibility | Done |
| 2 | Fix `sed -i` in `scripts/test-install.sh` — same `sed_inplace` wrapper for all 9 `sed -i` calls | Done |
| 3 | Add macOS (`macos-latest`) job to CI workflow — run install tests on both Ubuntu and macOS | Done |
| 4 | Fix symlink resolution in `ScriptDir` — `os.Executable()` returns the symlink path on macOS, so `filepath.EvalSymlinks` is needed to find the real binary directory containing Dockerfile and runtime files | Done |
| 5 | Fix empty array expansion in `install.sh` — `"${FORWARD_ARGS[@]}"` fails with `set -u` on macOS bash 3.2 when no flags are passed; use `${FORWARD_ARGS[@]+"${FORWARD_ARGS[@]}"}` | Done |

### Sprint 11: UX & Installer Polish (v1.2.0)

Delivered 2026-03-08. Docker inspect suppression, TUI keybinding change, upgrade-aware installer.

| # | Item | Status |
|---|------|--------|
| 1 | Suppress `docker inspect` output when starting a container from the web interface | Done |
| 2 | Change TUI kill shortcut from `K` to the `Del` key | Done |
| 3 | Upgrade-aware installer — store version in `config.json`, detect existing install, replace binary and migrate config fields as needed | Done |

### Sprint 10: Container Polish (v1.1.0)

Delivered 2026-03-08. Suppress docker command echo on container startup.

| # | Item | Status |
|---|------|--------|
| 1 | Suppress docker compose command and env exports from terminal on container startup | Done |

### Sprint 9: 1.0 Preparation (v1.0.0)

Delivered 2026-03-07. Stability audit, integration tests, CI/CD, man page, final docs.

| # | Item | Status |
|---|------|--------|
| 1 | Stability audit — review and freeze public API surface (CLI, config, registry, env vars) | Done |
| 2 | End-to-end integration test suite — cross-package workflow tests | Done |
| 3 | Binary releases via GitHub Actions (CI + release workflows, Linux/macOS amd64/arm64) | Done |
| 4 | Man page generation — `daedalus(1)` roff man page from CLI help | Done |
| 5 | Final documentation pass — README, CONTRIBUTING, ARCHITECTURE, CHANGELOG, VERSION bump to 1.0.0 | Done |

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

### Hotfix v0.4.1: DinD & Prune Fixes (post-Sprint 4)

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


