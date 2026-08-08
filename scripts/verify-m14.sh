#!/usr/bin/env bash
# Copyright (C) 2026 Techdelight BV
#
# verify-m14.sh — host verification for Milestone 14 (Independent Verification).
#
# M14 adds the plane-owned verify pass: the frozen `.daedalus/verify.json` policy,
# the test-integrity gate, the null-agent floor, image-digest pinning, and the
# clean verifier container that performs candidate → verified.
#
# Like verify-m13.sh, the container-less dev env can prove all the *logic* (the
# full suite + a fake-verifier smoke); two things need a real host:
#   * the end-to-end verify flow with a real binary and the STUB verifier
#     (the `fake` phase — no Docker, fully automated), and
#   * the ACTUAL clean-verifier container running policy.checks in the project's
#     pinned image (the `real` phase — needs Docker + a built image + a project
#     carrying a `.daedalus/verify.json`). That is the one seam CI cannot exercise.
#
# Everything runs in an ISOLATED data dir (default: a temp dir), so it never
# touches your real registry, control.db, or a running daedalus-control daemon.
# The daemon it spawns is killed by its own pidfile, not by name.
#
# Phases (run from the repo root):
#
#   bash scripts/verify-m14.sh fake              # no Docker — verified / null-agent
#                                                #   floor / integrity gate / verifier-
#                                                #   fail / freeze, all asserted
#   bash scripts/verify-m14.sh real <project> [objective]
#                                                # needs Docker + a built image + a
#                                                #   project with .daedalus/verify.json;
#                                                #   dispatches the REAL agent then runs
#                                                #   the REAL clean verifier
#   bash scripts/verify-m14.sh all <project>     # fake, then real if Docker is present
#
# Building + overrides mirror verify-m13.sh (DATA_DIR, BIN_DIR, BUILD, REAL_DATA_DIR).

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

PASS=0; FAIL=0
pass() { echo "  ✓ $1"; PASS=$((PASS + 1)); }
fail() { echo "  ✗ $1"; FAIL=$((FAIL + 1)); }
info() { echo "  · $1"; }
hdr()  { echo; echo "── $1 ──"; }

# ── Isolated workspace ───────────────────────────────────────────────────────
WORK="$(mktemp -d)"
DATA_DIR="${DATA_DIR:-$WORK/data}"
BIN_DIR="${BIN_DIR:-}"
mkdir -p "$DATA_DIR"

REAL_PIDFILE=""
kill_pidfile() {
    [[ -f "$1" ]] || return 0
    local pid; pid="$(cat "$1" 2>/dev/null || true)"
    [[ -n "${pid:-}" ]] && kill "$pid" 2>/dev/null || true
}
cleanup() {
    kill_pidfile "$DATA_DIR/.daedalus/control.pid"
    [[ -n "$REAL_PIDFILE" ]] && kill_pidfile "$REAL_PIDFILE"
    rm -rf "$WORK"
}
trap cleanup EXIT

detect_real_datadir() {
    [[ -n "${REAL_DATA_DIR:-}" ]] && { printf '%s' "$REAL_DATA_DIR"; return 0; }
    local dbin; dbin="$(command -v daedalus 2>/dev/null)" || return 1
    dbin="$(readlink -f "$dbin" 2>/dev/null || printf '%s' "$dbin")"
    local d; d="$(dirname "$dbin")"
    local cfg dd
    for cfg in "$d/config.json" "$d/current/config.json"; do
        [[ -f "$cfg" ]] || continue
        dd="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1])).get("data-dir",""))' "$cfg" 2>/dev/null)"
        [[ -n "$dd" && -f "$dd/projects.json" ]] && { printf '%s' "$dd"; return 0; }
    done
    return 1
}

GO_MIN_MINOR=25
host_go_ok() {
    command -v go >/dev/null 2>&1 || return 1
    local v; v="$(go version 2>/dev/null | grep -oE 'go[0-9]+\.[0-9]+' | head -1 | sed 's/go//')"
    [[ -n "$v" ]] || return 1
    local maj="${v%%.*}" min="${v##*.}"
    [[ "$maj" -gt 1 || ( "$maj" -eq 1 && "$min" -ge "$GO_MIN_MINOR" ) ]]
}

