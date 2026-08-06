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
	projectDir := flag.String("project-dir", "/workspace", "project directory containing ROADMAP.md / SPRINTS.md")
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

	registerReadTools(server, projectDir)
	registerLifecycleTools(server, projectDir)
	return server
}

// registerReadTools registers the read-only views over a project's documents.
//
// These derive everything from the project's own files, so what they return is
// always the current on-disk truth — there is no separate state to keep in sync.
func registerReadTools(server *mcp.Server, projectDir string) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_progress",
		Description: "Get the project's read-side state: version (from VERSION), vision (from VISION.md), and the current sprint's completion percentage (derived from its item statuses). All derived from files — there is no progress to report by hand.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, progress.Data, error) {
		d, err := progress.Read(projectDir)
		if err != nil {
			return errResult(err), progress.Data{}, nil
		}
		return nil, d, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_roadmap",
		Description: "Parse and return all sprints from SPRINTS.md (falls back to ROADMAP.md)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, RoadmapOutput, error) {
		content, err := readSprintsFile(projectDir)
		if err != nil {
			return errResult(err), RoadmapOutput{}, nil
		}
		if content == "" {
			return nil, RoadmapOutput{Sprints: []core.Sprint{}}, nil
		}
		return nil, RoadmapOutput{Sprints: core.ParseSprints(content)}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_current_sprint",
		Description: "Return the current sprint from SPRINTS.md (falls back to ROADMAP.md)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, *core.Sprint, error) {
		content, err := readSprintsFile(projectDir)
		if err != nil {
			return errResult(err), nil, nil
		}
		if content == "" {
			return nil, nil, nil
		}
		sprints := core.ParseSprints(content)
		for i := range sprints {
			if sprints[i].IsCurrent {
				return nil, &sprints[i], nil
			}
		}
		return nil, nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_sprints",
		Description: "Parse and return all sprints from SPRINTS.md (falls back to ROADMAP.md)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, RoadmapOutput, error) {
		content, err := readSprintsFile(projectDir)
		if err != nil {
			return errResult(err), RoadmapOutput{}, nil
		}
		if content == "" {
			return nil, RoadmapOutput{Sprints: []core.Sprint{}}, nil
		}
		return nil, RoadmapOutput{Sprints: core.ParseSprints(content)}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_backlog",
		Description: "Parse and return all backlog items from BACKLOG.md",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, BacklogOutput, error) {
		data, err := os.ReadFile(filepath.Join(projectDir, "BACKLOG.md"))
		if err != nil {
			if os.IsNotExist(err) {
				return nil, BacklogOutput{Items: []core.BacklogItem{}}, nil
			}
			return errResult(err), BacklogOutput{}, nil
		}
		return nil, BacklogOutput{Items: core.ParseBacklog(string(data))}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_strategic_roadmap",
		Description: "Return the raw ROADMAP.md content (strategic milestones and goals)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, StrategicRoadmapOutput, error) {
		data, err := os.ReadFile(filepath.Join(projectDir, "ROADMAP.md"))
		if err != nil {
			if os.IsNotExist(err) {
				return nil, StrategicRoadmapOutput{}, nil
			}
			return errResult(err), StrategicRoadmapOutput{}, nil
		}
		return nil, StrategicRoadmapOutput{Content: string(data)}, nil
	})
}

