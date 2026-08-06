// Copyright (C) 2026 Techdelight BV

package versions

import (
	"os"
	"path/filepath"
	"testing"
)

// newTree fabricates a $PREFIX/versions/* layout with the given version dirs and
// an optional current version, returning the prefix and its Layout.
func newTree(t *testing.T, current string, vers ...string) (string, Layout) {
	t.Helper()
	prefix := t.TempDir()
	for _, v := range vers {
		if err := os.MkdirAll(filepath.Join(prefix, "versions", v), 0o755); err != nil {
			t.Fatal(err)
		}
		// a stand-in binary so refreshBinSymlink has a real target
		if err := os.WriteFile(filepath.Join(prefix, "versions", v, "daedalus"), []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	l := Layout{Prefix: prefix}
	if current != "" {
		if err := os.Symlink(filepath.Join("versions", current), l.CurrentLink()); err != nil {
			t.Fatal(err)
		}
	}
	return prefix, l
}

func TestResolvePrefix_EnvOverride(t *testing.T) {
	t.Setenv("DAEDALUS_PREFIX", "/opt/daedalus")
	got, err := ResolvePrefix("/anything/versions/1.0.0/daedalus")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/opt/daedalus" {
		t.Fatalf("env override: got %q want /opt/daedalus", got)
	}
}

func TestResolvePrefix_DerivesThreeUp(t *testing.T) {
	os.Unsetenv("DAEDALUS_PREFIX")
	// Build a real tree so EvalSymlinks succeeds.
	prefix := t.TempDir()
	bin := filepath.Join(prefix, "versions", "1.2.3", "daedalus")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolvePrefix(bin)
	if err != nil {
		t.Fatal(err)
	}
	// t.TempDir may contain symlinks (e.g. /tmp -> /private/tmp on macOS);
	// compare resolved forms.
	wantResolved, _ := filepath.EvalSymlinks(prefix)
	if got != wantResolved && got != prefix {
		t.Fatalf("derive: got %q want %q", got, prefix)
	}
}

func TestList_OrderAndCurrent(t *testing.T) {
	_, l := newTree(t, "0.2.0", "0.1.0", "0.2.0", "0.10.0")
	infos, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{"0.10.0", "0.2.0", "0.1.0"} // semver-descending, not lexical
	if len(infos) != len(wantOrder) {
		t.Fatalf("got %d versions, want %d", len(infos), len(wantOrder))
	}
	for i, w := range wantOrder {
		if infos[i].Version != w {
			t.Errorf("order[%d] = %q want %q", i, infos[i].Version, w)
		}
	}
	for _, info := range infos {
		if (info.Version == "0.2.0") != info.Current {
			t.Errorf("current marking wrong for %q: %v", info.Version, info.Current)
		}
	}
}

func TestUse_SwitchesAndRecordsPrevious(t *testing.T) {
	_, l := newTree(t, "0.2.0", "0.1.0", "0.2.0")
	ok, err := l.Use("0.1.0", "")
	if err != nil || !ok {
		t.Fatalf("use: ok=%v err=%v", ok, err)
	}
	if l.Current() != "0.1.0" {
		t.Errorf("current = %q want 0.1.0", l.Current())
	}
	if l.Previous() != "0.2.0" {
		t.Errorf("previous = %q want 0.2.0", l.Previous())
	}
}

func TestUse_AlreadyCurrentIsNoop(t *testing.T) {
	_, l := newTree(t, "0.2.0", "0.1.0", "0.2.0")
	ok, err := l.Use("0.2.0", "")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Errorf("expected ok=false (no-op) when using already-current version")
	}
	if l.Previous() != "" {
		t.Errorf("no-op use must not record a previous, got %q", l.Previous())
	}
}

func TestUse_UnknownVersionErrors(t *testing.T) {
	_, l := newTree(t, "0.1.0", "0.1.0")
	if _, err := l.Use("9.9.9", ""); err == nil {
		t.Fatal("expected error using an uninstalled version")
	}
}

