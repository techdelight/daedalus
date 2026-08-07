# The Guild Master: Total Plan & Proposal (control-plane architecture)

*The decision-ready design for evolving the Guild Master from a read-only
programme overseer (shipped in M12) into a **controlling entity**. This revision
adopts the **control-plane architecture** from the evaluation in
[`../daedalus-control-plane-report.md`](../daedalus-control-plane-report.md); the
research evidence base is in [`guild-master-control.md`](guild-master-control.md).*

**Status:** proposal for build. Milestones M13–M17 are **Planned**, sequenced as
the report's **V1 → V2 → V3**. Nothing is built until a milestone is explicitly
opened.

**Revision note.** The first draft of this plan gave the Guild Master its own
"control tools" and parked its task ledger in its own workspace docs. The
evaluation correctly replaced that with a **host-side control plane** as the
authority, and moved authoritative state out of any agent workspace. That is a
strictly better, safer design and is adopted wholesale below; §7 records the
handful of refinements added on top of it.

---

## 1. The idea, in one paragraph

Daedalus runs a *guild* of projects, each an autonomous coding agent in a
hardened container. **The Guild Master is the guild's leader.** Today (M12,
v0.47.0) it can *see* every project's documents but cannot *act*. This plan gives
Daedalus genuine supervisory authority without taking away agent autonomy, via a
new host-side **control plane**:

> **The Guild Master has initiative. The control plane has authority.**
> The Guild Master decides *what* should happen; the control plane decides
> *whether* it may and *makes* it happen; project agents decide *how* to do their
> assigned work; independent verification decides whether the *result* is
> acceptable.

## 2. Where we are today (the baseline)

M12 shipped the Guild Master as a **supervisor by visibility**: an un-removable
`guild-master` project with **read-only** cross-project document access
(`/guild/<name>` mounts + `guild-mcp`), rendered as a crowned hero. It advises;
it does not command. This plan closes that gap — honestly.

## 3. The core thesis (design principles)

1. **Control must be externally *imposable*, not *cooperative*.** Cooperative
   control (in-process handoffs, shared graph state, `interrupt()`) needs the
   worker built into a framework — impossible for an autonomous CLI. Imposable
   control (session lifecycle, resource caps, **verification of a committed
   artifact**, the integration gate, host-mediated policy) works regardless of
   the worker's internals. Hard guarantees sit on the imposable column.
2. **Verification is the crown jewel.** MAST: ~24% of multi-agent failures are
   "declared done ≠ verified done", and structural verify gates beat prompts. So
   "done" is decided by the control plane checking an **artifact**, never by the
   worker's self-report.
3. **Gate at boundaries, not mid-turn — and don't even need to.** Daedalus can't
   pause a token stream. It doesn't have to: by making verification structural
   (on the committed artifact, in a clean container) the whole design sidesteps
   mid-turn control. Boundary hooks (`PreToolUse`/`Stop`) and typed steering are
   *secondary*, not the authority.
4. **The Guild Master is a supervisor, never a puppeteer.** It orchestrates only
   parallelisable, well-specified work; its product is *decomposition, specs, and
   gates*. It **proposes**; the control plane **adjudicates and executes**.

## 4. Architecture — the control plane

```text
        Human ──approve / cancel / inspect──┐
                                            ▼
   ┌──────────────────────────────────────────────────────┐
   │            DAEDALUS CONTROL PLANE (host-side)         │
   │  Tasks · Jobs · Artifacts · Policies · Budgets ·      │
   │  Verify · Approvals · Audit   — authoritative state   │
   └───────┬───────────────────────────────┬──────────────┘
           │ lifecycle (coordinator.sock)   │ clean verification
           ▼                                ▼
      Coordinator ──► Project containers   Verifier container
           ▲
           │ constrained requests (control.sock)
   ┌───────┴────────┐        Guild Master runs guild-control-mcp and receives
   │  Guild Master  │        ONLY control.sock — never coordinator.sock. Even a
   │ plan/decompose │        direct call to control.sock gains no extra authority;
   │ dispatch/eval  │        the socket exposes only constrained, policy-checked ops.
   └────────────────┘
```

- **The control plane is the security boundary** — not the MCP. `guild-control-mcp`
  is an ergonomic front-end; the real enforcement is the `control.sock` API,
  which validates every request against project identity, task state, budgets,
  policies, approval requirements, runner capabilities, artifact provenance, and
  verification state.
- **Intent-level interface only.** The Guild Master says *what*, never *how*:
  `create_task(project, objective, acceptance_criteria, budget)`,
  `dispatch_task`, `get_task`, `cancel_task`, `request_verification`,
  `request_integration`, `steer_job`, `list_pending/blocked_tasks`. It is **never**
  given `run_shell` / `docker_run` / `mount` / `git_exec` / `start_container`. The
  control plane resolves the project through the trusted registry and constructs
  all execution details itself — so the Guild Master can never become a privileged
  remote shell.
