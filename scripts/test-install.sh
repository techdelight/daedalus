# Copyright (C) 2026 Techdelight BV
#
# Tests for install.sh + setup.sh
#
# Validates install, upgrade, and uninstall flows by running a patched
# copy of install.sh that uses local files instead of curl downloads.
#
# Usage: bash scripts/test-install.sh

set -euo pipefail

# ── Portable sed -i (BSD vs GNU) ──────────────────────────────────────────
sed_inplace() {
    if sed --version >/dev/null 2>&1; then
        sed -i "$@"
    else
        sed -i '' "$@"
    fi
}

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
INSTALL_SH="$REPO_ROOT/install.sh"
SETUP_SH="$REPO_ROOT/setup.sh"
PACKAGE_SH="$SCRIPT_DIR/package-release.sh"
PASS=0
FAIL=0

# ── Helpers ──────────────────────────────────────────────────────────────────

assert_equals() {
    local test_name="$1"
    local expected="$2"
    local actual="$3"
    if [ "$expected" = "$actual" ]; then
        echo "  PASS: $test_name"
        PASS=$((PASS + 1))
    else
        echo "  FAIL: $test_name"
        echo "    expected: $(echo "$expected" | head -3)"
        echo "    actual:   $(echo "$actual" | head -3)"
        FAIL=$((FAIL + 1))
    fi
}

assert_contains() {
    local test_name="$1"
    local needle="$2"
    local haystack="$3"
    if echo "$haystack" | grep -qF "$needle"; then
        echo "  PASS: $test_name"
        PASS=$((PASS + 1))
    else
        echo "  FAIL: $test_name"
        echo "    expected to contain: $needle"
        echo "    actual: $(echo "$haystack" | head -3)"
        FAIL=$((FAIL + 1))
    fi
}

assert_file_exists() {
    local test_name="$1"
    local path="$2"
    if [ -f "$path" ]; then
        echo "  PASS: $test_name"
        PASS=$((PASS + 1))
    else
        echo "  FAIL: $test_name"
        echo "    file does not exist: $path"
        FAIL=$((FAIL + 1))
    fi
}

assert_file_not_exists() {
    local test_name="$1"
    local path="$2"
    if [ ! -f "$path" ]; then
        echo "  PASS: $test_name"
        PASS=$((PASS + 1))
    else
        echo "  FAIL: $test_name"
        echo "    file should not exist: $path"
        FAIL=$((FAIL + 1))
    fi
}

assert_dir_not_exists() {
    local test_name="$1"
    local path="$2"
    if [ ! -d "$path" ]; then
        echo "  PASS: $test_name"
        PASS=$((PASS + 1))
    else
        echo "  FAIL: $test_name"
        echo "    directory should not exist: $path"
        FAIL=$((FAIL + 1))
    fi
}

assert_dir_exists() {
    local test_name="$1"
    local path="$2"
    if [ -d "$path" ]; then
        echo "  PASS: $test_name"
        PASS=$((PASS + 1))
    else
        echo "  FAIL: $test_name"
        echo "    directory does not exist: $path"
        FAIL=$((FAIL + 1))
    fi
}

assert_symlink() {
    local test_name="$1"
    local path="$2"
    if [ -L "$path" ]; then
        echo "  PASS: $test_name"
        PASS=$((PASS + 1))
    else
        echo "  FAIL: $test_name"
        echo "    not a symlink: $path"
        FAIL=$((FAIL + 1))
    fi
}

# Assert a symlink resolves (via readlink) to an expected relative/absolute target.
assert_link_target() {
    local test_name="$1"
    local link="$2"
    local expected="$3"
    local actual
    actual="$(readlink "$link" 2>/dev/null || true)"
    if [ "$actual" = "$expected" ]; then
        echo "  PASS: $test_name"
        PASS=$((PASS + 1))
    else
        echo "  FAIL: $test_name"
        echo "    link:     $link"
        echo "    expected: $expected"
        echo "    actual:   $actual"
        FAIL=$((FAIL + 1))
    fi
}

assert_executable() {
    local test_name="$1"
    local path="$2"
    if [ -x "$path" ]; then
        echo "  PASS: $test_name"
        PASS=$((PASS + 1))
    else
        echo "  FAIL: $test_name"
        echo "    file is not executable: $path"
        FAIL=$((FAIL + 1))
    fi
}

