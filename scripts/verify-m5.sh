#!/usr/bin/env bash
# Copyright (C) 2026 Techdelight BV
#
# verify-m5.sh — host verification for Milestone 5 / Sprint 43.
#
# Runs the Docker-side checks that the container-only dev environment can't:
# image builds + cache win (item 1), installer pins (item 5), and — against a
# running project — the coordinator mounts, the uid/permission top risk, and
# trust-prompt idempotency (items 2 and 3). The full narrative is in
# docs/m5-verification.md; this is the runnable form of it.
#
# Phases (run from the repo root, as a NON-root user with Docker access):
#
#   bash scripts/verify-m5.sh build            # items 1 + 5 — fully automated
#   bash scripts/verify-m5.sh mounts <project> # items 2 + 3 — needs the project
#                                              #   running: `daedalus <project>`
#                                              #   in another terminal first
#   bash scripts/verify-m5.sh persist <project># item 2 #27 — run AFTER a
#                                              #   stop+relaunch of the project
#
# A few steps stay MANUAL (they need real Claude credentials or a human looking
# at the terminal); the script prints those clearly rather than faking them.
#
# Overrides: DATA_DIR (default <repo>/.cache), CONTAINER_PREFIX (default
# "claude-run-"), IMAGE_PREFIX (default "techdelight/claude-runner").

set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

DATA_DIR="${DATA_DIR:-$ROOT/.cache}"
CONTAINER_PREFIX="${CONTAINER_PREFIX:-claude-run-}"
IMAGE_PREFIX="${IMAGE_PREFIX:-techdelight/claude-runner}"

PASS=0
FAIL=0
MANUAL=0

pass()    { echo "  PASS   $1"; PASS=$((PASS + 1)); }
fail()    { echo "  FAIL   $1"; [ -n "${2:-}" ] && echo "         $2"; FAIL=$((FAIL + 1)); }
manual()  { echo "  MANUAL $1"; MANUAL=$((MANUAL + 1)); }
section() { echo; echo "== $1 =="; }
die()     { echo "verify-m5: $1" >&2; exit 2; }

require_docker() {
    command -v docker >/dev/null 2>&1 || die "docker CLI not found"
    docker info >/dev/null 2>&1 || die "docker daemon unreachable"
    command -v jq >/dev/null 2>&1 || die "jq not found (needed for mount inspection)"
    [ "$(id -u)" -ne 0 ] || die "run as a non-root user — the image builds its claude user at CLAUDE_UID=\$(id -u), and uid 0 collides with root"
}

# ── Phase: build (items 1 + 5) ───────────────────────────────────────────────
phase_build() {
    require_docker
    local uid; uid="$(id -u)"

    section "Item 1 — Go binaries"
    if ./build.sh; then pass "build.sh produced the Go binaries"; else fail "build.sh"; return; fi

    section "Item 1 — every Dockerfile target builds"
    local t
    for t in base utils dev godot copilot-base copilot-dev; do
        if docker build --target "$t" --build-arg CLAUDE_UID="$uid" -t "daedalus:m5-$t" . >/tmp/verify-m5-build-$t.log 2>&1; then
            pass "target $t"
        else
            fail "target $t" "see /tmp/verify-m5-build-$t.log"
        fi
    done

    section "Item 1 — cache win (touch a Go binary → heavy layers stay CACHED)"
    touch daedalus-runner
    if docker build --target dev --build-arg CLAUDE_UID="$uid" -t daedalus:m5-dev . >/tmp/verify-m5-cache.log 2>&1; then
        local cached; cached="$(grep -c 'CACHED' /tmp/verify-m5-cache.log || true)"
        if [ "${cached:-0}" -gt 0 ]; then
            pass "rebuild reused $cached cached layers (apt/Go/SDKMAN/installers); only the final COPYs re-ran"
        else
            fail "no CACHED layers on rebuild" "the layer graph is busting the cache — see /tmp/verify-m5-cache.log"
        fi
    else
        fail "dev rebuild" "see /tmp/verify-m5-cache.log"
    fi

    section "Item 5 — installer pins (versions come from the Dockerfile)"
    local claude_pin copilot_pin out
    claude_pin="$(sed -n 's/^ARG CLAUDE_VERSION=//p' Dockerfile)"
    copilot_pin="$(sed -n 's/^ARG COPILOT_VERSION=//p' Dockerfile)"
    out="$(docker run --rm --entrypoint /opt/claude/bin/claude daedalus:m5-dev --version 2>/dev/null || true)"
    if printf '%s' "$out" | grep -qF "$claude_pin"; then
        pass "claude pinned to $claude_pin ($out)"
    else
        fail "claude version" "expected to contain $claude_pin, got: ${out:-<none>}"
    fi
    out="$(docker run --rm --entrypoint /usr/local/bin/copilot daedalus:m5-copilot-dev --version 2>/dev/null || true)"
    if printf '%s' "$out" | grep -qF "$copilot_pin"; then
        pass "copilot pinned to $copilot_pin ($out)"
    else
        fail "copilot version" "expected to contain $copilot_pin, got: ${out:-<none>}"
    fi
    manual "optional: docker build --build-arg CLAUDE_VERSION=stable --target dev . should also build (proves the arg is wired)"
}

