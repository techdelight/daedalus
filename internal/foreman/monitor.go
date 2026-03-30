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
		entry, found, err := m.registry.GetProject(fp.Name)
		newFp := core.ForemanProject{
			Name:       fp.Name,
			AgentState: string(agentstate.StateUnknown),
		}

		if err == nil && found {
			status, err := m.client.GetProjectStatus(fp.Name, entry.Directory)
			if err == nil {
				newFp.ProgressPct = status.ProgressPct
				newFp.CurrentSprint = status.CurrentSprint
			}

			containerName := "claude-run-" + fp.Name
			newFp.AgentState = string(m.observer.GetState(containerName))
		}

		updated.ActiveProjects = append(updated.ActiveProjects, newFp)
	}

	updated.Summary = buildSummary(updated.ActiveProjects)

	return updated, nil
}

// buildSummary generates a human-readable summary from project statuses.
func buildSummary(projects []core.ForemanProject) string {
	total := len(projects)
	if total == 0 {
		return "no projects in programme"
	}
	sumPct := 0
	running := 0
	for _, p := range projects {
		sumPct += p.ProgressPct
		if p.AgentState == string(agentstate.StateRunning) {
			running++
		}
	}
	return fmt.Sprintf("%d projects, %d%% avg progress, %d running", total, sumPct/total, running)
}
