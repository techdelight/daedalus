# Ledger + Forgejo MVP: implementation roadmap

**Status:** proposed implementation roadmap; no code or operational configuration changed by this document.  
**Date:** 2026-09-26.  
**Scope:** one Forgejo instance, one pilot repository, one configured target branch.  
**Design basis:** [Ledger v2 design](ledger-v2-forge-design.md), [reviewing and landing](reviewing-and-landing.md), and the MVP discussion.  
**Planning authority:** this document details the MVP; implementation work should be linked into the existing `BACKLOG.md`, not maintained as a competing backlog. Backlog changes are a separate implementation step.

## 1. Outcome and definition of the MVP

An existing Daedalus task produces a candidate, the daemon publishes it as a pull request, a human reviews the evidence and approves it in the Ledger, and the daemon lands the exact checked integration commit on the remote target branch. The operator does not manually push, merge, synchronize the target, or shepherd routine transitions.

The first version keeps the existing Ledger. It adds enough evidence and progress reporting to make the new route usable. A redesigned decision inbox is not a prerequisite.

The normal path is:

1. Create and execute a task through the existing task flow.
2. Capture an immutable candidate artifact and publish/update its PR.
3. Show the PR link, frozen verification outcome, advisory review, and unverified claims.
4. Human selects **Approve and queue landing**.
5. Daemon reserves the repository landing slot and prepares a commit on the current remote target.
6. Run frozen verification and the configured project pipeline for that prepared commit.
7. Conditionally advance the remote target to that exact commit.
8. Reconcile the local record and PR; show completion.

The MVP succeeds when ordinary work follows this path with less operator effort than the current flow, while failures remain visible and recoverable.

## 2. Existing implementation and the actual change

The following observations come from reading the repository during roadmap preparation, not from running its test suite. Line numbers and behavior should be rechecked when implementation begins.

| Existing area | Reuse | Required change |
|---|---|---|
| `internal/control/integrate.go` | Rebase onto target, verify merged result, bounded retry logic, already-landed recovery | Add a durable asynchronous route for remote landing; do not stretch a synchronous HTTP request across CI |
| `internal/control/store.go` | Task/job/artifact identities, frozen acceptance hash, integration targets, events | Add forge references, approval binding and durable landing/check records |
| `internal/control/approval.go` | Human approval operations and project policy | Bind approval to the artifact and policy; require approval for the pilot |
| `internal/control/authority.go` and `daemon.go` | Caller-class boundary and routes | Apply the same authority checks to new consequential operations |
| `internal/control/acceptance.go` and verifier seam | Frozen verification machinery | Preserve its meaning when evaluating prepared integration commits |
| `internal/control/operations.go` | Shared operation availability | Expose legal queue, retry and cancellation operations consistently |
| `internal/control/service.go` and reconciliation machinery | Background coordination and lifecycle | Resume persisted landings after restart without duplicating external effects |
| `internal/web/static/control.js`, web routes and CLI | Existing operator surfaces | Add PR evidence, check progress and actionable blockers |

Today the authoritative target is stored locally and projected to a Daedalus ref. Updating the checkout branch is an optional follow-up. That is not equivalent to advancing a remote branch safely.

For a forge-backed project, the remote target branch becomes the observed code location; the daemon owns authorization and the journal of its operations. The local target record becomes a reconciled projection. The current local-only route remains available to explicitly local-only projects.

Two existing behaviors need particular attention:

- Human approval is opt-in in the current policy machinery. Enable it explicitly for the pilot.
- The integration code can carry a job waiver through failed merged verification. The MVP forge route must not inherit that behavior implicitly.

## 3. Scope boundaries and proposed defaults

These are implementation defaults proposed for this MVP, not authorization to change a live repository's protections or credentials.

