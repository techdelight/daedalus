// Copyright (C) 2026 Techdelight BV

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/techdelight/daedalus/core"
)

// roadmapJSON is the JSON response for the sprints (legacy: roadmap) endpoint.
type roadmapJSON struct {
	Sprints []core.Sprint `json:"sprints"`
}

// backlogJSON is the JSON response for the backlog endpoint.
type backlogJSON struct {
	Items []core.BacklogItem `json:"items"`
}

// strategicRoadmapJSON is the JSON response for the strategic-roadmap endpoint.
type strategicRoadmapJSON struct {
	Content string `json:"content"`
}

// HandleRoadmap is the exported handler for GET /api/projects/{name}/roadmap.
func (ws *WebServer) HandleRoadmap(w http.ResponseWriter, r *http.Request) {
	ws.handleRoadmap(w, r)
}

// HandleBacklog is the exported handler for GET /api/projects/{name}/backlog.
func (ws *WebServer) HandleBacklog(w http.ResponseWriter, r *http.Request) {
	ws.handleBacklog(w, r)
}

// HandleStrategicRoadmap is the exported handler for GET /api/projects/{name}/strategic-roadmap.
func (ws *WebServer) HandleStrategicRoadmap(w http.ResponseWriter, r *http.Request) {
	ws.handleStrategicRoadmap(w, r)
}

// handleRoadmap returns parsed sprints for a project. Reads SPRINTS.md
// first, falling back to ROADMAP.md for projects predating the doc split.
// Registered for both /roadmap (legacy) and /sprints (current frontend).
func (ws *WebServer) handleRoadmap(w http.ResponseWriter, r *http.Request) {
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

	data, err := readSprints(entry.Directory)
	if err != nil {
		http.Error(w, fmt.Sprintf("reading sprints: %v", err), http.StatusInternalServerError)
		return
	}

	sprints := core.ParseSprints(string(data))
	if sprints == nil {
		sprints = []core.Sprint{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(roadmapJSON{Sprints: sprints})
}

// handleBacklog returns parsed BACKLOG.md items for a project.
func (ws *WebServer) handleBacklog(w http.ResponseWriter, r *http.Request) {
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

	data, err := os.ReadFile(filepath.Join(entry.Directory, "BACKLOG.md"))
	if err != nil {
		if os.IsNotExist(err) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(backlogJSON{Items: []core.BacklogItem{}})
			return
		}
		http.Error(w, fmt.Sprintf("reading backlog: %v", err), http.StatusInternalServerError)
		return
	}

	items := core.ParseBacklog(string(data))
	if items == nil {
		items = []core.BacklogItem{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(backlogJSON{Items: items})
}

// handleStrategicRoadmap returns the raw ROADMAP.md content for a project.
// Empty content (file missing) is returned as 200 with an empty string so
// the frontend can render its own empty-state without distinguishing 404.
func (ws *WebServer) handleStrategicRoadmap(w http.ResponseWriter, r *http.Request) {
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

	data, err := os.ReadFile(filepath.Join(entry.Directory, "ROADMAP.md"))
	if err != nil && !os.IsNotExist(err) {
		http.Error(w, fmt.Sprintf("reading strategic roadmap: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(strategicRoadmapJSON{Content: string(data)})
}

// readSprints reads SPRINTS.md from dir, falling back to ROADMAP.md.
// Returns (nil, nil) if neither file exists. Mirrors readSprintsFile in
// cmd/project-mgmt-mcp so MCP tools and the web API behave identically.
func readSprints(dir string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(dir, "SPRINTS.md"))
	if err == nil {
		return data, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	data, err = os.ReadFile(filepath.Join(dir, "ROADMAP.md"))
	if err == nil {
		return data, nil
	}
	if os.IsNotExist(err) {
		return nil, nil
	}
	return nil, err
}
