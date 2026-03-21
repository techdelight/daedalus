#!/bin/bash
set -e

DEFAULTS_DIR="/opt/claude/defaults"

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
