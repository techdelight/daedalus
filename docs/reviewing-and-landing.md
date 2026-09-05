# Reviewing the change, and landing it through the pipeline

**Status: PROPOSAL. Not a plan of record.**

This document answers two questions asked on 2026-09-02:

1. *"I want to be able to quickly see the diff when reviewing."*
2. *"After accepting the review, it first needs to be pushed, so the pipeline on
   Forgejo/GitHub tests the complete set of changes before integrating."*

They are separate pieces of work with separate risk. Part A is a display gap and
is small. Part B changes what integration *is* and is a milestone.

Part B's design is checked against how bors, GitHub's merge queue, Zuul, Prow and
GitLab handle the same two problems — see *Prior art*. That section is placed
before the findings because it supplies the vocabulary they use, and because the
consensus on its first question turns out to validate a decision this plane
already made.

Both are written as **amendments to the backlog**, not as a parallel roadmap.
That is deliberate: finding 7 of `daedalus-programme-runtime-migration-review.md`
was that the migration plan had become a second plan of record, and the same
mistake is available here. Part A is proposed as **#97**. Part B is proposed as
**#98**, and it revisits the door that **#83** explicitly left open.

---

# Part A — the diff belongs in the Ledger

## What is true today

- An `Artifact` already carries `BaseSHA`, `HeadSHA` and `Branch`
  (`internal/control/store.go:1587`). The branch is
  `daedalus/<task>/<job>` (`internal/control/worktree.go:41`), and it lives in
  the host repository. `git diff base..head` works right now, with no network.
- The plane already runs precisely that command:
  `internal/control/acceptance.go:241` shells out
  `git diff --no-renames --name-status <base> <head>`. Nothing consumes the
  result for display.
- What the operator sees at the approval gate is one line per artifact
  (`internal/web/static/control.js:1337-1345`):
  `verify passed · review passed · daedalus/T-28/J-31`.

## Finding: this is M21's defect, unfixed for the diff

`control.js:1229-1235` states the M21 argument in its own words: the reviewer
agent is handed the diff, the objective, the rationale and the programme, while
this page handed the person holding the seal an objective and a base SHA — *"the
party that only reports was being shown more of the intent than the party with
the authority to act, which is backwards."*

M21 fixed the rationale and the programme. **It did not fix the diff.** The
reviewer still sees the change; the human with the authority still does not.

`service.go:172-177` gives the rule that decides where the fix goes: reviews ride
on the status rather than living behind a route of their own, because *"a finding
one fetch away from the approval gate is a finding nobody reads."* A link to a
forge is exactly one fetch away, plus a context switch, plus a second
authentication on the phone the last three commits were spent making this work
on.

## Recommendation: `GET .../diff`, rendered on a fourth tab

### The tab

`paintTabs(['entry', 'terms', 'change', 'record'])` — `control.js:1086` already
takes the list, so this is one argument. Shown only when the task has an
artifact. The order follows the decision: what it was for → what it is graded
against → **what actually changed** → what happened.

### Level 1 — the file list *is* the tab

One row per file, from `git diff --numstat --name-status base..head`:

```
M  internal/control/operations.go            +142  −38
A  internal/control/operations_test.go       +310   −0
D  internal/control/authority_states.go        +0  −96
M  docs/no-dead-ends.md            ⚠ acceptance  +12   −2
M  internal/control/review.go      ▶ 1 blocking   +8   −1
```

This is the triage view, and it is the point: twenty rows scan on a phone, and
you decide where to look. Two marks earn their place by being relevant to the
*decision* rather than to git:

- **`⚠ acceptance`** — the path matches the frozen policy's `AcceptanceGlobs`.
  `AcceptanceFileChanges` already computes this set. `acceptance.go:219-221`
  records why it matters: a diff cannot tell *"added a test that pins the fix"*
  from *"deleted the assertion that was failing"*. A human can — but only if the
  row says which file to open.
- **`▶ n blocking`** — a review finding names this file. `review.go:89` already
  carries `File` and `Line`.

### Level 2 — the patch, as a disclosure in place

Tapping a row expands it. This **must** use the existing `remember()` /
`disclosureOpen(key, openByDefault)` pair (`control.js:126-142`), keyed on the
file path.

