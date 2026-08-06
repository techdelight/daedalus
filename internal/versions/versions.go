// Copyright (C) 2026 Techdelight BV

// Package versions manages the side-by-side versioned install layout:
//
//	$PREFIX/
//	  versions/<version>/   one full payload per installed version
//	  current   -> versions/<active>     (symlink; the PATH link resolves through it)
//	  previous  -> versions/<prior>      (symlink; set when current flips; rollback target)
//	  .cache/   shared data dir (registry etc.), never per-version
//
// The active daedalus binary lives at $PREFIX/versions/<v>/daedalus and the
// PATH symlink (~/.local/bin/<link-name>) points at $PREFIX/current/daedalus,
// so switching versions is a single repoint of the `current` symlink.
package versions

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// DefaultPruneKeep is the number of most-recent versions `prune` keeps when
// --keep is not given (the current version is always kept on top of this).
const DefaultPruneKeep = 3

// Info describes one installed version.
type Info struct {
	Version string
	Current bool
}

// Layout resolves the paths of the versioned install rooted at prefix.
type Layout struct {
	Prefix string
}

// VersionsDir is $PREFIX/versions.
func (l Layout) VersionsDir() string { return filepath.Join(l.Prefix, "versions") }

// VersionDir is $PREFIX/versions/<v>.
func (l Layout) VersionDir(v string) string { return filepath.Join(l.VersionsDir(), v) }

// CurrentLink is the $PREFIX/current symlink.
func (l Layout) CurrentLink() string { return filepath.Join(l.Prefix, "current") }

// PreviousLink is the $PREFIX/previous symlink.
func (l Layout) PreviousLink() string { return filepath.Join(l.Prefix, "previous") }

// ResolvePrefix determines the install prefix. DAEDALUS_PREFIX wins (used by
// tests and advanced setups); otherwise it is derived from the running binary,
// which lives at $PREFIX/versions/<v>/daedalus — so $PREFIX is three parent
// directories up from the resolved executable path.
func ResolvePrefix(executable string) (string, error) {
	if p := os.Getenv("DAEDALUS_PREFIX"); p != "" {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", fmt.Errorf("resolving DAEDALUS_PREFIX: %w", err)
		}
		return abs, nil
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("resolving executable symlink: %w", err)
	}
	// $PREFIX/versions/<v>/daedalus -> up three: <v> -> versions -> $PREFIX
	prefix := filepath.Dir(filepath.Dir(filepath.Dir(resolved)))
	return prefix, nil
}

// linkTargetVersion returns the version basename that a symlink (current /
// previous) points at, or "" if the link is missing or dangling.
func linkTargetVersion(link string) string {
	target, err := os.Readlink(link)
	if err != nil {
		return ""
	}
	// Resolve relative links against the link's directory.
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(link), target)
	}
	target = filepath.Clean(target)
	if _, err := os.Stat(target); err != nil {
		return "" // dangling
	}
	return filepath.Base(target)
}

// Current returns the active version, or "" if there is no current link.
func (l Layout) Current() string { return linkTargetVersion(l.CurrentLink()) }

// Previous returns the rollback version, or "" if there is none.
func (l Layout) Previous() string { return linkTargetVersion(l.PreviousLink()) }

// List returns installed versions (semver-descending), marking the current one.
func (l Layout) List() ([]Info, error) {
	entries, err := os.ReadDir(l.VersionsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading versions directory: %w", err)
	}
	current := l.Current()
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sortVersionsDesc(names)
	out := make([]Info, 0, len(names))
	for _, n := range names {
		out = append(out, Info{Version: n, Current: n == current})
	}
	return out, nil
}

// Use makes version the active one: it records the currently-active version as
// `previous`, repoints `current`, and refreshes the PATH symlink. Using the
// already-current version is a no-op (reported by ok=false). binDir may be ""
// to skip PATH-symlink maintenance.
func (l Layout) Use(version, binDir string) (ok bool, err error) {
	if !l.exists(version) {
		return false, fmt.Errorf("version %q is not installed (see 'daedalus version list')", version)
	}
	if l.Current() == version {
		return false, nil
	}
	if prev := l.Current(); prev != "" {
		if err := replaceSymlink(l.PreviousLink(), filepath.Join("versions", prev)); err != nil {
			return false, fmt.Errorf("recording previous version: %w", err)
		}
	}
	if err := replaceSymlink(l.CurrentLink(), filepath.Join("versions", version)); err != nil {
		return false, fmt.Errorf("switching current version: %w", err)
	}
	if err := l.refreshBinSymlink(binDir); err != nil {
		return false, err
	}
	return true, nil
}

// Rollback switches back to the `previous` version.
func (l Layout) Rollback(binDir string) (string, error) {
	prev := l.Previous()
	if prev == "" {
		return "", fmt.Errorf("no previous version to roll back to")
	}
	if !l.exists(prev) {
		return "", fmt.Errorf("previous version %q is no longer installed", prev)
	}
	if _, err := l.Use(prev, binDir); err != nil {
		return "", err
	}
	return prev, nil
}

