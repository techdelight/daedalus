// Copyright (C) 2026 Techdelight BV

package foreman

import (
	"fmt"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/agentstate"
	"github.com/techdelight/daedalus/internal/mcpclient"
	"github.com/techdelight/daedalus/internal/registry"
)

// Monitor polls project and agent state and updates the Foreman's plan.
type Monitor struct {
	registry *registry.Registry
	client   *mcpclient.Client
	observer AgentObserver
}

// NewMonitor creates a Monitor.
func NewMonitor(reg *registry.Registry, client *mcpclient.Client, observer AgentObserver) *Monitor {
	return &Monitor{registry: reg, client: client, observer: observer}
}

// UpdatePlan refreshes the plan with current project and agent state.
func (m *Monitor) UpdatePlan(plan *core.ForemanPlan) (*core.ForemanPlan, error) {
	updated := &core.ForemanPlan{
		Programme: plan.Programme,
		Summary:   plan.Summary,
	}

	for _, fp := range plan.ActiveProjects {
		entry, found, _ := m.registry.GetProject(fp.Name)
		newFp := core.ForemanProject{
			Name:       fp.Name,
			AgentState: string(agentstate.StateUnknown),
		}

		if found {
			status, _ := m.client.GetProjectStatus(fp.Name, entry.Directory)
			newFp.ProgressPct = status.ProgressPct
			newFp.CurrentSprint = status.CurrentSprint

			containerName := "claude-run-" + fp.Name
			newFp.AgentState = string(m.observer.GetState(containerName))
		}

		updated.ActiveProjects = append(updated.ActiveProjects, newFp)
	}

	// Update summary
	total := len(updated.ActiveProjects)
	if total > 0 {
		sumPct := 0
		for _, proj := range updated.ActiveProjects {
			sumPct += proj.ProgressPct
		}
		avgPct := sumPct / total
		running := 0
		for _, proj := range updated.ActiveProjects {
			if proj.AgentState == string(agentstate.StateRunning) {
				running++
			}
		}
		updated.Summary = fmt.Sprintf("%d projects, %d%% avg progress, %d running", total, avgPct, running)
	}

	return updated, nil
}
