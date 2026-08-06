// Copyright (C) 2026 Techdelight BV

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/techdelight/daedalus/core"
)

// TestRunInit_ScaffoldsAndLints proves init writes the required docs and that
// the freshly scaffolded output passes the --ci gate.
func TestRunInit_ScaffoldsAndLints(t *testing.T) {
	dir := t.TempDir()
	cfg := &core.Config{InitArgs: []string{dir}}

	if err := runInit(cfg); err != nil {
		t.Fatalf("runInit() = %v, want nil", err)
	}
	for _, doc := range core.RequiredDocs() {
		if _, err := os.Stat(filepath.Join(dir, doc.Filename)); err != nil {
			t.Errorf("expected %s on disk after init: %v", doc.Filename, err)
		}
	}
	if err := lintDocs([]string{"--ci", dir}); err != nil {
		t.Errorf("lintDocs(--ci) on init output = %v, want nil", err)
	}
}

// TestRunInit_NoScaffoldSkipsWriting proves --no-scaffold prints guidance only,
// writing nothing.
func TestRunInit_NoScaffoldSkipsWriting(t *testing.T) {
	dir := t.TempDir()
	cfg := &core.Config{InitArgs: []string{"--no-scaffold", dir}}

	if err := runInit(cfg); err != nil {
		t.Fatalf("runInit(--no-scaffold) = %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("--no-scaffold wrote %d entries, want 0", len(entries))
	}
}

// TestRunInit_SkipsExisting proves a second run does not clobber an existing doc.
func TestRunInit_SkipsExisting(t *testing.T) {
	dir := t.TempDir()
	const sentinel = "hand-written\n"
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(sentinel), 0o644); err != nil {
		t.Fatalf("seeding README.md: %v", err)
	}

	cfg := &core.Config{InitArgs: []string{dir}}
	if err := runInit(cfg); err != nil {
		t.Fatalf("runInit() = %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}
	if string(got) != sentinel {
		t.Errorf("README.md was clobbered: got %q, want %q", string(got), sentinel)
	}
}

// TestRunInit_Force proves --force (seeded from the global parser's cfg.Force)
// overwrites an existing doc.
func TestRunInit_Force(t *testing.T) {
	dir := t.TempDir()
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("stale\n"), 0o644); err != nil {
		t.Fatalf("seeding README.md: %v", err)
	}

	cfg := &core.Config{InitArgs: []string{dir}, Force: true}
	if err := runInit(cfg); err != nil {
		t.Fatalf("runInit(force) = %v", err)
	}
	got, err := os.ReadFile(readme)
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}
	if string(got) == "stale\n" {
		t.Error("README.md not overwritten under --force")
	}
}

func TestRunInit_UnknownFlag(t *testing.T) {
	cfg := &core.Config{InitArgs: []string{"--nope"}}
	if err := runInit(cfg); err == nil {
		t.Error("runInit(--nope) = nil, want an error")
	}
}

func TestRunInit_TooManyArgs(t *testing.T) {
	cfg := &core.Config{InitArgs: []string{"a", "b"}}
	if err := runInit(cfg); err == nil {
		t.Error("runInit(a b) = nil, want an error")
	}
}
