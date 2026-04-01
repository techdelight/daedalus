// Copyright (C) 2026 Techdelight BV

package foreman

import "github.com/techdelight/daedalus/internal/agentstate"

// AgentObserver is the Foreman's view of agent state observation.
// It wraps the agentstate.Observer interface for use in the Foreman loop.
type AgentObserver interface {
	// IsActive returns true if the agent for the given container is running.
	IsActive(containerName string) bool
	// GetState returns the detailed agent state.
	GetState(containerName string) agentstate.State
}

// DefaultObserver wraps an agentstate.Observer as an AgentObserver.
type DefaultObserver struct {
	inner agentstate.Observer
}

// NewDefaultObserver creates a DefaultObserver.
func NewDefaultObserver(inner agentstate.Observer) *DefaultObserver {
	return &DefaultObserver{inner: inner}
}

// IsActive returns true if the agent is in a running state.
func (o *DefaultObserver) IsActive(containerName string) bool {
	return o.inner.GetState(containerName) == agentstate.StateRunning
}

// GetState delegates to the inner observer.
func (o *DefaultObserver) GetState(containerName string) agentstate.State {
	return o.inner.GetState(containerName)
}
