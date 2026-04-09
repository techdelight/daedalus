// Copyright (C) 2026 Techdelight BV

package activity

import "github.com/techdelight/daedalus/core"

// RunnerActivityDetector detects the activity state of a running project.
// Each runner type (Claude Code, Copilot, etc.) provides its own implementation.
type RunnerActivityDetector interface {
	// Detect returns the activity info for the runner in the given project directory.
	// Called only when the container is known to be running.
	Detect(projectDir string) core.ActivityInfo
}
