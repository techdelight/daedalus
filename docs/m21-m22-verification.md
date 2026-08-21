# Verifying Milestones 21 and 22

A playbook for what M21 (*The Programme, Read by the Person Deciding*) and M22
(*Two Graphs, and the Distance Between Them*) actually built, and for telling the
proven parts from the argued ones.

Read it in the order things deserve to be distrusted. The plane's own logic is
cheap to check and rarely wrong. **The browser is the opposite, and in this
milestone it is also the deliverable** — which is uncomfortable, because this
environment has no browser, so the Ledger's half is the one part of M21 that no
test here touches. That is stated up front rather than discovered at the bottom.

---

## 0. The 60-second version

```bash
go test ./internal/control/ ./internal/web/ ./internal/tui/ -count=1
bash scripts/verify-guild-control.sh static     # 29 assertions, no Docker
```

The second one drives the Guild Master's whole programme path over the **real
sockets** — list, form, amend, dissolve, each as a proposal, each confirmed by a
human before anything changes.

---

## 1. Was the person deciding actually shown anything?

The claim: the programme and the rationale reach the human, not only the reviewer
agent.

```bash
daedalus programmes create fluency "one way to theme, everywhere"
daedalus programmes add-project fluency my-app
daedalus task create --project my-app --objective "theme the app" \
  --programme fluency --rationale "the operator asked"
```

Then open the Ledger, put the cursor on the task, and read the entry page.

**What to look for.** A `for:` line naming the programme *and its description*,
and a `reason:` line with `(human)` or `(agent)` beside it. The author is not
decoration: an agent may draft a good reason, and the person deciding is the one
who should weigh whose reason it is.

Then look at a task with neither. It should say **"No programme, and no recorded
reason. You are deciding on the objective alone."** — an absence stated, not a
blank line. A blank says nothing; the sentence says something true.

```bash
curl --unix-socket … /api/control/approvals    # or just read the page
```

**What this replaced.** `approvalTask` used to carry an id, a project, an
objective, a base SHA and a state. `AgentReviewer` has been handed the diff, the
objective, the rationale and the programme since Sprint 67. The party that only
reports was shown more of the intent than the party with the authority to act.

**Asserted by tests:**
`TestApprovals_CarryProgrammeAndRationale`,
`TestApprovals_ProgrammeUnreadable` (the queue survives a failed programme
lookup — an approval that vanishes reads as "nothing needs you"),
`TestApprovals_ShowIntentUnderTheCursor` and
`TestApprovals_UnknownProgrammeShowsItsID` in the TUI.

**Not asserted anywhere:** that the Ledger renders any of it. See §4.

---

## 2. Is the declared order still a claim nobody checks?

The claim: the two dependency graphs are now compared.

```bash
daedalus programmes add-dep fluency other app
daedalus programmes status fluency
```

**What to look for.** A section headed *Declared order, and what enforces it*.
An edge that nothing enforces says so, and says **why** — one of four answers, and
the difference between them is the point:

| What it says | What it means |
|---|---|
| `no open work on either side yet` | nothing to declare; the work does not exist |
| `nothing open in X to wait for` | half the work exists |
| `nothing open in X to do the waiting` | the other half |
| `T-5 could wait for T-9` | a declaration you have not made |

```bash
daedalus programmes status fluency --suggest-deps
#   → daedalus task depends T-1 --on T-2
```

**It prints and does not run.** Check this by reading the code if you like, but
the argument is the check: a dependency edge decides what must happen before a
Task is graded, so a tool that wrote edges from a plan would be filling in the
graph that gates from the graph that does not. An agent proposing its own
dependency edges is already `TierProposal` for exactly this reason.

Run the suggested command and re-read the status. The same edge now reads
`enforced by T-1 → T-2`.

**The other direction, and the more interesting one:**

```
Enforced, but never declared:
  T-1 waits for T-2  (app ← other)
```

The work found a dependency the plan does not mention. Either the plan is out of
date or the edge is wrong, and both are worth knowing.

**Asserted by tests:** `TestProgrammeStatus_ADeclaredEdgeNobodyEnforces`,
`TestProgrammeStatus_ReportsAnEnforcedEdgeNobodyDeclared`,
`TestProgrammeStatus_SameProjectEdgesAreNotADivergence`,
`TestProgrammeStatus_AnEdgeLeavingTheProgrammeStillEnforces`.

**Verified by reverting.** Flipping the direction of the enforcement map's key
fails the first and the last with the messages that name the property. That check
matters more than the tests passing: an enforcement report that is right about
existence and wrong about direction would be worse than none.

---

## 3. The Guild Master's whole programme path

```bash
bash scripts/verify-guild-control.sh static
```

**What to look for.** Checks 21–29: the agent lists programmes (allowed), forms
one (422 `proposal_recorded`, nothing created), a human confirms, the description
survives a colon intact; then the same shape for **amend** and for **dissolve**,
each unconfirmed-then-confirmed, with an assertion between the two halves that
nothing changed while the proposal was pending.

29 passed, 0 failed, up from 20 in #82.

---

## 4. What M21 and M22 did **not** do

Stated here so it is not discovered later.

- **No test in this repository renders the Ledger.** The programme view, the
  intent block on the Task entry and the divergence display are JavaScript, and
  this environment has no browser and no Playwright run. They are checked by
  reading, by a bracket-balance pass, and by the fact that the routes and payloads
  underneath them are tested. **This is the same class of gap that let the e2e
  suite drive deleted routes for three sprints** — see below.
- **The e2e suite was found testing routes deleted in Sprint 66.** Nine tests
  against `/api/programmes`, a handler that no longer exists. They are rewritten
  against `/api/control/programmes` and skip when the daemon is down, and **they
  have still not been run**, for the same reason they rotted.
- **Programme-aware admission does not exist.** The per-programme running and
  queued counts are reporting. The scheduler admits on the global and per-project
  limits and knows nothing about programmes. Making the numbers a scheduling input
  waits on backlog **#70**: `waiting` is an in-memory map a restart erases, and
  fair-share over something that forgets would look like a guarantee and not be
  one.
- **The declared graph still gates nothing**, deliberately, and now says so in
  three places instead of one.
- **A dissolution proposal carries no reason.** `callerScope` builds its argument
  as `dissolve <id>` and there is no room for one; carrying a `why` would mean
  widening `DeleteProgramme` across `TaskAPI`. The tool therefore does not offer a
  field, because a reason the record silently dropped would be worse than an
  absent one — the agent would believe it had explained itself to whoever has to
  decide.
- **A programme still cannot be concluded.** It has a description and no way to
  record whether it was worth forming. `daedalus programmes create` says in its
  own hint that a programme with no stated purpose "cannot tell you later whether
  it was worth it" — and a programme *with* one still cannot, because dissolving
  deletes the row rather than closing it with an outcome. That is the candidate
  M23 in the roadmap and it is deliberately not built.