# ── Phase: mounts (items 2 + 3) ──────────────────────────────────────────────
phase_mounts() {
    require_docker
    local proj="${1:-}"
    [ -n "$proj" ] || die "usage: verify-m5.sh mounts <project>  (start it first: daedalus <project>)"
    local c="${CONTAINER_PREFIX}${proj}"

    docker ps --format '{{.Names}}' | grep -qx "$c" \
        || die "container $c is not running — start it first in another terminal: daedalus $proj [dir]"

    section "Item 2 — uid preflight (the top risk — check FIRST)"
    local build_uid run_uid ctr_uid
    build_uid="$(cat "$DATA_DIR/build-uid" 2>/dev/null || echo '?')"
    run_uid="$(id -u)"
    ctr_uid="$(docker exec "$c" id -u 2>/dev/null || echo '?')"
    if grep -qi 'built as uid' "$DATA_DIR/.daedalus/coordinator.log" 2>/dev/null; then
        fail "coordinator logged a uid mismatch" "$(grep -i 'built as uid' "$DATA_DIR/.daedalus/coordinator.log" | tail -1)"
    else
        pass "no uid-mismatch warning in the coordinator log"
    fi
    if [ "$build_uid" = "$run_uid" ] && [ "$run_uid" = "$ctr_uid" ]; then
        pass "uids line up (build=$build_uid run=$run_uid container=$ctr_uid)"
    else
        fail "uid mismatch (build=$build_uid run=$run_uid container=$ctr_uid)" "rebuild as the current user: daedalus --build"
    fi

    section "Item 2 — mounts present (#55/#37/#21/#27)"
    local dests expected d
    dests="$(docker inspect "$c" --format '{{json .Mounts}}' | jq -r '.[].Destination')"
    expected="/home/claude/.local/share/claude/versions
/home/claude/.m2
/opt/skills
/opt/tools
/workspace/.daedalus"
    while IFS= read -r d; do
        if printf '%s\n' "$dests" | grep -qxF "$d"; then pass "mount $d"; else fail "mount $d missing"; fi
    done <<< "$expected"

    section "Item 2 — writable by the container (proves the uid check directly)"
    for d in /opt/skills /opt/tools /home/claude/.m2 /home/claude/.local/share/claude/versions /workspace/.daedalus; do
        if docker exec "$c" sh -c "touch $d/.wtest && rm $d/.wtest" 2>/dev/null; then
            pass "writable $d"
        else
            fail "NOT writable $d" "the container's claude user can't write here — see the uid preflight above"
        fi
    done

    section "Item 2 — shared caches on the host (#37/#21)"
    for d in "$DATA_DIR/shared/claude-versions" "$DATA_DIR/shared/m2"; do
        if [ -d "$d" ]; then
            if [ -n "$(ls -A "$d" 2>/dev/null)" ]; then pass "$d exists and is populated"; else manual "$d exists but is empty — populates after real Claude/Maven use"; fi
        else
            fail "$d missing"
        fi
    done

    section "Item 2 — #27 tools persistence (setup; run 'persist' after a restart)"
    if docker exec "$c" sh -c 'cp "$(command -v jq)" /opt/tools/bin/jq' 2>/dev/null; then
        pass "placed jq in /opt/tools/bin (host: $DATA_DIR/tools/$proj/bin/)"
        manual "now stop + relaunch '$proj', then: bash scripts/verify-m5.sh persist $proj"
    else
        fail "could not write /opt/tools/bin/jq" "see the writability check above"
    fi

    section "Item 3 — trust idempotency"
    if bash "$ROOT/scripts/test-trust-idempotency.sh" >/tmp/verify-m5-trust.log 2>&1; then
        pass "filter-level regression green (scripts/test-trust-idempotency.sh)"
    else
        fail "trust filter regression" "see /tmp/verify-m5-trust.log"
    fi
    local cfg="$DATA_DIR/$proj/.claude-config/.claude.json"
    if [ -f "$cfg" ]; then
        if jq 'del(.projects["/workspace"])' "$cfg" > "$cfg.tmp" 2>/dev/null && mv "$cfg.tmp" "$cfg"; then
            manual "simulated an OLD cache (dropped the /workspace trust keys in $cfg)."
            manual "  stop + relaunch '$proj' and confirm NO \"trust this folder?\" dialog fires."
        else
            rm -f "$cfg.tmp"
            fail "could not edit $cfg to simulate an old cache"
        fi
    else
        manual "no live cache at $cfg yet — start a real session once, then re-run to simulate the old-cache case"
    fi
    manual "fresh attach earlier should also have shown NO trust dialog (needs your eyes + Claude login)"
}

