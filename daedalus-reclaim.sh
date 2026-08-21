#!/usr/bin/env bash
# Copyright (C) 2026 Techdelight BV
#
# daedalus-reclaim.sh — report, and optionally remove, what daedalus has left behind.
#
# Three leaks were fixed in the tool itself (pinned compose project name,
# superseded-image removal, automatic version pruning), but a fix only stops
# accumulation — it does not clear what has already piled up. This clears it.
#
# The measurement that prompted it, on one real host: 21 orphaned compose
# networks, which exhausted Docker's default address pools (~31 bridge networks)
# so that NO project could start, with an error naming a network and saying
# nothing about daedalus.
#
# IT REPORTS BY DEFAULT AND DELETES NOTHING WITHOUT --apply. Every section says
# what it would remove before it removes anything, because "reclaim disk space"
# and "delete the wrong thing" are one typo apart.

set -euo pipefail

APPLY=false
KEEP=3
LOG_DAYS=""
DO_IMAGES=false
DATA_DIR=""

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

usage() {
    cat <<'EOF'
Usage: daedalus-reclaim.sh [options]

Reports what daedalus has left behind on this machine, and removes it when asked.
Nothing is deleted unless --apply is given.

Options:
  --apply                 actually remove what is reported (default: report only)
  --keep N                installed versions to keep (default: 3)
  --logs-older-than DAYS  also reclaim job/review logs older than DAYS days
  --images                also reclaim DANGLING docker images — see the warning
                          the report prints; this one is not daedalus-scoped
  --data-dir PATH         daedalus data dir (default: read from config.json)
  -h, --help              this text

Reclaimed by default (with --apply):
  * orphaned compose networks from superseded installs, with nothing attached
  * leftover job/review homes whose container is no longer running
  * installed versions beyond --keep, via `daedalus version prune`

Opt-in, because they cannot be attributed or are a retention decision:
  * dangling docker images   (--images)
  * job and review logs      (--logs-older-than DAYS)
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --apply) APPLY=true; shift ;;
        --keep) KEEP="${2:?--keep requires a number}"; shift 2 ;;
        --logs-older-than) LOG_DAYS="${2:?--logs-older-than requires a number of days}"; shift 2 ;;
        --images) DO_IMAGES=true; shift ;;
        --data-dir) DATA_DIR="${2:?--data-dir requires a path}"; shift 2 ;;
        -h|--help) usage; exit 0 ;;
        *) echo "Unknown option: $1" >&2; echo "Try --help." >&2; exit 2 ;;
    esac
done

for n in "$KEEP" ${LOG_DAYS:+"$LOG_DAYS"}; do
    [[ "$n" =~ ^[0-9]+$ ]] || { echo "Error: expected a non-negative number, got '$n'" >&2; exit 2; }
done

# ── Locating things ──────────────────────────────────────────────────────────

# The daedalus binary: on PATH, else beside this script (which is where the
# installer puts both).
DAEDALUS=""
if command -v daedalus >/dev/null 2>&1; then
    DAEDALUS="$(command -v daedalus)"
elif [[ -x "$SELF_DIR/daedalus" ]]; then
    DAEDALUS="$SELF_DIR/daedalus"
fi

# The data dir: --data-dir wins, then config.json beside this script, then the
# documented default. Deliberately not a JSON parser — one field, one line.
if [[ -z "$DATA_DIR" && -f "$SELF_DIR/config.json" ]]; then
    DATA_DIR="$(sed -n 's/.*"data-dir"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$SELF_DIR/config.json" | head -1)"
fi
[[ -n "$DATA_DIR" ]] || DATA_DIR="$HOME/.local/share/daedalus/.cache"

HAVE_DOCKER=false
command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1 && HAVE_DOCKER=true

# ── Output helpers ───────────────────────────────────────────────────────────

if [[ -t 1 ]]; then
    B=$'\033[1m'; DIM=$'\033[2m'; Y=$'\033[33m'; G=$'\033[32m'; R=$'\033[0m'
else
    B=""; DIM=""; Y=""; G=""; R=""
fi

section() { printf '\n%s%s%s\n' "$B" "$1" "$R"; }
note()    { printf '  %s%s%s\n' "$DIM" "$1" "$R"; }
item()    { printf '    %s\n' "$1"; }
warn()    { printf '  %s%s%s\n' "$Y" "$1" "$R"; }

