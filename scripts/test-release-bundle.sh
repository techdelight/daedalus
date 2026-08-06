#!/usr/bin/env bash
# Copyright (C) 2026 Techdelight BV
#
# test-release-bundle.sh — local end-to-end proof of the bundled-release chain.
#
# Because .github/workflows/release.yml only runs on GitHub and install.sh
# normally pulls from a real GitHub Release, neither is runnable here. This
# script exercises the SAME packaging + install logic locally, with no network:
#
#   1. Build the host-platform binaries with their release -o names.
#   2. Stage the shared runtime files.
#   3. Run scripts/package-release.sh (the single packaging source of truth)
#      to produce daedalus-<os>-<arch>.tar.gz + SHA256SUMS.txt.
#   4. Drive install.sh's real verify + extract + setup.sh path against the
#      produced archive (via DAEDALUS_ARCHIVE_DIR) into a throwaway PREFIX.
#   5. Assert the 5 binaries + runtime files land in PREFIX and the symlink is
#      created, that checksum verification passes, and that a corrupted archive
#      is rejected.
#
# Usage: bash scripts/test-release-bundle.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PACKAGE_SH="$SCRIPT_DIR/package-release.sh"
INSTALL_SH="$REPO_ROOT/install.sh"

TEST_VERSION="9.9.9"

PASS=0
FAIL=0

pass() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL + 1)); }

