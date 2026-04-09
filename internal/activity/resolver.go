// Copyright (C) 2026 Techdelight BV

package activity

import (
	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/agentstate"
)

// Resolver combines container state with runner activity detection
// to produce a unified three-state view: busy, idle, or sleeping.
type Resolver struct {
	observer agentstate.Observer
	detector RunnerActivityDetector
}

// NewResolver creates a Resolver with the given observer and detector.
func NewResolver(observer agentstate.Observer, detector RunnerActivityDetector) *Resolver {
	return &Resolver{observer: observer, detector: detector}
}

// Resolve returns the activity state for the named project.
// If the container is not running, the state is sleeping.
// Otherwise, the runner detector determines busy vs idle.
func (r *Resolver) Resolve(containerName, projectDir string) core.ActivityInfo {
	state := r.observer.GetState(containerName)
	if state != agentstate.StateRunning {
		return core.ActivityInfo{State: core.ActivitySleeping}
	}
	return r.detector.Detect(projectDir)
}
