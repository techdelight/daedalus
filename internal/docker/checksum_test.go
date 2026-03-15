// Copyright (C) 2026 Techdelight BV

package docker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/techdelight/daedalus/core"
)

func TestReadBuildFilesContent_ReadsFiles(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM golang"), 0644)
	os.WriteFile(filepath.Join(dir, "entrypoint.sh"), []byte("#!/bin/bash"), 0644)

	// Act
	content, err := ReadBuildFilesContent(dir)

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "FROM golang#!/bin/bash"
	if string(content) != want {
		t.Errorf("ReadBuildFilesContent() = %q, want %q", string(content), want)
	}
}

func TestReadBuildFilesContent_MissingFilesSkipped(t *testing.T) {
	// Arrange — only Dockerfile present, rest missing
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM golang"), 0644)

	// Act
	content, err := ReadBuildFilesContent(dir)

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "FROM golang"
	if string(content) != want {
		t.Errorf("ReadBuildFilesContent() = %q, want %q", string(content), want)
	}
}

func TestReadBuildFilesContent_AllMissing(t *testing.T) {
	// Arrange — empty directory
	dir := t.TempDir()

	// Act
	content, err := ReadBuildFilesContent(dir)

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) != 0 {
		t.Errorf("expected empty content, got %d bytes", len(content))
	}
}

func TestWriteAndReadChecksum(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	path := filepath.Join(dir, "build-checksum")
	checksum := "abc123def456"

	// Act
	err := WriteChecksum(path, checksum)
	if err != nil {
		t.Fatalf("WriteChecksum failed: %v", err)
	}
	got := ReadStoredChecksum(path)

	// Assert
	if got != checksum {
		t.Errorf("ReadStoredChecksum() = %q, want %q", got, checksum)
	}
}

func TestReadStoredChecksum_Missing(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "nonexistent")

	// Act
	got := ReadStoredChecksum(path)

	// Assert
	if got != "" {
		t.Errorf("ReadStoredChecksum(missing) = %q, want empty string", got)
	}
}

func TestNeedsRebuild_FirstRun(t *testing.T) {
	// Arrange — no stored checksum file
	scriptDir := t.TempDir()
	os.WriteFile(filepath.Join(scriptDir, "Dockerfile"), []byte("FROM golang"), 0644)
	checksumPath := filepath.Join(t.TempDir(), "build-checksum")

	// Act
	result := NeedsRebuild(scriptDir, checksumPath)

	// Assert
	if !result {
		t.Error("NeedsRebuild() = false, want true (no stored checksum)")
	}
}

func TestNeedsRebuild_NoChange(t *testing.T) {
	// Arrange — store checksum matching current files
	scriptDir := t.TempDir()
	os.WriteFile(filepath.Join(scriptDir, "Dockerfile"), []byte("FROM golang"), 0644)

	content, err := ReadBuildFilesContent(scriptDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checksum := core.ComputeBuildChecksum(content)

	checksumDir := t.TempDir()
	checksumPath := filepath.Join(checksumDir, "build-checksum")
	WriteChecksum(checksumPath, checksum)

	// Act
	result := NeedsRebuild(scriptDir, checksumPath)

	// Assert
	if result {
		t.Error("NeedsRebuild() = true, want false (checksum unchanged)")
	}
}

func TestNeedsRebuild_Changed(t *testing.T) {
	// Arrange — store stale checksum
	scriptDir := t.TempDir()
	os.WriteFile(filepath.Join(scriptDir, "Dockerfile"), []byte("FROM golang:1.24"), 0644)

	checksumDir := t.TempDir()
	checksumPath := filepath.Join(checksumDir, "build-checksum")
	WriteChecksum(checksumPath, "stale-checksum-value")

	// Act
	result := NeedsRebuild(scriptDir, checksumPath)

	// Assert
	if !result {
		t.Error("NeedsRebuild() = false, want true (checksum changed)")
	}
}
