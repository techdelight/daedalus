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
strictly better, safer design and is adopted wholesale below. The refinements it
left open are now **decided and folded into the design** — the execution model
(the Job wrapper, §5), the verifier and gate decisions (§6), and the V1-minimal
scoping (§8–§9); §7 records the provenance.

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

**How a Job produces its Artifact (the execution model).** Orchestration is
**Git-native** — `base_sha`, branches, worktrees, commits, rebase, and merge are
load-bearing, so a guild-managed project **must be a Git repository** (a stated
prerequisite, not a generic "artifact" abstraction). A Job runs the project's
agent through the coordinator inside a **Job wrapper** that pins `base_sha` and
runs in a **dedicated, isolated Git worktree** checked out clean at `base_sha` on
branch `daedalus/<task>/<job>` (under the Daedalus state dir, mounted as
`/workspace`) — **not** the developer's live checkout. Isolation here is an
*artifact-provenance* property, not a parallelism one: it guarantees the captured
commit contains **only** the Job's changes, never a developer's unrelated dirty
edits, an IDE's writes, or a build watcher's output. A **Job ends at process
exit** (a headless runner invocation is the execution boundary); the wrapper then
classifies the outcome and, separately, snapshots the tree:

- `execution_result` ∈ {success, failed, timeout, cancelled} — *how the run ended*.
- `output_snapshot` — the committed tree (`head_sha`), captured even on
  failure/timeout as a salvage snapshot.

**Only a `success` execution promotes its snapshot to a candidate Artifact.**
Commit-exists never implies job-succeeded. To start there is **one active Job per
project and no concurrent interactive session**; V3 relaxes this with multiple
worktrees. The Guild Master sees none of this — it created a Task and asked to
dispatch; the control plane resolves the project through the trusted registry and
constructs all execution.

## 6. The load-bearing guarantees (stated honestly)

The critical evaluation (`../guild-master-plan-critical-evaluation.md`) rightly
noted the first draft *overstated* several of these. The claims below are
deliberately narrowed to what the mechanism actually proves — the research behind
each is in §12.

- **Independent, reproducible verification (not "proof of done").** Verification
  never runs in the worker's live workspace. The control plane checks out the
  Artifact's commit into a **clean verifier container** and runs the project's
  verify policy (build + tests + `daedalus docs lint` + acceptance). This proves
  exactly one thing: *"this committed artifact, in this pinned environment, causes
  this procedure to report success"* — an **independently reproducible
  verification result**, not a proof the task is satisfied. It closes a real,
  distinct class of cheating (uncommitted/environment-resident state — stack
  introspection, live monkey-patches, dirty-tree passes). It does **not**, by
  itself, stop an agent that *committed* weakened tests (see the next bullet).
  Runner-agnostic: it checks a git commit, so it works for any runner. (The
  verifier is the project's image + clean checkout, ~2× container use per verify —
  reuse cached images.)
- **The acceptance oracle must live outside the agent's write scope.** Freezing
  and hashing the verify command at `base_sha` (`acceptance_policy@924ab7f`) stops
  the crudest self-grading — swapping `go test ./...` for `echo success`. It does
  **not** stop the agent editing the *tests/fixtures/build-config it commits* to
  make the frozen command pass vacuously — a measured, not hypothetical, failure
  (frontier agents do this in 30–100% of adversarial runs; §12). So a verification
  result is evidence of *completion* only when the oracle is beyond the agent's
  reach. The ladder, cheapest first: (1) reproducible clean-checkout verify →
  (2) **reject any Job whose diff touches the frozen test/acceptance files**
  (integrity gate — cheap, belongs in V1) → (3) **control-plane-supplied held-out
  acceptance tests** the worker never sees → (4) differential/property checks →
  (5) independent review → (6) human approval. Defense-in-depth; no single rung
  suffices.
- **Pinned execution environment (by digest).** The verifier image is pinned by
  **`sha256:` digest** captured at task creation, not a mutable tag like `:dev` —
  otherwise a rebuilt image verifies the artifact in a *different* environment than
  it was authored against. The policy on network / credentials / `/opt/tools` /
  caches in the verifier is **explicit** (default: network off or allowlisted, no
  ambient credentials, no inherited per-project tools) so verification is a
  decision, not an accident.
- **Project documents are untrusted input, not instructions.** The Guild Master
  reads project-controlled docs (README/VISION/ROADMAP/…) *and* holds intent-level
  action tools — the classic **lethal trifecta** (private data + untrusted content
  + an action vector), a documented, high-success attack class (§12). Its
  consequential operations are therefore **tiered**: read/status always allowed;
  create-bounded-task usually allowed; **cancel another Job, raise a budget, and
  request/perform integration are restricted — proposals a human (or a separate,
  non-doc-reading authority) confirms.** A poisoned README can *propose*, never
  *execute*. This — not prompt hardening — is the real defense.
