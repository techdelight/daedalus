#!/usr/bin/env bash
# Copyright (C) 2026 Techdelight BV
set -euo pipefail

# ── Release tag (patched by CI pipeline) ──────────────────────────────────────
RELEASE_TAG="__RELEASE_TAG__"
# Fall back to "latest" when running unpatched source
[[ "$RELEASE_TAG" == "__RELEASE_TAG__" ]] && RELEASE_TAG="latest"

GITHUB_REPO="https://api.github.com/repos/techdelight/daedalus/releases"

# ── Runtime files to download alongside binaries ─────────────────────────────
RUNTIME_FILES=(
    claude.json
    docker-compose.yml
    Dockerfile
    entrypoint.sh
    settings.json
    logo.txt
    config.json
)

# ── Collect flags to forward to setup.sh ─────────────────────────────────────
FORWARD_ARGS=()
UNINSTALL=false

usage() {
    cat <<EOF
Usage: $0 [--prefix <dir>] [--no-link] [--uninstall] [--verbose]

Options:
  --prefix <dir>  Installation directory (default: ~/.local/share/daedalus)
  --no-link       Skip creating a symlink in PATH
  --uninstall     Remove Daedalus installation (prompts before deleting project data)
  --verbose       Enable shell tracing (set -x) for debugging

Downloads a pre-built Daedalus binary from the latest GitHub Release,
then invokes setup.sh to install runtime files and create a PATH symlink.

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
        --no-link)
            FORWARD_ARGS+=("--no-link")
            shift
            ;;
        --uninstall)
            UNINSTALL=true
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

# ── Uninstall shortcut (no downloads needed) ──────────────────────────────────
# setup.sh handles uninstall with just PREFIX — no WORK_DIR required.
# Download a minimal setup.sh from the release to perform the uninstall.
if [[ "$UNINSTALL" == true ]]; then
    if ! command -v curl &>/dev/null; then
        echo "Error: curl is not installed or not in PATH." >&2
        exit 1
    fi

    WORK_DIR="$(mktemp -d)"
    cleanup() { rm -rf "$WORK_DIR"; }
    trap cleanup EXIT

    # Resolve tag for download URL
    if [[ "$RELEASE_TAG" == "latest" ]]; then
        GITHUB_API="${GITHUB_REPO}/latest"
    else
        GITHUB_API="${GITHUB_REPO}/tags/${RELEASE_TAG}"
    fi

    RELEASE_JSON="$(curl -fsSL "$GITHUB_API")"
    TAG="$(echo "$RELEASE_JSON" | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"

    if [[ -z "$TAG" ]]; then
        echo "Error: could not determine release tag." >&2
        exit 1
    fi

    DOWNLOAD_BASE="https://github.com/techdelight/daedalus/releases/download/${TAG}"
    curl -fsSL -o "$WORK_DIR/setup.sh" "${DOWNLOAD_BASE}/setup.sh"
    chmod 755 "$WORK_DIR/setup.sh"

    export WORK_DIR
    exec "$WORK_DIR/setup.sh" "${FORWARD_ARGS[@]}"
fi

# ── Prerequisite checks ──────────────────────────────────────────────────────
echo "Checking prerequisites..."

if ! command -v curl &>/dev/null; then
    echo "Error: curl is not installed or not in PATH." >&2
    exit 1
fi

echo "  curl: OK"

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

echo "  Platform: ${OS}/${ARCH}"

# ── Fetch release tag ─────────────────────────────────────────────────────
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

# ── Download binary and runtime files ────────────────────────────────────────
WORK_DIR="$(mktemp -d)"
cleanup() { rm -rf "$WORK_DIR"; }
trap cleanup EXIT

BINARY_NAME="daedalus-${OS}-${ARCH}"
MCP_BINARY_NAME="skill-catalog-mcp-${OS}-${ARCH}"
echo ""
echo "Downloading ${BINARY_NAME}..."
curl -fsSL -o "$WORK_DIR/daedalus" "${DOWNLOAD_BASE}/${BINARY_NAME}"
chmod 755 "$WORK_DIR/daedalus"

echo "Downloading ${MCP_BINARY_NAME}..."
curl -fsSL -o "$WORK_DIR/skill-catalog-mcp" "${DOWNLOAD_BASE}/${MCP_BINARY_NAME}"
chmod 755 "$WORK_DIR/skill-catalog-mcp"

echo "Downloading setup.sh..."
curl -fsSL -o "$WORK_DIR/setup.sh" "${DOWNLOAD_BASE}/setup.sh"
chmod 755 "$WORK_DIR/setup.sh"

echo "Downloading runtime files..."
for f in "${RUNTIME_FILES[@]}"; do
    curl -fsSL -o "$WORK_DIR/$f" "${DOWNLOAD_BASE}/${f}"
done

# Patch version into downloaded config.json for setup.sh
NEW_VERSION="${TAG#v}"
sed -i "s/\"version\": *\"\"/\"version\": \"$NEW_VERSION\"/" "$WORK_DIR/config.json"

echo "  Downloaded 2 binaries, setup.sh, and ${#RUNTIME_FILES[@]} runtime files."

# ── Hand off to setup.sh ─────────────────────────────────────────────────────
export WORK_DIR
exec "$WORK_DIR/setup.sh" "${FORWARD_ARGS[@]}"
