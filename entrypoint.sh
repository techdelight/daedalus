#!/bin/bash
set -e

DEFAULTS_DIR="/opt/claude/defaults"
CREDENTIALS_DIR="/opt/claude/credentials"

# Ensure config directory exists
mkdir -p "$CLAUDE_CONFIG_DIR"

# Seed config files on first run
if [ ! -f "$CLAUDE_CONFIG_DIR/.claude.json" ]; then
    cp "$DEFAULTS_DIR/.claude.json" "$CLAUDE_CONFIG_DIR/.claude.json"
    cp "$DEFAULTS_DIR/settings.json" "$CLAUDE_CONFIG_DIR/settings.json"
fi

# Symlink credentials into config dir (refreshed each run)
if [ -f "$CREDENTIALS_DIR/.credentials.json" ]; then
    ln -sf "$CREDENTIALS_DIR/.credentials.json" "$CLAUDE_CONFIG_DIR/.credentials.json"
fi

exec /opt/claude/bin/claude --dangerously-skip-permissions "$@"
