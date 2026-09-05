# Whose responsibility is what

*A design pass on the question `docs/system-map.md` ends on: the milestone layer has no owner and no gate, and the Guild Master has no place to put a noticing. Written 2026-08-26. **Status: proposal. Nothing here is built.***

*Read `docs/system-map.md` first — it describes what exists. This file argues about what should.*

**Sourcing caveat.** PMI quotations below come from training material that reproduces *The Standard for Program Management*, 4th ed. module by module, not from the standard itself; verify anything load-bearing against ANSI/PMI 08-002-2017. SAFe quotations are pre-paywall text recovered from Internet Archive snapshots — `scaledagileframework.com` is now login-gated. MSP and PRINCE2 quotations are from the official AXELOS glossaries. Where the frameworks disagree this file says so rather than blending them.

---

## 1. The good news first

Daedalus did not invent its authority model, and it lands very close to where four independent traditions land. Worth establishing before criticising anything.

Across MSP, PRINCE2, PMI and SAFe, a programme-level role legitimately decides exactly five things on behalf of a project:

| Verb | Doctrine | Daedalus today |
|---|---|---|
| **Start** it | PMI component authorization; MSP project brief | `create_task` — **allowed directly** |
| **Redirect** it | PMI: *"change the direction of a component"* | `request_replan` — proposal |
| **Cancel** it | PMI: *"cancel a component"* | `request_cancel` — proposal |
| **Set the envelope** | PRINCE2 tolerance; SAFe MVP spend cap | budget narrowing at create — **allowed, downward only** |
| **Rule on a breach** | PRINCE2 exception; PMI steering committee | budget amend — **human only, not even proposable** |

Four and a half of five, already built, with the tiering roughly where the literature would put it. And — more importantly — **nothing in the Guild Master's surface reaches inside a project's sequencing or method**, which is precisely the line all four frameworks draw. PMI's formulation is the sharpest:

> "programs should be better equipped to deal with change because they have the ability to **change the direction of a component, cancel a component, or start a new component**."

That is the *entire* scope of programme authority over a project. Everything inside stays with the component. `cmd/guild-control-mcp/main.go:8-14` arrived at the same place from a security argument rather than a governance one — *"It exposes what to accomplish, never how"* — and the two arguments converge.

