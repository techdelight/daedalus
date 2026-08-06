#!/usr/bin/env bash
# Copyright (C) 2026 Techdelight BV
set -euo pipefail

# ── Defaults ──────────────────────────────────────────────────────────────────
PREFIX="$HOME/.local/share/daedalus"
CREATE_LINK=true
LINK_NAME="daedalus"
UNINSTALL=false

# Optional config-isolation overrides for parallel test installs. Empty
# means "use the daedalus default" (claude-run-/claude-/techdelight/...).
CONTAINER_PREFIX=""
IMAGE_PREFIX_OVERRIDE=""

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
Usage: $0 [--prefix <dir>] [--link-name <name>] [--no-link]
          [--container-prefix <p>] [--image-prefix <p>]
          [--uninstall] [--verbose]

Install options:
  --prefix <dir>           Installation directory (default: ~/.local/share/daedalus)
  --link-name <name>       Symlink name in ~/.local/bin (default: daedalus)
  --no-link                Skip creating a symlink in PATH

Test-isolation options (for parallel installs alongside production):
  --container-prefix <p>   Override docker container name prefix (default: claude-run-)
  --image-prefix <p>       Override docker image prefix (default: techdelight/claude-runner)

Maintenance:
  --uninstall              Remove Daedalus installation (prompts before deleting project data)
  --verbose                Enable shell tracing (set -x) for debugging

Installs Daedalus binaries and runtime files from WORK_DIR to the prefix
directory, merges configuration on upgrade, and creates a PATH symlink.

For local-build test installs (e.g. while developing the runner stack):

  ./build.sh
  WORK_DIR=\$PWD ./setup.sh \\
      --prefix ~/.local/share/daedalus-test \\
      --link-name daedalus-test \\
      --container-prefix test-run- \\
      --image-prefix test/claude-runner

