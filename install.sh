#!/usr/bin/env bash
# Copyright (C) 2026 Techdelight BV
set -euo pipefail

# ── Defaults ──────────────────────────────────────────────────────────────────
PREFIX="$HOME/.local/share/daedalus"
CREATE_LINK=true
UNINSTALL=false
GITHUB_API="https://api.github.com/repos/techdelight/daedalus/releases/latest"

# ── Runtime files to install alongside the binary ────────────────────────────
RUNTIME_FILES=(
    claude.json
    docker-compose.yml
    Dockerfile
    entrypoint.sh
    settings.json
    logo.txt
    config.json
)

# ── Argument parsing ─────────────────────────────────────────────────────────
usage() {
    cat <<EOF
Usage: $0 [--prefix <dir>] [--no-link] [--uninstall]

Options:
  --prefix <dir>  Installation directory (default: ~/.local/share/daedalus)
  --no-link       Skip creating a symlink in PATH
  --uninstall     Remove Daedalus installation (prompts before deleting project data)

Downloads a pre-built Daedalus binary from the latest GitHub Release,
installs runtime files to the prefix directory, and creates a PATH symlink.
EOF
    exit 0
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --prefix)
            [[ $# -lt 2 ]] && { echo "Error: --prefix requires a directory argument." >&2; exit 1; }
            PREFIX="$2"
            shift 2
            ;;
        --no-link)
            CREATE_LINK=false
            shift
            ;;
        --uninstall)
            UNINSTALL=true
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

# ── Uninstall ─────────────────────────────────────────────────────────────────
if [[ "$UNINSTALL" == true ]]; then
    if [[ ! -d "$PREFIX" ]]; then
        echo "Nothing to uninstall: $PREFIX does not exist."
        exit 0
    fi

    echo "Uninstalling Daedalus from $PREFIX..."

    # Remove symlink
    LINK="$HOME/.local/bin/daedalus"
    if [[ -L "$LINK" ]]; then
        rm -f "$LINK"
        echo "  Removed symlink $LINK"
    fi

    # Prompt before removing project data
    if [[ -d "$PREFIX/.cache" ]]; then
        printf "Remove project data in %s/.cache/? (y/N) " "$PREFIX"
        read -r answer
        if [[ "$answer" =~ ^[Yy]$ ]]; then
            rm -rf "$PREFIX/.cache"
            echo "  Removed project data."
        else
            echo "  Kept project data."
        fi
    fi

    # Remove runtime files and binary
    for f in "${RUNTIME_FILES[@]}"; do
        rm -f "$PREFIX/$f"
    done
    rm -f "$PREFIX/daedalus"
    echo "  Removed binary and runtime files."

    # Remove prefix directory if empty
    rmdir "$PREFIX" 2>/dev/null && echo "  Removed empty directory $PREFIX" || true

    echo ""
    echo "Daedalus uninstalled."
    exit 0
fi

# ── Reject root ───────────────────────────────────────────────────────────────
if [[ $EUID -eq 0 ]]; then
    echo "Error: do not run this script as root. Install under your own user account." >&2
    exit 1
fi

# ── Prerequisite checks ─────────────────────────────────────────────────────
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

# ── Fetch latest release tag ────────────────────────────────────────────────
echo ""
echo "Fetching latest release..."

RELEASE_JSON="$(curl -fsSL "$GITHUB_API")"
TAG="$(echo "$RELEASE_JSON" | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"

if [[ -z "$TAG" ]]; then
    echo "Error: could not determine latest release tag." >&2
    exit 1
fi

echo "  Latest release: $TAG"

DOWNLOAD_BASE="https://github.com/techdelight/daedalus/releases/download/${TAG}"

# ── Download binary and runtime files ────────────────────────────────────────
WORK_DIR="$(mktemp -d)"
cleanup() { rm -rf "$WORK_DIR"; }
trap cleanup EXIT

BINARY_NAME="daedalus-${OS}-${ARCH}"
echo ""
echo "Downloading ${BINARY_NAME}..."
curl -fsSL -o "$WORK_DIR/daedalus" "${DOWNLOAD_BASE}/${BINARY_NAME}"
chmod 755 "$WORK_DIR/daedalus"

echo "Downloading runtime files..."
for f in "${RUNTIME_FILES[@]}"; do
    curl -fsSL -o "$WORK_DIR/$f" "${DOWNLOAD_BASE}/${f}"
done

echo "  Downloaded binary and ${#RUNTIME_FILES[@]} runtime files."

