#!/usr/bin/env bash
# Copyright (C) 2026 Techdelight BV
set -euo pipefail

cd "$(dirname "$0")"
# Go library deps cached in a project-local, git-ignored volume (./.build-cache),
# shared with build.sh — resolved once, never in the global module cache.
docker run --rm -v "$PWD":/src -w /src \
  -e GOMODCACHE=/src/.build-cache/go/mod -e GOCACHE=/src/.build-cache/go/build \
  golang:1.25-bookworm go test -v ./...