func TestRollback(t *testing.T) {
	_, l := newTree(t, "0.2.0", "0.1.0", "0.2.0")
	if _, err := l.Use("0.1.0", ""); err != nil {
		t.Fatal(err)
	}
	target, err := l.Rollback("")
	if err != nil {
		t.Fatal(err)
	}
	if target != "0.2.0" || l.Current() != "0.2.0" {
		t.Fatalf("rollback target=%q current=%q want 0.2.0", target, l.Current())
	}
	// After rolling back, previous should now be the version we rolled from.
	if l.Previous() != "0.1.0" {
		t.Errorf("previous after rollback = %q want 0.1.0", l.Previous())
	}
}

func TestRollback_NoPreviousErrors(t *testing.T) {
	_, l := newTree(t, "0.1.0", "0.1.0")
	if _, err := l.Rollback(""); err == nil {
		t.Fatal("expected error rolling back with no previous")
	}
}

func TestPrune_KeepsCurrentAndLastN(t *testing.T) {
	_, l := newTree(t, "0.4.0", "0.1.0", "0.2.0", "0.3.0", "0.4.0")
	// keep last 1 (0.4.0) + current (0.4.0) => removes 0.1.0/0.2.0/0.3.0
	removed, err := l.Prune(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 3 {
		t.Fatalf("removed %v, want 3", removed)
	}
	infos, _ := l.List()
	if len(infos) != 1 || infos[0].Version != "0.4.0" {
		t.Fatalf("after prune got %v, want [0.4.0]", infos)
	}
}

func TestPrune_NeverRemovesCurrent(t *testing.T) {
	// current is the OLDEST version; keep last 1 would target the newest, but
	// current must survive regardless.
	_, l := newTree(t, "0.1.0", "0.1.0", "0.2.0", "0.3.0")
	removed, err := l.Prune(1)
	if err != nil {
		t.Fatal(err)
	}
	// keep-set = {current 0.1.0} ∪ {last 1 = 0.3.0}; 0.2.0 removed.
	if len(removed) != 1 || removed[0] != "0.2.0" {
		t.Fatalf("removed %v, want [0.2.0]", removed)
	}
	if !l.exists("0.1.0") {
		t.Fatal("current version 0.1.0 was removed by prune")
	}
}

func TestPrune_ClearsDanglingPrevious(t *testing.T) {
	_, l := newTree(t, "0.2.0", "0.1.0", "0.2.0")
	if _, err := l.Use("0.1.0", ""); err != nil { // current=0.1.0, previous=0.2.0
		t.Fatal(err)
	}
	// keep last 1 (0.2.0)? current is 0.1.0. keep-set = {0.1.0, 0.2.0} -> nothing
	// removed. Force removal of previous by keeping 0.
	if _, err := l.Use("0.2.0", ""); err != nil { // current=0.2.0, previous=0.1.0
		t.Fatal(err)
	}
	removed, err := l.Prune(0) // keep only current 0.2.0 -> removes 0.1.0 (=previous)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "0.1.0" {
		t.Fatalf("removed %v, want [0.1.0]", removed)
	}
	if l.Previous() != "" {
		t.Errorf("dangling previous should be cleared, got %q", l.Previous())
	}
}

func TestRefreshBinSymlink(t *testing.T) {
	prefix, l := newTree(t, "0.2.0", "0.1.0", "0.2.0")
	binDir := filepath.Join(prefix, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(binDir, "daedalus")
	// Initially points into the prefix (through current).
	if err := os.Symlink(filepath.Join(l.CurrentLink(), "daedalus"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Use("0.1.0", binDir); err != nil {
		t.Fatal(err)
	}
	// The bin symlink should still resolve to the (new) current daedalus.
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatal(err)
	}
	wantResolved, _ := filepath.EvalSymlinks(filepath.Join(prefix, "versions", "0.1.0", "daedalus"))
	if resolved != wantResolved {
		t.Fatalf("bin symlink resolves to %q, want %q", resolved, wantResolved)
	}
}
