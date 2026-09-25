# Postmortem: one week of agent-delivered work on Egor, 2026-09-19 to 2026-09-25

## 1. Incident details

| Field | Value |
| --- | --- |
| **Title** | Review of a week's agent-delivered work: five releases, one defect shipped to users, and a recurring pattern of documentation and delivery artifacts shipping wrong |
| **Period** | 2026-09-19 07:04 → 2026-09-25 10:00 |
| **Affected services** | Egor, all modules; the published releases v0.17.0 through v0.21.0 |
| **Document status** | Final for the period. Action items outstanding, including one P1 still affecting the published release. |
| **Accountable owner** | David Stibbe (sole maintainer) |
| **Contributors** | Agent sessions across two agent tools; work directed throughout by the maintainer |

All times are **UTC**. This is a review of delivered work rather than of a single outage, so
"impact" below covers both what was produced and what escaped.

## 2. Executive summary

Over seven days, agent sessions delivered 77 commits, 19 pull requests, and five releases,
growing the test suite from 358 to 479 tests. Coding standards held completely: zero banned
constructs in main source, and every one of the 42 new Kotlin files carries the required
copyright header.

The work that went into the *product* held up. The work that went into *describing and delivering*
the product did not. More lines changed in markdown (3,984) than in main source (2,478), and a
recurring corrective pattern runs through the week: documentation and configuration were written
from intention rather than from execution, then corrected once someone ran them. Eight commits
exist purely to undo or correct earlier claims.

One defect reached users. The dashboard rendered its own source code across the page, shipped in
v0.21.0 on 2026-09-24, and was found three days later by the maintainer — not by any automated
check. It is **still in the published release**, because the fix sits on `development` along with
14 other unreleased commits.

The root cause of the escapes is consistent: nothing ever executed the delivered artifact. CI
compiled and tested Kotlin; no check ran the dashboard, the README's commands, or the release
archive. For most of the week the agent could not have run them either — the sandbox had no JDK,
Maven, or Docker daemon, so the first local build in the project's agent history ran on the final
day.

## 3. Impact

### Delivered (measured)

| Metric | Value |
| --- | --- |
| Commits | 77 |
| Lines | +8,915 / −1,747 |
| Pull requests | 19 opened, 17 merged, 2 closed unmerged |
| Releases | 5 (v0.17.0, v0.18.0, v0.19.0, v0.20.0, v0.21.0) |
| Tests | 358 → **479** (+121, +34%) across 36 → 53 test files |
| Test:main churn ratio | 3,023 : 2,478 lines — **1.22:1** |

Features landed: shared-secret authentication on every endpoint but `/health` (F2); idempotent,
auditable reply delivery (F3); rule abstention on delivery mechanics, with measurement of whether
it stops urgent mail being buried (F4); bounded ingestion (F6); the `kotlinx.serialization`
migration replacing hand-written JSON (F7); server-side filtering and make-before-break reconnect
reconciliation (F8); classification corrections recorded as training data; staleness as its own
dimension. Plus a distribution arc: per-push development builds, per-PR builds, a single-archive
release, a run script with local-JDK and container modes, and a throwaway demo mailbox.

### Standards compliance (measured on `development`)

| Check | Result |
| --- | --- |
| `!!` non-null assertions in main source | **0** |
| `lateinit var` in main source | **0** |
| Hand-rolled JSON construction | **0** |
| New Kotlin files with correct copyright header | **42 / 42** |
| Commits touching `CHANGELOG.md` | 44 of 74 |

### Escaped (measured)

- **One defect reached a release and remains there.** The dashboard source leak shipped in
  v0.21.0 (2026-09-24 18:19), was reported 2026-09-25 07:34, fixed on `development` 08:38, and is
  **still unfixed in the published release** at time of writing. `README.md` line 29 pins the
  quickstart to v0.21.0 and line 35 offers `releases/latest`; both serve the affected build.
- **Fifteen commits sit on `development` and not on `master`**, including that P1 fix. Five
  releases were cut in six days, then none since — through the week's most urgent defect.
