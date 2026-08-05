// Copyright (C) 2026 Techdelight BV

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"

	"github.com/techdelight/daedalus/core"
)

// milestonesJSON is the JSON response for the milestones endpoint.
type milestonesJSON struct {
	Milestones []core.Milestone `json:"milestones"`
}

// overviewJSON is the JSON response for the overview endpoint: everything the
// project-journey dashboard renders, in one payload.
//
// The dashboard tells the project's story as Purpose -> Arc -> Backlog, which
// spans four documents. Fetching them separately cost four round-trips to
// paint one view; this is the one. The granular endpoints stay for their other
// consumers.
//
// CurrentSprint is a pointer because "no current sprint" is a real state that
// must reach the client as null, distinct from a zero-valued Sprint 0.
type overviewJSON struct {
	Vision        string             `json:"vision"`
	Milestones    []core.Milestone   `json:"milestones"`
	CurrentSprint *core.Sprint       `json:"currentSprint"`
	Backlog       []core.BacklogItem `json:"backlog"`
}

// handleMilestones returns the parsed ROADMAP.md milestones for a project.
func (ws *WebServer) handleMilestones(w http.ResponseWriter, r *http.Request) {
	entry, ok := ws.lookupProject(w, r)
	if !ok {
		return
	}

	milestones, err := readMilestones(entry.Directory)
	if err != nil {
		http.Error(w, fmt.Sprintf("reading milestones: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, milestonesJSON{Milestones: milestones})
}

// handleOverview returns the whole project journey in one response.
//
// A missing document is an empty section, never an error: the dashboard's job
// is to show a project the shape of its own gaps, and 404-ing the entire view
// because one file is absent would hide exactly what it exists to reveal. This
// is a deliberate divergence from handleVision, which 404s an absent VISION.md
// — that endpoint is *about* one document, so its absence is the answer; here
// the subject is the project, and one missing file is a detail of it.
func (ws *WebServer) handleOverview(w http.ResponseWriter, r *http.Request) {
	entry, ok := ws.lookupProject(w, r)
	if !ok {
		return
	}

	resp := overviewJSON{
		Milestones: []core.Milestone{},
		Backlog:    []core.BacklogItem{},
	}

	// Vision: the file on disk, empty when absent or a `touch`ed placeholder,
	// matching what the docs badge counts.
	if docPresent(entry.Directory, "VISION.md") {
		data, err := os.ReadFile(filepath.Join(entry.Directory, "VISION.md"))
		if err != nil {
			http.Error(w, fmt.Sprintf("reading vision: %v", err), http.StatusInternalServerError)
			return
		}
		resp.Vision = string(data)
	}

	milestones, err := readMilestones(entry.Directory)
	if err != nil {
		http.Error(w, fmt.Sprintf("reading milestones: %v", err), http.StatusInternalServerError)
		return
	}
	if milestones != nil {
		resp.Milestones = milestones
	}

	sprintData, err := readSprints(entry.Directory)
	if err != nil {
		http.Error(w, fmt.Sprintf("reading sprints: %v", err), http.StatusInternalServerError)
		return
	}
	resp.CurrentSprint = currentSprint(core.ParseSprints(string(sprintData)))

	backlogData, err := os.ReadFile(filepath.Join(entry.Directory, "BACKLOG.md"))
	if err != nil && !os.IsNotExist(err) {
		http.Error(w, fmt.Sprintf("reading backlog: %v", err), http.StatusInternalServerError)
		return
	}
	if items := core.ParseBacklog(string(backlogData)); items != nil {
		resp.Backlog = items
	}

	writeJSON(w, resp)
}

// readMilestones parses ROADMAP.md from dir. A missing file yields no
// milestones and no error — a project that has not written a roadmap has no
// milestones, which is a fact about the project, not a failure to report it.
func readMilestones(dir string) ([]core.Milestone, error) {
	data, err := os.ReadFile(filepath.Join(dir, "ROADMAP.md"))
	if err != nil {
		if os.IsNotExist(err) {
			return []core.Milestone{}, nil
		}
		return nil, err
	}

	milestones := core.ParseMilestones(string(data))
	if milestones == nil {
		return []core.Milestone{}, nil
	}
	return milestones, nil
}

// currentSprint returns the sprint marked current, or nil. The first wins:
// "## Current Sprint" holding two is a malformed document, and picking one
// beats failing the whole view over it.
func currentSprint(sprints []core.Sprint) *core.Sprint {
	for i := range sprints {
		if sprints[i].IsCurrent {
			return &sprints[i]
		}
	}
	return nil
}

// lookupProject resolves the {name} path value, writing the error response
// itself. The bool reports whether the caller should carry on.
func (ws *WebServer) lookupProject(w http.ResponseWriter, r *http.Request) (core.ProjectEntry, bool) {
	name := r.PathValue("name")

	entry, found, err := ws.registry.GetProject(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return entry, false
	}
	if !found {
		http.Error(w, fmt.Sprintf("project %q not found", name), http.StatusNotFound)
		return entry, false
	}
	return entry, true
}

// writeJSON encodes v as the response body.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// milestoneSprintJSON is one sprint in the active-milestone pipeline, tagged
// with its derived ship-pipeline phase and item progress (Milestone 6).
type milestoneSprintJSON struct {
	Number  int              `json:"number"`
	Title   string           `json:"title"`
	Version string           `json:"version,omitempty"`
	Phase   core.SprintPhase `json:"phase"`
	Done    int              `json:"done"`
	Total   int              `json:"total"`
}

// milestoneSprintsJSON is the response for the milestone-sprints endpoint: the
// active (In Progress) milestone and its sprints, each phased. ActiveMilestone
// is null when no milestone is In Progress — a valid state the sidebar renders
// as "no active milestone".
type milestoneSprintsJSON struct {
	ActiveMilestone *core.Milestone       `json:"activeMilestone"`
	Sprints         []milestoneSprintJSON `json:"sprints"`
}

// handleMilestoneSprints returns the active milestone and its sprints, framed
// by ship-pipeline phase (Milestone 6) — the sidebar renders it as a
// Building → Ready → Shipped (+ Proposed) pipeline for the milestone in flight.
func (ws *WebServer) handleMilestoneSprints(w http.ResponseWriter, r *http.Request) {
	entry, ok := ws.lookupProject(w, r)
	if !ok {
		return
	}

	milestones, err := readMilestones(entry.Directory)
	if err != nil {
		http.Error(w, fmt.Sprintf("reading milestones: %v", err), http.StatusInternalServerError)
		return
	}
	sprintData, err := readSprints(entry.Directory)
	if err != nil {
		http.Error(w, fmt.Sprintf("reading sprints: %v", err), http.StatusInternalServerError)
		return
	}

	resp := milestoneSprintsJSON{Sprints: []milestoneSprintJSON{}}
	if active := activeMilestone(milestones); active != nil {
		resp.ActiveMilestone = active
		for _, s := range core.ParseSprints(string(sprintData)) {
			if s.Milestone != active.Number {
				continue
			}
			done, total := core.SprintProgress(s)
			resp.Sprints = append(resp.Sprints, milestoneSprintJSON{
				Number:  s.Number,
				Title:   s.Title,
				Version: s.Version,
				Phase:   core.PhaseOf(s),
				Done:    done,
				Total:   total,
			})
		}
		// Newest sprint first. The frontend groups by phase, so this only
		// orders within a bucket (e.g. shipped sprints most-recent-first).
		sort.Slice(resp.Sprints, func(i, j int) bool {
			return resp.Sprints[i].Number > resp.Sprints[j].Number
		})
	}
	writeJSON(w, resp)
}

// activeMilestone returns a copy of the first milestone marked In Progress, or
// nil when none is — the roadmap's "what are we working on now". A copy so the
// caller can take its address for the nullable JSON field without aliasing the
// slice's backing array.
func activeMilestone(milestones []core.Milestone) *core.Milestone {
	for i := range milestones {
		if milestones[i].Status == core.StatusInProgress {
			m := milestones[i]
			return &m
		}
	}
	return nil
}
