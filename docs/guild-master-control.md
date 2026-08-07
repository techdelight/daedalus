# The Guild Master as a Controlling Entity — Research & Milestone Plan

*Status: proposal. Research-backed targets and a milestone arc for evolving the
Guild Master (M12) from a **read-only programme overseer** into a **controlling
entity** over the guild of projects. Nothing here is built yet; the milestones
below are candidates, not the active roadmap.*

## 1. The control question, stated honestly

Daedalus launches each project's agent as its **own autonomous CLI** (Claude
Code / Copilot CLI) inside a hardened container. It **offers** tools and
**configures** the runtime; it does **not** sit inside the agent's turn loop. So
"controlling entity" cannot mean an in-process supervisor graph that intercepts
token streams (LangGraph/CrewAI style). It must be assembled from the levers
Daedalus actually owns.

The decisive axis from the research is **cooperative/instrumented control vs.
externally-imposable control**:

- **Cooperative** control (handoff tools, shared graph state, `interrupt()`
  checkpoints, in-code guardrails) requires the worker to be *built into* the
  orchestration framework. It cannot be imposed on an arbitrary autonomous agent.
- **Externally-imposable** control (session lifecycle over a sandbox, resource
  caps, verification via CI on the artifact, the PR/merge gate, host-mediated
  permissions) is enforced **regardless of the worker's internals** — the control
  plane owns the sandbox, the gate, or the runtime.

A Guild Master over independent projects must build its **hard guarantees on the
externally-imposable column**, and treat cooperative mechanisms as an
optimization available only because Daedalus *is* the runtime substrate (see §4).

## 2. What the field does — seven categories of control

Synthesized from academic, platform, and practitioner research (§8). Every
production system's control surface decomposes into these seven:

| # | Category | How platforms do it | Externally imposable? |
|---|----------|---------------------|-----------------------|
| A | **Lifecycle** (start/stop/pause a worker) | In-graph node turns (LangGraph, CrewAI, AutoGen `max_turns`); **session over a sandbox** (Devin/OpenHands: create → poll `status` → suspend/kill; cost caps `max_acu_limit`; concurrency auto-pause) | **Yes** — sandbox lifecycle needs no worker cooperation |
| B | **Task dispatch** (send task, get result) | Blocking tool-call (CrewAI delegation, OpenAI `as_tool()`); fan-out+await (LangGraph `Send`, Anthropic parallel subagents); **async dispatch→poll→artifact** (Devin/OpenHands sessions, A2A `message/send`→`tasks/get`→`Artifact`) | Partly — the async-artifact shape fits long coding jobs |
| C | **Handoff semantics** | **Replace** (control leaves supervisor: `transfer_to_X`, LangGraph `Command(goto)`) vs **Return** (control comes back: CrewAI delegation, `as_tool()`, Anthropic lead, Bedrock supervisor, **Devin coordinator**) | A Guild Master wants **Return** — dispatch, collect, decide, stay authoritative |
| D | **Gating / approval** | Interrupt-and-resume (LangGraph `interrupt()`, A2A `input-required`, OpenHands `WAITING_FOR_CONFIRMATION`); validation-gate-with-retry (CrewAI `guardrail`, OpenAI tripwires); **permission chokepoint (Anthropic hooks: deny beats bypass)**; **human PR-merge gate** | **Yes**, at boundaries the controller owns (merge gate, session-start, tool-call seam) |
| E | **Shared state** | Blackboard (LangGraph reducer state, AutoGen transcript+ledgers); explicit output hand-off (CrewAI `TaskOutput`, A2A `contextId`); **isolate + summarize-return** (Anthropic subagents, Devin VMs) | Daedalus already has this: **files/docs as the durable blackboard** |
| F | **Verification** (how "done" is confirmed) | Self-report / LLM-judge; dedicated verifier agent (Anthropic `CitationAgent`); **external ground truth — tests + CI on the PR** | **Yes, and highest-leverage** — the only category that doesn't trust the worker's self-assessment |
| G | **Protocol** (control across a boundary) | In-process library; **MCP** (vertical, agent→tool); **A2A** (horizontal, agent→agent: Agent Cards + `Task` state machine + Artifacts); vendor REST session API | MCP is imposable host-side; A2A needs a cooperative-to-spec worker |

**Two proven skeletons worth copying wholesale:**

