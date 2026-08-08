#!/usr/bin/env bash
# Copyright (C) 2026 Techdelight BV
#
# verify-m13.sh — host verification for Milestone 13 (the control plane, V1).
#
# The container-less dev environment can prove the control-plane *logic* (the
# full suite + a StubRunner smoke), but two things need a real host:
#   * the end-to-end daemon + CLI + isolated-worktree flow with a real binary
#     (the `fake` phase — no Docker, fully automated), and
#   * the ACTUAL headless agent run in a container against the worktree — the
#     CoordinatorRunner (the `real` phase — needs Docker + a built image + a
#     working runner/creds). That is the one seam CI cannot exercise.
#
# Everything runs in an ISOLATED data dir (default: a temp dir), so it never
# touches your real registry, control.db, or a running daedalus-control daemon.
# The daemon it spawns is killed by its own pidfile, not by name.
#
# Phases (run from the repo root):
#
#   bash scripts/verify-m13.sh fake              # no Docker — the full CLI/daemon/
#                                                #   worktree/reconcile loop, asserted
#   bash scripts/verify-m13.sh real <project> [objective]
#                                                # needs Docker + a built image;
#                                                #   dispatches the REAL agent against
#                                                #   an isolated worktree of <project>
#   bash scripts/verify-m13.sh all <project>     # fake, then real if Docker is present
#
# Building: the repo needs Go 1.25. If your host Go is older or missing, the
# script automatically builds the two binaries inside a golang:1.25 container
# (like build.sh), so you only need Docker — not a matching host Go.
#
# Overrides:
#   DATA_DIR   isolated state dir            (default: a fresh temp dir)
#   BIN_DIR    dir with prebuilt daedalus +  (default: build fresh; set this to a
#              daedalus-control binaries       version dir of a 0.48.0+ install to
#                                              skip building entirely)
#   BUILD      host | docker | auto          (default: auto — host Go if ≥1.25,
#                                              else Docker)
#   REAL_DATA_DIR  your data dir for the      (default: auto-detected from your
#              `real` phase (dir with           installed daedalus's config.json)
#              projects.json where <project>
#              is registered)

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

REAL_PIDFILE=""   # set by the real phase (a daemon spawned in your real data dir)
kill_pidfile() {
    [[ -f "$1" ]] || return 0
    local pid; pid="$(cat "$1" 2>/dev/null || true)"
    [[ -n "${pid:-}" ]] && kill "$pid" 2>/dev/null || true
}
cleanup() {
    # Kill only the daemons WE spawned (scoped by pidfile), never a pre-existing one.
    kill_pidfile "$DATA_DIR/.daedalus/control.pid"
    [[ -n "$REAL_PIDFILE" ]] && kill_pidfile "$REAL_PIDFILE"
    rm -rf "$WORK"
}
trap cleanup EXIT

# Find the data dir your installed `daedalus` uses (where projects are registered).
# REAL_DATA_DIR overrides. Reads data-dir from the installed config.json.
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

# Go version required by go.mod (host must match or newer; else we build in Docker).
GO_MIN_MINOR=25

host_go_ok() {
    command -v go >/dev/null 2>&1 || return 1
    local v; v="$(go version 2>/dev/null | grep -oE 'go[0-9]+\.[0-9]+' | head -1 | sed 's/go//')"
    [[ -n "$v" ]] || return 1
    local maj="${v%%.*}" min="${v##*.}"
    [[ "$maj" -gt 1 || ( "$maj" -eq 1 && "$min" -ge "$GO_MIN_MINOR" ) ]]
}

# ── Build (or locate) the two binaries into one dir ──────────────────────────
# BIN_DIR override → use as-is. Else: build with the host Go if it's ≥1.25;
# otherwise build inside a golang:1.25 container (like build.sh), so the host's
# Go version doesn't matter. Both binaries land in one dir so EnsureRunning finds
# daedalus-control beside daedalus.
ensure_binaries() {
    if [[ -n "$BIN_DIR" ]]; then
        [[ -x "$BIN_DIR/daedalus" && -x "$BIN_DIR/daedalus-control" ]] \
            || { echo "BIN_DIR=$BIN_DIR must contain daedalus + daedalus-control"; exit 2; }
        info "using pre-built binaries in $BIN_DIR"
        return
    fi
    BIN_DIR="$WORK/bin"; mkdir -p "$BIN_DIR"

    if [[ "${BUILD:-auto}" != docker ]] && host_go_ok; then
        info "building daedalus + daedalus-control with host Go ($(go version | grep -oE 'go[0-9.]+' | head -1))…"
        ( cd "$REPO_ROOT" && go build -o "$BIN_DIR/daedalus" ./cmd/daedalus \
            && go build -o "$BIN_DIR/daedalus-control" ./cmd/daedalus-control ) \
            || { echo "host build failed"; exit 2; }
    elif command -v docker >/dev/null 2>&1; then
        info "host Go missing or <1.$GO_MIN_MINOR — building in golang:1.25-bookworm (Docker)…"
        docker run --rm \
            -v "$REPO_ROOT":/src -v "$BIN_DIR":/out -w /src \
            --user "$(id -u):$(id -g)" -e HOME=/tmp \
            golang:1.25-bookworm \
            sh -c "go build -buildvcs=false -o /out/daedalus ./cmd/daedalus && \
                   go build -buildvcs=false -o /out/daedalus-control ./cmd/daedalus-control" \
            || { echo "docker build failed"; exit 2; }
    else
        echo "Need either Go ≥ 1.$GO_MIN_MINOR or Docker to build, OR set BIN_DIR to a dir"
        echo "containing pre-built daedalus + daedalus-control binaries."
        exit 2
    fi
    [[ -x "$BIN_DIR/daedalus" && -x "$BIN_DIR/daedalus-control" ]] \
        || { echo "build produced no binaries in $BIN_DIR"; exit 2; }
}