This script is downloaded as a release asset and invoked by install.sh.
Set WORK_DIR to the directory containing the downloaded assets (or
build artefacts, for a local-source install).
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
        --link-name)
            [[ $# -lt 2 ]] && { echo "Error: --link-name requires a name argument." >&2; exit 1; }
            LINK_NAME="$2"
            shift 2
            ;;
        --no-link)
            CREATE_LINK=false
            shift
            ;;
        --container-prefix)
            [[ $# -lt 2 ]] && { echo "Error: --container-prefix requires a prefix argument." >&2; exit 1; }
            CONTAINER_PREFIX="$2"
            shift 2
            ;;
        --image-prefix)
            [[ $# -lt 2 ]] && { echo "Error: --image-prefix requires a prefix argument." >&2; exit 1; }
            IMAGE_PREFIX_OVERRIDE="$2"
            shift 2
            ;;
        --uninstall)
            UNINSTALL=true
            shift
            ;;
        --verbose)
            set -x
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

    # Remove symlink. Use --link-name (default: "daedalus") so a test
    # install installed as `daedalus-test` is uninstalled cleanly.
    LINK="$HOME/.local/bin/$LINK_NAME"
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
    rm -f "$PREFIX/daedalus-runner"
    rm -f "$PREFIX/daedalus-coordinator"
    rm -f "$PREFIX/skill-catalog-mcp"
    rm -f "$PREFIX/project-mgmt-mcp"
    rm -f "$PREFIX/setup.sh"
    echo "  Removed binaries and runtime files."

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

# ── Validate WORK_DIR ─────────────────────────────────────────────────────────
if [[ -z "${WORK_DIR:-}" || ! -d "$WORK_DIR" ]]; then
    echo "Error: WORK_DIR is not set or does not exist." >&2
    echo "This script is meant to be invoked by install.sh, not run directly." >&2
    exit 1
fi

# ── Detect existing installation ────────────────────────────────────────────
INSTALLED_VERSION=""
UPGRADING=false
if [[ -f "$PREFIX/config.json" ]]; then
    INSTALLED_VERSION="$(grep '"version"' "$PREFIX/config.json" | sed 's/.*"version": *"\([^"]*\)".*/\1/' || true)"
    if [[ -n "$INSTALLED_VERSION" ]]; then
        UPGRADING=true
    fi
fi

# ── Determine new version ──────────────────────────────────────────────────
# install.sh patches the version into WORK_DIR/config.json before calling us
NEW_VERSION="$(grep '"version"' "$WORK_DIR/config.json" | sed 's/.*"version": *"\([^"]*\)".*/\1/' || true)"
if [[ -z "$NEW_VERSION" ]]; then
    NEW_VERSION="unknown"
fi

# ── Install ──────────────────────────────────────────────────────────────────
if [[ "$UPGRADING" == true ]]; then
    echo ""
    echo "Upgrading Daedalus from $INSTALLED_VERSION to $NEW_VERSION..."

    # Preserve user settings from existing config
    OLD_CONFIG="$PREFIX/config.json"
    OLD_DATA_DIR="$(grep '"data-dir"' "$OLD_CONFIG" | sed 's/.*"data-dir": *"\([^"]*\)".*/\1/' || true)"
    OLD_DEBUG="$(grep '"debug"' "$OLD_CONFIG" | sed 's/.*"debug": *\([a-z]*\).*/\1/' || true)"
    OLD_IMAGE_PREFIX="$(grep '"image-prefix"' "$OLD_CONFIG" | sed 's/.*"image-prefix": *"\([^"]*\)".*/\1/' || true)"
    OLD_CONTAINER_PREFIX="$(grep '"container-prefix"' "$OLD_CONFIG" | sed 's/.*"container-prefix": *"\([^"]*\)".*/\1/' || true)"
    OLD_LOG_FILE="$(grep '"log-file"' "$OLD_CONFIG" | sed 's/.*"log-file": *"\([^"]*\)".*/\1/' || true)"
else
    echo ""
    echo "Installing to $PREFIX..."
fi

mkdir -p "$PREFIX"

cp "$WORK_DIR/daedalus" "$PREFIX/daedalus"
chmod 755 "$PREFIX/daedalus"
cp "$WORK_DIR/skill-catalog-mcp" "$PREFIX/skill-catalog-mcp"
chmod 755 "$PREFIX/skill-catalog-mcp"
cp "$WORK_DIR/project-mgmt-mcp" "$PREFIX/project-mgmt-mcp"
chmod 755 "$PREFIX/project-mgmt-mcp"
# daedalus-runner is the in-container PID-1 binary the runner path
# launches. The Dockerfile COPYs it from the build context (= PREFIX)
# at image-build time, so it has to be staged here.
if [[ -f "$WORK_DIR/daedalus-runner" ]]; then
    cp "$WORK_DIR/daedalus-runner" "$PREFIX/daedalus-runner"
    chmod 755 "$PREFIX/daedalus-runner"
fi
# daedalus-coordinator is the host-side daemon that owns runner-attached
# container lifecycles. `daedalus coordinator start` expects it to sit
# next to the main binary in PREFIX. Conditional because older release
# tarballs won't ship it.
if [[ -f "$WORK_DIR/daedalus-coordinator" ]]; then
    cp "$WORK_DIR/daedalus-coordinator" "$PREFIX/daedalus-coordinator"
    chmod 755 "$PREFIX/daedalus-coordinator"
fi

# Copy setup.sh itself so users can run uninstall locally
SELF="$(cd "$(dirname "$0")" && pwd)/$(basename "$0")"
cp "$SELF" "$PREFIX/setup.sh"
chmod 755 "$PREFIX/setup.sh"

for f in "${RUNTIME_FILES[@]}"; do
    # Config is written separately with merged settings
    if [[ "$f" == "config.json" ]]; then
        continue
    fi
    cp "$WORK_DIR/$f" "$PREFIX/$f"
done

# Write config.json with version and preserved/default settings.
# CLI flags (--container-prefix, --image-prefix) win over the existing
# config; otherwise the previous value is preserved on upgrade, and a
# fresh install gets the documented defaults.
if [[ "$UPGRADING" == true ]]; then
    DATA_DIR="${OLD_DATA_DIR}"
    DEBUG="${OLD_DEBUG:-false}"
    IMAGE_PREFIX="${IMAGE_PREFIX_OVERRIDE:-${OLD_IMAGE_PREFIX:-techdelight/claude-runner}}"
    CONTAINER_PREFIX_VAL="${CONTAINER_PREFIX:-${OLD_CONTAINER_PREFIX:-}}"
    LOG_FILE="${OLD_LOG_FILE:-$DATA_DIR/daedalus.log}"
else
    DATA_DIR="$PREFIX/.cache"
    DEBUG="false"
    IMAGE_PREFIX="${IMAGE_PREFIX_OVERRIDE:-techdelight/claude-runner}"
    CONTAINER_PREFIX_VAL="$CONTAINER_PREFIX"
    LOG_FILE="$DATA_DIR/daedalus.log"
fi

cat > "$PREFIX/config.json" <<EOCFG
{
  "version": "$NEW_VERSION",
  "data-dir": "$DATA_DIR",
  "debug": $DEBUG,
  "image-prefix": "$IMAGE_PREFIX",
  "container-prefix": "$CONTAINER_PREFIX_VAL",
  "log-file": "$LOG_FILE"
}
EOCFG

echo "  Copied 3 binaries and $((${#RUNTIME_FILES[@]} - 1)) runtime files."
echo "  Configuration: $PREFIX/config.json"

# ── Symlink ──────────────────────────────────────────────────────────────────
if [[ "$CREATE_LINK" == true ]]; then
    LINK_DIR="$HOME/.local/bin"
    mkdir -p "$LINK_DIR"

    ln -sf "$PREFIX/daedalus" "$LINK_DIR/$LINK_NAME"
    echo "  Symlinked $LINK_DIR/$LINK_NAME -> $PREFIX/daedalus"

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
    echo "  Symlink:  $LINK_DIR/$LINK_NAME"
fi
echo "  Config:   $PREFIX/config.json"
if [[ -n "$CONTAINER_PREFIX_VAL" ]]; then
    echo "  Container prefix: ${CONTAINER_PREFIX_VAL:-claude-run-}"
fi
echo ""
echo "  Note: Docker is required at runtime to run projects."
echo "  Edit config.json to customize settings (data-dir, debug, etc.)."
echo ""
echo "  Get started:"
echo "    1. $LINK_NAME init /path/to/project    # scaffold docs + show next steps"
echo "    2. $LINK_NAME my-app /path/to/project  # register and start a project"
echo "  Run '$LINK_NAME --help' for the full command reference."
