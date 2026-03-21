#!/usr/bin/env bash
# Copyright (C) 2026 Techdelight BV
set -euo pipefail

cd "$(dirname "$0")"
VERSION=$(cat VERSION)
docker run --rm -v "$PWD":/src -w /src golang:1.24-bookworm \
  sh -c "go build -buildvcs=false -ldflags '-X github.com/techdelight/daedalus/core.Version=$VERSION' -o daedalus ./cmd/daedalus && \
         go build -buildvcs=false -o skill-catalog-mcp ./cmd/skill-catalog-mcp"