- **Authoritative state is reconciled, not just stored.** SQLite gives atomicity
  *within the DB*; it cannot atomically wrap a DB write + `Coordinator.Start()` +
  `git worktree add` (the **dual-write problem**). So SQLite holds *desired* state
  as the single source of truth; containers/worktrees are derived, reconstructible
  state; and a **reconcile-on-boot + periodic loop** (the Kubernetes
  level-triggered controller pattern, minimal single-host form) drives reality
  toward desired state and repairs post-crash divergence — backed by
  **idempotent, deterministically-named** side-effects. This is what makes "crash
  recovery" a mechanism rather than optimistic language.
- **The plane can reject the Guild Master.** Budget too high → `REJECTED`. Artifact
  built from a stale base → `REJECTED, must rebase + re-verify`. The plane enforces
  policy; it does not merely execute.
- **Integration is a race-safe transaction, not a merge.** Two artifacts that each
  pass verification against base A can conflict when combined (a **semantic merge
  conflict**, no textual conflict). So landing is the **merge-queue pattern**:
  serialize → rebase onto the current target tip → **re-verify the *merged*
  result** (not the pre-merge branch) → **compare-and-swap** the target ref (retry
  if it moved). Human approval, where required, gates before the swap — so the
  Guild Master can never approve its own work.
- **Budgets are enforced host-side.** Strongly enforceable: wall-clock,
  concurrency, max-attempts, review-cycles, session lifecycle. Runner-dependent
  *measurement*: turn/token/exact cost — the *policy* still lives in the plane.
- **Event history, honestly named.** The control plane keeps a
  **control-plane-managed event log** — immutable *through the API* (agents can't
  alter history), not cryptographically tamper-proof unless events are later
  hash-chained (an optional V2+ property, not oversold as "tamper-proof audit").

## 7. Provenance & revision history

- **Round 1** adopted the control-plane architecture from
  [`../daedalus-control-plane-report.md`](../daedalus-control-plane-report.md)
  wholesale (authority/initiative split, `control.sock` boundary, Task/Job/Artifact,
  host-side SQLite, independent verification, frozen acceptance policy, human
  approval).
- **Round 2** (this revision) responds to a critical evaluation
  ([`../guild-master-plan-critical-evaluation.md`](../guild-master-plan-critical-evaluation.md)),
  pressure-tested against the literature (§12). It **narrows the guarantee
  language** (§6), and moves several things earlier: **isolated worktrees and
  reconciliation into M13**, **digest-pinning and a test-integrity gate into M14**,
  the **integration transaction into M15**, a **human-first CLI path before the
  Guild Master client**, and **prompt-injection defense** as a first-class concern.
  M17 (typed steering) is **demoted** toward backlog until real use proves it.

This plan supersedes the pre-control-plane targets and milestone arc in
[`guild-master-control.md`](guild-master-control.md) §5–§6, which remain valid only
as the research "why".

## 8. The milestone plan (M13–M17 = V1 → V2 → V3)

Each is a ~2-sprint unit, host-side testable (the actual container run is
host-only, as always). Suggested repo shape: `cmd/daedalus-control`,
`cmd/guild-control-mcp`,
`internal/control/{service,task,job,artifact,policy,verify,approval,reconcile,store}.go`,
`internal/verifier`, `core/{task,policy,capabilities}.go`. **The overriding V1 goal
is not features — it is a boringly reliable path** that survives daemon crashes,
dirty workspaces, agent crashes, malicious project docs, changed images, stale
bases, edited tests, duplicate dispatch, partial artifacts, restarts, and timeouts
without losing track of what happened.

### V1 — Minimal, reliable orchestration (human-driven first)
- **M13 · Control Plane Foundation + the deterministic CLI path.**
  `daedalus-control` daemon + SQLite (durable *desired* state) + Task/Job/Artifact
  model + `control.sock`; **isolated Git worktree per Job** (Git required) +
  headless Job (process-exit boundary) + `execution_result` vs `output_snapshot`
  (only success → candidate); **reconcile-on-boot + periodic loop** with idempotent,
  deterministically-named side-effects. The **only client is a human CLI** —
  `daedalus task create|dispatch|status|cancel|verify` — the deterministic
  reference path that makes the plane useful at N=1 and isolates bugs to one layer
  before any agent drives it. *One active Job per project; no parallelism yet; no
  Guild Master client yet.*