That is not a stylistic preference. `paintEntry` empties and refills the panel
every fifteen seconds, and `control.js:121-124` and `control.js:1088-1093` are
both scars from exactly this: disclosures springing shut, and the scroll jumping
to the top, under someone mid-read. A diff pane is the longest thing this panel
will ever hold. It has to inherit that machinery rather than re-earn it.

Default-open rule, mirroring the findings default: a file named by a blocking
finding opens by default, everything else closed, applied once, and the reader's
own toggle wins from then on.

The body is fetched per file (`?path=`), so the wire carries the stat list plus
only what was opened.

### Rendering

- **Unified, never side-by-side.** `NARROW` (`control.js:151`) commits this page
  to one screen at a time at 780px, where side-by-side is unreadable.
- `<pre>` per hunk, line classes for added / removed / context / hunk header,
  `+`/`−` colour only.
- **No syntax highlighting.** `control.js` is hand-written plain JS served
  embedded, with no build step. A highlighter means a bundler, and that is a
  larger change than the feature.
- Each hunk block scrolls horizontally inside itself, so a long line never makes
  the page body scroll sideways.

### Truncation is stated, never silent

A per-file cap (~500 lines), and when it trips the row says so: *"truncated —
3,200 more lines, `git diff T-28`."* A silently short diff reads as a small
change, which is the failure this feature exists to prevent. Binary files and
renames say what they are rather than rendering nothing.

### The payoff: findings link to hunks

On RECORD, a finding with `File`+`Line` becomes a control that switches to
`change`, opens that file and scrolls to the line. This is why disclosures key on
path. Today a blocking finding hands over coordinates the reader resolves by
hand, which by `service.go:175` is a finding nobody reads.

### Retries

A task with several artifacts gets one row of pills above the list
(`J-31 · J-33 · J-34`), latest selected. Not a dropdown — one tap, and three
attempts are visible at a glance.

## Deliberately not done

- **Keyboard navigation.** `control.js` binds only Escape and Enter, and only
  inside the prompt dialog (`control.js:2100-2106`). Adding `j`/`k` here makes
  one surface keyboard-navigable and every other one not, which is a worse
  inconsistency than the convenience is worth. If keyboard nav is wanted it is a
  decision about the whole board.
- **Comment-on-line.** Findings come from the reviewer; the human's verdict is
  approve/reject with a reason. A second annotation channel invents a workflow
  that does not exist.
- **Push the branch and link to the forge.** Rejected as the *primary* path: it
  requires the credential Part B has to argue for, it is one fetch away from the
  gate, and it cannot show a rejected or superseded attempt that will never be
  pushed anywhere. Reasonable later as a *complement* — an "open on the forge"
  link beside the inline diff, for a diff large enough that a real review UI
  earns its cost.

## Cost

A `Service` method reading the diff, a wire route, the Ledger tab, and
`daedalus task diff <id>` for CLI symmetry. The git read is a few lines on top of
what `acceptance.go` already does. No new credentials, no new trust boundary,
works offline.

---

# Part B — the pipeline gate before landing

## The door #83 left open

BACKLOG #83 decided that a Job cannot reach a git remote, and closed with:

> If this is ever revisited, the thing to reach for is a DEPLOY KEY scoped to one
> repository, **or the plane pushing on the Job's behalf after verification** —
> never a general credential in the container.

The plane runs on the host, outside the container, and already holds the
repository. So this proposal is not in tension with #83; it is the branch #83
named. `runner.go:167` refuses *"a container with the network, an untrusted
objective and a push-capable key"* — a host-side deploy key held by the plane is
a different and much smaller grant.

## Prior art — how other systems answer this

Researched 2026-09-02, before designing anything, because this is a solved
problem with thirty years of practice behind it. Two structural questions. The
field agrees firmly on the first and splits four ways on the second.

### Question 1: what gets tested — the branch, or the merged result?

Unanimous: **the merged result.** This is the "Not Rocket Science Rule",
implemented by bors for Rust — test the merge commit, fast-forward `main` only if
green, leave `main` untouched if not — and its stated motivation is *merge skew*,
changes that look compatible in isolation and break once combined. GitHub's merge
queue builds a temporary branch containing the base, the earlier queue entries and
your PR, and runs the required checks against that. Prow's `tide` ensures PRs are
tested against the most recent base commit, retesting if necessary, and batches.

