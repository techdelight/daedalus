// Copyright (C) 2026 Techdelight BV

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- fixture -----------------------------------------------------------------

const healthyRoadmap = `# Roadmap

## Milestones

### Milestone 1: Foundations (Done)

Laid the groundwork.

### Milestone 2: Cross-Project (In Progress)

The current focus.

## Current Focus

Milestone 2.
`

const healthySprints = `# Sprints

## Current Sprint

### Sprint 7: Guild Visibility

Goal: read across projects.

Milestone: 2

| # | Item | Status |
|---|------|--------|
| 1 | Mount builder | Done |
| 2 | guild-mcp | In Progress |

## Sprint History

_None yet._
`

// makeGuildRoot builds a fixture guild root with:
//   - "alpha": a healthy project (roadmap + sprints + VERSION + VISION)
//   - "beta": a docless project (empty dir)
func makeGuildRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	alpha := filepath.Join(root, "alpha")
	if err := os.MkdirAll(alpha, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(alpha, "ROADMAP.md"), healthyRoadmap)
	write(t, filepath.Join(alpha, "SPRINTS.md"), healthySprints)
	write(t, filepath.Join(alpha, "VERSION"), "1.2.3\n")
	write(t, filepath.Join(alpha, "VISION.md"), "Alpha's vision line.\n")

	beta := filepath.Join(root, "beta")
	if err := os.MkdirAll(beta, 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// --- direct (pure) logic tests ----------------------------------------------

func TestListGuildProjects(t *testing.T) {
	root := makeGuildRoot(t)
	got, err := listGuildProjects(root)
	if err != nil {
		t.Fatalf("listGuildProjects: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d projects, want 2: %+v", len(got), got)
	}
	// Sorted by name: alpha then beta.
	if got[0].Name != "alpha" || got[1].Name != "beta" {
		t.Fatalf("names = %q, %q; want alpha, beta", got[0].Name, got[1].Name)
	}
	if !strings.Contains(got[0].State, "M2") || !strings.Contains(got[0].State, "Sprint 7") {
		t.Errorf("alpha state = %q, want it to mention M2 and Sprint 7", got[0].State)
	}
	if got[1].State != "no docs" {
		t.Errorf("beta state = %q, want 'no docs'", got[1].State)
	}
}

func TestListGuildProjects_MissingRoot(t *testing.T) {
	got, err := listGuildProjects(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("missing root should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d, want 0", len(got))
	}
}

func TestReadProjectDoc_Healthy(t *testing.T) {
	root := makeGuildRoot(t)
	content, err := readProjectDoc(root, "alpha", "ROADMAP.md")
	if err != nil {
		t.Fatalf("readProjectDoc: %v", err)
	}
	if !strings.Contains(content, "Cross-Project") {
		t.Errorf("content missing expected text: %q", content)
	}
}

func TestReadProjectDoc_RejectsDisallowedDoc(t *testing.T) {
	root := makeGuildRoot(t)
	if _, err := readProjectDoc(root, "alpha", "secrets.txt"); err == nil {
		t.Error("expected rejection of non-allowlisted doc")
	}
}

func TestReadProjectDoc_RejectsTraversal(t *testing.T) {
	root := makeGuildRoot(t)
	// Plant a secret a naive join could escape to.
	write(t, filepath.Join(root, "secret.md"), "top secret")

	for _, tc := range []struct{ project, doc string }{
		{"alpha", "../secret.md"},
		{"alpha", "../../etc/passwd"},
		{"..", "ROADMAP.md"},
		{"../alpha", "ROADMAP.md"},
		{"alpha", "/etc/passwd"},
	} {
		if _, err := readProjectDoc(root, tc.project, tc.doc); err == nil {
			t.Errorf("expected traversal rejection for project=%q doc=%q", tc.project, tc.doc)
		}
	}
}

func TestGuildOverview(t *testing.T) {
	root := makeGuildRoot(t)
	got, err := guildOverview(root)
	if err != nil {
		t.Fatalf("guildOverview: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	alpha := got[0]
	if alpha.Name != "alpha" || !alpha.HasDocs {
		t.Fatalf("alpha = %+v, want name alpha, HasDocs true", alpha)
	}
	if len(alpha.Milestones) != 2 {
		t.Errorf("alpha milestones = %d, want 2", len(alpha.Milestones))
	}
	if len(alpha.Sprints) != 1 {
		t.Errorf("alpha sprints = %d, want 1", len(alpha.Sprints))
	}
	if alpha.Progress.ProjectVersion != "1.2.3" {
		t.Errorf("alpha version = %q, want 1.2.3", alpha.Progress.ProjectVersion)
	}
	beta := got[1]
	if beta.Name != "beta" || beta.HasDocs {
		t.Errorf("beta = %+v, want name beta, HasDocs false", beta)
	}
	if len(beta.Milestones) != 0 || len(beta.Sprints) != 0 {
		t.Errorf("docless beta should have no milestones/sprints: %+v", beta)
	}
}

// --- end-to-end over the MCP transport (handlers invoked as a client) -------

func connect(t *testing.T, root string) *mcp.ClientSession {
	t.Helper()
	server := newServer(root)
	ct, st := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestMCP_ListAndRead(t *testing.T) {
	cs := connect(t, makeGuildRoot(t))

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_guild_projects", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool list: %v", err)
	}
	if res.IsError {
		t.Fatalf("list reported error: %v", res.Content)
	}
	var list ListOutput
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Projects) != 2 {
		t.Fatalf("list returned %d projects, want 2", len(list.Projects))
	}

	// read a permitted doc
	res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "read_project_doc",
		Arguments: map[string]any{"project": "alpha", "doc": "VERSION"},
	})
	if err != nil {
		t.Fatalf("CallTool read: %v", err)
	}
	if res.IsError {
		t.Fatalf("read reported error: %v", res.Content)
	}
	var doc ReadDocOutput
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &doc); err != nil {
		t.Fatalf("decode read: %v", err)
	}
	if strings.TrimSpace(doc.Content) != "1.2.3" {
		t.Errorf("VERSION content = %q, want 1.2.3", doc.Content)
	}
}