check_file() { if [ -f "$2" ]; then pass "$1"; else fail "$1 (missing: $2)"; fi; }
check_exec() { if [ -x "$2" ]; then pass "$1"; else fail "$1 (not executable: $2)"; fi; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

STAGING="$TMP/staging"
DIST="$TMP/dist"
PREFIX="$TMP/prefix"
FAKE_HOME="$TMP/home"
mkdir -p "$STAGING" "$DIST" "$FAKE_HOME"

# ── Host platform (release naming) ───────────────────────────────────────────
GOOS="$(go env GOOS)"
GOARCH="$(go env GOARCH)"
PLATFORM="${GOOS}-${GOARCH}"
echo "Host platform: $PLATFORM"

# ── 1. Build the host-platform binaries with their release -o names ──────────
echo ""
echo "[1/5] Building host binaries into staging..."
VERSION_FILE="$(cat "$REPO_ROOT/VERSION")"
( cd "$REPO_ROOT"
  go build -ldflags="-X github.com/techdelight/daedalus/core.Version=$VERSION_FILE" \
      -o "$STAGING/daedalus-${PLATFORM}" ./cmd/daedalus
  go build -o "$STAGING/skill-catalog-mcp-${PLATFORM}"    ./cmd/skill-catalog-mcp
  go build -o "$STAGING/project-mgmt-mcp-${PLATFORM}"     ./cmd/project-mgmt-mcp
  go build -o "$STAGING/daedalus-coordinator-${PLATFORM}" ./cmd/daedalus-coordinator
  go build -o "$STAGING/daedalus-runner-${PLATFORM}"      ./cmd/daedalus-runner
)
echo "  Built 5 binaries."

# ── 2. Stage the shared runtime files ────────────────────────────────────────
echo ""
echo "[2/5] Staging runtime files..."
cp "$REPO_ROOT/claude.json" "$REPO_ROOT/config.json" "$REPO_ROOT/docker-compose.yml" \
   "$REPO_ROOT/Dockerfile" "$REPO_ROOT/entrypoint.sh" "$REPO_ROOT/settings.json" \
   "$REPO_ROOT/logo.txt" "$REPO_ROOT/setup.sh" "$STAGING/"
cp "$REPO_ROOT/scripts/wsl2-network.bat" "$STAGING/"
echo "  Staged runtime files."

# ── 3. Run the packaging script (host platform only) ─────────────────────────
echo ""
echo "[3/5] Packaging via scripts/package-release.sh..."
bash "$PACKAGE_SH" --staging "$STAGING" --out "$DIST" --version "$TEST_VERSION" --platforms "$PLATFORM"

ARCHIVE_NAME="daedalus-${PLATFORM}.tar.gz"
check_file "archive produced" "$DIST/$ARCHIVE_NAME"
check_file "SHA256SUMS.txt produced" "$DIST/SHA256SUMS.txt"

# Confirm the archive layout (flat: install names, no top-level dir) and that
# it holds exactly the set setup.sh installs.
echo ""
echo "  Archive contents:"
tar -tzf "$DIST/$ARCHIVE_NAME" | sed 's/^/    /'
EXPECTED_ENTRIES="Dockerfile
claude.json
config.json
daedalus
daedalus-coordinator
daedalus-runner
docker-compose.yml
entrypoint.sh
logo.txt
project-mgmt-mcp
settings.json
setup.sh
skill-catalog-mcp
wsl2-network.bat"
ACTUAL_ENTRIES="$(tar -tzf "$DIST/$ARCHIVE_NAME" | LC_ALL=C sort)"
if [ "$EXPECTED_ENTRIES" = "$ACTUAL_ENTRIES" ]; then
    pass "archive contains exactly the expected 14 flat entries"
else
    fail "archive contents differ from expected"
    echo "    --- expected ---"; echo "$EXPECTED_ENTRIES" | sed 's/^/      /'
    echo "    --- actual   ---"; echo "$ACTUAL_ENTRIES"   | sed 's/^/      /'
fi

# ── 4. Drive install.sh's verify + extract + setup.sh path (no network) ──────
echo ""
echo "[4/5] Installing from the local archive via install.sh..."
HOME="$FAKE_HOME" DAEDALUS_ARCHIVE_DIR="$DIST" \
    bash "$INSTALL_SH" --prefix "$PREFIX" --link-name daedalus

echo ""
echo "  Verifying installed tree..."
check_exec "daedalus binary installed"              "$PREFIX/daedalus"
check_exec "skill-catalog-mcp installed"            "$PREFIX/skill-catalog-mcp"
check_exec "project-mgmt-mcp installed"             "$PREFIX/project-mgmt-mcp"
check_exec "daedalus-coordinator installed"         "$PREFIX/daedalus-coordinator"
check_exec "daedalus-runner installed"              "$PREFIX/daedalus-runner"
check_file "claude.json installed"                  "$PREFIX/claude.json"
check_file "docker-compose.yml installed"           "$PREFIX/docker-compose.yml"
check_file "Dockerfile installed"                   "$PREFIX/Dockerfile"
check_file "entrypoint.sh installed"                "$PREFIX/entrypoint.sh"
check_file "settings.json installed"                "$PREFIX/settings.json"
check_file "logo.txt installed"                     "$PREFIX/logo.txt"
check_file "config.json installed"                  "$PREFIX/config.json"
check_file "setup.sh installed"                      "$PREFIX/setup.sh"

# Symlink created in the (fake) home.
LINK="$FAKE_HOME/.local/bin/daedalus"
if [ -L "$LINK" ] && [ "$(readlink "$LINK")" = "$PREFIX/daedalus" ]; then
    pass "symlink created -> $PREFIX/daedalus"
else
    fail "symlink not created correctly at $LINK"
fi

# Version baked into config.json at package time (not patched by install.sh).
if grep -q "\"version\": \"$TEST_VERSION\"" "$PREFIX/config.json"; then
    pass "config.json records packaged version $TEST_VERSION"
else
    fail "config.json version not baked correctly"
    sed 's/^/      /' "$PREFIX/config.json"
fi

# ── 5. Corrupted-archive rejection ───────────────────────────────────────────
echo ""
echo "[5/5] Corrupted-archive rejection..."
BAD_DIR="$TMP/bad"
mkdir -p "$BAD_DIR"
cp "$DIST/SHA256SUMS.txt" "$BAD_DIR/"
# Copy the good archive then corrupt it so its hash no longer matches SHA256SUMS.
cp "$DIST/$ARCHIVE_NAME" "$BAD_DIR/$ARCHIVE_NAME"
printf 'corruption' >> "$BAD_DIR/$ARCHIVE_NAME"

set +e
HOME="$FAKE_HOME" DAEDALUS_ARCHIVE_DIR="$BAD_DIR" \
    bash "$INSTALL_SH" --prefix "$TMP/prefix-bad" --no-link > "$TMP/bad.log" 2>&1
BAD_EXIT=$?
set -e

if [ "$BAD_EXIT" -ne 0 ]; then
    pass "corrupted archive rejected (exit $BAD_EXIT)"
else
    fail "corrupted archive was NOT rejected (exit 0)"
fi
if grep -q "checksum mismatch" "$TMP/bad.log"; then
    pass "rejection reports a checksum mismatch"
else
    fail "rejection did not report a checksum mismatch"
    sed 's/^/      /' "$TMP/bad.log"
fi
if [ ! -f "$TMP/prefix-bad/daedalus" ]; then
    pass "no binary installed from corrupted archive"
else
    fail "a binary was installed despite corruption"
fi

# ── Summary ──────────────────────────────────────────────────────────────────
echo ""
echo "Results: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] || exit 1
echo "Local end-to-end release-bundle chain verified."