| Topic | MVP choice |
|---|---|
| Forge support | Forgejo only; a narrow internal boundary, not a generalized multi-provider framework |
| Pilot | One repository and one target branch |
| Publication | PR at candidate time; one open PR per task where feasible |
| Git transport | Choose managed branch or AGit after the compatibility spike; AGit is not a requirement |
| Approval | Human required; one action approves the reviewed candidate and queues landing |
| Concurrency | One active landing per repository; later approved tasks wait durably |
| Integration | Prepare and check an immutable commit; advance the remote branch to that exact commit |
| Target movement | Unexpected remote movement blocks and requires explicit reconciliation |
| Rebase | Permit a clean deterministic replay under a documented approval policy; conflicts require revision and fresh approval |
| Pipeline configuration changes | Excluded from this route initially; clear refusal with reason |
| Waivers | No frozen-floor or pipeline waivers in the forge-backed route |
| Outage | Wait with a deadline, then block; no automatic downgrade to local-only landing |
| CI notifications | Poll authoritative API state; webhooks are deferred |
| PR reconciliation | Must truthfully identify the landed commit; exact mechanism proved during the spike |
| Releases | Out of scope |

### Explicit exclusions

- Four-lane inbox redesign, batch approval and new keyboard navigation system.
- Stacked PRs, batch integration and speculative parallel landing.
- GitHub or other forge support.
- Draft PRs at dispatch.
- Full agent-review comment mirroring and line-level discussions in Ledger.
- Protection-drift watchdog, release automation and artifact smoke-test orchestration as platform features.
- Automatic adoption of external target changes.
- New waiver or emergency bypass mechanisms.

Project CI may already include a release-artifact smoke test; it can be required like another existing check. Building a release platform is excluded.

## 4. Non-negotiable invariants

1. **Approval identifies content.** It records task, artifact, candidate SHA, frozen policy identity and human decision. A new candidate cannot reuse it.
2. **Every gate names its input.** A check records which prepared SHA it evaluated, its run identity, source and outcome.
3. **Only the prepared commit may land.** Do not verify one commit and allow a forge merge operation to manufacture an unverified replacement.
4. **Target movement is conditional.** The update requires the expected remote target value and a fast-forward relationship. A stale expectation cannot overwrite newer work.
5. **No success by absence.** Missing, skipped, cancelled, unknown or timed-out required results do not count as passing.
6. **Intent precedes side effects.** Persist sufficient operation identity before publishing, triggering checks or attempting a branch update.
7. **Recovery is evidence-based.** A timeout does not prove that a remote write failed. Inspect remote state before retrying.
8. **Worker jobs receive no forge credential.** Publication and remote writes belong to the host-side daemon.
9. **The route cannot silently weaken.** A forge-backed task cannot fall back to a local landing because the forge or CI is unavailable.
10. **Completion follows remote reality.** Do not satisfy downstream tasks merely because a local target row advanced.

Frozen verification may deliberately restore acceptance files in its verification checkout. Its record must distinguish that frozen-oracle view from the unmodified prepared commit executed by project CI. The UI must not claim both checks ran against identical filesystem contents if they did not.

## 5. Phase 0 — prove the forge contract

**Purpose:** establish that the installed Forgejo and runner configuration can support the required transaction before committing to a publishing design.

Use a disposable repository on the target Forgejo version. This phase is an implementation experiment, not production rollout.

### Work

- Record Forgejo version, runner version, workflow trigger behavior and repository protection configuration.
- Publish a candidate and create/update a PR; record stable repository and PR identities.
- Publish a prepared integration commit using a daemon-controlled ref.
- Trigger the required pipeline and confirm which commit is actually checked out and reported by each run.
- Query check provenance, run identity and terminal results through the available API.
- Exercise a conditional remote branch advance to the prepared commit.
- Race that update with a second writer and demonstrate that the stale operation is refused.
- Demonstrate how the PR reaches a truthful final state when the daemon advances the branch itself.
- If using a forge merge API, demonstrate that it preserves the exact checked commit and enforces the expected target. Otherwise do not use that endpoint for landing.
- Check whether ordinary managed branches or AGit provide the simpler reliable route. Include force-push, retries, closed PRs and CI triggers in the comparison.
- Determine credentials and permissions needed for Git publication, status reads and PR updates. A Git deploy key alone may not cover API operations.

