// Copyright (C) 2026 Techdelight BV

package coordinator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/techdelight/daedalus/core"
)

// writeRegistryWithMounts writes projects.json BY HAND rather than through the
// Registry API, and that is deliberate: the operator configures these mounts by
// editing that file, so the test's fixture is the documented JSON. If the field
// name or shape changes, this stops parsing — which the API-shaped alternative
// would not notice.
func writeRegistryWithMounts(t *testing.T, cfg *core.Config, mounts []core.ProjectMount) {
	t.Helper()
	data := core.RegistryData{
		Version: core.CurrentRegistryVersion,
		Projects: map[string]core.ProjectEntry{
			cfg.ProjectName: {
				Directory: cfg.ProjectDir,
				Target:    cfg.Target,
				Mounts:    mounts,
			},
		},
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.RegistryPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.RegistryPath(), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStart_MountsConfiguredHostDirsUnderMnt(t *testing.T) {
	cfg := configFor(t, "my-app")
	datasets := filepath.Join(cfg.DataDir, "datasets")
	out := filepath.Join(cfg.DataDir, "out")
	for _, d := range []string{datasets, out} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeRegistryWithMounts(t, cfg, []core.ProjectMount{
		{Name: "datasets", Host: datasets, ReadOnly: true},
		{Name: "out", Host: out},
	})

	args := capturedRunArgs(t, cfg)
	joined := strings.Join(args, " ")

	for _, want := range []string{
		datasets + ":/mnt/datasets:ro",
		out + ":/mnt/out",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("compose run missing configured mount %q; args = %v", want, args)
		}
	}
}

func TestStart_ProjectWithoutMountsGetsNone(t *testing.T) {
	cfg := configFor(t, "my-app")
	writeRegistryWithMounts(t, cfg, nil)

	args := capturedRunArgs(t, cfg)
	if joined := strings.Join(args, " "); strings.Contains(joined, "/mnt/") {
		t.Errorf("project configuring no mounts got a /mnt mount; args = %v", args)
	}
}

// A project that is not in the registry at all — a `daedalus-job-*` throwaway
// is the real case — must still start. Reading mounts is an enhancement to the
// launch, never a precondition for it.
func TestStart_UnregisteredProjectStillLaunches(t *testing.T) {
	cfg := configFor(t, "not-in-the-registry")

	args := capturedRunArgs(t, cfg)
	if len(args) == 0 {
		t.Fatal("Start produced no docker args")
	}
	if joined := strings.Join(args, " "); strings.Contains(joined, "/mnt/") {
		t.Errorf("unregistered project got a /mnt mount; args = %v", args)
	}
}

// The refusals from core.ProjectMountArgs must survive the trip through the
// coordinator: a bad row costs its own mount and nothing else — neither the
// launch nor the sibling mount beside it.
func TestStart_RefusedMountDoesNotReachDockerOrStopTheLaunch(t *testing.T) {
	cfg := configFor(t, "my-app")
	good := filepath.Join(cfg.DataDir, "good")
	if err := os.MkdirAll(good, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRegistryWithMounts(t, cfg, []core.ProjectMount{
		{Name: "../../etc", Host: good},
		{Name: "typo", Host: filepath.Join(cfg.DataDir, "does-not-exist")},
		{Name: "good", Host: good},
	})

	args := capturedRunArgs(t, cfg) // Start succeeding at all is half the assertion
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, good+":/mnt/good") {
		t.Errorf("the one valid mount did not survive its bad neighbours; args = %v", args)
	}
	if strings.Contains(joined, "/mnt/../") || strings.Contains(joined, "/etc:") {
		t.Errorf("a name that escapes /mnt reached docker; args = %v", args)
	}
	if strings.Contains(joined, "does-not-exist") {
		t.Errorf("a missing host path reached docker (it would be created root-owned); args = %v", args)
	}
}
