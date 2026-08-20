#!/usr/bin/env bash
# Copyright (C) 2026 Techdelight BV
#
# verify-guild-control.sh — verification for the Guild Master's control-plane
# client (BACKLOG #72: the restricted agent socket is now mounted into the Guild
# Master's container).
#
# The capability has two halves, and only one of them can be proven without
# Docker:
#
#   * the HOST half — the plane offers a restricted socket, the launch mounts it
#     into the Guild Master's container and nowhere else, and a caller on that
#     socket gets agent authority (create allowed, cancel becomes a proposal,
#     self-confirmation refused). All of that runs here, in the `static` phase.
#   * the CONTAINER half — the socket actually arrives inside the container, the
#     entrypoint wires guild-control-mcp on the strength of it, and the agent's
#     uid can open it. That needs a real Docker daemon and a built image: the
#     `real` phase.
#
# Everything runs in an ISOLATED data dir (default: a temp dir), so it never
# touches your real registry, control.db, or a running daedalus-control.
#
# Usage (from the repo root):
#
#   bash scripts/verify-guild-control.sh static      # no Docker
#   bash scripts/verify-guild-control.sh real        # needs Docker + a built image
#   bash scripts/verify-guild-control.sh all
#
# Overrides: DATA_DIR, BIN_DIR, BUILD=0 (skip the go build).

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
BIN_DIR="${BIN_DIR:-$WORK/bin}"
mkdir -p "$DATA_DIR" "$BIN_DIR"

cleanup() {
    local pid; pid="$(cat "$DATA_DIR/.daedalus/control.pid" 2>/dev/null || true)"
    [[ -n "${pid:-}" ]] && kill "$pid" 2>/dev/null
    rm -rf "$WORK"
}
trap cleanup EXIT

HUMAN_SOCK="$DATA_DIR/.daedalus/control.sock"
AGENT_SOCK="$DATA_DIR/.daedalus/control-agent.sock"
CONTAINER_SOCK="/var/run/daedalus/control-agent.sock"

dae() { NO_COLOR=1 DAEDALUS_DATA_DIR="$DATA_DIR" "$BIN_DIR/daedalus" "$@"; }

# curl over a Unix socket. $1 = socket, $2 = METHOD, $3 = path, $4 = body (opt).
# Prints "<status> <body>".
api() {
    local sock="$1" method="$2" path="$3" body="${4:-}"
    if [[ -n "$body" ]]; then
        curl -s -o /tmp/gc-body.$$ -w '%{http_code}' --unix-socket "$sock" \
            -X "$method" -H 'Content-Type: application/json' -d "$body" "http://local$path"
    else
        curl -s -o /tmp/gc-body.$$ -w '%{http_code}' --unix-socket "$sock" \
            -X "$method" "http://local$path"
    fi
    printf ' '; cat /tmp/gc-body.$$; rm -f /tmp/gc-body.$$
}

build() {
    [[ "${BUILD:-1}" == "0" ]] && { info "BUILD=0 — using $BIN_DIR as-is"; return; }
    hdr "build"
    ( cd "$REPO_ROOT" && CGO_ENABLED=0 go build -o "$BIN_DIR/daedalus" ./cmd/daedalus \
        && CGO_ENABLED=0 go build -o "$BIN_DIR/daedalus-control" ./cmd/daedalus-control \
        && CGO_ENABLED=0 go build -o "$BIN_DIR/guild-control-mcp" ./cmd/guild-control-mcp ) \
        && pass "built daedalus, daedalus-control, guild-control-mcp" \
        || { fail "build"; exit 1; }
}

new_git_repo() {
    local dir="$1"; mkdir -p "$dir"
    ( cd "$dir" && git init -q && git config user.email v@guild.test && git config user.name guild \
      && echo seed > seed.txt && git add -A && git commit -q -m init )
}

register() {
    python3 - "$DATA_DIR/projects.json" "$1" "$2" <<'PY'
import json,os,sys
path,name,dir=sys.argv[1:4]
reg=json.load(open(path)) if os.path.exists(path) else {"version":3,"projects":{}}
reg.setdefault("projects",{})[name]={"directory":dir,"target":"dev"}
json.dump(reg,open(path,"w"))
PY
}

