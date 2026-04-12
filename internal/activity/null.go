// Copyright (C) 2026 Techdelight BV

package activity

import "github.com/techdelight/daedalus/core"

// NullDetector is a fallback for runners without activity detection.
// It always reports idle, allowing the resolver to distinguish
// "running but no detection" from "not running" (sleeping).
type NullDetector struct{}

// Detect always returns ActivityIdle for runners without activity hooks.
func (d *NullDetector) Detect(projectDir string) core.ActivityInfo {
	return core.ActivityInfo{State: core.ActivityIdle}
}
