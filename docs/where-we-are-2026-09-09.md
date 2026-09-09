# Where we are — 2026-09-09

A snapshot of `development`, written to answer "where were we" without having to
reconstruct it from the log again. Point-in-time: everything below was true on
2026-09-09 and stops being trustworthy the moment somebody commits.

---

## Unpushed: 3 commits ahead of `origin/development`

| Commit | What it is |
|---|---|
| `6be5ae5` | Merge of a daedalus job target — **the Ledger redesign**: `docs/ledger-ui-design.md`, `internal/web/static/ledger-next.css`, and two mockups (`ledger-project-preview.html`, `ledger-programme-preview.html`) |
| `7fae6d4` | "added docs" — the five docs that had been sitting untracked: `backlog-97-artifact-diff.md`, `reviewing-and-landing.md`, the programme-runtime migration plan + its review, `system-map.md`, `responsibilities.md` |
| `ae042b2` | A job snapshot |

The #79b adopt fix/tests (`659dedf`, `ecd8ad0`) and the #99 backlog entry
(`d9f5ff6`) are **pushed**.

## Uncommitted: narrow-width work on the redesign mockups

In `internal/web/static/`, on top of the merge:

- `.chart-compact-state` — a state word that **replaces** the progression track
  and the AP gauge below 900px, so a compact row keeps the one fact it must
  never lose.
- The chart and the map now wear control.css's existing **`is-entry`
  one-screen-at-a-time phone contract** below 780px, with a `◀` back button that
  returns focus to the current row. That contract was written for
  `.ledger-list`; these two new primary surfaces now say so explicitly.
- `:focus-visible` rings on map nodes and stubs.

None of this is described in `docs/ledger-ui-design.md` yet.

## The blocker the design doc names itself

`docs/ledger-ui-design.md:6` says **DESIGNED, NOT BUILT**, and line 17 says the
two mockups **have not been looked at** — authored headless, checked only
structurally (sheets balance, markup nests, every class resolves, scripts'
brackets close).

That was written when a browser was believed unavailable. **It is available
here** — the no-root Playwright + Chromium recipe works, fonts included. So the
redesign is unverified in exactly the way #95 was when a screenshot caught a bug
no assertion could see, and the uncommitted phone work is unverified twice over.

**Next move:** open both previews in Chromium at desktop and phone widths,
screenshot them, and *read* the result. Then commit the narrow-width work with
whatever that turns up, and correct the doc's status line.

## Also open

- **Host-only TODOs** (need the user's machine): `daedalus task target daedalus
  --sync`, then `task reverify T-28 --amended`; and `daedalus control restart`.
- **#96** — px → rem for prose tiers.
- **#99** — `daemon.go` has 19 of 40 HTTP handlers at 0% coverage, including
  `handleOperations` (#95) and `handleTargetLags`.
- **The migration review's finding 1** — rewrite §12.2 of the programme-runtime
  migration plan so a review finding is a **veto**, not a permission.
- **Nobody owns milestones.**
- Open questions inside `reviewing-and-landing.md` (#97/#98): whether workflow
  files escalate to a human or degrade privilege Zuul-style.

---

## Since this snapshot was written: extra host mounts (`/mnt/<name>`)

Landed in the working tree the same day, unrelated to the Ledger work.

A project can now name extra host directories in its `projects.json` entry and
get them inside its container at `/mnt/<name>`:

```json
"my-app": {
  "directory": "/home/me/src/my-app",
  "target": "dev",
  "mounts": [
    { "name": "datasets", "host": "/srv/datasets", "readOnly": true },
    { "name": "out",      "host": "/home/me/out" }
  ]
}
```

- `core/mounts.go` — the shape, the `/mnt` root, and the four refusals
  (name that is not a single path segment; repeated name; host path relative or
  containing a colon; host path missing or not a directory). Refusals are values,
  not silence: an empty `/mnt/<name>` reads to the agent as "missing", not
  "refused".
- `internal/coordinator/coordinator.go` — reads the list from the **registry**,
  not from the start request, so whoever can write `projects.json` decides which
  host paths a container can see, and whoever can reach the coordinator socket
  does not.
- `cmd/daedalus/launch.go` — prints refusals on the operator's own terminal;
  both sides derive from one function.
- Registry schema **v4** (additive; the v2 and v3 bumps set the precedent).
- `readOnly` defaults to **false**, unlike `/guild/<name>`, which is always `:ro`.

Tests: `core/mounts_test.go`, `internal/coordinator/mounts_test.go`, plus a
registry round-trip that proves a launch's `TouchProject` rewrite does not eat
the configured mounts. All four assertions were **mutation-tested** — drop the
`:ro`, skip the name validation, drop the coordinator's append, rename the JSON
tag — and each mutation failed a test.
