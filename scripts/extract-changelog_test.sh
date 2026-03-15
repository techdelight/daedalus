# Copyright (C) 2026 Techdelight BV
#
# Tests for extract-changelog.sh
#
# Usage: bash scripts/extract-changelog_test.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
EXTRACT="$SCRIPT_DIR/extract-changelog.sh"
PASS=0
FAIL=0

# --- Helpers ---

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

# --- Setup ---

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

# Create a test CHANGELOG.md
cat > "$TMPDIR/CHANGELOG.md" << 'CHANGELOG'
# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

## [1.2.0] - 2026-03-15

### Added
- Feature Alpha
- Feature Beta

### Fixed
- Bug fix Gamma

## [1.1.0] - 2026-03-10

### Changed
- Improvement Delta

## [1.0.0] - 2026-03-01

### Added
- Initial release
CHANGELOG

# --- Tests ---

echo "Running extract-changelog.sh tests..."
echo ""

# Test 1: Extract middle version
echo "Test 1: Extract middle version (1.1.0)"
output=$(CHANGELOG_FILE="$TMPDIR/CHANGELOG.md" bash "$EXTRACT" "1.1.0")
assert_contains "contains Changed heading" "### Changed" "$output"
assert_contains "contains Delta" "Improvement Delta" "$output"

# Test 2: Extract first version (after Unreleased)
echo "Test 2: Extract first released version (1.2.0)"
output=$(CHANGELOG_FILE="$TMPDIR/CHANGELOG.md" bash "$EXTRACT" "1.2.0")
assert_contains "contains Added heading" "### Added" "$output"
assert_contains "contains Feature Alpha" "Feature Alpha" "$output"
assert_contains "contains Fixed heading" "### Fixed" "$output"
assert_contains "contains Bug fix Gamma" "Bug fix Gamma" "$output"

# Test 3: Extract last version
echo "Test 3: Extract last version (1.0.0)"
output=$(CHANGELOG_FILE="$TMPDIR/CHANGELOG.md" bash "$EXTRACT" "1.0.0")
assert_contains "contains Added heading" "### Added" "$output"
assert_contains "contains Initial release" "Initial release" "$output"

# Test 4: Missing version returns fallback
echo "Test 4: Missing version returns fallback message"
output=$(CHANGELOG_FILE="$TMPDIR/CHANGELOG.md" bash "$EXTRACT" "9.9.9")
assert_contains "contains fallback" "No changelog entry found for version 9.9.9" "$output"

# Test 5: Missing version exits with code 0
echo "Test 5: Missing version exits with code 0"
set +e
CHANGELOG_FILE="$TMPDIR/CHANGELOG.md" bash "$EXTRACT" "9.9.9" > /dev/null 2>&1
exit_code=$?
set -e
assert_exit_code "exit code is 0" 0 "$exit_code"

# Test 6: No arguments exits with code 1
echo "Test 6: No arguments exits with code 1"
set +e
bash "$EXTRACT" > /dev/null 2>&1
exit_code=$?
set -e
assert_exit_code "exit code is 1" 1 "$exit_code"

# Test 7: Output does not include version heading itself
echo "Test 7: Output excludes the version heading line"
output=$(CHANGELOG_FILE="$TMPDIR/CHANGELOG.md" bash "$EXTRACT" "1.2.0")
if echo "$output" | grep -q "^## \[1.2.0\]"; then
    echo "  FAIL: output should not contain the version heading"
    FAIL=$((FAIL + 1))
else
    echo "  PASS: output does not contain the version heading"
    PASS=$((PASS + 1))
fi

# Test 8: Output does not include next version heading
echo "Test 8: Output excludes the next version heading"
output=$(CHANGELOG_FILE="$TMPDIR/CHANGELOG.md" bash "$EXTRACT" "1.2.0")
if echo "$output" | grep -q "^## \[1.1.0\]"; then
    echo "  FAIL: output should not contain the next version heading"
    FAIL=$((FAIL + 1))
else
    echo "  PASS: output does not contain the next version heading"
    PASS=$((PASS + 1))
fi

# Test 9: Unreleased section is not extracted by version number
echo "Test 9: Unreleased section is not matched"
output=$(CHANGELOG_FILE="$TMPDIR/CHANGELOG.md" bash "$EXTRACT" "Unreleased")
assert_contains "contains fallback" "No changelog entry found" "$output"

# Test 10: Works against real CHANGELOG.md
echo "Test 10: Real CHANGELOG.md extraction (0.7.8)"
output=$(bash "$EXTRACT" "0.7.8")
assert_contains "contains Fixed heading" "### Fixed" "$output"
assert_contains "contains symlink fix" "symlink rewrite" "$output"

# --- Summary ---

echo ""
echo "Results: $PASS passed, $FAIL failed"

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
