// Copyright (C) 2026 Techdelight BV

package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildUID_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := WriteBuildUID(dir, 1000); err != nil {
		t.Fatalf("WriteBuildUID: %v", err)
	}
	uid, ok := ReadBuildUID(dir)
	if !ok {
		t.Fatal("ReadBuildUID ok = false, want true")
	}
	if uid != 1000 {
		t.Errorf("uid = %d, want 1000", uid)
	}
}

func TestReadBuildUID_Missing(t *testing.T) {
	if _, ok := ReadBuildUID(t.TempDir()); ok {
		t.Error("ReadBuildUID ok = true for a dir with no build-uid file, want false")
	}
}

func TestReadBuildUID_Unparseable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(BuildUIDPath(dir), []byte("not-a-number\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadBuildUID(dir); ok {
		t.Error("ReadBuildUID ok = true for a non-numeric file, want false")
	}
}

func TestBuildUIDPath(t *testing.T) {
	got := BuildUIDPath("/data")
	want := filepath.Join("/data", "build-uid")
	if got != want {
		t.Errorf("BuildUIDPath = %q, want %q", got, want)
	}
}
