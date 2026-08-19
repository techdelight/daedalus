// Copyright (C) 2026 Techdelight BV

package coordinator

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestComposeRunsAnInitThatReaps DERIVES the requirement rather than asserting a
// remembered line: it reads entrypoint.sh, and only if that entrypoint hands pid
// 1 to daedalus-runner does it require `init: true` in the compose service.
//
// The defect it guards, measured on a real session (2026-08-19): the container
// held 394 zombies out of 405 processes — 321 chrome-headless, 70 esbuild — with
// only 68 live tasks against a `pids_limit` of 512. `entrypoint.sh` execs the
// runner, so the runner IS pid 1; it waits on its one direct child and nothing
// else; and every process a tool orphaned was re-parented to it, exited, and
// stayed a zombie because only a parent can clear one. The pids cgroup counts
// each zombie, so the container walks to its cap and can no longer fork. It is
// not recoverable from inside a running container: `kill -CHLD 1` does nothing,
// a zombie cannot be killed twice, and `pids.max` is on a read-only mount.
//
// If someone later makes entrypoint.sh exec a real init and run the runner as
// its child, this test stands down on its own — the requirement is about which
// process ends up at pid 1, not about a line in a YAML file.
func TestComposeRunsAnInitThatReaps(t *testing.T) {
	root := filepath.Join("..", "..")
	entrypoint, err := os.ReadFile(filepath.Join(root, "entrypoint.sh"))
	if err != nil {
		t.Skipf("entrypoint.sh not readable from here: %v", err)
	}
	compose, err := os.ReadFile(filepath.Join(root, "docker-compose.yml"))
	if err != nil {
		t.Skipf("docker-compose.yml not readable from here: %v", err)
	}

	// Does the entrypoint hand pid 1 straight to the runner? `exec` replaces the
	// shell, so whatever it execs becomes pid 1.
	execsRunner := regexp.MustCompile(`(?m)^\s*exec\s+\S*daedalus-runner`).Match(entrypoint)
	if !execsRunner {
		t.Skip("entrypoint.sh no longer execs daedalus-runner as pid 1 — this requirement is moot")
	}
	// A real init in front of it would also settle the question.
	for _, init := range []string{"tini", "dumb-init"} {
		if strings.Contains(string(entrypoint), init) {
			t.Skipf("entrypoint.sh runs %s, so pid 1 already reaps", init)
		}
	}

	if !regexp.MustCompile(`(?m)^\s*init:\s*true\s*$`).Match(compose) {
		t.Error("docker-compose.yml does not set `init: true`, but entrypoint.sh execs " +
			"daedalus-runner as pid 1 and the runner reaps only its own child. Every orphan a " +
			"tool leaves behind becomes a permanent zombie holding a slot against pids_limit, " +
			"until the container cannot fork at all.")
	}

	// The cap only matters because zombies count against it; if it is ever
	// removed the reasoning above still holds, so this is a note rather than a
	// second requirement.
	if !regexp.MustCompile(`(?m)^\s*pids_limit:`).Match(compose) {
		t.Log("note: pids_limit is gone from the compose service; unreaped zombies would then " +
			"exhaust the host's pid space rather than the container's")
	}
}

// TestComposeInitDoesNotDisplaceTheRunnerContract pins the two properties the
// rest of the system depends on and that adding an init could plausibly have
// broken: the service still runs the agent user, and it still keeps stdin open
// with a tty, which is what the runner's PTY needs.
//
// Docker's init sits in front as pid 1 and forwards signals, so the runner's own
// exit code still reaches the coordinator — the value the control plane reads to
// decide success → candidate. That part cannot be asserted from a file, and is
// recorded here so the next person knows it was considered rather than missed.
func TestComposeInitDoesNotDisplaceTheRunnerContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docker-compose.yml"))
	if err != nil {
		t.Skipf("docker-compose.yml not readable from here: %v", err)
	}
	compose := string(data)

	for _, want := range []string{`user: "claude"`, "stdin_open: true", "tty: true"} {
		if !strings.Contains(compose, want) {
			t.Errorf("compose service no longer sets %q — the runner's PTY and the "+
				"container's uid contract both depend on it", want)
		}
	}
}
