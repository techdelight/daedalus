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
            # Two idempotent patches on every boot:
            #  1. Merge in any missing MCP-server entries (existing ones win).
            #  2. FORCE-set the trust / onboarding keys. The write-once copy
            #     above only seeds them into a fresh cache; a project cache
            #     predating those keys would never get them, so the "trust
            #     this folder?" dialog could still fire. The container is the
            #     trust boundary, so we assert them true (Milestone 5).
            # Non-fatal by design: a malformed or unreadable cache leaves the
            # cache untouched and startup continues — the worst case is a
            # one-time dialog, never a crash under `set -e`.
            # The jq program between the two sentinel comments is extracted
            # verbatim by scripts/test-trust-idempotency.sh — keep them in place.
            if PATCHED=$(jq --slurpfile defaults "$DEFS" '
                # trust-onboarding-filter-start
                (.mcpServers // {}) as $live |
                ($defaults[0].mcpServers // {}) as $required |
                .mcpServers = ($required * $live)
                | .hasCompletedOnboarding = true
                | .bypassPermissionsModeAccepted = true
                | .projects["/workspace"].hasTrustDialogAccepted = true
                | .projects["/workspace"].hasCompletedProjectOnboarding = true
                # trust-onboarding-filter-end
            ' "$LIVE" 2>/dev/null) && [ -n "$PATCHED" ]; then
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

# Per-project persistent tools prefix (#27). /opt/tools is bind-mounted from
# the host (empty on first use); ensure its bin/ exists so tools the agent
# installs there are on PATH (PATH is set in the Dockerfile). Best-effort:
# a permission failure (host-dir uid mismatch) must not abort startup.
mkdir -p /opt/tools/bin 2>/dev/null || true

# Daedalus-runner mode (phase 6 of the layered-stack rebuild). When
# DAEDALUS_RUNNER=1 is set, replace the legacy direct-claude exec with
# daedalus-runner, which owns the PTY, exposes a Unix socket, and lets
# host UIs attach. The shell vars below are the contract with the
# host-side CLI in launchProject.
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