### Deliverables

- A short compatibility decision record with observed behavior and reproducible commands/tests.
- Selected publishing, pipeline-trigger, conditional-update and PR-reconciliation mechanisms.
- A narrowly scoped credential/protection setup for the pilot.
- A list of version-dependent assumptions covered by integration tests.

### Exit gate

Demonstrate `publish M → checks for M → conditional target update to M → truthful PR completion`, including refusal when the target changed.

If this is unsupported by the installed setup, revise the mechanism or pilot environment before Phase 1. Do not substitute “green PR” for proof of the exact landing contract.

## 6. Phase 1 — configuration, persistence and authority

**Purpose:** make identities and restart behavior explicit before introducing background remote work.

### Configuration

Add an opt-in forge-backed project configuration with:

- Forge base URL and stable repository identity.
- Remote name/URL and fully qualified target branch.
- Credential reference resolved host-side; never credentials in event bodies or PR text.
- Approved ref namespace for candidate and integration publication.
- Required pipeline checks and allowed sources, incorporated into the frozen policy identity.
- Poll cadence, overall verification deadline and bounded retry behavior.
- Protected configuration paths and the clean-replay approval rule.

Reject incomplete or inconsistent configuration before publishing. Route selection must be explicit and stable for active landings.

### Persistent records

These are conceptual records; use the smallest schema that preserves the invariants.

| Record | Minimum information |
|---|---|
| Forge publication | Task/artifact, repository, PR identifier and URL, candidate SHA/ref, publication operation identity |
| Approval | Human event, task/artifact, candidate SHA, policy hash, timestamp; invalidation/supersession |
| Landing | Unique ID, repository/target, approved candidate, policy identity, expected base, prepared SHA/ref, phase, deadlines, last error |
| Check evidence | Landing ID, prepared SHA, check/source/run identity, attempt, status, timestamps, evidence URL |
| Remote advance | Expected old SHA, intended new SHA, persisted intent, observed outcome and reconciliation status |
| Unverified claims | Artifact/attempt-scoped text; distinguish absent declaration from an explicit “none declared” |

Add migrations without rewriting historical events or pretending old approvals contain identities they never recorded. Legacy pending work needs an explicit migration/reapproval rule.

### Authority and concurrency

- Human approval and policy decisions remain human-only.
- Agent clients may read status and use existing bounded/proposal mechanisms; they cannot approve or bypass a blocked landing.
- Acquire one durable active-landing slot per repository. Do not hold a global service mutex while waiting for CI.
- Ensure a second daemon instance cannot process the same operation concurrently; reuse deployment exclusivity if it is sufficient, otherwise add ownership/fencing.
- Make repeated approve-and-queue requests return the existing landing rather than enqueue duplicates.

### Exit gate

Migration and service tests demonstrate immutable approval bindings, duplicate-request safety and resumable landing records. Existing local-only behavior still passes its relevant tests.

## 7. Phase 2 — publish candidates and show review evidence

**Purpose:** deliver the first usable improvement before remote landing is enabled.

### Work

- Publish on the daemon's behalf after candidate capture; never add credentials to the worker container.
- Create or update the task's PR using the mechanism selected in Phase 0.
- Retain all attempt/artifact identities locally even if a PR branch is overwritten.
- Populate the PR with objective, deliverables, task reference and unverified claims.
- Keep machine-owned PR content identifiable so retries do not overwrite human edits or duplicate comments.
- Record publication intent before calling the forge. Recover from “PR created, response lost” by locating the existing operation before creating another.
- Treat candidate CI as preliminary evidence; it cannot replace integration-commit CI.
- Surface publication errors with retry action and no effect on approval authority.
- Reconcile externally closed or changed PRs explicitly; do not silently reopen or adopt them.

