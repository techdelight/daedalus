# Daedalus Control Plane Proposal

## Executive Summary

Daedalus should introduce a **host-side orchestration control plane** that sits above the existing coordinator.

The coordinator should remain responsible for **session lifecycle**: starting, stopping, listing, and tracking project containers and agent sessions.

The new control plane should become the authoritative layer for:

- tasks
- jobs
- policies
- budgets
- verification
- approvals
- artifacts
- orchestration state
- audit history

The key architectural rule is:

> **The Guild Master can request privileged actions, but must never perform privileged actions directly.**

The Guild Master remains the reasoning and planning agent. The control plane decides whether its requests are permitted and executes them through trusted host-side mechanisms.

This creates a clean separation:

> **The Guild Master has initiative. The control plane has authority.**

---

# 1. High-Level Architecture

```text
                        Human
                          │
                  approve / cancel / inspect
                          │
                          ▼
┌─────────────────────────────────────────────────────┐
│              DAEDALUS CONTROL PLANE                 │
│                     host-side                       │
│                                                     │
│  Tasks       Jobs        Policies      Artifacts    │
│  Budgets     Verify      Approvals     Audit Log    │
│                                                     │
│            authoritative state lives here           │
└───────────┬───────────────────┬─────────────────────┘
            │                   │
            │ lifecycle         │ clean verification
            ▼                   ▼
     Coordinator             Verifier
            │
            ▼
     Project containers
            ▲
            │
            │ task/result
            │
┌───────────┴────────────┐
│      Guild Master      │
│                       │
│ plan / decompose /     │
│ dispatch / evaluate    │
└────────────────────────┘
```

The Guild Master should reason freely about the programme, but host-side Daedalus infrastructure must remain the trusted enforcement boundary.

---

# 2. Guild Master → Control Plane Interface

The Guild Master currently has `guild-mcp` for cross-project visibility.

The next step should be a second MCP server, for example:

```text
guild-control-mcp
```

This MCP should expose **intent-level operations**, such as:

```text
create_task(
    project,
    objective,
    acceptance_criteria,
    budget
)

dispatch_task(task_id)

get_task(task_id)

cancel_task(task_id)

request_verification(task_id)

request_integration(task_id)

list_pending_tasks()

list_blocked_tasks()
```

It should **not** expose low-level privileged mechanisms such as:

```text
run_shell(command)

docker_run(args)

mount(path)

git_exec(args)

start_container(image, path, ...)
```

The Guild Master should describe **what it wants done**, not **how the host should do it**.

For example:

```text
GOOD

dispatch_task(
    project="daedalus-web",
    objective="Add dark mode..."
)
```

Not:

```text
BAD

docker compose run \
    -v /home/user/foo:/workspace \
    ...
```

The control plane should resolve the project through Daedalus's trusted registry and construct all execution details itself.

This prevents the Guild Master from becoming a privileged remote shell.

---

# 3. The Control Plane Is the Security Boundary

The Guild Master should never receive direct access to the coordinator's privileged host API.

Instead:

```text
Guild Master
     │
     │ constrained requests
     ▼
guild-control-mcp
     │
     ▼
control.sock
     │
     ▼
Daedalus Control Plane
     │
     ▼
coordinator.sock
```

The control plane should validate every request against:

- known project identities
- current task state
- budgets
- policies
- approval requirements
- runner capabilities
- artifact provenance
- verification state

The MCP layer is an ergonomic interface.

The actual security boundary is the **control-plane API itself**.

Even if an agent bypasses MCP and communicates directly with `control.sock`, it should gain no additional authority because that socket exposes only constrained, policy-checked operations.

---

# 4. Core Data Model: Task, Job, Artifact

The control plane should explicitly distinguish three concepts:

```text
Task
    what should be accomplished

Job
    one attempt to accomplish it

Artifact
    the durable result of that attempt
```

Example:

```text
Task T-104
  project: compiler
  objective: implement incremental parser
  acceptance policy: AP-7

        │
        ▼

Job J-221
  base: 924ab7f
  runner: claude
  budget: 30 min
  status: working

        │
        ▼

Artifact A-309
  base_sha: 924ab7f
  head_sha: 6ab71cd
  branch: daedalus/T-104/J-221
  verify: passed
  review: passed
```

A task may require multiple attempts:

```text
T-104
 ├── J-221  failed
 ├── J-222  timed-out
 └── J-223  succeeded
                 │
                 ▼
              A-311
```

This distinction is important because failure, retry, timeout, and re-planning are normal orchestration events.

A session should therefore not be treated as the primary unit of orchestration.

The **Job** should be.

---

# 5. Authoritative State Must Live Host-Side

The Guild Master's workspace may contain useful programme-level documents such as:

```text
TASKS.md
STATUS.md
PROGRESS.md
```

But these should not be the authoritative system of record.

Instead:

```text
Control-plane DB
       │
       ├── task T-104
       ├── job J-223
       ├── artifact A-311
       ├── verify PASS
       └── integration pending
              │
              ▼
       Guild Master reads it
              │
              ▼
      optionally renders:
      TASKS.md / STATUS.md
```

The Guild Master's documents become **views or projections** of control-plane state.

This prevents the controlling agent from modifying the state that determines whether its own work is considered valid.

---

# 6. Persistence

A lightweight host-side SQLite database would be a good fit.

Suggested tables:

```text
tasks
jobs
artifacts
verification_runs
approvals
events
```

SQLite provides:

- atomic state transitions
- transactional updates
- crash recovery
- querying
- durable history
- simple deployment
- no external infrastructure dependency

The database should be owned by Daedalus, not by any agent workspace.

---

# 7. Task State Machine

Tasks should follow a strict state model controlled by Daedalus.

For example:

```text
planned
   │
   ▼
queued
   │
   ▼
working ───────────────┐
   │                   │
   │                   ▼
   │              input_required
   │                   │
   └───────────────────┘
   │
   ▼
candidate
   │
   ▼
verifying
   │
   ├──── FAIL ──► rejected ──► retry/replan
   │
   ▼
verified
   │
   ▼
approval_required
   │
   ▼
approved
   │
   ▼
integrated
```

Terminal states could include:

```text
failed
cancelled
expired
integrated
```

The worker agent may declare:

> I think the task is complete.

But this should only result in:

```text
working → candidate
```

Only Daedalus should be able to perform:

```text
candidate → verified
```

That distinction makes verification structural rather than conversational.

---

# 8. Verification Must Be Independent

Verification should not run inside the worker's live workspace.

Instead:

```text
Worker container
       │
       │ produces commit 6ab71cd
       ▼
Control Plane
       │
       │ creates clean worktree
       ▼
Verifier container
       │
       ├── checkout 6ab71cd
       ├── build
       ├── test
       ├── docs lint
       └── acceptance checks
       │
       ▼
PASS / FAIL
```

The thing being verified is therefore the **artifact**, not the worker's mutable environment.

This avoids ambiguity caused by:

- uncommitted files
- modified test configuration
- altered build scripts
- residual state in the worker container
- local caches
- accidental environment dependencies

---

# 9. Freeze the Acceptance Policy

The worker must not be able to weaken the verification policy for the task it is currently solving.

Suppose the project contains:

```yaml
verify:
  - go test ./...
```

The worker should not be able to change it to:

```yaml
verify:
  - echo success
```

and then pass its own task.

Therefore, when a task is created, the control plane should capture the verification policy from the task's base revision:

```text
Task T-104

base_sha: 924ab7f

acceptance_policy:
    source: project-policy@924ab7f
    hash: a948fe...
```

The worker may propose changing the project's verification policy, but that change should only affect **future tasks after integration and approval**.

It must never retroactively alter the acceptance criteria of the current task.

---

# 10. Budgets

Budgets should also be enforced by the control plane.

Example:

```text
Task budget

max_attempts       3
wall_clock         30m
max_parallel_jobs  2
max_review_cycles  2
```

Then:

```text
Control Plane
    │
    ├── starts J-221
    │
    ├── timer reaches 30m
    │
    └── coordinator.Stop(...)
```

This makes limits externally enforceable rather than prompt-dependent.

Not all possible budgets are equally reliable.

Strongly enforceable today:

- wall-clock duration
- concurrency
- number of attempts
- number of review cycles
- session lifecycle

Potentially runner-dependent:

- turn count
- exact token count
- exact monetary cost

The policy should still live in the control plane even when the measurement capability depends on the runner.

---

# 11. Human Approval

Approval requirements fit naturally into this architecture.

Example:

```text
Guild Master:

request_integration(T-104)
```

The control plane evaluates:

```text
artifact exists?           YES
verification passed?       YES
independent review passed? YES
project requires approval? YES
human approved?            NO
```

Result:

```text
T-104 → approval_required
```

The Guild Master receives:

```text
Integration denied:
human approval required.

Approval ID: AP-991
```

The Web UI or TUI could show:

```text
┌──────────────────────────────────────┐
│ T-104 — Incremental parser           │
│                                      │
│ ✓ build                              │
│ ✓ tests                              │
│ ✓ docs                               │
│ ✓ reviewer                           │
│                                      │
│ Waiting for integration approval     │
│                                      │
│       [ Reject ]   [ Approve ]       │
└──────────────────────────────────────┘
```

The Guild Master must not be able to approve its own work when policy requires human authorization.

This removes the self-grading problem from the architecture.

---

# 12. The Control Plane Can Reject Guild Master Decisions

The control plane should not simply execute commands.

It should enforce policy.

For example:

```text
Guild Master:

dispatch_task(
    project="api",
    objective="Rewrite authentication",
    budget="8 hours"
)
```