**This plane is already on the right side of it.** `integrate.go`'s
serialize → rebase → re-verify-merged → CAS *is* the merge queue, and it predates
this proposal by a long way. The gap is not the pattern; it is only that the
oracle at the re-verify step is the plane's own frozen policy and never the
project's pipeline. That is a much smaller gap than it first appears, and it is
why Part B is an addition to the transaction rather than a redesign of it.

### Question 2: may a change modify the pipeline that judges it?

**(a) Zuul — split by trust, and make self-testing and privilege exclusive.**
The closest analogue to this plane's problem, and the most thought-through.
Zuul divides repositories into *config-projects* (trusted, elevated privileges)
and *untrusted-projects*. For a config-project, a proposed config change is
parsed for errors but **is not used to test the change** — the running
configuration is. For an untrusted-project, the proposed config *is* merged into
the running config so that config changes are self-testing — but those jobs run
without elevated privileges.

The rule underneath: **you may change the thing that tests you, or you may have
power, never both.** That is the same trade the frozen acceptance policy makes,
arrived at independently.

**(b) Move the definition out of the diff's reach.** Kubernetes keeps every job
definition in `kubernetes/test-infra` under `config/jobs` — a PR to
`kubernetes/kubernetes` cannot alter its own presubmits. GitLab's `ci_config_path`
can point at another project, and its own docs advise applying protected-branch
rules to the ref in that other project. Buildkite and Jenkins define the pipeline
server-side by default. This is the strongest form, and it is `.daedalus/verify.json`
taken one step further: not frozen, *external*.

**(c) Keep the gate's NAME LIST out of tree, and fail closed.** GitHub's branch
protection stores literal required check *names*, in repository settings rather
than in the repository. Delete or rename the workflow and the check simply never
reports; the PR sits on *"Expected — Waiting for status to be reported"* and is
blocked forever. The notorious footgun — rename a job and every PR jams — is this
mechanism working exactly as designed.

**(d) GitHub Actions' two-event model — the same trade as Zuul, as event names.**
`pull_request` runs the workflow from the *merge commit*, so a PR can change its
own workflow, but a fork PR gets a read-only token and no secrets.
`pull_request_target` takes the workflow definition and context from the *base
branch*, so a PR cannot change it, and secrets are available — which is precisely
why combining it with a checkout of PR head code is the classic CI vulnerability.

**(e) What most projects actually do:** CODEOWNERS on `.github/workflows/` plus
required code-owner review. Every writeup that recommends it also calls it a
partial gate.

### A note on Forgejo specifically

Forgejo has protected branches with required status checks, but **no native merge
queue** — the third-party `shunt` exists to fill that gap — and
`forgejo/forgejo#11224`, *"Pull Requests merge despite incomplete or failed
checks"*, is open.

This matters less than it looks, and in a way that favours the design proposed
here: **the plane never presses the forge's merge button.** It polls the check
status for a SHA and performs the fast-forward itself. Forgejo's own gating being
weaker than GitHub's is therefore not load-bearing — the plane's CAS is the gate.
That is an argument for doing the landing plane-side rather than delegating to
the forge's automerge, on any forge.

## Finding 1: what would be pushed at approval time is the wrong tree

The request was to push after accepting the review and before integrating. The
artifact branch is based on the artifact's own `BaseSHA`, not on the target. **The
complete set of changes does not exist yet at that moment** — it is produced by
the rebase in step 1 of the integration transaction (`integrate.go:326`).

`integrate.go:25-27` is explicit about what testing the pre-merge branch buys:

> The step people drop is the third one. Verifying the pre-merge branch proves
> something about a tree that will never exist; only the merged result is what
> actually lands.

CI on the artifact branch would re-run against roughly the tree the plane's
verifier already graded (`integrate.go:339-360` is the merged re-verify), and
would still miss the semantic merge conflict that the merge-queue pattern exists
to catch. It is useful signal. It must not be the thing that authorises landing,
or the hole reopens one layer further out.

## Recommendation: the gate goes inside the transaction, on the merged commit

```
serialize → rebase onto target → re-verify merged → [PUSH merged, await pipeline] → CAS target
```

Push the merged commit, poll the forge for the check status **of that exact
SHA**, and compare-and-swap only if it is green. This is what a merge queue is;
the plane already owns the serialization half.

## Finding 2: integration stops being a function call

