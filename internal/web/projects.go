// Copyright (C) 2026 Techdelight BV

package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/techdelight/daedalus/core"
)

// projectJSON is the JSON representation of a project for the REST API.
type projectJSON struct {
	Name         string `json:"name"`
	Directory    string `json:"directory"`
	Target       string `json:"target"`
	LastUsed     string `json:"lastUsed"`
	Running      bool   `json:"running"`
	SessionCount int    `json:"sessionCount"`
}

// renameRequest is the JSON body for the rename endpoint.
type renameRequest struct {
	NewName string `json:"newName"`
}

// handleListProjects returns all registered projects with their running status.
func (ws *WebServer) handleListProjects(w http.ResponseWriter, r *http.Request) {
	entries, err := ws.registry.GetProjectEntries()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	projects := make([]projectJSON, 0, len(entries))
	for _, e := range entries {
		containerName := core.ContainerNameFor(ws.cfg.ContainerPrefix, e.Name)
		running, err := ws.docker.IsContainerRunning(containerName)
		if err != nil {
			log.Printf("Docker status check failed for %s: %v", e.Name, err)
		}
		projects = append(projects, projectJSON{
			Name:         e.Name,
			Directory:    e.Entry.Directory,
			Target:       e.Entry.Target,
			LastUsed:     e.Entry.LastUsed,
			Running:      running,
			SessionCount: len(e.Entry.Sessions),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(projects)
}

// handleStopProject stops a project's container.
func (ws *WebServer) handleStopProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	_, found, err := ws.registry.GetProject(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, fmt.Sprintf("project %q not found", name), http.StatusNotFound)
		return
	}

	containerName := core.ContainerNameFor(ws.cfg.ContainerPrefix, name)
	if _, err := ws.executor.Output("docker", "stop", containerName); err != nil {
		http.Error(w, fmt.Sprintf("stopping container: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "stopped", "project": name})
}

// handleRenameProject renames a project.
func (ws *WebServer) handleRenameProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	_, found, err := ws.registry.GetProject(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, fmt.Sprintf("project %q not found", name), http.StatusNotFound)
		return
	}

	var req renameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := core.ValidateProjectName(req.NewName); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	containerName := core.ContainerNameFor(ws.cfg.ContainerPrefix, name)
	running, err := ws.docker.IsContainerRunning(containerName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if running {
		http.Error(w, fmt.Sprintf("project %q is running — stop it before renaming", name), http.StatusConflict)
		return
	}

	if err := ws.registry.RenameProject(name, req.NewName); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "renamed", "oldName": name, "newName": req.NewName})
}
