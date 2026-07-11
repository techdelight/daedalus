// Copyright (C) 2026 Techdelight BV

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func writePID(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write pid: %v", err)
	}
}

func TestReadPIDIfAlive_MissingFile(t *testing.T) {
	_, alive := readPIDIfAlive(filepath.Join(t.TempDir(), "nope.pid"))
	if alive {
		t.Error("alive = true for missing file, want false")
	}
}

func TestReadPIDIfAlive_MalformedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "coord.pid")
	writePID(t, path, "not-a-pid\n")

	_, alive := readPIDIfAlive(path)
	if alive {
		t.Error("alive = true for garbage pidfile, want false")
	}
}

func TestReadPIDIfAlive_NegativePID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "coord.pid")
	writePID(t, path, "-1\n")

	_, alive := readPIDIfAlive(path)
	if alive {
		t.Error("alive = true for negative pid, want false")
	}
}

func TestReadPIDIfAlive_LiveProcess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "coord.pid")

	// Our own process is guaranteed alive during the test.
	writePID(t, path, fmt.Sprintf("%d\n", os.Getpid()))

	pid, alive := readPIDIfAlive(path)
	if !alive {
		t.Fatalf("alive = false for own pid %d, want true", os.Getpid())
	}
	if pid != os.Getpid() {
		t.Errorf("pid = %d, want %d", pid, os.Getpid())
	}
}

func TestReadPIDIfAlive_DeadPID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "coord.pid")

	// PID 1 is init/systemd; can't use it. Use a very high PID that
	// almost certainly isn't allocated. Not perfect but the alternative
	// (fork/kill a child) is more brittle in CI.
	writePID(t, path, "2147483000\n")

	_, alive := readPIDIfAlive(path)
	if alive {
		t.Error("alive = true for improbable-high pid, want false")
	}
}
