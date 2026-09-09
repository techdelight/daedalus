// Copyright (C) 2026 Techdelight BV

package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mountDirs makes real directories, because ProjectMountArgs stats the host
// path: a fixture of made-up paths would exercise only the "does not exist"
// refusal and never reach the argument the mount is actually for.
func mountDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestProjectMountArgs_MountsUnderMntWithReadOnlyHonoured(t *testing.T) {
	data := mountDir(t, "datasets")
	out := mountDir(t, "out")

	args, refused := ProjectMountArgs([]ProjectMount{
		{Name: "datasets", Host: data, ReadOnly: true},
		{Name: "out", Host: out},
	})

	if len(refused) != 0 {
		t.Fatalf("refused = %v, want none", refused)
	}
	want := []string{
		"-v", data + ":/mnt/datasets:ro",
		"-v", out + ":/mnt/out",
	}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

func TestProjectMountArgs_ReadWriteIsTheDefault(t *testing.T) {
	dir := mountDir(t, "scratch")
	args, _ := ProjectMountArgs([]ProjectMount{{Name: "scratch", Host: dir}})
	if len(args) != 2 {
		t.Fatalf("args = %v, want one mount", args)
	}
	// The documented default. A silent :ro here would look identical at launch
	// and fail on the agent's first write, far from this decision.
	if strings.HasSuffix(args[1], ":ro") {
		t.Errorf("args[1] = %q, want no :ro suffix when ReadOnly is false", args[1])
	}
}

// TestProjectMountArgs_Refusals is the table of everything that must NOT reach
// docker. Each case asserts both halves: the mount is refused, AND no `-v`
// argument was produced from it — a refusal that still emitted the mount would
// pass a "was it rejected?" check while doing the exact damage the rule exists
// to prevent.
func TestProjectMountArgs_Refusals(t *testing.T) {
	dir := mountDir(t, "real")
	file := filepath.Join(dir, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		mount ProjectMount
		want  string // substring of the reason
	}{
		{"escapes /mnt", ProjectMount{Name: "../../etc", Host: dir}, "single path segment"},
		{"nested path as name", ProjectMount{Name: "sub/dir", Host: dir}, "single path segment"},
		{"dot", ProjectMount{Name: ".", Host: dir}, "not a directory name"},
		{"dotdot", ProjectMount{Name: "..", Host: dir}, "not a directory name"},
		{"empty name", ProjectMount{Name: "", Host: dir}, "empty"},
		{"name reads as a flag", ProjectMount{Name: "-rf", Host: dir}, "must start with"},
		{"backslash", ProjectMount{Name: `a\b`, Host: dir}, "single path segment"},
		{"relative host", ProjectMount{Name: "data", Host: "relative/dir"}, "absolute"},
		{"host with colon", ProjectMount{Name: "data", Host: "/tmp/x:/etc"}, "field separator"},
		{"empty host", ProjectMount{Name: "data", Host: ""}, "empty"},
		{"missing host", ProjectMount{Name: "data", Host: filepath.Join(dir, "nope")}, "does not exist"},
		{"host is a file", ProjectMount{Name: "data", Host: file}, "not a directory"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args, refused := ProjectMountArgs([]ProjectMount{tc.mount})
			if len(args) != 0 {
				t.Fatalf("args = %v, want none for a refused mount", args)
			}
			if len(refused) != 1 {
				t.Fatalf("refused = %v, want exactly one", refused)
			}
			if !strings.Contains(refused[0].Reason, tc.want) {
				t.Errorf("reason = %q, want it to mention %q", refused[0].Reason, tc.want)
			}
			// The refusal has to be reportable: a reason nobody can attribute to a
			// row in projects.json is not much better than silence.
			if s := refused[0].String(); !strings.Contains(s, tc.want) {
				t.Errorf("String() = %q, want it to carry the reason", s)
			}
		})
	}
}

func TestProjectMountArgs_OneBadRowDoesNotCostTheGoodOnes(t *testing.T) {
	good := mountDir(t, "good")

	args, refused := ProjectMountArgs([]ProjectMount{
		{Name: "../escape", Host: good},
		{Name: "good", Host: good},
		{Name: "gone", Host: "/definitely/not/here"},
	})

	if len(refused) != 2 {
		t.Fatalf("refused = %v, want 2", refused)
	}
	if len(args) != 2 || args[1] != good+":/mnt/good" {
		t.Errorf("args = %v, want the one valid mount to survive", args)
	}
}

func TestProjectMountArgs_DuplicateNameRefusedFirstWins(t *testing.T) {
	first := mountDir(t, "first")
	second := mountDir(t, "second")

	args, refused := ProjectMountArgs([]ProjectMount{
		{Name: "data", Host: first},
		{Name: "data", Host: second},
	})

	// Two -v flags on one target is decided by docker's argument order, which is
	// not a decision this config should be making silently.
	if len(args) != 2 || args[1] != first+":/mnt/data" {
		t.Errorf("args = %v, want only the first %q mount", args, first)
	}
	if len(refused) != 1 || !strings.Contains(refused[0].Reason, "duplicate") {
		t.Errorf("refused = %v, want the second refused as a duplicate", refused)
	}
}

func TestProjectMountArgs_Empty(t *testing.T) {
	args, refused := ProjectMountArgs(nil)
	if args != nil || refused != nil {
		t.Errorf("ProjectMountArgs(nil) = (%v, %v), want (nil, nil)", args, refused)
	}
}

// A symlink to a directory is accepted: os.Stat resolves it, and docker binds
// what it resolves to. Recorded as a test because it is the one case where "is
// it a directory" and "is the configured path a directory" differ.
func TestProjectMountArgs_SymlinkToDirIsMounted(t *testing.T) {
	real := mountDir(t, "real")
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	args, refused := ProjectMountArgs([]ProjectMount{{Name: "data", Host: link}})
	if len(refused) != 0 {
		t.Fatalf("refused = %v, want none", refused)
	}
	if len(args) != 2 || args[1] != link+":/mnt/data" {
		t.Errorf("args = %v, want the symlink mounted as given", args)
	}
}

func TestProjectMountTarget(t *testing.T) {
	m := ProjectMount{Name: "datasets"}
	if got := m.Target(); got != "/mnt/datasets" {
		t.Errorf("Target() = %q, want /mnt/datasets", got)
	}
	if !strings.HasPrefix(m.Target(), ProjectMountRoot+"/") {
		t.Errorf("Target() = %q, want it under %q", m.Target(), ProjectMountRoot)
	}
}

func TestValidateMountName(t *testing.T) {
	for _, ok := range []string{"data", "d", "data-2", "data_2", "data.2", "0", "A"} {
		if err := ValidateMountName(ok); err != nil {
			t.Errorf("ValidateMountName(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", ".", "..", "a/b", `a\b`, "-a", "_a", ".a", "a b", "a:b", "a*"} {
		if err := ValidateMountName(bad); err == nil {
			t.Errorf("ValidateMountName(%q) = nil, want a refusal", bad)
		}
	}
}