// registerLifecycleTools registers the write side: the tools that manage the
// milestone/sprint lifecycle by editing ROADMAP.md and SPRINTS.md structurally.
//
// Every one of these reads the documents, applies a surgical edit (via the core
// writer, which preserves all surrounding prose), validates the whole result
// with core.ValidateWrite, and only then writes back — so a change that would
// leave the roadmap inconsistent (two milestones In Progress, a dangling link,
// finishing a milestone whose sprint is still open, …) is refused with a clear
// message and nothing is written. Prefer these over hand-editing the documents:
// the edits stay consistent and the invariants are enforced for you.
func registerLifecycleTools(server *mcp.Server, projectDir string) {
	// --- milestones (ROADMAP.md) ---

	mcp.AddTool(server, &mcp.Tool{
		Name:        "add_milestone",
		Description: "Add a new strategic milestone to ROADMAP.md. It is numbered automatically (one past the highest) and starts Planned (no status parenthetical). Use this to record a new milestone instead of editing ROADMAP.md by hand.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input AddMilestoneInput) (*mcp.CallToolResult, MilestonesOutput, error) {
		roadmap, sprints, err := readBoth(projectDir)
		if err != nil {
			return errResult(err), MilestonesOutput{}, nil
		}
		newRoadmap, number, err := core.AddMilestone(roadmap, input.Title, input.Description)
		if err != nil {
			return errResult(err), MilestonesOutput{}, nil
		}
		return commitRoadmapResult(projectDir, newRoadmap, sprints, fmt.Sprintf("added milestone %d", number))
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "remove_milestone",
		Description: "Remove a milestone (heading and body) from ROADMAP.md by its number. Use for a milestone created in error; to shelve one that is still relevant, pause it instead.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input MilestoneNumberInput) (*mcp.CallToolResult, MilestonesOutput, error) {
		roadmap, sprints, err := readBoth(projectDir)
		if err != nil {
			return errResult(err), MilestonesOutput{}, nil
		}
		newRoadmap, err := core.RemoveMilestone(roadmap, input.Number)
		if err != nil {
			return errResult(err), MilestonesOutput{}, nil
		}
		return commitRoadmapResult(projectDir, newRoadmap, sprints, fmt.Sprintf("removed milestone %d", input.Number))
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "start_milestone",
		Description: "Mark a milestone In Progress — the one milestone currently under way. Refused if another milestone is already In Progress (finish or pause it first): the roadmap names exactly one current focus.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input MilestoneNumberInput) (*mcp.CallToolResult, MilestonesOutput, error) {
		roadmap, sprints, err := readBoth(projectDir)
		if err != nil {
			return errResult(err), MilestonesOutput{}, nil
		}
		newRoadmap, err := core.SetMilestoneStatus(roadmap, input.Number, core.StatusInProgress)
		if err != nil {
			return errResult(err), MilestonesOutput{}, nil
		}
		return commitRoadmapResult(projectDir, newRoadmap, sprints, fmt.Sprintf("milestone %d started", input.Number))
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "finish_milestone",
		Description: "Mark a milestone Done. Refused while a current (non-history) sprint is still linked to it — roll or move that sprint first, so the roadmap never shows finished work with an open sprint under it.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input MilestoneNumberInput) (*mcp.CallToolResult, MilestonesOutput, error) {
		roadmap, sprints, err := readBoth(projectDir)
		if err != nil {
			return errResult(err), MilestonesOutput{}, nil
		}
		newRoadmap, err := core.FinishMilestone(roadmap, sprints, input.Number)
		if err != nil {
			return errResult(err), MilestonesOutput{}, nil
		}
		return commitRoadmapResult(projectDir, newRoadmap, sprints, fmt.Sprintf("milestone %d finished", input.Number))
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "pause_milestone",
		Description: "Put a milestone on hold: mark it (Paused). Distinct from Done — the work is parked, not finished. Start it again later with start_milestone.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input MilestoneNumberInput) (*mcp.CallToolResult, MilestonesOutput, error) {
		roadmap, sprints, err := readBoth(projectDir)
		if err != nil {
			return errResult(err), MilestonesOutput{}, nil
		}
		newRoadmap, err := core.SetMilestoneStatus(roadmap, input.Number, core.StatusPaused)
		if err != nil {
			return errResult(err), MilestonesOutput{}, nil
		}
		return commitRoadmapResult(projectDir, newRoadmap, sprints, fmt.Sprintf("milestone %d paused", input.Number))
	})

	// --- sprints (SPRINTS.md) ---

	mcp.AddTool(server, &mcp.Tool{
		Name:        "add_sprint",
		Description: "Add a new sprint at the top of the Current Sprint section of SPRINTS.md, numbered automatically, linked to a milestone, with an item table built from the given item descriptions (all starting Pending). Only one sprint should be current at a time.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input AddSprintInput) (*mcp.CallToolResult, SprintsOutput, error) {
		roadmap, sprints, err := readBoth(projectDir)
		if err != nil {
			return errResult(err), SprintsOutput{}, nil
		}
		newSprints, number, err := core.AddSprint(sprints, input.Title, input.Milestone, input.Items)
		if err != nil {
			return errResult(err), SprintsOutput{}, nil
		}
		return commitSprintsResult(projectDir, roadmap, newSprints, fmt.Sprintf("added sprint %d", number))
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "remove_sprint",
		Description: "Remove a sprint (heading and body) from SPRINTS.md by its number. Use for a sprint created in error.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SprintNumberInput) (*mcp.CallToolResult, SprintsOutput, error) {
		roadmap, sprints, err := readBoth(projectDir)
		if err != nil {
			return errResult(err), SprintsOutput{}, nil
		}
		newSprints, err := core.RemoveSprint(sprints, input.Number)
		if err != nil {
			return errResult(err), SprintsOutput{}, nil
		}
		return commitSprintsResult(projectDir, roadmap, newSprints, fmt.Sprintf("removed sprint %d", input.Number))
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "move_sprint",
		Description: "Re-link a sprint to a different milestone by rewriting its Milestone: line (adding one if absent). Use when a sprint should advance a different milestone than it was filed under.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input MoveSprintInput) (*mcp.CallToolResult, SprintsOutput, error) {
		roadmap, sprints, err := readBoth(projectDir)
		if err != nil {
			return errResult(err), SprintsOutput{}, nil
		}
		newSprints, err := core.MoveSprint(sprints, input.Number, input.ToMilestone)
		if err != nil {
			return errResult(err), SprintsOutput{}, nil
		}
		return commitSprintsResult(projectDir, roadmap, newSprints, fmt.Sprintf("moved sprint %d to milestone %d", input.Number, input.ToMilestone))
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "start_sprint",
		Description: "Resume a paused sprint: remove its Status: Paused line so its phase is derived from its items again. (A sprint's normal state is derived, not stored, so there is nothing to set when starting a fresh one.)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SprintNumberInput) (*mcp.CallToolResult, SprintsOutput, error) {
		roadmap, sprints, err := readBoth(projectDir)
		if err != nil {
			return errResult(err), SprintsOutput{}, nil
		}
		newSprints, err := core.SetSprintStatus(sprints, input.Number, core.StatusPending)
		if err != nil {
			return errResult(err), SprintsOutput{}, nil
		}
		return commitSprintsResult(projectDir, roadmap, newSprints, fmt.Sprintf("sprint %d resumed", input.Number))
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "finish_sprint",
		Description: "Roll a sprint into Sprint History, stamping the release version on its heading and emptying the current-sprint slot. Refused if the sprint still has unfinished items unless force is set.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input FinishSprintInput) (*mcp.CallToolResult, SprintsOutput, error) {
		roadmap, sprints, err := readBoth(projectDir)
		if err != nil {
			return errResult(err), SprintsOutput{}, nil
		}
		newSprints, err := core.FinishSprint(sprints, input.Number, input.Version, input.Force)
		if err != nil {
			return errResult(err), SprintsOutput{}, nil
		}
		return commitSprintsResult(projectDir, roadmap, newSprints, fmt.Sprintf("sprint %d finished (v%s)", input.Number, input.Version))
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "pause_sprint",
		Description: "Put a sprint on hold: add a Status: Paused line so it reports as Paused regardless of its item state. Resume it later with start_sprint.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SprintNumberInput) (*mcp.CallToolResult, SprintsOutput, error) {
		roadmap, sprints, err := readBoth(projectDir)
		if err != nil {
			return errResult(err), SprintsOutput{}, nil
		}
		newSprints, err := core.SetSprintStatus(sprints, input.Number, core.StatusPaused)
		if err != nil {
			return errResult(err), SprintsOutput{}, nil
		}
		return commitSprintsResult(projectDir, roadmap, newSprints, fmt.Sprintf("sprint %d paused", input.Number))
	})
}

