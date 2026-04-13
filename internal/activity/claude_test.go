// Copyright (C) 2026 Techdelight BV

package activity

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/techdelight/daedalus/core"
)

func TestClaudeCodeDetector_MissingFile(t *testing.T) {
	// Arrange
	d := &ClaudeCodeDetector{now: time.Now}

	// Act
	info := d.Detect(t.TempDir())

	// Assert — missing file defaults to idle
	if info.State != core.ActivityIdle {
		t.Errorf("got %q, want %q", info.State, core.ActivityIdle)
	}
}

func TestClaudeCodeDetector_BusyFresh(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	writeActivity(t, dir, `{"state":"busy","detail":"tool_use","ts":"`+time.Now().UTC().Format(time.RFC3339)+`"}`)
	d := &ClaudeCodeDetector{now: time.Now}

	// Act
	info := d.Detect(dir)

	// Assert
	if info.State != core.ActivityBusy {
		t.Errorf("got %q, want %q", info.State, core.ActivityBusy)
	}
	if info.Detail != "tool_use" {
		t.Errorf("detail: got %q, want %q", info.Detail, "tool_use")
	}
}

func TestClaudeCodeDetector_IdleFresh(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	writeActivity(t, dir, `{"state":"idle","detail":"waiting","ts":"`+time.Now().UTC().Format(time.RFC3339)+`"}`)
	d := &ClaudeCodeDetector{now: time.Now}

	// Act
	info := d.Detect(dir)

	// Assert
	if info.State != core.ActivityIdle {
		t.Errorf("got %q, want %q", info.State, core.ActivityIdle)
	}
}

func TestClaudeCodeDetector_StaleTimestamp(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	staleTime := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339)
	writeActivity(t, dir, `{"state":"busy","detail":"tool_use","ts":"`+staleTime+`"}`)
	d := &ClaudeCodeDetector{now: time.Now}

	// Act
	info := d.Detect(dir)

	// Assert — stale busy falls back to idle
	if info.State != core.ActivityIdle {
		t.Errorf("got %q, want %q", info.State, core.ActivityIdle)
	}
}

func TestClaudeCodeDetector_InvalidJSON(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	writeActivity(t, dir, `not json`)
	d := &ClaudeCodeDetector{now: time.Now}

	// Act
	info := d.Detect(dir)

	// Assert
	if info.State != core.ActivityIdle {
		t.Errorf("got %q, want %q", info.State, core.ActivityIdle)
	}
}

func TestClaudeCodeDetector_InvalidTimestamp(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	writeActivity(t, dir, `{"state":"busy","detail":"x","ts":"not-a-date"}`)
	d := &ClaudeCodeDetector{now: time.Now}

	// Act
	info := d.Detect(dir)

	// Assert
	if info.State != core.ActivityIdle {
		t.Errorf("got %q, want %q", info.State, core.ActivityIdle)
	}
}

func writeActivity(t *testing.T, dir, content string) {
	t.Helper()
	daedalusDir := filepath.Join(dir, ".daedalus")
	if err := os.MkdirAll(daedalusDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(daedalusDir, "activity.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
