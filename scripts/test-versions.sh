#!/usr/bin/env bash
# Copyright (C) 2026 Techdelight BV
#
# test-versions.sh — local end-to-end proof of side-by-side versioned installs
# with switch / rollback / prune, plus the flat->versioned migration.
#
# It reuses Sprint 48's packaging + DAEDALUS_ARCHIVE_DIR install path (no GitHub,
# no Docker): build the host binaries once, package several versions, install
# them one after another into a throwaway prefix, then drive the real
# `daedalus version` subcommand and assert the current/previous symlinks and the
# pruned tree.
#
# Usage: bash scripts/test-versions.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PACKAGE_SH="$SCRIPT_DIR/package-release.sh"
INSTALL_SH="$REPO_ROOT/install.sh"

PASS=0
FAIL=0
pass() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL + 1)); }

check_dir()     { if [ -d "$2" ]; then pass "$1"; else fail "$1 (missing dir: $2)"; fi; }
check_no_dir()  { if [ ! -d "$2" ]; then pass "$1"; else fail "$1 (dir should be gone: $2)"; fi; }
check_link()    { local a; a="$(readlink "$2" 2>/dev/null || true)"; if [ "$a" = "$3" ]; then pass "$1"; else fail "$1 (link $2 -> '$a', want '$3')"; fi; }
check_contains() { if printf '%s' "$2" | grep -qF "$3"; then pass "$1"; else fail "$1 (output missing '$3')"; fi; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

STAGING="$TMP/staging"
FAKE_HOME="$TMP/home"
BIN="$FAKE_HOME/.local/bin"
PREFIX="$FAKE_HOME/.local/share/daedalus"
mkdir -p "$STAGING" "$BIN"

GOOS="$(go env GOOS)"; GOARCH="$(go env GOARCH)"
PLATFORM="${GOOS}-${GOARCH}"
ARCHIVE_NAME="daedalus-${PLATFORM}.tar.gz"
echo "Host platform: $PLATFORM"

# ── Build host binaries once ─────────────────────────────────────────────────
echo ""
echo "[build] Compiling host binaries into staging..."
( cd "$REPO_ROOT"
  go build -o "$STAGING/daedalus-${PLATFORM}"              ./cmd/daedalus
  go build -o "$STAGING/skill-catalog-mcp-${PLATFORM}"     ./cmd/skill-catalog-mcp
  go build -o "$STAGING/project-mgmt-mcp-${PLATFORM}"      ./cmd/project-mgmt-mcp
  go build -o "$STAGING/daedalus-coordinator-${PLATFORM}"  ./cmd/daedalus-coordinator
  go build -o "$STAGING/daedalus-runner-${PLATFORM}"       ./cmd/daedalus-runner
)
cp "$REPO_ROOT/claude.json" "$REPO_ROOT/config.json" "$REPO_ROOT/docker-compose.yml" \
   "$REPO_ROOT/Dockerfile" "$REPO_ROOT/entrypoint.sh" "$REPO_ROOT/settings.json" \
   "$REPO_ROOT/logo.txt" "$REPO_ROOT/setup.sh" "$STAGING/"
cp "$REPO_ROOT/scripts/wsl2-network.bat" "$STAGING/"
echo "  done."

# Package + install a version into $PREFIX via the real install.sh path.
install_version() {
    local ver="$1"
    local dist="$TMP/dist-$ver"
    mkdir -p "$dist"
    bash "$PACKAGE_SH" --staging "$STAGING" --out "$dist" --version "$ver" --platforms "$PLATFORM" >/dev/null
    HOME="$FAKE_HOME" DAEDALUS_ARCHIVE_DIR="$dist" \
        bash "$INSTALL_SH" --prefix "$PREFIX" --link-name daedalus >/dev/null
}

# Run the installed CLI through its PATH symlink, no color, stderr dropped.
cli() {
    NO_COLOR=1 HOME="$FAKE_HOME" DAEDALUS_BIN_DIR="$BIN" "$BIN/daedalus" "$@" 2>/dev/null
}

# ── 1. Side-by-side installs ─────────────────────────────────────────────────
echo ""
echo "[1] Installing 1.0.0, 1.1.0, 1.2.0 side by side..."
install_version "1.0.0"
install_version "1.1.0"
install_version "1.2.0"

check_dir  "versions/1.0.0 present" "$PREFIX/versions/1.0.0"
check_dir  "versions/1.1.0 present" "$PREFIX/versions/1.1.0"
check_dir  "versions/1.2.0 present" "$PREFIX/versions/1.2.0"
check_link "current -> versions/1.2.0" "$PREFIX/current" "versions/1.2.0"
check_link "previous -> versions/1.1.0" "$PREFIX/previous" "versions/1.1.0"
check_link "PATH symlink -> current/daedalus" "$BIN/daedalus" "$PREFIX/current/daedalus"

# ── 2. version list ──────────────────────────────────────────────────────────
echo ""
echo "[2] daedalus version list"
LIST_OUT="$(cli version list)"
printf '%s\n' "$LIST_OUT" | sed 's/^/    /'
check_contains "list shows 1.0.0" "$LIST_OUT" "1.0.0"
check_contains "list shows 1.1.0" "$LIST_OUT" "1.1.0"
check_contains "list shows 1.2.0" "$LIST_OUT" "1.2.0"
check_contains "list marks a current version" "$LIST_OUT" "(current)"
CURLINE="$(printf '%s\n' "$LIST_OUT" | grep '(current)')"
check_contains "1.2.0 is the current one" "$CURLINE" "1.2.0"

# ── 3. use + rollback ────────────────────────────────────────────────────────
echo ""
echo "[3] daedalus version use 1.1.0"
cli version use 1.1.0 >/dev/null
check_link "current -> versions/1.1.0 after use" "$PREFIX/current" "versions/1.1.0"
check_link "previous -> versions/1.2.0 after use" "$PREFIX/previous" "versions/1.2.0"
check_link "PATH symlink still -> current/daedalus" "$BIN/daedalus" "$PREFIX/current/daedalus"

echo ""
echo "[4] daedalus version rollback"
cli version rollback >/dev/null
check_link "current -> versions/1.2.0 after rollback" "$PREFIX/current" "versions/1.2.0"
check_link "previous -> versions/1.1.0 after rollback" "$PREFIX/previous" "versions/1.1.0"

# ── 5. prune keeps current even when it is not among the newest ──────────────
echo ""
echo "[5] switch to oldest (1.0.0), then prune --keep 1"
cli version use 1.0.0 >/dev/null       # current is now the OLDEST version
check_link "current -> versions/1.0.0" "$PREFIX/current" "versions/1.0.0"
PRUNE_OUT="$(cli version prune --keep 1)"
printf '%s\n' "$PRUNE_OUT" | sed 's/^/    /'
# keep-set = {current 1.0.0} ∪ {newest 1 = 1.2.0}; only 1.1.0 is removed.
check_dir    "current 1.0.0 protected from prune" "$PREFIX/versions/1.0.0"
check_dir    "newest 1.2.0 kept"                  "$PREFIX/versions/1.2.0"
check_no_dir "1.1.0 pruned"                       "$PREFIX/versions/1.1.0"

echo ""
echo "[6] prune --keep 0 must still refuse to drop the current version"
cli version prune --keep 0 >/dev/null
check_dir "current 1.0.0 survives --keep 0" "$PREFIX/versions/1.0.0"

# ── 7. flat -> versioned migration (real binaries) ───────────────────────────
echo ""
echo "[7] flat -> versioned migration"
MIG_HOME="$TMP/mig-home"
MIG_PREFIX="$MIG_HOME/.local/share/daedalus"
mkdir -p "$MIG_PREFIX/.cache"
# Fabricate a legacy FLAT install (payload directly under the prefix root).
cp "$STAGING/daedalus-${PLATFORM}"              "$MIG_PREFIX/daedalus"
cp "$STAGING/skill-catalog-mcp-${PLATFORM}"     "$MIG_PREFIX/skill-catalog-mcp"
cp "$STAGING/project-mgmt-mcp-${PLATFORM}"      "$MIG_PREFIX/project-mgmt-mcp"
cp "$REPO_ROOT/docker-compose.yml" "$REPO_ROOT/Dockerfile" "$REPO_ROOT/entrypoint.sh" \
   "$REPO_ROOT/settings.json" "$REPO_ROOT/logo.txt" "$REPO_ROOT/claude.json" "$MIG_PREFIX/"
cp "$REPO_ROOT/setup.sh" "$MIG_PREFIX/setup.sh"
chmod 755 "$MIG_PREFIX/daedalus" "$MIG_PREFIX/setup.sh"
cat > "$MIG_PREFIX/config.json" <<EOCFG
{
  "version": "0.9.0",
  "data-dir": "$MIG_PREFIX/.cache",
  "debug": false,
  "image-prefix": "techdelight/claude-runner",
  "container-prefix": "",
  "log-file": "$MIG_PREFIX/.cache/daedalus.log"
}
EOCFG

DIST_MIG="$TMP/dist-mig"; mkdir -p "$DIST_MIG"
bash "$PACKAGE_SH" --staging "$STAGING" --out "$DIST_MIG" --version "2.0.0" --platforms "$PLATFORM" >/dev/null
HOME="$MIG_HOME" DAEDALUS_ARCHIVE_DIR="$DIST_MIG" \
    bash "$INSTALL_SH" --prefix "$MIG_PREFIX" --link-name daedalus >/dev/null

check_dir    "legacy 0.9.0 migrated into versions/0.9.0" "$MIG_PREFIX/versions/0.9.0"
check_dir    "new 2.0.0 installed"                       "$MIG_PREFIX/versions/2.0.0"
check_link   "current -> versions/2.0.0"                 "$MIG_PREFIX/current" "versions/2.0.0"
check_link   "previous -> versions/0.9.0"                "$MIG_PREFIX/previous" "versions/0.9.0"
if [ ! -e "$MIG_PREFIX/daedalus" ]; then pass "flat binary moved out of prefix root"; else fail "flat binary still at prefix root"; fi
check_dir    ".cache preserved at prefix root"           "$MIG_PREFIX/.cache"
# The migrated install is runnable and can roll back to the legacy version.
MIG_LIST="$(NO_COLOR=1 HOME="$MIG_HOME" DAEDALUS_BIN_DIR="$MIG_HOME/.local/bin" "$MIG_HOME/.local/bin/daedalus" version list 2>/dev/null)"
check_contains "migrated list shows 0.9.0" "$MIG_LIST" "0.9.0"
check_contains "migrated list shows 2.0.0" "$MIG_LIST" "2.0.0"

# ── 8. uninstall ─────────────────────────────────────────────────────────────
echo ""
echo "[8] uninstall"
HOME="$FAKE_HOME" WORK_DIR="$TMP" bash "$PREFIX/current/setup.sh" --prefix "$PREFIX" --uninstall <<<"n" >/dev/null 2>&1 || true
check_no_dir "versions dir removed on uninstall" "$PREFIX/versions"
if [ ! -e "$PREFIX/current" ]; then pass "current link removed on uninstall"; else fail "current link remains"; fi
if [ ! -L "$BIN/daedalus" ]; then pass "PATH symlink removed on uninstall"; else fail "PATH symlink remains"; fi

# ── Summary ──────────────────────────────────────────────────────────────────
echo ""
echo "Results: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] || exit 1
echo "Side-by-side install + switch/rollback/prune + migration verified."
