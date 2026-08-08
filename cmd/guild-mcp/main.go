// Copyright (C) 2026 Techdelight BV

// Command guild-mcp is the Guild Master's read-only, cross-project MCP server.
// It reads the tree of OTHER projects that the launch mounts read-only at
// /guild/<name> (root overridable via GUILD_ROOT / --guild-root so tests can
// point at a fixture) and exposes visibility-only tools over it. It never
// writes another project's files, and it does not — cannot — control other
// agents; this is visibility, not dispatch.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/progress"
)

func main() {
	guildRoot := flag.String("guild-root", resolveGuildRoot(), "root directory holding the read-only /guild/<name> project mounts")
	flag.Parse()

	server := newServer(*guildRoot)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintf(os.Stderr, "guild-mcp: %v\n", err)
		os.Exit(1)
	}
}

// resolveGuildRoot picks GUILD_ROOT when set, else the standard mount root.
func resolveGuildRoot() string {
	if r := os.Getenv("GUILD_ROOT"); r != "" {
		return r
	}
	return core.GuildMountRoot // "/guild"
}

func newServer(guildRoot string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "guild",
		Version: version(),
	}, nil)
	registerGuildTools(server, guildRoot)
	return server
}

// registerGuildTools wires the three read-only cross-project views. Every one
// derives from the mounted /guild/* files, so what they return is always the
// current on-disk truth of each project — there is no state to keep in sync.
func registerGuildTools(server *mcp.Server, guildRoot string) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_guild_projects",
		Description: "List every project visible under the guild root (/guild/<name>), each with a one-line parsed state (its current milestone and/or sprint, or 'no docs').",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, ListOutput, error) {
		projects, err := listGuildProjects(guildRoot)
		if err != nil {
			return errResult(err), ListOutput{}, nil
		}
		return nil, ListOutput{Projects: projects}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "read_project_doc",
		Description: "Read one document from a guild project. `project` is a directory under the guild root; `doc` must be one of the standard project documents (README.md, VISION.md, ARCHITECTURE.md, ROADMAP.md, BACKLOG.md, SPRINTS.md, CHANGELOG.md, CONTRIBUTING.md, CLAUDE.md, VERSION). Read-only, and path traversal is rejected.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ReadDocInput) (*mcp.CallToolResult, ReadDocOutput, error) {
		content, err := readProjectDoc(guildRoot, input.Project, input.Doc)
		if err != nil {
			return errResult(err), ReadDocOutput{}, nil
		}
		return nil, ReadDocOutput{Project: input.Project, Doc: input.Doc, Content: content}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "guild_overview",
		Description: "Return a parsed overview of every guild project: its milestones (from ROADMAP.md), sprints (from SPRINTS.md), and derived progress (version/vision/current-sprint %). Robust to missing or half-written docs — such a project is reported with whatever parsed, not skipped or errored.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, OverviewOutput, error) {
		projects, err := guildOverview(guildRoot)
		if err != nil {
			return errResult(err), OverviewOutput{}, nil
		}
		return nil, OverviewOutput{Projects: projects}, nil
	})
}

// --- tool logic (pure over a guild-root dir; unit-tested directly) ----------

// ProjectSummary is one row of list_guild_projects.
type ProjectSummary struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// ListOutput wraps the list_guild_projects rows.
type ListOutput struct {
	Projects []ProjectSummary `json:"projects"`
}

// ReadDocInput is the input for read_project_doc.
type ReadDocInput struct {
	Project string `json:"project" jsonschema:"The project directory name under the guild root"`
	Doc     string `json:"doc" jsonschema:"The document filename to read (must be a standard project document)"`
}

// ReadDocOutput is the response of read_project_doc.
type ReadDocOutput struct {
	Project string `json:"project"`
	Doc     string `json:"doc"`
	Content string `json:"content"`
}

// ProjectOverview is one project's parsed state for guild_overview.
type ProjectOverview struct {
	Name       string           `json:"name"`
	HasDocs    bool             `json:"hasDocs"`
	Milestones []core.Milestone `json:"milestones"`
	Sprints    []core.Sprint    `json:"sprints"`
	Progress   progress.Data    `json:"progress"`
}

// OverviewOutput wraps the guild_overview rows.
type OverviewOutput struct {
	Projects []ProjectOverview `json:"projects"`
}

// allowedDocs is the read allow-list for read_project_doc: the standard project
// documents plus VERSION and the Guild Master's own CLAUDE.md. Restricting reads
// to this set (no arbitrary path) is the primary traversal guard.
var allowedDocs = buildAllowedDocs()

