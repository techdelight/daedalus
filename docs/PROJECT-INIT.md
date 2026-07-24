# Project Init — Claude Code Bootstrap Guide

> **Version:** 1.2
> **Source:** distilled from the DareAgent governance + engineering patterns; document conventions aligned with Daedalus's structured-docs contract (`structured-docs.md`)
> **Audience:** Claude Code (primary) and human contributors (secondary)

A self-contained instruction set for bootstrapping a new project, or reconciling an existing one, against a coherent set of governance and engineering practices.

Projects bootstrapped from this file are meant to run under **Daedalus**, which reads their markdown as its source of truth and paints a per-project "journey" dashboard directly from it. The document set and formats below are therefore not arbitrary house style — three of the documents (`ROADMAP.md`, `SPRINTS.md`, `BACKLOG.md`) are **machine-parsed**, so keeping to their small conventions is what makes the dashboard, its validator, and the `daedalus docs lint` gate work. See [Required Project Documents](#required-project-documents).

---

## Contents

1. [How to use this file](#how-to-use-this-file)
2. [The Daedalus Environment — where this project lives](#the-daedalus-environment--where-this-project-lives)
3. [First Principles](#first-principles)
4. [Reading Order — joining the project](#reading-order--joining-the-project)
5. [Process Model](#process-model)
6. [Required Project Documents](#required-project-documents)
   - [Structured documents — the parseable contract](#structured-documents--the-parseable-contract)
7. [Meta Instruction — persisting rules to CLAUDE.md](#meta-instruction--persisting-rules-to-claudemd)
8. [Code Quality Principles](#code-quality-principles)
9. [Concrete Examples — good vs bad](#concrete-examples--good-vs-bad)
10. [Scope Discipline](#scope-discipline)
11. [Anti-Patterns — what NOT to do](#anti-patterns--what-not-to-do)
12. [CQS — Command-Query Separation](#cqs--command-query-separation)
13. [I/O Separation](#io-separation)
14. [Service Conventions](#service-conventions)
15. [Testing Strategy](#testing-strategy)
16. [Source File Headers](#source-file-headers)
17. [Workflow — TDD red-green-refactor](#workflow--tdd-red-green-refactor)
18. [Definition of Done](#definition-of-done)
19. [Pre-Commit Checklist](#pre-commit-checklist)
20. [Git & Commit Conventions](#git--commit-conventions)
21. [Release Procedure](#release-procedure)
22. [Tooling — Tasks, Plans, Memory](#tooling--tasks-plans-memory)
23. [Working Style — Communication](#working-style--communication)
24. [Carefulness — Risky Actions](#carefulness--risky-actions)
25. [Bootstrap Procedure](#bootstrap-procedure)
26. [Appendix — Document Templates](#appendix--document-templates)

---

## How to use this file

1. **Brand-new project** — Place this file in an empty repo, then tell Claude:
   > "Read PROJECT-INIT.md and bootstrap the project."

   Claude works through the **[Bootstrap Procedure](#bootstrap-procedure)** — asks a short questionnaire, then scaffolds the documents. Source code is scaffolded only after the documents are agreed on.

2. **Existing project lacking governance** — Place this file in the repo, then tell Claude:
   > "Read PROJECT-INIT.md and reconcile the project against it. Propose missing documents and a migration path before changing anything."

   Claude diffs what exists against this file, reports gaps, and proposes a sequence — without writing files until the user confirms.

3. **Reference template for sister projects** — Once `CLAUDE.md` exists in a project, the per-project rules live there. This file can be deleted, kept as a reference, or copied into other repos to keep them aligned.

---

## The Daedalus Environment — where this project lives

This project is (or will be) run under **Daedalus**, which owns the *space* around your code so a session can focus on the code itself. Know the split before you start.

**Daedalus provides this — you do not set it up:**

- **An isolated, hardened Docker container per project.** Your session runs *inside* it, with the project directory bind-mounted read-write at `/workspace` (and that mount is the only place you can write). The container **is** the trust boundary, so the agent runs without permission prompts — there is no Docker, sandbox, or permission setup for you to do.
- **Session & lifecycle management.** A host-side `daedalus-coordinator` daemon starts and stops containers and owns sessions; a `daedalus-runner` PID-1 process inside the container fans your terminal out to thin **CLI, TUI, and Web** clients. Attach, detach, and multi-viewer sharing are handled for you.
- **Project registration.** `daedalus <project>` registers the project (name, directory, target) in Daedalus's own registry. Nothing to scaffold for this. `CLAUDE.md` and `.claude/` are auto-mounted from the project root; a shared skill catalog is mounted at `/opt/skills`.

**You — this session — provide the project's own content:**

- **The required documents** (see [below](#required-project-documents)) and the **source code**. That is the job here.
- Those documents are **Daedalus's source of truth.** It paints a per-project dashboard — a **journey** of Purpose → Arc → Backlog — derived *live* from `VISION.md`, `ROADMAP.md`, `SPRINTS.md`, and `BACKLOG.md` on every request, and a doc-health badge tracks which of the canonical eight exist. Keeping the three parsed docs conformant ([the contract](#structured-documents--the-parseable-contract)) is what makes that dashboard real; `daedalus docs lint` verifies it.
- **Build, run, and test commands** belong in `CLAUDE.md` and `CONTRIBUTING.md`. The container supplies the toolchain — your job is to document how to drive it.

**Practical implication:** don't spend effort on containerization, ports, or standing up a dev sandbox — Daedalus already handed you one. Spend it on the documents and the code.

---

## First Principles

These three drive every other rule in this file:

1. **Small reversible steps over big bangs.** Every change is a thin slice that integrates to the default branch and ships through a release. If a change can't be sliced, that's a planning problem to fix before coding.
2. **Tests describe behavior, then code makes them true.** Tests come first because the cost of debugging untested code over its lifetime dwarfs the cost of writing the test.
3. **Documents are not bureaucracy — they are decision memory.** Each required document answers a question that the next contributor (Claude or human) will otherwise ask. Keep them current; delete what no longer applies.

---

## Reading Order — joining the project

When Claude (or a human) joins this repo, read in this order:

1. `CLAUDE.md` — operational instructions, build commands, conventions. Auto-loaded by Claude Code each session.
2. `VISION.md` — what we're building and why.
3. `ROADMAP.md` — the strategic **milestone arc**: where the project is going and what phase it's in now.
4. `SPRINTS.md` — the current sprint (goal, item table, the milestone it advances) + sprint history.
5. `BACKLOG.md` — the pool of unscheduled work.
6. `CHANGELOG.md` — `Unreleased` section + last 1–2 versions to catch recent shifts.
7. `ARCHITECTURE.md` — module map and dependencies (skim; deep-read on demand).
8. `CONTRIBUTING.md` — full development workflow.
9. `README.md` — only if reproducing a Quick Start; otherwise skip (it's user-facing).

`PROJECT-INIT.md` itself is for bootstrap and reconciliation — once `CLAUDE.md` is in place, it is no longer in the reading order.

---

## Process Model

A hybrid of:
- **PRINCE2** — governance, documented decisions, controlled stages.
- **Scrum** — sprints, prioritized backlog, demoable increments.
- **XP** — TDD, refactoring, small commits, pairing/mob review when useful.

Defaults:
- Small incremental changes; integrate to default branch often.
- Tests are first-class, not optional.
- Refactor opportunistically (Boy Scout Rule) within current scope.
- Pause and refactor before adding to code that has become hard to read.

---

## Required Project Documents

Maintain the following at the repo root. Update them when work materially affects them — never as a separate "documentation pass" weeks later.

**The canonical set** is the eight documents Daedalus expects every project to carry (its doc-health badge reports which are present). Together they answer: why the project exists, what it is and how to use it, how it's built, where it's going, and what has changed.

| Document | Purpose | Parsed? |
|----------|---------|---------|
| `README.md` | End-user facing. Why, Quick Start, Installation, Usage, Configuration, Documentation index, Troubleshooting. **No build instructions.** | served verbatim |
| `VISION.md` | Why we're building this. Problem, target users, success metrics, non-goals, constraints, principles. | served verbatim (the dashboard's **Purpose**) |
| `ARCHITECTURE.md` | Modules, components, protocols/ports, dependency graph, deployment topology, lessons learned. | no |
| `ROADMAP.md` | The strategic **milestone arc** — a short list of milestones from done → in-progress → planned, plus the end goal and current focus. **Not** sprints. | **yes** — `core.ParseMilestones` (the dashboard's **Arc**) |
| `BACKLOG.md` | The pool of unscheduled, un-prioritized work items. | **yes** — `core.ParseBacklog` (the dashboard's **Backlog**) |
| `SPRINTS.md` | The current sprint (goal, item table, the milestone it advances) + sprint history. | **yes** — `core.ParseSprints` (nested into the Arc) |
| `CHANGELOG.md` | Per-release history, always with an `Unreleased` section. | no (Keep a Changelog) |
| `CONTRIBUTING.md` | Developer guide: branching, TDD, code quality, conventions, Definition of Done, build commands, debug mode. | no |

**Supporting files** — expected, but outside the doc-health set:

| File | Purpose | Format |
|------|---------|--------|
| `CLAUDE.md` | Claude Code instructions. Auto-loaded each session. Build commands, conventions, anti-patterns, lessons learned. | Markdown |
| `VERSION` | Single line, semver (e.g., `0.1.0`). | Plain text |
| `logo.txt` | ASCII logo printed at service startup. | Plain text |

> **Why `ROADMAP` / `SPRINTS` / `BACKLOG` are three files, not one.** They change on different clocks and answer different questions. `ROADMAP.md` is the slow-moving strategic arc (milestones); `SPRINTS.md` is the fast-moving execution record (what's being built this cycle); `BACKLOG.md` is the unordered pool feeding future sprints. Daedalus renders them as one continuous **journey** — Purpose → Arc (with the current sprint nested inside the in-progress milestone) → Backlog — but each stays independently authored and independently parsed.

### Structured documents — the parseable contract

Daedalus derives its per-project dashboard from the four journey documents on **every request** — nothing is cached or generated to a sidecar file. The documents stay hand-authored markdown as the single source of truth: **no YAML frontmatter, no `.json` data file**. In return, three of them must keep to a small set of conventions. The parsers are **pure and total** — a missing file yields an empty result (never an error), and a heading that doesn't match is skipped rather than fatal — so a half-written document still renders; it just carries less. The authoritative spec is [`structured-docs.md`](structured-docs.md); the essentials:

**`ROADMAP.md` — milestones.** `###` headings of the form:

```
### Milestone 4: Layered Runner/Coordinator Architecture (In Progress)
```

- **Number** after `Milestone` (from 1); **title** after the colon; **status** the trailing parenthetical, pinned to exactly `(Done)` or `(In Progress)`. No parenthetical means **Planned**. Prose under the heading (until the next heading) is the description. Milestones are deliberately **tri-state with no per-milestone percentage** — progress is itemised at the sprint level, where the work actually lives.

**`SPRINTS.md` — sprints.** `###` headings; the current one under `## Current Sprint`, the rest under `## Sprint History`:

```
### Sprint 41: Trust-Prompt & Runner Terminal Fidelity (v0.40.0)

Goal: one-line statement of the sprint's aim.

Milestone: 4

| # | Item                     | Status      |
|---|--------------------------|-------------|
| 1 | Pre-seed workspace trust | Done        |
| 2 | Initial PTY sizing       | In Progress |
| 3 | Repaint-on-attach        |             |
```

- Optional trailing `(vX.Y.Z)` version (the `v` is required inside the parens, stripped on store). Optional `Goal:` line. Optional `Milestone: N` line linking the sprint to the milestone it advances — a **line of its own**, never a `[M4]` tag in the heading (a heading tag would be absorbed into the title and silently drop the version). Item table rows `| # | Item | Status |`; an empty status cell is **Pending**.

**`BACKLOG.md` — the pool.** Numbered rows in a table, `| # | Item |`. Everything after the number, to the trailing pipe, is the item.

**Status vocabulary** (shared, **case-sensitive**, across milestones and sprint items): `Done`, `In Progress`, empty cell = *Pending* (sprint item), no marker = *Planned* (milestone). The casing is exact — `In progress` (lowercase *p*) parses but then compares unequal everywhere and silently stops counting. Validation catches this; a parser will not.

**Validation & the gate.** Because each parser sees only one file, none can catch a contradiction *between* files (a sprint linked to a non-existent milestone, two current sprints, a heading typo'd so it's silently dropped from the arc). `daedalus docs lint [--ci] [dir]` runs the cross-file checks (`core.ValidateDocs`) plus the raw-text heading checks (`core.LintHeadings`) over `ROADMAP.md` and `SPRINTS.md`, exiting non-zero on any error (with `--ci`, on any warning too). Run it to keep a project's arc consistent; wire it into CI to keep it that way.

---

## Meta Instruction — persisting rules to CLAUDE.md

When the user prefixes a message with `Instruction:`, the message is a durable rule. Persist it by appending to `CLAUDE.md` so the rule survives across sessions.

> **User**: `Instruction: never commit lockfiles touched only by peer-dep flag changes.`
>
> Claude appends the rule under an appropriate section in `CLAUDE.md` and confirms.

Convention: Group instructions by topic in `CLAUDE.md` (e.g., "Coding Conventions", "Build & Test", "Repository Hygiene") rather than chronologically.

---

## Code Quality Principles

- **Names reveal intent.** Avoid `util`, `helper`, `manager`, `process`, `do`, `data`. Prefer concrete domain names.
- **Functions stay small.** Single purpose, single level of abstraction.
- **Pure logic at the core, IO at the edges.** Domain logic must run without a network, filesystem, or clock.
- **SRP.** High cohesion, low coupling. Dependencies explicit; no hidden globals.
- **Remove duplication with judgment.** Don't abstract prematurely; three similar lines beat the wrong abstraction.
- **Validate at boundaries.** Trust internals. Fail fast on bad input. Never swallow errors.
- **Refactor opportunistically** (Boy Scout Rule) within current scope.
- **Stop and refactor** before adding code that has become hard to read.
- **No backwards-compat scaffolding** for unreleased code: no `_legacy_` shims, no comments where deleted code lived, no version-gated branches that always run the new path.
- **Comments explain WHY, not WHAT.** Default to none; add only when a future reader would be surprised.
- **No secrets in code or commits.** `.env`, credentials, tokens stay out of git history.

---

## Concrete Examples — good vs bad

### Names

| Bad | Why it's bad | Better |
|-----|--------------|--------|
| `processData()` | "Process" hides what's happening | `extractInvoiceLineItems()` |
| `DataManager` | "Manager" is a god-object smell | `DocumentRepository` |
| `helperUtils.kt` | Dumping ground | Split by domain: `dateFormatting.kt`, `tagFiltering.kt` |
| `doStuff(x)` | Sounds like a placeholder that shipped | Verb the actual operation: `parseReceipt(text)` |
| `tmp`, `data`, `result` | Says nothing about meaning | `extractedAmounts`, `parsedReceipt`, `searchHits` |

### Comments

```
// BAD — narrates the code
// Loop through documents and increment counter
for (doc in documents) { count++ }

// BAD — tracks history; belongs in commit message
// Added 2026-03-19 to fix bug #142

// GOOD — explains a non-obvious WHY
// Paperless API returns paginated results capped at 100;
// we coalesce here so callers see a single list.
val all = paginate(client::fetch).flatten()
```

### Commit messages

```
BAD:  "fix bug"                            (says nothing)
BAD:  "WIP"                                (commit isn't ready)
BAD:  "Update PaperlessClient.kt"          (the diff already shows what)
GOOD: "Coalesce paginated Paperless results

       The /documents endpoint returns at most 100 hits per page,
       which broke the receipt-extraction flow on accounts with
       >100 receipts in a quarter. Paginate transparently in the
       client so callers see a single list."
```

---

## Scope Discipline

Critical for AI agents. The user asked for X — do X, not X plus a refactor plus a feature plus a cleanup.

- **One task at a time.** Bug fix doesn't need surrounding cleanup. One-shot operation doesn't need a helper. Three similar lines doesn't need an abstraction.
- **No feature flags or compat shims** when you can just change the code.
- **No half-finished implementations.** If the task can't fit in one slice, propose splitting before starting.
- **Don't add error handling for impossible cases.** Trust internal code and framework guarantees. Validate only at system boundaries.
- **Don't validate input that the type system already validates.**
- **Don't design for hypothetical future requirements.**
- **If you find a real problem outside the current scope**, surface it to the user and ask — don't silently fix it in the same change.

---

## Anti-Patterns — what NOT to do

These are concrete, observed mistakes. Treat them as hard rules.

- **Do not** "improve" code unrelated to the task.
- **Do not** add framework features (DI containers, mediator patterns, repositories around a single call) just because they're idiomatic.
- **Do not** add try/catch that re-throws with a wrapped exception unless the wrapping adds genuine information.
- **Do not** add `// TODO` comments for things you could fix now in scope, or that no one will ever pick up.
- **Do not** create "future-proof" abstractions with one implementation. Wait for the second concrete need.
- **Do not** rename a thing across the codebase as a side effect of fixing it. Rename, then fix, in two changes.
- **Do not** swallow errors. If you can't handle it, log at the right level and re-raise.
- **Do not** use `--no-verify` to bypass hooks. Fix the hook failure.
- **Do not** force-push to default branches.
- **Do not** commit secrets, generated files, or build artifacts.
- **Do not** use destructive commands (`rm -rf`, `git reset --hard`, `DROP TABLE`) without explicit confirmation.

---

## CQS — Command-Query Separation

- **Query** — returns data, has no side effects.
- **Command** — performs an action, returns nothing meaningful (`Void` / `Unit` / `void` / `()`).

A function that does both must be split. The split is almost always cleaner downstream.

---

## I/O Separation

All inputs and outputs (HTTP, file, DB, network, terminal, environment, clock) live in **separate packages or modules** from the core domain logic.

- The core domain runs in unit tests with no external system available.
- Adapters at the edge depend on the core, never the reverse.
- This is not "hexagonal architecture for its own sake" — it's the cheapest way to keep tests fast and reliable.

---

## Service Conventions

Every executable service must:

1. **Display a startup banner**: project logo (`logo.txt`), VERSION, build timestamp.
2. **Embed VERSION and build timestamp at build time** (not read from filesystem at runtime). The artifact is self-describing.
3. **Support a debug mode** via `DEBUG=true` env var that logs all incoming/outgoing protocol messages (HTTP, RPC, WebSocket frames).
4. **Expose `/health`** for liveness, returning at least `{ status, version, uptime }`.
5. **Log to stderr from STDIO subprocesses** when stdout is reserved for a protocol stream.

---

## Testing Strategy

Test pyramid, top to bottom: **unit (most) → integration (some) → end-to-end (few)**.

### Unit tests

- Required for all new code.
- Run in milliseconds. No network, no filesystem, no clock except a controllable one.
- Format: **Arrange / Act / Assert**, separated by blank lines or comments.
- One behavior per test. The test name describes the behavior, not the function.
- Mock external systems at the adapter boundary, not deep inside domain logic.
- Coverage target: **80%+ on new code**, but coverage is a smell-check, not a goal — write tests that describe behavior the code must satisfy.

### Integration tests

- Add when the feature crosses module boundaries or hits an external service.
- Use real protocols where possible (real HTTP server, real STDIO pipe, in-memory DB) — mock only the external system itself.
- Slower than unit tests; not on the inner dev loop.

### End-to-end tests

- Required for **critical user flows only.**
- Run on CI before merge to default branch.
- Brittle by nature — keep the count low and the assertions focused on user-observable outcomes.

### What to avoid

- **Tests that mirror the implementation** — they break on every refactor without catching real bugs.
- **Tests that assert on log output** unless logging is the feature.
- **Tests that depend on test order.**
- **Mocking what you don't own** at deep call sites — mock at the adapter boundary instead.

---

## Source File Headers

Every source file begins with a copyright comment in the file's native comment syntax:

```
// Copyright (C) <year> <organization>, All rights reserved.
```

Substitute `//` for `#`, `/* */`, `<!-- -->`, `;` etc. as appropriate.

---

## Workflow — TDD red-green-refactor

1. **Branch** from default (`master` / `main`) for each feature or fix.
2. **Red** — write a failing test that describes the missing behavior.
3. **Green** — write the minimum code to make the test pass.
4. **Refactor** — clean up while staying green.
5. **Repeat** for the next slice.
6. **When [Definition of Done](#definition-of-done) is met** — merge to default branch and push.

---

## Definition of Done

A feature is Done when **all** of the following are true:

- [ ] Acceptance criteria for the `SPRINTS.md` item met.
- [ ] Code quality up to standards (CQS, I/O separation, no language anti-patterns).
- [ ] Documentation up-to-date: `SPRINTS.md` item status reflects reality (and `ROADMAP.md` milestone status flips when a milestone completes), `CHANGELOG.md` `Unreleased`, `README.md`, `VERSION` if releasing, `ARCHITECTURE.md` if topology changed. Parsed docs still pass `daedalus docs lint`.
- [ ] Build green (project's primary build commands).
- [ ] All tests pass (unit + integration; e2e where applicable).
- [ ] Software still runs (manual smoke or e2e).
- [ ] `VERSION` bumped semantically if this is a release.
- [ ] All changes committed.

---

## Pre-Commit Checklist

1. Code compiles (project's primary build).
2. Tests pass.
3. Lint clean.
4. No language anti-patterns introduced (recorded in `CONTRIBUTING.md`).
5. No secrets committed.
6. `CHANGELOG.md` `Unreleased` section reflects user-facing changes.

---

## Git & Commit Conventions

- **Subject**: imperative mood, ≤ 70 chars, summarizes the change.
- **Body** (when needed): bulleted list, **why first**, then **what**.
- **One logical change per commit** where reasonable.
- **No `--no-verify`** unless the user explicitly authorizes for this commit.
- **No force-push to default branch.**
- **No secrets in commits.** Audit `git diff --cached` before committing if unsure.
- **Co-authored-by** lines are valid for pair work and AI assistance, when relevant.

---

## Release Procedure

A release is a deliberate act, not a side effect of a merge.

1. **Confirm Definition of Done** for everything in the `Unreleased` section.
2. **Bump `VERSION`** semantically:
   - **Major** (`X.0.0`): breaking change to a public interface or contract.
   - **Minor** (`x.Y.0`): backwards-compatible feature.
   - **Patch** (`x.y.Z`): backwards-compatible fix.
3. **Move `CHANGELOG.md` `Unreleased` content** under a new `## [X.Y.Z] - YYYY-MM-DD` section.
4. **Recreate an empty `Unreleased` section** at the top.
5. **Commit**: `Release X.Y.Z` with the changelog body.
6. **Tag**: `git tag vX.Y.Z` and push the tag.
7. **Build artifacts** with the new VERSION and build timestamp embedded.
8. **Update the arc** — move the shipped sprint from `## Current Sprint` to `## Sprint History` in `SPRINTS.md`, and flip the milestone's `(In Progress)` → `(Done)` in `ROADMAP.md` if the release completes it. Confirm `daedalus docs lint` stays clean.

---

## Tooling — Tasks, Plans, Memory

Claude Code provides several persistence mechanisms — use the right one for the right scope:

| Mechanism | Scope | When to use |
|-----------|-------|-------------|
| **TaskCreate / TaskUpdate** | Within the current conversation | Multi-step work where progress is worth tracking. Mark each task `completed` as soon as it's done; don't batch. |
| **Plan** | Within the current conversation, before implementation | Aligning on approach before non-trivial work. Use when the path is ambiguous. |
| **`CLAUDE.md`** | Across sessions in this project | Durable rules, conventions, lessons learned. Updated via `Instruction:` prefix. |
| **`ARCHITECTURE.md` "Lessons Learned"** | Across sessions, project-wide context | Surprising facts about the system, gotchas, framework quirks discovered the hard way. |
| **Git history + `CHANGELOG.md`** | Permanent record | What changed and why. Don't duplicate this in memory. |

What **NOT** to track in memory:
- Code patterns, conventions, file paths — readable from the code.
- Commit history or who changed what — `git log` is authoritative.
- Bug fixes — the fix is in the code, the reason is in the commit message.
- Ephemeral conversation state.

---

## Working Style — Communication

- **State briefly what's about to happen** before the first tool call.
- **Surface updates** when finding something significant, changing direction, or hitting a blocker.
- **Match response weight to task**: a simple question gets a direct answer; a structural change gets a structured update.
- **End with one or two sentences**: what changed, what's next.
- **Don't narrate internal deliberation.** State results and decisions; spare the reader the running monologue.
- **For exploratory questions** ("what could we do about X?", "how should we approach Y?"), respond with a recommendation and the main tradeoff in 2–3 sentences. Don't implement until the user agrees.

---

## Carefulness — Risky Actions

For irreversible or shared-state actions, **confirm with the user first**:

- Destructive: deleting files/branches, dropping tables, killing processes, `rm -rf`, overwriting uncommitted work.
- Hard-to-reverse: force-pushing, `git reset --hard`, amending published commits, removing or downgrading dependencies, modifying CI/CD pipelines.
- Visible to others: pushing to remotes, opening/closing PRs, sending messages, posting to external services.
- Uploading content to third-party services (diagram renderers, pastebins, gists) — assume cached and indexed.

When you encounter an obstacle, **diagnose root causes; don't bypass safety checks** to make the obstacle go away. If you find unexpected state (unfamiliar files, branches, configs), **investigate before deleting** — it may be the user's in-progress work.

A user authorizing an action once does **not** authorize it in all contexts. Match the scope of your actions to what was explicitly requested.

---

## Bootstrap Procedure

When asked to bootstrap a new project from this file, Claude follows this checklist:

### Step 1 — Questionnaire

Ask the user the following before creating any files:

1. Project name and one-sentence elevator pitch.
2. Problem statement and primary target user(s).
3. Primary tech stack (language, build tool, runtime, framework).
4. Organization name and copyright year for source headers.
5. Default branch name (`master` or `main` — confirm).
6. Hosting (GitHub, GitLab, etc.) and remote URL, if known.

If the user is brief or unsure, mark fields as `TBD` and proceed; do not stall.

### Step 2 — Scaffold documents

Create these in this order. Use the templates in the [Appendix](#appendix--document-templates).

1. `VERSION` — `0.1.0`.
2. `logo.txt` — placeholder; user replaces.
3. `CLAUDE.md` — populate from questionnaire.
4. `VISION.md` — populate Problem Statement and Target Users; mark unanswered sections `TBD`.
5. `ROADMAP.md` — End Goal + a first `### Milestone 1: … (In Progress)` (the strategic arc; **not** sprints).
6. `SPRINTS.md` — empty `## Current Sprint` and `## Sprint History` sections.
7. `BACKLOG.md` — empty numbered table (`| # | Item |`).
8. `CHANGELOG.md` — empty `Unreleased` section.
9. `README.md` — skeleton with elevator pitch.
10. `CONTRIBUTING.md` — populated from templates.
11. `ARCHITECTURE.md` — empty Overview.
12. `.gitignore` — appropriate to chosen stack.

After scaffolding the parsed trio, run `daedalus docs lint` (if the project is managed by Daedalus) to confirm the arc is consistent before moving on.

### Step 3 — Confirm

Show the resulting tree to the user. **Do not scaffold source code in the same step** — bootstrapping documents is its own deliverable. Wait for explicit instruction before creating code.

### Step 4 — Optional follow-ups

Offer (do not assume):
- Initialize git repo (`git init`) if not present.
- Add a license file (ask which license).
- Set up CI configuration.
- Scaffold a "hello world" service per the chosen stack to validate the [Service Conventions](#service-conventions).

---

## Appendix — Document Templates

These templates are **self-contained** — they do not assume `PROJECT-INIT.md` will remain in the repo. Replace the italicized prompts with the project's actual content during bootstrap.

### `CLAUDE.md`

```markdown
# CLAUDE.md

Guidance for Claude Code when working in this repository.

## Meta

When the user gives an instruction prefixed with `Instruction:`, persist it by updating this file.

## Process Model

PRINCE2 (governance) + Scrum (sprints) + XP (TDD, refactoring).
Default expectations: small incremental changes, frequent integration, automated tests.

## Project Documents

- `README.md` — end-user facing
- `VISION.md` — why we're building this
- `ARCHITECTURE.md` — components, ports, dependencies
- `ROADMAP.md` — strategic milestone arc (parsed: `### Milestone N: … (Status)`)
- `SPRINTS.md` — current sprint + history (parsed: `### Sprint N: … (vX.Y.Z)`, `Goal:`, `Milestone: N`, item table)
- `BACKLOG.md` — unscheduled work pool (parsed: `| # | Item |` table)
- `CHANGELOG.md` — Keep a Changelog format
- `CONTRIBUTING.md` — developer guide
- `VERSION` — semver

`ROADMAP.md` / `SPRINTS.md` / `BACKLOG.md` are machine-parsed — keep to the format above (see the project's structured-docs contract). `daedalus docs lint` checks them.

## Project Overview

*One- or two-sentence description of what this project is and who it serves.*

## Build & Test Commands

*Primary build, test, lint, and run commands. Group by language/module if there are several.*

## Architecture

*Brief module map and the dependency direction between them. Link to `ARCHITECTURE.md` for the full picture.*

## Coding Conventions

Full details in `CONTRIBUTING.md`. Key rules summarized here.

### Copyright Header

Every source file starts with:

    // Copyright (C) <year> <organization>, All rights reserved.

(Use the comment syntax appropriate to the file type.)

### Code Quality

- Intention-revealing names; avoid util/helper/manager/process/do.
- Small functions, single purpose, one abstraction level.
- Pure logic at the core, IO at the edges.
- SRP, high cohesion, low coupling, explicit dependencies.
- Validate at boundaries; fail fast; no swallowed errors.
- Refactor opportunistically without scope creep.
- Comments explain WHY, not WHAT.
- No backwards-compat scaffolding for unreleased code.

### CQS

A function is a Query (returns data, no side effects) or a Command (acts, returns nothing). Never both.

### I/O Separation

I/O lives in separate packages/modules from core domain logic.

### Service Startup

Every executable service displays VERSION, build timestamp, and logo at startup; supports `DEBUG=true`; exposes `/health`.

### Scope Discipline

- Bug fix doesn't need surrounding cleanup.
- One-shot operation doesn't need a helper.
- Don't add error handling for impossible cases.
- Don't design for hypothetical future requirements.

### Language-Specific Conventions

*Stack-specific rules: idiomatic patterns, mandatory standard-library features, formatting choices, framework preferences.*

### Anti-Patterns

*Stack-specific things never to do (e.g., for Kotlin: no `!!`, no `private lateinit var`; for Angular: no `*ngIf`, no `@Input()` decorators).*

## Pre-Commit Quality Checklist

1. Code compiles
2. Tests pass
3. Lint clean
4. No anti-patterns introduced
5. No secrets committed
6. CHANGELOG `Unreleased` reflects user-facing changes

## Definition of Done

- Acceptance criteria for the SPRINTS.md item met
- Code quality up to standards
- Documentation up-to-date: SPRINTS.md item status current (ROADMAP.md milestone flips when a milestone completes), plus README, CHANGELOG, VERSION, ARCHITECTURE as affected
- Build green; all tests pass; software runs
- VERSION bumped if releasing
- Changes committed

## Risky Actions

For destructive, hard-to-reverse, or shared-state actions, confirm with the user before proceeding.

## Lessons Learned

(append surprising facts, framework quirks, and gotchas here as they're discovered)
```

### `VISION.md`

```markdown
# Vision

## Problem Statement

*The user pain or unmet need this project addresses, in a paragraph. State it from the user's perspective, not the technology's.*

## Target Users

- *Primary user group — who they are, what role they play.*
- *Secondary user group, if any.*

## Success Metrics

- *Observable, measurable outcome that means the project is succeeding.*
- TBD

## Non-Goals

- *Something this project will deliberately not do, with reasoning.*
- TBD

## Constraints

- *External or self-imposed limit (technology, budget, time, regulation).*
- TBD

## Guiding Principles

1. *Decision rule that resolves trade-offs in favor of one direction.*
2. TBD
```

### `ROADMAP.md` — the milestone arc (parsed)

Milestones are `### Milestone N: Title (Status)` headings. Status is pinned to `(Done)` or `(In Progress)`; **no parenthetical means Planned**. Keep the list short — this is strategy, not the sprint board.

```markdown
# Roadmap

## End Goal

*One paragraph: the state of the world when this project has fully succeeded.*

## Milestones

### Milestone 1: *First Capability* (In Progress)

*Prose (or a bullet list) describing the milestone, until the next heading.*

### Milestone 2: *Next Capability*

*Planned — no status parenthetical. Describe the outcome, not the tasks.*

## Phasing

    M1 (In Progress) ──► M2 ──► M3
    First               Next    Later

## Current Focus

*Which milestone is mid-flight, and the one-line "where we are" within it.*
```

### `SPRINTS.md` — sprints (parsed)

The current sprint lives under `## Current Sprint`; everything shipped moves under `## Sprint History`. Each sprint is a `### Sprint N: Title (vX.Y.Z)` heading with an optional `Goal:` line, an optional `Milestone: N` link (**its own line**, never a heading tag), and an item table. An **empty status cell means Pending**.

```markdown
# Sprints

## Current Sprint

### Sprint 1: *Sprint Name* (v0.1.0)

Goal: *one-sentence outcome the sprint is committing to.*

Milestone: 1

| # | Item                       | Status |
|---|----------------------------|--------|
| 1 | *Concrete deliverable*     |        |
| 2 | *Another deliverable*      |        |

## Sprint History

(shipped sprints move here, newest first, with their final item statuses)
```

### `BACKLOG.md` — the pool (parsed)

Numbered rows in a table. The number is a stable id; everything after it, to the trailing pipe, is the item.

```markdown
# Backlog

| # | Item |
|---|------|
| 1 | *Idea or fix awaiting prioritization — one line, self-contained.* |
```

### `CHANGELOG.md`

```markdown
# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

### Changed

### Fixed

### Removed

### Security
```

### `CONTRIBUTING.md`

```markdown
# Contributing Guide

Development workflow, code quality, Definition of Done.

## Process Model

PRINCE2 + Scrum + XP hybrid. Small incremental changes, frequent integration, automated tests.

## Branching

1. Branch from default (`master` / `main`) for each feature or fix.
2. Develop on the feature branch following TDD.
3. Merge to default branch when Definition of Done is met.

## Test-Driven Development

Red → Green → Refactor.

### Test Format

Arrange / Act / Assert, separated by blank lines or comments. One behavior per test. Test name describes the behavior, not the function.

### Test Pyramid

- **Unit** — most. No network, no filesystem, no real clock. Run in ms.
- **Integration** — some. Cross-module or external service. Mock the external system itself, not the protocol.
- **End-to-end** — few. Critical user flows only.

### Coverage

80%+ on new code. Coverage is a smell-check, not a goal.

## Code Quality

- Intention-revealing names; avoid util/helper/manager/process/do.
- Small functions, single purpose, one abstraction level.
- Pure logic at the core, IO at the edges.
- SRP, high cohesion, low coupling, explicit dependencies.
- Remove duplication with judgment; avoid premature abstraction.
- Validate at boundaries; fail fast; no swallowed errors.
- Refactor opportunistically without scope creep.
- Comments explain WHY, not WHAT.

### CQS — Command-Query Separation

A function is a Query (returns data, no side effects) or a Command (acts, returns nothing). Never both.

### I/O Separation

I/O lives in separate packages/modules from core domain logic.

### Copyright Header

Every source file starts with:

    // Copyright (C) <year> <organization>, All rights reserved.

### Service Startup

Every executable service displays VERSION, build timestamp, and logo at startup; supports `DEBUG=true`; exposes `/health`.

### Language-Specific Conventions

*Stack-specific rules: idiomatic patterns, mandatory standard-library features, formatting choices, framework preferences.*

### Anti-Patterns

*Stack-specific things never to do (e.g., for Kotlin: no `!!`, no `private lateinit var`; for Angular: no `*ngIf`, no `@Input()` decorators).*

## Definition of Done

- [ ] Acceptance criteria for the SPRINTS.md item met.
- [ ] Code quality up to standards.
- [ ] Documentation up-to-date: SPRINTS.md item status reflects reality (ROADMAP.md milestone status flips when a milestone completes), plus README, CHANGELOG, VERSION, ARCHITECTURE as affected.
- [ ] Build green.
- [ ] All tests pass.
- [ ] Software still runs.
- [ ] VERSION bumped semantically if releasing.
- [ ] All changes committed.

## Build & Test Commands

*Primary build, test, lint, and run commands. Group by language/module if there are several.*

## Debug Mode

Set `DEBUG=true` to log all incoming/outgoing protocol messages.

## Release Procedure

1. Confirm Definition of Done for everything in `Unreleased`.
2. Bump VERSION semantically (major / minor / patch).
3. Move `Unreleased` content into a new `## [X.Y.Z] - YYYY-MM-DD` section in CHANGELOG.
4. Recreate empty `Unreleased`.
5. Commit `Release X.Y.Z` with the changelog body.
6. Tag `vX.Y.Z` and push.
7. Build artifacts with new VERSION and build timestamp embedded.
8. Move the shipped sprint from `## Current Sprint` to `## Sprint History` in SPRINTS.md; flip the milestone to `(Done)` in ROADMAP.md if the release completes it.
```

### `README.md`

```markdown
# *Project Name*

## Why

*One- or two-sentence pitch describing what this project does and why it exists.*

## Quick Start

*The shortest path from a fresh clone to seeing the project run. Aim for under five commands.*

## Installation

*Prerequisites, supported platforms, and the canonical install steps.*

## Usage

*How an end user invokes the main flows, with copy-pasteable examples.*

## Configuration

*Environment variables, config files, and their effects. Table form works well.*

## Project Documentation

| Document | Purpose |
|----------|---------|
| [VISION.md](VISION.md) | Goals, principles, non-goals |
| [ROADMAP.md](ROADMAP.md) | Strategic milestone arc |
| [SPRINTS.md](SPRINTS.md) | Current sprint + history |
| [BACKLOG.md](BACKLOG.md) | Unscheduled work pool |
| [CHANGELOG.md](CHANGELOG.md) | Version history |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Developer guide |
| [ARCHITECTURE.md](ARCHITECTURE.md) | Modules, ports, dependencies |
| [CLAUDE.md](CLAUDE.md) | Build commands, conventions |

## Troubleshooting

*Common failure modes and their fixes. Add entries as users hit and report issues.*
```

### `ARCHITECTURE.md`

```markdown
# Architecture

## Overview

*ASCII or Mermaid diagram showing the major components and how they communicate. Keep it readable in a terminal.*

## Modules

| Module | Description | Port | Internal Dependencies |
|--------|-------------|------|------------------------|

## Protocols & Ports

| Protocol | Port | Service |
|----------|------|---------|

## External Dependencies

| Library | Repository | Version | Purpose |
|---------|------------|---------|---------|

## Deployment Topology

*Where each component runs (process, container, network), and how they're wired in dev vs prod.*

## Lessons Learned

(append surprising facts, framework quirks, and gotchas here as they're discovered)
```

### `.gitignore` (stack-agnostic seed)

```gitignore
# Build artifacts
/target/
/build/
/dist/
/out/

# Dependencies
/node_modules/
__pycache__/
.venv/

# IDE / editor
.idea/
.vscode/
*.iml
*.swp
.DS_Store

# Environment / secrets
.env
.env.local
*.local

# Logs
*.log
logs/

# Test output
coverage/
test-results/
playwright-report/

# OS
Thumbs.db
```