### Minimal Ledger additions

- PR link and candidate identity.
- Frozen-floor result and advisory review.
- Unverified claims, labeled as worker declarations.
- Publication/check error details and timestamps where freshness matters.
- Preserve reading position and selected task when these fields refresh.

The complete diff remains on the forge for the MVP. An independent built-in diff feature can proceed separately; it is not a blocker for this pilot.

### Exit gate

A task produces a reviewable PR without manual Git steps. Retrying publication after a lost response does not duplicate the PR, and a new candidate invalidates the previous approval.

## 8. Phase 3 — durable preparation and pipeline verification

**Purpose:** replace the long synchronous landing operation with restartable background work.

### Work

- Add **Approve and queue landing** as one user action, persisting approval and queue intent consistently.
- Return promptly with an operation ID and status; do not wait for CI in the request handler.
- At dequeue, fetch and observe the remote target. Refuse unexpected divergence from the last reconciled target.
- Reuse clean rebase/replay machinery to prepare a deterministic integration candidate.
- Refuse merge conflicts; do not have an agent edit conflict resolutions inside an already-approved landing.
- Persist the prepared SHA and expected target before publication.
- Run the frozen verifier and publish the prepared commit for pipeline execution.
- Freeze required checks independently of candidate-controlled modifications.
- Poll results for the selected run identities and SHA, with explicit handling of reruns and superseded results.
- Block on failed checks, missing results, unsupported provenance or deadline expiry.
- Retry transient reads with bounded backoff; persist deadlines across daemon restarts.

### Configuration-change restriction

Detect changes to workflow definitions, verification policy and declared trusted pipeline entrypoints before queueing. Refuse this route with an explanation.

Blocking workflow YAML alone does not prevent candidate code from weakening a script or test invoked by it. Document that boundary: CI is evidence about execution, not independent proof of all requirements. Use frozen verification and human review for the remaining trust assumptions; do not claim that path protection solves all test manipulation.

### Exit gate

The daemon can stop during preparation or CI waiting, restart and resume the same landing. Missing, failed or stale check evidence cannot advance any target.

## 9. Phase 4 — conditional remote landing and reconciliation

**Purpose:** make the remote branch, local task state and PR converge without duplicate landings.

### Work

- Recheck candidate/approval identity, protected-path policy and required evidence immediately before attempting the remote advance.
- Confirm that the prepared commit descends from the expected remote target.
- Persist advance intent and execute the mechanism proved in Phase 0.
- A conditional Git update must name the explicit expected old SHA; do not rely on an implicitly refreshed tracking ref. Never permit rewriting unrelated target history.
- On definite stale-target rejection, block for reconciliation. On ambiguous transport failure, inspect remote state before making another write.
- When the remote target is confirmed at the intended commit, record the landing and update the local target projection.
- Reconcile the PR without generating a different merge commit. If the supported outcome is closure with a landed-SHA record rather than native “merged,” label it honestly and record that tradeoff in the Phase 0 decision.
- Expose “code landed; PR reconciliation pending” separately from “not landed.”
- Complete downstream scheduling only after durable local confirmation of the remote landing. PR bookkeeping failure must not cause the code to be landed again.

### Recovery cases

| Observation after restart/timeout | Behavior |
|---|---|
| Target equals expected old SHA | Revalidate gates; retry the same intended update if still eligible |
| Target equals prepared SHA | Record already-landed outcome; reconcile local state and PR |
| Target contains prepared SHA as an ancestor | Confirm exact commit identity and inspect external movement; record landing truth, pause further work for target reconciliation |
| Target differs and does not contain prepared SHA | Block; do not infer success from a similar patch or overwrite the branch |
| Forge cannot be read | Wait/block with deadline; do not guess whether the write succeeded |

Patch equivalence alone is not proof that this exact CI-verified commit was remotely landed. Review the existing already-landed helper before reusing it for this purpose.

### Exit gate

