// Copyright (C) 2026 Techdelight BV

package docker

import (
	"testing"

	"github.com/techdelight/daedalus/core"
)

// TestComposeProject_DoesNotVaryWithTheInstallDirectory.
//
// This is the leak, stated as a property. Compose names a project after the
// directory holding its compose file unless told otherwise, and setup.sh installs
// one payload per version at $PREFIX/versions/<version>/ — so an unpinned project
// name changes on every upgrade, and each new project brings a new
// `<project>_default` network that nothing removes.
//
// Measured 2026-08-21: 21 orphaned `dev_*_default` networks on the operator's
// host, Docker's address pools exhausted, and EVERY project failing to start with
// "all predefined address pools have been fully subnetted" — an error that names a
// network and says nothing about daedalus or about whichever project was being
// opened when it finally tipped over.
//
// Asserting the two argv are equal is what makes this a test of the property
// rather than of the string: it fails if -p is dropped, and it fails if the
// project name is ever derived from anything that moves.
func TestComposeProject_DoesNotVaryWithTheInstallDirectory(t *testing.T) {
	// Two versions of the same install — a dev build and a release.
	a := projectArg(t, NewDocker(nil, "/opt/daedalus/versions/dev_20260821134040/docker-compose.yml").
		ComposeRunCommand("claude-run-app", nil, nil))
	b := projectArg(t, NewDocker(nil, "/opt/daedalus/versions/0.54.0/docker-compose.yml").
		ComposeRunCommand("claude-run-app", nil, nil))

	if a != b {
		t.Errorf("compose project differs between installs (%q vs %q) — every upgrade will leak a network", a, b)
	}
	if a != core.ComposeProject {
		t.Errorf("compose project = %q, want %q", a, core.ComposeProject)
	}
}

// projectArg returns the value of -p in a docker compose argv, or "" if the
// project was left to be derived — which is the failure being guarded.
func projectArg(t *testing.T, argv []string) string {
	t.Helper()
	for i, a := range argv {
		if (a == "-p" || a == "--project-name") && i+1 < len(argv) {
			// It has to be a flag to `docker compose`, not to `run`: after the
			// subcommand it would be a different flag with a different meaning, and
			// the network would still be derived from the directory.
			for _, before := range argv[:i] {
				if before == "run" {
					t.Fatalf("-p appears AFTER `run`, so it does not set the compose project: %v", argv)
				}
			}
			return argv[i+1]
		}
	}
	return ""
}