- **Eight corrective commits**, the majority against documentation and configuration:
  - "Correct four stale or false claims in AGENTS.md"
  - "Fix the Ollama container recipe in the README"
  - "Fix `.env.example` IMAP settings and align Compose user default"
  - "Fix the download instructions in the quickstart"
  - "Correct two documents left stale by the dev-build rename"
  - "Stop asking users to clone and build Egor"
  - "Stop treating an unsubscribe link as a marketing signal" (a classification-logic correction the maintainer had to make in person: *"'unsubscribe' only indicates it is a mailing list. nothing more."*)
  - `Revert "Point CI checkout at the Docker gateway instead of localhost"` — reverted one commit later
- **CI took three attempts at one problem.** Gateway-IP checkout → revert → check out by hand →
  finally, run jobs on a Node-bearing image and pin only the build.
- **Two pull requests produced nothing.** #16 and #18 were closed unmerged: 10.5% of PRs opened.
  PR #16 carried a 155-line test file, larger than the 77-line file that shipped in its place.
- **Two UI defects found on the final day remain unfixed**: `fetchResults()` not checking
  `res.ok`, so a 401 renders as an empty dashboard; and a stored API token never invalidated on
  401, so a stale token cannot self-correct.

### Not affected

No mail was lost, mis-sent, or mis-delivered. Egor never sends replies automatically, so no
classification defect could produce a wrong outward action. The `kotlinx.serialization` migration
— the week's largest structural change — shipped without a regression.

## 4. Background

Egor is a Kotlin/JVM mail-handling service: nine Maven modules with dependencies pointing inward
to a pure `egor-domain`. Development runs under PRINCE2 governance, Scrum sprints, and XP
practices — TDD and refactoring — with `CLAUDE.md` encoding the conventions.

Work arrives as short maintainer instructions in a consistent form: *"TDD red pattern, separate
branch, deliver a PR. Document assumptions in the PR."* Followed by *"merge PR N and continue with
the next item."* That cadence is deliberate and it is fast — nineteen pull requests in seven days.

Two facts about the execution environment shape everything below. The agent sandbox had **no JDK,
no Maven, and no reachable Docker daemon** for the entire week; `PATH` referenced an SDKMAN
toolchain that was never installed. And CI compiles and tests Kotlin but never executes the
dashboard, the README's commands, or the release archive. Between them, no agent and no automated
check ever ran the thing being shipped.

## 5. Timeline and recovery

