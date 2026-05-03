// Copyright (C) 2026 Techdelight BV

// Package runner provides per-runner adapters that give daedalus-runner
// a uniform, opinionated interface for launching an AI CLI subprocess.
// Each adapter encapsulates the binary path, flag conventions, and
// environment variables for one supported runner (claude, copilot, …).
//
// The adapter runs in-process inside daedalus-runner, which itself is
// PID 1 in the project's container. Adapters must NOT do any host-side
// work — they describe how to spawn a subprocess given a small set of
// runtime options, and that's all.
package runner

// LaunchOptions holds the runtime parameters daedalus-runner passes
// to an adapter when building the launch command. Fields map directly
// to the user-facing CLI flags that influence what the runner does
// once spawned.
type LaunchOptions struct {
	// Debug toggles runner-side debug output. Adapters that do not
	// support a debug flag silently ignore this.
	Debug bool

	// Resume is a session id to resume; empty means start fresh.
	Resume string

	// Prompt is a one-shot headless prompt; empty means interactive.
	// When set, the adapter emits whatever flags its runner uses for
	// non-interactive single-prompt execution.
	Prompt string
}

// Adapter wraps a specific AI CLI runner. The contract is intentionally
// narrow: name yourself, describe how to launch yourself. Future phases
// will extend this surface (event parsing, prompt injection) — for now,
// daedalus-runner only needs to spawn the subprocess.
type Adapter interface {
	// Name returns the canonical adapter name. Stable across versions;
	// used by the registry and by `daedalus --runner <name>`.
	Name() string

	// Command returns the binary, argv, and environment for spawning
	// the runner subprocess. argv excludes the binary itself (so the
	// caller invokes `exec.Command(binary, argv...)`).
	Command(opts LaunchOptions) (binary string, argv []string, env []string)
}