ensure_binaries() {
    if [[ -n "$BIN_DIR" ]]; then
        [[ -x "$BIN_DIR/daedalus" && -x "$BIN_DIR/daedalus-control" ]] \
            || { echo "BIN_DIR=$BIN_DIR must contain daedalus + daedalus-control"; exit 2; }
        info "using pre-built binaries in $BIN_DIR"
        return
    fi
    BIN_DIR="$WORK/bin"; mkdir -p "$BIN_DIR"
    if [[ "${BUILD:-auto}" != docker ]] && host_go_ok; then
        info "building daedalus + daedalus-control with host Go…"
        ( cd "$REPO_ROOT" && go build -o "$BIN_DIR/daedalus" ./cmd/daedalus \
            && go build -o "$BIN_DIR/daedalus-control" ./cmd/daedalus-control ) \
            || { echo "host build failed"; exit 2; }
    elif command -v docker >/dev/null 2>&1; then
        info "host Go missing or <1.$GO_MIN_MINOR — building in golang:1.25-bookworm (Docker)…"
        docker run --rm -v "$REPO_ROOT":/src -v "$BIN_DIR":/out -w /src \
            --user "$(id -u):$(id -g)" -e HOME=/tmp golang:1.25-bookworm \
            sh -c "go build -buildvcs=false -o /out/daedalus ./cmd/daedalus && \
                   go build -buildvcs=false -o /out/daedalus-control ./cmd/daedalus-control" \
            || { echo "docker build failed"; exit 2; }
    else
        echo "Need Go ≥ 1.$GO_MIN_MINOR or Docker to build, OR set BIN_DIR."; exit 2
    fi
    [[ -x "$BIN_DIR/daedalus" && -x "$BIN_DIR/daedalus-control" ]] \
        || { echo "build produced no binaries in $BIN_DIR"; exit 2; }
}

# `daedalus` with our isolated data dir; the daemon inherits any env we export.
dae() { NO_COLOR=1 DAEDALUS_DATA_DIR="$DATA_DIR" "$BIN_DIR/daedalus" "$@"; }

# Restart the isolated daemon so a changed DAEDALUS_CONTROL_FAKE_* env takes
# effect (the daemon captures env at spawn time).
restart_daemon() { kill_pidfile "$DATA_DIR/.daedalus/control.pid"; sleep 0.5; }

register() {
    local name="$1" dir="$2"
    python3 - "$DATA_DIR/projects.json" "$name" "$dir" <<'PY'
import json,os,sys
path,name,dir=sys.argv[1:4]
reg=json.load(open(path)) if os.path.exists(path) else {"version":3,"projects":{}}
reg.setdefault("projects",{})[name]={"directory":dir,"target":"dev"}
json.dump(reg,open(path,"w"))
PY
}

new_git_repo() {
    local dir="$1"; mkdir -p "$dir"
    ( cd "$dir" && git init -q && git config user.email v@m14.test && git config user.name m14 \
      && echo seed > seed.txt && git add -A && git commit -q -m init )
}

db() {
    if command -v sqlite3 >/dev/null 2>&1; then
        sqlite3 "$DATA_DIR/control.db" "$1" 2>/dev/null
    else
        python3 - "$DATA_DIR/control.db" "$1" <<'PY' 2>/dev/null
import sqlite3,sys
con=sqlite3.connect(sys.argv[1])
for row in con.execute(sys.argv[2]).fetchall():
    print("|".join("" if v is None else str(v) for v in row))
PY
    fi
}

