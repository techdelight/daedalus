// Copyright (C) 2026 Techdelight BV

package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInit_CreatesFile(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "deep", "test.log")

	// Act
	err := Init(path, false)
	defer Close()

	// Assert
	if err != nil {
		t.Fatalf("Init() returned error: %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("log file not created at %q: %v", path, statErr)
	}
}

func TestInfo_WritesTimestampAndLevel(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	if err := Init(path, false); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	// Act
	Info("hello world")
	Close()

	// Assert
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}
	line := strings.TrimSpace(string(content))
	if !strings.Contains(line, "[INFO] hello world") {
		t.Errorf("log line = %q, want substring %q", line, "[INFO] hello world")
	}
	// Verify timestamp prefix (RFC3339 starts with a year digit)
	if len(line) < 20 || line[4] != '-' {
		t.Errorf("log line = %q, expected RFC3339 timestamp prefix", line)
	}
}

func TestDebug_WritesOnlyWhenEnabled(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	if err := Init(path, false); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	// Act
	Debug("should not appear")
	Close()

	// Assert
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}
	if len(strings.TrimSpace(string(content))) > 0 {
		t.Errorf("debug message written with debug=false: %q", string(content))
	}
}

func TestDebug_WritesWhenEnabled(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	if err := Init(path, true); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	// Act
	Debug("debug message")
	Close()

	// Assert
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}
	line := strings.TrimSpace(string(content))
	if !strings.Contains(line, "[DEBUG] debug message") {
		t.Errorf("log line = %q, want substring %q", line, "[DEBUG] debug message")
	}
}

func TestError_WritesErrorLevel(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	if err := Init(path, false); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	// Act
	Error("something failed")
	Close()

	// Assert
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}
	line := strings.TrimSpace(string(content))
	if !strings.Contains(line, "[ERROR] something failed") {
		t.Errorf("log line = %q, want substring %q", line, "[ERROR] something failed")
	}
}

func TestClose_FlushesAndCloses(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	if err := Init(path, true); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	// Act
	Info("first")
	Debug("second")
	Error("third")
	Close()

	// Assert — all three lines present after close
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 log lines, got %d: %q", len(lines), string(content))
	}
	if !strings.Contains(lines[0], "[INFO] first") {
		t.Errorf("line 0 = %q, want [INFO] first", lines[0])
	}
	if !strings.Contains(lines[1], "[DEBUG] second") {
		t.Errorf("line 1 = %q, want [DEBUG] second", lines[1])
	}
	if !strings.Contains(lines[2], "[ERROR] third") {
		t.Errorf("line 2 = %q, want [ERROR] third", lines[2])
	}
}
