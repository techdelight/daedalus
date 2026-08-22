// Copyright (C) 2026 Techdelight BV

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/control"
)

// An unknown subcommand must be refused by NAME, and the refusal must list what
// is available. `daedalus control star` is a typo, and a message that only says
// "unknown subcommand" makes the operator go and read the help for a word they
// nearly typed.
func TestManageControl_RefusesAnUnknownSubcommand(t *testing.T) {
	err := manageControl(&core.Config{ControlArgs: []string{"star"}})
	if err == nil {
		t.Fatal("an unknown subcommand should be an error")
	}
	for _, want := range []string{"star", "start", "stop", "restart", "status"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

// No subcommand at all is a usage error, not a default action. Defaulting to
// `start` would mean a mistyped command silently spawned a daemon, and
// defaulting to `stop` does not bear thinking about.
func TestManageControl_RequiresASubcommand(t *testing.T) {
	if err := manageControl(&core.Config{}); err == nil {
		t.Fatal("no subcommand should be an error rather than a default action")
	}
}

// The upgrade case: a running daemon serves the routes it was BUILT with, so a
// new command 404s against a daemon that is behaving perfectly. The pidfile is
// written when the daemon starts and the binary's mtime is when the version was
// installed, so binary-newer-than-pidfile means the process is not the code on
// disk.
func TestDaemonPredatesItsBinary(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "control.pid")
	binPath := filepath.Join(dir, "daedalus-control")
	write := func(path string, at time.Time) {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	opts := control.BootstrapOptions{PIDPath: pidPath, DaemonBin: binPath}

	// Daemon started AFTER the binary was installed: this is the normal case, and
	// it must stay quiet. A warning that fires when nothing is wrong is a warning
	// nobody reads by the third time.
	now := time.Now()
	write(binPath, now.Add(-time.Hour))
	write(pidPath, now)
	if stale, _ := daemonPredatesItsBinary(opts); stale {
		t.Error("a daemon started after its binary was installed is not stale")
	}

	// Binary installed after the daemon started — the upgrade case.
	write(binPath, now.Add(2*time.Hour))
	stale, since := daemonPredatesItsBinary(opts)
	if !stale {
		t.Fatal("a daemon that predates its own binary should be reported")
	}
	if since < time.Hour {
		t.Errorf("age = %s, want roughly the two hours between them", since)
	}

	// A missing pidfile or a missing binary answers "not stale" rather than
	// guessing. Nothing is running, or nothing is installed; either way this
	// heuristic has nothing to say, and saying something anyway would be noise on
	// a fresh install.
	os.Remove(pidPath)
	if stale, _ := daemonPredatesItsBinary(opts); stale {
		t.Error("no pidfile means no claim")
	}
	write(pidPath, now)
	os.Remove(binPath)
	if stale, _ := daemonPredatesItsBinary(opts); stale {
		t.Error("no binary means no claim")
	}
}