# ── Phase: fake (no Docker) — the plane-owned verify flow with the STUB verifier
#
# The daemon captures the DAEDALUS_CONTROL_FAKE_* env at SPAWN time, so each
# sub-phase exports the modes it needs and restart_daemon forces a respawn that
# inherits them (the next `dae` call spawns it). unset clears a stale marker.
phase_fake() {
    hdr "fake — verify flow: verified / floor / gate / verifier-fail / freeze (no Docker)"
    local proj="$WORK/proj-fake"; new_git_repo "$proj"; register demo "$proj"
    local out

    # ── verified: gate-clean src change + passing stub verifier ──
    export DAEDALUS_CONTROL_FAKE_VERIFY=pass DAEDALUS_CONTROL_FAKE_RUNNER=1
    unset DAEDALUS_CONTROL_FAKE_RUNNER_MARKER; restart_daemon
    dae task create --project demo --objective 'add feature' >/dev/null 2>&1
    dae task dispatch T-1 >/dev/null 2>&1
    out="$(dae task verify T-1 2>&1)"
    echo "$out" | grep -q "VERIFIED" && pass "gate-clean + verifier pass → verified" || { fail "verified"; echo "$out"; }
    [[ "$(db "SELECT state FROM tasks WHERE id='T-1';")" == verified ]] && pass "task T-1 = verified" || fail "task not verified"
    [[ "$(db "SELECT verify FROM artifacts WHERE id='A-1';")" == pass ]] && pass "artifact verify = pass" || fail "artifact verify != pass"
    dae task cancel T-1 >/dev/null 2>&1   # free the project

    # ── null-agent floor: empty change (head == base) → rejected, verifier skipped ──
    export DAEDALUS_CONTROL_FAKE_RUNNER=empty; restart_daemon
    dae task create --project demo --objective 'do nothing' >/dev/null 2>&1
    dae task dispatch T-2 >/dev/null 2>&1
    [[ "$(db "SELECT output_snapshot=base_sha FROM jobs WHERE id='J-2';")" == 1 ]] && pass "empty job: head == base" || fail "empty job not head==base"
    out="$(dae task verify T-2 2>&1)"
    echo "$out" | grep -qi "null-agent floor\|empty change" && pass "null-agent floor rejects the empty change" || { fail "floor"; echo "$out"; }
    [[ "$(db "SELECT state FROM tasks WHERE id='T-2';")" == rejected ]] && pass "task T-2 = rejected (floor)" || fail "floor task not rejected"
    db "SELECT note FROM events WHERE entity_type='task' AND entity_id='T-2' AND to_state='rejected';" | grep -qi "floor" && pass "floor recorded in event log" || fail "floor note missing"
    [[ "$(db "SELECT count(*) FROM events WHERE entity_id='T-2' AND to_state='verifying';")" == 0 ]] && pass "verifier NOT called on the floor hit" || fail "verifier called on floor hit"
    dae task cancel T-2 >/dev/null 2>&1

    # ── integrity gate: edits a *_test.go → rejected BEFORE the verifier ──
    export DAEDALUS_CONTROL_FAKE_RUNNER=1 DAEDALUS_CONTROL_FAKE_RUNNER_MARKER='sneaky_test.go'; restart_daemon
    dae task create --project demo --objective 'sneak test edit' >/dev/null 2>&1
    dae task dispatch T-3 >/dev/null 2>&1
    out="$(dae task verify T-3 2>&1)"
    echo "$out" | grep -qi "integrity gate" && pass "integrity gate rejects test-editing job" || { fail "gate"; echo "$out"; }
    db "SELECT note FROM events WHERE entity_type='task' AND entity_id='T-3' AND to_state='rejected';" | grep -qi "integrity gate" \
        && pass "gate short-circuit recorded (candidate→rejected, no verifying)" || fail "gate note missing"
    [[ "$(db "SELECT count(*) FROM events WHERE entity_id='T-3' AND to_state='verifying';")" == 0 ]] \
        && pass "verifier NOT called on a gate hit" || fail "verifier was called on a gate hit"
    dae task cancel T-3 >/dev/null 2>&1

    # ── verifier fail: gate-clean job but the stub verifier fails → rejected ──
    export DAEDALUS_CONTROL_FAKE_VERIFY=fail DAEDALUS_CONTROL_FAKE_RUNNER=1
    unset DAEDALUS_CONTROL_FAKE_RUNNER_MARKER; restart_daemon
    dae task create --project demo --objective 'clean but fails' >/dev/null 2>&1
    dae task dispatch T-4 >/dev/null 2>&1
    out="$(dae task verify T-4 2>&1)"
    echo "$out" | grep -qi "REJECTED" && pass "gate-clean + verifier fail → rejected" || { fail "verifier-fail"; echo "$out"; }
    [[ "$(db "SELECT count(*) FROM events WHERE entity_id='T-4' AND to_state='verifying';")" == 1 ]] \
        && pass "verifier WAS called (candidate→verifying→rejected)" || fail "expected a verifying step"
    dae task cancel T-4 >/dev/null 2>&1

    # ── freeze: policy hash is captured at create and immune to working-tree edits ──
    export DAEDALUS_CONTROL_FAKE_VERIFY=pass DAEDALUS_CONTROL_FAKE_RUNNER=1; restart_daemon
    local fp="$WORK/proj-freeze"; new_git_repo "$fp"
    mkdir -p "$fp/.daedalus"
    echo '{"checks":["go test ./..."],"acceptanceGlobs":["**/*_test.go"]}' > "$fp/.daedalus/verify.json"
    ( cd "$fp" && git add -A && git commit -q -m 'seed verify policy' )
    register freeze "$fp"
    dae task create --project freeze --objective 'freeze test' >/dev/null 2>&1
    local h1; h1="$(db "SELECT acceptance_hash FROM tasks WHERE project='freeze';")"
    [[ -n "$h1" && "$h1" == sha256:* ]] && pass "acceptance_hash frozen at create ($h1)" || fail "no acceptance hash"
    echo '{"checks":["echo cheat"],"acceptanceGlobs":[]}' > "$fp/.daedalus/verify.json"   # edit working tree
    local h2; h2="$(db "SELECT acceptance_hash FROM tasks WHERE project='freeze';")"
    [[ "$h1" == "$h2" ]] && pass "frozen hash unchanged after a working-tree policy edit" || fail "hash changed ($h1 → $h2)"
}

