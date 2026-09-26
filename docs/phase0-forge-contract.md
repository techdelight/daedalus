# Phase 0 — the forge contract, proved

**Status: COMPLETE.** Compatibility decision record for
[`ledger-forge-mvp-roadmap.md`](ledger-forge-mvp-roadmap.md) §5, executed
2026-09-26 against a disposable repository. The reproducible sequence is
`scripts/spike-forge-contract.sh`; this document records what was observed and
what was decided. Line-item observations carry the exact SHAs so they can be
checked against the test repository's history.

## Environment

| Fact | Observed |
|---|---|
| Forge | Forgejo **15.0.7+gitea-1.22.0**, `http://172.19.0.1:3000` (Docker gateway; `localhost` inside runners refers to the runner, not the forge) |
| Test repository | `dstibbe/daedalus-test`, private, `default_branch: master`, was empty at start |
| Runner | Present at instance/user level (repo-level runner list is empty); picks up `runs-on: docker`; image ships `git` and `curl`; trivial job completes in ~2 s |
| Git transport | SSH `172.19.0.1:5522` with the `daedalus-forge-deploy` key — registered as a **user key** on `dstibbe`, not a repo-scoped deploy key. HTTP with the API token as password also works |
| API token | Repo read shows `permissions.admin: true`, and PR create/close/comment/merge all work — but `branch_protections` reads and repo-settings `PATCH` return 403, and `/user` returns nulls. The token's scopes cover PR and content operations, **not** repo administration |

## The exit checkpoint, demonstrated

`publish M → checks for M → conditional target update to M → truthful PR
completion`, including refusal when the target changed:

| Step | Evidence |
|---|---|
| publish M | Prepared commit `8138fb4b` (candidate `2e99ccb2` replayed onto the moved target) pushed to daemon-controlled branch `daedalus/landing/T-100`; separately, prepared candidate `e9587e52` published via AGit topic `T-101` |
| checks for M | `GET /commits/{sha}/status` returns combined state plus per-context rows; the workflow's own in-run report (`spike/checked-out`) confirmed the runner checked out **exactly** the reported SHA in both cases |
| conditional update | Route 1: `git push --force-with-lease=refs/heads/master:<expected>` advanced master to `8138fb4b` exactly. Route 2: merge API `Do: fast-forward-only` + `head_commit_id` advanced master to `e9587e52` exactly — same object, no synthetic merge commit |
| truthful completion | Route 2 is **natively truthful**: PR 2 closed with `merged: true, merge_commit_sha: e9587e52`. Route 1 is not: no auto-detection fired, `manually-merged` is disallowed on this repo and the token cannot enable it → fallback demonstrated on PR 1: closed with a comment naming the landed SHA, labeled as such |
| refusal on moved target | Four distinct refusals observed, below |

### The four refusals

1. Stale lease: `! [rejected] ... (stale info)` — exact compare-and-swap
   against a named expected SHA.
2. Plain push of a sibling: `! [rejected] ... (fetch first)` — inherent
   fast-forward-only.
3. Merge API on a stale-based PR: **405**, `Not possible to fast-forward` —
   the base moved past the candidate.
4. Merge API with a wrong `head_commit_id`: **409 `head out of date`** — the
   head pin is enforced server-side.

**Refusal order matters and was measured:** with a diverged base the API
answers 405 *before* it evaluates the head pin; the 409 is reachable only
once the base is aligned. A client must treat both as "nothing landed" and
neither as retryable without re-observing the remote.

In the race drill, the second writer's commit (`90541129`) was never
disturbed.

## Decisions (roadmap §5 deliverable 2)

**Publication: AGit for the candidate PR, managed branch for the landing
ref.** AGit (`push HEAD:refs/for/<target> -o topic=<task>`) creates the PR by
push alone, creates **no branch**, triggers CI once (`pull_request` only,
where a managed branch double-fires `push`+`pull_request`), updates by
re-pushing the same topic, and — on this version — **loudly rejects** an
amend without `-o force-push=true` instead of silently opening a duplicate
(better than the documentation warns). The prepared integration commit still
goes to a real branch (`daedalus/landing/<task>`), because a `push`-event
run is wanted for it and branches are what plain git tooling can always
reach.