// Prune removes old versions, keeping the keep most-recent (semver order) plus
// the current version, which is never removed. If keep < 0, DefaultPruneKeep is
// used. It returns the versions actually removed. A `previous` link left
// dangling by a removal is cleared.
func (l Layout) Prune(keep int) ([]string, error) {
	if keep < 0 {
		keep = DefaultPruneKeep
	}
	infos, err := l.List()
	if err != nil {
		return nil, err
	}
	current := l.Current()

	// Build the keep-set: the current version, plus the keep most-recent.
	protected := map[string]bool{}
	if current != "" {
		protected[current] = true
	}
	kept := 0
	for _, info := range infos { // already semver-descending
		if kept >= keep {
			break
		}
		protected[info.Version] = true
		kept++
	}

	var removed []string
	for _, info := range infos {
		if protected[info.Version] {
			continue
		}
		if err := os.RemoveAll(l.VersionDir(info.Version)); err != nil {
			return removed, fmt.Errorf("removing version %q: %w", info.Version, err)
		}
		removed = append(removed, info.Version)
	}

	// A removed version may have been the rollback target; clear a dangling link.
	if l.Previous() == "" {
		_ = os.Remove(l.PreviousLink())
	}
	return removed, nil
}

func (l Layout) exists(version string) bool {
	info, err := os.Stat(l.VersionDir(version))
	return err == nil && info.IsDir()
}

// refreshBinSymlink repoints any symlink in binDir that resolves into this
// prefix at $PREFIX/current/daedalus, so a customized --link-name keeps working
// after a switch. It is best-effort and a no-op when binDir is empty.
func (l Layout) refreshBinSymlink(binDir string) error {
	if binDir == "" {
		return nil
	}
	entries, err := os.ReadDir(binDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading bin directory: %w", err)
	}
	want := filepath.Join(l.CurrentLink(), "daedalus")
	prefixAbs := filepath.Clean(l.Prefix) + string(os.PathSeparator)
	for _, e := range entries {
		link := filepath.Join(binDir, e.Name())
		target, err := os.Readlink(link)
		if err != nil {
			continue // not a symlink
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(binDir, target)
		}
		target = filepath.Clean(target)
		if target == want || strings.HasPrefix(target+string(os.PathSeparator), prefixAbs) {
			if err := replaceSymlinkAbs(link, want); err != nil {
				return fmt.Errorf("refreshing PATH symlink %s: %w", link, err)
			}
		}
	}
	return nil
}

// replaceSymlink atomically replaces link with a symlink to target, where
// target is interpreted relative to the link's parent directory (kept relative
// on disk so the tree is relocatable).
func replaceSymlink(link, relTarget string) error {
	tmp := link + ".tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(relTarget, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, link)
}

// replaceSymlinkAbs is replaceSymlink with an absolute target.
func replaceSymlinkAbs(link, absTarget string) error {
	tmp := link + ".tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(absTarget, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, link)
}

// sortVersionsDesc sorts semantic-ish version strings newest-first. Numeric
// dotted components compare numerically; anything else falls back to a reverse
// lexical compare so non-semver tags (e.g. dev_*) still order deterministically.
func sortVersionsDesc(v []string) {
	sort.SliceStable(v, func(i, j int) bool {
		return compareVersions(v[i], v[j]) > 0
	})
}

// compareVersions returns >0 if a is newer than b, <0 if older, 0 if equal.
func compareVersions(a, b string) int {
	as, bs := splitVersion(a), splitVersion(b)
	for i := 0; i < len(as) || i < len(bs); i++ {
		var an, bn int
		var aok, bok bool
		if i < len(as) {
			an, aok = as[i].num, as[i].isNum
		}
		if i < len(bs) {
			bn, bok = bs[i].num, bs[i].isNum
		}
		switch {
		case aok && bok:
			if an != bn {
				return an - bn
			}
		case i < len(as) && i < len(bs):
			if c := strings.Compare(as[i].str, bs[i].str); c != 0 {
				return c
			}
		case i < len(as):
			return 1 // a has an extra component -> newer
		default:
			return -1
		}
	}
	return 0
}

type verPart struct {
	num   int
	isNum bool
	str   string
}

// splitVersion breaks a version into dot-separated parts, tagging numeric ones.
func splitVersion(v string) []verPart {
	fields := strings.FieldsFunc(v, func(r rune) bool { return r == '.' || r == '-' || r == '_' || r == '+' })
	parts := make([]verPart, 0, len(fields))
	for _, f := range fields {
		if n, err := strconv.Atoi(f); err == nil {
			parts = append(parts, verPart{num: n, isNum: true, str: f})
		} else {
			parts = append(parts, verPart{str: f})
		}
	}
	return parts
}