| Date | Event |
| --- | --- |
| 09-19 | Project docs and config; communication-style doc adopted into `CLAUDE.md`; Forgejo CI stood up. Remote repointed between `localhost` and the gateway IP across several attempts. |
| 09-20 | Release 0.17.0. `AGENTS.md` added, then corrected for four stale or false claims. `.env.example` and Compose defaults corrected. README's Ollama recipe corrected. |
| 09-21 | Sprint 19: UID keying, acknowledge-only-once-stored, `/api/failures`, per-poll summary logging, F2 shared secret (PR #1). Release 0.18.0. F3 delivery lifecycle (PR #2). CI checkout fixed, reverted, then fixed a different way. |
| 09-22 | F4 abstention (PR #3) and the unsubscribe-signal correction. Release 0.19.0. Maintainer asks whether a battle-tested Kotlin JSON library exists; `kotlinx.serialization` adopted red-first (PRs #4, #5). Release 0.20.0. **`f2c5bdd` introduces the dashboard defect at 21:54, unnoticed.** |
| 09-23 | Server-side filtering (#6), make-before-break reconnect (#7) — the design improved by maintainer pushback — F4 measurement (#8), bounded ingestion (#9), classification corrections (#10), staleness (#11). |
| 09-24 | Distribution arc: dev builds (#12), README/CONTRIBUTING split (#13), `egor.sh` dual-mode runner. **Release 0.21.0 at 18:19, carrying the dashboard defect.** |
| 09-25 06:06 | Demo mailbox and single-archive releases (#14, #15). |
| 09-25 07:34 | **Maintainer reports the dashboard defect** while running the demo path end to end for the first time. |
| 09-25 07:40–08:17 | Two agents independently fix it in incompatible ways (PR #16, PR #17). PR #17 is opened against `master`, contrary to every prior PR. |
| 09-25 08:38 | PR #17 merged to `development` after retargeting and a hand-resolved changelog conflict. |
| 09-25 08:40 | **First local build in the project's agent history**: 479 tests green. The toolchain had to be installed first. |
| 09-25 08:42–09:22 | A third agent opens PR #18 (closed unmerged) and PR #19 (merged) on `EGOR_NOTIFY_URL` handling — work running concurrently with this review. |
| 09-25 ~08:55 | Live instance verified serving the fixed dashboard. Two further UI defects found, unfixed. |

**Recovery is incomplete.** The published release still carries the defect.

## 6. Causes and trigger

**Trigger:** none single. The week's escapes share one mechanism rather than one moment.

**Underlying cause: nothing executed the delivered artifact.**

The verification available was compilation and unit tests over Kotlin. Everything that escaped
lived outside that boundary:

- The dashboard is a static resource. A broken HTML attribute is still valid HTML, so packaging
  succeeded and 475 tests passed while the page was unusable. `egor-ui` had **no test directory at
  all** until 2026-09-25.
- README commands, `.env.example` values and the Ollama recipe are text. They were written from
  intention and were wrong until someone ran them — which was always the maintainer, always after
  release.
- CI workflows can only be tested by running CI, which is why one problem took three attempts.

**Contributing conditions:**

1. **The agent could not run what it shipped.** No JDK, no Maven, no Docker daemon for the entire
   week. PR #17 stated plainly that its Kotlin test had never been compiled. That honesty is right,
   but it means CI was the first execution of every change, and CI's coverage is narrower than the
   product's surface.
2. **Cadence favoured merging over verifying.** "Merge PR N and continue with the next item" is an
   efficient instruction and it produced nineteen PRs in a week. It also means the interval between
   a change being written and being trusted was measured in minutes, with no step in between that
   ran the artifact.
3. **Documentation was treated as text to write, not as instructions to execute.** This is the
   single largest defect class of the week and the easiest to automate away.
4. **Release cadence was tied to feature completion, not to defect severity.** Five releases in six
   days while features landed; zero releases in the day since a P1 defect was fixed.
5. **Concurrent agents with no coordination** produced two fixes for one defect, and 10.5% of PRs
   opened were discarded.

**Why safeguards did not catch it:** the safeguards that existed worked. Tests passed because the
code compiled and behaved. Branch protection refused a wrong-base merge. The standards held at
zero violations. None of them were designed to notice that a page renders wrong or that a README
command does not run.

## 7. Lessons learned

**What worked — worth keeping deliberately:**

- **Red-first TDD, applied consistently, delivered a 34% test increase** and a 1.22:1 test-to-main
  churn ratio. This is the practice that kept the domain work sound, and it should not be traded
  away for speed.
- **Coding standards held at zero violations** across 8,915 changed lines: no `!!`, no
  `lateinit var`, no hand-rolled JSON, every copyright header present. Conventions written into
  `CLAUDE.md` genuinely propagate to cold sessions.
- **Documenting assumptions in PR bodies paid off directly.** PR #17 stated it had never been
  built; that admission is what made the final day's verification targeted rather than a search.
- **Small, single-concern PRs merged fast.** Seventeen merges in seven days with one conflict.
- **Maintainer pushback improved designs.** Make-before-break sockets came from the maintainer
  asking whether the connection really had to drop on a filter change. The agent's first answer
  was not the best one available.
- **The `kotlinx.serialization` call was correct and cleanly executed** — the right response to a
  known hazard already recorded in the project's lessons learned.

**What failed:**

- Verification stopped at the Kotlin boundary while the product's surface extended well past it.
- Documentation and configuration shipped wrong often enough to constitute a pattern, not a series
  of accidents.
- A defect reached a release and is still there a day after being fixed.
- Work was proposed, reviewed and merged without anyone — human or agent — running it.

**Where luck limited damage:**

- The dashboard defect was loud and cosmetic. A truncated attribute that silently dropped behaviour
  while still rendering would have survived far longer.
- Egor is pre-1.0 with essentially one user, who is also the maintainer, and who found the defect
  himself within a day of looking.
- The two competing fixes arrived 35 minutes apart, so both were visible in one `git fetch`. Days
  apart, the second would likely have been merged on top of the first.

## 8. Action items

| # | Action | Type | Priority | Owner | Tracking |
| --- | --- | --- | --- | --- | --- |
| C1 | **Release the dashboard fix.** Cut v0.21.1 from the 15 unreleased commits on `development`. The published release and both README download commands still serve the broken dashboard. | Mitigate | **P1** | David Stibbe | to file |
| C2 | Fix the two open UI defects: `fetchResults()` must check `res.ok` and surface an authorization failure as one; the stored token must be invalidated on 401. | Fix | **P1** | David Stibbe | to file |
| C3 | **Execute the README in CI.** Run the quickstart commands against a published artifact and fail the build if they do not work. This is the single highest-value item here — it addresses the largest defect class of the week, and every one of those five corrective commits would have been caught by it. | Prevent | **P1** | David Stibbe | to file |
| C4 | Add a headless-browser check that loads the dashboard and asserts the component initialises. Carried from the dashboard postmortem (A3). | Prevent | P2 | David Stibbe | to file |
| C5 | Require that a change has actually been built and run before merge, now that the sandbox has a working toolchain. "CI is green" and "someone ran it" are different claims and were conflated all week. | Prevent | P2 | David Stibbe | to file |
| C6 | Adopt a release rule tied to severity: a P1 fix triggers a release rather than waiting for the next feature batch. Fifteen commits including a P1 fix have been sitting unreleased. | Prevent | P2 | David Stibbe | to file |
| C7 | Restore `required_approvals: 1` on `development`. Carried from both prior postmortems and still outstanding. | Restore | P2 | David Stibbe | to file |
| C8 | Require agents to check open PRs and remote branches before starting work, given that three agent sessions were active on this repository today. | Prevent | P3 | David Stibbe | to file |

The pattern across C3, C4 and C5: extend verification to cover what is shipped, not only what is
compiled. The week's engineering discipline inside the Kotlin boundary was genuinely strong; every
escape happened outside it.

## 9. References

**Releases:** v0.17.0 (09-20 02:02), v0.18.0 (09-21 18:27), v0.19.0 (09-22 11:46),
v0.20.0 (09-22 21:44), v0.21.0 (09-24 18:19)

**Pull requests:** #1–#19. Merged: #1–#15, #17, #19. Closed unmerged: #16, #18.

**Key commits:** `f2c5bdd` (defect introduced), `6b6cb1b` (v0.21.0, shipped it), `217e671` +
`b680043` (fix and its tests), `793ef71` (merged to `development`), `fe86df4` (the CI revert),
`464f46a` (unsubscribe-signal correction), `de90323` (`kotlinx.serialization` replacement)

**Related documents:**

- `docs/postmortem-dashboard-source-leak.md` — the defect that reached users
- `docs/postmortem-agent-session-continuity.md` — the duplicated-work and lost-context failures
- `ARCHITECTURE.md` "Lessons Learned" — where the JSON hazard was already recorded before F7

**Method note**

Figures are measured from git history, the Forgejo API, and the working tree: commit and line
counts from `git log --numstat`; test counts from backtick-quoted test functions per tag;
compliance counts from `git grep` over `development`. Churn-by-area is a path-based
classification and should be read as indicative, not exact. One earlier claim in this review's
preparation — that the unsubscribe correction had not reached a release — was wrong and is
corrected here: `464f46a` is an ancestor of v0.21.0.
