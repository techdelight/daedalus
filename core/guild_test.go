// Copyright (C) 2026 Techdelight BV

package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mkdirs creates each named subdir under base and returns base.
func mkdirs(t *testing.T, base string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(base, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGuildMounts_NonGuildMaster(t *testing.T) {
	base := t.TempDir()
	mkdirs(t, base, "alpha", "beta")
	projects := []ProjectInfo{
		{Name: "alpha", Entry: ProjectEntry{Directory: filepath.Join(base, "alpha")}},
		{Name: "beta", Entry: ProjectEntry{Directory: filepath.Join(base, "beta")}},
	}
	// A normal project gets NO guild mounts.
	if got := GuildMounts("alpha", projects); got != nil {
		t.Errorf("GuildMounts for a non-guild-master = %v, want nil", got)
	}
}

func TestGuildMounts_GuildMasterMountsOthers(t *testing.T) {
	base := t.TempDir()
	mkdirs(t, base, "alpha", "beta", "gm-workspace")
	projects := []ProjectInfo{
		{Name: GuildMasterName, Entry: ProjectEntry{Directory: filepath.Join(base, "gm-workspace")}},
		{Name: "alpha", Entry: ProjectEntry{Directory: filepath.Join(base, "alpha")}},
		{Name: "beta", Entry: ProjectEntry{Directory: filepath.Join(base, "beta")}},
		{Name: "ghost", Entry: ProjectEntry{Directory: filepath.Join(base, "does-not-exist")}},
		{Name: "empty-dir", Entry: ProjectEntry{Directory: ""}},
	}

	got := GuildMounts(GuildMasterName, projects)

	joined := strings.Join(got, " ")
	// Others are mounted read-only at /guild/<name>.
	wantAlpha := "-v " + filepath.Join(base, "alpha") + ":/guild/alpha:ro"
	wantBeta := "-v " + filepath.Join(base, "beta") + ":/guild/beta:ro"
	if !strings.Contains(joined, wantAlpha) {
		t.Errorf("missing alpha mount %q in %v", wantAlpha, got)
	}
	if !strings.Contains(joined, wantBeta) {
		t.Errorf("missing beta mount %q in %v", wantBeta, got)
	}
	// Self excluded.
	if strings.Contains(joined, "/guild/"+GuildMasterName) {
		t.Errorf("guild master mounted into itself: %v", got)
	}
	// Missing dir skipped.
	if strings.Contains(joined, "/guild/ghost") {
		t.Errorf("ghost with missing dir should be skipped: %v", got)
	}
	// Empty directory skipped.
	if strings.Contains(joined, "/guild/empty-dir") {
		t.Errorf("empty-directory entry should be skipped: %v", got)
	}
	// Every emitted mount is read-only.
	for i := 0; i < len(got); i += 2 {
		if got[i] != "-v" {
			t.Fatalf("arg %d = %q, want -v", i, got[i])
		}
		if !strings.HasSuffix(got[i+1], ":ro") {
			t.Errorf("mount %q is not read-only", got[i+1])
		}
	}
}

func TestGuildMounts_SanitisesMalformedName(t *testing.T) {
	base := t.TempDir()
	mkdirs(t, base, "escapee")
	projects := []ProjectInfo{
		// A registry key that could escape /guild — must be dropped, not mounted.
		{Name: "../escape", Entry: ProjectEntry{Directory: filepath.Join(base, "escapee")}},
	}
	if got := GuildMounts(GuildMasterName, projects); len(got) != 0 {
		t.Errorf("malformed name should yield no mounts, got %v", got)
	}
}
