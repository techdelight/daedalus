// Copyright (C) 2026 Techdelight BV

package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/techdelight/daedalus/core"
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
	Name         string   `json:"name"`
	Activity     string   `json:"activity"`
	Detail       string   `json:"detail"`
	ProgressPct  int      `json:"progressPct"`
	Vision       string   `json:"vision"`
	Target       string   `json:"target"`
	LastUsed     string   `json:"lastUsed"`
	SessionCount int      `json:"sessionCount"`
	Level        int      `json:"level"`
	Achievements []string `json:"achievements"`
}

// guildProgression derives a hero's "level" and earned achievement keys from a
// project's parsed docs. Pure and total (no I/O) so it is unit-testable in
// isolation; the caller does the file reads.
//
// Level rule: level = milestonesDone. When no milestone is marked Done yet, it
// falls back to sprintsShipped, so a young project that has shipped sprints but
// completed no milestone still shows a non-zero level; a project with neither
// milestones nor shipped sprints is Lv 0. (sprintsShipped = sprints carrying a
// non-empty Version — shipped/history sprints record one.)
//
// Achievement keys are returned in a stable order; the frontend owns their
// icon/label/tooltip. Only earned keys are returned.
func guildProgression(milestones []core.Milestone, sprints []core.Sprint, sessionCount int) (int, []string) {
	milestonesDone := 0
	milestoneInProgress := false
	for _, m := range milestones {
		switch m.Status {
		case core.StatusDone:
			milestonesDone++
		case core.StatusInProgress:
			milestoneInProgress = true
		}
	}

	sprintsShipped := 0
	for _, s := range sprints {
		if s.Version != "" {
			sprintsShipped++
		}
	}

	level := milestonesDone
	if milestonesDone == 0 {
		level = sprintsShipped
	}

	// Non-nil so an empty result serialises as [] rather than null.
	achievements := []string{}
	if sprintsShipped >= 1 {
		achievements = append(achievements, "first-release")
	}
	if milestonesDone >= 5 {
		achievements = append(achievements, "milestone-master")
	}
	if milestoneInProgress {
		achievements = append(achievements, "trailblazer")
	}
	if sprintsShipped >= 10 {
		achievements = append(achievements, "sprinter")
	}
	if sessionCount >= 10 {
		achievements = append(achievements, "veteran")
	}
	return level, achievements
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

	containerName := core.ContainerNameFor(ws.cfg.ContainerPrefix, name)
	running, err := ws.docker.IsContainerRunning(containerName)
	if err != nil {
		log.Printf("Docker status check failed for %s: %v", name, err)
	}

	// Derive progress/vision/version from the project's own files (VERSION,
	// VISION.md, SPRINTS.md) — see progress.Read and Backlog #52. Preferred over
	// the registry's stored copy below when a file supplies a value.
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
	containerName := core.ContainerNameFor(ws.cfg.ContainerPrefix, name)
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
		containerName := core.ContainerNameFor(ws.cfg.ContainerPrefix, e.Name)
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

		// Level + achievements from the project's own docs. A missing/half-written
		// ROADMAP/SPRINTS is a normal state — the readers return empty, not error,
		// so the hero simply shows Lv 0 with no badges.
		milestones, err := readMilestones(e.Entry.Directory)
		if err != nil {
			log.Printf("read milestones for %s: %v", e.Name, err)
		}
		sprintData, err := readSprints(e.Entry.Directory)
		if err != nil {
			log.Printf("read sprints for %s: %v", e.Name, err)
		}
		sessionCount := len(e.Entry.Sessions)
		level, achievements := guildProgression(milestones, core.ParseSprints(string(sprintData)), sessionCount)

		members = append(members, guildMemberJSON{
			Name:         e.Name,
			Activity:     string(info.State),
			Detail:       info.Detail,
			ProgressPct:  progressPct,
			Vision:       vision,
			Target:       e.Entry.Target,
			LastUsed:     e.Entry.LastUsed,
			SessionCount: sessionCount,
			Level:        level,
			Achievements: achievements,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(members)
}
