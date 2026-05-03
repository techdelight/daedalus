#!/bin/bash
set -e

RUNNER="${RUNNER:-claude}"
DEFAULTS_DIR="/opt/claude/defaults"

# Per-runner pre-flight: seed config files / ensure config dirs exist.
# Runs in both legacy and daedalus-runner modes since the runner needs
# the same on-disk config the CLI used to.
case "$RUNNER" in
    claude)
        mkdir -p "$CLAUDE_CONFIG_DIR"
        if [ ! -f "$CLAUDE_CONFIG_DIR/.claude.json" ]; then
            cp "$DEFAULTS_DIR/.claude.json" "$CLAUDE_CONFIG_DIR/.claude.json"
            cp "$DEFAULTS_DIR/settings.json" "$CLAUDE_CONFIG_DIR/settings.json"
        fi
        # Ensure daedalus-specific MCP servers are configured. Adds any
        # missing entries from the defaults file without modifying
        # existing ones.
        LIVE="$CLAUDE_CONFIG_DIR/.claude.json"
        DEFS="$DEFAULTS_DIR/.claude.json"
        if [ -f "$LIVE" ] && [ -f "$DEFS" ]; then
            PATCHED=$(jq --slurpfile defaults "$DEFS" '
                (.mcpServers // {}) as $live |
                ($defaults[0].mcpServers // {}) as $required |
                .mcpServers = ($required * $live)
            ' "$LIVE")
            if [ -n "$PATCHED" ]; then
                printf '%s\n' "$PATCHED" > "$LIVE"
            fi
        fi
        ;;
    copilot)
        mkdir -p "${COPILOT_HOME:-$HOME/.copilot}"
        ;;
    *)
        echo "Unknown runner: $RUNNER" >&2
        exit 1
        ;;
esac

# Daedalus-runner mode (phase 6 of the layered-stack rebuild). When
# DAEDALUS_RUNNER=1 is set, replace the legacy direct-claude exec with
# daedalus-runner, which owns the PTY, exposes a Unix socket, and lets
# host UIs attach. The shell vars below are the contract with the
# host-side CLI in launchProjectViaRunner.
if [ -n "${DAEDALUS_RUNNER:-}" ]; then
    : "${DAEDALUS_SOCKET:?DAEDALUS_SOCKET is required when DAEDALUS_RUNNER is set}"
    mkdir -p "$(dirname "$DAEDALUS_SOCKET")"
    args=(--adapter "$RUNNER" --socket "$DAEDALUS_SOCKET")
    [ -n "${DAEDALUS_DEBUG:-}" ] && args+=(--debug)
    [ -n "${DAEDALUS_RESUME:-}" ] && args+=(--resume "$DAEDALUS_RESUME")
    [ -n "${DAEDALUS_PROMPT:-}" ] && args+=(--prompt "$DAEDALUS_PROMPT")
    exec /usr/local/bin/daedalus-runner "${args[@]}"
fi

# Legacy direct-launch path (default, unchanged behaviour).
case "$RUNNER" in
    claude)
        exec /opt/claude/bin/claude --dangerously-skip-permissions "$@"
        ;;
    copilot)
        exec /usr/local/bin/copilot --allow-all "$@"
        ;;
esac