Policy:

```text
maximum job duration = 60m
```

Response:

```text
REJECTED

requested wall-clock: 8h
maximum permitted: 1h
```

Another example:

```text
Guild Master:

request_integration(T-104)
```

Response:

```text
REJECTED

artifact A-311 was produced from base 924ab7f
current integration base is f18c992

artifact must be rebased and reverified
```

This is the core authority relationship:

> **The Guild Master proposes. The control plane adjudicates and executes.**

---

# 13. Steering

Steering should also be represented as a typed control-plane operation.

Avoid an unrestricted primitive such as:

```text
send_to_agent(project, arbitrary_bytes)
```

Prefer:

```text
steer_job(
    J-221,
    instruction="Stop changing parser API; preserve backwards compatibility"
)
```

The control plane records:

```text
SteeringEvent S-81
  job: J-221
  instruction: ...
  issued_by: guild-master
  timestamp: ...
  delivery: pending
```

The runner or hook layer delivers this instruction at the next available supported boundary.

This provides:

- provenance
- history
- delivery state
- cancellation
- auditability

It also makes steering part of the same orchestration model rather than an ad-hoc terminal injection.

---

# 14. Suggested Daedalus Repository Structure

A possible implementation layout:

```text
cmd/
  daedalus-control/
  guild-control-mcp/

internal/
  control/
    service.go
    task.go
    job.go
    artifact.go
    policy.go
    verify.go
    approval.go
    store.go

  coordinator/
    ...existing session lifecycle...

  verifier/
    ...

core/
  task.go
  policy.go
  capabilities.go
```

Communication:

```text
Guild Master container
        │
        │ guild-control-mcp
        ▼
/host-mounted/control.sock
        │
        ▼
daedalus-control
        │
        ├── coordinator.sock
        ├── registry
        ├── git/worktrees
        └── verifier
```

The Guild Master should **not** receive `coordinator.sock`.

Only the constrained control-plane socket should cross the container boundary.

---

# 15. Relationship to the Existing Coordinator

The existing coordinator should remain focused on:

```text
Start
Stop
Get
List
session persistence
container reconciliation
runner sockets
```

The new control plane should sit above it:

```text
Control Plane
     │
     ├── create task
     ├── schedule job
     ├── enforce policy
     ├── enforce budget
     ├── request session start
     ├── collect artifact
     ├── run verification
     ├── manage approval
     └── integrate result
             │
             ▼
        Coordinator
             │
             ▼
        Runner Session
```

This preserves a clean responsibility boundary.

The coordinator answers:

> Where does this agent session live, and how do I control its lifecycle?

The control plane answers:

> Why is this agent running, what task is it performing, what is it allowed to do, and is its result acceptable?

---

# 16. Build the Control Plane Incrementally

The control plane does not need to begin as a complete distributed scheduler.

## Version 1 — Minimal Orchestration

```text
create_task
dispatch
status
cancel
artifact commit
verify
```

Constraints:

- one active dispatched job per project
- reuse existing coordinator session model
- simple SQLite persistence
- simple clean-worktree verifier

This is enough to prove the architecture.

---

## Version 2 — Governance

Add:

```text
budgets
retry
approval
integration
audit
reviewer
```

At this point Daedalus becomes a governed orchestrator.

---

## Version 3 — Parallel Programme Execution

Add:

```text
multiple concurrent jobs/project
isolated worktrees
dependency scheduling
steering
cross-project task graph
```

This is where Daedalus becomes a true multi-agent programme execution platform.

---

# 17. Revised Conceptual Model

Without the control plane, the architecture tends toward:

```text
Guild Master
   ↓
agents
```

With the control plane:

```text
             reasoning
                │
                ▼
          Guild Master
                │
             requests
                │
                ▼
     ┌── Daedalus Control Plane ──┐
     │       authority            │
     └────────────┬───────────────┘
                  │
             execution
                  │
                  ▼
            Project Agents
```

The Guild Master remains an autonomous agent with broad reasoning freedom.

Daedalus itself becomes the trusted mechanism that converts selected decisions into controlled execution.

---

# 18. Architectural Principle

The cleanest summary of the design is:

> **The Guild Master decides what should happen.**
>
> **The Daedalus Control Plane decides whether it may happen and makes it happen.**
>
> **Project agents decide how to perform their assigned work.**
>
> **Independent verification decides whether the resulting artifact is acceptable.**

This separation preserves agent autonomy while giving Daedalus genuine supervisory authority.

It also aligns naturally with Daedalus's existing architecture:

- containers provide isolation
- runners execute agents
- the coordinator owns session lifecycle
- the Guild Master provides programme-level reasoning
- the new control plane provides authority
- verification provides objective completion criteria

The resulting system is no longer merely a collection of autonomous coding agents.

It becomes an **operating environment for governed autonomous software development**.
