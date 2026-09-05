# The system as it stands

*What Daedalus, the Guild, the Guild Master, the control plane and the Ledger actually are today, and how they relate. Written 2026-08-26 from the code on `development`, not from the older design documents — where the two disagree, this file follows the code and says so.*

*Companion document: `docs/responsibilities.md` asks what the arrangement below *should* be. This one only describes what it *is*.*

---

## 1. In one paragraph

Daedalus launches an AI coding agent inside a Docker container, one container per project. That is the whole of the original product, and `VISION.md` still describes only that. Layered above it since M13 is a **control plane**: a second host-side daemon that decides what work may run, whether a result is done, and what may land. Above that sits the **Guild Master**, a reserved project whose container can read every other project read-only and file work into the plane through a restricted socket. The **Ledger** is the web page a human reads to adjudicate what the plane holds. Four layers, and the seam this document exists to make visible is between the top two: planning documents and plane state are two separate hierarchies that never touch.

---

## 2. The four layers

```
  PLANNING          ROADMAP.md (milestones) → SPRINTS.md (sprints)
  (markdown)        per project, in the repo, parsed but never enforced
                    ─────────── no link of any kind ───────────
  INTENT            Programme (PR-n) — the shared intent several projects serve
  (control.db)      plane-owned identity; a Task may point at one
                          │
  WORK              Task (T-n) ──► Job (J-n) ──► Artifact (A-n)
  (control.db)      intent        one attempt    durable result
                          │
  EXECUTION         a container: worktree, pinned image, frozen acceptance policy
  (docker)
```

The dashed line is the finding. `Task` carries `ProgrammeID` and `Rationale`; it carries **no `MilestoneID` and no `SprintID`** (`internal/control/model.go:347-451`). The control plane contains no milestone or sprint entity at all — ten tables, none of them milestone-shaped (`internal/control/store.go:152-341`). The plane's only contact with a roadmap is that the *default* acceptance check happens to be the string `"daedalus docs lint"` (`internal/control/acceptance.go:106`): it shells out to a linter, it never parses a roadmap.

The roadmap named this itself, in M20 (`ROADMAP.md:178`):

> **There are two hierarchies in this system and they never touch:** `VISION → ROADMAP (milestones) → SPRINTS`, which is documents, per project; and `Task → Job → Artifact`, which is authoritative state. A Task carries a free-text `Objective` and nothing else — **no link to a milestone, a sprint, a programme, or a vision.** **So the most important relationship in the system, 'why is this work worth doing', is held only in the Guild Master's context window.**

M20 then joined the *programme* half and left the milestone half exactly where it found it.

---

## 3. The physical arrangement

### Host and containers

| | Runs on | Purpose |
|---|---|---|
| `daedalus` | host CLI (also copied into every image) | dials both daemons; auto-spawns them ssh-agent style |
| `daedalus-coordinator` | host daemon | container lifecycle — "run this project's agent" |
| `daedalus-control` | host daemon | owns `control.db` — "what may run, what is done, what may land" |
| `daedalus-runner` | in every project container | wraps `claude`/`copilot` on a PTY, fans it out to attached clients |
| `project-mgmt-mcp` | in every project container | reads and **writes** that project's ROADMAP/SPRINTS |
| `skill-catalog-mcp` | in every project container | the shared skill catalogue |
| `guild-mcp` | Guild Master container only | read-only cross-project document reads |
| `guild-control-mcp` | Guild Master container only | the Guild Master's only route to the plane |

The two-daemon split is the declared boundary (`ARCHITECTURE.md:381-384`): *"The coordinator answers 'run this project's agent'; the control plane decides what may run, whether the result is done, and what may land."*

Note a distinction that is easy to get wrong: **distribution is not gating.** `guild-mcp` and `guild-control-mcp` are copied into *every* project image (`Dockerfile:191`ff). What is Guild-Master-only is the MCP server *entry* injected into `.claude.json` by `entrypoint.sh:55-79`, gated on `DAEDALUS_GUILD_MASTER`.

### Sockets

| Socket | Bound by | Dialed by | Caller class |
|---|---|---|---|
| `runner.sock` | the container's runner | host CLI / TUI / Web | — (terminal I/O) |
| `coordinator.sock` | coordinator | CLI, TUI, Web, `daedalus-control` | — |
| `control.sock` | control plane | CLI, Web (the Ledger), TUI | **human** |
| `control-agent.sock` | control plane | `guild-control-mcp` only | **agent** |

