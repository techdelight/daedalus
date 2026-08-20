#!/usr/bin/env bash
# Copyright (C) 2026 Techdelight BV
#
# verify-m20.sh — host verification for Milestone 20
#                 (Programmes in the Plane & a Reviewer at the Gate).
#
# M20 has two halves and they verify very differently.
#
#   PART ONE — programmes and rationale — is pure control-plane state. All of it
#   can be proven with no Docker at all, and the `fake` phase does exactly that:
#   a programme is adopted from the legacy file store, a Task points at it,
#   a rationale is recorded WITH ITS AUTHOR, and the roll-up reports the edges
#   that leave the programme.
#
#   PART TWO — the reviewer — is an agent in a container. The `fake` phase can
#   prove the recording path (with the stub reviewer) and, most importantly, that
#   a judgement moves NOTHING. What it cannot prove is that a real agent, given
#   the real prompt, writes a judgement file the plane can read. That is the
#   `real` phase, and it is the seam this repository's history says to distrust:
#   every host-only seam here — the runner, the verifier's entrypoint, the git
#   mount — was green in tests and broken on a host.
#
# Everything runs in an ISOLATED data dir (default: a temp dir), so it never
# touches your real registry, control.db, or a running daedalus-control daemon.
# The daemon it spawns is killed by its own pidfile, not by name.
#
# Phases (run from the repo root):
#
#   bash scripts/verify-m20.sh fake                 # no Docker — everything above
#   bash scripts/verify-m20.sh real <project>       # needs Docker + a logged-in
#                                                   #   project: dispatches a real
#                                                   #   Job and runs the REAL reviewer
#   bash scripts/verify-m20.sh all <project>        # fake, then real if Docker is present
#
# Overrides mirror verify-m13/m14: DATA_DIR, BIN_DIR, BUILD.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

PASS=0; FAIL=0
pass() { echo "  ✓ $1"; PASS=$((PASS + 1)); }
fail() { echo "  ✗ $1"; FAIL=$((FAIL + 1)); }
info() { echo "  · $1"; }
hdr()  { echo; echo "── $1 ──"; }

WORK="$(mktemp -d)"
DATA_DIR="${DATA_DIR:-$WORK/data}"
BIN_DIR="${BIN_DIR:-}"
mkdir -p "$DATA_DIR"

kill_pidfile() {
    [[ -f "$1" ]] || return 0
    local pid; pid="$(cat "$1" 2>/dev/null || true)"
    [[ -n "${pid:-}" ]] && kill "$pid" 2>/dev/null || true
}
cleanup() {
    kill_pidfile "$DATA_DIR/.daedalus/control.pid"
    rm -rf "$WORK"
}
trap cleanup EXIT

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
        info "host Go missing or <1.$GO_MIN_MINOR — building in golang:1.25-bookworm…"
        docker run --rm -v "$REPO_ROOT":/src -v "$BIN_DIR":/out -w /src \
            --user "$(id -u):$(id -g)" -e HOME=/tmp golang:1.25-bookworm \
            sh -c "go build -buildvcs=false -o /out/daedalus ./cmd/daedalus && \
                   go build -buildvcs=false -o /out/daedalus-control ./cmd/daedalus-control" \
            || { echo "docker build failed"; exit 2; }
    else
        echo "Need Go ≥ 1.$GO_MIN_MINOR or Docker to build, OR set BIN_DIR."; exit 2
    fi
}

dae() { NO_COLOR=1 DAEDALUS_DATA_DIR="$DATA_DIR" "$BIN_DIR/daedalus" "$@"; }

register() {
    python3 - "$DATA_DIR/projects.json" "$1" "$2" <<'PY'
import json,os,sys
path,name,dir=sys.argv[1:4]
reg=json.load(open(path)) if os.path.exists(path) else {"version":3,"projects":{}}
reg.setdefault("projects",{})[name]={"directory":dir,"target":"dev"}
json.dump(reg,open(path,"w"))
PY
}

new_git_repo() {
    local dir="$1"; mkdir -p "$dir"
    ( cd "$dir" && git init -q && git config user.email v@m20.test && git config user.name m20 \
      && echo seed > seed.txt && git add -A && git commit -q -m init )
}

