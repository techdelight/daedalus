// Copyright (C) 2026 Techdelight BV

package foreman

import (
	"fmt"
	"time"

	"github.com/techdelight/daedalus/core"
)

// CascadeEvent represents a single cascade action.
type CascadeEvent struct {
	Timestamp  string              `json:"timestamp"`
	Upstream   string              `json:"upstream"`
	Downstream string              `json:"downstream"`
	Strategy   core.CascadeStrategy `json:"strategy"`
	Action     string              `json:"action"` // "propagate", "notify", "skip"
	Message    string              `json:"message"`
}

// CascadeResult holds the outcome of a cascade evaluation.
type CascadeResult struct {
	Programme string         `json:"programme"`
	Events    []CascadeEvent `json:"events"`
	DryRun    bool           `json:"dryRun"`
}

// EvaluateCascade checks which downstream projects should receive work
// after changes complete in the given upstream project.
func EvaluateCascade(prog core.Programme, completedProject string, dryRun bool) CascadeResult {
	graph := core.NewDependencyGraph(prog.Projects, prog.Deps)
	downstreams := graph.Downstreams(completedProject)

	result := CascadeResult{
		Programme: prog.Name,
		DryRun:    dryRun,
	}

	now := time.Now().UTC().Format(time.RFC3339)

	for _, ds := range downstreams {
		// Find the edge to get the strategy
		var strategy core.CascadeStrategy
		for _, e := range prog.Deps {
			if e.Upstream == completedProject && e.Downstream == ds {
				strategy = e.DefaultStrategy()
				break
			}
		}

		event := CascadeEvent{
			Timestamp:  now,
			Upstream:   completedProject,
			Downstream: ds,
			Strategy:   strategy,
		}

		switch strategy {
		case core.CascadeAuto:
			event.Action = "propagate"
			event.Message = fmt.Sprintf("auto-propagate to %s: upstream %s completed", ds, completedProject)
		case core.CascadeNotify:
			event.Action = "notify"
			event.Message = fmt.Sprintf("notify: %s may need updates after %s completed", ds, completedProject)
		case core.CascadeManual:
			event.Action = "skip"
			event.Message = fmt.Sprintf("manual: %s → %s cascade skipped (manual strategy)", completedProject, ds)
		default:
			event.Action = "notify"
			event.Message = fmt.Sprintf("notify (default): %s may need updates after %s completed", ds, completedProject)
		}

		result.Events = append(result.Events, event)
	}

	return result
}