Fault injection around the remote update and local writes demonstrates no duplicate merge, no false completion and no overwrite of external changes.

## 10. Phase 5 — operator recovery and minimal UX completion

**Purpose:** make failure paths usable without database edits or undocumented Git repair.

### Visible states

- **Queued:** approval recorded; waiting for the repository landing slot.
- **Integrating:** preparation or verification in progress, with separate check results.
- **Blocked:** concise reason, exact relevant identity and legal next action.
- **Landed; reconciliation pending:** remote code update confirmed; bookkeeping incomplete.
- **Integrated:** remote landing and required local completion recorded.

Keep detailed phases in the landing record. Do not multiply top-level task states unless necessary for legal operation handling.

### Required operator actions

- Open the PR and check evidence.
- Approve and queue; request revision through the existing task flow.
- Retry publication, transient checks or PR reconciliation without duplicating operations.
- Cancel before remote advance; release the queue slot and clean up safely.
- Inspect unexpected target movement and explicitly reconcile before preparing again.
- See which failures require a new candidate and therefore a new approval.

Cancellation must be serialized against remote advance. Once an advance has started with an uncertain outcome, reconcile first. Cancellation after landing must not pretend to undo code; reverts are separate changes.

For explicit target reconciliation, display the observed branch difference. Re-evaluate policy compatibility and require a new approval if the approved content or applicable policy changes. Do not silently refresh the frozen oracle.

### Exit gate

Each supported blocked state has an understandable explanation and recovery action in both CLI and Ledger. Normal approval requires one operator action, with no manual refresh loop.

## 11. Phase 6 — pilot rollout and adoption evaluation

1. Finish the isolated-repository tests before connecting production credentials.
2. Select the pilot repository/branch and verify runner capability, protections, credential scope and required checks.
3. Snapshot current configuration and back up the database before migrations.
4. Enable publication first; inspect PR quality and evidence on representative tasks.
5. Enable remote landing explicitly for the pilot once the preceding gates pass.
6. Process a small, pre-agreed sample of ordinary changes; aim for ten completed changes plus the failure drills below.
7. Review operator effort and bypass reasons before adding more projects or redesigning the inbox.

### What to measure

- Operator actions and manual Git steps per completed task.
- Time from candidate availability to human decision.
- Time from approval to landing, distinguishing CI time from daemon overhead.
- Failed/retried publications, blocked landings and recovery interventions.
- Duplicate PRs, duplicate landings, stale approvals and incorrect completion reports.
- How often the operator bypasses the route and why.

Capture a small baseline from the existing workflow. Report observed results; do not invent a productivity target from external review statistics.

### Rollback and disablement

- Stop accepting new forge-backed landings before changing configuration.
- Drain or explicitly cancel queued/pre-advance work; reconcile all uncertain remote writes.
- Leave already-landed commits in place. Disabling the feature is not a code rollback.
- Preserve landing history and configuration needed for recovery.
- Do not switch an active forge-backed task to the legacy route as an outage workaround.
- If reverting the executable, verify schema compatibility or restore a coordinated backup only after remote outcomes have been reconciled. Never restore a stale database and assume the remote branch also rolled back.

## 12. Validation matrix

Use focused unit/service tests for logic, a controllable forge adapter for failure injection, and real Forgejo integration tests for behavior the adapter cannot prove.