- **The coordinator stays lifecycle-only** (start/stop/get/list, session
  persistence, runner sockets). The control plane sits *above* it and answers a
  different question: *why is this agent running, what task, what is it allowed
  to do, and is its result acceptable?*

## 5. Core data model — Task / Job / Artifact

The unit of orchestration is the **Job**, not the session — because failure,
retry, timeout, and re-planning are normal events.

```text
Task  T-104   what to accomplish   (project, objective, acceptance_policy)
  ├─ Job J-221  one attempt        (base_sha, runner, budget, status) → failed
  ├─ Job J-222                                                        → timed-out
  └─ Job J-223                                                        → succeeded
        └─ Artifact A-311  the durable result  (base_sha, head_sha,
                            branch daedalus/T-104/J-223, verify:pass, review:pass)
```

- **Authoritative state lives host-side** in a Daedalus-owned **SQLite** store
  (`tasks`, `jobs`, `artifacts`, `verification_runs`, `approvals`, `events`) —
  atomic transitions, crash recovery, durable audit, zero external deps. The
  Guild Master's `TASKS.md`/`STATUS.md` are **read-only projections** of this
  state, never the system of record. *A controlling agent must not be able to
  edit the state that determines whether its own work is valid.*

**Task state machine (control-plane owned):**

```text
planned → queued → working → candidate → verifying →(FAIL)→ rejected → retry/replan
                     │  ▲                     │
              input_required                  └─(PASS)→ verified → approval_required
                                                          → approved → integrated
terminal: failed · cancelled · expired · integrated
```

The worker may only drive `working → candidate` ("I think it's done"). **Only the
control plane** performs `candidate → verified`. That single rule makes
verification structural rather than conversational.

## 6. The load-bearing guarantees

- **Independent verification.** Verification never runs in the worker's live
  workspace. The control plane checks out the Artifact's commit into a **clean
  verifier container** (the project's image, no worker mutable state) and runs the
  project's verify policy (build + tests + `daedalus docs lint` + acceptance).
  What's verified is the **artifact**, not the worker's environment — immune to
  uncommitted files, altered test config, residual caches. **Runner-agnostic:** it
  checks a git commit, so it works for Claude *or* Copilot; an injected Stop-hook
  is only an optional early nudge.
- **Frozen acceptance policy.** When a Task is created, the control plane captures
  the verify policy from the task's `base_sha` and hashes it
  (`acceptance_policy: project-policy@924ab7f`). A worker **cannot weaken the check
  it must pass** (e.g. rewrite `go test ./...` to `echo success`); a proposed
  policy change takes effect only for *future* tasks after integration.
- **The plane can reject the Guild Master.** Budget too high → `REJECTED`. Artifact
  built from a stale base → `REJECTED, must rebase + re-verify`. The plane
  enforces policy; it does not merely execute.
- **Human approval removes self-grading.** For projects that require it,
  `verified → approval_required` and a human approves/rejects in the Web UI/TUI.
  The Guild Master can never approve its own work.
- **Budgets are enforced host-side.** Strongly enforceable: wall-clock,
  concurrency, max-attempts, review-cycles, session lifecycle. Runner-dependent
  measurement: turn/token/exact cost — the *policy* still lives in the plane.
- **Steering is a typed op.** `steer_job(job, instruction)` recorded as a
  `SteeringEvent` (issuer, timestamp, delivery state, cancellable), delivered by
  the runner/hook layer at the next supported boundary — not an ad-hoc terminal
  injection.

## 7. Refinements added on top of the evaluation

The report's architecture is adopted as-is; these are the gaps worth closing
during build (they do not change the design, only sharpen V1):

1. **The artifact-capture problem (make it explicit in V1).** Agents don't
   reliably `git commit`, yet "the Artifact is a commit" depends on it. V1 needs a
   **Job wrapper** that pins `base_sha`, instructs/enforces a commit to
   `daedalus/<task>/<job>`, and captures `head_sha` — otherwise there is nothing
   to verify. (The wrapper can auto-commit the working tree at job end as a
   fallback.)
2. **Job vs. interactive session.** A dispatched Job and a human working the same
   project collide. V1's "one active job per project" must also exclude a live
   human session (or move to a per-job branch/worktree earlier than V3).
3. **The verifier needs the project's toolchain.** The verifier container is the
   project's image + a clean checkout — roughly doubling container use per
   verification. Acceptable, but note it (and reuse cached images).
4. **Two distinct gates, not one.** The report's **integration approval** (of code
   artifacts) is primary. The earlier idea of **roadmap-transition governance**
   (gating milestone/sprint edits) is a *small optional add-on* that reuses the
   same approval machinery (M15), not its own milestone.
