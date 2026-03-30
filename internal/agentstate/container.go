// Copyright (C) 2026 Techdelight BV

package agentstate

import (
	"strings"

	"github.com/techdelight/daedalus/internal/executor"
)

// ContainerObserver determines agent state by checking Docker container status.
// This is a basic observer that maps container running/stopped to agent state.
// A future ACP-based observer would provide richer state (thinking, tool_use, etc).
type ContainerObserver struct {
	exec executor.Executor
}

// NewContainerObserver creates a ContainerObserver.
func NewContainerObserver(exec executor.Executor) *ContainerObserver {
	return &ContainerObserver{exec: exec}
}

// GetState returns the agent state based on container status.
func (o *ContainerObserver) GetState(containerName string) State {
	output, err := o.exec.Output("docker", "inspect", "-f", "{{.State.Status}}", containerName)
	if err != nil {
		return StateUnknown
	}
	status := strings.TrimSpace(output)
	switch status {
	case "running":
		return StateRunning
	case "exited", "dead":
		return StateStopped
	case "paused":
		return StateIdle
	default:
		return StateUnknown
	}
}
