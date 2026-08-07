// Copyright (C) 2026 Techdelight BV

package coordinator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/registry"
)

// seedRegistry writes a registry under cfg.DataDir containing one ordinary
// project "alpha" whose directory exists on disk, so GuildMounts has something
// to mount.
func seedRegistry(t *testing.T, cfg *core.Config) string {
	t.Helper()
	alphaDir := filepath.Join(cfg.DataDir, "alpha-src")
	if err := os.MkdirAll(alphaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	reg := registry.NewRegistry(cfg.RegistryPath())
	if err := reg.Init(); err != nil {
		t.Fatal(err)
	}
	if err := reg.AddProject("alpha", alphaDir, "dev"); err != nil {
		t.Fatal(err)
	}
	return alphaDir
}

// capturedRunArgs runs Start and returns the args passed to `docker compose run`.
func capturedRunArgs(t *testing.T, cfg *core.Config) []string {
	t.Helper()
	var got []string
	exec := newSpyExec()
	exec.onRunWithEnv = func(_ []string, _ string, args ...string) {
		got = append([]string(nil), args...)
		touchSocket(t, cfg.RunnerSocketPath())
	}
	c := newTestCoordinator(t, exec)
	if _, err := c.Start(cfg); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return got
}

func TestStart_GuildMaster_MountsOthersAndSetsEnv(t *testing.T) {
	cfg := configFor(t, core.GuildMasterName)
	alphaDir := seedRegistry(t, cfg)

	args := capturedRunArgs(t, cfg)
	joined := strings.Join(args, " ")

	wantMount := alphaDir + ":/guild/alpha:ro"
	if !strings.Contains(joined, wantMount) {
		t.Errorf("guild master run missing read-only mount %q; args = %v", wantMount, args)
	}
	if !hasEnvFlag(args, "DAEDALUS_GUILD_MASTER=1") {
		t.Errorf("guild master run missing -e DAEDALUS_GUILD_MASTER=1; args = %v", args)
	}
}

func TestStart_NormalProject_NoGuildMountsOrEnv(t *testing.T) {
	cfg := configFor(t, "my-app")
	seedRegistry(t, cfg)

	args := capturedRunArgs(t, cfg)
	joined := strings.Join(args, " ")

	if strings.Contains(joined, "/guild/") {
		t.Errorf("normal project got a /guild mount; args = %v", args)
	}
	if hasEnvFlag(args, "DAEDALUS_GUILD_MASTER=1") {
		t.Errorf("normal project got DAEDALUS_GUILD_MASTER=1; args = %v", args)
	}
}

func hasEnvFlag(args []string, kv string) bool {
	for i, a := range args {
		if a == "-e" && i+1 < len(args) && args[i+1] == kv {
			return true
		}
	}
	return false
}
