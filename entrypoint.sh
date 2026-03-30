#!/bin/bash
set -e

RUNNER="${RUNNER:-claude}"
DEFAULTS_DIR="/opt/claude/defaults"

case "$RUNNER" in
    claude)
        # Ensure config directory exists
        mkdir -p "$CLAUDE_CONFIG_DIR"

        # Ensure skills directory exists for skill catalog
        mkdir -p /workspace/.claude/skills

        # Ensure .daedalus directory exists for project management MCP server
        mkdir -p /workspace/.daedalus

        # Seed config files on first run
        if [ ! -f "$CLAUDE_CONFIG_DIR/.claude.json" ]; then
            cp "$DEFAULTS_DIR/.claude.json" "$CLAUDE_CONFIG_DIR/.claude.json"
            cp "$DEFAULTS_DIR/settings.json" "$CLAUDE_CONFIG_DIR/settings.json"
        fi

        exec /opt/claude/bin/claude --dangerously-skip-permissions "$@"
        ;;
    copilot)
        mkdir -p "${COPILOT_HOME:-$HOME/.copilot}"
        exec /usr/local/bin/copilot --allow-all "$@"
        ;;
    *)
        echo "Unknown runner: $RUNNER" >&2
        exit 1
        ;;
esac
