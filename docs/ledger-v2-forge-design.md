# The Ledger, re-forged

*Should the Ledger incorporate Forgejo — and if so, how? A design for a
Ledger that helps productivity instead of taxing it, built from the four-flow
ratchet analysis, three post-mortems, current research, and what forges can
actually do in 2026. Includes an honest look at the jRPG skin.*

**Status: PROPOSAL.** Written 2026-09-26. Starting point:
[`flows-checkpoints-and-ratchets.md`](flows-checkpoints-and-ratchets.md).
Predecessor design: [`reviewing-and-landing.md`](reviewing-and-landing.md)
(2026-09-02), whose Part B this supersedes and extends.

---

## 0 · The answer first

**Yes, incorporate the forge — but as the *medium* of work, not the
authority over it.** Three roles, cleanly split:

- **The forge holds the work.** Every attempt becomes a pull request; the
  project's own pipeline runs against it; the diff, the comments and the
  history live where they already live for every other change.
- **The daemon holds the authority.** Frozen intent, the independent oracle
  floor, budgets, caller-class tiers, the serialized landing, the append-only
  record — the things a forge structurally cannot provide, because on a forge
  the checks live inside the diff they judge and the settings can be lowered
  by anyone with admin (Egor did exactly that, and never restored it).
- **The Ledger becomes the decision surface.** Not a second forge UI — an
  inbox of exactly the things only a human can do, with the evidence attached,
  and deep links to the forge for everything else.

The alternative extremes both fail on the evidence. *Full forge replacement*
throws away the frozen oracle and serialized landing precisely when research
says agents game editable checks and uncoordinated agent PRs collide. *Status
quo* keeps a parallel universe with a soft pawl, coarse teeth, a jammed wheel,
and a cost-per-click that made two real projects route around it entirely.

On the UI: **keep the jRPG identity, take it out of the working vocabulary.**
The evidence says decoration alone doesn't help and pretty interfaces make
people *more tolerant of usability flaws* — which for a solo tool means the
skin may be quietly subsidizing the defects. The productivity cost of today's
Ledger is not the parchment; it is the missing diff, the destructive repaints,
the three-names-for-one-state vocabulary, and the price of a click. Fix those;
let the scroll icon stay.

---

## 1 · Why change anything — the productivity case

The four-flow analysis ended with a verdict: the Ledger is the most
literature-correct design of the four flows and the least effective in
practice. Nobody used it. Three projects shipped real work in the same weeks
through plain PR flows, and their post-mortems record what that cost — a
broken dashboard shipped and stayed shipped (Egor), six defects invisible to
green CI (flashcards), a discarded PR and a startup failure in a packaged
release (Snowball).

The 2026 numbers say this problem is now general, not local:

- Teams using agent CLIs report **~98% more pull requests** opened and
  **median review time up 441%** — the bottleneck has relocated from writing
  code to verifying it
  ([The Human Review Bottleneck](https://codex.danielvaughan.com/2026/05/24/human-review-bottleneck-code-review-strategies-agent-output/)).
- LinearB measures agent-authored PRs waiting **4.6–5.3× longer** for first
  review than human ones
  ([AI agents open PRs faster than we review them](https://dev.to/prokopsimek/ai-agents-open-pull-requests-faster-than-we-review-them-a-setup-that-keeps-the-queue-moving-3427)).
- GitHub shipped a dedicated **pull-request dashboard with per-agent queues**
  (`copilot/`, `claude/`, `codex/`, `cursor/`, `devin/` branch prefixes) —
  the "agent inbox" is now a product category
  ([same source](https://dev.to/prokopsimek/ai-agents-open-pull-requests-faster-than-we-review-them-a-setup-that-keeps-the-queue-moving-3427)).
- OpenAI's Codex documentation states the underlying bet plainly: *"the safe
  unit of AI coding work is a reviewable change rather than an automatic
  commit"* — and its whole UI exists to keep human review fast enough that
  the human stays in the loop without becoming the bottleneck
  ([How OpenAI Codex works](https://theaiengineer.substack.com/p/how-openai-codex-works)).

Read together: **the industry converged on the PR as the unit of agent work,
and then immediately hit the wall the Ledger was built for** — review capacity.
The Ledger's *concept* (a governed queue of decisions over agent output) is
validated by the whole field building inboxes. Its *substrate* (a parallel
task store with its own diff-less viewer, its own states, its own landing
ref nobody checks out) is what nobody adopted. Keep the concept; change the
substrate.

---

## 2 · What a forge can and cannot give us

### 2.1 What Forgejo brings (researched, current)

- **AGit flow: a PR by push alone.** `git push origin
  HEAD:refs/for/development -o topic=T-42 -o title="…" -o description="…"`
  creates a pull request **with no fork and no persistent branch** — the PR
  is a database object keyed by topic. Updates reuse the topic; rebases and
  amends need `-o force-push=true` or a new PR is opened
  ([Forgejo AGit documentation](https://forgejo.org/docs/latest/user/agit-support/)).
  This dissolves the branch-hygiene objection from the September 2
  design (one ref per attempt accumulating forever): attempts are
  force-pushes to one topic, and nothing persists but the PR record.
- **A full PR API** — create, draft state, reviewers, labels; commit
  statuses per SHA; webhooks; Forgejo Actions for CI
  ([Forgejo docs](https://forgejo.org/docs/latest/user/),
  [PRs and Git flow](https://forgejo.org/docs/latest/user/pull-requests-and-git-flow/)).
- **Branch protection with required checks** — but **no native merge queue**;
  the third-party `shunt` exists to fill that gap, and
  [forgejo#11224](https://codeberg.org/forgejo/forgejo/issues/11224)
  (*PRs merge despite incomplete or failed checks*) is open.

### 2.2 What a forge structurally cannot give

These are the daemon's reasons to exist, each anchored in evidence:

1. **An oracle outside the diff.** Workflow files live in the repository; a
   change can edit, weaken or neuter the pipeline that judges it. Egor's CI
   was broken and reverted by the very stream of changes it was grading.
   The research is blunt:
   [agents game tests they control](https://arxiv.org/pdf/2602.07900), and
   pre-frozen, independently-held oracles are the countermeasure — which is
   what the daemon's `acceptance_hash` mechanism already is.
2. **Settings that cannot be quietly lowered.** Forge protection is admin
   config. Egor lowered `required_approvals` to unblock one merge and never
   restored it — the *lifted pawl*. Authority that lives in a mutable setting
   is a lever, not a ratchet.
3. **A serialized, re-verified landing.** Forgejo has no merge queue, and its
   check enforcement has open defects. Two changes green in isolation can
   fail combined; only re-verifying the *merged* result catches that, and the
   daemon's rebase → re-verify → compare-and-swap transaction already does.
4. **Caller-class authority.** On a forge, a bot account with a token is just
   an account; the daemon derives agent-vs-human from which socket a request
   arrived on, and consequential agent requests become human-confirmed
   proposals. Prompt-injected agents on a forge can approve things; here they
   structurally cannot.
5. **Cross-project intent.** Programmes, rationale, budgets, the Guild
   Master's read-across — none of it exists in forge vocabulary.

**Conclusion of §2:** neither replacement nor status quo. The forge is the
best available *work substrate* — diffs, comments, CI, artifacts, identity —
and a poor *authority*. The daemon is the inverse. Combine them along that
seam.

---

## 3 · The design: Ledger v2

### 3.1 One sentence

*A Task becomes a pull request the moment it has something to show; the
project's own pipeline becomes the loudest check on it; the daemon still
freezes intent and oracle, still serializes the landing, and still owns
approval; and the Ledger shrinks to the one screen the human actually needs —
a triaged inbox of decisions with evidence attached.*

### 3.2 The lifecycle, mapped

| Daemon state | Forge reality | Who acts |
|---|---|---|
| **planned** — objective, deliverables, non-goals, rationale, budget; base + oracle + image frozen | nothing yet — intent precedes the PR | human (or agent via bounded create) |
| **working** — Job in an isolated worktree at the frozen base | *optional:* a **draft PR** opened at dispatch via AGit push, so the work is visible from minute one | daemon |
| **candidate** — the attempt committed | AGit push to `refs/for/<target-branch>` with `topic=T-n`: the PR exists (or leaves draft), body = objective + deliverables + **the unverified-claims list** | daemon |
| **verifying** | two pawls, both reported as commit statuses on the head SHA: **(a) the frozen floor** — the daemon's hermetic checks, oracle restored, exactly as today; **(b) the real pipeline** — the project's own Forgejo Actions run | daemon + forge |
| reviewer pass | posted as a **PR review** (comment thread), advisory as today — findings land where every other reviewer's comments land | daemon |
| **approval_required** | the PR carries statuses, the agent review, the diff. The Ledger shows the decision card; the forge shows the change | **human** |
| **approved → integrating** | the landing transaction, now asynchronous: rebase onto target → push merged SHA → **await the required checks on that exact SHA** → compare-and-swap | daemon |
| **integrated** | the daemon **merges the PR and advances the real branch** — the target and the branch tip become the same thing | daemon |
| retry / refine | force-push to the **same AGit topic** — the PR's history *is* the attempt chain | daemon |
| cancel / reject | PR closed with the typed reason as the closing comment | daemon |

Six load-bearing choices inside that table:

**(1) Approval stays in the daemon.** A forge approval is a click governed by
a setting an admin can lower (Egor's C7, live). The Ledger's seal is an
operation on the human socket, recorded, with a waiver that snaps back. The
forge's review UI is welcome as *input*; it is never the mechanism. Mirroring
is one-way: the daemon may *post* "sealed by operator" to the PR; it never
*reads* approval from it.

**(2) Two pawls, honestly labelled.** The hermetic frozen check answers "does
this artifact still meet the bar the task was created under, judged by an
oracle the worker couldn't touch" — keep it, it is the piece nobody else has.
The Actions run answers "does the real build and test suite pass" — realistic,
network-attached, *not* hermetic, and editable by the diff. The two-layer
protection from the September 2 design applies: the **required check names**
ride in the frozen `.daedalus/verify.json`, and a diff touching
`.forgejo/workflows/**` forfeits the automatic path and is flagged to the
human in exactly those words: *"this change edits the pipeline that is about
to grade it."* (Zuul's config-project rule; prior art in
`reviewing-and-landing.md`.)

**(3) The landing closes the jammed wheel.** Today "integrated" advances a
ref nobody checks out, and the target drifted four days behind a moving
branch. In v2 the daemon merges the PR: **the branch is the target.** Target
lag stops being a state to monitor and becomes a contradiction in terms —
provided direct pushes to the target branch are either protected away or
*adopted*: a reconcile pass that observes a moved branch and re-pins, loudly,
instead of silently grading against the past. Sync stops being a ritual and
becomes an observation.

**(4) The lifted-pawl watchdog.** The daemon knows what the forge settings
*should* be (required checks, required approvals, protected paths) because
the frozen policy names them. A reconcile tick compares and reports drift:
*"required_approvals on development is 0; the policy expects 1; lowered
2026-09-25, never restored."* It reports rather than rewrites — the operator
may have meant it — but it reports every tick until acknowledged. This is the
cheapest genuinely new mechanism in this design, and Egor's week is its whole
justification.

**(5) The unverified-claims field.** Egor's most useful honest artifact was a
PR body saying *"this test has never been executed."* Make that a first-class
Task field the worker fills ("what I could not verify and why"), rendered in
the PR body **and** on the decision card. Honest text is not a pawl, but it
is exactly what the human should spend their attention on — it converts
review from a search into a checklist, which is what it did for Egor's
final-day defect hunt.

**(6) The delivery tooth, with its execution pawl.** Snowball's per-PR
runnable prerelease becomes a first-class option: a landing can trigger the
project's release workflow, and — the Egor amendment — a **smoke check that
starts the artifact** is a named required check like any other. Producing and
executing are two teeth. Severity coupling (a P1 fix triggers a release
rather than waiting for a feature batch) is one policy line once releases are
forge workflows.

### 3.3 Tooth size: smaller tasks, stacked when needed

The review-unit research is unambiguous:
[PRs of 200–400 lines have ~40% fewer defects and are approved ~3× faster;
each additional 100 lines adds ~25 minutes of review; past 1,000 lines defect
detection drops ~70%](https://pullnotifier.com/tools/stacked-prs). The
[stacked-diff lineage](https://newsletter.pragmaticengineer.com/p/stacked-diffs)
(Phabricator → ghstack → Graphite) and
[Gerrit's change-per-commit model](https://graphite.com/guides/gerrits-approach-to-code-review)
exist because the *reviewable unit* is the productivity lever.

For the Ledger this means two things, one cheap and one structural:

- **Cheap:** the Task creation surface nudges toward slice-sized objectives —
  a soft line-budget on the diff (warn past ~400 changed lines, per the
  triage literature's 250-line hard cap for agent output), and the deliverables
  field doubles as the slice list.
- **Structural (later):** AGit topics give dependent-PR stacking almost for
  free — `T-42.1`, `T-42.2` as chained topics, landed in order by the same
  serialized transaction. This is the flashcards walking-skeleton cadence
  expressed in daemon vocabulary. It is deliberately *not* in the first
  phase; nothing else here depends on it.

### 3.4 What the Ledger UI becomes

Today the Ledger is a full parallel client: board, archive, programmes,
proposals, adoption rows, per-task tabs — everything, except the one thing a
decision needs (the diff). v2 inverts the emphasis:

**The home screen is a triaged decision inbox.** Following the risk-tier
result — [gating only the riskiest ~20% of PRs captures ~69% of total review
effort](https://codex.danielvaughan.com/2026/05/24/human-review-bottleneck-code-review-strategies-agent-output/)
— every waiting decision is a card in one of four lanes:

- **Decide now** — P0/P1 paths touched (auth, migrations, workflow files,
  release config; path patterns declared in `verify.json`), or a check
  waived, or a proposal from an agent.
- **Decide today** — standard feature work, all checks green, agent review
  clean.
- **Glance** — mechanical changes, green, no findings: one-click seal, batch
  seal allowed.
- **Stuck** — failing checks, unsatisfiable dependencies, budget exhausted:
  things needing an operator action other than approval.

**Each card carries the decision evidence, nothing else:** objective ·
deliverables checked off · **the unverified-claims list** · risk paths hit ·
check statuses (frozen floor + pipeline, separately) · the agent reviewer's
verdict line · diff stat — and one deep link to the forge PR for the full
diff and conversation. The card answers the intent-verification question the
triage literature puts first (*"does this change match what was asked?"*);
the forge answers the code questions. The Ledger stops re-implementing a diff
viewer the day the forge link exists — for no-forge projects the minimal
built-in file-list-plus-patch (#97) remains the fallback.

**Everything else demotes.** Board, programmes, archive, adoption become
secondary views reachable from the inbox, not the front page. The Guild view
stays what it is — an ambient status wall (see §4).

**Interaction costs drop.** Keyboard: `j/k` between cards, `a` seal, `r`
reject, `o` open on forge — the inbox pattern users already have reflexes
for. Batch-seal on the Glance lane. And the repaint machinery honors reading:
the current 15-second empty-and-refill with its scroll resets is replaced by
diffed updates (the Guild view already diff-updates cards; the pattern is in
the codebase).

### 3.5 Authority, credentials, adapters

- **Credential:** the daemon holds one forge token per project (or a deploy
  key), host-side, never inside a Job container — #83's decision stands; the
  daemon pushes on the Job's behalf after capture. Scope: that repository.
- **Trust the status, not the messenger:** webhooks may *wake* the daemon;
  decisions poll the API for the status of an exact SHA (the September 2
  rule, unchanged).
- **Forge adapter seam:** a small interface — `CreatePR / UpdatePR /
  StatusFor(sha) / Merge / ProtectionSettings` — with Forgejo/Gitea first
  (AGit is their native strength) and GitHub later (same shape: PR + checks
  + merge; AGit replaced by an ordinary branch push). This is the same seam
  discipline as `AgentRunner` / `VerifyRunner`.
- **Agents on the forge:** worker agents get no forge credential at all.
  The Guild Master reads PR state through the daemon (`guild-control-mcp`
  read tools extended with PR references), never through a forge token of
  its own — the lethal-trifecta boundary stays where it is.

### 3.6 Migration, in phases that each pay for themselves

1. **Diff at the decision** (#97, already designed) — needed regardless of
   any forge; also the fallback view forever. *Small.*
2. **Candidate → AGit PR + statuses.** The daemon pushes candidates as PRs,
   posts the frozen-floor result as a commit status, posts the agent review
   as a PR review. Nothing about authority changes; visibility moves to the
   forge. *Medium.*
3. **Landing waits on the pipeline** (#98's shape): integration becomes a
   state; merged SHA pushed; required check names from frozen policy;
   workflow-file protected path; merge advances the real branch. *The
   milestone.*
4. **The inbox UI** — lanes, cards, evidence, keyboard, batch; board and
   programmes demote to secondary views. *Medium.*
5. **The watchdog and the delivery tooth** — protection-drift reporting;
   optional release-on-landing with a smoke check. *Small each.*
6. *(Optional, later)* stacked topics for slice-sized tasks.

Phases 1–2 are useful even if 3 is never built. Phase 3 is where the soft
pawl actually hardens. Nothing requires a big-bang cutover, and the no-forge
degraded mode is the current system, which keeps working.

### 3.7 What this deliberately does not do

- **No auto-merge on green.** The human seal survives; what changes is what
  the human is shown and how many clicks the seal costs. Layered supervision
  redistributes oversight; it does not delete it.
- **No forge-side authority.** Required approvals on the forge are welcome
  *defence in depth*; the daemon never treats them as the mechanism.
- **No second reviewer path.** The agent review stays advisory and stays
  single — a verdict that moved state would be an unbounded oracle.
- **No GitHub-first.** The projects at hand run Forgejo; the adapter seam
  keeps the door open without paying for it now.

---

## 4 · The jRPG question

The Ledger speaks in fantasy: *the Ledger* 📜, *entries*, *awaiting seal*,
*"Awaiting your word"*, *"The ledger is closed"*; the Guild renders projects
as Secret-of-Mana pixel heroes who work, idle and sleep. The question asked:
lovely — but does it help?

### What the evidence actually says

- **The [aesthetic-usability effect](https://www.nngroup.com/articles/aesthetic-usability-effect/)**
  (NN/g): visually appealing interfaces make users *more tolerant of real
  usability problems* — the correlation between beauty and *perceived* ease
  of use is stronger than between beauty and *actual* ease of use. For a
  solo maintainer who is also the designer, this cuts one way: the skin you
  love is subsidizing the flaws you'd otherwise fix. The Ledger's genuinely
  missing pieces — no diff, scroll-eating repaints, no keyboard path, no
  batching — coexisted with a beautiful surface for months.
- **Gamification meta-analyses** ([Ritzhaupt et al., 2023](https://pmc.ncbi.nlm.nih.gov/articles/PMC10591086/);
  [overview of the 2025 nuance literature](https://theeconomyofmeaning.com/2025/04/22/gamification-more-than-just-fun-a-meta-analysis-provides-nuance/)):
  overall effects exist, but **decorative elements alone — badges, progress
  bars, theme — show no significant effect**; what works is goals, challenge,
  feedback and curiosity, and there is a **novelty curve** — the charm fades
  after weeks. Translated: the parchment is not producing productivity; fast
  feedback loops would be. (The evidence base is education-heavy; treat it as
  directional for tools.)
- **[Recognition over recall](https://aguayo.co/en/blog-aguayo-user-experience/what-are-the-10-usability-principles-by-nielsen/)**
  (Nielsen heuristic #6) and
  [aesthetic-minimalist design](https://www.nngroup.com/articles/aesthetic-minimalist-design/)
  (#8): every extra mapping a reader must hold — *awaiting seal* ↔
  `approval_required` ↔ a forge review request — is recall load. The Ledger's
  own MARK table translates nine daemon states into flavor words, the CLI
  speaks the state names, and the forge will speak a third dialect. Three
  names per state is a tax paid on every glance, and it is about to get worse
  with a forge in the loop.
- **[Skeuomorphism research](https://www.nngroup.com/articles/aesthetic-usability-effect/)**
  cuts both ways: familiar metaphor aids first contact; it costs precision
  for expert daily use. The Ledger's user is one expert, daily.

### The verdict: split identity from information

The jRPG style is doing two different jobs, and only one of them is earning:

- **As identity and ambience — keep it.** The name, the scroll, the empty-state
  sentences, and above all the **Guild wall**: an at-a-glance, low-attention
  status surface is exactly where character art and animation are appropriate
  — glanceability is the job, delight is free, and nothing on that screen is
  a decision. This is also, frankly, why the tool is loved by its own
  maintainer, which for a solo project is a real productivity input the
  meta-analyses don't measure.
- **As working vocabulary — retire it.** Anywhere a decision or a state is
  communicated, use **one name per state, the daemon's name, everywhere** —
  Ledger chips, CLI output, forge labels, event log. *approval_required* is
  shown as **Approval required**, not *awaiting seal*; the flavor line can
  sit beneath in secondary text if wanted. Actions say what they do —
  **Approve**, not *Seal*. The 📜 stays in the corner; the state column stops
  being a translation exercise.

And name the real productivity list plainly, because none of it is the skin:
the diff at the decision (§3.4), diffed repaints instead of empty-and-refill,
list scroll preserved, keyboard navigation, batch actions on the low-risk
lane, and fewer clicks per decision. The skin was never the problem; it was
the alibi.

---

## 5 · Is it a good idea? The honest weighing

**For** — grounded in the four-flow evidence:

- Hardens the soft pawl with the project's own real pipeline (the single
  biggest defect found in the Ledger), while keeping the frozen floor no
  forge can offer.
- Closes the jammed wheel by making the landing move the branch people use.
- Adds the lifted-pawl watchdog the Egor week proves is needed.
- Meets agents where they already are: every major agent product ships
  PR-based flows; the forge is where diffs, comments and CI already work,
  maintained by someone else.
- Shrinks the Ledger's own surface — less parallel UI to maintain, less
  drift of the #95/#99 kind (three copies of one truth).

**Against — and the mitigations:**

- *A new dependency and credential.* The daemon now needs a forge token and
  the forge needs to be up. Mitigated: degraded mode is today's flow; the
  token is host-side and repo-scoped (#83's rule).
- *The realistic pawl is not hermetic.* Actions runners have network and
  caches; a verdict from them is evidence, not reproducible proof. Mitigated:
  two pawls, labelled; the hermetic floor keeps its meaning; `verified` never
  claims more than what ran.
- *Forgejo's own enforcement is imperfect* (no merge queue, #11224 open).
  Mitigated — in fact neutralized: the daemon never presses the forge's merge
  button on faith; it polls the SHA and performs the landing itself. The
  forge's weakness is an argument *for* daemon-side landing, not against
  integration.
- *Complexity moves into the seam.* An adapter, webhook/poll plumbing, AGit
  topic bookkeeping. Real cost; the phases keep each step small and
  independently useful.
- *The one-way door risk:* once workflows, statuses and PR habits assume the
  forge, retreating to the pure-daemon flow costs a redesign. Accepted
  knowingly — the pure-daemon flow's observed adoption is zero.

**Net: yes.** The Ledger's authority machinery is validated by the research
and by nobody else having it; its work-holding machinery is duplicating a
forge, worse. Specialize each.

---

## 6 · Open questions for the operator

1. **Workflow-file edits** (the one that blocks phase 3, unchanged from the
   September 2 design): does a diff touching `.forgejo/workflows/**`
   escalate to a human, or land under reduced privilege — never
   automatically, never with a waiver?
2. **Draft-PR-at-dispatch or PR-at-candidate?** Visibility from minute one
   versus forge noise per attempt. Recommendation: candidate, with dispatch
   optional per project.
3. **Who may merge the target branch besides the daemon?** Protect it fully
   (daemon-only, cleanest ratchet) or allow direct human pushes with
   adopt-and-repin reconciliation (friendlier, weaker)?
4. **Does the inbox replace the board as the front page**, or sit beside it?
   This design says replace; the board demotes to a view.
5. **State vocabulary:** confirm the one-name-per-state rule, since it
   touches CLI output and the MARK table and is user-visible churn.
6. **Risk-tier paths** (`P0`/`P1` patterns) live in `verify.json` — per
   project, frozen with the rest? (Recommended: yes, same freeze, same
   reasons.)

---

## 7 · Sources and references

**Forge capabilities**

- [AGit workflow usage — Forgejo docs](https://forgejo.org/docs/latest/user/agit-support/) — PR-by-push, topics, force-push semantics, no persistent branch
- [Pull requests and Git flow — Forgejo docs](https://forgejo.org/docs/latest/user/pull-requests-and-git-flow/)
- [Forgejo user guide](https://forgejo.org/docs/latest/user/) and [Forgejo Actions reference](https://forgejo.org/docs/v15.0/user/actions/reference/)
- [Create a pull request from git in Forgejo — Miek Gieben](https://miek.nl/2026/january/28/create-a-pull-request-from-git-in-forgejo/) — AGit in practice
- [forgejo#11224 — PRs merge despite incomplete or failed checks](https://codeberg.org/forgejo/forgejo/issues/11224)
- [shunt — a merge queue for Forgejo and Gitea](https://github.com/rbtr/shunt)
- [forgejo-create-pr action](https://github.com/maxking/forgejo-create-pr) — API-based PR automation precedent

**The review bottleneck and agent-PR practice**

- [The Human Review Bottleneck — Codex Knowledge Base](https://codex.danielvaughan.com/2026/05/24/human-review-bottleneck-code-review-strategies-agent-output/) — risk tiers P0–P3, the 20%/69% concentration result, the 250-line cap, the five-layer review stack
- [AI agents open pull requests faster than we review them — DEV Community](https://dev.to/prokopsimek/ai-agents-open-pull-requests-faster-than-we-review-them-a-setup-that-keeps-the-queue-moving-3427) — LinearB pickup-time numbers; GitHub's agent-queue dashboard; queue-discipline habits
- [How OpenAI Codex works](https://theaiengineer.substack.com/p/how-openai-codex-works) — "the safe unit of AI coding work is a reviewable change"
- [8 cloud coding agents that open PRs while you sleep](https://ssojet.com/blog/best-cloud-coding-agents) — the PR-based agent product landscape

**Review-unit research and stacked change flows**

- [Stacked diffs (and why you should know about them) — Pragmatic Engineer](https://newsletter.pragmaticengineer.com/p/stacked-diffs)
- [Stacked PRs explained (2026)](https://pullnotifier.com/tools/stacked-prs) — the 200–400-line defect and approval-speed numbers
- [Gerrit's approach to code review — Graphite](https://graphite.com/guides/gerrits-approach-to-code-review) and [Basic Gerrit walkthrough for GitHub users](https://gerrit-review.googlesource.com/Documentation/intro-gerrit-walkthrough-github.html)
- [Stacked diffs versus pull requests — Jackson Gabbard](https://jg.gg/2018/09/29/stacked-diffs-versus-pull-requests/)

**Research (2025–2026), carried over from the ratchet analysis**

- [When Review Alone No Longer Scales: Layered Supervision in AI-Assisted Software Engineering](https://arxiv.org/pdf/2608.26316)
- [Rethinking the Value of Agent-Generated Tests](https://arxiv.org/pdf/2602.07900) — agents game tests they control; frozen/independent oracles win
- [Independent Patch Verification for Coding Agents](https://arxiv.org/pdf/2608.08950)
- [BulkPR-Bench: Queue-Level Governance of Interacting Pull Requests](https://arxiv.org/pdf/2608.02685)
- [Code Review Agent Benchmark](https://arxiv.org/html/2603.23448v3)

**UI evidence**

- [The aesthetic-usability effect — NN/g](https://www.nngroup.com/articles/aesthetic-usability-effect/) — beauty raises tolerance of real usability flaws
- [Aesthetic and minimalist design — NN/g](https://www.nngroup.com/articles/aesthetic-minimalist-design/)
- [Nielsen's usability heuristics (recognition over recall)](https://aguayo.co/en/blog-aguayo-user-experience/what-are-the-10-usability-principles-by-nielsen/)
- [Gamification effectiveness meta-analysis (49 samples, g = 0.822 overall; decorative elements alone not significant)](https://pmc.ncbi.nlm.nih.gov/articles/PMC10591086/)
- [Gamification: more than just fun? — 2025 meta-analysis nuance, the novelty U-curve](https://theeconomyofmeaning.com/2025/04/22/gamification-more-than-just-fun-a-meta-analysis-provides-nuance/)
- [The role of skeuomorphism in modern UI](https://medium.com/@blessingokpala/the-role-of-skeuomorphism-in-modern-ui-more-than-just-nostalgia-844ee11819e9)

**This repository**

- [`flows-checkpoints-and-ratchets.md`](flows-checkpoints-and-ratchets.md) — the four-flow analysis and failure-mode vocabulary this design starts from
- [`reviewing-and-landing.md`](reviewing-and-landing.md) — the 2026-09-02 predecessor: diff-at-the-decision (#97) and pipeline-inside-the-landing (#98), including the prior art (bors, Zuul, GitHub merge queue, Prow) this design inherits
- [`post-mortem-lessons-for-daedalus.md`](post-mortem-lessons-for-daedalus.md) — verified defects in this repository the phases address
- [`control-plane.md`](control-plane.md) — the as-built daemon this design keeps as the authority
- `post-mortems/egor-postmortem-week-2026-09-19.md`, `post-mortems/flashcards-m28-postmortem.md`, `post-mortems/snowballv2-POST_MORTEM.md` (local, not committed) — the measured weeks behind every mechanism above
- `BACKLOG.md` #74, #79, #83, #89 — the soft pawl, the invisible landing, the credential rule, the jammed wheel

**Method note.** Forge capabilities were taken from Forgejo's current
documentation and one practitioner writeup; AGit semantics (topic reuse,
force-push) should be verified against the target Forgejo version before
phase 2 is built. The review-bottleneck and review-unit numbers are vendor
and practitioner measurements, not peer-reviewed; they agree with each other
and with the arxiv results in direction, and are used here for direction
only. The gamification evidence is education-heavy and applied to a
developer tool by argument, not by measurement — the aesthetic-usability
effect is the best-attested piece and the one this design leans on.
