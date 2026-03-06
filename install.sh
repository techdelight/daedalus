#!/usr/bin/env bash
# Copyright (C) 2026 Techdelight BV
set -euo pipefail

# ── Defaults ──────────────────────────────────────────────────────────────────
PREFIX="$HOME/.local/share/daedalus"
CREATE_LINK=true
UNINSTALL=false
REPO_URL="https://github.com/techdelight/daedalus/archive/master.tar.gz"

# ── Runtime files to install alongside the binary ────────────────────────────
RUNTIME_FILES=(
    docker-compose.yml
    Dockerfile
    entrypoint.sh
    .claude.json
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

Downloads the Daedalus source, builds the binary via Docker, installs
runtime files to the prefix directory, and creates a PATH symlink.
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

if ! command -v docker &>/dev/null; then
    echo "Error: Docker is not installed or not in PATH." >&2
    echo "  Install Docker: https://docs.docker.com/get-docker/" >&2
    exit 1
fi

if ! docker info &>/dev/null 2>&1; then
    echo "Error: Docker daemon is not running." >&2
    echo "  Start Docker and try again." >&2
    exit 1
fi

echo "  curl: OK"
echo "  Docker: OK"

# ── Download source ──────────────────────────────────────────────────────────
WORK_DIR="$(mktemp -d)"
cleanup() { rm -rf "$WORK_DIR"; }
trap cleanup EXIT

echo ""
echo "Downloading Daedalus source..."
curl -fsSL "$REPO_URL" | tar xz -C "$WORK_DIR" --strip-components=1

# Verify runtime files exist in the download
for f in "${RUNTIME_FILES[@]}"; do
    if [[ ! -f "$WORK_DIR/$f" ]]; then
        echo "Error: required file '$f' not found in downloaded source." >&2
        exit 1
    fi
done

echo "  Source downloaded to temporary directory."

# ── Build ────────────────────────────────────────────────────────────────────
echo ""
echo "Building Daedalus binary..."
bash "$WORK_DIR/build.sh"

if [[ ! -f "$WORK_DIR/daedalus" ]]; then
    echo "Error: build did not produce the 'daedalus' binary." >&2
    exit 1
fi

# ── Install ──────────────────────────────────────────────────────────────────
echo ""
echo "Installing to $PREFIX..."
mkdir -p "$PREFIX"

cp "$WORK_DIR/daedalus" "$PREFIX/daedalus"
chmod 755 "$PREFIX/daedalus"

for f in "${RUNTIME_FILES[@]}"; do
    cp "$WORK_DIR/$f" "$PREFIX/$f"
done

echo "  Copied binary and ${#RUNTIME_FILES[@]} runtime files."

# Set the default data-dir to the resolved absolute path
DATA_DIR="$PREFIX/.cache"
sed "s|\"data-dir\": \"\"|\"data-dir\": \"$DATA_DIR\"|" "$PREFIX/config.json" > "$PREFIX/config.json.tmp" && mv "$PREFIX/config.json.tmp" "$PREFIX/config.json"

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
echo "Daedalus installed successfully."
echo ""
echo "  Location: $PREFIX/daedalus"
if [[ "$CREATE_LINK" == true ]]; then
    echo "  Symlink:  $LINK_DIR/daedalus"
fi
echo "  Config:   $PREFIX/config.json"
echo ""
echo "  Edit config.json to customize settings (data-dir, debug, etc.)."
echo "  Get started: daedalus my-app /path/to/project"
