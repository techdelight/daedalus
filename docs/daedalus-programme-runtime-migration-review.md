# Critical Evaluation of the Programme Runtime Migration Plan

**Subject:** [`docs/daedalus-programme-runtime-migration.md`](docs/daedalus-programme-runtime-migration.md)
**Baseline:** `development` at `6592bdb` (2026-08-29)
**Method:** every factual claim in the plan's §4 checked against the code; every proposed mechanic checked against the decision it changes.

## Executive Summary

**The diagnosis is accurate and the thesis is right. Three of the mechanics quietly reverse decisions this repository made on purpose and wrote down, and in each case the plan does not argue against the original reasoning — it does not appear to know it exists.**

The plan's central claim survives scrutiny:

> if Daedalus automates before it can distinguish **work completed** from **benefit realised**, it will become extremely efficient at finishing the wrong programme.

That is correct, and it earns the whole Charter/Outcome apparatus in §5 and §11. The `Programme` really does lack a benefit, an owner, a lifecycle and a completion rule, and no amount of Task roll-up supplies them.

What needs to change before this is built is not the semantics. It is three reversals and one hidden precondition:

| # | Finding | Severity |
|---|---|---|
| 1 | §12.2 makes the **advisory reviewer authoritative**, removing the sole compensating control on a component with network, credentials and untrusted input | **Would not ship** |
| 2 | §5.5 and §6.3 **re-merge the two graphs** M22 deliberately kept apart, promoting the most carefully-tiered operation in `authority.go` to automatic | **Argue it or drop it** |
| 3 | §11 rests on an oracle the repo itself documents as non-authoritative; **#74 is a precondition of M28**, not an unrelated backlog item | **Blocks M28** |
| 4 | §5.2 reintroduces the second intent field `programme.go:57` deliberately refused | Minor |
| 5 | §17.3's dual-model compatibility period runs against M20's migration practice | Minor, needs a sentence |
| 6 | §6.3 tiers operations that do not exist — the repo's named recurring defect | Sequencing |
| 7 | M24/M25 restate backlog #95/#69/#70; §17.2 cites a decision recorded nowhere | Bookkeeping |

## What the plan gets right

§4's gap analysis was verified line by line and holds without exception:

| Claim | Verified against |
|---|---|
| §4.1 Programme has no benefit, owner, lifecycle or budget | `internal/control/programme.go:49-68` — ID, Name, Description, Projects, Deps, timestamps. Nothing else |
| §4.2 the Guild Master has no autonomous control loop | no ticker or background loop anywhere in `internal/control` |
| §4.3 capacity waiters live in memory | `internal/control/scheduler.go:87` — `waiting map[string]waitTicket` |
| §4.4 authority is caller-class only | `internal/control/authority.go:44-51` — `Tier` has exactly two values |
| §4.7 capability definitions are scattered | `model.go:105`, `review.go:236`, `refine.go:61`, `steer.go:186` — four tables, as claimed |

Several judgements are sharp and should survive into whatever gets built:

- **§3's invariant list** is the best short statement of the plane's guarantees anywhere in the repo, including the docs it summarises.
- **§6.2, on why non-delegable operations must not be proposal-tier**: *"the agent should not be able to compose a command that becomes valid merely when a tired human clicks Confirm."* That is a real failure mode of the current proposal model, and nothing in the tree defends against it today.
- **§12.1**: the LLM may explain risk but may not assign its own authoritative class. Exactly consistent with the plane's existing "worker output is a claim, never its own verdict."
- **§16's autonomy modes**, with promotion by human decision and demotion automatic on policy breach.
- **§18.1's deterministic simulation** — fake clock, runner, verifier, reviewer and measure source, replayable from an event stream without Docker or an LLM. This matches how the package's hardest bugs have actually been caught.

## Finding 1 — §12.2 makes the advisory reviewer authoritative

**This is the one that should not ship as written.**

`AgentReviewer` is advisory by design, and the design is documented as load-bearing. From `docs/control-plane.md:817-832`:

> unlike the verifier this container has the **network and credentials**. It must; it is a language model making a call. So the clean-room property does not hold here, and the compensating control is that its output is advisory.
>
> **Why advisory is the design and not a hedge.** A verifier runs a frozen, human-authored command and returns an exit code. A reviewer is a model reading a diff it did not write — untrusted input, by construction. Two consequences point the same way: a verdict that moved plane state would be an oracle nobody bounded, and a PASS that carried authority would be the lethal trifecta with the parts relabelled, **since the diff can address the reviewer directly**.

§12.2 permits automatic integration only when, among other conditions, *"the independent reviewer found no blocking issue."* A required PASS is authority. It removes the only compensating control from a component that:

- has network access and credentials (it must — it is a model making an API call);
- reads input written by the agent whose work is being landed;
- is being asked to gate that landing with no human present.

