#!/usr/bin/env bash
# Copyright (C) 2026 Techdelight BV
#
# Tests the idempotent trust/onboarding + MCP-merge jq filter that entrypoint.sh
# applies to ~/.claude.json on every container boot (Sprint 43 item 3: an older
# project cache must never re-fire Claude's "trust this folder?" dialog). The
# filter is extracted VERBATIM from entrypoint.sh (between its sentinel
# comments) and run against the REAL image defaults (claude.json), so this is
# the actual production logic — a drift in either is caught here, no Docker
# needed.
#
# Usage: bash scripts/test-trust-idempotency.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
ENTRYPOINT="$ROOT/entrypoint.sh"
DEFS="$ROOT/claude.json"

PASS=0
FAIL=0

command -v jq >/dev/null 2>&1 || { echo "jq is required" >&2; exit 2; }
[ -f "$ENTRYPOINT" ] || { echo "missing entrypoint: $ENTRYPOINT" >&2; exit 2; }
[ -f "$DEFS" ]       || { echo "missing defaults: $DEFS" >&2; exit 2; }

# Extract the jq program entrypoint.sh actually runs (between the sentinel
# comments), so the test can never drift from the shipped filter.
FILTER=$(awk '/trust-onboarding-filter-start/{f=1;next} /trust-onboarding-filter-end/{f=0} f' "$ENTRYPOINT")
[ -n "$FILTER" ] || { echo "could not extract trust filter from $ENTRYPOINT" >&2; exit 2; }

# patch reads a live .claude.json on stdin and prints the patched result,
# exactly as entrypoint.sh invokes it.
patch() { jq --slurpfile defaults "$DEFS" "$FILTER"; }

pass() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $1"; [ -n "${2:-}" ] && echo "    $2"; FAIL=$((FAIL + 1)); }

# assert_jq NAME JSON EXPR — EXPR (a jq boolean over the patched doc) must hold.
assert_jq() {
    local name="$1" json="$2" expr="$3" got
    got=$(printf '%s' "$json" | jq -r "$expr")
    if [ "$got" = "true" ]; then pass "$name"; else fail "$name" "$expr => $got, want true"; fi
}

# The four keys whose absence/`false` lets the trust dialog fire.
assert_trusted() {
    local name="$1" json="$2"
    assert_jq "$name — hasCompletedOnboarding"       "$json" '.hasCompletedOnboarding == true'
    assert_jq "$name — bypassPermissionsModeAccepted" "$json" '.bypassPermissionsModeAccepted == true'
    assert_jq "$name — hasTrustDialogAccepted"       "$json" '.projects["/workspace"].hasTrustDialogAccepted == true'
    assert_jq "$name — hasCompletedProjectOnboarding" "$json" '.projects["/workspace"].hasCompletedProjectOnboarding == true'
}

echo "entrypoint.sh — trust/onboarding filter"

# 1. Fresh/empty cache — all trust keys set, both default MCP servers merged in.
OUT=$(printf '{}' | patch)
assert_trusted "fresh cache" "$OUT"
assert_jq "fresh cache — skill-catalog MCP present" "$OUT" '.mcpServers["skill-catalog"] != null'
assert_jq "fresh cache — project-mgmt MCP present"  "$OUT" '.mcpServers["project-mgmt"] != null'

# 2. Older cache: no `projects` key at all, no trust keys, has a user setting
#    and the user's own MCP server. Trust keys get vivified; user data survives.
OUT=$(printf '{"someUserSetting":42,"mcpServers":{"my-own":{"command":"x"}}}' | patch)
assert_trusted "old cache (no projects key)" "$OUT"
assert_jq "old cache — user setting preserved"   "$OUT" '.someUserSetting == 42'
assert_jq "old cache — user MCP server preserved" "$OUT" '.mcpServers["my-own"].command == "x"'
assert_jq "old cache — default MCP merged in"    "$OUT" '.mcpServers["skill-catalog"] != null'

# 3. Stale `false` — force-set to true; unrelated project data survives.
OUT=$(printf '{"projects":{"/workspace":{"hasTrustDialogAccepted":false,"history":["a"]}}}' | patch)
assert_trusted "stale hasTrustDialogAccepted=false" "$OUT"
assert_jq "stale false — project history preserved" "$OUT" '.projects["/workspace"].history == ["a"]'

# 4. User MCP override wins over the default (existing entries win the merge).
OUT=$(printf '{"mcpServers":{"skill-catalog":{"command":"/custom/mine"}}}' | patch)
assert_jq "user MCP override survives merge" "$OUT" '.mcpServers["skill-catalog"].command == "/custom/mine"'

# 5. Idempotent — patching an already-patched cache changes nothing.
ONE=$(printf '{"foo":1}' | patch -c)
TWO=$(printf '%s' "$ONE" | patch -c)
if [ "$ONE" = "$TWO" ]; then pass "idempotent (patch twice == once)"; else fail "idempotent" "run1 != run2"; fi

# 6. Malformed cache → jq exits non-zero, which entrypoint.sh treats as
#    non-fatal (leaves the cache untouched, startup continues).
if printf '{bad json' | patch >/dev/null 2>&1; then
    fail "malformed cache → jq non-zero" "jq unexpectedly succeeded"
else
    pass "malformed cache → jq exits non-zero (entrypoint non-fatal)"
fi

echo
echo "  $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
