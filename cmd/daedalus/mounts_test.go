// Copyright (C) 2026 Techdelight BV

package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/registry"
)

// captureStderr runs fn with os.Stderr redirected and returns what it wrote.
// The warning surface is stderr on purpose — it is a note beside the launch,
// not part of the runner's output — so the test has to read it from there.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	fn()
	w.Close()
	os.Stderr = saved
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// registryWithMounts returns a registry holding one project "alpha" configured
// with the given mounts, plus the config that names it.
//
// projects.json is written directly rather than through the registry API,
// because there is no API for mounts and there is not meant to be one: the
// operator edits the file. Writing it here means the fixture is the documented
// shape, and a change to the JSON tag breaks this test rather than passing it.
func registryWithMounts(t *testing.T, mounts []core.ProjectMount) (*core.Config, *registry.Registry) {
	t.Helper()
	dir := t.TempDir()
	cfg := &core.Config{ProjectName: "alpha", ProjectDir: dir, DataDir: dir}
	path := filepath.Join(dir, "projects.json")

	data := core.RegistryData{
		Version: core.CurrentRegistryVersion,
		Projects: map[string]core.ProjectEntry{
			"alpha": {Directory: dir, Target: "dev", Mounts: mounts},
		},
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg, registry.NewRegistry(path)
}

func TestWarnRefusedMounts_NamesTheRowAndTheReason(t *testing.T) {
	cfg, reg := registryWithMounts(t, []core.ProjectMount{
		{Name: "typo", Host: "/definitely/not/here"},
	})

	out := captureStderr(t, func() { warnRefusedMounts(cfg, reg) })

	// All three have to be there, and each is a different failure if it is not:
	// without the NAME the operator cannot find the row, without the REASON they
	// cannot fix it, and without the CONSEQUENCE they may read a warning as
	// cosmetic and go looking for /mnt/typo inside the container.
	for _, want := range []string{"typo", "/definitely/not/here", "does not exist", "will not appear"} {
		if !strings.Contains(out, want) {
			t.Errorf("warning did not mention %q:\n%s", want, out)
		}
	}
}

func TestWarnRefusedMounts_SaysNothingWhenEveryMountIsGood(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, reg := registryWithMounts(t, []core.ProjectMount{
		{Name: "data", Host: filepath.Join(dir, "data")},
	})

	// A warning on a correct config is worse than none: it trains the operator to
	// scroll past the line that will one day matter.
	if out := captureStderr(t, func() { warnRefusedMounts(cfg, reg) }); out != "" {
		t.Errorf("a valid mount produced a warning:\n%s", out)
	}
}

func TestWarnRefusedMounts_WarnsPerRefusedRowOnly(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	if err := os.MkdirAll(good, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, reg := registryWithMounts(t, []core.ProjectMount{
		{Name: "good", Host: good},
		{Name: "../escape", Host: good},
		{Name: "gone", Host: filepath.Join(dir, "gone")},
	})

	out := captureStderr(t, func() { warnRefusedMounts(cfg, reg) })

	if n := strings.Count(out, "Warning:"); n != 2 {
		t.Errorf("warnings = %d, want one per refused row (2):\n%s", n, out)
	}
	if strings.Contains(out, `"good"`) {
		t.Errorf("the valid mount was warned about:\n%s", out)
	}
}

// An unregistered project, an unreadable registry, and a project that configured
// nothing must all pass in silence — this runs on EVERY launch, so anything it
// says when there is nothing to say is noise on every launch.
func TestWarnRefusedMounts_SilentWhenThereIsNothingToSay(t *testing.T) {
	t.Run("project not in the registry", func(t *testing.T) {
		_, reg := registryWithMounts(t, nil)
		cfg := &core.Config{ProjectName: "not-registered"}
		if out := captureStderr(t, func() { warnRefusedMounts(cfg, reg) }); out != "" {
			t.Errorf("unregistered project warned:\n%s", out)
		}
	})
	t.Run("no mounts configured", func(t *testing.T) {
		cfg, reg := registryWithMounts(t, nil)
		if out := captureStderr(t, func() { warnRefusedMounts(cfg, reg) }); out != "" {
			t.Errorf("project with no mounts warned:\n%s", out)
		}
	})
	t.Run("no registry file at all", func(t *testing.T) {
		cfg := &core.Config{ProjectName: "alpha"}
		reg := registry.NewRegistry(filepath.Join(t.TempDir(), "missing.json"))
		if out := captureStderr(t, func() { warnRefusedMounts(cfg, reg) }); out != "" {
			t.Errorf("missing registry warned:\n%s", out)
		}
	})
}
