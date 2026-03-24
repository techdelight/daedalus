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

MOCK_RELEASE="$TMPDIR_ROOT/mock-release"
PATCHED_INSTALLER="$TMPDIR_ROOT/install-patched.sh"

RUNTIME_FILES=(
    claude.json
    docker-compose.yml
    Dockerfile
    entrypoint.sh
    settings.json
    logo.txt
    config.json
)

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

BINARY_NAME="daedalus-${OS}-${ARCH}"
MCP_BINARY_NAME="skill-catalog-mcp-${OS}-${ARCH}"

# Create mock release directory with fake files
create_mock_release() {
    local version="$1"
    rm -rf "$MOCK_RELEASE"
    mkdir -p "$MOCK_RELEASE"

    # Fake binaries
    printf '#!/bin/sh\necho "daedalus %s"\n' "$version" > "$MOCK_RELEASE/$BINARY_NAME"
    chmod 755 "$MOCK_RELEASE/$BINARY_NAME"
    printf '#!/bin/sh\necho "skill-catalog-mcp %s"\n' "$version" > "$MOCK_RELEASE/$MCP_BINARY_NAME"
    chmod 755 "$MOCK_RELEASE/$MCP_BINARY_NAME"

    # Fake runtime files
    echo '{"version":""}' > "$MOCK_RELEASE/config.json"
    echo "compose: true" > "$MOCK_RELEASE/docker-compose.yml"
    echo "FROM alpine" > "$MOCK_RELEASE/Dockerfile"
    echo '#!/bin/sh' > "$MOCK_RELEASE/entrypoint.sh"
    echo '{}' > "$MOCK_RELEASE/claude.json"
    echo '{}' > "$MOCK_RELEASE/settings.json"
    echo "DAEDALUS" > "$MOCK_RELEASE/logo.txt"

    # Include real setup.sh
    cp "$SETUP_SH" "$MOCK_RELEASE/setup.sh"
    chmod 755 "$MOCK_RELEASE/setup.sh"
}