| Scenario | Required result |
|---|---|
| Normal candidate → PR → approval → landing | Exact checked prepared SHA reaches the target; PR and local record agree |
| Duplicate approve/queue request | One approval-bound landing operation |
| Candidate changes after approval | Approval invalid; no advance |
| Candidate changes while CI is running | Old operation cannot land superseded work |
| Required check fails, disappears, skips or is cancelled | Block; target unchanged |
| Old candidate check is green | Cannot satisfy integration checks |
| Wrong SHA, source or run reports success | Cannot satisfy required evidence |
| Workflow/policy/protected entrypoint changes | Route refuses with actionable explanation |
| Existing job waiver | Does not bypass forge-backed gate |
| PR creation succeeds but response is lost | Recover existing PR; do not duplicate |
| Forge outage during CI | Resume existing operation or time out; no downgrade |
| Target moves before/during branch update | Refuse stale update; preserve external work |
| Daemon stops before remote update | Resume persisted operation after revalidation |
| Daemon stops after remote update | Detect exact landed commit; no duplicate landing |
| Local recording fails after remote success | Recover from journal and remote state |
| PR reconciliation fails | Report landed truth; retry bookkeeping only |
| External PR closure/head modification | Detect disagreement and block/reconcile |
| Cancel races with branch update | One serialized outcome; never falsely report rollback |
| Two queued tasks / multiple daemon processes | At most one active landing owner per repository |
| Legacy local-only project | Existing behavior remains available and tested |

Also check credential redaction, remote-host verification, command argument handling, and rendering of untrusted PR/worker text. These are specific consequences of adding remote credentials and external content, not a separate security platform project.

## 13. Suggested implementation slices and dependencies

Each slice should be reviewable and linked into the existing backlog when work starts. These are work packages, not invented issue numbers or delivery estimates.

| Slice | Deliverable | Depends on |
|---|---|---|
| A | Forge compatibility spike and transport decision | None |
| B | Project config, record migrations and approval identities | A |
| C | Idempotent candidate publication and PR link | B |
| D | Durable landing queue and preparation | B |
| E | Exact-commit pipeline gate and evidence persistence | A, D |
| F | Conditional remote advance and crash recovery | E |
| G | PR/local reconciliation and downstream completion | C, F |
| H | Ledger/CLI recovery actions and progress | C–G; incremental UI work can accompany each slice |
| I | Isolated end-to-end failure drills, pilot and adoption review | H |

Do not use the number of slices as a time estimate. The compatibility spike determines the largest uncertainty: remote branch advancement and truthful PR completion. Estimate implementation after that result, including the discovered API/version constraints.

## 14. Decisions to close before enabling the pilot

- Which repository and branch are the pilot?
- Which installed Forgejo/runner versions are supported and tested?
- Managed branches or AGit, and how does the chosen route trigger CI?
- Which mechanism guarantees the exact remote advance, and how is the PR finalized?
- Which checks and check producers are authoritative for the pilot?
- Which pipeline/configuration paths are excluded from this route?
- What clean-replay transformations does approval permit?
- Who may write the target, and how is exceptional human maintenance handled?
- What deadline and retry limits are acceptable for this project's CI?
- How are existing pending tasks handled at activation: finish in their original mode or migrate with fresh approval?

Engineering can proceed with the proposed defaults while these are prepared. Resolve environment-dependent choices before production configuration and credentials are applied.

## 15. Final acceptance checklist

- [ ] A normal task becomes a PR without manual pushing.
- [ ] The operator sees evidence and remaining uncertainty at approval time.
- [ ] One human action approves the reviewed candidate and queues landing.
- [ ] Candidate changes invalidate approval.
- [ ] Both verification layers report their actual inputs and outcomes.
- [ ] Only the exact prepared commit may advance the remote target.
- [ ] Missing/failed evidence, protected changes and waivers cannot bypass this route.
- [ ] Unexpected target movement preserves external work and produces an actionable blocker.
- [ ] Restart/timeout recovery cannot duplicate a PR or landing.
- [ ] Local completion and downstream scheduling follow confirmed remote landing.
- [ ] The Ledger distinguishes failed landing from incomplete PR bookkeeping.
- [ ] Credential handling and caller authority remain outside worker control.
- [ ] Local-only projects retain their explicit existing route.
- [ ] Pilot changes and failure drills pass without manual database repair.
- [ ] Adoption review shows whether operator effort improved and records remaining bypass reasons.

The MVP is a reliable, usable route from candidate to remote landing. A broader inbox, richer risk classification and release automation should follow evidence from this route, not expand its initial definition.