# ── Phase: static (no Docker) ────────────────────────────────────────────────
phase_static() {
    hdr "contracts — the mount is refused in every unsafe shape"
    ( cd "$REPO_ROOT" && go test ./core/ -run TestGuildControlSocketMount -count=1 >/dev/null 2>&1 ) \
        && pass "core: guild-master only · a real socket only · never control.sock" \
        || fail "core GuildControlSocketMount tests"
    ( cd "$REPO_ROOT" && go test ./internal/coordinator/ -run 'TestStart_(GuildMaster|NormalProject)' -count=1 >/dev/null 2>&1 ) \
        && pass "coordinator: mounted for the Guild Master, never for an ordinary project" \
        || fail "coordinator launch-args tests"

    hdr "in-container gate + distribution"
    grep -q 'DAEDALUS_CONTROL_AGENT_SOCKET:-/var/run/daedalus/control-agent.sock' "$REPO_ROOT/entrypoint.sh" \
        && pass "entrypoint.sh gates guild-control on the socket being present" \
        || fail "entrypoint.sh socket gate missing"
    grep -q 'guild-control-mcp' "$REPO_ROOT/Dockerfile" && grep -q 'guild-control-mcp' "$REPO_ROOT/build.sh" \
        && grep -q 'guild-control-mcp' "$REPO_ROOT/scripts/package-release.sh" \
        && pass "guild-control-mcp is in Dockerfile, build.sh and package-release.sh" \
        || fail "guild-control-mcp missing from the distribution chain"

    hdr "the plane's two sockets"
    local proj="$WORK/proj"; new_git_repo "$proj"; register demo "$proj"
    dae task list >/dev/null 2>&1   # auto-spawns daedalus-control
    sleep 1
    [[ -S "$HUMAN_SOCK" ]] && pass "human socket exists: $HUMAN_SOCK" || fail "no human socket"
    [[ -S "$AGENT_SOCK" ]] && pass "agent socket exists: $AGENT_SOCK" || fail "no agent socket"
    local mode; mode="$(stat -c '%a' "$AGENT_SOCK" 2>/dev/null)"
    [[ "$mode" == "660" ]] && pass "agent socket is mode 0660" || fail "agent socket mode $mode (want 660)"
    info "container uid must be able to open it — see 'real', and note the"
    info "build-uid warning the coordinator already logs on a uid mismatch"

    hdr "caller class — what the Guild Master will and will not be able to do"
    local r
    r="$(api "$AGENT_SOCK" POST /tasks '{"project":"demo","objective":"agent-created task"}')"
    [[ "$r" == 201* ]] && pass "agent: create_task ALLOWED (bounded by policy) → ${r:0:3}" \
                       || fail "agent create_task: $r"
    r="$(api "$AGENT_SOCK" GET /tasks)"
    [[ "$r" == 200* ]] && pass "agent: list_tasks ALLOWED" || fail "agent list_tasks: $r"
    r="$(api "$AGENT_SOCK" DELETE /tasks/T-1)"
    if [[ "$r" == 4* && "$r" == *proposal* ]]; then
        pass "agent: cancel becomes a PROPOSAL, loudly (not a silent success)"
    else
        fail "agent cancel should have been recorded as a proposal: $r"
    fi
    [[ "$(dae task status T-1 2>&1 | grep -c 'cancelled')" == "0" ]] \
        && pass "…and the task was NOT cancelled by the agent" || fail "agent cancelled a task"
    r="$(api "$AGENT_SOCK" POST /proposals/P-1/confirm)"
    [[ "$r" == 4* ]] && pass "agent: confirming its OWN proposal REFUSED → ${r:0:3}" \
                     || fail "agent confirmed its own proposal: $r"
    r="$(api "$HUMAN_SOCK" POST /proposals/P-1/confirm)"
    [[ "$r" == 200* ]] && pass "human: confirming the proposal executes it" || fail "human confirm: $r"
    dae task status T-1 2>&1 | grep -q 'cancelled' \
        && pass "…and only then is the task cancelled" || fail "confirmed proposal did not execute"

    # Programmes (#82). The Guild Master's whole job is noticing what projects
    # have in common; until these worked it could not see a programme at all —
    # the operations were tiered and reachable from nowhere, and a confirmed
    # proposal failed closed on "an operation this plane cannot execute".
    r="$(api "$AGENT_SOCK" GET /programmes)"
    [[ "$r" == 200* ]] && pass "agent: list_programmes ALLOWED (noticing is the job)" \
        || fail "agent list_programmes: $r"
    r="$(api "$AGENT_SOCK" POST /programmes '{"name":"fluency","description":"get conversational: by spring"}')"
    if [[ "$r" == 422* && "$r" == *proposal_recorded* ]]; then
        pass "agent: forming a programme becomes a PROPOSAL"
    else
        fail "agent form_programme: $r"
    fi
    r="$(api "$HUMAN_SOCK" GET /programmes)"
    [[ "$r" != *fluency* ]] && pass "…and no programme was formed by the agent" \
        || fail "an agent formed a programme directly"
    r="$(api "$HUMAN_SOCK" POST /proposals/P-2/confirm)"
    [[ "$r" == 200* ]] && pass "human: confirming the programme proposal executes it" \
        || fail "human confirm of P-2: $r"
    r="$(api "$HUMAN_SOCK" GET /programmes)"
    if [[ "$r" == *fluency* && "$r" == *"get conversational: by spring"* ]]; then
        # The colon is the point: an encoding that split on a separator would
        # have truncated the description, which is the field a task's rationale
        # is later judged against.
        pass "…and the programme exists with its description intact through the round trip"
    else
        fail "programme missing or its description was mangled: $r"
    fi

    hdr "what remains host-only"
    info "the socket arriving INSIDE the container, the entrypoint wiring"
    info "guild-control on it, and the agent uid opening it — run: real"
}