- **Magentic-One's dual ledger** ([arXiv:2411.04468](https://arxiv.org/abs/2411.04468)):
  an outer-loop **Task Ledger** (facts given / to-find / to-derive / guesses + a
  step→agent plan) and an inner-loop **Progress Ledger** regenerated every step
  (`is_request_satisfied`, `is_in_loop`, `is_progress_being_made`, `next_speaker`,
  `instruction`), with a **stall counter → forced re-plan** (shipped default
  `max_stalls=3`). This is the single most directly reusable supervisor skeleton.
- **Devin "manages Devins"** / OpenHands sessions: model each worker as a
  **session over an isolated sandbox** you create/poll/suspend/kill, delivering a
  **CI-verified PR** as the artifact of record, with a human as the final merge
  gate. This is *exactly* Daedalus's coordinator-owns-sessions shape.

## 3. Failure modes to design against

From **MAST — "Why Do Multi-Agent LLM Systems Fail?"**
([arXiv:2503.13657](https://arxiv.org/abs/2503.13657), 150+ traces, 7 frameworks):

- **44% of failures are the *controller's* fault** — bad/violated spec, **step
  repetition (15.7%)**, **unaware of termination (12.4%)**.
- **~24% are verification failures** — "declared done" ≠ "verified done"
  (no/incomplete verification 8.2%, incorrect verification 9.1%).
- **Structural/topology fixes beat prompt-engineering** (ChatDev +15.6pp from
  enforced hierarchy + multi-level verification vs +9.4pp from prompt tweaks) —
  **and even then, gains stay modest.** There is no cheap fix.

Practitioner corroboration (Anthropic harness team; Osmani; Kilo; battyterm):
agents "declare victory" before a feature works end-to-end; a prompt instruction
("always run tests") is *a hope, not a gate*; the fix is a mechanism the agent
**cannot skip** (Stop/pre-push hook, CI exit-code gate). One-file-one-owner via
worktree/branch/container isolation; plan-approval before code; a **separate
review agent** ("the author is the worst reviewer"); context rot at ~60% fill →
budgets + kill/rollback; durable file+git handoff over agent memory.

Verdict across all three streams: **"coherence through orchestration, not
autonomy."** A controlling manager pays off only when it (1) owns disjoint,
review-sized, well-specified tasks over *parallelizable* work, (2) imposes an
un-skippable machine-checked definition of done, (3) enforces structural
isolation, and (4) manages context/concurrency budgets. It *underperforms* a lone
strong agent on ambiguous specs, coupled/sequential work, and security-semantic
risks tests miss.

## 4. Where Daedalus already stands (its control assets)

Daedalus is unusually well-positioned because it already owns the two
hardest-won primitives the practitioner literature begs for:

- **Per-project container isolation** — the strongest form of "one file, one
  owner"; concurrency is already safe.
- **File-derived state (M7)** — ROADMAP/SPRINTS/VERSION as the durable-artifact
  blackboard Anthropic's harness team advocates (files + git over agent memory).
- **The coordinator owns session lifecycles** — this *is* the Devin/OpenHands
  "session over a sandbox" model. Start/stop already exist; **externally-imposable
  lifecycle control is a small step away.**
- **MCP substrate** — `project-mgmt-mcp` (write, with `ValidateWrite` gates),
  `guild-mcp` (M12, read-only cross-project visibility). The vertical tool layer
  is in place.
- **Headless `-p` runs** (task-dispatch primitive) and **runner PTY input
  injection** (steering primitive) already exist.
- **`activity.json`** — a busy/idle signal (progress-introspection input).

**The key realization the research surfaces:** Daedalus *is* the runtime
substrate. It writes the container's Claude Code `settings.json` / hooks (see
`entrypoint.sh`). Claude Code's permission chain is **hooks → deny → ask → mode →
allow → callback, where `deny` beats even bypass mode**. That means Daedalus can
**impose `PreToolUse`/`Stop` hooks into a project's agent** — an *externally-
imposable* gate over an in-runtime worker. This meaningfully revives the
"gatekeeper" idea abandoned earlier (when the conclusion was "we can only offer
MCPs"): we can't gate arbitrary tokens, but we **can** gate at tool-call and
stop boundaries via hooks we inject. (Claude-runner-specific; Copilot may lack
equivalents — treat as a runner capability, like `guild-mcp`.)

**What's missing** for control: a machine-checked verify gate; a Guild-Master→
project task-dispatch/artifact API; boundary approval gates; a programme-level
task+progress ledger; a non-destructive steering channel; a cross-project
task/status contract.

## 5. Targets — capabilities the Guild Master needs to control

| ID | Target | Grounded in | Imposable? |
|----|--------|-------------|------------|
| **T1** | **Machine-checked verification gate.** A project's work is never "done" by self-report: a project-declared check (`daedalus verify` = build + tests + `docs lint`) whose **exit code** is the gate, enforced as an injected Claude Code **Stop-hook** so the agent can't stop on red. Guild Master reads verify status. | MAST FC3; practitioner CI-as-gate; Anthropic hooks | **Yes** (hook + exit code) |
| **T2** | **Lifecycle command + budgets.** Guild Master can start/stop/pause any project's session via the coordinator, with concurrency caps, wall-clock/turn/cost budgets, and auto-pause. | Devin/OpenHands sessions; context-rot budgets | **Yes** (coordinator owns the sandbox) |
| **T3** | **Task dispatch → artifact.** Hand a well-specified task to a project (headless run or session injection); collect a **durable artifact** (branch/commit/PR + structured status) via **async dispatch→poll→artifact**, **Return** semantics (GM stays authoritative). | A2A task lifecycle; Devin coordinator; `as_tool()` | Partly (dispatch imposable; quality not) |
| **T4** | **Programme Task Ledger + Progress Ledger.** A persistent plan/facts store in the GM workspace + a per-cycle progress check (which projects satisfied? looping? progressing? what next?) with a **stall→replan** escape and explicit budgets/termination. | Magentic-One; MAST (step-repetition, termination) | **Yes** (GM-side state) |
| **T5** | **Boundary approval gates (PM-governed).** Approval the GM/human owns at seams Daedalus *can* gate: milestone/sprint transitions (extend `ValidateWrite` + a `PreToolUse` hook on `project-mgmt-mcp` writes), session-start, and the **merge/integration gate**. An interrupt-state the controller owns. Opt-in per project ("PM enabled"). | A2A `input-required`; OpenHands confirmation; PR-merge gate; Anthropic hooks | **Yes** at boundaries (not mid-turn) |
| **T6** | **Independent review pass.** A dedicated reviewer step (a review agent and/or LLM-judge over the diff + a `docs lint`/tests check) gating integration — "the author is the worst reviewer." | Anthropic `CitationAgent`; Kilo review agent; MAST | Partly |
| **T7** | **Non-destructive steering channel.** Deliver a priority/steering message to a running project agent **at a tool-call boundary** (runner injection + a `PreToolUse` hook that surfaces queued steering), instead of a destructive `Ctrl-C`. | Claude Code steering thread; OpenHands pause | **Yes** (runner + hook) |
| **T8** | **Cross-project coordination contract.** An internal A2A-style task/status/artifact contract: projects "advertise" via their docs + activity (Agent-Card analog); the GM dispatches a `Task` with a lifecycle (`submitted→working→input-required→done/failed`) and collects artifacts; a shared task board for cross-project dependencies. Built on the coordinator API + MCP (not necessarily full A2A). | A2A; blackboard; `programmes` topology | Partly |

## 6. Proposed milestone arc

Sequenced by **leverage and dependency** — verification first (MAST's highest-
leverage, and the foundation the rest gate on), then the imposable lifecycle
layer, then dispatch, then the softer coordination/approval layers. Each is a
2-sprint milestone in the established rhythm; all are host-side-testable with the
usual "the container run is host-only" caveat.

- **M-A · The Verify Gate (T1, T6).** `daedalus verify` contract (a project
  declares its build/test/lint check); a Daedalus-injected Claude Code **Stop-hook**
  so a project agent cannot self-declare done on red; `guild-mcp`/coordinator
  expose per-project verify status; an optional review-agent pass. *Foundation —
  makes "done" mean something. Highest leverage per MAST.*

- **M-B · Lifecycle Command & Budgets (T2).** Guild-Master control tools
  (`start`/`stop`/`pause_project`) over the coordinator; concurrency caps + wall-
  clock/turn budgets + auto-pause; surfaced in the Guild view (the crowned hero
  can command the party). *Pure externally-imposable control; builds on the
  coordinator it already has.*

- **M-C · Task Dispatch & the Programme Ledger (T3, T4).** A GM→project dispatch
  tool (headless run / session injection) returning a durable artifact (branch +
  status); a **Task Ledger + Progress Ledger** in the GM workspace with
  stall→replan and budgets/termination (the Magentic-One skeleton, doc-driven).
  *Turns visibility into orchestration.*

- **M-D · Boundary Gates & Approval (T5).** PM-governed projects: milestone/
  sprint transitions and merges require GM/human approval, via `ValidateWrite`
  extensions + a `PreToolUse` hook on `project-mgmt-mcp` writes; an interrupt-
  state the GM owns; opt-in at install ("PM enabled", default off). *The
  gatekeeper, done at the seams Daedalus can actually gate.*

- **M-E · Coordination & Steering (T7, T8).** A non-destructive steering channel
  (runner injection + `PreToolUse` surfacing); an internal task/status/artifact
  contract + a cross-project task board for dependencies (composing with
  `programmes`). *The horizontal coordination layer.*

## 7. Honest constraints & non-goals

- **No mid-turn token gating.** Daedalus cannot pause an agent between tokens.
  All gating is at **boundaries** — tool calls (`PreToolUse`), stop (`Stop`),
  session start, and merge. This is a real limitation, not hidden.
- **Runner-specific.** Hook-based gates (T1/T5/T7) depend on Claude Code's hook
  system; Copilot CLI may lack equivalents. Treat as a runner capability, gated
  like `guild-mcp`, with graceful degradation.
- **Opt-in.** PM-governance (T5) is intrusive; keep it **off by default** and
  per-project opt-in — most projects want autonomy, not a gatekeeper.
- **Multi-agent is not automatically better.** Research shows 39–70% degradation
  on sequential tasks vs +81% on parallelizable ones. The GM should orchestrate
  only genuinely parallel, well-specified work; its real product is
  **decomposition, specs, and gates**, not spawning.
- **Never a token-stream controller, never a task-dispatcher that bypasses the
  agent's autonomy.** The GM stays a *supervisor by visibility + boundary
  control*, not an in-process puppeteer.

## 8. Sources

**Academic:** MetaGPT [2308.00352](https://arxiv.org/abs/2308.00352) · ChatDev
[2307.07924](https://arxiv.org/abs/2307.07924) · AutoGen
[2308.08155](https://arxiv.org/abs/2308.08155) · Magentic-One
[2411.04468](https://arxiv.org/abs/2411.04468) · CAMEL
[2303.17760](https://arxiv.org/abs/2303.17760) · AgentVerse
[2308.10848](https://arxiv.org/abs/2308.10848) · HuggingGPT
[2303.17580](https://arxiv.org/abs/2303.17580) · **MAST** failure taxonomy
[2503.13657](https://arxiv.org/abs/2503.13657) · Collaboration-mechanisms survey
[2501.06322](https://arxiv.org/abs/2501.06322) · Communication-centric survey
[2502.14321](https://arxiv.org/abs/2502.14321) · Orchestration survey
[2601.13671](https://arxiv.org/abs/2601.13671) · Interoperability-protocols survey
[2505.02279](https://arxiv.org/abs/2505.02279) · ReVeal self-verification
[2506.11442](https://arxiv.org/abs/2506.11442).

**Platform docs:** LangGraph supervisor/`Command`/`interrupt`/persistence ·
CrewAI hierarchical process + delegation tools + guardrails · AutoGen
Selector/Swarm + Magentic-One orchestrator · OpenAI Agents SDK handoffs/`as_tool`/
guardrails · Anthropic [multi-agent research system](https://www.anthropic.com/engineering/multi-agent-research-system)
+ [Claude Code subagents](https://code.claude.com/docs/en/sub-agents)/hooks/
permissions · [MCP spec](https://modelcontextprotocol.io/specification/2025-06-18/architecture)
· Amazon Bedrock multi-agent collaboration · Google [A2A protocol](https://a2a-protocol.org/latest/specification/)
· Devin [sessions API](https://docs.devin.ai/api-reference/v1/sessions/create-a-new-devin-session)
+ ["Devin manages Devins"](https://cognition.com/blog/devin-can-now-manage-devins) ·
[OpenHands Cloud API](https://docs.openhands.dev/openhands/usage/cloud/cloud-api).

**Practitioner:** Anthropic [effective harnesses for long-running agents](https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents)
· Osmani [The Code Agent Orchestra](https://addyosmani.com/blog/code-agent-orchestra/)
· Kilo [7 engineers, 20 parallel agents](https://blog.kilo.ai/p/how-7-kilo-code-engineers-run-up)
· battyterm [5 lessons](https://dev.to/battyterm/5-lessons-from-running-ai-coding-agents-in-parallel-53on)
/ [supervising agents](https://dev.to/battyterm/how-to-supervise-ai-coding-agents-without-losing-your-mind-53m4)
· O'Reilly [Conductors to Orchestrators](https://www.oreilly.com/radar/conductors-to-orchestrators-the-future-of-agentic-coding/)
· [quality-gates via Stop hooks](https://fbakkensen.github.io/ai/devtools/development/2026/03/27/quality-gates-for-coding-agents-how-stop-hooks-make-validation-mandatory.html)
· [Claude Code steering thread #30492](https://github.com/anthropics/claude-code/issues/30492).