# `daedalus` with our isolated data dir. The daemon is auto-spawned from
# BIN_DIR (it sits beside the daedalus binary), inheriting any env we set here.
# NO_COLOR keeps ANSI escapes out of output we grep.
dae() { NO_COLOR=1 DAEDALUS_DATA_DIR="$DATA_DIR" "$BIN_DIR/daedalus" "$@"; }

# Register a project directly in the registry (the normal `daedalus <name> <dir>`
# path would launch Docker, which the fake phase avoids).
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
    ( cd "$dir" && git init -q && git config user.email v@m13.test && git config user.name m13 \
      && echo seed > seed.txt && git add -A && git commit -q -m init )
}

# Query control.db. Prefers the sqlite3 CLI; falls back to python3's sqlite3
# module (more universally present). Returns rows as pipe-joined lines.
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

# ── Phase: fake (no Docker) ──────────────────────────────────────────────────
phase_fake() {
    hdr "fake — daemon + CLI + worktree + reconcile (no Docker)"
    local proj="$WORK/proj-fake"; new_git_repo "$proj"; register demo "$proj"
    local head; head="$(git -C "$proj" rev-parse --short HEAD)"

    # 1. create — captures base_sha == HEAD
    local out; out="$(DAEDALUS_CONTROL_FAKE_RUNNER=success dae task create --project demo --objective 'add dark mode' 2>&1)"
    echo "$out" | grep -q "created task T-1" && pass "create → T-1" || { fail "create"; echo "$out"; }
    echo "$out" | grep -q "$head" && pass "base_sha matches HEAD ($head)" || fail "base_sha != HEAD"

    # 2. dispatch success → candidate + artifact + worktree on branch
    out="$(DAEDALUS_CONTROL_FAKE_RUNNER=success dae task dispatch T-1 2>&1)"
    echo "$out" | grep -q "state candidate" && pass "dispatch(success) → candidate" || { fail "dispatch success"; echo "$out"; }
    [[ "$(db "SELECT state FROM jobs WHERE id='J-1';")" == candidate ]] && pass "job J-1 = candidate" || fail "job state"
    [[ "$(db "SELECT count(*) FROM artifacts WHERE job_id='J-1';")" == 1 ]] && pass "artifact created" || fail "no artifact"
    [[ -d "$DATA_DIR/control/worktrees/J-1" ]] && pass "worktree materialised" || fail "no worktree"
    git -C "$proj" branch --list 'daedalus/T-1/J-1' | grep -q . && pass "branch daedalus/T-1/J-1 exists" || fail "no branch"

    # 3. reconcile-on-boot idempotency: kill the daemon, next call restarts it,
    #    the candidate + worktree must survive untouched.
    local pid; pid="$(cat "$DATA_DIR/.daedalus/control.pid" 2>/dev/null)"
    kill "$pid" 2>/dev/null; sleep 1
    dae task status T-1 >/dev/null 2>&1   # restarts daemon → reconcile runs
    [[ "$(db "SELECT state FROM jobs WHERE id='J-1';")" == candidate ]] && pass "candidate survives daemon restart (reconcile idempotent)" || fail "reconcile disturbed candidate"
    [[ -d "$DATA_DIR/control/worktrees/J-1" ]] && pass "candidate worktree survives restart" || fail "worktree lost on restart"

    # 4. failure path → failed, no artifact, worktree cleaned
    dae task cancel T-1 >/dev/null 2>&1    # free the project (one active task)
    DAEDALUS_CONTROL_FAKE_RUNNER=fail dae task create --project demo --objective 'flaky' >/dev/null 2>&1
    out="$(DAEDALUS_CONTROL_FAKE_RUNNER=fail dae task dispatch T-2 2>&1)"
    echo "$out" | grep -q "state failed" && pass "dispatch(fail) → failed" || { fail "dispatch fail"; echo "$out"; }
    [[ "$(db "SELECT count(*) FROM artifacts WHERE job_id='J-2';")" == 0 ]] && pass "failed job produced NO artifact" || fail "artifact from failed job"
    [[ ! -d "$DATA_DIR/control/worktrees/J-2" ]] && pass "failed job's worktree cleaned" || fail "worktree not cleaned"

    # 5. guardrails
    dae task cancel T-2 >/dev/null 2>&1
    dae task create --project demo --objective a >/dev/null 2>&1
    dae task create --project demo --objective b >/dev/null 2>&1 && fail "second active task allowed" || pass "one-active-task-per-project enforced"
    mkdir -p "$WORK/notgit"; register notgit "$WORK/notgit"
    dae task create --project notgit --objective x >/dev/null 2>&1 && fail "non-git dir accepted" || pass "non-Git project rejected"

    # 6. store shape + event log
    local tables; tables="$(db "SELECT name FROM sqlite_master WHERE type='table' AND name IN ('tasks','jobs','artifacts','events') ORDER BY name;" | tr '\n' ' ')"
    [[ "$tables" == "artifacts jobs tasks events "* || "$tables" == "artifacts events jobs tasks "* ]] \
        && pass "control.db has tasks/jobs/artifacts/events" || fail "missing tables (got: $tables)"
    local nevents; nevents="$(db "SELECT count(*) FROM events;")"
    [[ "${nevents:-0}" -ge 1 ]] && pass "append-only event log populated ($nevents events)" || fail "empty event log"

    # 7. daemon we spawned is alive via its pidfile (scoped — not your real one)
    local dpid; dpid="$(cat "$DATA_DIR/.daedalus/control.pid" 2>/dev/null)"
    if [[ -n "${dpid:-}" ]] && kill -0 "$dpid" 2>/dev/null; then pass "control daemon running (pid $dpid)"; else fail "control daemon not running"; fi
}