assert_exit_code() {
    local test_name="$1"
    local expected="$2"
    local actual="$3"
    if [ "$expected" -eq "$actual" ]; then
        echo "  PASS: $test_name"
        PASS=$((PASS + 1))
    else
        echo "  FAIL: $test_name"
        echo "    expected exit code: $expected"
        echo "    actual exit code:   $actual"
        FAIL=$((FAIL + 1))
    fi
}

# ── Setup ────────────────────────────────────────────────────────────────────

TMPDIR_ROOT=$(mktemp -d)
trap 'rm -rf "$TMPDIR_ROOT"' EXIT

# Directory that holds the produced daedalus-<os>-<arch>.tar.gz + SHA256SUMS.txt.
# install.sh is pointed at it via DAEDALUS_ARCHIVE_DIR, so the real
# verify + extract + setup.sh path runs with no network access.
ARCHIVE_DIR="$TMPDIR_ROOT/archive"

# Detect platform (same logic as install.sh)
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$OS" in
    linux)  OS="linux" ;;
    darwin) OS="darwin" ;;
esac
case "$ARCH" in
    x86_64)  ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    arm64)   ARCH="arm64" ;;
esac
PLATFORM="${OS}-${ARCH}"

# Build a real release bundle (fake binaries, real runtime files + setup.sh)
# through scripts/package-release.sh — the same packager CI uses — then point
# install.sh at it via DAEDALUS_ARCHIVE_DIR. The version is baked into the
# packaged config.json by the packager, exactly as in a real release.
create_mock_release() {
    local version="$1"
    local staging
    staging="$(mktemp -d)"

    # Fake per-platform binaries (host platform names).
    for b in daedalus skill-catalog-mcp project-mgmt-mcp guild-mcp daedalus-coordinator daedalus-runner; do
        printf '#!/bin/sh\necho "%s %s"\n' "$b" "$version" > "$staging/${b}-${PLATFORM}"
        chmod 755 "$staging/${b}-${PLATFORM}"
    done

    # Runtime files (config.json ships the empty-version template).
    echo '{"version":""}' > "$staging/config.json"
    echo "compose: true" > "$staging/docker-compose.yml"
    echo "FROM alpine" > "$staging/Dockerfile"
    echo '#!/bin/sh' > "$staging/entrypoint.sh"
    echo '{}' > "$staging/claude.json"
    echo '{}' > "$staging/settings.json"
    echo "DAEDALUS" > "$staging/logo.txt"
    echo "@echo off" > "$staging/wsl2-network.bat"
    cp "$SETUP_SH" "$staging/setup.sh"
    chmod 755 "$staging/setup.sh"

    rm -rf "$ARCHIVE_DIR"
    mkdir -p "$ARCHIVE_DIR"
    bash "$PACKAGE_SH" --staging "$staging" --out "$ARCHIVE_DIR" \
        --version "$version" --platforms "$PLATFORM" >/dev/null
    rm -rf "$staging"
}

# Run install.sh against the produced local archive (no network).
run_install() {
    DAEDALUS_ARCHIVE_DIR="$ARCHIVE_DIR" bash "$INSTALL_SH" "$@"
}

# ── Tests ────────────────────────────────────────────────────────────────────

echo "Running install.sh + setup.sh tests..."
echo ""

# --------------------------------------------------------------------------
# Test 1: Fresh install
# --------------------------------------------------------------------------
echo "Test 1: Fresh install"

TEST_PREFIX="$TMPDIR_ROOT/test1-prefix"
create_mock_release "0.8.0"

run_install --prefix "$TEST_PREFIX" --no-link > /dev/null 2>&1