# Create a patched copy of install.sh that uses local files instead of curl.
# Replaces the curl-based download section with local file copies and sets
# a fixed TAG value so no network access is required.
create_patched_installer() {
    local version_tag="$1"
    local mock_dir="$2"
    local dest="$3"

    cp "$INSTALL_SH" "$dest"

    # Replace the release JSON fetch and TAG extraction with a fixed TAG.
    # Original lines:
    #   RELEASE_JSON="$(curl -fsSL "$GITHUB_API")"
    #   TAG="$(echo "$RELEASE_JSON" | grep '"tag_name"' | ...)"
    sed_inplace 's|^RELEASE_JSON=.*|TAG="'"$version_tag"'"|' "$dest"
    sed_inplace 's|^TAG=.*grep.*|# patched: TAG already set above|' "$dest"

    # Remove the empty-tag check (TAG is always set now)
    sed_inplace 's|^if \[\[ -z "\$TAG" \]\];|if false;|' "$dest"

    # Remove "Fetching latest release" echo (cosmetic)
    sed_inplace 's|echo "Fetching latest release..."|# patched: no fetch needed|' "$dest"

    # Replace binary download with local copy.
    sed_inplace 's|curl -fsSL -o "\$WORK_DIR/daedalus" .*|cp "'"$mock_dir"'/'"$BINARY_NAME"'" "$WORK_DIR/daedalus"|' "$dest"

    # Replace MCP binary download with local copy.
    sed_inplace 's|curl -fsSL -o "\$WORK_DIR/skill-catalog-mcp" .*|cp "'"$mock_dir"'/'"$MCP_BINARY_NAME"'" "$WORK_DIR/skill-catalog-mcp"|' "$dest"

    # Replace setup.sh download with local copy.
    sed_inplace 's|curl -fsSL -o "\$WORK_DIR/setup.sh" .*|cp "'"$mock_dir"'/setup.sh" "$WORK_DIR/setup.sh"|' "$dest"

    # Replace runtime file downloads with local copies.
    sed_inplace 's|curl -fsSL -o "\$WORK_DIR/\$f" .*|cp "'"$mock_dir"'/$f" "$WORK_DIR/$f"|' "$dest"

    chmod +x "$dest"
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
create_patched_installer "v0.8.0" "$MOCK_RELEASE" "$PATCHED_INSTALLER"

bash "$PATCHED_INSTALLER" --prefix "$TEST_PREFIX" --no-link > /dev/null 2>&1

assert_file_exists "binary exists" "$TEST_PREFIX/daedalus"
assert_executable "binary is executable" "$TEST_PREFIX/daedalus"
assert_file_exists "MCP binary exists" "$TEST_PREFIX/skill-catalog-mcp"
assert_executable "MCP binary is executable" "$TEST_PREFIX/skill-catalog-mcp"
assert_file_exists "claude.json present" "$TEST_PREFIX/claude.json"
assert_file_exists "docker-compose.yml present" "$TEST_PREFIX/docker-compose.yml"
assert_file_exists "Dockerfile present" "$TEST_PREFIX/Dockerfile"
assert_file_exists "entrypoint.sh present" "$TEST_PREFIX/entrypoint.sh"
assert_file_exists "settings.json present" "$TEST_PREFIX/settings.json"
assert_file_exists "logo.txt present" "$TEST_PREFIX/logo.txt"
assert_file_exists "config.json present" "$TEST_PREFIX/config.json"
assert_file_exists "setup.sh copied to prefix" "$TEST_PREFIX/setup.sh"
assert_executable "setup.sh is executable" "$TEST_PREFIX/setup.sh"

# --------------------------------------------------------------------------
# Test 2: Config.json fields
# --------------------------------------------------------------------------
echo "Test 2: Config.json fields"

CONFIG_CONTENT="$(cat "$TEST_PREFIX/config.json")"

assert_contains "version field" '"version": "0.8.0"' "$CONFIG_CONTENT"
assert_contains "data-dir field" '"data-dir":' "$CONFIG_CONTENT"
assert_contains "debug field" '"debug": false' "$CONFIG_CONTENT"
assert_contains "no-tmux field" '"no-tmux": false' "$CONFIG_CONTENT"
assert_contains "image-prefix field" '"image-prefix": "techdelight/claude-runner"' "$CONFIG_CONTENT"
assert_contains "log-file field" '"log-file":' "$CONFIG_CONTENT"

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
    if [ "$LINK_TARGET" = "$TEST_PREFIX/daedalus" ]; then
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
create_patched_installer "v0.7.0" "$MOCK_RELEASE" "$PATCHED_INSTALLER"
bash "$PATCHED_INSTALLER" --prefix "$TEST_PREFIX_UPG" --no-link > /dev/null 2>&1

# Modify config.json to simulate user customization
cat > "$TEST_PREFIX_UPG/config.json" <<EOCFG
{
  "version": "0.7.0",
  "data-dir": "/custom/data",
  "debug": true,
  "no-tmux": true,
  "image-prefix": "my-registry/runner",
  "log-file": "/custom/data/my.log"
}
EOCFG

# Upgrade to v0.8.0
create_mock_release "0.8.0"
create_patched_installer "v0.8.0" "$MOCK_RELEASE" "$PATCHED_INSTALLER"
bash "$PATCHED_INSTALLER" --prefix "$TEST_PREFIX_UPG" --no-link > /dev/null 2>&1

UPG_CONFIG="$(cat "$TEST_PREFIX_UPG/config.json")"

assert_contains "version updated to 0.8.0" '"version": "0.8.0"' "$UPG_CONFIG"
assert_contains "data-dir preserved" '"data-dir": "/custom/data"' "$UPG_CONFIG"
assert_contains "debug preserved" '"debug": true' "$UPG_CONFIG"
assert_contains "no-tmux preserved" '"no-tmux": true' "$UPG_CONFIG"
assert_contains "image-prefix preserved" '"image-prefix": "my-registry/runner"' "$UPG_CONFIG"
assert_contains "log-file preserved" '"log-file": "/custom/data/my.log"' "$UPG_CONFIG"

# --------------------------------------------------------------------------
# Test 5: Uninstall removes files
# --------------------------------------------------------------------------
echo "Test 5: Uninstall removes files"

TEST_PREFIX_RM="$TMPDIR_ROOT/test5-prefix"

# Install first
create_mock_release "0.8.0"
create_patched_installer "v0.8.0" "$MOCK_RELEASE" "$PATCHED_INSTALLER"
bash "$PATCHED_INSTALLER" --prefix "$TEST_PREFIX_RM" --no-link > /dev/null 2>&1

# Verify install worked before uninstalling
assert_file_exists "pre-uninstall binary exists" "$TEST_PREFIX_RM/daedalus"

# Uninstall via setup.sh directly (no download needed for uninstall)
WORK_DIR="$MOCK_RELEASE" bash "$SETUP_SH" --prefix "$TEST_PREFIX_RM" --uninstall > /dev/null 2>&1

assert_file_not_exists "binary removed" "$TEST_PREFIX_RM/daedalus"
assert_file_not_exists "skill-catalog-mcp removed" "$TEST_PREFIX_RM/skill-catalog-mcp"
assert_file_not_exists "config.json removed" "$TEST_PREFIX_RM/config.json"
assert_file_not_exists "claude.json removed" "$TEST_PREFIX_RM/claude.json"
assert_file_not_exists "docker-compose.yml removed" "$TEST_PREFIX_RM/docker-compose.yml"
assert_file_not_exists "Dockerfile removed" "$TEST_PREFIX_RM/Dockerfile"
assert_file_not_exists "entrypoint.sh removed" "$TEST_PREFIX_RM/entrypoint.sh"
assert_file_not_exists "settings.json removed" "$TEST_PREFIX_RM/settings.json"
assert_file_not_exists "logo.txt removed" "$TEST_PREFIX_RM/logo.txt"
assert_file_not_exists "setup.sh removed" "$TEST_PREFIX_RM/setup.sh"

# --------------------------------------------------------------------------
# Test 6: Uninstall with --prefix removes directory
# --------------------------------------------------------------------------
echo "Test 6: Uninstall with --prefix removes directory"

TEST_PREFIX_DIR="$TMPDIR_ROOT/test6-prefix"

# Install, then uninstall
create_mock_release "0.8.0"
create_patched_installer "v0.8.0" "$MOCK_RELEASE" "$PATCHED_INSTALLER"
bash "$PATCHED_INSTALLER" --prefix "$TEST_PREFIX_DIR" --no-link > /dev/null 2>&1
WORK_DIR="$MOCK_RELEASE" bash "$SETUP_SH" --prefix "$TEST_PREFIX_DIR" --uninstall > /dev/null 2>&1

assert_dir_not_exists "prefix directory removed" "$TEST_PREFIX_DIR"

# --------------------------------------------------------------------------
# Test 7: Install with no flags (empty FORWARD_ARGS)
# --------------------------------------------------------------------------
echo "Test 7: Install with no flags"

# On bash 3.2 (macOS), "${arr[@]}" on an empty array fails under set -u.
# This test verifies the installer handles zero flags correctly.
TEST_PREFIX_NOFLAGS="$TMPDIR_ROOT/test7-prefix"
create_mock_release "0.8.0"
create_patched_installer "v0.8.0" "$MOCK_RELEASE" "$PATCHED_INSTALLER"

# Patch HOME so the symlink goes into our temp dir, not the real home
set +e
HOME="$TMPDIR_ROOT/fakehome" bash "$PATCHED_INSTALLER" > /dev/null 2>&1
exit_code=$?
set -e

assert_exit_code "exits with code 0" 0 "$exit_code"
assert_file_exists "binary exists at default prefix" "$TMPDIR_ROOT/fakehome/.local/share/daedalus/daedalus"

# Clean up
rm -rf "$TMPDIR_ROOT/fakehome"

# --------------------------------------------------------------------------
# Test 8: Root rejection
# --------------------------------------------------------------------------
echo "Test 8: Root rejection"

if [ "$(id -u)" -eq 0 ]; then
    echo "  SKIP: running as root, cannot test root rejection"
else
    TEST_PREFIX_ROOT="$TMPDIR_ROOT/test7-prefix"
    create_mock_release "0.8.0"

    # Test root rejection in setup.sh (where the check lives)
    # Patch setup.sh to fake EUID=0
    cp "$SETUP_SH" "$TMPDIR_ROOT/setup-root-test.sh"
    sed_inplace 's|\$EUID -eq 0|0 -eq 0|' "$TMPDIR_ROOT/setup-root-test.sh"
    chmod +x "$TMPDIR_ROOT/setup-root-test.sh"

    set +e
    WORK_DIR="$MOCK_RELEASE" bash "$TMPDIR_ROOT/setup-root-test.sh" --prefix "$TEST_PREFIX_ROOT" --no-link > /dev/null 2>&1
    exit_code=$?
    set -e

    assert_exit_code "exits with code 1" 1 "$exit_code"
    assert_dir_not_exists "no files installed" "$TEST_PREFIX_ROOT"
fi

# ── Summary ──────────────────────────────────────────────────────────────────

echo ""
echo "Results: $PASS passed, $FAIL failed"

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