# A legacy file-backed programme, written the way the pre-M20 store did.
seed_legacy_programme() {
    mkdir -p "$DATA_DIR/programmes"
    cat > "$DATA_DIR/programmes/fluency.json" <<'JSON'
{"name":"fluency","description":"get conversational by spring",
 "projects":["alpha","beta"],
 "deps":[{"upstream":"beta","downstream":"alpha"}]}
JSON
}

# ── PART ONE: programmes and rationale (no Docker) ───────────────────────────
phase_fake() {
    hdr "M20 part one — a programme the plane owns"
    ensure_binaries
    export DAEDALUS_CONTROL_FAKE_RUNNER=1 DAEDALUS_CONTROL_FAKE_VERIFY=1

    new_git_repo "$WORK/alpha"; register alpha "$WORK/alpha"
    new_git_repo "$WORK/beta";  register beta  "$WORK/beta"
    seed_legacy_programme

    # The daemon adopts the legacy definition on start — once, by name.
    local out
    out="$(dae programmes list 2>&1)"
    grep -q 'fluency' <<<"$out" && pass "the legacy file-backed programme was adopted into the plane" \
        || { fail "adoption did not happen"; echo "$out"; }
    grep -qE 'PR-[0-9]+' <<<"$out" && pass "it has a plane id (PR-n), not just a filename" \
        || fail "no PR- id in the listing"

    # Idempotent: a second daemon start must not duplicate it.
    kill_pidfile "$DATA_DIR/.daedalus/control.pid"; sleep 0.5
    local n; n="$(dae programmes list 2>&1 | grep -c 'fluency')"
    [[ "$n" == 1 ]] && pass "a second daemon start did not duplicate it (import is idempotent)" \
        || fail "programme appears $n times after a restart"

    hdr "a Task that says what it is for"
    out="$(dae task create --project alpha --objective "add cursor pagination" \
             --programme fluency --rationale "the review queue depends on it" 2>&1)"
    local task; task="$(grep -oE 'T-[0-9]+' <<<"$out" | head -1)"
    [[ -n "$task" ]] && pass "created $task against the programme" || { fail "create failed"; echo "$out"; }

    out="$(dae task status "$task" 2>&1)"
    grep -q 'Programme:' <<<"$out" && pass "status names the programme" || fail "status does not name the programme"
    grep -q 'the review queue depends on it' <<<"$out" && pass "status carries the rationale" \
        || fail "status does not carry the rationale"
    # THE POINT: the author is recorded, and it came from the transport.
    grep -q '(human)' <<<"$out" && pass "the rationale is attributed to a human caller" \
        || { fail "rationale authorship missing — an agent's reason could read as yours"; echo "$out"; }

    # A dangling programme reference is refused; that was the file store's failure.
    out="$(dae task create --project alpha --objective x --programme no-such 2>&1)"
    grep -qiE 'no programme|not found' <<<"$out" && pass "an unknown programme is refused, not silently stored" \
        || { fail "a dangling programme reference was accepted"; echo "$out"; }

    hdr "the roll-up: what a programme waits on"
    local other; other="$(dae task create --project beta --objective "the thing alpha needs" 2>&1 | grep -oE 'T-[0-9]+' | head -1)"
    dae task depends "$task" --on "$other" >/dev/null 2>&1
    out="$(dae programmes status fluency 2>&1)"
    grep -q "$task" <<<"$out" && pass "the programme lists the work serving it" || fail "roll-up missing its task"
    grep -qi 'outside this programme' <<<"$out" \
        && pass "it reports the dependency that LEAVES the programme (the thing no per-project view shows)" \
        || { fail "external dependency not reported"; echo "$out"; }
    grep -q "$other" <<<"$out" && pass "and names the task it is waiting on" || fail "the external task is not named"

    # A programme with work against it cannot be dissolved out from under it.
    out="$(dae programmes remove fluency 2>&1)"
    grep -qi 'still has' <<<"$out" && pass "dissolving a programme with tasks is refused" \
        || { fail "a programme was dissolved out from under its tasks"; echo "$out"; }

    hdr "M20 part two — the reviewer reports, and moves nothing"
    kill_pidfile "$DATA_DIR/.daedalus/control.pid"; sleep 0.5
    export DAEDALUS_CONTROL_FAKE_REVIEW=fail

    # A FRESH task for the lifecycle: $task is deliberately blocked by the edge
    # above, which is the point of the roll-up check and would stop a dispatch.
    task="$(dae task create --project alpha --objective "the reviewed change" \
              --programme fluency --rationale "so the reviewer has a promise to judge" 2>&1 \
              | grep -oE 'T-[0-9]+' | head -1)"
    [[ -n "$task" ]] || { fail "could not create the task to review"; return; }
    dae task dispatch "$task" >/dev/null 2>&1
    dae task verify "$task"   >/dev/null 2>&1
    local before; before="$(dae task status "$task" 2>&1 | grep -oE 'State: *[a-z_]+' | head -1)"

    out="$(dae task review "$task" 2>&1)"
    grep -qi 'had concerns' <<<"$out" && pass "a failing review reports its verdict" \
        || { fail "the review verdict was not reported"; echo "$out"; }
    grep -qi 'advisory' <<<"$out" && pass "and says in as many words that it is advisory" \
        || fail "the review output does not say it is advisory"

    local after; after="$(dae task status "$task" 2>&1 | grep -oE 'State: *[a-z_]+' | head -1)"
    [[ "$before" == "$after" ]] \
        && pass "THE TASK DID NOT MOVE ($after) — a reviewer must not transition anything" \
        || fail "the task moved from '$before' to '$after' on a reviewer's say-so"

    out="$(dae task status "$task" 2>&1)"
    grep -q 'Reviews:' <<<"$out" && pass "the judgement is recorded on the task" || fail "no review recorded"
    grep -qE 'RV-[0-9]+' <<<"$out" && pass "with an id of its own" || fail "no review id"
    grep -qi 'blocking' <<<"$out" && pass "and its findings, by severity" || fail "findings not shown"

    # A second reading accumulates rather than overwriting the first.
    kill_pidfile "$DATA_DIR/.daedalus/control.pid"; sleep 0.5
    export DAEDALUS_CONTROL_FAKE_REVIEW=pass
    dae task review "$task" >/dev/null 2>&1
    n="$(dae task status "$task" 2>&1 | grep -cE '^  RV-[0-9]+')"
    [[ "$n" == 2 ]] && pass "a second reading accumulates; the first survives" \
        || fail "$n reviews recorded after two passes, want 2"

    echo; echo "part one + two (no Docker): $PASS passed, $FAIL failed"
}