# Versioned layout: payload lives under versions/<v>/, reachable via `current`.
assert_dir_exists "versions/0.8.0 directory created" "$TEST_PREFIX/versions/0.8.0"
assert_symlink "current symlink created" "$TEST_PREFIX/current"
assert_file_exists "binary exists" "$TEST_PREFIX/current/daedalus"
assert_executable "binary is executable" "$TEST_PREFIX/current/daedalus"
assert_file_exists "skill-catalog-mcp binary exists" "$TEST_PREFIX/current/skill-catalog-mcp"
assert_executable "skill-catalog-mcp binary is executable" "$TEST_PREFIX/current/skill-catalog-mcp"
assert_file_exists "project-mgmt-mcp binary exists" "$TEST_PREFIX/current/project-mgmt-mcp"
assert_executable "project-mgmt-mcp binary is executable" "$TEST_PREFIX/current/project-mgmt-mcp"
assert_file_exists "daedalus-runner binary exists" "$TEST_PREFIX/current/daedalus-runner"
assert_file_exists "daedalus-coordinator binary exists" "$TEST_PREFIX/current/daedalus-coordinator"
assert_file_exists "claude.json present" "$TEST_PREFIX/current/claude.json"
assert_file_exists "docker-compose.yml present" "$TEST_PREFIX/current/docker-compose.yml"
assert_file_exists "Dockerfile present" "$TEST_PREFIX/current/Dockerfile"
assert_file_exists "entrypoint.sh present" "$TEST_PREFIX/current/entrypoint.sh"
assert_file_exists "settings.json present" "$TEST_PREFIX/current/settings.json"
assert_file_exists "logo.txt present" "$TEST_PREFIX/current/logo.txt"
assert_file_exists "config.json present" "$TEST_PREFIX/current/config.json"
assert_file_exists "setup.sh copied to version dir" "$TEST_PREFIX/current/setup.sh"
assert_executable "setup.sh is executable" "$TEST_PREFIX/current/setup.sh"

# --------------------------------------------------------------------------
# Test 2: Config.json fields
# --------------------------------------------------------------------------
echo "Test 2: Config.json fields"

CONFIG_CONTENT="$(cat "$TEST_PREFIX/current/config.json")"

assert_contains "version field" '"version": "0.8.0"' "$CONFIG_CONTENT"
assert_contains "data-dir field" '"data-dir":' "$CONFIG_CONTENT"
assert_contains "debug field" '"debug": false' "$CONFIG_CONTENT"
assert_contains "image-prefix field" '"image-prefix": "techdelight/claude-runner"' "$CONFIG_CONTENT"
assert_contains "log-file field" '"log-file":' "$CONFIG_CONTENT"
# The shared data dir must live at the prefix root, not inside the version dir.
assert_contains "data-dir is prefix-level .cache" "\"data-dir\": \"$TEST_PREFIX/.cache\"" "$CONFIG_CONTENT"

# --------------------------------------------------------------------------
# Test 3: --no-link flag prevents symlink
# --------------------------------------------------------------------------
echo "Test 3: --no-link flag"

# The test1 install used --no-link; verify no symlink was created.
# install.sh creates symlink at $HOME/.local/bin/daedalus, which should
# NOT point to our test prefix if --no-link was used.
EXPECTED_LINK="$HOME/.local/bin/daedalus"
if [ -L "$EXPECTED_LINK" ]; then
    LINK_TARGET="$(readlink "$EXPECTED_LINK")"
    if [ "$LINK_TARGET" = "$TEST_PREFIX/current/daedalus" ]; then
        echo "  FAIL: symlink was created despite --no-link"
        FAIL=$((FAIL + 1))
    else
        echo "  PASS: symlink does not point to test prefix"
        PASS=$((PASS + 1))
    fi
else
    echo "  PASS: no symlink created"
    PASS=$((PASS + 1))
fi

# --------------------------------------------------------------------------
# Test 4: Upgrade preserves settings
# --------------------------------------------------------------------------
echo "Test 4: Upgrade preserves settings"

TEST_PREFIX_UPG="$TMPDIR_ROOT/test4-prefix"

# First install with v0.7.0
create_mock_release "0.7.0"
run_install --prefix "$TEST_PREFIX_UPG" --no-link > /dev/null 2>&1

# Modify the active version's config.json to simulate user customization
cat > "$TEST_PREFIX_UPG/current/config.json" <<EOCFG
{
  "version": "0.7.0",
  "data-dir": "/custom/data",
  "debug": true,
  "image-prefix": "my-registry/runner",
  "log-file": "/custom/data/my.log"
}
EOCFG

# Upgrade to v0.8.0 (installs alongside 0.7.0 and flips current)
create_mock_release "0.8.0"
run_install --prefix "$TEST_PREFIX_UPG" --no-link > /dev/null 2>&1

UPG_CONFIG="$(cat "$TEST_PREFIX_UPG/current/config.json")"

assert_contains "version updated to 0.8.0" '"version": "0.8.0"' "$UPG_CONFIG"
assert_contains "data-dir preserved" '"data-dir": "/custom/data"' "$UPG_CONFIG"
assert_contains "debug preserved" '"debug": true' "$UPG_CONFIG"
assert_contains "image-prefix preserved" '"image-prefix": "my-registry/runner"' "$UPG_CONFIG"
assert_contains "log-file preserved" '"log-file": "/custom/data/my.log"' "$UPG_CONFIG"

