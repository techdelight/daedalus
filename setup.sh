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

# Every binary the payload may carry (runner/coordinator are optional on old
# archives). Used by install, migration, and uninstall so they stay in sync.
BINARIES=(
    daedalus
    skill-catalog-mcp
    project-mgmt-mcp
    guild-mcp
    daedalus-runner
    daedalus-coordinator
    daedalus-control
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

Installs Daedalus into a versioned layout under the prefix directory:

  \$PREFIX/versions/<version>/   one full payload per installed version
  \$PREFIX/current  -> versions/<active>   (the PATH symlink resolves through it)
  \$PREFIX/previous -> versions/<prior>    (rollback target)
  \$PREFIX/.cache/  shared data dir (registry etc.), never per-version

Upgrades install alongside existing versions and flip \$PREFIX/current, so prior
versions stay in place for 'daedalus version use/rollback'. A legacy flat
install (binaries directly under \$PREFIX) is migrated into versions/<old>/ on
the first versioned upgrade.

For local-build test installs (e.g. while developing the runner stack):

  ./build.sh
  WORK_DIR=\$PWD ./setup.sh \\
      --prefix ~/.local/share/daedalus-test \\
      --link-name daedalus-test \\
      --container-prefix test-run- \\
      --image-prefix test/claude-runner

This script is downloaded as part of the release archive and invoked by
install.sh. Set WORK_DIR to the directory containing the extracted assets (or
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

# ── Layout paths ─────────────────────────────────────────────────────────────
VERSIONS_DIR="$PREFIX/versions"
CURRENT_LINK="$PREFIX/current"
PREVIOUS_LINK="$PREFIX/previous"

# Read the "version" field from a config.json; empty if missing/unset.
read_config_version() {
    local file="$1"
    [[ -f "$file" ]] || { echo ""; return; }
    grep '"version"' "$file" 2>/dev/null | sed 's/.*"version": *"\([^"]*\)".*/\1/' | head -1 || true
}

# Replace a symlink (or stray dir/file) at $1 with a symlink to $2.
# $2 is kept relative to $PREFIX so the tree is relocatable.
link_replace() {
    rm -rf "$1"
    ln -s "$2" "$1"
}

# ── Uninstall ─────────────────────────────────────────────────────────────────
if [[ "$UNINSTALL" == true ]]; then
    if [[ ! -d "$PREFIX" ]]; then
        echo "Nothing to uninstall: $PREFIX does not exist."
        exit 0
    fi

    echo "Uninstalling Daedalus from $PREFIX..."

    # Remove PATH symlink. Use --link-name (default: "daedalus") so a test
    # install installed as `daedalus-test` is uninstalled cleanly.
    LINK="$HOME/.local/bin/$LINK_NAME"
    if [[ -L "$LINK" ]]; then
        rm -f "$LINK"
        echo "  Removed symlink $LINK"
    fi

    # Prompt before removing project data (shared across versions).
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

    # Remove the versioned layout: current/previous links, all versions, plus
    # any legacy flat files left directly under $PREFIX.
    rm -f "$CURRENT_LINK" "$PREVIOUS_LINK"
    if [[ -d "$VERSIONS_DIR" ]]; then
        rm -rf "$VERSIONS_DIR"
        echo "  Removed all installed versions."
    fi
    for f in "${BINARIES[@]}" setup.sh "${RUNTIME_FILES[@]}"; do
        rm -f "$PREFIX/$f"
    done
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

# ── Determine new version ──────────────────────────────────────────────────
# install.sh/package-release bake the version into WORK_DIR/config.json.
NEW_VERSION="$(read_config_version "$WORK_DIR/config.json")"
if [[ -z "$NEW_VERSION" ]]; then
    NEW_VERSION="unknown"
fi

# ── Migrate a legacy flat install into the versioned layout ──────────────────
# Older installs placed binaries directly under $PREFIX. On the first versioned
# upgrade, move that payload into versions/<old-version>/ and point `current` at
# it, so the existing install keeps working and becomes a rollback target.
if [[ -f "$PREFIX/daedalus" && ! -L "$PREFIX/daedalus" ]]; then
    OLD_VERSION="$(read_config_version "$PREFIX/config.json")"
    [[ -z "$OLD_VERSION" ]] && OLD_VERSION="legacy"
    LEGACY_DIR="$VERSIONS_DIR/$OLD_VERSION"
    echo ""
    echo "Migrating legacy flat install ($OLD_VERSION) into $LEGACY_DIR..."
    mkdir -p "$LEGACY_DIR"
    for f in "${BINARIES[@]}" setup.sh "${RUNTIME_FILES[@]}"; do
        if [[ -e "$PREFIX/$f" && ! -L "$PREFIX/$f" ]]; then
            mv "$PREFIX/$f" "$LEGACY_DIR/$f"
        fi
    done
    link_replace "$CURRENT_LINK" "versions/$OLD_VERSION"
    echo "  Migrated. Prior version preserved as $OLD_VERSION."
fi

# ── Detect existing (versioned) installation ─────────────────────────────────
PREV_CURRENT_TARGET=""
INSTALLED_VERSION=""
UPGRADING=false
if [[ -L "$CURRENT_LINK" ]]; then
    PREV_CURRENT_TARGET="$(readlink "$CURRENT_LINK" 2>/dev/null || true)"  # versions/<v>
    INSTALLED_VERSION="$(read_config_version "$CURRENT_LINK/config.json")"
    if [[ -n "$INSTALLED_VERSION" ]]; then
        UPGRADING=true
    fi
fi

# ── Install into the versioned directory ─────────────────────────────────────
VERSION_DIR="$VERSIONS_DIR/$NEW_VERSION"

if [[ "$UPGRADING" == true ]]; then
    echo ""
    echo "Installing Daedalus $NEW_VERSION (current: $INSTALLED_VERSION)..."

    # Preserve user settings from the active version's config.
    OLD_CONFIG="$CURRENT_LINK/config.json"
    OLD_DATA_DIR="$(grep '"data-dir"' "$OLD_CONFIG" | sed 's/.*"data-dir": *"\([^"]*\)".*/\1/' || true)"
    OLD_DEBUG="$(grep '"debug"' "$OLD_CONFIG" | sed 's/.*"debug": *\([a-z]*\).*/\1/' || true)"
    OLD_IMAGE_PREFIX="$(grep '"image-prefix"' "$OLD_CONFIG" | sed 's/.*"image-prefix": *"\([^"]*\)".*/\1/' || true)"
    OLD_CONTAINER_PREFIX="$(grep '"container-prefix"' "$OLD_CONFIG" | sed 's/.*"container-prefix": *"\([^"]*\)".*/\1/' || true)"
    OLD_LOG_FILE="$(grep '"log-file"' "$OLD_CONFIG" | sed 's/.*"log-file": *"\([^"]*\)".*/\1/' || true)"
else
    echo ""
    echo "Installing Daedalus $NEW_VERSION to $PREFIX..."
fi

mkdir -p "$VERSION_DIR"

# Required binaries.
cp "$WORK_DIR/daedalus" "$VERSION_DIR/daedalus"
chmod 755 "$VERSION_DIR/daedalus"
cp "$WORK_DIR/skill-catalog-mcp" "$VERSION_DIR/skill-catalog-mcp"
chmod 755 "$VERSION_DIR/skill-catalog-mcp"
cp "$WORK_DIR/project-mgmt-mcp" "$VERSION_DIR/project-mgmt-mcp"
chmod 755 "$VERSION_DIR/project-mgmt-mcp"
# guild-mcp is the in-container read-only cross-project MCP server; the
# Dockerfile COPYs it from the build context (= the version dir) at image-build
# time, so it has to be staged next to the main binary. Conditional because
# older tarballs (pre-Sprint 53) won't ship it.
if [[ -f "$WORK_DIR/guild-mcp" ]]; then
    cp "$WORK_DIR/guild-mcp" "$VERSION_DIR/guild-mcp"
    chmod 755 "$VERSION_DIR/guild-mcp"
fi
# daedalus-runner is the in-container PID-1 binary the runner path launches; the
# Dockerfile COPYs it from the build context (= the version dir) at image-build
# time, so it has to be staged next to the main binary.
if [[ -f "$WORK_DIR/daedalus-runner" ]]; then
    cp "$WORK_DIR/daedalus-runner" "$VERSION_DIR/daedalus-runner"
    chmod 755 "$VERSION_DIR/daedalus-runner"
fi
# daedalus-coordinator is the host-side daemon; `daedalus coordinator start`
# expects it beside the main binary. Conditional because older tarballs won't
# ship it.
if [[ -f "$WORK_DIR/daedalus-coordinator" ]]; then
    cp "$WORK_DIR/daedalus-coordinator" "$VERSION_DIR/daedalus-coordinator"
    chmod 755 "$VERSION_DIR/daedalus-coordinator"
fi
# daedalus-control is the host-side control-plane daemon; `daedalus task`
# auto-spawns it from beside the main binary. Conditional because older
# tarballs won't ship it.
if [[ -f "$WORK_DIR/daedalus-control" ]]; then
    cp "$WORK_DIR/daedalus-control" "$VERSION_DIR/daedalus-control"
    chmod 755 "$VERSION_DIR/daedalus-control"
fi

# Copy setup.sh itself so users can run uninstall/upgrade locally.
SELF="$(cd "$(dirname "$0")" && pwd)/$(basename "$0")"
cp "$SELF" "$VERSION_DIR/setup.sh"
chmod 755 "$VERSION_DIR/setup.sh"

for f in "${RUNTIME_FILES[@]}"; do
    # config.json is written separately with merged settings.
    if [[ "$f" == "config.json" ]]; then
        continue
    fi
    cp "$WORK_DIR/$f" "$VERSION_DIR/$f"
done

# Write config.json for this version. The shared data dir lives at the prefix
# root ($PREFIX/.cache), NOT per-version, so the registry and caches survive a
# version switch. CLI flags win over the previous config; otherwise the prior
# value is preserved on upgrade and a fresh install gets the documented defaults.
if [[ "$UPGRADING" == true ]]; then
    DATA_DIR="${OLD_DATA_DIR:-$PREFIX/.cache}"
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

cat > "$VERSION_DIR/config.json" <<EOCFG
{
  "version": "$NEW_VERSION",
  "data-dir": "$DATA_DIR",
  "debug": $DEBUG,
  "image-prefix": "$IMAGE_PREFIX",
  "container-prefix": "$CONTAINER_PREFIX_VAL",
  "log-file": "$LOG_FILE"
}
EOCFG

echo "  Installed payload into $VERSION_DIR"
echo "  Configuration: $VERSION_DIR/config.json"

# ── Flip `current` (recording `previous` for rollback) ───────────────────────
if [[ -n "$PREV_CURRENT_TARGET" && "$PREV_CURRENT_TARGET" != "versions/$NEW_VERSION" ]]; then
    link_replace "$PREVIOUS_LINK" "$PREV_CURRENT_TARGET"
    echo "  Recorded previous version: ${PREV_CURRENT_TARGET#versions/}"
fi
link_replace "$CURRENT_LINK" "versions/$NEW_VERSION"
echo "  Active version: $NEW_VERSION (via $CURRENT_LINK)"

# ── PATH symlink → current/daedalus ──────────────────────────────────────────
# Pointing at `current` (not a specific version) means 'daedalus version
# use/rollback' only has to repoint `current` for the switch to take effect.
if [[ "$CREATE_LINK" == true ]]; then
    LINK_DIR="$HOME/.local/bin"
    mkdir -p "$LINK_DIR"

    ln -sf "$CURRENT_LINK/daedalus" "$LINK_DIR/$LINK_NAME"
    echo "  Symlinked $LINK_DIR/$LINK_NAME -> $CURRENT_LINK/daedalus"

    # Check if the link directory is on PATH
    if [[ ":$PATH:" != *":$LINK_DIR:"* ]]; then
        echo ""
        echo "  Note: $LINK_DIR is not on your PATH."
        echo "  Add it with: export PATH=\"$LINK_DIR:\$PATH\""
    fi
fi

# ── Summary ──────────────────────────────────────────────────────────────────
echo ""
if [[ "$UPGRADING" == true && "$INSTALLED_VERSION" != "$NEW_VERSION" ]]; then
    echo "Daedalus installed $NEW_VERSION alongside $INSTALLED_VERSION and switched to it."
    echo "  Roll back with: $LINK_NAME version rollback"
else
    echo "Daedalus $NEW_VERSION installed successfully."
fi
echo ""
echo "  Location: $VERSION_DIR/daedalus"
echo "  Active:   $CURRENT_LINK -> versions/$NEW_VERSION"
if [[ "$CREATE_LINK" == true ]]; then
    echo "  Symlink:  $LINK_DIR/$LINK_NAME"
fi
echo "  Config:   $VERSION_DIR/config.json"
if [[ -n "$CONTAINER_PREFIX_VAL" ]]; then
    echo "  Container prefix: ${CONTAINER_PREFIX_VAL:-claude-run-}"
fi
echo ""
echo "  Note: Docker is required at runtime to run projects."
echo "  Edit config.json to customize settings (data-dir, debug, etc.)."
echo ""
echo "  Manage versions:"
echo "    $LINK_NAME version list                # show installed versions"
echo "    $LINK_NAME version use <version>       # switch active version"
echo "    $LINK_NAME version rollback            # revert to the previous version"
echo ""
echo "  Get started:"
echo "    1. $LINK_NAME init /path/to/project    # scaffold docs + show next steps"
echo "    2. $LINK_NAME my-app /path/to/project  # register and start a project"
echo "  Run '$LINK_NAME --help' for the full command reference."
