// Copyright (C) 2026 Techdelight BV

package core

import (
	"os"
	"strings"
)

// UseRunner reports whether to launch via the runner path — the host-side
// coordinator daemon plus the in-container daedalus-runner (PID 1), which
// fans one PTY out to many clients over a Unix socket — instead of the
// classic tmux launch path.
//
// The runner path is the DEFAULT. Opt back into tmux with DAEDALUS_USE_TMUX=1
// (or DAEDALUS_USE_RUNNER=0). The legacy DAEDALUS_USE_RUNNER=1 — which used to
// be required to reach the runner path — is now a harmless explicit-on and is
// still honoured. This is the single seam the CLI (main.go, launch.go) and the
// Web UI (web.go) share so the default stays consistent across surfaces.
func UseRunner() bool {
	if os.Getenv("DAEDALUS_USE_TMUX") == "1" {
		return false
	}
	switch strings.ToLower(os.Getenv("DAEDALUS_USE_RUNNER")) {
	case "0", "false", "no", "off":
		return false
	}
	return true
}