// --- input types (lifecycle tools) ---

// AddMilestoneInput is the input for add_milestone.
type AddMilestoneInput struct {
	Title       string `json:"title" jsonschema:"The milestone title (the text after the colon in the heading)"`
	Description string `json:"description,omitempty" jsonschema:"Prose describing the milestone; may span multiple lines or bullets"`
}

// MilestoneNumberInput is the input for the by-number milestone tools.
type MilestoneNumberInput struct {
	Number int `json:"number" jsonschema:"The milestone number"`
}

// AddSprintInput is the input for add_sprint.
type AddSprintInput struct {
	Title     string   `json:"title" jsonschema:"The sprint title"`
	Milestone int      `json:"milestone,omitempty" jsonschema:"The number of the milestone this sprint advances"`
	Items     []string `json:"items,omitempty" jsonschema:"Item descriptions; each becomes a Pending row in the sprint's table"`
}

// SprintNumberInput is the input for the by-number sprint tools.
type SprintNumberInput struct {
	Number int `json:"number" jsonschema:"The sprint number"`
}

// MoveSprintInput is the input for move_sprint.
type MoveSprintInput struct {
	Number      int `json:"number" jsonschema:"The sprint number to move"`
	ToMilestone int `json:"to_milestone" jsonschema:"The milestone number to link the sprint to"`
}

