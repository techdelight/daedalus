// Copyright (C) 2026 Techdelight BV

package core

import (
	"testing"
)

func TestComputeBuildChecksum_Deterministic(t *testing.T) {
	// Arrange
	input := []byte("FROM golang:1.24\nRUN apt-get update")

	// Act
	hash1 := ComputeBuildChecksum(input)
	hash2 := ComputeBuildChecksum(input)

	// Assert
	if hash1 != hash2 {
		t.Errorf("same input produced different hashes: %q vs %q", hash1, hash2)
	}
	if len(hash1) != 64 {
		t.Errorf("expected 64-char hex string, got %d chars: %q", len(hash1), hash1)
	}
}

func TestComputeBuildChecksum_DifferentInput(t *testing.T) {
	// Arrange
	input1 := []byte("FROM golang:1.24")
	input2 := []byte("FROM golang:1.25")

	// Act
	hash1 := ComputeBuildChecksum(input1)
	hash2 := ComputeBuildChecksum(input2)

	// Assert
	if hash1 == hash2 {
		t.Errorf("different inputs produced same hash: %q", hash1)
	}
}

func TestComputeBuildChecksum_EmptyInput(t *testing.T) {
	// Arrange
	input := []byte{}

	// Act
	hash := ComputeBuildChecksum(input)

	// Assert
	if len(hash) != 64 {
		t.Errorf("expected 64-char hex string, got %d chars: %q", len(hash), hash)
	}
	// SHA-256 of empty input is the well-known value
	want := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if hash != want {
		t.Errorf("ComputeBuildChecksum(empty) = %q, want %q", hash, want)
	}
}

func TestBuildFiles_ReturnsExpectedFiles(t *testing.T) {
	// Act
	files := BuildFiles()

	// Assert
	expected := []string{"Dockerfile", "entrypoint.sh", "docker-compose.yml", "settings.json", "claude.json"}
	if len(files) != len(expected) {
		t.Fatalf("BuildFiles() returned %d files, want %d", len(files), len(expected))
	}
	for i, f := range files {
		if f != expected[i] {
			t.Errorf("BuildFiles()[%d] = %q, want %q", i, f, expected[i])
		}
	}
}
