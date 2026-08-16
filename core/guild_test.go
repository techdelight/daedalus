// Copyright (C) 2026 Techdelight BV

package core

import (
	"net"
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

// listenSocket creates a real Unix socket at dir/name and returns its path. A
// real socket matters: the mount refuses anything that is not one, so a test
// that faked it with a regular file would assert the opposite of the contract.
func listenSocket(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen on %s: %v", path, err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return path
}

func TestGuildControlSocketMount_GuildMasterGetsTheAgentSocket(t *testing.T) {
	sock := listenSocket(t, t.TempDir(), "control-agent.sock")

	got := GuildControlSocketMount(GuildMasterName, sock)
	want := []string{"-v", sock + ":" + GuildControlSocketTarget}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("GuildControlSocketMount = %v, want %v", got, want)
	}
}

func TestGuildControlSocketMount_NotForOrdinaryProjects(t *testing.T) {
	sock := listenSocket(t, t.TempDir(), "control-agent.sock")

	if got := GuildControlSocketMount("alpha", sock); got != nil {
		t.Errorf("an ordinary project got a control socket: %v", got)
	}
}

// The mount must refuse the HUMAN socket by name. Caller class is decided by
// which file is mounted, so mounting control.sock would silently promote the
// agent to full authority — the one mistake the design cannot absorb.
func TestGuildControlSocketMount_RefusesTheHumanSocket(t *testing.T) {
	dir := t.TempDir()
	human := listenSocket(t, dir, "control.sock")

	if got := GuildControlSocketMount(GuildMasterName, human); got != nil {
		t.Fatalf("the human control.sock was mounted into the Guild Master: %v", got)
	}
}

func TestGuildControlSocketMount_RefusesAnythingThatIsNotASocket(t *testing.T) {
	dir := t.TempDir()

	// Missing: the plane is not running. Mounting it would have Docker create a
	// directory at the target inside the container.
	if got := GuildControlSocketMount(GuildMasterName, filepath.Join(dir, "control-agent.sock")); got != nil {
		t.Errorf("a missing socket was mounted: %v", got)
	}
	// Present, right name, wrong kind — a plain file is not the daemon's listener.
	plain := filepath.Join(dir, "sub", "control-agent.sock")
	if err := os.MkdirAll(filepath.Dir(plain), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plain, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := GuildControlSocketMount(GuildMasterName, plain); got != nil {
		t.Errorf("a regular file was mounted as the agent socket: %v", got)
	}
	// Nothing at all.
	if got := GuildControlSocketMount(GuildMasterName, ""); got != nil {
		t.Errorf("an empty path produced a mount: %v", got)
	}
}