The last two are the same daemon, the same ~40 routes, the same handler — and different authority. **Which socket accepted the connection *is* the caller identity.** `cmd/daedalus-control/main.go:211-237`, and the reasoning at `internal/control/caller.go:7-33`:

> **WHY NOT A REQUEST FIELD.** The obvious design — an `actor` field on the request — is worse than having no identity at all: a client that can name its own actor can name "human", so the label would be an assertion by the very party it is meant to constrain.
>
> **WHY NOT PEER CREDENTIALS.** […] the Guild Master's agent runs as the same uid as the human operating the CLI, so peer credentials separate *users*, not *caller classes*.

And the honest limit, stated in the same comment: *"the boundary is the container's mount namespace, not the filesystem permissions, and only the agent socket is ever mounted into a container."*

The mount that carries it is fail-closed three ways (`core/guild.go:70-108`), and the third is the one that matters:

> 3. The basename is not exactly `control-agent.sock` → nil. This is the one mistake the design cannot absorb: mounting the HUMAN `control.sock` here would silently promote the agent to full authority, since the class comes from the file, not from the request.

---

## 4. The Guild

"The Guild" is just the project registry. Every registered project except the Guild Master itself is bind-mounted read-only at `/guild/<name>` in the Guild Master's container (`core/guild.go:35-57`). There is no membership list, no opt-in and no opt-out: **registering a project is the act of granting the Guild Master read access to it.** Membership is a launch-time snapshot — a project registered later appears on the next launch (`ARCHITECTURE.md:350-351`).

Worth being precise about the boundary: the bind mount exposes the *entire* project directory read-only, so ordinary file tools can read anything under `/guild/<name>`. The `read_project_doc` allow-list (the eight standard documents plus `VERSION` and `CLAUDE.md`) bounds one *tool*, not the agent. No document states what the Guild Master may read outside that set.

---

## 5. The Guild Master

A reserved, un-removable, un-renameable built-in project (`core/project.go:13-22`) with a Daedalus-owned workspace. Its `CLAUDE.md` is a role document that Daedalus regenerates on launch — but only when the file byte-matches the current version or one of three recorded priors, so a user's edits are never clobbered (`core/guild.go:349-355`, `internal/registry/registry.go:203-222`).

The design thesis, `docs/guild-master-plan.md:33-36`:

> **The Guild Master has initiative. The control plane has authority.** The Guild Master decides *what* should happen; the control plane decides *whether* it may and *makes* it happen; project agents decide *how* to do their assigned work; independent verification decides whether the *result* is acceptable.

And Principle 4 (`:62-65`): *"its product is **decomposition, specs, and gates**."*

### What it can do

**Directly:** all reads (documents via `guild-mcp`; tasks, jobs, events, boards, programmes via `guild-control-mcp`); `create_task`; `request_verification`.

`create_task` is allowed directly for a reason worth quoting, because it is the template for any future grant (`internal/control/authority.go:115-118`):

> Bounded creation: allowed, because it cannot exceed policy. The budget is clamped to the project ceiling and the acceptance oracle is frozen at the plane-owned target, so the worst a poisoned doc achieves is a task nobody wanted — visible, budgeted, and cancellable by a human.

**By proposal only** (recorded for a human to confirm; never executed): dispatch, retry, replan, cancel, integrate, steer, and programme form/amend/dissolve.

**Refused outright, not even proposable:** confirming its own proposal; amending a Task's acceptance checks; amending a Task's budget.

### Why the tiering exists

`internal/control/authority.go:5-24` — the reasoning, not the rule:

> The Guild Master reads project-controlled documents (README, VISION, ROADMAP…) AND holds action tools. That is the textbook lethal trifecta: private data + untrusted content + an action vector. Prompt hardening is not a defence against it; the defence is that the consequential actions are not available to that agent in the first place.

Two things are absent **by construction** rather than by rule, which is a stronger property than being forbidden:

- *There is no raise-a-budget operation at all* (`:27-31`). A budget may only ever be narrowed, so "the Guild Master raises its own budget" is not an operation that can be attempted, tiered, or refused — it does not exist.
- *Amending a task's checks* has no operation name, no tier entry and no proposal path (`:33-41`), because *"a proposal would launder exactly what the rule forbids — the agent authors the command, a human clicks confirm, and a command written by the party being graded is now running inside the verifier."*

