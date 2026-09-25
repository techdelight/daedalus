# What two post-mortems say about Daedalus

*A review of `post-mortems/egor-postmortem-week-2026-09-19.md` and
`post-mortems/flashcards-m28-postmortem.md`, read for what they say about the
tool both projects ran inside. Written 2026-09-25.*

---

## The answer first

**Both weeks were strong inside a boundary and weak outside it, and the boundary
was drawn by what could be executed.** Egor's Kotlin work was excellent and
everything that shipped wrong was outside the compiler. Flashcards caught six
real defects and not one of them was found by an automated check — they were
found by running the app and reading the code.

**Egor's agent could not build what it shipped, and nothing told it so.** The
post-mortem says the agent had no JDK, no Maven and no Docker daemon for the
entire week, with `PATH` naming an SDKMAN toolchain that was not there. The
missing toolchain was an environment-lifecycle event, not a defect in the image
(§3.1). What *is* a Daedalus defect is that nothing checked — a container can
present a `PATH` naming tools it does not have, and no surface reports it. That
cost a week. Details in §3.

**On Forgejo versus the Ledger: the instinct is right, and the finished form is
not "swap one for the other".** The PR flow wins on the three things that
actually decided these two weeks — the diff is in front of the person deciding,
a merge moves the branch, and the checks run the project's real tests. What it
gives up is serialization, a frozen list of what must be green, and any record of
the attempt chain. `docs/reviewing-and-landing.md` already designed the
combination on 2026-09-02 and filed it as **#98**. These two post-mortems are the
evidence for it. §5.

---

## 1 · How the two weeks went

### Egor — 2026-09-19 to 09-25

| | |
|---|---|
| Delivered | 77 commits, 19 PRs (17 merged), 5 releases, tests 358 → 479 |
| Discipline | zero banned constructs, 42/42 copyright headers, 1.22:1 test-to-main churn |
| Escaped | 1 P1 defect shipped and still shipped; 8 corrective commits; 2 PRs discarded |

The product work held. The work that *describes and delivers* the product did
not — more lines changed in markdown (3,984) than in main source (2,478), and
eight commits exist purely to undo earlier claims. Documentation, `.env.example`,
the Ollama recipe and the CI workflow were each written from intention and
corrected once somebody ran them.

The dashboard rendered its own source across the page, shipped in v0.21.0, and
was found three days later by the maintainer running the demo path by hand for
the first time.

### Flashcards — M28, 2026-09-22 to 09-23

| | |
|---|---|
| Delivered | 7 slices, ~40h, 74 files, +6,929 / −8,265, 2,082 unit tests + 894 browser checks |
| Automated checks | 22 CI runs, 0 failures |
| Findings | 13 recorded, 6 real app defects, **0 caught by a pre-existing check** |

Every defect was found and fixed inside the slice that introduced it. Nothing
reached a learner. The three automated detections that did fire were all about
the test harness, not the app.

### The contrast is the useful part

Both projects had green CI throughout. Both had a defect class their checks
structurally could not see. The difference is not discipline — Egor's TDD was
exemplary — it is **whether anything executed the thing that ships**.

Flashcards ran a real browser against every slice under a walking-skeleton rule,
so a defect had at most one slice to hide in. Egor's verification stopped at
`mvn test`; the dashboard is a static resource, a broken HTML attribute is still
valid HTML, and 475 tests passed while the page was unusable. `egor-ui` had no
test directory at all until 09-25.

That is the same finding this repository already holds in its own lessons —
render the thing and read it. Three projects, one result.

**One difference the PR flow created rather than caught.** Egor had three agent
sessions live on one repository in a single day: two incompatible fixes for one
defect, 35 minutes apart; one PR opened against the wrong base; 10.5% of PRs
discarded. Flashcards ran one agent through sequential slices and wasted nothing.

---

## 2 · The honest caveat

Neither post-mortem mentions Daedalus by name, and **neither project used
`daedalus-control`** — no Tasks, no Jobs, no independent verification, no
approval checkpoint. So most of "how Daedalus could have helped" is
counterfactual and is labelled as such below.