func buildAllowedDocs() map[string]bool {
	m := map[string]bool{
		"VERSION":   true,
		"CLAUDE.md": true,
	}
	for _, d := range core.RequiredDocs() {
		m[d.Filename] = true
	}
	return m
}

// listGuildProjects enumerates the directories directly under root, each with a
// one-line parsed state. A missing root yields an empty list, not an error — the
// Guild Master may simply have no other projects mounted yet.
func listGuildProjects(root string) ([]ProjectSummary, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return []ProjectSummary{}, nil
		}
		return nil, err
	}
	out := make([]ProjectSummary, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		out = append(out, ProjectSummary{
			Name:  name,
			State: projectStateLine(filepath.Join(root, name)),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// projectStateLine derives a short human-readable status for a project dir: its
// current (In Progress) milestone and/or current sprint. "no docs" when neither
// ROADMAP.md nor SPRINTS.md is present.
func projectStateLine(dir string) string {
	roadmap := readDocFile(dir, "ROADMAP.md")
	sprintsSrc := readDocFile(dir, "SPRINTS.md")
	if roadmap == "" && sprintsSrc == "" {
		return "no docs"
	}

	var parts []string
	if roadmap != "" {
		for _, m := range core.ParseMilestones(roadmap) {
			if m.Status == core.StatusInProgress {
				parts = append(parts, fmt.Sprintf("M%d: %s", m.Number, m.Title))
				break
			}
		}
	}
	src := sprintsSrc
	if src == "" {
		src = roadmap // legacy: sprints may live in ROADMAP.md
	}
	for _, s := range core.ParseSprints(src) {
		if s.IsCurrent {
			parts = append(parts, fmt.Sprintf("Sprint %d: %s", s.Number, s.Title))
			break
		}
	}
	if len(parts) == 0 {
		return "docs present; no current milestone/sprint"
	}
	return strings.Join(parts, " · ")
}

// readProjectDoc reads doc from project under root, with two guards: project
// must be a single safe path segment, and doc must be on the allow-list. Any
// traversal attempt ("../…", an absolute path, a separator) is rejected before
// any file is touched.
func readProjectDoc(root, project, doc string) (string, error) {
	if !safeSegment(project) {
		return "", fmt.Errorf("invalid project name %q", project)
	}
	if !allowedDocs[doc] {
		return "", fmt.Errorf("document %q is not permitted (allowed: standard project documents, VERSION, CLAUDE.md)", doc)
	}
	base := filepath.Join(root, project)
	full := filepath.Join(base, doc)
	// Belt-and-braces: the resolved path must stay within base.
	rel, err := filepath.Rel(base, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal rejected for %q/%q", project, doc)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("document %q not found for project %q", doc, project)
		}
		return "", err
	}
	return string(data), nil
}

// guildOverview parses milestones, sprints, and progress for every project dir
// under root. Missing/half-written docs parse to empty rather than error, so a
// docless project still appears (HasDocs=false) instead of failing the call.
func guildOverview(root string) ([]ProjectOverview, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return []ProjectOverview{}, nil
		}
		return nil, err
	}
	out := make([]ProjectOverview, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		roadmap := readDocFile(dir, "ROADMAP.md")
		sprintsSrc := readDocFile(dir, "SPRINTS.md")
		src := sprintsSrc
		if src == "" {
			src = roadmap
		}
		// progress.Read only errors on a genuine I/O failure of a file that does
		// exist; a missing file is zero. Treat any error as "no derivable
		// progress" so one unreadable project can't sink the whole overview.
		prog, _ := progress.Read(dir)
		out = append(out, ProjectOverview{
			Name:       e.Name(),
			HasDocs:    roadmap != "" || sprintsSrc != "",
			Milestones: core.ParseMilestones(roadmap),
			Sprints:    core.ParseSprints(src),
			Progress:   prog,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// --- helpers ----------------------------------------------------------------

// safeSegment reports whether s is usable as a single path segment: non-empty,
// not "." or "..", no path separator, and unchanged by filepath.Base.
func safeSegment(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	if strings.ContainsAny(s, `/\`) {
		return false
	}
	return filepath.Base(s) == s
}

// readDocFile reads a named doc from dir, returning "" for a missing or
// unreadable file (visibility is best-effort; a bad file is treated as absent).
func readDocFile(dir, name string) string {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return string(data)
}

// errResult returns a CallToolResult indicating an error.
func errResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
		IsError: true,
	}
}

// version reads the in-container VERSION file or returns "dev".
func version() string {
	data, err := os.ReadFile("/opt/claude/VERSION")
	if err != nil {
		return "dev"
	}
	return strings.TrimRight(string(data), "\n")
}
