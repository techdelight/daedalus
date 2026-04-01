// Copyright (C) 2026 Techdelight BV

package agentstate

// State represents the observable state of an AI agent.
type State string

const (
	StateUnknown State = "unknown"
	StateIdle    State = "idle"
	StateRunning State = "running"
	StateStopped State = "stopped"
	StateError   State = "error"
)

// Observer provides the ability to observe agent state.
type Observer interface {
	// GetState returns the current state of the agent for the named project.
	GetState(containerName string) State
}