# ── Phase: real (needs Docker + a built image) ───────────────────────────────
phase_real() {
    hdr "real — the container half (Docker)"
    if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
        fail "no usable Docker daemon — this phase cannot run here"
        return
    fi
    local name="guild-master" cname="claude-run-guild-master"

    info "launch it yourself (this phase inspects a RUNNING Guild Master):"
    info "    daedalus guild-master        # starts the plane, then the container"
    if ! docker inspect "$cname" >/dev/null 2>&1; then
        fail "container $cname is not running — launch it, then re-run: real"
        return
    fi

    # 1. The mount is present, and it is the AGENT socket.
    local mounts; mounts="$(docker inspect -f '{{range .Mounts}}{{.Source}}:{{.Destination}}{{"\n"}}{{end}}' "$cname")"
    grep -q "control-agent.sock:$CONTAINER_SOCK" <<<"$mounts" \
        && pass "control-agent.sock is mounted at $CONTAINER_SOCK" \
        || fail "the restricted socket is NOT mounted into $cname"
    grep -q "control.sock:" <<<"$mounts" \
        && fail "the HUMAN control.sock is mounted into the container — authority leak" \
        || pass "the human control.sock is NOT mounted (correct)"

    # 2. It is a socket inside, and the agent's uid can open it.
    docker exec "$cname" test -S "$CONTAINER_SOCK" \
        && pass "it is a socket inside the container" \
        || fail "no socket at $CONTAINER_SOCK inside the container"
    docker exec "$cname" node -e '
      const net=require("net");
      const s=net.connect("'"$CONTAINER_SOCK"'");
      s.on("connect",()=>{s.end();process.exit(0)});
      s.on("error",e=>{console.error(e.message);process.exit(1)});' \
        && pass "the container user can CONNECT to it (uid/permission OK)" \
        || fail "the container user cannot open the socket — check host uid vs container uid 1000"

    # 3. The entrypoint wired the tool on the strength of it.
    docker exec "$cname" sh -c 'jq -e ".mcpServers[\"guild-control\"]" ~/.claude.json >/dev/null 2>&1 \
        || jq -e ".mcpServers[\"guild-control\"]" /home/claude/.claude.json >/dev/null 2>&1' \
        && pass "guild-control is registered in the agent's MCP config" \
        || fail "guild-control was not wired (check the entrypoint gate and the socket)"
    docker exec "$cname" test -x /usr/local/bin/guild-control-mcp \
        && pass "guild-control-mcp is on the image" || fail "guild-control-mcp missing from the image"

    # 4. An ordinary project must get NONE of this.
    info "now confirm the negative: launch any ordinary project and check"
    info "    docker inspect claude-run-<project> | grep control-agent   # expect nothing"
}

main() {
    local phase="${1:-static}"
    build
    case "$phase" in
        static) phase_static ;;
        real)   phase_real ;;
        all)    phase_static; phase_real ;;
        *) echo "usage: verify-guild-control.sh [static|real|all]"; exit 2 ;;
    esac
    echo
    echo "── summary ──"
    echo "  $PASS passed, $FAIL failed"
    [[ "$FAIL" -eq 0 ]]
}

main "$@"