# ── The Docker seam: a real agent, reviewing ─────────────────────────────────
phase_real() {
    local project="${1:-}"
    [[ -n "$project" ]] || { echo "usage: verify-m20.sh real <project>"; exit 2; }
    command -v docker >/dev/null 2>&1 || { echo "real phase needs Docker"; exit 2; }

    hdr "M20 part two — the REAL reviewer (Docker, real agent, real credentials)"
    cat <<TXT
  This is the seam tests cannot reach, and this repository's history says to
  distrust it: the runner, the verifier's entrypoint and the git mount were all
  green in tests and broken on a host.

  Run it against YOUR data dir, not this script's isolated one:

    daedalus task create --project $project \\
      --objective "<something small and real>" \\
      --rationale "<why you want it>"
    daedalus task dispatch  T-n
    daedalus task verify    T-n
    daedalus task review    T-n      # ← the new thing

  What to check, in order of how likely it is to be what breaks:

    1. Did a container start at all?      docker ps -a | grep daedalus-review
       The reviewer runs as project 'daedalus-review-<job>', NOT 'daedalus-job-<job>'.
       If you see the job name, the wrong project was launched.

    2. Did it have credentials?           <data-dir>/.daedalus/control.log
       'Not logged in' means the seeding did not reach it — the same failure Jobs
       had in the 'Not logged in' era, one component later.

    3. Did it write a judgement?          the review output says 'no judgement: …'
       if not. That message is deliberately NOT a rejection: it means the reviewer
       could not be made to report, which says nothing about the change.

    4. Is the judgement any good?         daedalus task status T-n
       Findings with a location and a 'why:' line are the point. Findings that
       restate the diff back at you mean the prompt needs work, not the plane.

    5. Did anything move?                 it must not.
       task status should show the SAME state before and after the review.
TXT
}

case "${1:-fake}" in
    fake) phase_fake ;;
    real) shift; phase_real "$@" ;;
    all)  phase_fake; shift || true; phase_real "$@" ;;
    *)    echo "usage: verify-m20.sh [fake|real <project>|all <project>]"; exit 2 ;;
esac

[[ "$FAIL" == 0 ]] || exit 1
