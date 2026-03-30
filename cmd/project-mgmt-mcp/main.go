// Copyright (C) 2026 Techdelight BV

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/progress"
)

func main() {
	projectDir := flag.String("project-dir", "/workspace", "project directory containing .daedalus/")
	flag.Parse()

	server := newServer(*projectDir)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintf(os.Stderr, "project-mgmt-mcp: %v\n", err)
		os.Exit(1)
	}
}

func newServer(projectDir string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "project-mgmt",
		Version: version(),
	}, nil)

	registerTools(server, projectDir)
	return server
}

func registerTools(server *mcp.Server, projectDir string) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "report_progress",
		Description: "Report project completion progress with an optional status message",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ProgressInput) (*mcp.CallToolResult, StatusOutput, error) {
		if err := progress.Update(projectDir, input.Pct, "", "", input.Message); err != nil {
			return errResult(err), StatusOutput{}, nil
		}
		return nil, StatusOutput{Status: fmt.Sprintf("progress updated to %d%%", input.Pct)}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "set_vision",
		Description: "Set the project vision statement",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input VisionInput) (*mcp.CallToolResult, StatusOutput, error) {
		if err := progress.Update(projectDir, 0, input.Vision, "", ""); err != nil {
			return errResult(err), StatusOutput{}, nil
		}
		return nil, StatusOutput{Status: "vision updated"}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "set_version",
		Description: "Set the project version string",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input VersionInput) (*mcp.CallToolResult, StatusOutput, error) {
		if err := progress.Update(projectDir, 0, "", input.Version, ""); err != nil {
			return errResult(err), StatusOutput{}, nil
		}
		return nil, StatusOutput{Status: "version updated to " + input.Version}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_progress",
		Description: "Get the current project progress data",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, progress.Data, error) {
		d, err := progress.Read(projectDir)
		if err != nil {
			return errResult(err), progress.Data{}, nil
		}
		return nil, d, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_roadmap",
		Description: "Parse and return all sprints from the project's ROADMAP.md",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, RoadmapOutput, error) {
		content, err := os.ReadFile(filepath.Join(projectDir, "ROADMAP.md"))
		if err != nil {
			if os.IsNotExist(err) {
				return nil, RoadmapOutput{Sprints: []core.Sprint{}}, nil
			}
			return errResult(err), RoadmapOutput{}, nil
		}
		sprints := core.ParseRoadmap(string(content))
		return nil, RoadmapOutput{Sprints: sprints}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_current_sprint",
		Description: "Return the current sprint from the project's ROADMAP.md",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, *core.Sprint, error) {
		content, err := os.ReadFile(filepath.Join(projectDir, "ROADMAP.md"))
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil, nil
			}
			return errResult(err), nil, nil
		}
		sprints := core.ParseRoadmap(string(content))
		for i := range sprints {
			if sprints[i].IsCurrent {
				return nil, &sprints[i], nil
			}
		}
		return nil, nil, nil
	})
}

// ProgressInput is the input for the report_progress tool.
type ProgressInput struct {
	Pct     int    `json:"pct" jsonschema:"description=Completion percentage (0-100)"`
	Message string `json:"message,omitempty" jsonschema:"description=Status message"`
}

// VisionInput is the input for the set_vision tool.
type VisionInput struct {
	Vision string `json:"vision" jsonschema:"description=Project vision statement"`
}

// VersionInput is the input for the set_version tool.
type VersionInput struct {
	Version string `json:"version" jsonschema:"description=Project version string"`
}

// RoadmapOutput wraps parsed sprints for the MCP response.
type RoadmapOutput struct {
	Sprints []core.Sprint `json:"sprints"`
}

// StatusOutput wraps a status message for the MCP response.
type StatusOutput struct {
	Status string `json:"status"`
}

// errResult returns a CallToolResult indicating an error.
func errResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
		IsError: true,
	}
}

// version reads the VERSION file or returns "dev".
func version() string {
	data, err := os.ReadFile("/opt/claude/VERSION")
	if err != nil {
		return "dev"
	}
	v := string(data)
	if len(v) > 0 && v[len(v)-1] == '\n' {
		v = v[:len(v)-1]
	}
	return v
}