# Upgrade keeps the prior version and records it as `previous` for rollback.
assert_dir_exists "prior version 0.7.0 kept" "$TEST_PREFIX_UPG/versions/0.7.0"
assert_dir_exists "new version 0.8.0 present" "$TEST_PREFIX_UPG/versions/0.8.0"
assert_link_target "current -> versions/0.8.0" "$TEST_PREFIX_UPG/current" "versions/0.8.0"
assert_link_target "previous -> versions/0.7.0" "$TEST_PREFIX_UPG/previous" "versions/0.7.0"

# --------------------------------------------------------------------------
# Test 5: Uninstall removes files
# --------------------------------------------------------------------------
echo "Test 5: Uninstall removes files"

TEST_PREFIX_RM="$TMPDIR_ROOT/test5-prefix"

# Install first
create_mock_release "0.8.0"
run_install --prefix "$TEST_PREFIX_RM" --no-link > /dev/null 2>&1

# Verify install worked before uninstalling
assert_file_exists "pre-uninstall binary exists" "$TEST_PREFIX_RM/current/daedalus"

# Uninstall via setup.sh directly (no download needed for uninstall)
WORK_DIR="$TMPDIR_ROOT" bash "$SETUP_SH" --prefix "$TEST_PREFIX_RM" --uninstall > /dev/null 2>&1

assert_dir_not_exists "versions directory removed" "$TEST_PREFIX_RM/versions"
assert_file_not_exists "current symlink removed" "$TEST_PREFIX_RM/current"
assert_file_not_exists "previous symlink removed" "$TEST_PREFIX_RM/previous"
assert_file_not_exists "binary removed" "$TEST_PREFIX_RM/current/daedalus"

# --------------------------------------------------------------------------
# Test 6: Uninstall with --prefix removes directory
# --------------------------------------------------------------------------
echo "Test 6: Uninstall with --prefix removes directory"

TEST_PREFIX_DIR="$TMPDIR_ROOT/test6-prefix"

# Install, then uninstall
create_mock_release "0.8.0"
run_install --prefix "$TEST_PREFIX_DIR" --no-link > /dev/null 2>&1
WORK_DIR="$TMPDIR_ROOT" bash "$SETUP_SH" --prefix "$TEST_PREFIX_DIR" --uninstall > /dev/null 2>&1

assert_dir_not_exists "prefix directory removed" "$TEST_PREFIX_DIR"

# --------------------------------------------------------------------------
# Test 7: Install with no flags (empty FORWARD_ARGS)
# --------------------------------------------------------------------------
echo "Test 7: Install with no flags"

# On bash 3.2 (macOS), "${arr[@]}" on an empty array fails under set -u.
# This test verifies the installer handles zero flags correctly.
TEST_PREFIX_NOFLAGS="$TMPDIR_ROOT/test7-prefix"
create_mock_release "0.8.0"

# Patch HOME so the symlink goes into our temp dir, not the real home
set +e
HOME="$TMPDIR_ROOT/fakehome" DAEDALUS_ARCHIVE_DIR="$ARCHIVE_DIR" \
    bash "$INSTALL_SH" > /dev/null 2>&1
exit_code=$?
set -e

assert_exit_code "exits with code 0" 0 "$exit_code"
assert_file_exists "binary exists at default prefix" "$TMPDIR_ROOT/fakehome/.local/share/daedalus/current/daedalus"
assert_link_target "default-prefix PATH symlink -> current/daedalus" \
    "$TMPDIR_ROOT/fakehome/.local/bin/daedalus" \
    "$TMPDIR_ROOT/fakehome/.local/share/daedalus/current/daedalus"

# Clean up
rm -rf "$TMPDIR_ROOT/fakehome"

# --------------------------------------------------------------------------
# Test 8: Root rejection
# --------------------------------------------------------------------------
echo "Test 8: Root rejection"

if [ "$(id -u)" -eq 0 ]; then
    echo "  SKIP: running as root, cannot test root rejection"
else
    TEST_PREFIX_ROOT="$TMPDIR_ROOT/test8-prefix"

    # Test root rejection in setup.sh (where the check lives)
    # Patch setup.sh to fake EUID=0
    cp "$SETUP_SH" "$TMPDIR_ROOT/setup-root-test.sh"
    sed_inplace 's|\$EUID -eq 0|0 -eq 0|' "$TMPDIR_ROOT/setup-root-test.sh"
    chmod +x "$TMPDIR_ROOT/setup-root-test.sh"

    set +e
    WORK_DIR="$TMPDIR_ROOT" bash "$TMPDIR_ROOT/setup-root-test.sh" --prefix "$TEST_PREFIX_ROOT" --no-link > /dev/null 2>&1
    exit_code=$?
    set -e

    assert_exit_code "exits with code 1" 1 "$exit_code"
    assert_dir_not_exists "no files installed" "$TEST_PREFIX_ROOT"