section "daedalus reclaim"
note "data dir: $DATA_DIR"
if [[ "$APPLY" == true ]]; then
    warn "--apply given: this WILL remove what is listed below."
else
    note "report only — re-run with --apply to remove any of this"
fi
$HAVE_DOCKER || warn "docker is not available; skipping every docker section"

# ── 1. Orphaned compose networks ─────────────────────────────────────────────
#
# Before the project name was pinned, compose named the project after the
# directory holding the compose file — the VERSIONED install dir — so every
# install minted a new project and a new `<project>_default` network that nothing
# removed. Those are the orphans: a compose network whose project looks like an
# install version, with nothing attached to it.
#
# The live project (`daedalus`) is left alone even when idle: removing it gains
# nothing, because the next run recreates it.

section "1. Orphaned compose networks"
if [[ "$HAVE_DOCKER" == true ]]; then
    NETS=()
    while IFS= read -r name; do
        [[ -n "$name" ]] || continue
        # project|attached-container-count
        meta="$(docker network inspect "$name" \
            --format '{{index .Labels "com.docker.compose.project"}}|{{len .Containers}}' 2>/dev/null || true)"
        project="${meta%%|*}"
        attached="${meta##*|}"
        [[ -n "$project" ]] || continue
        [[ "$project" == "daedalus" ]] && continue          # the live one
        [[ "$attached" == "0" ]] || continue                # something is using it
        # A daedalus install version: dev_<timestamp>, a semver, or "unknown"
        # (setup.sh's fallback when config.json carries no version).
        if [[ "$project" =~ ^dev_[0-9]+$ || "$project" =~ ^[0-9]+\.[0-9]+\.[0-9]+ || "$project" == "unknown" ]]; then
            NETS+=("$name")
        fi
    done < <(docker network ls --filter 'label=com.docker.compose.project' --format '{{.Name}}' 2>/dev/null || true)

    if [[ ${#NETS[@]} -eq 0 ]]; then
        note "none"
    else
        note "${#NETS[@]} orphaned network(s) from superseded installs:"
        for n in "${NETS[@]}"; do item "$n"; done
        if [[ "$APPLY" == true ]]; then
            for n in "${NETS[@]}"; do
                docker network rm "$n" >/dev/null 2>&1 \
                    || warn "could not remove $n (in use?) — left in place"
            done
            printf '  %sremoved %d network(s)%s\n' "$G" "${#NETS[@]}" "$R"
        fi
    fi
else
    note "skipped — no docker"
fi

# ── 2. Leftover job and review homes ─────────────────────────────────────────
#
# A Job runs under a throwaway project (`daedalus-job-<id>`), and its home under
# the data dir is removed with its registry entry when the Job ends. A crashed
# daemon, or a deregistration that failed, leaves the directory — and it holds a
# COPY OF THE PROJECT'S CREDENTIALS, so it is worth clearing rather than merely
# worth the disk.
#
# A home whose container is still running is skipped: that is a live Job.

section "2. Leftover job/review homes"
HOMES=()
if [[ -d "$DATA_DIR" ]]; then
    while IFS= read -r dir; do
        [[ -n "$dir" ]] || continue
        base="$(basename "$dir")"
        if [[ "$HAVE_DOCKER" == true ]] && docker ps --format '{{.Names}}' 2>/dev/null | grep -qx "claude-run-$base"; then
            note "skipping $base — its container is running"
            continue
        fi
        HOMES+=("$dir")
    done < <(find "$DATA_DIR" -maxdepth 1 -type d \( -name 'daedalus-job-*' -o -name 'daedalus-review-*' \) 2>/dev/null | sort)
fi
if [[ ${#HOMES[@]} -eq 0 ]]; then
    note "none"
else
    note "${#HOMES[@]} leftover home(s) — these can contain copied credentials:"
    for h in "${HOMES[@]}"; do item "$(basename "$h")"; done
    if [[ "$APPLY" == true ]]; then
        for h in "${HOMES[@]}"; do rm -rf "$h"; done
        printf '  %sremoved %d home(s)%s\n' "$G" "${#HOMES[@]}" "$R"
    fi
fi

# ── 3. Installed versions ────────────────────────────────────────────────────
#
# Delegated to `daedalus version prune`, which protects the current version plus
# the most recent few — reimplementing that here would be a second, untested copy
# of the one rule that must not be got wrong.

section "3. Installed versions"
if [[ -z "$DAEDALUS" ]]; then
    note "daedalus binary not found on PATH or beside this script — skipping"
elif [[ "$APPLY" == true ]]; then
    "$DAEDALUS" version prune --keep "$KEEP" 2>&1 | sed 's/^/  /' || warn "version prune failed"
else
    note "installed versions (keeping current + $KEEP on --apply):"
    "$DAEDALUS" version list 2>&1 | sed 's/^/    /' || note "could not list versions"
fi

# ── 4. Dangling images (opt-in) ──────────────────────────────────────────────
#
# Opt-in, and the reason is honest rather than cautious: once an image is
# untagged there is nothing left on it identifying who built it. daedalus cannot
# prove which of these are its own, so it does not claim to — this sweep is
# machine-wide, and that is the operator's call to make with the list in view.
#
# The tool itself stays scoped: `Build` removes exactly the image it superseded,
# never anyone else's.

section "4. Dangling images"
if [[ "$HAVE_DOCKER" == true ]]; then
    mapfile -t IMGS < <(docker images -f dangling=true --format '{{.ID}} {{.Size}}' 2>/dev/null || true)
    if [[ ${#IMGS[@]} -eq 0 ]]; then
        note "none"
    elif [[ "$DO_IMAGES" != true ]]; then
        note "${#IMGS[@]} dangling image(s) on this machine:"
        for i in "${IMGS[@]}"; do item "$i"; done
        warn "NOT daedalus-scoped: an untagged image carries nothing saying who built it,"
        warn "so some of these may belong to other tools. Re-run with --images --apply"
        warn "to remove them, once you are happy with the list above."
    else
        note "${#IMGS[@]} dangling image(s):"
        for i in "${IMGS[@]}"; do item "$i"; done
        if [[ "$APPLY" == true ]]; then
            removed=0
            for i in "${IMGS[@]}"; do
                # No -f: an image still referenced by a tag or a container makes
                # docker refuse, which is the answer we want.
                if docker rmi "${i%% *}" >/dev/null 2>&1; then removed=$((removed + 1)); fi
            done
            printf '  %sremoved %d image(s); %d still referenced and left alone%s\n' \
                "$G" "$removed" "$(( ${#IMGS[@]} - removed ))" "$R"
        fi
    fi
else
    note "skipped — no docker"
fi

# ── 5. Job and review logs (opt-in) ──────────────────────────────────────────
#
# Opt-in because retention is a decision, not a default: a job log is the only
# account of what an agent actually did, and the age at which that stops being
# worth keeping is the operator's to choose. (Backlog #77(c).)

section "5. Job and review logs"
if [[ -z "$LOG_DAYS" ]]; then
    total=0
    for d in "$DATA_DIR/.daedalus/jobs" "$DATA_DIR/.daedalus/reviews"; do
        [[ -d "$d" ]] || continue
        n="$(find "$d" -maxdepth 1 -name '*.log' 2>/dev/null | wc -l | tr -d ' ')"
        total=$((total + n))
    done
    if [[ "$total" -eq 0 ]]; then
        note "none"
    else
        note "$total log file(s) kept, with no retention policy"
        note "pass --logs-older-than DAYS to reclaim the older ones"
    fi
else
    OLD=()
    for d in "$DATA_DIR/.daedalus/jobs" "$DATA_DIR/.daedalus/reviews"; do
        [[ -d "$d" ]] || continue
        while IFS= read -r f; do [[ -n "$f" ]] && OLD+=("$f"); done \
            < <(find "$d" -maxdepth 1 -name '*.log' -mtime "+$LOG_DAYS" 2>/dev/null | sort)
    done
    if [[ ${#OLD[@]} -eq 0 ]]; then
        note "no logs older than $LOG_DAYS day(s)"
    else
        note "${#OLD[@]} log file(s) older than $LOG_DAYS day(s):"
        for f in "${OLD[@]}"; do item "${f#"$DATA_DIR/.daedalus/"}"; done
        if [[ "$APPLY" == true ]]; then
            for f in "${OLD[@]}"; do rm -f "$f"; done
            printf '  %sremoved %d log file(s)%s\n' "$G" "${#OLD[@]}" "$R"
        fi
    fi
fi

# ── Summary ──────────────────────────────────────────────────────────────────

echo ""
if [[ "$APPLY" == true ]]; then
    echo "Done."
else
    echo "Nothing was removed. Re-run with --apply to act on the above:"
    echo "    $0 --apply"
fi
echo ""