**Landing: the merge API, `Do: fast-forward-only` with `head_commit_id`
pinned to the prepared SHA.** It is the only observed mechanism that is
simultaneously (a) exact — the landed commit is byte-identical to the checked
one, (b) conditional — refuses on base movement and on head mismatch, and
(c) **natively truthful** — the PR ends `merged: true` with the exact SHA
recorded. Direct push-with-lease remains the fallback for landings that
carry no PR, with close-plus-recorded-SHA as its labeled completion.

**Sequence for one landing:** update the PR head to the prepared commit
(AGit force-push to the topic) → await required contexts on that SHA → merge
API ff-only with pinned head. Every step re-checks by SHA, so nothing
depends on the PR object staying still.

**CI checkout: fetch `$GITHUB_REF` explicitly.** An AGit commit exists only
under `refs/pull/N/head`; a plain clone cannot check it out — observed as a
real red run (`c1b82753`). The workflow fetches the event's own ref before
`checkout $GITHUB_SHA`. `actions/checkout` was deliberately not relied on
(instance ROOT_URL is `localhost`, unreachable from runners — the same class
of problem Egor's CI hit three times).

## Version-dependent assumptions (deliverable 4 — pin these in integration tests)

1. AGit amend without `force-push=true` is **rejected**, not duplicated.
2. `fast-forward-only` merge style exists and is allowed
   (`allow_fast_forward_only_merge: true`).
3. `head_commit_id` on the merge endpoint is enforced (409 on mismatch).
4. Actions runs surface in `GET /commits/{sha}/status` as contexts named
   `/ <job> (<event>)`.
5. Re-pushing a topic whose PR was **closed** opens a **new PR** — the
   topic→PR mapping is not stable across a close; the daemon must track PR
   ids, not topics.
6. `POST /statuses/{sha}` against an AGit-only SHA appears to fail silently
   (status absent) while Actions' own contexts attach fine — in-run
   provenance for AGit commits must ride a check, not a posted status.
7. Job logs require a web session; the API token cannot read them. Anything
   the daemon needs from inside a run must be reported outward (a status on a
   branch commit, or the run's own conclusion).
8. No manual-merge marking: `manually-merged` returns 405 unless enabled
   repo-side, which needs admin scope this token lacks.
9. Merge-refusal ordering: base divergence (405) is evaluated before the
   head pin (409).

The whole list is executable: `scripts/spike-forge-contract.sh` re-proves it
against a disposable repository (13 assertions, count asserted so a skipped
block cannot read as a clean run — observed green 2026-09-26).

## Credential findings (deliverable 3)

- **Two credentials are required**, as the roadmap predicted: an SSH key for
  git transport and an API token for PR/status/merge operations. HTTP+token
  can substitute for SSH, collapsing to one credential — at the cost of the
  token traveling in remote URLs; keep them split.
- For the pilot: replace the current **user-level** key with a repo-scoped
  deploy key (write), and mint the token with PR/status/contents scopes only.
  Neither protections nor repo settings need to be writable by the daemon —
  that separation held throughout the spike and is worth keeping.
- The token used for the spike was provided in conversation and should be
  treated as disposable: revoke and re-mint scoped before the pilot.

## Incidents during the spike, kept because they are findings

- **A wrong flag silently skipped the preparation step** (`cherry-pick -q`
  is not a flag): the "prepared" ref briefly pointed at the unprepared target
  tip and CI dutifully passed it. The daemon-side lesson is roadmap invariant
  6 sharpened: after pushing the landing ref, **verify the remote ref equals
  the recorded prepared SHA** before trusting any check on it.
- **The conflict case arrived unprompted**: the second candidate genuinely
  conflicted with the first landing (both edit `VALUE.txt`), and the
  preparation refused. The recovery that worked is the one §8 prescribes —
  a revised candidate through the normal flow, never a conflict resolution
  inside an approved landing.

## What Phase 0 did not test

Deadline/timeout behavior under a hung runner; webhook delivery (polling was
sufficient and is the roadmap's MVP choice); multi-repo behavior; protections
interacting with the daemon's push (no protections were set — the token
cannot manage them); performance beyond a trivial workflow.