// TestSmoke_GuildRootEnv is the host-side smoke: it points GUILD_ROOT at a temp
// fixture (healthy "alpha" + docless "beta"), resolves the root the way main()
// does, and drives all three tools plus a traversal rejection over the MCP
// transport, logging each result. Run with -v to see the output.
func TestSmoke_GuildRootEnv(t *testing.T) {
	root := makeGuildRoot(t)
	t.Setenv("GUILD_ROOT", root)
	if got := resolveGuildRoot(); got != root {
		t.Fatalf("resolveGuildRoot() = %q, want %q (GUILD_ROOT)", got, root)
	}

	cs := connect(t, resolveGuildRoot())
	ctx := context.Background()

	// list_guild_projects
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_guild_projects", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("list_guild_projects failed: err=%v isErr=%v", err, res.IsError)
	}
	t.Logf("list_guild_projects -> %s", res.Content[0].(*mcp.TextContent).Text)

	// read_project_doc (healthy)
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "read_project_doc", Arguments: map[string]any{"project": "alpha", "doc": "ROADMAP.md"}})
	if err != nil || res.IsError {
		t.Fatalf("read_project_doc failed: err=%v isErr=%v", err, res.IsError)
	}
	t.Logf("read_project_doc(alpha, ROADMAP.md) -> %.60s...", res.Content[0].(*mcp.TextContent).Text)

	// guild_overview
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "guild_overview", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("guild_overview failed: err=%v isErr=%v", err, res.IsError)
	}
	t.Logf("guild_overview -> %s", res.Content[0].(*mcp.TextContent).Text)

	// traversal attempt → tool error
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "read_project_doc", Arguments: map[string]any{"project": "alpha", "doc": "../../etc/passwd"}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("traversal attempt was not rejected")
	}
	t.Logf("read_project_doc(alpha, ../../etc/passwd) -> REJECTED: %s", res.Content[0].(*mcp.TextContent).Text)
}

func TestMCP_ReadTraversalIsError(t *testing.T) {
	cs := connect(t, makeGuildRoot(t))
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "read_project_doc",
		Arguments: map[string]any{"project": "alpha", "doc": "../../etc/passwd"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected traversal attempt to be reported as a tool error")
	}
}