The one deviation is worth noting as a deliberate choice, not a defect: doctrine puts *start* at the same tier as redirect and cancel (PMI's component authorization is a steering-committee act). Daedalus grants it directly, on an explicit and sound rationale — `authority.go:115-118`, *"it cannot exceed policy […] the worst a poisoned doc achieves is a task nobody wanted."* Bounded creation is safe in a way bounded cancellation is not, because creation cannot destroy anyone's work.

---

## 2. Where the model is genuinely off-doctrine

### 2.1 The Programme has no benefit, so by the book it is a portfolio

Every framework defines a programme by an accountability no constituent project can hold.

> **MSP:** "A temporary flexible organization structure created to coordinate, direct and oversee the implementation of a set of related projects and activities in order to **deliver outcomes and benefits** related to an organization's strategic objectives."
>
> **PMI:** "related projects, subsidiary programs, and program activities managed in a coordinated manner to **obtain benefits not available from managing them individually**."

MSP makes the chain precise: **output** (what a project produces) → **capability** (the assembled set) → **outcome** (the change in real-world behaviour) → **benefit** ("the measurable improvement resulting from an outcome"). *The project stops at output. The programme owns everything to the right of it.*

Daedalus's `Programme` carries `Name`, `Description`, `Projects`, `Deps`, and a derived `ProgrammeStatus` that rolls up task states, external waits, and declared-vs-enforced edges. It has **no benefit, no measure, and no realisation check**. Nothing ever asks whether the thing it was formed *for* happened. `programme.go:298-300` says so plainly and treats it as a virtue: *"There is no programme state to store — a programme is not in a lifecycle, its Tasks are."*

By the doctrine that is aggregation, and aggregation over things "related in any way the owner chooses" is PMI's definition of a **portfolio**, not a programme.

This is not a naming quibble. It has a consequence you are already feeling: a programme that is only a tag can be *drifted from* but never *failed*, so nothing forces it to be revisited. `propose_programme_amendment` exists precisely because drift was anticipated — but drift from what? The description is prose, so the answer is "somebody's reading of a paragraph."

**Two honest ways out.** Either give a programme one falsifiable statement — a benefit with a measure and a check date, which makes `ProgrammeStatus` able to say *"formed to X; X has not moved"* — or rename it to portfolio and stop implying an accountability that isn't there. The first is more work and more useful; the second is free and honest. What is not defensible is keeping programme-shaped vocabulary over portfolio-shaped mechanics, because the vocabulary is what makes people expect the mechanics.

### 2.2 A milestone is not a governance object — and three of four frameworks are emphatic

This is the direct answer to "how should the Guild Master add milestones."

| Framework | What a milestone is | Where authority actually lives |
|---|---|---|
| **PRINCE2** | "A significant event in a plan's schedule" — decorative | the **management stage** |
| **MSP** | *no glossary entry at all* | the **tranche boundary** |
| **PMI** | "A significant point or event in a project, program, or portfolio" — and merged with decision points on the roadmap | the **phase gate** |
| **SAFe** | a roadmap marker; in 6.0 the standalone Milestones article was **absorbed into Roadmap** — demoted from first-class | the **PI boundary** |

And SAFe supplies the argument against ever gating on one:

> "In the past, many progress milestones were based on phase-gate activities. But experience has shown that **stage gate milestones generally do not reduce risk.**"

So the instinct to give milestones a tier and a table should be resisted. **A milestone is a marker. If you want an authority rhythm — a point where permission is renewed, the business case rechecked, and a go/no-go given — that is a tranche, and it is a different type from a milestone.** Making them the same type is the mistake PRINCE2, MSP and SAFe all avoid by construction.

Daedalus's milestones are markers with a status field. That is correct as far as it goes. The defect is not their shape; it is that nobody owns them.

### 2.3 A Task is a PRINCE2 work package missing one half of its contract

The correspondence is close enough to be useful:

| PRINCE2 work package | Daedalus Task |
|---|---|
| the work to be done | `Objective` — one sentence |
| product description: *"quality criteria… produced at planning time"* | the acceptance policy, frozen at `base_sha` |
| what will exist | `Deliverables` |
| constraints | `Budget`, `Checks`, `ImageDigest` |
| **"confirmation of the agreement between the project manager and the person or team manager who is to implement the work package that the work can be done within the constraints"** | **— nothing** |

The missing half is the receiving side's acceptance of the terms. A Daedalus Task is dispatched *at* a container; the container never gets to say "not within this envelope" before spending the envelope finding out. `MaxAttempts` exhaustion is how that conversation happens now — expensively, after the fact, and it is exactly what killed T-28 and T-29 on 2026-08-25 (`budgetamend.go:7-41`).

This is a real, small, buildable gap: **a Job that can decline its terms up front costs one attempt's worth of nothing instead of three attempts' worth of budget.** It is also the shape of PRINCE2's escalation — a forecast breach, raised before the breach, not a report afterwards.

---

## 3. The Guild Master's real risk is irrelevance

This is the finding that most changes how I'd design the fix, and it inverts the instinct the security model creates.

The literature has an unusually clear taxonomy of cross-cutting bodies by what they *own*:

- **Owns the decision** → Architecture Review Board. Measured as organisational drag: DORA asks *"What percentage of design and architecture changes require a formal approval process from a body outside your team?"* as a **negative** signal; ThoughtWorks reports ARBs *"correlating with low organizational performance."*
- **Owns the standard** → Centre of Excellence. Recurring charge: bottleneck, missing local context, *"experience sharing may not be welcomed if it is not aligned with the leaders' views."*
- **Owns the observation** → guild, community of practice, enabling team. **Its failure mode is being ignored.**

Daedalus put the Guild Master structurally in the third category — read-all, write-none, propose-only — for excellent trifecta reasons. But that means **the thing to defend against is not overreach. It is nobody reading it.** And on that, the evidence is brutal and specific.

Spotify's own peer-reviewed guild study (Šmite, Moe, Floryan, Levinta & Chatzipetrou, CACM 63(3), 2020 — two authors were serving Spotify engineering leaders):

> "Of all Spotify employees, 60% are said to be in some capacity associated with at least one guild… **We found that only 20% of the members regularly engage in the guild activities, while the majority merely subscribes to the latest news.**"
>
> "**Guild volunteers feel that time spent is not valued by the rest of the organization and we lose them to the tribe work that is valued.**"

And the failure of Spotify's advisory role proper, from Jeremiah Lee's *Failed #SquadGoals*:

> "'Agile coaches' were internal consultants… While well-intentioned, there were not enough coaches to help every team… **More so, they were not accountable for anything.**"

Gregor Hohpe names the mechanism:

> "A well-known architecture department anti-pattern is the 'ivory tower'… **Such a setup has one cardinal flaw: it doesn't provide feedback to the architects as to the effectiveness nor the cost of their decisions.**"

Now compare the current state, from `docs/system-map.md` §8: the Guild Master has **no trigger** (the stall→replan loop was never built), **no `list_proposals` tool** (so it cannot learn whether anything it asked for happened), and **five tools no role-doc version mentions**. It is an advisory body with no cadence, no feedback loop, and an incomplete map of its own capabilities. That is the documented recipe for irrelevance, three ingredients out of three.

The M21 incident is this failure, not a socket failure. A noticing occurred, and the only place to put it was a loose file in `/workspace/notes/`.

### What the literature says to do instead

The **advice process** (Andrew Harmel-Law, martinfowler.com) is the most directly transplantable:

> "**The Rule: anyone can make an architectural decision. The Qualifier: before making the decision, the decision-taker must consult** […] everyone who will be meaningfully affected […] [and] people with expertise in the area."
>
> "while decision-takers are in no way obliged to agree with the advice … **they must seek it out, and they must listen to and record it**."

Advice is **binding-to-seek and binding-to-record, never binding-to-obey**, and the obligation sits on the *decider*, not the advisor. The durable record is written by the *receiving* party — an ADR that engages with the advice *"whether they choose to follow it or not."*

PRINCE2 supplies the same pattern at the programme seam, and it is the strongest single idea for a system where one party must not write into another's repository: **the handover artefact dies on receipt.** The project brief *"is superseded by the PID and not maintained."* The receiving level rewrites the intent into its own owned document. Nothing upstream keeps a live copy to diverge from.

---

## 4. Recommendation

Four changes. The first is the one that makes the rest cheap and is worth doing alone.

### 4.1 Name an owner for the markdown layer, in writing

Currently: no sentence anywhere assigns it, `structured-docs.md` contradicts itself 176 lines apart, and 11 ungated lifecycle tools sit in every project container.

**Recommended assignment: the project's own agent and its human, jointly — the project owns its roadmap; nobody outside it may write one.** This is SAFe Principle #9 applied honestly, and the test is theirs:

> Centralize a decision only if it is **infrequent**, **long-lasting**, and **provides significant economies of scale**. "**Decentralize Everything Else.**" — frequent, time-critical, and requiring local information.

Roadmap maintenance is frequent, time-critical and local. It decentralises. The 11 ungated tools are therefore **defensible as they stand** — the defect was never that they exist, it is that no document says whose they are, so nobody knows the job is theirs. Which is why the roadmap has not been touched in five days and 23 commits.

Write the sentence. Reconcile `structured-docs.md`. That is most of the fix.

### 4.2 Do not make milestones plane state

Against it: three of four frameworks separate marker from gate; SAFe reports stage-gate milestones don't reduce risk; and your own `ROADMAP.md:230` argument applies with full force —

> "**an agent that can draft a plan would gain the power to gate work, which is the authority the proposal tier exists to withhold.**"

That was written about dependency graphs. It transfers exactly. A milestone in the plane, tiered, would be a plan the Guild Master could draft; the next question would be whether tasks must belong to one; and the answer to *that* is a gate.

If you later want an authority rhythm, build a **tranche** — a decision, a business-case recheck, a go/no-go — and keep it a different type from a milestone.

### 4.3 Give the Guild Master an Observation: the missing artefact

The Guild Master's product is *"decomposition, specs, and gates."* It has a first-class artefact for decomposition (`Task`) and for intent (`Programme`). **It has no artefact for a noticing** — which is the one thing its role doc says is its actual job: *"Your job is to **notice**, and then to **ask**."*

Proposed shape, deliberately minimal:

- **An Observation is plane state**, addressed to a project, written by the Guild Master, `TierAllowed` — because like `create_task` it cannot exceed policy. The worst a poisoned README achieves is a note nobody wanted.
- **It gates nothing and blocks nothing.** Same posture as a Review (`review.go:274-291`: *"THE REVIEWER REPORTS; IT DOES NOT ACT."*), for the same reason.
- **It is answered, not obeyed.** The receiving project records what it did — adopted, rejected, superseded — and that answer is the durable record, per the advice process. The Observation itself dies on receipt, per PRINCE2's brief.
- **It appears in the Ledger** as a third section beside Proposals, since that is where a human already reads what the plane holds and answers it.
- **It is falsifiable and scored.** Uptake is measurable; an advisory body whose advice is never adopted should be visibly failing rather than quietly ignored.

This is where "M21, flashcards, verbs" belongs. Not a milestone the Guild Master writes; an observation the flashcards project answers — by writing its own milestone, in its own repo, in its own words.

### 4.4 Close the three feedback holes

Cheap, and each is the difference between an advisory role that works and one that atrophies:

- **`list_proposals`** — `callerScope.ListProposals` already passes through untiered (`scope.go:271-273`); there is simply no tool. Without it the Guild Master is instructed to report "asked, not done" and can never learn the answer.
- **Refresh the role doc.** Five tools it has never mentioned, and a live contradiction: *"you never launch or drive another agent"* against `request_dispatch`. `core/guild.go:133-139` already says why this matters — *"a tool nobody is told about is a tool nobody reaches for."*
- **Give the noticing a cadence.** The stall→replan loop from `guild-master-control.md:135` was never built, so the Guild Master notices only when a human launches it. Any periodic trigger beats none.

---

## 5. What I would not do

**Don't give the Guild Master write access to another project's documents**, in any tier. The mounts are `:ro`, which makes it structural rather than promised — the property the whole design prefers. A proposal path would trade a structural guarantee for a procedural one, and §2.1's own reasoning applies: a human clicking confirm on an agent-authored roadmap edit is the launder, not the check.

**Don't merge the document hierarchy into the plane.** M22's move is better and already proven here: don't merge the graphs — **compare** them, and report the distance. `programme.go:324-341` —

> "The defect was never the two graphs. It was that **NOTHING EVER COMPARED THEM**… Reporting the distance turns the declared graph into something you can be WRONG about, which is the only way it earns its keep."

A `guild_overview` that reported *"this project's ROADMAP claims Milestone 7 in progress; the plane has landed nothing against it in 14 days"* would be the same move at the document seam, and needs no new authority whatsoever.

**Don't rename anything yet.** The literature offers two fair warnings — "Master" implies a directive power this role does not have, and "guild" imports Spotify baggage — but the evidence on guilds is *degradation at scale*, not failure, and the study's own conclusion cuts against a rename: *"having few attendants in the regular meetings is not necessarily a sign of failure. **What matters is the diversity of value-adding activities.**"* Fix the activities first; the name is the cheapest thing to change later and the most disruptive to change twice.

---

## 6. Open questions for the operator

1. **Programme or portfolio?** Give a programme a falsifiable benefit, or admit it aggregates and rename it. This one is genuinely yours — it decides whether `ProgrammeStatus` ever becomes able to say a programme *failed*.
2. **Does an Observation need a reply?** Advice-process doctrine says the recording obligation sits on the decider. Enforcing that on a project agent is a gate on the project, which contradicts §4.1's decentralisation. My inclination: unanswered Observations expire visibly, and expiry is the score. But an unanswered-advice policy is a governance decision, not a technical one.
3. **Should a Job be able to decline its terms?** §2.3. Small, buildable, and it is the fix T-28 and T-29 actually needed.
