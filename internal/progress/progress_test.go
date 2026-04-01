// Copyright (C) 2026 Techdelight BV

package progress

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRead_NoFile(t *testing.T) {
	// Arrange
	dir := t.TempDir()

	// Act
	data, err := Read(dir)

	// Assert
	if err != nil {
		t.Fatalf("Read() error = %v, want nil", err)
	}
	if data != (Data{}) {
		t.Errorf("Read() = %+v, want zero-value Data", data)
	}
}

func TestWrite_CreatesDir(t *testing.T) {
	// Arrange
	dir := filepath.Join(t.TempDir(), "project")

	d := Data{
		ProgressPct:    42,
		Vision:         "Build something great",
		ProjectVersion: "1.0.0",
		Message:        "In progress",
	}

	// Act
	err := Write(dir, d)

	// Assert
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	path := filepath.Join(dir, ".daedalus", "progress.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}

	var got Data
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("parsing written file: %v", err)
	}
	if got != d {
		t.Errorf("written data = %+v, want %+v", got, d)
	}
}

func TestWriteAndRead_Roundtrip(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	want := Data{
		ProgressPct:    75,
		Vision:         "Automate everything",
		ProjectVersion: "2.3.1",
		Message:        "Almost done",
	}

	// Act
	if err := Write(dir, want); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	got, err := Read(dir)

	// Assert
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got != want {
		t.Errorf("Read() = %+v, want %+v", got, want)
	}
}

func TestUpdate_PartialUpdate(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	initial := Data{
		ProgressPct:    10,
		Vision:         "Original vision",
		ProjectVersion: "0.1.0",
		Message:        "Just started",
	}
	if err := Write(dir, initial); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	// Act — update only pct
	if err := Update(dir, 50, "", "", ""); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// Assert
	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got.ProgressPct != 50 {
		t.Errorf("ProgressPct = %d, want 50", got.ProgressPct)
	}
	if got.Vision != "Original vision" {
		t.Errorf("Vision = %q, want %q", got.Vision, "Original vision")
	}
	if got.ProjectVersion != "0.1.0" {
		t.Errorf("ProjectVersion = %q, want %q", got.ProjectVersion, "0.1.0")
	}
	if got.Message != "Just started" {
		t.Errorf("Message = %q, want %q", got.Message, "Just started")
	}
}

func TestUpdate_ClampsPct(t *testing.T) {
	// Arrange
	dir := t.TempDir()

	// Act
	if err := Update(dir, 200, "", "", ""); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// Assert
	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got.ProgressPct != 100 {
		t.Errorf("ProgressPct = %d, want 100 (clamped from 200)", got.ProgressPct)
	}
}
