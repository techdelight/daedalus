// Copyright (C) 2026 Techdelight BV

package mcpclient

import (
	"fmt"
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

// ReadSprints reads sprint data from SPRINTS.md, falling back to ROADMAP.md.
func (c *Client) ReadSprints(projectDir string) ([]core.Sprint, error) {
	content, err := readFileWithFallback(projectDir, "SPRINTS.md", "ROADMAP.md")
	if err != nil {
		return nil, err
	}
	if content == "" {
		return nil, nil
	}
	return core.ParseSprints(content), nil
}

// ReadBacklog reads backlog items from BACKLOG.md.
func (c *Client) ReadBacklog(projectDir string) ([]core.BacklogItem, error) {
	path := filepath.Join(projectDir, "BACKLOG.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return core.ParseBacklog(string(data)), nil
}

// ReadStrategicRoadmap reads the strategic ROADMAP.md as raw markdown.
func (c *Client) ReadStrategicRoadmap(projectDir string) (string, error) {
	path := filepath.Join(projectDir, "ROADMAP.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// ReadRoadmap is a backward-compatible alias for ReadSprints.
// Deprecated: use ReadSprints for new code.
func (c *Client) ReadRoadmap(projectDir string) ([]core.Sprint, error) {
	return c.ReadSprints(projectDir)
}

// GetCurrentSprint returns the current sprint from SPRINTS.md (or legacy ROADMAP.md).
func (c *Client) GetCurrentSprint(projectDir string) (*core.Sprint, error) {
	sprints, err := c.ReadSprints(projectDir)
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

// readFileWithFallback tries to read primary, then falls back to fallback.
// Returns empty string if neither exists.
func readFileWithFallback(dir, primary, fallback string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, primary))
	if err == nil {
		return string(data), nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	data, err = os.ReadFile(filepath.Join(dir, fallback))
	if err == nil {
		return string(data), nil
	}
	if os.IsNotExist(err) {
		return "", nil
	}
	return "", err
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
		return ProjectStatus{Name: name}, fmt.Errorf("reading progress for %q: %w", name, err)
	}
	sprint, err := c.GetCurrentSprint(projectDir)
	if err != nil {
		return ProjectStatus{Name: name}, fmt.Errorf("reading roadmap for %q: %w", name, err)
	}
	return ProjectStatus{
		Name:           name,
		ProgressPct:    prog.ProgressPct,
		Vision:         prog.Vision,
		ProjectVersion: prog.ProjectVersion,
		Message:        prog.Message,
		CurrentSprint:  sprint,
	}, nil
}