5. **Scale honesty.** A control plane is a large expansion for a personal/small-
   team tool. The report's **V1 → V2 → V3** incrementalism is the de-risking, and
   the milestones below track it: V1 is deliberately minimal (SQLite, one job per
   project, reuse the coordinator, a simple clean-worktree verifier) — enough to
   prove the architecture before any governance or parallelism.

## 8. The milestone plan (M13–M17 = V1 → V2 → V3)

Each is a ~2-sprint unit, host-side testable (the actual container run is
host-only, as always). Suggested repo shape:
`cmd/daedalus-control`, `cmd/guild-control-mcp`,
`internal/control/{service,task,job,artifact,policy,verify,approval,store}.go`,
`internal/verifier`, `core/{task,policy,capabilities}.go`. The Guild Master
receives **only** `control.sock`.

### V1 — Minimal orchestration
- **M13 · Control Plane Foundation — the Job model.** `daedalus-control` service +
  Task/Job/Artifact model + SQLite store + early state machine
  (planned→queued→working→candidate); `guild-control-mcp` over `control.sock`
  (`create_task`/`dispatch_task`/`get_task`/`cancel_task`); the **Job wrapper**
  (pin base_sha → dispatch via coordinator → capture commit as Artifact); GM docs
  become projections. *One active job per project; reuse the coordinator session.*
- **M14 · Independent Verification & Frozen Acceptance.** The clean-worktree
  **verifier container** performing `candidate → verified`; the `daedalus verify`
  contract; **frozen `acceptance_policy@base_sha`**. *Makes "done" structural. The
  highest-leverage guarantee; runner-agnostic.*

### V2 — Governance
- **M15 · Governance — budgets, approval & integration.** Budget enforcement +
  request rejection; the human **approval → integration** state machine + Web/TUI
  approve/reject; retry/replan; an independent **reviewer** pass; the append-only
  **audit** log. Optional add-on: roadmap-transition governance (PM-opt-in). *Now
  Daedalus is a governed orchestrator.*

### V3 — Parallel programme execution
- **M16 · Parallel Programme Execution.** Multiple concurrent Jobs via isolated
  **git worktrees/branches** (one-owner-per-attempt); a job scheduler; a
  **cross-project task graph** + dependency scheduling, composing with
  `programmes`. *Now a true multi-agent programme-execution platform.*
- **M17 · Typed Steering & Coordination Polish.** `steer_job` as an audited,
  cancellable op delivered at a supported boundary; cross-project task-board views
  over control-plane state; uniform provenance/audit across tasks, jobs, steering,
  approvals.

## 9. Risks & decisions to evaluate

- **Runner coupling** — the core (Task/Job/Artifact + artifact verification) is
  **runner-agnostic** (a git commit + clean-checkout verify). Only *steering
  delivery* and any optional Stop-hook are runner-specific. Decide: are
  runner-specific niceties acceptable if the authority path is runner-agnostic?
  (Recommended: yes.)
- **The self-grading trap** — removed for *integration* by human approval, but if
  the Guild Master's own agent adjudicates a reviewer pass (M15), keep the
  reviewer independent and the human gate available. Decide which projects require
  a human.
- **Intrusiveness vs. adoption** — governance (M15) and steering (M17) change how a
  project agent is treated; keep them opt-in per project.
- **Scope-creep guardrail** — the invariant to hold across all five milestones:
  **boundary control + verified artifacts, never mid-turn puppeteering**; the
  Guild Master proposes, the plane adjudicates.
- **Is there a natural stop point?** V1 (M13–M14) alone already delivers the
  crown-jewel value — a real, un-fakeable definition of done and a dispatch/verify
  loop. V2/V3 are worth it only if programme-scale orchestration is actually
  wanted.

## 10. Non-goals

- No mid-turn / token-stream gating of an agent.
- No low-level privileged tools exposed to the Guild Master (`run_shell`,
  `docker_run`, `mount`, `git_exec`, `start_container`) — intent-level only.
- No authoritative state in any agent workspace.
- No dependency on an external orchestration framework — Daedalus *is* the
  substrate.
- No claim that more agents = better; orchestrate only parallelisable,
  well-specified work.

## 11. References

- **Evaluation / architecture source:** [`../daedalus-control-plane-report.md`](../daedalus-control-plane-report.md)
  (the control-plane design this plan adopts).
- **Research / evidence base:** [`guild-master-control.md`](guild-master-control.md)
  — 7-category control taxonomy, cooperative-vs-imposable analysis, MAST failure
  modes, per-platform control primitives (Magentic-One dual-ledger, Anthropic
  orchestrator-worker, A2A/MCP, Devin/OpenHands sessions), with sources.
- **Tracked in** `ROADMAP.md` (M13–M17, Planned; V1→V2→V3) and `BACKLOG.md`
  (#57–#64).
