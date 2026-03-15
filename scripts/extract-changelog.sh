# Copyright (C) 2026 Techdelight BV
#
# Extract the changelog section for a given version from CHANGELOG.md.
#
# Usage: bash scripts/extract-changelog.sh <version>
# Example: bash scripts/extract-changelog.sh 0.8.0
#
# Reads CHANGELOG.md from the repository root (relative to the script location).
# Outputs the content between the version heading and the next version heading.
# If no matching section is found, outputs a fallback message.

set -euo pipefail

VERSION="${1:-}"

if [ -z "$VERSION" ]; then
    echo "Usage: $0 <version>" >&2
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CHANGELOG="${CHANGELOG_FILE:-${SCRIPT_DIR}/../CHANGELOG.md}"

if [ ! -f "$CHANGELOG" ]; then
    echo "CHANGELOG.md not found at: $CHANGELOG" >&2
    exit 1
fi

# Extract content between ## [VERSION] heading and the next ## [ heading.
# Uses awk to capture lines between the matching section boundaries.
# The `|| true` prevents set -e from aborting when awk exits non-zero (version not found).
content=$(awk -v ver="$VERSION" '
    BEGIN { found = 0; collecting = 0 }
    /^## \[/ {
        if (collecting) { exit }
        header = $0
        gsub(/^## \[/, "", header)
        gsub(/\].*/, "", header)
        if (header == ver) {
            found = 1
            collecting = 1
            next
        }
    }
    collecting { print }
    END { if (!found) exit 1 }
' "$CHANGELOG" || true)

if [ -z "$content" ]; then
    echo "No changelog entry found for version $VERSION."
    exit 0
fi

# Trim leading and trailing blank lines
echo "$content" | sed -e '/./,$!d' -e :a -e '/^\s*$/{ $d; N; ba; }'
