#!/usr/bin/env bash
# Copyright (C) 2026 Techdelight BV
set -euo pipefail

cd "$(dirname "$0")"
VERSION=$(cat VERSION)

# ── Expected build outputs ────────────────────────────────────────────────
# Kept as a single list so the build step below and the post-build
# verification below cannot drift apart. Adding a new binary means adding
# it here and to the go-build compound command in one place.
REQUIRED_BINARIES=(
    daedalus              # main CLI
    skill-catalog-mcp     # in-container MCP server
    project-mgmt-mcp      # in-container MCP server
    guild-mcp             # in-container MCP server (Guild Master only)
    daedalus-runner       # in-container PID-1; Dockerfile COPYs it in
    daedalus-coordinator  # host-side daemon
    daedalus-control      # host-side control-plane daemon
)

# Run the compiler as the invoking user (not root) so the output binaries land
# owned by you, not root:root — otherwise a later `touch`/rebuild/rm in your
# checkout hits "Permission denied". HOME=/tmp keeps GOCACHE/GOPATH in a dir any
# uid can write (the default /root/... isn't writable when we drop root).
docker run --rm -v "$PWD":/src -w /src \
  --user "$(id -u):$(id -g)" -e HOME=/tmp \
  golang:1.25-bookworm \
  sh -c "go build -buildvcs=false -ldflags '-X github.com/techdelight/daedalus/core.Version=$VERSION' -o daedalus ./cmd/daedalus && \
         go build -buildvcs=false -o skill-catalog-mcp ./cmd/skill-catalog-mcp && \
         go build -buildvcs=false -o project-mgmt-mcp ./cmd/project-mgmt-mcp && \
         go build -buildvcs=false -o guild-mcp ./cmd/guild-mcp && \
         go build -buildvcs=false -o daedalus-runner ./cmd/daedalus-runner && \
         go build -buildvcs=false -o daedalus-coordinator ./cmd/daedalus-coordinator && \
         go build -buildvcs=false -o daedalus-control ./cmd/daedalus-control"

# ── Verify every expected binary landed ───────────────────────────────────
# Guards against the class of bug where a new binary is added to one
# place (setup.sh, Dockerfile COPY, release workflow) but silently
# missed here. Without this check the omission surfaces much later,
# usually as a confusing `docker build` failure downstream.
missing=()
for bin in "${REQUIRED_BINARIES[@]}"; do
    if [[ ! -x "$bin" ]]; then
        missing+=("$bin")
    fi
done

if [[ ${#missing[@]} -gt 0 ]]; then
    echo "" >&2
    echo "build.sh: missing expected binaries after build:" >&2
    for bin in "${missing[@]}"; do
        echo "  - $bin" >&2
    done
    exit 1
fi

echo ""
echo "build.sh: ${#REQUIRED_BINARIES[@]} binaries built:"
for bin in "${REQUIRED_BINARIES[@]}"; do
    echo "  - $bin"
done
