#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BINARY="$SCRIPT_DIR/daedalus"

if [ "${1:-}" = "build" ]; then
    echo "Building daedalus..."
    docker run --rm -v "$SCRIPT_DIR":/src -w /src golang:1.24-bookworm go build -buildvcs=false -o daedalus .
    echo "Done: $BINARY"
    exit 0
fi

if [ ! -x "$BINARY" ]; then
    echo "Binary not found, building..."
    docker run --rm -v "$SCRIPT_DIR":/src -w /src golang:1.24-bookworm go build -buildvcs=false -o daedalus .
fi

exec "$BINARY" "$@"