// FinishSprintInput is the input for finish_sprint.
type FinishSprintInput struct {
	Number  int    `json:"number" jsonschema:"The sprint number to finish"`
	Version string `json:"version" jsonschema:"The release version to stamp on the sprint heading, e.g. 0.42.0"`
	Force   bool   `json:"force,omitempty" jsonschema:"Roll the sprint even if it still has unfinished items"`
}

// --- output types ---

// RoadmapOutput wraps parsed sprints for the MCP response.
type RoadmapOutput struct {
	Sprints []core.Sprint `json:"sprints"`
}

// BacklogOutput wraps parsed backlog items for the MCP response.
type BacklogOutput struct {
	Items []core.BacklogItem `json:"items"`
}

// StrategicRoadmapOutput wraps raw roadmap markdown for the MCP response.
type StrategicRoadmapOutput struct {
	Content string `json:"content"`
}

// MilestonesOutput is the response of a milestone lifecycle tool: a status line
// plus the milestones re-parsed from the freshly written ROADMAP.md.
type MilestonesOutput struct {
	Status     string           `json:"status"`
	Milestones []core.Milestone `json:"milestones"`
}

// SprintsOutput is the response of a sprint lifecycle tool: a status line plus
// the sprints re-parsed from the freshly written SPRINTS.md.
type SprintsOutput struct {
	Status  string        `json:"status"`
	Sprints []core.Sprint `json:"sprints"`
}

// --- write helpers ---

// commitRoadmapResult validates the (newRoadmap, sprints) pair, writes
// ROADMAP.md on success, and returns the tool result with the re-parsed
// milestones. A rejected write (core.InvariantError) or an I/O failure is
// surfaced as a tool error and nothing is written.
func commitRoadmapResult(projectDir, newRoadmap, sprints, status string) (*mcp.CallToolResult, MilestonesOutput, error) {
	if err := core.ValidateWrite(newRoadmap, sprints); err != nil {
		return errResult(err), MilestonesOutput{}, nil
	}
	if err := writeDoc(projectDir, "ROADMAP.md", newRoadmap); err != nil {
		return errResult(err), MilestonesOutput{}, nil
	}
	return nil, MilestonesOutput{Status: status, Milestones: core.ParseMilestones(newRoadmap)}, nil
}

// commitSprintsResult is the SPRINTS.md counterpart of commitRoadmapResult.
func commitSprintsResult(projectDir, roadmap, newSprints, status string) (*mcp.CallToolResult, SprintsOutput, error) {
	if err := core.ValidateWrite(roadmap, newSprints); err != nil {
		return errResult(err), SprintsOutput{}, nil
	}
	if err := writeDoc(projectDir, "SPRINTS.md", newSprints); err != nil {
		return errResult(err), SprintsOutput{}, nil
	}
	return nil, SprintsOutput{Status: status, Sprints: core.ParseSprints(newSprints)}, nil
}

// readBoth reads ROADMAP.md and SPRINTS.md so a mutation to either can be
// validated against the pair. A missing file reads as "" (a project may have
// only one of the two).
func readBoth(projectDir string) (roadmap, sprints string, err error) {
	roadmap, err = readDoc(projectDir, "ROADMAP.md")
	if err != nil {
		return "", "", err
	}
	sprints, err = readDoc(projectDir, "SPRINTS.md")
	if err != nil {
		return "", "", err
	}
	return roadmap, sprints, nil
}

// readDoc reads a named document from projectDir, returning "" if it does not
// exist.
func readDoc(projectDir, name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(projectDir, name))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// writeDoc writes content to a named document in projectDir.
func writeDoc(projectDir, name, content string) error {
	return os.WriteFile(filepath.Join(projectDir, name), []byte(content), 0644)
}

// readSprintsFile reads SPRINTS.md, falling back to ROADMAP.md.
func readSprintsFile(projectDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(projectDir, "SPRINTS.md"))
	if err == nil {
		return string(data), nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	data, err = os.ReadFile(filepath.Join(projectDir, "ROADMAP.md"))
	if err == nil {
		return string(data), nil
	}
	if os.IsNotExist(err) {
		return "", nil
	}
	return "", err
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
