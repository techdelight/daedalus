#!/usr/bin/env bash
# Copyright (C) 2026 Techdelight BV
#
# Runs the runner repaint-on-attach e2e (Sprint 41 item 4 / Backlog #38).
# These tests need a real PTY + signals, so they live behind the `e2e`
# build tag and are excluded from the default `./test.sh` suite. This
# script runs them in the same pinned golang image the rest of the build
# uses, so the result is reproducible off any dev machine.
set -euo pipefail

cd "$(dirname "$0")/.."
# Go library deps cached in a project-local, git-ignored volume (./.build-cache),
# shared with build.sh — resolved once, never in the global module cache.
docker run --rm -v "$PWD":/src -w /src \
  -e GOMODCACHE=/src/.build-cache/go/mod -e GOCACHE=/src/.build-cache/go/build \
  golang:1.25-bookworm \
  go test -tags e2e -v ./cmd/daedalus-runner/
