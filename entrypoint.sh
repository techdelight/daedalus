#!/bin/bash
set -e

AGENT="${AGENT:-claude}"
DEFAULTS_DIR="/opt/claude/defaults"

case "$AGENT" in
    claude)
        # Ensure config directory exists
        mkdir -p "$CLAUDE_CONFIG_DIR"

        # Ensure commands directory exists for skill catalog
        mkdir -p "$HOME/.claude/commands"

        # Seed config files on first run
        if [ ! -f "$CLAUDE_CONFIG_DIR/.claude.json" ]; then
            cp "$DEFAULTS_DIR/.claude.json" "$CLAUDE_CONFIG_DIR/.claude.json"
            cp "$DEFAULTS_DIR/settings.json" "$CLAUDE_CONFIG_DIR/settings.json"
        fi

        exec /opt/claude/bin/claude --dangerously-skip-permissions "$@"
        ;;
    copilot)
        mkdir -p "${COPILOT_HOME:-$HOME/.copilot}"
        exec /home/claude/.local/bin/copilot --allow-all "$@"
        ;;
    *)
        echo "Unknown agent: $AGENT" >&2
        exit 1
        ;;
esac
