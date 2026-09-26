#!/usr/bin/env bash
# Copyright (C) 2026 Techdelight BV
#
# Phase 0 forge-contract spike (docs/ledger-forge-mvp-roadmap.md §5).
# Reproduces the observed run recorded in docs/phase0-forge-contract.md.
#
# DESTRUCTIVE toward its target repository: seeds master, opens and closes
# PRs, advances the target branch. Point it ONLY at a disposable repo.
#
# Required environment:
#   FORGE_URL    e.g. http://172.19.0.1:3000
#   FORGE_SSH    e.g. ssh://git@172.19.0.1:5522
#   FORGE_REPO   e.g. dstibbe/daedalus-test
#   FORGE_TOKEN_FILE  path to a file holding the API token (never inline)
#   GIT_SSH_COMMAND   ssh command selecting the key, e.g.
#                     "ssh -i ~/.ssh/daedalus-forge-deploy -o IdentitiesOnly=yes -p 5522"
set -euo pipefail

: "${FORGE_URL:?}" ; : "${FORGE_SSH:?}" ; : "${FORGE_REPO:?}" ; : "${FORGE_TOKEN_FILE:?}"
TOKEN=$(cat "$FORGE_TOKEN_FILE")
API="$FORGE_URL/api/v1/repos/$FORGE_REPO"
auth=(-H "Authorization: token $TOKEN")
WORK=$(mktemp -d /tmp/forge-spike.XXXXXX)

PASS=0; FAIL=0
pass() { echo "  ✓ $1"; PASS=$((PASS+1)); }
fail() { echo "  ✗ $1"; FAIL=$((FAIL+1)); }
finish() {
    echo; echo "── summary ── $PASS passed, $FAIL failed (expected: see EXPECTED_CHECKS)"
    [[ "$PASS" -eq "$EXPECTED_CHECKS" && "$FAIL" -eq 0 ]] || exit 1
}
# The summary-line rule, mechanized: a silently skipped block must not read
# as a clean run, so the expected count is asserted, not implied.
EXPECTED_CHECKS=13
trap finish EXIT

api() { curl -sS -m10 "${auth[@]}" "$@"; }
wait_status() { # wait_status <sha> ; echoes combined state
    for _ in $(seq 1 30); do
        s=$(api "$API/commits/$1/status" | jq -r .state)
        [[ "$s" == "success" || "$s" == "failure" || "$s" == "error" ]] && { echo "$s"; return; }
        sleep 2
    done
    echo "timeout"
}

echo "[0] preflight"
ver=$(curl -sS -m5 "$FORGE_URL/api/v1/version" "${auth[@]}" | jq -r .version)
echo "  forge version: $ver"
empty=$(api "$API" | jq -r .empty)
if [[ "$empty" != "true" && "${FORCE:-}" != "1" ]]; then
    echo "refusing: $FORGE_REPO is not empty and FORCE=1 not set (this script is destructive)"
    EXPECTED_CHECKS=0; exit 1
fi

echo "[1] seed master: README, check.sh, provenance workflow"
git init -q -b master "$WORK/repo"; cd "$WORK/repo"
git config user.name daedalus-spike; git config user.email spike@invalid
cat > check.sh <<'EOF'
#!/bin/sh
set -e
test -f VALUE.txt && grep -q '^value=' VALUE.txt && echo "check.sh OK"
EOF
chmod +x check.sh; echo "value=0" > VALUE.txt
mkdir -p .forgejo/workflows
cat > .forgejo/workflows/ci.yml <<EOF
on: [push, pull_request]
jobs:
  verify:
    runs-on: docker
    steps:
      - name: clone-check
        run: |
          git clone -q --no-checkout http://token:\${{ secrets.GITHUB_TOKEN }}@\${FORGE_HOSTPATH} src
          cd src
          git fetch -q origin "\$GITHUB_REF" || true   # AGit commits live only under refs/pull/N/head
          git checkout -q "\$GITHUB_SHA"
          sh ./check.sh
EOF
# the workflow needs the host:port/path form for the token-in-URL fallback
sed -i "s|\${FORGE_HOSTPATH}|${FORGE_URL#http://}/$FORGE_REPO.git|" .forgejo/workflows/ci.yml
git add -A; git commit -qm "Seed"
git remote add origin "$FORGE_SSH/$FORGE_REPO.git"
if [[ "${FORCE:-}" == "1" ]]; then
    for b in $(api "$API/branches" | jq -r '.[].name | select(. != "master")'); do
        api -o /dev/null -X DELETE "$API/branches/$b"
    done
    git push -qf origin master
else
    git push -q origin master
fi
[[ $(wait_status "$(git rev-parse HEAD)") == success ]] \
    && pass "runner exists; seed push checked green" || fail "seed push not green"
