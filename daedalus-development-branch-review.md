# Daedalus Development Branch Review

**Reviewed branch:** [`development`](https://github.com/techdelight/daedalus/tree/development)  
**Reviewed revision:** [`e2139df`](https://github.com/techdelight/daedalus/commit/e2139dff10d085604adcbd6de3fcc62dcdf81b73)  
**Scope:** Milestones M13–M17 and the post-v0.52 target refactor

## Verdict

Daedalus has evolved from “Docker wrapper around Claude” into a credible, security-conscious agent control-plane prototype. The M13–M17 arc contains substantially more real engineering than most agent frameworks, which generally consist of three prompts, a state graph, and optimism.

But I would not call it production-ready orchestration yet.

My cynical summary:

> Daedalus now has a serious control plane surrounding an execution substrate that still cannot reliably stop, steer, or automatically schedule the work it claims to control.

## Findings

### 1. High: dependencies can be added after execution starts and then have no effect

[`Service.AddDependency`](https://github.com/techdelight/daedalus/blob/development/internal/control/graph.go#L237) accepts any existing task. But [`refreshBlockedState`](https://github.com/techdelight/daedalus/blob/development/internal/control/graph.go#L258) only acts on `planned` and `blocked` tasks.

Consequently, a dependency can be added to a task that is:

- `working`
- `candidate`
- `verifying`
- `verified`
- `approval_required`
- `approved`
- even `integrated`

The edge is recorded and displayed, but it does not prevent verification or integration. That violates the graph’s central claim: that dependencies determine what must happen before work is graded or landed.

This is especially nasty because the UI will show a legitimate plane-owned dependency while the lifecycle has already ignored it.

**Recommended fix:** Allow dependency declaration only while the dependent task is `planned` or `blocked`. Enforce the state check and edge insertion in the same transaction. Add tests for every later lifecycle state.

### 2. Medium-high: steering’s timeout does not actually bound delivery

[`deliverSteering`](https://github.com/techdelight/daedalus/blob/development/internal/control/steer.go#L246) creates a timeout context, then calls the adapter synchronously:

```go
err = deliverer.DeliverSteering(ctx, target, steer.Instruction)
```

If an adapter ignores context cancellation, the call hangs forever. `steerTimeout` therefore does not “bound how long a steering handoff may take”; it merely asks a cooperative adapter to stop.

This repeats the exact limitation already acknowledged in wall-clock execution, but without the protective goroutine/select used by `runUnderWallClock`.

It is dormant today because the shipped runner has no steering implementation. The first real adapter will inherit the trap.

**Recommended fix:** Race delivery against `ctx.Done()` using a buffered result channel, or define and enforce that delivery must be a non-blocking custody handoff rather than delivery itself.

### 3. Medium: superseding an instruction and creating its replacement are non-atomic

[`steerJob`](https://github.com/techdelight/daedalus/blob/development/internal/control/steer.go#L204) first supersedes all pending steering, then creates the new row in a separate transaction.

If creation fails after superseding succeeds, the old valid instruction is gone and no replacement exists. The comment promises there will never be two pending instructions, but it obtains that property by allowing zero instructions during failure.

**Recommended fix:** Combine “supersede current pending instruction + insert replacement + append events” in one store transaction.

### 4. High operational limitation: wall-clock enforcement does not terminate the actual job

The code is commendably honest about this in [`runUnderWallClock`](https://github.com/techdelight/daedalus/blob/development/internal/control/service.go#L902): Daedalus records `timeout`, but the real coordinator runner may leave the container running.

That means:

- timed-out work may continue mutating its worktree;
- compute expenditure can continue past the declared budget;
- the database and physical execution can disagree;
- later reconciliation has to reason about a process the control plane has already declared dead.

This is acceptable in a prototype, but it makes “strongly enforceable wall-clock budget” too strong a description.

**Recommended fix:** Give every job an execution handle with an idempotent `Stop`/`Kill`, persist that identity, and require the runner to confirm termination before releasing capacity.

### 5. Medium architectural concern: this is admission control, not yet a scheduler

The scheduler itself admits this: a refused task remains `planned`, its ticket exists only in memory, and progress occurs only if the caller retries dispatch. See [`scheduler.go`](https://github.com/techdelight/daedalus/blob/development/internal/control/scheduler.go#L95).

Therefore:

- daemon restart erases queue order;
- free capacity does not automatically start the oldest task;
- a caller must repeatedly poll and retry;
- “queued for capacity” is ephemeral process state, not durable desired state.

The leases prevent an abandoned waiter from permanently blocking others. Good. But that is fair admission control, not programme scheduling.

**Recommended fix:** Persist queue entries and add a scheduler loop that selects and dispatches runnable work when capacity changes.

## What Is Genuinely Good

### The authority model is better than most agent frameworks

The human/agent caller class is derived from transport, unknown callers fail closed, and consequential agent operations become proposals. The explicit mutation table in [`authority.go`](https://github.com/techdelight/daedalus/blob/development/internal/control/authority.go) is exactly the kind of boring security mechanism agent projects usually avoid in favour of “the system prompt says not to.”

Particularly good:

- agents cannot approve their own proposals;
- unknown operations fail into proposal tier;
- acceptance and integration targets are plane-owned;
- agent-facing projections hide host paths;
- confirmation of steering delivery has no client route.

### The frozen oracle and plane-owned target are the strongest part of the design

Separating worker claims from independent verification is correct. Moving the authoritative integration target out of worker-writable Git refs is also the right structural answer.

The latest [`TargetFor` refactor](https://github.com/techdelight/daedalus/commit/e2139dff10d085604adcbd6de3fcc62dcdf81b73) improves this further: reading the target and adopting one are no longer misleadingly combined. That is a small refactor with disproportionate security value.

### Recovery is treated as a first-class feature

The reconcile paths, compare-and-swap updates, in-flight claims, per-job liveness seam, and explicit refusal events show mature instincts. This is not an agent demo pretending failures do not happen.

### The project documents its limitations unusually well

The M17 release explicitly admits steering is currently undeliverable. The follow-up correction in [`b9df320`](https://github.com/techdelight/daedalus/commit/b9df3209f65c5605903c46da80a8d39fde037086) records that an earlier justification used the wrong evidence.

That is healthy engineering. Slightly embarrassing, yes—but much healthier than quietly rewriting history.

## Broader Criticism

The implementation accumulated nearly 8,000 changed lines during M15–M17, with enormous comments explaining invariants, prior mistakes, mutation scenarios, and architectural philosophy.

The comments are often excellent, but their volume is becoming a smell. Some functions now carry miniature post-mortems. Eventually those explanations will diverge from behavior—as already happened with the wall-clock “terminated” claim and the M17 stdin argument.

Move historical reasoning into ADRs. Keep code comments focused on the invariant and immediate reason.

The tests are extensive, but heavily white-box. They prove that the implementation behaves as its author expects. The next testing investment should be hostile black-box scenarios:

- daemon killed between every pair of durable operations;
- concurrent HTTP/MCP clients;
- malformed or hand-edited databases;
- runner ignoring cancellation;
- dependency added at every lifecycle state;
- restart during queue contention;
- crash during supersede-and-replace steering;
- real Docker/container termination;
- shared-repository integrations with several dependent tasks.

## Review Limitation

I could not execute the suite locally because the review environment had neither Go nor Docker installed. GitHub reported no pull-request workflow runs associated with the reviewed head. This assessment is therefore based on source, tests, commit history, and architecture rather than a fresh green run.

## Final Assessment

| Area | Assessment |
|---|---|
| Control-plane model | Strong |
| Agent/human authority separation | Very strong |
| Verification/oracle integrity | Strong |
| Failure transparency | Strong |
| Dependency implementation | Needs correction |
| Parallel scheduling | Admission control, not autonomous scheduling |
| Runtime cancellation | Not enforceable yet |
| Typed steering | Honest scaffold, not a working feature |
| Production readiness | No |
| Architectural potential | High |

## Recommended Priority

1. Fix the late-dependency hole.
2. Make execution termination real.
3. Replace retry-driven admission with a durable scheduler.

Until those three are done, the control plane is more trustworthy than the execution machinery it controls—which, admittedly, is still a much better problem than the reverse.
