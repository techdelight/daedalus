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
docker run --rm -v "$PWD":/src -w /src golang:1.25-bookworm \
  go test -tags e2e -v ./cmd/daedalus-runner/