`IntegrateTask` is synchronous over HTTP and holds `withClaim` for its whole
duration (`integrate.go:355`). A pipeline is five to twenty minutes. That is not
a request.

Integration has to become a **state** the task sits in while the plane polls.

The cost of that just dropped. `4f60fa6` made `internal/control/operations.go`
the single operation→states table, so a new state is a row there, and the CLI and
the Ledger pick up the legal commands from `GET /operations` without a second
list to maintain. This would be the first real dividend from what shipped on
2026-08-31.

## Finding 3: the CAS retry loop becomes expensive

`integrateAttempts = 3` (`integrate.go:39`), and each attempt is a full rebase
plus re-verify against a fresh target. Add a pipeline and each attempt costs a
quarter of an hour; if the target moves underneath, it is paid again. Today the
loop is cheap enough to be invisible.

Project-wide serialization of landings, or batching, should be settled before
this ships. Not after.

## Finding 4 — the one that must be decided first: a diff can edit its own pipeline

The plane's oracle is protected. The policy is frozen at `BaseSHA`, and
`RestoreAcceptanceFiles` (`acceptance.go:271-277`) rewrites the verifier's
checkout so acceptance files are exactly the base's before grading — *"cheating
becomes ineffective rather than forbidden, which is the same protection without
the collateral refusal"* (`acceptance.go:226-231`). That is the Sprint-59
anti-laundering design.

**An external pipeline has none of it.** It is defined by
`.forgejo/workflows/*.yml` or `.github/workflows/*.yml` at the merged commit —
files the diff can change. Adding a CI gate without covering this bolts on a
second oracle that launders trivially, one milestone after the first hole was
closed.

### Two obvious treatments, neither of which is sufficient alone

**Make workflow files acceptance globs.** Cheap; reuses `AcceptanceGlobs` and the
frozen `AcceptanceHash` wholesale. But `RestoreAcceptanceFiles` rewrites the
verifier's *throwaway* checkout, and the CI push is a real commit. Restoring
before pushing means CI tests a tree that is not what lands — Finding 1 again, in
a new place. Not restoring means the glob buys nothing. **This one does not
work.**

**Import GitHub's name list (prior art (c)).** Put the required check *names* in
`.daedalus/verify.json` — already an acceptance file, already frozen at
`BaseSHA`, already restored before grading. The workflow YAML could then change
freely; what the diff could not change is the plane's list of what must be green.

This is tempting, and it is half an answer. **It closes deletion and renaming. It
does not close neutering.** A diff that rewrites the job named `build` to
`exit 0` leaves a check called `build` reporting green, having tested nothing.
GitHub gets away with the name list only because human code review is doing the
other half of the work — which is prior art (e), the part everyone admits is
partial.

### Recommendation: both layers

1. **The required check names live in the frozen policy** — `.daedalus/verify.json`,
   frozen at `BaseSHA`, restored before grading, exactly as the `Checks` list is
   today. A missing or never-reporting check blocks. Cheap, reuses everything,
   and makes the gate fail closed the way branch protection does.
2. **Workflow files are a protected path that forfeits the automatic gate.** A
   diff touching `.forgejo/workflows/**` or `.github/workflows/**` does not
   auto-land; a human is told plainly *"this change edits the pipeline that is
   about to grade it."* This is Zuul's config-project rule and GitHub's
   `pull_request_target` rule, it matches the risk-class distinction §12.1 of the
   migration plan already draws, and it is the only thing that closes neutering.

Layer 1 without layer 2 is a gate that can be hollowed out. Layer 2 without layer
1 relies on catching every path by which a check can stop reporting.

**Still needs the operator's decision**, but the question is now narrower: whether
layer 2 escalates to a human, or instead *degrades privilege* the way Zuul does —
for this plane, plausibly "such a change may land, but never automatically, and
never with a waiver."

## Smaller things, each of which is still a decision

- **Trust the status, not the messenger.** Poll the forge API for the check run
  on the SHA the plane pushed. Do not act on a webhook payload.
- **No judgement must mean no landing.** The inverse of the reviewer rule:
  `review.go:462-476` refuses to write "no judgement" through as disapproval,
  because the reviewer is advisory. This gate is not advisory, so an unavailable
  or never-reporting pipeline must block, with a timeout that says which it was.
  This is prior art (c) — GitHub's *"Expected — Waiting for status to be
  reported"* is the same rule, and it is what makes layer 1 of Finding 4 worth
  anything. A gate that passes when it cannot hear from the pipeline is not a
  gate.
