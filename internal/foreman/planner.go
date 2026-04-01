// Copyright (C) 2026 Techdelight BV

package foreman

import (
	"fmt"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/mcpclient"
	"github.com/techdelight/daedalus/internal/programme"
	"github.com/techdelight/daedalus/internal/registry"
)

// Planner builds a ForemanPlan from programme and project data.
type Planner struct {
	programmes *programme.Store
	registry   *registry.Registry
	client     *mcpclient.Client
}

// NewPlanner creates a Planner.
func NewPlanner(programmes *programme.Store, reg *registry.Registry, client *mcpclient.Client) *Planner {
	return &Planner{programmes: programmes, registry: reg, client: client}
}

// BuildPlan reads the programme and gathers status from all member projects.
func (p *Planner) BuildPlan(programmeName string) (*core.ForemanPlan, error) {
	prog, err := p.programmes.Read(programmeName)
	if err != nil {
		return nil, fmt.Errorf("reading programme %q: %w", programmeName, err)
	}

	plan := &core.ForemanPlan{
		Programme: programmeName,
	}

	for _, projName := range prog.Projects {
		entry, found, err := p.registry.GetProject(projName)
		fp := core.ForemanProject{
			Name:       projName,
			AgentState: "unknown",
		}
		if err != nil {
			fp.AgentState = fmt.Sprintf("registry error: %v", err)
		} else if found {
			status, err := p.client.GetProjectStatus(projName, entry.Directory)
			if err == nil {
				fp.ProgressPct = status.ProgressPct
				fp.CurrentSprint = status.CurrentSprint
			}
			// Non-fatal: project may not have progress data yet
		}
		plan.ActiveProjects = append(plan.ActiveProjects, fp)
	}

	plan.Summary = buildSummary(plan.ActiveProjects)

	return plan, nil
}