N0=$(api "$API/branches" | jq length)

echo "[2] AGit candidate: PR by push alone, no branch"
git checkout -qb cand; echo "value=1" > VALUE.txt; git commit -qam "T-A: candidate"
A=$(git rev-parse HEAD)
git push -q origin HEAD:refs/for/master -o topic=T-A -o title="T-A candidate" 2>/dev/null
PR=$(api "$API/pulls?state=open" | jq -r '.[0].number')
[[ -n "$PR" && "$PR" != null ]] && pass "AGit push created PR #$PR" || fail "no PR from AGit push"
[[ $(api "$API/branches" | jq length) -eq "$N0" ]] \
    && pass "no branch created by AGit push" || fail "AGit created a branch"
[[ $(wait_status "$A") == success ]] && pass "CI green on AGit-only commit" || fail "CI not green on AGit commit"

echo "[3] AGit amend without force-push is rejected"
echo "value=2" > VALUE.txt; git commit -q --amend -am "T-A: amended"
A2=$(git rev-parse HEAD)
if git push -q origin HEAD:refs/for/master -o topic=T-A 2>/dev/null; then
    fail "amend without force-push was accepted"
else
    pass "amend without force-push rejected"
fi
git push -q origin HEAD:refs/for/master -o topic=T-A -o force-push=true 2>/dev/null
[[ $(api "$API/pulls/$PR" | jq -r .head.sha) == "$A2" ]] \
    && pass "force-push updated PR head to exact amended SHA" || fail "PR head wrong after force-push"

echo "[4] race: second writer moves master; stale expectations refuse"
git clone -q "$FORGE_SSH/$FORGE_REPO.git" "$WORK/writer2"
( cd "$WORK/writer2" && git config user.name w2 && git config user.email w2@invalid \
  && echo x > OTHER.txt && git add OTHER.txt && git commit -qm "N" && git push -q origin master )
OLD=$(git rev-parse origin/master)   # stale by construction
if git push -q origin "$A2:refs/heads/master" --force-with-lease=refs/heads/master:"$OLD" 2>/dev/null; then
    fail "stale lease was accepted"
else
    pass "stale lease refused (exact CAS)"
fi
code=$(api -o /dev/null -w '%{http_code}' -X POST "$API/pulls/$PR/merge" \
    -H 'Content-Type: application/json' -d "{\"Do\":\"fast-forward-only\",\"head_commit_id\":\"$A\"}")
# Refusal ORDER is version behavior: base divergence (405) is checked before
# the head pin (409). Either way, nothing may land.
[[ "$code" == 405 || "$code" == 409 ]] \
    && pass "merge refused on diverged base ($code)" || fail "diverged-base merge not refused ($code)"

echo "[5] prepare on the true tip; land via ff-only merge with pinned head"
git fetch -q origin
git checkout -qB prep origin/master
if ! git cherry-pick cand >/dev/null 2>&1; then
    git cherry-pick --abort
    # conflict path: revise instead of resolving inside a landing (roadmap §8)
    echo "value=9" > VALUE.txt; git add VALUE.txt; git commit -qm "T-A revised on tip"
fi
M=$(git rev-parse HEAD)
git push -q origin HEAD:refs/for/master -o topic=T-A -o force-push=true 2>/dev/null
[[ $(api "$API/pulls/$PR" | jq -r .head.sha) == "$M" ]] \
    && pass "PR head = prepared commit" || fail "PR head is not the prepared commit"
[[ $(wait_status "$M") == success ]] && pass "checks green on prepared commit" || fail "prepared commit not green"
code=$(api -o /dev/null -w '%{http_code}' -X POST "$API/pulls/$PR/merge" \
    -H 'Content-Type: application/json' -d "{\"Do\":\"fast-forward-only\",\"head_commit_id\":\"$A\"}")
[[ "$code" == 409 ]] && pass "head pin enforced with aligned base (409)" || fail "head pin not enforced ($code)"
code=$(api -o /dev/null -w '%{http_code}' -X POST "$API/pulls/$PR/merge" \
    -H 'Content-Type: application/json' -d "{\"Do\":\"fast-forward-only\",\"head_commit_id\":\"$M\"}")
[[ "$code" == 200 ]] || fail "ff-only merge refused ($code)"
git fetch -q origin
[[ $(git rev-parse origin/master) == "$M" ]] \
    && pass "master advanced to the EXACT prepared commit" || fail "master is not the prepared commit"
merged=$(api "$API/pulls/$PR" | jq -r '"\(.merged) \(.merge_commit_sha)"')
[[ "$merged" == "true $M" ]] \
    && pass "PR natively truthful: merged=true at exact SHA" || fail "PR completion untruthful: $merged"
