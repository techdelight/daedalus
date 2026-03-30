// Copyright (C) 2026 Techdelight BV

package mcpclient

import (
	"os"
	"path/filepath"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/progress"
)

// Client reads project state from the host filesystem.
// Data is written by the project-mgmt-mcp server inside containers
// and visible on the host via bind mounts.
type Client struct{}

// New creates a new MCP client.
func New() *Client {
	return &Client{}
}

// ReadProgress reads progress data for a project from its .daedalus/progress.json.
func (c *Client) ReadProgress(projectDir string) (progress.Data, error) {
	return progress.Read(projectDir)
}

// ReadRoadmap parses the ROADMAP.md from a project directory.
func (c *Client) ReadRoadmap(projectDir string) ([]core.Sprint, error) {
	path := filepath.Join(projectDir, "ROADMAP.md")
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return core.ParseRoadmap(string(content)), nil
}

// GetCurrentSprint returns the current sprint from a project's ROADMAP.md.
func (c *Client) GetCurrentSprint(projectDir string) (*core.Sprint, error) {
	sprints, err := c.ReadRoadmap(projectDir)
	if err != nil {
		return nil, err
	}
	for i := range sprints {
		if sprints[i].IsCurrent {
			return &sprints[i], nil
		}
	}
	return nil, nil
}

// ProjectStatus aggregates progress and sprint data for a project.
type ProjectStatus struct {
	Name           string       `json:"name"`
	ProgressPct    int          `json:"progressPct"`
	Vision         string       `json:"vision,omitempty"`
	ProjectVersion string       `json:"projectVersion,omitempty"`
	Message        string       `json:"message,omitempty"`
	CurrentSprint  *core.Sprint `json:"currentSprint,omitempty"`
}

// GetProjectStatus returns aggregated status for a project.
func (c *Client) GetProjectStatus(name, projectDir string) (ProjectStatus, error) {
	prog, err := c.ReadProgress(projectDir)
	if err != nil {
		return ProjectStatus{Name: name}, nil // non-fatal
	}
	sprint, _ := c.GetCurrentSprint(projectDir)
	return ProjectStatus{
		Name:           name,
		ProgressPct:    prog.ProgressPct,
		Vision:         prog.Vision,
		ProjectVersion: prog.ProjectVersion,
		Message:        prog.Message,
		CurrentSprint:  sprint,
	}, nil
}