What is *not* counterfactual is the container. Both projects ran inside it, and
that is where the measurable damage is.

---

## 3 · The environment Egor ran in

### 3.1 Why the toolchain was missing — corrected

An earlier revision of this document diagnosed the absent JVM toolchain as a
defect in the image: SDKMAN installs into `/home/claude/.sdkman`, and
`docker-compose.yml:7` mounts `${CACHE_DIR}` over `/home/claude`, so the image's
copy would be masked at runtime.

**That diagnosis was wrong and is withdrawn.** The operator's account, which is
authoritative here: Egor's container had been destroyed and reconstructed, and
the reconstruction left the `PATH` definition standing while the toolchain it
named was gone. That is an environment-lifecycle event. There is no image defect
to fix, and backlog #39, #40 and #42 are not the single bug this section
previously claimed they were.

The correction matters beyond the fact, because what survives is the more useful
half and it was buried under a wrong cause. Whatever removed the toolchain, **the
agent had no way to find out.** That gap is §3.2 and it is unchanged.

### 3.2 A toolchain that is absent and claims otherwise is worse than one that is missing

`PATH` named the directories. `docker.io` is installed and `usermod -aG docker
claude` has run, so the `docker` CLI answers and the group is right. Egor's
`CLAUDE.md` said the build path needs "nothing but Docker on the host". Every
signal an agent can cheaply check said the environment was fine.

The result was a week of work that was never built, then a post-mortem whose
single largest defect class is "nothing executed the delivered artifact".

**Add a startup self-check.** The container knows which target it was built as;
`dev` promises java, mvn, kotlinc, go, python3. Have `entrypoint.sh` resolve each
one and report loudly when a promise is unmet — to the log, and into a file the
agent is told to read. `daedalus-control` already does this for Jobs: `#83` chose
to *tell the Job* it has no git remote, at a cost of one paragraph, and bought a
refusal in the first minute instead of a wasted attempt. An interactive session
gets no such note, and that is the session both projects actually used.

### 3.3 `--dind` is inert on the only launch path

`core/command.go:85-87` mounts the Docker socket when `cfg.DinD` is set. That
code lives in `BuildExtraArgs`. The coordinator never calls it — the only mention
of the name in `internal/coordinator` is a comment at `coordinator.go:217` saying
the legacy path was the one that called it — and `StartRequest`
(`daemon.go:95-107`) carries no `DinD` field, so `toConfig` leaves it false
unconditionally.

`ARCHITECTURE.md:7` states that the runner path is the single launch path.
Therefore `--dind` parses, sets a flag, and nothing reads it. The same is true of
`--display`, and of persona overlay mounts: `Persona` reaches `core.Config` but
nothing on that path builds `OverlayPaths`.

This is the unfixed half of backlog **#55**. The symptoms #55 names (the skill
catalog, the `.daedalus` progress directory) were closed by adding
`RunnerVolumeArgs` to the coordinator; DinD, display and overlays were not, and
the entry still reads as originally filed.

**Decide and act.** Either carry these on `StartRequest` and call
`BuildExtraArgs` on the runner path, or delete the flags. A flag that does
nothing is the thing this repository files backlog entries about.

### 3.4 Resource caps are a contributing condition for flaky waits

Flashcards' defect 8 was a fixed 350 ms wait that killed an unrelated group
thirty seconds later; action item A4 records **543** `waitForTimeout` calls
against 85 event-based waits. Under the shipped defaults — `cpus: 2.0`,
`mem_limit: 4g` — a fixed wait is far more fragile than on the host.

Backlog #81b made these configurable per project, which is right, and nothing
tells an operator that a browser-heavy project should raise them. The same entry
measured 394 zombies of 405 processes on a real session, 321 of them
`chrome-headless` and 70 `esbuild` — a Playwright-plus-Vite profile that matches
this project's harness closely. That half is fixed by `init: true`, with the
caveat recorded in #81(c): **existing containers keep their zombies, and nothing
tells an operator to recreate one.**

---

## 4 · What Daedalus has, and why neither project could use it

The central finding of both post-mortems — verification stopped short of what
ships — is the exact problem `.daedalus/verify.json` exists to solve: a
per-project list of commands run in a clean container against the committed
artifact, frozen so the work cannot edit its own marking scheme.

