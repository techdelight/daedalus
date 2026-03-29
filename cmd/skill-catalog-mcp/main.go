// Copyright (C) 2026 Techdelight BV

package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/techdelight/daedalus/internal/catalog"
)

func main() {
	catalogDir := flag.String("catalog-dir", "/opt/skills", "directory containing the shared skill catalog")
	skillsDir := flag.String("skills-dir", "/home/claude/.claude/skills", "directory for installed per-project skills")
	flag.Parse()

	cat := catalog.New(*catalogDir, *skillsDir)
	server := newServer(cat)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintf(os.Stderr, "skill-catalog-mcp: %v\n", err)
		os.Exit(1)
	}
}

func newServer(cat *catalog.Catalog) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "skill-catalog",
		Version: version(),
	}, nil)

	registerTools(server, cat)
	return server
}

func registerTools(server *mcp.Server, cat *catalog.Catalog) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_skills",
		Description: "List all skills available in the shared catalog",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, []catalog.Skill, error) {
		skills, err := cat.List()
		if err != nil {
			return errResult(err), nil, nil
		}
		return nil, skills, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "read_skill",
		Description: "Read the full content of a skill from the catalog",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input NameInput) (*mcp.CallToolResult, ContentOutput, error) {
		content, err := cat.Read(input.Name)
		if err != nil {
			return errResult(err), ContentOutput{}, nil
		}
		return nil, ContentOutput{Content: content}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "install_skill",
		Description: "Install a skill from the catalog into the user's skills directory",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input NameInput) (*mcp.CallToolResult, StatusOutput, error) {
		if err := cat.Install(input.Name); err != nil {
			return errResult(err), StatusOutput{}, nil
		}
		return nil, StatusOutput{Status: "installed " + input.Name}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "uninstall_skill",
		Description: "Remove a skill from the user's skills directory",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input NameInput) (*mcp.CallToolResult, StatusOutput, error) {
		if err := cat.Uninstall(input.Name); err != nil {
			return errResult(err), StatusOutput{}, nil
		}
		return nil, StatusOutput{Status: "uninstalled " + input.Name}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_skill",
		Description: "Create a new skill in the shared catalog",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CreateInput) (*mcp.CallToolResult, StatusOutput, error) {
		if err := cat.Create(input.Name, input.Content); err != nil {
			return errResult(err), StatusOutput{}, nil
		}
		return nil, StatusOutput{Status: "created " + input.Name}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "update_skill",
		Description: "Update an existing skill in the shared catalog",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CreateInput) (*mcp.CallToolResult, StatusOutput, error) {
		if err := cat.Update(input.Name, input.Content); err != nil {
			return errResult(err), StatusOutput{}, nil
		}
		return nil, StatusOutput{Status: "updated " + input.Name}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "remove_skill",
		Description: "Delete a skill from the shared catalog",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input NameInput) (*mcp.CallToolResult, StatusOutput, error) {
		if err := cat.Remove(input.Name); err != nil {
			return errResult(err), StatusOutput{}, nil
		}
		return nil, StatusOutput{Status: "removed " + input.Name}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_installed",
		Description: "List skills currently installed in the user's skills directory",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, []catalog.Skill, error) {
		skills, err := cat.ListInstalled()
		if err != nil {
			return errResult(err), nil, nil
		}
		return nil, skills, nil
	})
}

// NameInput is the input for tools that take a skill name.
type NameInput struct {
	Name string `json:"name" jsonschema:"description=Name of the skill (without .md extension)"`
}

// CreateInput is the input for tools that take a name and content.
type CreateInput struct {
	Name    string `json:"name" jsonschema:"description=Name of the skill (without .md extension)"`
	Content string `json:"content" jsonschema:"description=Full markdown content of the skill"`
}

// ContentOutput wraps skill content for the MCP response.
type ContentOutput struct {
	Content string `json:"content"`
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
