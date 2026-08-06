#!/usr/bin/env bash
# Copyright (C) 2026 Techdelight BV
set -euo pipefail

# ── Release tag (patched by CI pipeline) ──────────────────────────────────────
RELEASE_TAG="__RELEASE_TAG__"
# Fall back to "latest" when running unpatched source
[[ "$RELEASE_TAG" == "__RELEASE_TAG__" ]] && RELEASE_TAG="latest"

GITHUB_REPO="https://api.github.com/repos/techdelight/daedalus/releases"

# ── Test/offline hook ─────────────────────────────────────────────────────────
# When DAEDALUS_ARCHIVE_DIR is set, install from a local directory holding
# daedalus-<os>-<arch>.tar.gz + SHA256SUMS.txt instead of downloading from a
# GitHub Release. scripts/test-release-bundle.sh uses this to exercise the real
# checksum-verify + extract + setup.sh path end to end without touching GitHub.
LOCAL_ARCHIVE_DIR="${DAEDALUS_ARCHIVE_DIR:-}"

# ── Collect flags to forward to setup.sh ─────────────────────────────────────
FORWARD_ARGS=()

usage() {
    cat <<EOF
Usage: $0 [--prefix <dir>] [--link-name <name>] [--no-link]
          [--container-prefix <p>] [--image-prefix <p>]
          [--uninstall] [--verbose]

Install options:
  --prefix <dir>           Installation directory (default: ~/.local/share/daedalus)
  --link-name <name>       Symlink name in ~/.local/bin (default: daedalus)
  --no-link                Skip creating a symlink in PATH

Test-isolation options (parallel install alongside production):
  --container-prefix <p>   Override docker container name prefix (default: claude-run-)
  --image-prefix <p>       Override docker image prefix (default: techdelight/claude-runner)

Maintenance:
  --uninstall              Remove Daedalus installation (prompts before deleting project data)
  --verbose                Enable shell tracing (set -x) for debugging

Downloads a single pre-built Daedalus release archive
(daedalus-<os>-<arch>.tar.gz) from the GitHub Release, verifies it against
SHA256SUMS.txt, extracts it, then invokes the bundled setup.sh to install the
binaries + runtime files and create a PATH symlink.

The RELEASE_TAG variable is baked in during the release pipeline.
Current RELEASE_TAG: ${RELEASE_TAG}
EOF
    exit 0
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --prefix)
            [[ $# -lt 2 ]] && { echo "Error: --prefix requires a directory argument." >&2; exit 1; }
            FORWARD_ARGS+=("--prefix" "$2")
            shift 2
            ;;
        --link-name)
            [[ $# -lt 2 ]] && { echo "Error: --link-name requires a name argument." >&2; exit 1; }
            FORWARD_ARGS+=("--link-name" "$2")
            shift 2
            ;;
        --no-link)
            FORWARD_ARGS+=("--no-link")
            shift
            ;;
        --container-prefix)
            [[ $# -lt 2 ]] && { echo "Error: --container-prefix requires a prefix argument." >&2; exit 1; }
            FORWARD_ARGS+=("--container-prefix" "$2")
            shift 2
            ;;
        --image-prefix)
            [[ $# -lt 2 ]] && { echo "Error: --image-prefix requires a prefix argument." >&2; exit 1; }
            FORWARD_ARGS+=("--image-prefix" "$2")
            shift 2
            ;;
        --uninstall)
            FORWARD_ARGS+=("--uninstall")
            shift
            ;;
        --verbose)
            set -x
            FORWARD_ARGS+=("--verbose")
            shift
            ;;
        --help|-h)
            usage
            ;;
        *)
            echo "Error: unknown option '$1'. Use --help for usage." >&2
            exit 1
            ;;
    esac
done

# ── Portable sha256 over a single file → bare hash ───────────────────────────
sha256_of() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    else
        echo "Error: neither sha256sum nor shasum is available to verify the download." >&2
        exit 1
    fi
}

# ── Detect OS and architecture ───────────────────────────────────────────────
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$OS" in
    linux)  OS="linux" ;;
    darwin) OS="darwin" ;;
    *)
        echo "Error: unsupported operating system '$OS'." >&2
        exit 1
        ;;
esac

case "$ARCH" in
    x86_64)  ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    arm64)   ARCH="arm64" ;;
    *)
        echo "Error: unsupported architecture '$ARCH'." >&2
        exit 1
        ;;
esac

ARCHIVE_NAME="daedalus-${OS}-${ARCH}.tar.gz"
SUMS_NAME="SHA256SUMS.txt"

# ── Working directory ─────────────────────────────────────────────────────────
WORK_DIR="$(mktemp -d)"
cleanup() { rm -rf "$WORK_DIR"; }
trap cleanup EXIT

ARCHIVE_PATH="$WORK_DIR/$ARCHIVE_NAME"
SUMS_PATH="$WORK_DIR/$SUMS_NAME"

