// Copyright (C) 2026 Techdelight BV

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/techdelight/daedalus/internal/progress"
)

// setup creates a server backed by a temp directory and returns a connected
// client session plus a cleanup function.
func setup(t *testing.T) (*mcp.ClientSession, string) {
	t.Helper()
	dir := t.TempDir()
	server := newServer(dir)

	ct, st := mcp.NewInMemoryTransports()
	_, err := server.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, dir
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return res
}

func TestNewServer_RegistersAllTools(t *testing.T) {
	cs, _ := setup(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"report_progress":  false,
		"set_vision":       false,
		"set_version":      false,
		"get_progress":     false,
		"get_roadmap":      false,
		"get_current_sprint": false,
	}
	for _, tool := range res.Tools {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("tool %q not registered", name)
		}
	}
}

func TestReportProgress(t *testing.T) {
	cs, dir := setup(t)
	res := callTool(t, cs, "report_progress", map[string]any{
		"pct":     50,
		"message": "halfway there",
	})
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}

	d, err := progress.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d.ProgressPct != 50 {
		t.Errorf("got pct %d, want 50", d.ProgressPct)
	}
	if d.Message != "halfway there" {
		t.Errorf("got message %q, want %q", d.Message, "halfway there")
	}
}

func TestSetVision(t *testing.T) {
	cs, dir := setup(t)
	res := callTool(t, cs, "set_vision", map[string]any{
		"vision": "Build the best project manager",
	})
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}

	d, err := progress.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d.Vision != "Build the best project manager" {
		t.Errorf("got vision %q, want %q", d.Vision, "Build the best project manager")
	}
}

func TestSetVersion(t *testing.T) {
	cs, dir := setup(t)
	res := callTool(t, cs, "set_version", map[string]any{
		"version": "1.2.3",
	})
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}

	d, err := progress.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d.ProjectVersion != "1.2.3" {
		t.Errorf("got version %q, want %q", d.ProjectVersion, "1.2.3")
	}
}

func TestGetProgress_Empty(t *testing.T) {
	cs, _ := setup(t)
	res := callTool(t, cs, "get_progress", map[string]any{})
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	// Should return zero-value data for empty project dir.
	var d progress.Data
	text := res.Content[0].(*mcp.TextContent).Text
	if err := json.Unmarshal([]byte(text), &d); err != nil {
		t.Fatal(err)
	}
	if d.ProgressPct != 0 || d.Vision != "" || d.ProjectVersion != "" || d.Message != "" {
		t.Errorf("expected zero-value data, got %+v", d)
	}
}

func TestGetProgress_AfterUpdate(t *testing.T) {
	cs, dir := setup(t)
	if err := progress.Update(dir, 75, "vision", "2.0.0", "almost done"); err != nil {
		t.Fatal(err)
	}

	res := callTool(t, cs, "get_progress", map[string]any{})
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	var d progress.Data
	text := res.Content[0].(*mcp.TextContent).Text
	if err := json.Unmarshal([]byte(text), &d); err != nil {
		t.Fatal(err)
	}
	if d.ProgressPct != 75 {
		t.Errorf("got pct %d, want 75", d.ProgressPct)
	}
	if d.Vision != "vision" {
		t.Errorf("got vision %q, want %q", d.Vision, "vision")
	}
}

func TestGetRoadmap_NoFile(t *testing.T) {
	cs, _ := setup(t)
	res := callTool(t, cs, "get_roadmap", map[string]any{})
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	var out RoadmapOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Sprints) != 0 {
		t.Errorf("expected 0 sprints, got %d", len(out.Sprints))
	}
}

func TestGetRoadmap_WithFile(t *testing.T) {
	cs, dir := setup(t)
	roadmap := `# ROADMAP

## Current Sprint

### Sprint 1: Foundation (v0.1.0)

| # | Item | Status |
|---|------|--------|
| 1 | Setup project | Done |
| 2 | Add tests | In Progress |
`
	if err := os.WriteFile(filepath.Join(dir, "ROADMAP.md"), []byte(roadmap), 0644); err != nil {
		t.Fatal(err)
	}

	res := callTool(t, cs, "get_roadmap", map[string]any{})
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	var out RoadmapOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Sprints) != 1 {
		t.Fatalf("expected 1 sprint, got %d", len(out.Sprints))
	}
	if out.Sprints[0].Title != "Foundation" {
		t.Errorf("got title %q, want %q", out.Sprints[0].Title, "Foundation")
	}
	if len(out.Sprints[0].Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(out.Sprints[0].Items))
	}
}

func TestGetCurrentSprint_NoFile(t *testing.T) {
	cs, _ := setup(t)
	res := callTool(t, cs, "get_current_sprint", map[string]any{})
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	// No ROADMAP.md means no current sprint; SDK serializes nil *Sprint as zero-value.
	text := res.Content[0].(*mcp.TextContent).Text
	var sprint map[string]any
	if err := json.Unmarshal([]byte(text), &sprint); err != nil {
		t.Fatal(err)
	}
	if sprint["isCurrent"] == true {
		t.Error("expected isCurrent to be absent or false")
	}
}

func TestGetCurrentSprint_WithFile(t *testing.T) {
	cs, dir := setup(t)
	roadmap := `# ROADMAP

## Current Sprint

### Sprint 2: Testing (v0.2.0)

| # | Item | Status |
|---|------|--------|
| 1 | Write unit tests | In Progress |

## Future Sprints

### Sprint 3: Release

| # | Item | Status |
|---|------|--------|
| 1 | Publish docs | |
`
	if err := os.WriteFile(filepath.Join(dir, "ROADMAP.md"), []byte(roadmap), 0644); err != nil {
		t.Fatal(err)
	}

	res := callTool(t, cs, "get_current_sprint", map[string]any{})
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	var sprint map[string]any
	if err := json.Unmarshal([]byte(text), &sprint); err != nil {
		t.Fatal(err)
	}
	if sprint["title"] != "Testing" {
		t.Errorf("got title %v, want %q", sprint["title"], "Testing")
	}
	if sprint["isCurrent"] != true {
		t.Errorf("expected isCurrent=true")
	}
}

func TestVersion_DefaultDev(t *testing.T) {
	v := version()
	// In tests, /opt/claude/VERSION likely doesn't exist.
	if v != "dev" {
		t.Logf("version() = %q (VERSION file exists)", v)
	}
}

func TestErrResult(t *testing.T) {
	res := errResult(os.ErrNotExist)
	if !res.IsError {
		t.Error("expected IsError=true")
	}
	if len(res.Content) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(res.Content))
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if text != "file does not exist" {
		t.Errorf("got %q, want %q", text, "file does not exist")
	}
}