Two things make it unreachable for these two projects.

**It is never scaffolded.** `daedalus init` and `daedalus docs scaffold` write
the eight documents in `core.RequiredDocs()` and no `verify.json` (checked:
no reference in `cmd/daedalus/init.go` or `docs.go`). Every new project
therefore inherits `DefaultAcceptancePolicy` — `daedalus docs lint` — which
grades documents. A project can be told it passed by an oracle that examined
nothing about the change.

**And it could not run their tests anyway.** Backlog **#74**: the verifier runs
`--network none` with no dependency cache, so `mvn verify` and `npm test` fail on
dependency resolution for any non-vendored project. `docs/control-plane.md`
gives the tally without flinching — of the machine oracle's first seven verdicts,
**one** was a statement about the work being graded.

So the one mechanism Daedalus has for "run it before it lands" is, today,
unavailable to exactly the two projects that needed it. **#74 is the highest-value
open item in the product**, and ROADMAP.md already says so.

Egor's own notes show the shape of the fix working: `mvn -B test
-Dmaven.repo.local=.build-cache/maven`, with a pre-populated cache, completes a
full reactor build in ~45 seconds. That is #74 option (b) — warm the language
cache into the project image so the pinned digest carries its own dependencies —
demonstrated by hand.

---

## 5 · Forgejo PRs versus the Ledger

### Why the PR flow is winning

**1. The diff is in front of the person deciding.** This is the Ledger's known,
unfixed defect, and `docs/reviewing-and-landing.md` Part A states it plainly:
M21 put the rationale and the programme on the approval screen and did not put
the diff there, so the reviewing agent sees the change and the human holding the
seal does not. `daedalus task diff` does not exist — it is not in `task.go`'s
dispatch. A pull request is a diff-first surface by construction.

**2. A merge moves the branch.** `daedalus task integrate` advances
`refs/daedalus/target`, deliberately not a branch anybody checks out. Backlog
**#79** records the operator's own first reaction to a successful landing: the
repository looked untouched. Adoption is now a separate operation with a button,
which is a good fix for a concept that a PR flow simply does not have.

**3. The checks are about the code.** This is the big one. Forgejo Actions runs
on a node with a network and a package cache, so it compiles Kotlin and runs 479
tests, or runs 894 browser checks in real Chromium. Daedalus's hermetic verifier
falls back to linting documents (§4). Between an oracle that runs the project's
real suite and one that reads its ROADMAP, there is no contest.

**4. It fits how the work is actually driven.** Both projects ran on short
maintainer instructions — *"TDD red pattern, separate branch, deliver a PR"* —
with the human choosing when to merge. `daedalus-control` assumes work is
dispatched by the plane into an isolated worktree. That is a different operating
model, and neither project adopted it.

### What the PR flow gives up

**1. No merge queue.** `docs/reviewing-and-landing.md` finds the prior art
unanimous: test the *merged* result, not the branch, because changes that look
compatible alone break combined. Forgejo has protected branches with required
checks and **no native merge queue**; `forgejo#11224` — PRs merging despite
incomplete or failed checks — is open. Egor merged 17 PRs in 7 days, including
the `kotlinx.serialization` migration, without a semantic conflict. That is a
good outcome, not a property of the system.

`internal/control/integrate.go` already implements the merge queue: serialize →
rebase onto target → re-verify the merged result → compare-and-swap.

**2. The checks can be edited by what they judge.** `.forgejo/workflows/*.yml`
is in the diff. Daedalus freezes `.daedalus/verify.json` at `base_sha` and
restores acceptance files before grading precisely so this cannot happen. Egor's
CI took three attempts at one problem, including a revert one commit later —
carelessness rather than malice, but the door is the same either way.

**3. No attempt budget and no attempt chain.** Two discarded PRs and two
competing fixes for one defect are what `max-attempts`, the per-project
concurrency limit and the claim set exist to prevent. The PR list shows #16 and
#18 closed; it does not show that two agents were answering one objective.