# ── Fetch the archive + checksums (network or local) ──────────────────────────
if [[ -n "$LOCAL_ARCHIVE_DIR" ]]; then
    echo "Installing from local archive directory: $LOCAL_ARCHIVE_DIR"
    if [[ ! -f "$LOCAL_ARCHIVE_DIR/$ARCHIVE_NAME" ]]; then
        echo "Error: $ARCHIVE_NAME not found in $LOCAL_ARCHIVE_DIR." >&2
        exit 1
    fi
    if [[ ! -f "$LOCAL_ARCHIVE_DIR/$SUMS_NAME" ]]; then
        echo "Error: $SUMS_NAME not found in $LOCAL_ARCHIVE_DIR." >&2
        exit 1
    fi
    cp "$LOCAL_ARCHIVE_DIR/$ARCHIVE_NAME" "$ARCHIVE_PATH"
    cp "$LOCAL_ARCHIVE_DIR/$SUMS_NAME" "$SUMS_PATH"
    echo "  Platform: ${OS}/${ARCH}"
else
    # ── Prerequisite checks ──────────────────────────────────────────────────
    echo "Checking prerequisites..."
    if ! command -v curl &>/dev/null; then
        echo "Error: curl is not installed or not in PATH." >&2
        exit 1
    fi
    echo "  curl: OK"
    echo "  Platform: ${OS}/${ARCH}"

    # ── Resolve release tag ──────────────────────────────────────────────────
    echo ""
    if [[ "$RELEASE_TAG" == "latest" ]]; then
        echo "Fetching latest stable release..."
        GITHUB_API="${GITHUB_REPO}/latest"
    else
        echo "Fetching release: ${RELEASE_TAG}..."
        GITHUB_API="${GITHUB_REPO}/tags/${RELEASE_TAG}"
    fi

    RELEASE_JSON="$(curl -fsSL "$GITHUB_API")"
    TAG="$(echo "$RELEASE_JSON" | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"

    if [[ -z "$TAG" ]]; then
        echo "Error: could not determine release tag." >&2
        exit 1
    fi
    echo "  Release: $TAG"

    DOWNLOAD_BASE="https://github.com/techdelight/daedalus/releases/download/${TAG}"

    echo ""
    echo "Downloading ${ARCHIVE_NAME}..."
    if ! curl -fsSL -o "$ARCHIVE_PATH" "${DOWNLOAD_BASE}/${ARCHIVE_NAME}"; then
        echo "Error: failed to download ${ARCHIVE_NAME} from release ${TAG}." >&2
        echo "  This release may predate the bundled-archive format (pre-v0.44.0)," >&2
        echo "  or may not publish an asset for ${OS}/${ARCH}." >&2
        exit 1
    fi

    echo "Downloading ${SUMS_NAME}..."
    if ! curl -fsSL -o "$SUMS_PATH" "${DOWNLOAD_BASE}/${SUMS_NAME}"; then
        echo "Error: failed to download ${SUMS_NAME} from release ${TAG}." >&2
        echo "  Cannot verify the archive without it; aborting." >&2
        exit 1
    fi
fi

# ── Verify the archive against SHA256SUMS.txt ─────────────────────────────────
echo ""
echo "Verifying ${ARCHIVE_NAME} against ${SUMS_NAME}..."
EXPECTED_SUM="$(awk -v f="$ARCHIVE_NAME" '$2 == f {print $1}' "$SUMS_PATH")"
if [[ -z "$EXPECTED_SUM" ]]; then
    echo "Error: no checksum for ${ARCHIVE_NAME} found in ${SUMS_NAME}." >&2
    exit 1
fi
ACTUAL_SUM="$(sha256_of "$ARCHIVE_PATH")"
if [[ "$EXPECTED_SUM" != "$ACTUAL_SUM" ]]; then
    echo "Error: checksum mismatch for ${ARCHIVE_NAME}." >&2
    echo "  expected: $EXPECTED_SUM" >&2
    echo "  actual:   $ACTUAL_SUM" >&2
    echo "  The download may be corrupt or tampered with; aborting." >&2
    exit 1
fi
echo "  Checksum OK: $ACTUAL_SUM"

# ── Extract the archive (flat layout) ─────────────────────────────────────────
EXTRACT_DIR="$WORK_DIR/extracted"
mkdir -p "$EXTRACT_DIR"
if ! tar -xzf "$ARCHIVE_PATH" -C "$EXTRACT_DIR"; then
    echo "Error: failed to extract ${ARCHIVE_NAME}." >&2
    exit 1
fi

if [[ ! -f "$EXTRACT_DIR/setup.sh" ]]; then
    echo "Error: extracted archive does not contain setup.sh." >&2
    exit 1
fi
chmod 755 "$EXTRACT_DIR/setup.sh"

echo "  Extracted release archive."

# ── Hand off to setup.sh ─────────────────────────────────────────────────────
# The archive already carries a config.json with the version baked in at
# package time (scripts/package-release.sh), so no version patch is needed here.
export WORK_DIR="$EXTRACT_DIR"
exec "$EXTRACT_DIR/setup.sh" ${FORWARD_ARGS[@]+"${FORWARD_ARGS[@]}"}
