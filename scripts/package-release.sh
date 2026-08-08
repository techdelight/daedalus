#!/usr/bin/env bash
# Copyright (C) 2026 Techdelight BV
#
# package-release.sh — the single source of truth for release packaging.
#
# Both the GitHub release workflow (.github/workflows/release.yml and
# dev-release.yml) and the local simulation (scripts/test-release-bundle.sh)
# invoke THIS script, so the packaging logic is genuinely exercised here even
# though the workflows themselves only run on GitHub.
#
# Given a staging directory that holds every per-platform binary plus the
# shared runtime files, it produces one self-contained archive per platform:
#
#   daedalus-<os>-<arch>.tar.gz
#
# Each archive contains exactly the set setup.sh installs, with the platform
# binaries renamed to their install names, plus a SHA256SUMS.txt over all
# archives.
#
# Archive layout: FLAT (no top-level directory). install.sh extracts an
# archive straight into WORK_DIR, which is the shape setup.sh expects.
#
# Usage:
#   scripts/package-release.sh --staging <dir> --out <dir> --version <ver>
#
#   --staging <dir>   Directory containing the built per-platform binaries
#                     (daedalus-<os>-<arch>, skill-catalog-mcp-<os>-<arch>,
#                     project-mgmt-mcp-<os>-<arch>, daedalus-coordinator-<os>-<arch>,
#                     daedalus-runner-<os>-<arch>) and the shared runtime files
#                     (claude.json, config.json, docker-compose.yml, Dockerfile,
#                     entrypoint.sh, settings.json, logo.txt, setup.sh,
#                     wsl2-network.bat).
#   --out <dir>       Output directory for the archives + SHA256SUMS.txt.
#   --version <ver>   Version string baked into the packaged config.json
#                     (e.g. "0.43.0" or "dev_20260806"). setup.sh reads it from
#                     config.json at install time, so install.sh no longer needs
#                     to patch it.

set -euo pipefail

# ── Platforms to package ─────────────────────────────────────────────────────
PLATFORMS=(
    linux-amd64
    linux-arm64
    darwin-amd64
    darwin-arm64
)

# ── Per-platform binaries: staging name (without suffix) -> install name ──────
# The staged file is "<binary>-<os>-<arch>"; inside the archive it is renamed
# to "<install name>" (== the staging base here, they happen to match).
BINARIES=(
    daedalus
    skill-catalog-mcp
    project-mgmt-mcp
    guild-mcp
    daedalus-coordinator
    daedalus-runner
)

# ── Shared runtime files (identical across platforms) ────────────────────────
# Exactly the set setup.sh installs (its RUNTIME_FILES + setup.sh itself) plus
# wsl2-network.bat, matching the pre-bundle release-asset set.
RUNTIME_FILES=(
    claude.json
    config.json
    docker-compose.yml
    Dockerfile
    entrypoint.sh
    settings.json
    logo.txt
    setup.sh
    wsl2-network.bat
)

# Fixed timestamp for reproducible archives (UTC).
SOURCE_EPOCH="2000-01-01 00:00:00Z"

STAGING=""
OUT=""
VERSION=""
PLATFORMS_OVERRIDE=""