- **Credentials.** A deploy key scoped to that one repository, held on the host.
  New for the plane, though the plane already runs a network-capable reviewer
  container.
- **What "landed" means changes.** Today landing moves no branch unless asked:
  the target is projected to `refs/daedalus/target`, which nobody checks out
  (`integrate.go:44-56`, `integrate.go:87-99`). If the plane is pushing, does the
  *remote* branch move? This should be opt-in per project the way `IntoBranch`
  is, rather than quietly redefining integration.
- **Branch hygiene.** Every integration attempt pushes a commit. Whatever ref
  namespace is used needs a cleanup rule, or the remote accumulates one ref per
  attempt forever.

## Note on a dead function

`requireReviewPassed` (`review.go:503-505`) returns `nil` and is called from
`integrate.go:281`. It is a deliberately dead no-op kept under its old name so
the decision stays visible where it used to be enforced. **Part B must not
quietly become its replacement.** A pipeline gate is deterministic evidence and
is a legitimate thing to require; an *agent* verdict is not, and §12.2 of the
migration plan already tries to switch that back on. The two must not be
conflated in the same gate.

---

# Proposed backlog entries

**#97** — Show the artifact's diff in the Ledger. Two-level: `--numstat` file
list on a new `change` tab, per-file patch behind the existing disclosure
machinery, blocking findings linking to their hunk. Marks for acceptance-glob
files and for files a finding names. No push, no credentials, no new trust
boundary — the commit is already in the host repo and `acceptance.go:241` already
reads it. Closes M21's remaining half: the reviewer is handed the diff and the
human holding the seal is not.

**#98** — Gate landing on the project's own pipeline. Revisits the door #83 left
open: the plane pushes on the Job's behalf, from the host, with a repository-
scoped deploy key. The gate goes on the **merged** commit inside the integration
transaction, between the merged re-verify and the compare-and-swap — not on the
artifact branch at approval time, which is a tree that will never exist.
Requires: integration becomes asynchronous (a state, not a call); the CAS retry
loop is re-costed or landings are serialized project-wide; and **a decision on
Finding 4 (revised)** — the required check names ride in the frozen
`.daedalus/verify.json`, *and* workflow files become a protected path that
forfeits the automatic gate. Both layers, per the prior art.

## Open questions for the operator

1. Finding 4, revised — layer 2 escalates to a human, or degrades privilege
   Zuul-style ("may land, never automatically, never with a waiver")? Blocks all
   of #98.
2. Does a landing push the remote branch, or only the plane's ref? Opt-in?
3. Is #97 wanted standalone and first? It has no dependency on #98, and #98's
   review step is more useful once #97 exists.

---

# Sources

Consulted 2026-09-02 for the prior-art section.

- [The origin story of merge queues](https://mergify.com/blog/the-origin-story-of-merge-queues) — bors, the Not Rocket Science Rule, merge skew
- [GitHub merge queue](https://matklad.github.io/2023/06/18/GitHub-merge-queue.html)
- [Zuul — project configuration](https://zuul-ci.org/docs/zuul/latest/project-config.html) — config-projects vs untrusted-projects
- [Zuul — tenant configuration](https://zuul-ci.org/docs/zuul/4.8.0/reference/tenants.html)
- [kubernetes/test-infra](https://github.com/kubernetes/test-infra) — job definitions and `tide`, outside the repo under test
- [Troubleshooting required status checks](https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/collaborating-on-repositories-with-code-quality-features/troubleshooting-required-status-checks) — the name list, and failing closed
- [Events that trigger workflows](https://docs.github.com/en/actions/using-workflows/events-that-trigger-workflows) — `pull_request` vs `pull_request_target`
- [GitLab #14376 — CI job definition outside the repository](https://gitlab.com/gitlab-org/gitlab/issues/14376)
- [Forgejo — branch and tag protection](https://forgejo.org/docs/latest/user/protection/)
- [shunt — a merge queue for Forgejo and Gitea](https://github.com/rbtr/shunt)
- [forgejo#11224 — PRs merge despite incomplete or failed checks](https://codeberg.org/forgejo/forgejo/issues/11224)