The table fails closed in both directions, deliberately: only an explicitly human class gets full authority (a zero-valued `Caller{}` is an agent), and an *unknown operation* is `TierProposal` for a non-human caller, never `TierAllowed` (`:199-215`). A test enumerates every mutating operation so that adding one and forgetting to tier it fails the build.

### How the role has changed

The comment explaining the rewrites is the clearest statement of intent in the repository (`core/guild.go:133-139`):

> **WHY IT KEEPS BEING REWRITTEN.** The first version was written at M12, when reading was genuinely all the Guild Master could do, and it said so: "you do not control, dispatch, or launch other agents — that is impossible by design". By M15 it could create Tasks and by M20/M21 it could propose programmes — so its own instructions told it it could not do things it could, **which is the surest way for a capability to go unused.**

M12 *visibility only* → M21 *notice, then ask* → S67 *attach the work to a programme and a reason* → now *write the task so a human can check it off*. **Responsibility widened from seeing to specifying; the boundary against executing never moved.**

---

## 6. The Ledger

A single web page — `internal/web/static/control.js` plus `internal/web/control.go`. There is no `Ledger` type in Go. It holds no state and no authority; the board it draws is itself derived (`internal/control/board.go:12-16`: *"There is no board table […] A board with its own state would be a second answer to 'what is happening', and the whole arc has been about there being exactly one."*). `ARCHITECTURE.md:429-433` calls it *"a relay, not a second implementation: nothing in `internal/web` decides what is legal."*

It is **human-only, structurally** — the web server dials `control.sock`, and caller class comes from the socket. No MCP surface exposes it; agents reach the same data through tools, with host paths and repo identities stripped.

Its own commit history shows what it is for: `f0f3d08` — *"The Ledger could show you a task the plane had refused and offer you nothing to do about it."* Recent work is about surviving its own 15-second poll well enough that a person can read a judgement and act on it.

> **Homonym warning.** `docs/guild-master-plan.md:375` and `docs/guild-master-control.md:135` discuss Magentic-One's "Task Ledger / Progress Ledger" (arXiv:2411.04468) as prior art for a *future Guild-Master planning store* with a stall→replan loop. That is an unbuilt concept that shares a word with the shipped page. Nothing in the tree implements it — see §8.

---

## 7. Who may write what

This is the table the rest of the document exists to produce.

| Artefact | Where it lives | Human | Project's own agent | Guild Master | Gate |
|---|---|---|---|---|---|
| **Milestones, sprints** | markdown in the repo | yes (hand edit) | **yes — 11 lifecycle tools, ungated** | **no** | **none** |
| **Programme** | `control.db` | forms directly | no access | proposes only | tier table |
| **Task** | `control.db` | creates directly | no access | **creates directly** | budget ceiling + frozen oracle |
| **Task→Task dependency** | `control.db` | adds directly | no access | proposes only | blocks landing |
| **Acceptance policy** | `.daedalus/verify.json` at `base_sha` | edits the file | edits the file | no | frozen at create; drift ⇒ rejection |
| **Per-task checks** | `control.db` | yes | no | **refused, not proposable** | — |
| **Budget ceiling** | `budgets.json` under the data dir | edits the file | no | **does not exist as an operation** | — |
| **Approval** | `control.db` | **human only** | no | proposes only | the gate |
| **Integration target** | `control.db` | `task target --sync` | no | proposes only | CAS on landing |

Every row has an owner and a gate except the first, and the first is the one at the top of the intent hierarchy.

Note the asymmetry that produces the practical problem: the milestone write tools live in the **project's own container**, reachable by that project's agent with nothing checking them, while the Guild Master — the one seat that can see every roadmap at once — can write none of them, including its own.

---

## 8. Gaps, verified

Things that are neither assigned nor forbidden, and stale claims found while mapping.