# ── Phase: persist (item 2 #27, after a restart) ─────────────────────────────
phase_persist() {
    require_docker
    local proj="${1:-}"
    [ -n "$proj" ] || die "usage: verify-m5.sh persist <project>"
    local c="${CONTAINER_PREFIX}${proj}"
    docker ps --format '{{.Names}}' | grep -qx "$c" \
        || die "container $c not running — relaunch it first: daedalus $proj"

    section "Item 2 — #27 tools survive a restart"
    if docker exec "$c" sh -c 'command -v jq | grep -qx /opt/tools/bin/jq' 2>/dev/null; then
        pass "jq still resolves to /opt/tools/bin/jq after restart"
    else
        fail "jq did not persist to /opt/tools/bin after restart"
    fi
    if [ -f "$DATA_DIR/tools/$proj/bin/jq" ]; then
        pass "present on the host at $DATA_DIR/tools/$proj/bin/jq"
    else
        fail "missing on the host at $DATA_DIR/tools/$proj/bin/jq"
    fi
}

usage() {
    cat <<'EOF'
verify-m5.sh — host verification for Milestone 5 / Sprint 43.

Runs the Docker-side checks the container-only dev environment can't: image
builds + cache win (item 1), installer pins (item 5), and — against a running
project — the coordinator mounts, the uid/permission top risk, and trust-prompt
idempotency (items 2 and 3). Full narrative: docs/m5-verification.md.

Run from the repo root, as a NON-root user with Docker access:

  bash scripts/verify-m5.sh build             items 1 + 5 — fully automated
  bash scripts/verify-m5.sh mounts <project>  items 2 + 3 — start the project
                                              first: `daedalus <project>` in
                                              another terminal
  bash scripts/verify-m5.sh persist <project> item 2 #27 — run AFTER a
                                              stop+relaunch of the project

Steps needing real Claude credentials or a human watching the terminal are
printed as MANUAL rather than faked.

Overrides (env): DATA_DIR (default <repo>/.cache), CONTAINER_PREFIX (default
"claude-run-"), IMAGE_PREFIX (default "techdelight/claude-runner").

Item 6 (Maven overlay) is deferred — only needed if the shared .m2 shows
cross-project pollution. Recommendation: no action unless observed.
EOF
}

main() {
    case "${1:-}" in
        build)   phase_build ;;
        mounts)  phase_mounts "${2:-}" ;;
        persist) phase_persist "${2:-}" ;;
        ""|-h|--help|help) usage; exit 0 ;;
        *) die "unknown phase '${1}' — try: build | mounts <project> | persist <project>" ;;
    esac

    echo
    echo "── $PASS passed, $FAIL failed, $MANUAL manual/creds step(s) remaining ──"
    [ "$FAIL" -eq 0 ]
}

main "$@"