The codebase anticipated this exact move. `requireReviewPassed` is *"a no-op kept under its old name so the decision is visible where it used to be enforced"* — there is a deliberately dead function in the tree named after the thing §12.2 switches back on.

**The evidence base is also thinner than M27 assumes.** As of `docs/control-plane.md`, `AgentReviewer` had produced one judgement in its life (RV-1 on T-14). The doc states what that does not establish:

> whether the reading is good enough to lean on. That needs several, on work of varying quality.

### Recommended fix

M27's goal is achievable without this. Make a review finding a **veto, never a permission**:

- Autoland is *enabled* by deterministic evidence only — frozen verification passed, protected paths untouched, risk class within policy, dependency gates satisfied, merged-result re-verification passed.
- A blocking review finding *stops* an otherwise-permitted landing.
- A reviewer that is unavailable, or that returns no judgement, changes nothing — consistent with `review.go:470-476`, which already refuses to write "no judgement" through as disapproval.

A veto is safe to hand an untrusted advisor: the worst a poisoned diff achieves is that its own landing is blocked. A required PASS inverts that — the worst a poisoned diff achieves is that it lands itself. §12.1 already draws precisely this distinction for risk classification and then does not apply it one subsection later.

## Finding 2 — §5.5 and §6.3 re-merge the two graphs

M22's whole finding, from `ROADMAP.md:273`:

> A programme's project→project edges plan; `task_dependencies` gates. That split is the standard shape and **merging it would hand an agent that can draft a plan the power to gate work.**

The source says the same, at `internal/control/programme.go:29-36`:

> `Deps` … are DECLARATIVE and gate nothing: the graph that actually blocks work is `task_dependencies` (Sprint 62) … the honest description is that one is a plan and the other is enforcement, and only the second has teeth.

The plan crosses this line in two places:

**§6.3 promotes "Add Task dependency" to automatic inside the envelope.** Today it is `OpAddDependency: TierProposal`, and `internal/control/authority.go:154-157` gives the reason in one sentence:

> A dependency edge decides what must happen before a Task is graded, which is as load-bearing as what grades it. **An agent that could declare its own dependencies could declare them satisfied.**

**§5.5 gives `ProgrammeTranche` `EntryCriteria` and `ExitCriteria`**, and §9.2 has the Guild Master emit `Dependencies []DependencyDraft`. That is a planning artifact with gates attached — the merge M22 refused, arriving as a new type rather than as an edit to the old one.

This may be the right call. The plan does not make the case, because it never states that a case is needed. **Either drop tranche gating and automatic dependency edges, or add a section answering `authority.go:154-157` and `ROADMAP.md:273` directly.** Crossing that line by omission is the problem.

## Finding 3 — benefit measurement rests on a non-authoritative oracle

§11.1 proposes a `MeasureRunner` "analogous to the verifier," supporting HTTP probes, metric queries and MCP queries against authoritative services.

Two facts the plan does not engage with:

- `internal/control/verifier.go:28` — `DefaultVerifierEnvPolicy` is `Network: "none"`, the checkout mounted and nothing else, no caches.
- **Backlog #74**, which `ROADMAP.md` does not treat as an ordinary item:

  > with the network off and no dependency cache the verifier cannot run a real build or test suite (#74), so it falls back to grading documents. **Until #74 is closed the gate is advice with a state machine attached** … **#74 is therefore the precondition for ever making the machine gate authoritative again**, and the standing candidate that most changes what this system can claim.

  With the tally stated: **of seven verifier verdicts to date, one was a statement about the work being graded.**

Benefit measurement needs strictly *more* environmental reach than verification — live systems, network, credentials — which is a different threat model, not an analogous one. M28's exit gate ("PR-2 … until the defined end-to-end Hebrew-learning journey passes its benefit measure") is unreachable until #74 is answered.

**#74 should be named as an explicit precondition of M28**, with the measurement sandbox's threat model designed rather than inherited. This is the highest-risk unexamined dependency in the plan: everything else is sequencing, but this sits underneath the milestone that carries the thesis.

## Finding 4 — §5.2 reintroduces the second intent field

`internal/control/programme.go:57-60`:

> Description is the programme's statement of purpose, in the words of whoever formed it. **There is deliberately no second "intent" field**: one free-text field that means "what this is for" is clearer than two that overlap.

`ProgrammeCharter` adds `Purpose` while `Programme.Description` persists — and §17.1 keeps both, since existing APIs "continue to render the Programme's current name, description, projects and dependency graph."

Fold `Purpose` into `Description`, or state why two fields now beat one.

## Finding 5 — §17.3 runs against M20's migration practice

M20's own programme migration is described as: *"The file-backed store was collapsed in rather than left beside the new one; **no double-write survived the sprint.**"*

§17.3 proposes the opposite — legacy Programmes and draft Charters coexisting for at least one release, with four live states of one concept (`legacy`, `draft`, `active`, `retired`) across CLI and Ledger.

This may genuinely be unavoidable: a Charter needs human activation, so it cannot be generated *with* authority, so some period of coexistence is forced. That argument just needs making, because the repo's established practice is the other way.

## Finding 6 — §6.3 tiers operations that do not exist

The repo has a *named* recurring defect. From backlog #82:

> **Tiering reserves authority over something that has to exist** — the recurring defect this repository keeps catching in itself.

Three instances in three days (#82, #85, #88), each the same shape: the plane supports an operation, a human can reach it, and the agent-facing surface was never widened to match.

§6.3's matrix assigns automatic-or-escalate to operations including "Record an Observation" and "Complete programme," neither of which exists. Writing the authority matrix before the operations is that shape exactly.

§7's registry is the cure, and §7 says so itself — *"This work should precede autonomous action. Otherwise Daedalus will automate the existing surface-drift problem."* That is an argument for the registry preceding the matrix, and for **backlog #95 shipping at its filed scope first**: #95 fixes dead ends operators hit last week, and its version is *one table of operation→states with an exhaustive agreement test*, not a nine-field descriptor generating eight surfaces. Ship the small one; grow it into `OperationDescriptor` later.

## Finding 7 — the plan is a second plan of record

Several milestones restate work already filed, under new names, without citation:

| Plan | Already filed |
|---|---|
| §7 / M24 `OperationDescriptor` registry | `docs/no-dead-ends.md` §1 + backlog **#95** (written and approved, not built) |
| §8.1 / M25 durable queue | **#70** — persisted queue entries, position/lease/reason durable across restart, plus a dispatch loop |
| §8.2 / M25 real termination | **#69** — persisted execution handle, idempotent `Stop`/`Kill`, capacity released only on confirmed death |
| §10 first-class `Observation` | `docs/responsibilities.md` §4.3 (2026-08-26) |

#69 and #70 are not loose parallels; they are the same designs in more implementation detail, naming `runUnderWallClock`, `CoordinatorRunner` and `daemon.go:178`. `ROADMAP.md:273` already records the dependency the plan re-derives: *"Programme-aware admission is deliberately absent and waits on #70 — the queue it would have to be fair over does not survive a restart."*

`Charter`, `Tranche` and `DelegationEnvelope` appear nowhere outside the plan. Those are the genuinely new parts, and they are the parts worth keeping.

Separately, **§17.2's "separate restructuring decision"** — retire PR-1 and PR-4, merge PR-8 into PR-3 — is recorded nowhere in `docs/`. M23 cannot be planned against a decision that does not exist in writing.

## Smaller observations

- **§5.2 says nothing about in-flight work.** Tasks retain their creation-time Charter version, but there is no rule for a *running* Job when its Charter is superseded and the new envelope is narrower. Grant revocation exists for pause (§14.4) and not for amendment.
- **§6.3 covers cross-programme dependency conflicts but not shared-project write conflicts.** Two active programmes sharing one project, both with `AllowAutoIntegration`, both landing into the same repository, is a distinct hazard from the cross-programme *optimization* §20 defers.
- **§19's "fewer than one user exception per ten accepted Tasks"** is a ratio improvable by accepting more Tasks. Every other metric in §19 is absolute.
- **`Value` and `Duration` are load-bearing and undefined.** `Value` in particular must carry counts, ratios, booleans and human attestations through the §11.3 stability-window comparison.

## Recommended changes, in order

1. **Rewrite §12.2 so a review finding is a veto, never a permission.** Deterministic evidence enables the landing; the advisory reviewer can only stop it. This preserves M27's goal and the reviewer's threat model together.
2. **Either drop tranche gating and automatic dependency edges, or argue against `authority.go:154-157` and `ROADMAP.md:273` explicitly.**
3. **Name #74 as a precondition of M28** and design the measurement sandbox's threat model rather than inheriting the verifier's.
4. **Pull #95 out of M24 and ship it at its filed scope**, ahead of everything here.
5. **Rewrite §15 as amendments to the existing backlog** — M25 *is* #69 + #70 — so there is one plan of record.
6. **Record the PR restructuring decision** referenced by §17.2, or remove the reference.
7. **Fold `Purpose` into `Description`**, and add a sentence justifying §17.3's coexistence period against M20's practice.

## Closing

The reasoning quality in this document is high, and §3, §6.2, §12.1, §16 and §18.1 are worth preserving close to verbatim. The Charter is the right answer to a real gap that no roll-up of Task states can fill.

What it needs is to stop being a second roadmap, and to stop reversing — silently — the three decisions that currently keep an untrusted model from gating work it wrote.