fi

# --------------------------------------------------------------------------
# Test 9: Side-by-side install keeps both versions
# --------------------------------------------------------------------------
echo "Test 9: Side-by-side install"

TEST_PREFIX_SBS="$TMPDIR_ROOT/test9-prefix"

create_mock_release "1.0.0"
run_install --prefix "$TEST_PREFIX_SBS" --no-link > /dev/null 2>&1
create_mock_release "1.1.0"
run_install --prefix "$TEST_PREFIX_SBS" --no-link > /dev/null 2>&1

assert_dir_exists "v1.0.0 dir kept" "$TEST_PREFIX_SBS/versions/1.0.0"
assert_dir_exists "v1.1.0 dir present" "$TEST_PREFIX_SBS/versions/1.1.0"
assert_link_target "current -> versions/1.1.0" "$TEST_PREFIX_SBS/current" "versions/1.1.0"
assert_link_target "previous -> versions/1.0.0" "$TEST_PREFIX_SBS/previous" "versions/1.0.0"

# --------------------------------------------------------------------------
# Test 10: Flat -> versioned migration
# --------------------------------------------------------------------------
echo "Test 10: Flat -> versioned migration"

TEST_PREFIX_MIG="$TMPDIR_ROOT/test10-prefix"

# Fabricate a legacy flat install (binaries + config directly under $PREFIX).
mkdir -p "$TEST_PREFIX_MIG/.cache"
printf '#!/bin/sh\necho old\n' > "$TEST_PREFIX_MIG/daedalus"
chmod 755 "$TEST_PREFIX_MIG/daedalus"
for f in skill-catalog-mcp project-mgmt-mcp; do
    printf '#!/bin/sh\n' > "$TEST_PREFIX_MIG/$f"; chmod 755 "$TEST_PREFIX_MIG/$f"
done
echo '{}' > "$TEST_PREFIX_MIG/claude.json"
echo '{}' > "$TEST_PREFIX_MIG/settings.json"
echo "DAEDALUS" > "$TEST_PREFIX_MIG/logo.txt"
echo "compose: true" > "$TEST_PREFIX_MIG/docker-compose.yml"
echo "FROM alpine" > "$TEST_PREFIX_MIG/Dockerfile"
echo '#!/bin/sh' > "$TEST_PREFIX_MIG/entrypoint.sh"
cp "$SETUP_SH" "$TEST_PREFIX_MIG/setup.sh"
cat > "$TEST_PREFIX_MIG/config.json" <<EOCFG
{
  "version": "0.6.0",
  "data-dir": "$TEST_PREFIX_MIG/.cache",
  "debug": false,
  "image-prefix": "techdelight/claude-runner",
  "log-file": "$TEST_PREFIX_MIG/.cache/daedalus.log"
}
EOCFG

# Upgrade to 0.8.0 — should migrate the flat 0.6.0 payload into versions/0.6.0.
create_mock_release "0.8.0"
run_install --prefix "$TEST_PREFIX_MIG" --no-link > /dev/null 2>&1

assert_dir_exists "legacy payload migrated to versions/0.6.0" "$TEST_PREFIX_MIG/versions/0.6.0"
assert_file_exists "migrated legacy binary present" "$TEST_PREFIX_MIG/versions/0.6.0/daedalus"
assert_dir_exists "new version 0.8.0 installed" "$TEST_PREFIX_MIG/versions/0.8.0"
assert_link_target "current -> versions/0.8.0 after migration" "$TEST_PREFIX_MIG/current" "versions/0.8.0"
assert_link_target "previous -> versions/0.6.0 after migration" "$TEST_PREFIX_MIG/previous" "versions/0.6.0"
assert_file_not_exists "flat binary no longer at prefix root" "$TEST_PREFIX_MIG/daedalus"
# Shared data dir preserved in place (not moved into a version dir).
assert_dir_exists ".cache preserved at prefix root" "$TEST_PREFIX_MIG/.cache"

# ── Summary ──────────────────────────────────────────────────────────────────

echo ""
echo "Results: $PASS passed, $FAIL failed"

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
