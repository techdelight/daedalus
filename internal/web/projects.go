// Copyright (C) 2026 Techdelight BV

package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/docker"
	"github.com/techdelight/daedalus/internal/platform"
	"github.com/techdelight/daedalus/internal/session"
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

// HandleListProjects is the exported handler for GET /api/projects.
func (ws *WebServer) HandleListProjects(w http.ResponseWriter, r *http.Request) {
	ws.handleListProjects(w, r)
}

// HandleStartProject is the exported handler for POST /api/projects/{name}/start.
func (ws *WebServer) HandleStartProject(w http.ResponseWriter, r *http.Request) {
	ws.handleStartProject(w, r)
}

// HandleStopProject is the exported handler for POST /api/projects/{name}/stop.
func (ws *WebServer) HandleStopProject(w http.ResponseWriter, r *http.Request) {
	ws.handleStopProject(w, r)
}

// HandleRenameProject is the exported handler for POST /api/projects/{name}/rename.
func (ws *WebServer) HandleRenameProject(w http.ResponseWriter, r *http.Request) {
	ws.handleRenameProject(w, r)
}

// HandleSendEnter is the exported handler for POST /api/projects/{name}/enter.
func (ws *WebServer) HandleSendEnter(w http.ResponseWriter, r *http.Request) {
	ws.handleSendEnter(w, r)
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
		containerName := "claude-run-" + e.Name
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

// handleStartProject starts a project's container and tmux session.
func (ws *WebServer) handleStartProject(w http.ResponseWriter, r *http.Request) {
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

	projCfg := &core.Config{
		ProjectName: name,
		ScriptDir:   ws.cfg.ScriptDir,
		DataDir:     ws.cfg.DataDir,
		ImagePrefix: ws.cfg.ImagePrefix,
	}
	core.ApplyRegistryEntry(projCfg, entry)

	if err := docker.SetupCacheDir(projCfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	running, err := ws.docker.IsContainerRunning(projCfg.ContainerName())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if running {
		http.Error(w, fmt.Sprintf("project %q is already running", name), http.StatusConflict)
		return
	}

	if !ws.docker.ImageExists(projCfg.Image()) {
		http.Error(w, fmt.Sprintf("image %s not found — run daedalus --build %s first", projCfg.Image(), name), http.StatusPreconditionFailed)
		return
	}

	sess := session.NewSession(ws.executor, projCfg.TmuxSession())
	if !sess.Exists() {
		if err := sess.Create(); err != nil {
			http.Error(w, fmt.Sprintf("creating tmux session: %v", err), http.StatusInternalServerError)
			return
		}
	}

	var displayArgs []string
	if projCfg.Display {
		displayArgs, _ = platform.DisplayArgs(
			os.Getenv("DISPLAY"),
			os.Getenv("WAYLAND_DISPLAY"),
			os.Getenv("XDG_RUNTIME_DIR"),
		)
	}
	extraArgs := core.BuildExtraArgs(projCfg, displayArgs, nil)

	claudeArgs := core.BuildRunnerArgs(projCfg)
	dockerCmd := ws.docker.ComposeRunCommand(projCfg.ContainerName(), claudeArgs, extraArgs)
	tmuxCmd := core.BuildTmuxCommand(projCfg, dockerCmd)

	if err := sess.SendKeys(tmuxCmd); err != nil {
		http.Error(w, fmt.Sprintf("sending command to tmux: %v", err), http.StatusInternalServerError)
		return
	}

	if err := ws.registry.TouchProject(name); err != nil {
		log.Printf("Failed to update timestamp for %s: %v", name, err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "started", "project": name})
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

	containerName := "claude-run-" + name
	if _, err := ws.executor.Output("docker", "stop", containerName); err != nil {
		http.Error(w, fmt.Sprintf("stopping container: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "stopped", "project": name})
}

// handleSendEnter sends an Enter keypress to a project's tmux session.
func (ws *WebServer) handleSendEnter(w http.ResponseWriter, r *http.Request) {
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

	sess := session.NewSession(ws.executor, "claude-"+name)
	if !sess.Exists() {
		http.Error(w, fmt.Sprintf("no tmux session for project %q", name), http.StatusNotFound)
		return
	}

	if err := ws.executor.Run("tmux", "send-keys", "-t", "claude-"+name, "Enter"); err != nil {
		http.Error(w, fmt.Sprintf("sending Enter to tmux: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
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

	containerName := "claude-run-" + name
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
