// Copyright (C) 2026 Techdelight BV

package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/techdelight/daedalus/internal/progress"
)

// dashboardJSON is the JSON representation of a project dashboard.
type dashboardJSON struct {
	Name           string `json:"name"`
	Directory      string `json:"directory"`
	Target         string `json:"target"`
	Running        bool   `json:"running"`
	ProgressPct    int    `json:"progressPct"`
	Vision         string `json:"vision"`
	ProjectVersion string `json:"projectVersion"`
	SessionCount   int    `json:"sessionCount"`
	TotalTimeSec   int    `json:"totalTimeSec"`
	LastUsed       string `json:"lastUsed"`
	Created        string `json:"created"`
}

// activityStateJSON is the JSON response for the project state endpoint.
type activityStateJSON struct {
	Activity       string `json:"activity"`       // busy/idle/sleeping
	Detail         string `json:"detail"`         // tool_use, stop, waiting, etc.
	UpdatedAt      string `json:"updatedAt"`      // RFC3339 timestamp of last state change
	ContainerState string `json:"containerState"` // raw docker state for backward compat
}

// guildMemberJSON is the JSON representation of a project for the guild hall view.
type guildMemberJSON struct {
	Name         string `json:"name"`
	Activity     string `json:"activity"`
	Detail       string `json:"detail"`
	ProgressPct  int    `json:"progressPct"`
	Vision       string `json:"vision"`
	Target       string `json:"target"`
	LastUsed     string `json:"lastUsed"`
	SessionCount int    `json:"sessionCount"`
}

// HandleDashboard is the exported handler for GET /api/projects/{name}/dashboard.
func (ws *WebServer) HandleDashboard(w http.ResponseWriter, r *http.Request) {
	ws.handleDashboard(w, r)
}

// HandleAgentState is the exported handler for GET /api/projects/{name}/state.
func (ws *WebServer) HandleAgentState(w http.ResponseWriter, r *http.Request) {
	ws.handleAgentState(w, r)
}

// handleDashboard returns dashboard data for a single project.
func (ws *WebServer) handleDashboard(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	entry, found, err := ws.registry.GetProject(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, fmt.Sprintf("project %q not found", name), http.StatusNotFound)
		return
	}

	containerName := "claude-run-" + name
	running, err := ws.docker.IsContainerRunning(containerName)
	if err != nil {
		log.Printf("Docker status check failed for %s: %v", name, err)
	}

	// Read progress data from .daedalus/progress.json in the project directory.
	// This is written by the project-mgmt-mcp server inside the container
	// and visible on the host via the bind mount.
	progData, err := progress.Read(entry.Directory)
	if err != nil {
		log.Printf("read progress for %s: %v", name, err)
	}

	totalTimeSec := 0
	for _, s := range entry.Sessions {
		totalTimeSec += s.Duration
	}

	progressPct := entry.ProgressPct
	vision := entry.Vision
	projectVersion := entry.ProjectVersion

	// Prefer progress file data over registry data (more current)
	if progData.ProgressPct > 0 {
		progressPct = progData.ProgressPct
	}
	if progData.Vision != "" {
		vision = progData.Vision
	}
	if progData.ProjectVersion != "" {
		projectVersion = progData.ProjectVersion
	}

	dash := dashboardJSON{
		Name:           name,
		Directory:      entry.Directory,
		Target:         entry.Target,
		Running:        running,
		ProgressPct:    progressPct,
		Vision:         vision,
		ProjectVersion: projectVersion,
		SessionCount:   len(entry.Sessions),
		TotalTimeSec:   totalTimeSec,
		LastUsed:       entry.LastUsed,
		Created:        entry.Created,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dash)
}

// handleAgentState returns the activity state for a project.
func (ws *WebServer) handleAgentState(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	entry, found, err := ws.registry.GetProject(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, fmt.Sprintf("project %q not found", name), http.StatusNotFound)
		return
	}
	containerName := "claude-run-" + name
	containerState := ws.observer.GetState(containerName)

	runnerName := entry.DefaultFlags["runner"]
	if runnerName == "" {
		runnerName = "claude"
	}
	info := ws.activityResolver.Resolve(containerName, entry.Directory, runnerName)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(activityStateJSON{
		Activity:       string(info.State),
		Detail:         info.Detail,
		UpdatedAt:      info.UpdatedAt,
		ContainerState: string(containerState),
	})
}

// handleGuild returns all projects with unified activity state for the guild hall view.
func (ws *WebServer) handleGuild(w http.ResponseWriter, r *http.Request) {
	entries, err := ws.registry.GetProjectEntries()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	members := make([]guildMemberJSON, 0, len(entries))
	for _, e := range entries {
		containerName := "claude-run-" + e.Name
		runnerName := e.Entry.DefaultFlags["runner"]
		if runnerName == "" {
			runnerName = "claude"
		}
		info := ws.activityResolver.Resolve(containerName, e.Entry.Directory, runnerName)

		progressPct := e.Entry.ProgressPct
		vision := e.Entry.Vision
		progData, err := progress.Read(e.Entry.Directory)
		if err == nil {
			if progData.ProgressPct > 0 {
				progressPct = progData.ProgressPct
			}
			if progData.Vision != "" {
				vision = progData.Vision
			}
		}

		members = append(members, guildMemberJSON{
			Name:         e.Name,
			Activity:     string(info.State),
			Detail:       info.Detail,
			ProgressPct:  progressPct,
			Vision:       vision,
			Target:       e.Entry.Target,
			LastUsed:     e.Entry.LastUsed,
			SessionCount: len(e.Entry.Sessions),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(members)
}
