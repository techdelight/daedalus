// Copyright (C) 2026 Techdelight BV

package coordinator

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/control"
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

// listenAgentSocket binds a real Unix socket where the control plane's agent
// listener would be, so Start sees the same thing it sees on a host with the
// plane running.
func listenAgentSocket(t *testing.T, cfg *core.Config) string {
	t.Helper()
	path := control.AgentSocketPath(cfg.DataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen on %s: %v", path, err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return path
}

func TestStart_GuildMaster_MountsTheRestrictedControlSocket(t *testing.T) {
	cfg := configFor(t, core.GuildMasterName)
	seedRegistry(t, cfg)
	sock := listenAgentSocket(t, cfg)

	args := capturedRunArgs(t, cfg)
	joined := strings.Join(args, " ")

	wantMount := sock + ":" + core.GuildControlSocketTarget
	if !strings.Contains(joined, wantMount) {
		t.Errorf("guild master run missing the agent-socket mount %q; args = %v", wantMount, args)
	}
	if !hasEnvFlag(args, "DAEDALUS_CONTROL_AGENT_SOCKET="+core.GuildControlSocketTarget) {
		t.Errorf("guild master run missing -e DAEDALUS_CONTROL_AGENT_SOCKET; args = %v", args)
	}
	// The HUMAN socket must never be mounted: caller class is decided by the file.
	if strings.Contains(joined, "control.sock:") {
		t.Errorf("the human control socket was mounted into the Guild Master; args = %v", args)
	}
}

// Without a running plane there is no socket, and the launch must proceed as the
// read-only overseer rather than fail — and, critically, must NOT set the env var
// that tells the entrypoint where to look. The mount and the gate move together.
func TestStart_GuildMaster_NoControlPlane_NoMountAndNoEnv(t *testing.T) {
	cfg := configFor(t, core.GuildMasterName)
	seedRegistry(t, cfg)

	args := capturedRunArgs(t, cfg)
	joined := strings.Join(args, " ")

	if strings.Contains(joined, "control-agent.sock") {
		t.Errorf("mounted an agent socket that does not exist; args = %v", args)
	}
	if hasEnvFlag(args, "DAEDALUS_CONTROL_AGENT_SOCKET="+core.GuildControlSocketTarget) {
		t.Errorf("set DAEDALUS_CONTROL_AGENT_SOCKET with no socket mounted; args = %v", args)
	}
	// Still a Guild Master: the read-only visibility is unaffected.
	if !hasEnvFlag(args, "DAEDALUS_GUILD_MASTER=1") {
		t.Errorf("guild master lost its own env when the plane was absent; args = %v", args)
	}
}

func TestStart_NormalProject_NeverGetsTheControlSocket(t *testing.T) {
	cfg := configFor(t, "my-app")
	seedRegistry(t, cfg)
	listenAgentSocket(t, cfg) // present, and still must not be mounted

	args := capturedRunArgs(t, cfg)
	joined := strings.Join(args, " ")

	if strings.Contains(joined, "control-agent.sock") {
		t.Errorf("an ordinary project got the control-plane agent socket; args = %v", args)
	}
	if hasEnvFlag(args, "DAEDALUS_CONTROL_AGENT_SOCKET="+core.GuildControlSocketTarget) {
		t.Errorf("an ordinary project got DAEDALUS_CONTROL_AGENT_SOCKET; args = %v", args)
	}
}