# ── Phase: real (Docker) — the CoordinatorRunner seam ────────────────────────
phase_real() {
    hdr "real — actual headless agent in a container (Docker)"
    local project="${1:-}"; local objective="${2:-Print the repo file list to STDOUT and make no changes.}"
    if [[ -z "$project" ]]; then
        echo "  usage: verify-m13.sh real <registered-project> [objective]"; FAIL=$((FAIL+1)); return
    fi
    command -v docker >/dev/null 2>&1 || { echo "  Docker not found — skipping the real phase."; return; }

    # The real phase must use YOUR data dir (where <project> is registered), not
    # the isolated temp one — otherwise the control plane can't see the project.
    local RD; RD="$(detect_real_datadir)" || {
        echo "  ✗ couldn't locate your real data dir (where '$project' is registered)."
        echo "    set it explicitly and re-run, e.g.:"
        echo "      REAL_DATA_DIR=~/.local/share/daedalus/.cache bash scripts/verify-m13.sh real $project"
        echo "    (your data dir is the directory that contains projects.json)"
        FAIL=$((FAIL+1)); return
    }
    info "using your real data dir: $RD"
    # Pre-check the project is actually registered there (clear error before the daemon).
    if ! python3 -c 'import json,sys;d=json.load(open(sys.argv[1]+"/projects.json"));sys.exit(0 if sys.argv[2] in d.get("projects",{}) else 1)' "$RD" "$project" 2>/dev/null; then
        echo "  ✗ project '$project' is not in $RD/projects.json — check the name with 'daedalus list'."
        FAIL=$((FAIL+1)); return
    fi
    info "dispatching a REAL agent (no fake env) against an isolated worktree of '$project'."
    info "needs a built daedalus image + working runner credentials; BLOCKS until the agent exits."
    info "your main checkout is untouched — the job runs on branch daedalus/<task>/<job> in a separate worktree."
    REAL_PIDFILE="$RD/.daedalus/control.pid"   # so cleanup kills the daemon we start there

    local run=( env DAEDALUS_DATA_DIR="$RD" "$BIN_DIR/daedalus" )
    local cout; cout="$("${run[@]}" task create --project "$project" --objective "$objective" 2>&1)"; echo "  $cout"
    local tid; tid="$(echo "$cout" | grep -oE 'T-[0-9]+' | head -1)"
    [[ -z "$tid" ]] && { fail "real: task create failed"; return; }
    info "dispatching $tid (this may take a while)…"
    local dout; dout="$("${run[@]}" task dispatch "$tid" 2>&1)"; echo "  $dout"
    if echo "$dout" | grep -qE "state (candidate|failed|cancelled)"; then
        pass "real runner executed to a terminal state (the CoordinatorRunner seam works)"
        echo "$dout" | grep -q "state candidate" \
            && pass "real job produced a candidate Artifact — verify the commit on its daedalus/* branch" \
            || info "real job did not succeed — inspect: daedalus task status $tid (a non-success is a fine outcome for this smoke; we're proving the path runs)"
    else
        fail "real dispatch did not reach a terminal state — inspect the daemon log at <data-dir>/.daedalus/control.log"
        echo "$dout"
    fi
    info "clean up when done:  daedalus task cancel $tid   (and prune its worktree/branch)"
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
echo "  M13 verification: $PASS passed, $FAIL failed"
echo "──────────────────────────────────────────"
[[ "$FAIL" -eq 0 ]]