# ── Phase: real (Docker) — the clean verifier container ──────────────────────
phase_real() {
    hdr "real — clean verifier container running policy.checks (Docker)"
    local project="${1:-}"; local objective="${2:-Add a trivial no-op change and keep the build+tests green.}"
    if [[ -z "$project" ]]; then
        echo "  usage: verify-m14.sh real <registered-project-with-.daedalus/verify.json> [objective]"; FAIL=$((FAIL+1)); return
    fi
    command -v docker >/dev/null 2>&1 || { echo "  Docker not found — skipping the real phase."; return; }

    local RD; RD="$(detect_real_datadir)" || {
        echo "  ✗ couldn't locate your real data dir. Set REAL_DATA_DIR=<dir-with-projects.json> and re-run."
        FAIL=$((FAIL+1)); return
    }
    info "using your real data dir: $RD"
    if ! python3 -c 'import json,sys;d=json.load(open(sys.argv[1]+"/projects.json"));sys.exit(0 if sys.argv[2] in d.get("projects",{}) else 1)' "$RD" "$project" 2>/dev/null; then
        echo "  ✗ project '$project' is not registered in $RD/projects.json."; FAIL=$((FAIL+1)); return
    fi
    info "the REAL verifier checks out the artifact's head_sha into a fresh clean worktree and runs"
    info "the project's .daedalus/verify.json checks in a container built from the project's PINNED image."
    info "requires a built daedalus image for '$project' and (for dispatch) working runner credentials."
    REAL_PIDFILE="$RD/.daedalus/control.pid"

    local run=( env DAEDALUS_DATA_DIR="$RD" "$BIN_DIR/daedalus" )
    local cout; cout="$("${run[@]}" task create --project "$project" --objective "$objective" 2>&1)"; echo "  $cout"
    local tid; tid="$(echo "$cout" | grep -oE 'T-[0-9]+' | head -1)"
    [[ -z "$tid" ]] && { fail "real: task create failed"; return; }
    info "dispatching $tid (real agent — may take a while)…"
    local dout; dout="$("${run[@]}" task dispatch "$tid" 2>&1)"; echo "  $dout"
    if ! echo "$dout" | grep -q "state candidate"; then
        info "dispatch did not reach candidate (a non-success is fine — nothing to verify). status: daedalus task status $tid"
        return
    fi
    pass "real agent produced a candidate"
    info "running the REAL clean verifier on $tid…"
    local vout; vout="$("${run[@]}" task verify "$tid" 2>&1)"; echo "  $vout"
    if echo "$vout" | grep -qiE "VERIFIED|REJECTED"; then
        pass "clean verifier reached a verdict (the CleanVerifier seam works)"
        echo "$vout" | grep -qi VERIFIED \
            && info "artifact verified in a clean checkout of the pinned image" \
            || info "rejected — inspect the daemon log at $RD/.daedalus/control.log for the failing check"
    else
        fail "verify did not produce a verdict — inspect $RD/.daedalus/control.log"
        echo "$vout"
    fi
    info "clean up when done:  daedalus task cancel $tid"
}

# ── Main ─────────────────────────────────────────────────────────────────────
MODE="${1:-fake}"
ensure_binaries
case "$MODE" in
    fake) phase_fake ;;
    real) phase_real "${2:-}" "${3:-}" ;;
    all)  phase_fake; phase_real "${2:-}" "${3:-}" ;;
    *)    echo "usage: $0 {fake|real <project> [objective]|all <project>}"; exit 2 ;;
esac

echo
echo "──────────────────────────────────────────"
echo "  M14 verification: $PASS passed, $FAIL failed"
echo "──────────────────────────────────────────"
[[ "$FAIL" -eq 0 ]]