usage() {
    cat <<EOF
Usage: $0 --staging <dir> --out <dir> --version <ver> [--platforms <list>]

Packages the built binaries + runtime files in <staging> into one
daedalus-<os>-<arch>.tar.gz per platform in <out>, plus SHA256SUMS.txt.

  --platforms <list>  Space/comma-separated subset of platforms to package
                      (default: all four -- ${PLATFORMS[*]}). Used by the local
                      simulation to package only the host platform.
EOF
    exit "${1:-0}"
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --staging)   [[ $# -lt 2 ]] && { echo "Error: --staging requires an argument." >&2; exit 1; }; STAGING="$2"; shift 2 ;;
        --out)       [[ $# -lt 2 ]] && { echo "Error: --out requires an argument." >&2; exit 1; };     OUT="$2";     shift 2 ;;
        --version)   [[ $# -lt 2 ]] && { echo "Error: --version requires an argument." >&2; exit 1; }; VERSION="$2"; shift 2 ;;
        --platforms) [[ $# -lt 2 ]] && { echo "Error: --platforms requires an argument." >&2; exit 1; }; PLATFORMS_OVERRIDE="$2"; shift 2 ;;
        --help|-h) usage 0 ;;
        *) echo "Error: unknown option '$1'." >&2; usage 1 ;;
    esac
done

# Apply a platform subset override (comma or whitespace separated).
if [[ -n "$PLATFORMS_OVERRIDE" ]]; then
    read -r -a PLATFORMS <<< "${PLATFORMS_OVERRIDE//,/ }"
fi

[[ -z "$STAGING" ]] && { echo "Error: --staging is required." >&2; exit 1; }
[[ -z "$OUT" ]]     && { echo "Error: --out is required." >&2; exit 1; }
[[ -z "$VERSION" ]] && { echo "Error: --version is required." >&2; exit 1; }
[[ ! -d "$STAGING" ]] && { echo "Error: staging directory '$STAGING' does not exist." >&2; exit 1; }

# ── Portable sed -i (BSD vs GNU) ─────────────────────────────────────────────
sed_inplace() {
    if sed --version >/dev/null 2>&1; then
        sed -i "$@"
    else
        sed -i '' "$@"
    fi
}

# ── Portable sha256 over a single file → bare hash ───────────────────────────
sha256_of() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    else
        echo "Error: neither sha256sum nor shasum is available." >&2
        exit 1
    fi
}

# ── Reproducible tar.gz from a directory ─────────────────────────────────────
# Files are passed as an explicit, sorted, relative list so ordering is stable.
# GNU tar gets --sort/--mtime/--owner/--group for byte-for-byte reproducibility;
# BSD tar falls back to a fixed mtime applied via touch + a sorted list. gzip -n
# drops the timestamp/name from the gzip header in both cases.
make_archive() {
    local outfile="$1" srcdir="$2"; shift 2
    local files=("$@")

    # Fixed mtime on every staged file (portable; also covers BSD tar).
    touch -t 200001010000.00 -- "${files[@]/#/$srcdir/}"

    if tar --version 2>/dev/null | grep -qi 'gnu tar'; then
        tar --sort=name \
            --mtime="$SOURCE_EPOCH" \
            --owner=0 --group=0 --numeric-owner \
            -C "$srcdir" -cf - -- "${files[@]}" | gzip -n > "$outfile"
    else
        tar -C "$srcdir" -cf - -- "${files[@]}" | gzip -n > "$outfile"
    fi
}

mkdir -p "$OUT"
# Absolute output path so the -C into the staging dir does not confuse it.
OUT="$(cd "$OUT" && pwd)"

echo "Packaging release archives (version: $VERSION)"
echo "  staging: $STAGING"
echo "  out:     $OUT"

ARCHIVE_NAMES=()

for platform in "${PLATFORMS[@]}"; do
    os="${platform%-*}"
    arch="${platform#*-}"

    # Assemble this platform's payload in an isolated flat directory.
    payload="$(mktemp -d)"

    # 1) Per-platform binaries, renamed to their install names.
    for bin in "${BINARIES[@]}"; do
        src="$STAGING/${bin}-${platform}"
        if [[ ! -f "$src" ]]; then
            echo "Error: missing binary '$src' for platform $platform." >&2
            rm -rf "$payload"
            exit 1
        fi
        cp "$src" "$payload/$bin"
        chmod 755 "$payload/$bin"
    done

    # 2) Shared runtime files.
    for f in "${RUNTIME_FILES[@]}"; do
        src="$STAGING/$f"
        if [[ ! -f "$src" ]]; then
            echo "Error: missing runtime file '$src'." >&2
            rm -rf "$payload"
            exit 1
        fi
        cp "$src" "$payload/$f"
    done
    chmod 755 "$payload/setup.sh"

    # 3) Bake the version into the packaged config.json. The template ships
    #    "version": "" — setup.sh reads it, so this is what ends up recorded in
    #    the installed config. (This replaces the old install.sh sed patch.)
    sed_inplace 's/"version": *""/"version": "'"$VERSION"'"/' "$payload/config.json"

    # 4) Build the archive from a sorted file list (flat layout).
    archive_name="daedalus-${platform}.tar.gz"
    sorted_files=()
    while IFS= read -r line; do
        sorted_files+=("$line")
    done < <(cd "$payload" && ls -1 | LC_ALL=C sort)
    make_archive "$OUT/$archive_name" "$payload" "${sorted_files[@]}"
    ARCHIVE_NAMES+=("$archive_name")
    echo "  built $archive_name (${#sorted_files[@]} files)"

    rm -rf "$payload"
done

# ── SHA256SUMS.txt over all archives (bare names, directory-independent) ──────
: > "$OUT/SHA256SUMS.txt"
for name in "${ARCHIVE_NAMES[@]}"; do
    printf '%s  %s\n' "$(sha256_of "$OUT/$name")" "$name" >> "$OUT/SHA256SUMS.txt"
done

echo "  wrote SHA256SUMS.txt (${#ARCHIVE_NAMES[@]} archives)"
echo "Done: ${#ARCHIVE_NAMES[@]} archives + SHA256SUMS.txt in $OUT"
