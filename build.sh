#!/usr/bin/env bash
# Copyright (C) 2026 Techdelight BV
set -euo pipefail

cd "$(dirname "$0")"
docker run --rm -v "$PWD":/src -w /src golang:1.24-bookworm go build -buildvcs=false -o daedalus ./cmd/daedalus