**4. The one real checkpoint was switched off.** Egor's C7: restore
`required_approvals: 1` on `development` — *carried from both prior
post-mortems and still outstanding*. A PR flow without required approvals is a
fast-forward with a URL attached.

### The combination, which is already designed

`docs/reviewing-and-landing.md` proposed this on 2026-09-02 as **#98**: keep the
integration transaction and put the project's own pipeline inside it.

```
serialize → rebase onto target → re-verify merged → [push merged, await pipeline] → CAS target
```

That is the merge queue with the project's *real* oracle at the grading step
instead of a document linter. It answers #74 by not trying to solve it — the
forge runner has the network and the cache — while keeping serialization, the
merged re-verify, the compare-and-swap and the attempt record.

And the Forgejo-specific argument is already made there: **Daedalus never presses
the forge's merge button.** It polls the check status for a SHA and performs the
fast-forward itself. So `forgejo#11224` stops being load-bearing — Forgejo's
weaker checking becomes an argument *for* landing host-side rather than against
it.

**So: keep from the PR flow** the diff as the review surface, the project's real
pipeline as the oracle, and a merge that moves the branch. **Keep from
`daedalus-control`** serialization, the merged re-verify, the compare-and-swap,
the budget and the attempt record.

---

## 6 · What to fix, add, and remove

### Fix

| | Item | Size | Evidence |
|---|---|---|---|
| F1 | Startup self-check: every tool the target declares must resolve, loudly, and the agent must be told | small | §3.2 — Egor's largest defect class went undetected for a week |
| F2 | Settle #55's remaining half — wire DinD / display / overlay onto the runner path, or delete the flags | small | §3.3 |
| F3 | Tell operators to recreate containers predating `init: true` (#81c) | trivial | §3.4 |

*(A fourth item — move SDKMAN out of `/home/claude` — was withdrawn when its
diagnosis turned out to be wrong. See §3.1.)*

### Add

| | Item | Size | Evidence |
|---|---|---|---|
| A1 | **#97** — the artifact diff in the Ledger, with a `task diff` for symmetry | small; no credentials, no new trust boundary, works offline | §5, and the operator asked for it on 09-02 |
| A2 | **#98** — land through the project's pipeline, inside the integration transaction | milestone | §5; both post-mortems are the evidence |
| A3 | Scaffold a `verify.json` in `daedalus init`, with the project's real build and test commands | small | §4 |
| A4 | **#74** — warm a dependency cache into the project image | milestone; overlaps A2 | §4 |

A1 is the best value in the list: it is small, it needs nothing new, it closes
M21's remaining half, and it makes A2's review step better.

A2 and A4 are alternative answers to the same question. **A2 is now the better
buy** — it uses the pipeline both projects already have, already trust, and
already run on every push.

### Remove

- **Steering.** `docs/control-plane.md` already records that every instruction
  against the shipped runner is undeliverable, and neither post-mortem describes
  a moment it would have helped. Both projects' correction loop was a new commit
  or a new PR. Mark it dormant at the CLI or take it out; it is a surface being
  maintained for no caller.
- **`--dind` and `--display` as shipped** — see F2. Wire them or delete them.
- **`docs lint` as the universal fallback oracle.** A green verdict that examined
  no code is worse than a refusal to grade. Better for a project with no
  `verify.json` to be told it has no policy than to be handed a pass. This is the
  open half of #78.

---

## 7 · Decisions that are yours

1. **#98's Finding 4** — a change that edits the pipeline judging it: does it
   escalate to a human, or land under reduced privilege the way Zuul does? This
   blocks all of #98.
2. **Does a landing push the remote branch**, or only Daedalus's own ref? Opt-in
   per project, like `--into-branch`?
3. **A2 or A4 first** — gate on the forge's pipeline, or make the hermetic
   verifier able to run a real build? They are not mutually exclusive, but doing
   A2 first makes A4 much less urgent.
4. **Is the interactive session a supported operating mode for agent work?** Both
   projects worked that way all week, and every safeguard Daedalus has built
   since M13 applies only to dispatched Jobs. If the answer is yes, the
   environment self-check (F1) and the missing mounts (F2) are not polish — they
   are the whole contract.