**No document names an owner for milestones or sprints.** An exhaustive sweep of `ROADMAP.md`, `SPRINTS.md`, `BACKLOG.md`, `ARCHITECTURE.md`, `VISION.md`, `MUSINGS.md`, all of `docs/`, `README.md`, `CONTRIBUTING.md` and the role doc found no sentence assigning it. `docs/structured-docs.md` contradicts itself 176 lines apart — `:15-17` *"The documents stay human-authored markdown […] nothing a tool writes and a human must not touch"* against `:191-194` *"an agent can manage the roadmap without hand-editing the markdown. **Prefer the tools**."* `docs/PROJECT-INIT.md` assigns bookkeeping (flip the status when a milestone completes) but never authorship.

**The only place roadmap authority was ever proposed is inside a milestone marked Done.** `ROADMAP.md:134` — *"(Optional add-on) roadmap-transition governance for PM-opt-in projects, reusing the approval machinery."* M15 is **(Done)**. No implementation, no backlog item, no further mention.

**The only component that ever read a ROADMAP to plan work was deleted.** The Foreman (`MUSINGS.md:17` — *"reads a ROADMAP.md, decomposes it into sprint items"*) was removed in `fb124db` because *"It never actuated anything."* Nothing replaced its planning function.

**The Guild Master's "notice" duty has no trigger.** `docs/guild-master-control.md:135` describes a Programme Task Ledger with a stall→replan loop; it was never built. There is no periodic loop, no stall counter, no termination duty. The Guild Master notices only when a human launches it.

**Five tools exist that no role-doc version has ever mentioned** — `request_dispatch`, `request_retry`, `request_replan`, `request_cancel`, `request_integration`. This is precisely the defect `core/guild.go:133-139` says the rewrites exist to prevent. Related: nine operations are tiered in `agentAuthority` but reachable from no tool, and there is **no `list_proposals` tool** — so the Guild Master is instructed to report "asked, not done" and then has no way to learn the answer.

**One live contradiction in the role doc.** It still says *"you never launch or drive another agent"* while `request_dispatch` asks for exactly that, subject to human confirmation. Nothing reconciles the two sentences.

**The project has stopped recording its own work in its own roadmap.** 23 commits between 2026-08-22 and 2026-08-26 — `feat/limits`, `feat/refine`, `feat/budget`, `feat/ledger`, the whole no-dead-ends run. `ROADMAP.md` and `SPRINTS.md`: untouched since 08-21. Five days of work exists only in commit messages and BACKLOG rows. This is the gap above, demonstrating itself.

**`VISION.md` describes a different product.** Unedited since March, it promises a Docker permission-wrapper and lists *"Container orchestration"* among its **non-goals** (`:29`). It contains the words "guild", "programme", "control plane", "Guild Master", "milestone" and "roadmap" exactly zero times. Ten milestones later nothing reconciles the two.

### Appendix: code/comment drift found while mapping

Small, all verified, none load-bearing on their own. Listed so they can be filed rather than rediscovered.

1. `internal/control/git.go:130-131` names `--show-toplevel` as the authority; the code tries `--git-common-dir` first (`:143`).
2. `internal/coordinator/coordinator.go:133-134` documents `--rm`; `:197-202` explains why there is deliberately no `--rm`. Same function.
3. `daedalus-runner` is documented as PID 1 in four places; `docker-compose.yml:30` sets `init: true` and its own comment says *"The runner becomes pid 2."*
4. `internal/control/scope.go:279` references a helper `proposalOnly` that does not exist. The behaviour is real; the name is not.
5. `core/guild.go:59-63` says three components *"agree on one string"* for the agent socket path — they are three independently-written copies of the literal. The repo's own "derive, don't enumerate" defect.
6. `core/guild.go:112-115` rests on `ValidateProjectName` having run; the primary CLI registration path never calls it. `sanitiseGuildMountName` is therefore load-bearing, not defence in depth. Not currently exploitable.
7. `ARCHITECTURE.md` — no row for `internal/control` (the largest, most authority-bearing package); `project-mgmt-mcp` listed with 4 tools, actual 17; `runproto` described as length-prefixed, actually newline-delimited JSON; `attach.go` listed but absent while six real topic files are omitted; the binaries table omits `daedalus-control`, `guild-mcp` and `guild-control-mcp`.
8. `internal/registry/registry.go:46-61` — the one-shot migration walks the data dir and registers every subdirectory as a project, unfiltered. Only fires when `projects.json` is absent, so bounded, but it would register `skills/`, `shared/`, `control/` and `.daedalus/` as projects.