- **M14 · Independent Verification (oracle outside the agent's reach).** The clean
  verifier container performing `candidate → verified`; **image pinned by digest** +
  an explicit network/creds/`/opt/tools` policy; **frozen `acceptance_policy@base_sha`**
  *plus a **test-integrity gate** that rejects any Job whose diff touches the frozen
  test/acceptance files*; a null-agent floor check. Output is an *independently
  reproducible verification result*, never "proven correct". *Highest-leverage
  guarantee; runner-agnostic.*

### V2 — Governance & the Guild Master as a (gated) client
- **M15 · Governance, integration & the Guild Master client.** Budgets + request
  rejection; **integration as a race-safe transaction** (serialize → rebase →
  re-verify the merged result → compare-and-swap the ref); the human
  `verified → approval_required → approved → integrated` state machine + Web/TUI
  approve/reject; retry/replan; an independent **reviewer** pass; a
  control-plane-managed **event log**. **Now the Guild Master joins as a client**
  via `guild-control-mcp` — reusing the exact CLI capabilities, but with
  **tiered, injection-safe authority** (read/status free; create-bounded-task
  usually allowed; cancel/raise-budget/request-integration are proposals a human
  confirms; project docs treated as untrusted). Optional add-on: roadmap-transition
  governance (PM-opt-in) reusing the approval machinery. *Now a governed
  orchestrator an agent can safely drive.*

### V3 — Parallel programme execution
- **M16 · Parallel Programme Execution.** Multiple concurrent Jobs — the worktrees
  already exist from M13, so this *adds concurrency, scheduling and a dependency
  graph — no new execution machinery*: a job scheduler + a **cross-project task
  graph** with dependency scheduling, composing with `programmes`. (The runner,
  worktree, verifier and container paths are genuinely untouched; the **state
  machine is not** — M16 added the `blocked` state, three plane-only edges, and a
  plane-owned dependency table.)
- **M17 · Typed Steering (demoted — validate before building).** `steer_job` as an
  audited, cancellable op delivered at a supported boundary. Kept Planned but
  low-priority: for short Jobs, **cancel + redispatch with corrected instructions**
  may suffice, so live steering should prove its value in real use before becoming
  a milestone (candidate to move to BACKLOG).

## 9. Risks & decisions to evaluate

- **Runner coupling** — the whole authority path (Task/Job/Artifact + clean-checkout
  verify + reconciliation) is **runner-agnostic** (git + containers). Only *steering
  delivery* and any optional Stop-hook nudge are runner-specific. Accept
  runner-specific niceties only where the authority path stays agnostic.
- **The self-grading trap** — narrowed but never fully closed: the test-integrity
  gate stops committed-test edits, held-out tests move the oracle out of reach, and
  human approval gates integration — but tests remain an incomplete oracle (§12), so
  "verified" is evidence, not proof. Decide which projects require a human gate.
- **Prompt injection** — the sharpest security concern (§12): keep consequential
  Guild-Master ops as human-confirmed proposals and never co-locate "reads untrusted
  docs" with "un-gated action" in one agent.
- **Scope discipline in M13** — resist overloading the foundation; the CLI path +
  worktree + reconciliation are the point, *not* breadth. Boring reliability comes
  from small surface area.
- **Intrusiveness vs. adoption** — governance and steering change how a project is
  treated; keep them opt-in per project.
- **Natural stop point** — **V1 (M13–M14) alone earns the control plane's keep**:
  a human gets reliable, isolated, reproducibly-verified job execution at N=1, with
  no agent orchestration at all. V2/V3 are worth it only if programme-scale, agent-
  driven orchestration is actually wanted. This CLI-first framing also answers "is a
  control plane justified for a small tool?" — yes, because it's useful before the
  guild exists.

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

## 12. Response to the critical evaluation

Point-by-point disposition of
[`../guild-master-plan-critical-evaluation.md`](../guild-master-plan-critical-evaluation.md),
after pressure-testing each claim against the literature. Verdict up front: the
review is strong — of its 13 points, **11 are adopted** (several because the
evidence is worse than the reviewer stated), and 2 are adopted-with-a-push-back.

| # | Reviewer's point | Disposition |
|---|------------------|-------------|
| 1 | Worktrees are provenance, not parallelism → M13 not V3 | **Adopted.** Isolated worktree per Job in M13 (§5). Correct: without it, an auto-commit in a dirty checkout attributes unrelated human edits to the Job. |
| 2 | "Un-fakeable done" is too strong | **Adopted, harder.** Reframed to "independently reproducible verification result" (§6). Evidence is damning: frontier agents edit tests / patch `conftest.py` / `sys.exit(0)` / hardcode outputs in **30–100%** of adversarial runs (Anthropic, OpenAI, METR, ImpossibleBench, Berkeley RDI). |
| 3 | Verifier environment not frozen (pin image digest; define /opt/tools, net, creds) | **Adopted.** Digest-pin + explicit env policy in M14 (§6). Standard supply-chain practice (mutable tags are overwritable). |
| 4 | SQLite ≠ distributed atomicity (dual-write) | **Adopted.** Reconcile-on-boot + periodic loop + idempotency in M13 (§6) — the K8s level-triggered controller pattern in minimal single-host form. |
| 5 | Define exactly when a Job ends | **Adopted.** Job = headless invocation; **process exit** is the boundary; classify outcome (§5). |
| 6 | Auto-commit is capture, not success | **Adopted.** `execution_result` vs `output_snapshot`; only success → candidate (§5). |
| 7 | Git is now a requirement — say so | **Adopted.** Stated Git-native prerequisite; no generic artifact abstraction (§5). |
| 8 | Prompt injection missing from the threat model | **Adopted — the sharpest catch.** The Guild Master is a textbook **lethal trifecta**; real CVEs exist (Cursor CurXecute, GitHub-MCP; 41–84% attack success). Docs are untrusted; consequential ops are human-confirmed proposals; tiered authority (§6). |
| 9 | Integration is harder than "merge the commit" | **Adopted.** Integration = merge-queue transaction (rebase → re-verify merged → CAS) in M15 (§6) — the standard fix for semantic/logical merge conflicts. |
| 10 | "Append-only audit log" over-claims | **Adopted.** Renamed "control-plane-managed event log"; hash-chain optional V2+ (§6). |
| 11 | Move worktrees + reconciliation earlier | **Adopted** (see #1/#4) — but see push-back below on M13 scope. |
| 12 | Give the control plane human CLI clients too | **Adopted, and elevated** — the review's best single idea. M13 is now **CLI-first, human-only**; the Guild Master client is deferred to M15. A deterministic reference path both eases debugging *and* answers "is a control plane worth it at N=1?" — yes. |
| 13 | Repo tracking out of sync (M13–M17 not committed) | **Was true at review time; now resolved** — M13–M17 + #57–#64 were committed (`d435b64`) before this response. A fitting irony for a plan about state/reality divergence. |

**Two push-backs (being cynical about the cynic):**

- **M13 scope vs. the reviewer's own "boring reliability" plea.** The review loads
  M13 with daemon + SQLite + Task/Job/Artifact + `control.sock` + `guild-control-mcp`
  + worktree + headless job + reconciliation + capture — then closes by demanding
  the path be *boringly reliable*. Those pull against each other; boring reliability
  comes from **small surface area**. Resolution: keep the worktree + reconciliation
  in M13 (they're load-bearing for provenance/recovery) but **cut the Guild Master
  MCP client out of M13 entirely** — M13 is human-CLI-only, the agent client lands
  in M15. This is *the review's own #12 defusing its own #8*: with no agent client
  in V1, the prompt-injection surface doesn't exist yet, and the foundation stays
  small.

- **Don't let the (correct) deflation of verification breed defeatism.** The review
  is right that "passes tests" ≠ "correct" — and the reward-hacking data makes it
  *more* right. But the same data is the strongest argument *for* the gate, not
  against it: when agents fake success 30–100% of the time, an
  *independently-reproducible verification against an oracle the agent can't edit*
  is a categorical improvement over "the agent said done." The limits set the honest
  ceiling ("verified", never "proven correct"); they don't diminish the value. And
  the highest-value defense — a **test-integrity gate + held-out tests** — is cheap
  enough to sit in V1 (M14), earlier than the review's "later".

**New-evidence sources (round 2).** Reward hacking / test-gaming: Anthropic
*Natural Emergent Misalignment from Reward Hacking* ([arXiv:2511.18397](https://arxiv.org/abs/2511.18397));
OpenAI *Monitoring Reasoning Models for Misbehavior* ([arXiv:2503.11926](https://arxiv.org/abs/2503.11926));
[METR — frontier models are reward hacking](https://metr.org/blog/2025-06-05-recent-reward-hacking/);
*ImpossibleBench* ([arXiv:2510.20270](https://arxiv.org/abs/2510.20270));
[Berkeley RDI — how we broke top agent benchmarks](https://rdi.berkeley.edu/blog/trustworthy-benchmarks-cont/);
*Are "Solved Issues" in SWE-bench Really Solved Correctly?* ([arXiv:2503.15223](https://arxiv.org/abs/2503.15223)).
Prompt injection: [Willison — the lethal trifecta](https://simonwillison.net/2025/Jun/16/the-lethal-trifecta/);
Cursor CVE-2025-54135; [CSA — README injection](https://cloudsecurityalliance.org/).
Reliability: [Kubernetes controllers](https://kubernetes.io/docs/concepts/architecture/controller/);
[transactional outbox](https://microservices.io/patterns/data/transactional-outbox.html);
[the dual-write problem](https://www.confluent.io/blog/dual-write-problem/). Merge:
GitHub merge queue / Zuul / Bors; *RefFilter* semantic-conflict detection
([arXiv:2510.01960](https://arxiv.org/pdf/2510.01960)). Image pinning:
[Bazel hermeticity](https://bazel.build/basics/hermeticity); pin-by-digest.