# ── Detect existing installation ────────────────────────────────────────────
INSTALLED_VERSION=""
UPGRADING=false
if [[ -f "$PREFIX/config.json" ]]; then
    INSTALLED_VERSION="$(grep '"version"' "$PREFIX/config.json" | sed 's/.*"version": *"\([^"]*\)".*/\1/' || true)"
    if [[ -n "$INSTALLED_VERSION" ]]; then
        UPGRADING=true
    fi
fi

# ── Install ──────────────────────────────────────────────────────────────────
NEW_VERSION="${TAG#v}"

if [[ "$UPGRADING" == true ]]; then
    echo ""
    echo "Upgrading Daedalus from $INSTALLED_VERSION to $NEW_VERSION..."

    # Preserve user settings from existing config
    OLD_CONFIG="$PREFIX/config.json"
    OLD_DATA_DIR="$(grep '"data-dir"' "$OLD_CONFIG" | sed 's/.*"data-dir": *"\([^"]*\)".*/\1/' || true)"
    OLD_DEBUG="$(grep '"debug"' "$OLD_CONFIG" | sed 's/.*"debug": *\([a-z]*\).*/\1/' || true)"
    OLD_NO_TMUX="$(grep '"no-tmux"' "$OLD_CONFIG" | sed 's/.*"no-tmux": *\([a-z]*\).*/\1/' || true)"
    OLD_IMAGE_PREFIX="$(grep '"image-prefix"' "$OLD_CONFIG" | sed 's/.*"image-prefix": *"\([^"]*\)".*/\1/' || true)"
    OLD_LOG_FILE="$(grep '"log-file"' "$OLD_CONFIG" | sed 's/.*"log-file": *"\([^"]*\)".*/\1/' || true)"
else
    echo ""
    echo "Installing to $PREFIX..."
fi

mkdir -p "$PREFIX"

cp "$WORK_DIR/daedalus" "$PREFIX/daedalus"
chmod 755 "$PREFIX/daedalus"

for f in "${RUNTIME_FILES[@]}"; do
    # Config is written separately with merged settings
    if [[ "$f" == "config.json" ]]; then
        continue
    fi
    cp "$WORK_DIR/$f" "$PREFIX/$f"
done

# Write config.json with version and preserved/default settings
if [[ "$UPGRADING" == true ]]; then
    DATA_DIR="${OLD_DATA_DIR}"
    DEBUG="${OLD_DEBUG:-false}"
    NO_TMUX="${OLD_NO_TMUX:-false}"
    IMAGE_PREFIX="${OLD_IMAGE_PREFIX:-techdelight/claude-runner}"
    LOG_FILE="${OLD_LOG_FILE:-$DATA_DIR/daedalus.log}"
else
    DATA_DIR="$PREFIX/.cache"
    DEBUG="false"
    NO_TMUX="false"
    IMAGE_PREFIX="techdelight/claude-runner"
    LOG_FILE="$DATA_DIR/daedalus.log"
fi

cat > "$PREFIX/config.json" <<EOCFG
{
  "version": "$NEW_VERSION",
  "data-dir": "$DATA_DIR",
  "debug": $DEBUG,
  "no-tmux": $NO_TMUX,
  "image-prefix": "$IMAGE_PREFIX",
  "log-file": "$LOG_FILE"
}
EOCFG

echo "  Copied binary and $((${#RUNTIME_FILES[@]} - 1)) runtime files."
echo "  Configuration: $PREFIX/config.json"

# ── Symlink ──────────────────────────────────────────────────────────────────
if [[ "$CREATE_LINK" == true ]]; then
    LINK_DIR="$HOME/.local/bin"
    mkdir -p "$LINK_DIR"

    ln -sf "$PREFIX/daedalus" "$LINK_DIR/daedalus"
    echo "  Symlinked $LINK_DIR/daedalus -> $PREFIX/daedalus"

    # Check if the link directory is on PATH
    if [[ ":$PATH:" != *":$LINK_DIR:"* ]]; then
        echo ""
        echo "  Note: $LINK_DIR is not on your PATH."
        echo "  Add it with: export PATH=\"$LINK_DIR:\$PATH\""
    fi
fi

# ── Summary ──────────────────────────────────────────────────────────────────
echo ""
if [[ "$UPGRADING" == true ]]; then
    echo "Daedalus upgraded successfully from $INSTALLED_VERSION to $NEW_VERSION."
else
    echo "Daedalus installed successfully."
fi
echo ""
echo "  Location: $PREFIX/daedalus"
if [[ "$CREATE_LINK" == true ]]; then
    echo "  Symlink:  $LINK_DIR/daedalus"
fi
echo "  Config:   $PREFIX/config.json"
echo ""
echo "  Note: Docker is required at runtime to run projects."
echo "  Edit config.json to customize settings (data-dir, debug, etc.)."
echo "  Get started: daedalus my-app /path/to/project"
