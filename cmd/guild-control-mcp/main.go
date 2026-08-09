// Copyright (C) 2026 Techdelight BV

// Command guild-control-mcp is the Guild Master's CONTROL client: the MCP server
// through which it can finally act on the control plane, as a gated caller.
//
// Three properties define it, and all three are structural rather than advisory:
//
//  1. INTENT-LEVEL ONLY. It exposes what to accomplish, never how. There is no
//     run_shell, docker_run, mount, git_exec or start_container here, and there
//     never can be: the plane resolves the project through the trusted registry
//     and constructs every execution detail itself. The Guild Master cannot
//     become a privileged remote shell because there is no tool through which to
//     ask for one.
//
//  2. THE RESTRICTED SOCKET ONLY. It talks to control-agent.sock, never
//     control.sock and never coordinator.sock. The socket it is given decides its
//     caller class (internal/control/caller.go), so its authority is fixed by the
//     mount namespace before it sends a single byte. Pointing it at the human
//     socket is not a thing the container can do — that file is not mounted.
//
//  3. TIERED AUTHORITY. Reads and bounded task creation execute. Cancel, retry,
//     replan, integrate, approve and target-resync come back as PROPOSALS for a
//     human to confirm. That is §6's lethal-trifecta answer: this agent reads
//     project-controlled documents, so a poisoned README may propose, never
//     execute.
//
// It is env-gated into the guild-master container only (DAEDALUS_GUILD_MASTER),
// exactly as guild-mcp is, so no ordinary project agent ever receives it.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/control"
)

// defaultAgentSocket is where the launch mounts the restricted control socket
// inside the Guild Master's container.
const defaultAgentSocket = "/var/run/daedalus/control-agent.sock"

func main() {
	socket := flag.String("socket", resolveSocket(), "path to the RESTRICTED control-plane socket (never the human one)")
	flag.Parse()

	server := newServer(control.NewClient(*socket))

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintf(os.Stderr, "guild-control-mcp: %v\n", err)
		os.Exit(1)
	}
}

// resolveSocket picks DAEDALUS_CONTROL_AGENT_SOCKET when set, else the standard
// in-container mount path.
func resolveSocket() string {
	if s := os.Getenv("DAEDALUS_CONTROL_AGENT_SOCKET"); s != "" {
		return s
	}
	return defaultAgentSocket
}

func newServer(api control.TaskAPI) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "guild-control",
		Version: core.Version,
	}, nil)
	registerControlTools(server, api)
	return server
}

// --- tool inputs/outputs --------------------------------------------------------

// CreateTaskInput is the intent-level task request: what to accomplish, for whom.
// Note what is absent — no directory, no image, no command, no branch. The plane
// derives all of it.
type CreateTaskInput struct {
	Project   string `json:"project" jsonschema:"the registered project the work belongs to"`
	Objective string `json:"objective" jsonschema:"what to accomplish, in plain language"`
	// Budget narrowing only: the plane clamps to the project's ceiling and refuses
	// anything wider, so this can reduce scope and never widen it.
	MaxAttempts      int `json:"maxAttempts,omitempty" jsonschema:"optional: cap the attempts for this task (may only narrow the project policy)"`
	WallClockSeconds int `json:"wallClockSeconds,omitempty" jsonschema:"optional: cap the wall-clock seconds per attempt (may only narrow the project policy)"`
}

// TaskRef names a task.
type TaskRef struct {
	TaskID string `json:"taskId" jsonschema:"the task id, e.g. T-12"`
}

// NoteRef names a task with an optional human-readable reason.
type NoteRef struct {
	TaskID string `json:"taskId" jsonschema:"the task id, e.g. T-12"`
	Note   string `json:"note,omitempty" jsonschema:"why this is being requested"`
}

// TaskSummary is the agent-facing view of a task. It deliberately carries no
// filesystem paths.
type TaskSummary struct {
	ID        string `json:"id"`
	Project   string `json:"project"`
	State     string `json:"state"`
	Objective string `json:"objective"`
	BaseSHA   string `json:"baseSha"`
	CreatedAt string `json:"createdAt"`
}

// ListOutput is a list of tasks.
type ListOutput struct {
	Tasks []TaskSummary `json:"tasks"`
}

// StatusOutput is one task with its attempts.
type StatusOutput struct {
	Task TaskSummary `json:"task"`
	Jobs []JobLine   `json:"jobs"`
}

// JobLine is one attempt, flattened for reading.
type JobLine struct {
	ID              string `json:"id"`
	State           string `json:"state"`
	ExecutionResult string `json:"executionResult,omitempty"`
	Verify          string `json:"verify,omitempty"`
	Review          string `json:"review,omitempty"`
}

// EventsOutput is a task's control-plane-managed history.
type EventsOutput struct {
	Events []EventLine `json:"events"`
}

// EventLine is one event, flattened.
type EventLine struct {
	Seq    int64  `json:"seq"`
	Kind   string `json:"kind"`
	Entity string `json:"entity"`
	Actor  string `json:"actor"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Reason string `json:"reason,omitempty"`
	Note   string `json:"note,omitempty"`
	At     string `json:"at"`
}

// OutcomeOutput reports what happened — including, importantly, when the answer
// is "recorded as a proposal for a human".
type OutcomeOutput struct {
	// Executed is false when the request became a proposal instead.
	Executed bool   `json:"executed"`
	Detail   string `json:"detail"`
	// Reason carries the machine-readable refusal (proposal_recorded, forbidden,
	// over_budget…) so the agent can tell "a human must confirm this" from "this
	// was refused outright" without parsing prose.
	Reason string `json:"reason,omitempty"`
}

// --- tools ----------------------------------------------------------------------

func registerControlTools(server *mcp.Server, api control.TaskAPI) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_tasks",
		Description: "List every control-plane task with its state and objective. Read-only.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, ListOutput, error) {
		tasks, err := api.ListTasks()
		if err != nil {
			return errResult(err), ListOutput{}, nil
		}
		out := ListOutput{Tasks: make([]TaskSummary, 0, len(tasks))}
		for _, t := range tasks {
			out.Tasks = append(out.Tasks, summarise(t))
		}
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_task",
		Description: "Show one task with its attempts (jobs) and their verify/review status. Read-only.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in TaskRef) (*mcp.CallToolResult, StatusOutput, error) {
		view, err := api.TaskStatus(in.TaskID)
		if err != nil {
			return errResult(err), StatusOutput{}, nil
		}
		out := StatusOutput{Task: summarise(view.Task)}
		for _, jv := range view.Jobs {
			line := JobLine{ID: jv.Job.ID, State: string(jv.Job.State), ExecutionResult: string(jv.Job.ExecutionResult)}
			if len(jv.Artifacts) > 0 {
				line.Verify = string(jv.Artifacts[0].Verify)
				line.Review = string(jv.Artifacts[0].Review)
			}
			out.Jobs = append(out.Jobs, line)
		}
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "task_events",
		Description: "The control-plane-managed event history for a task: every transition, budget decision, rejection, verification and approval. Read-only, and immutable through this API.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in TaskRef) (*mcp.CallToolResult, EventsOutput, error) {
		events, err := api.TaskEvents(in.TaskID)
		if err != nil {
			return errResult(err), EventsOutput{}, nil
		}
		out := EventsOutput{Events: make([]EventLine, 0, len(events))}
		for _, e := range events {
			out.Events = append(out.Events, EventLine{
				Seq: e.Seq, Kind: e.Kind, Entity: e.EntityID, Actor: e.Actor,
				From: string(e.From), To: string(e.To), Reason: string(e.Reason),
				Note: e.Note, At: e.At,
			})
		}
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_task",
		Description: "Create a task: state WHAT should be accomplished for a project. The control plane resolves the project, pins the base commit, freezes the acceptance policy and applies the project's budget ceiling — none of which this tool can influence. Allowed directly, because it cannot exceed policy.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in CreateTaskInput) (*mcp.CallToolResult, OutcomeOutput, error) {
		request := control.CreateTaskRequest{Project: in.Project, Objective: in.Objective}
		if in.MaxAttempts > 0 || in.WallClockSeconds > 0 {
			request.Budget = &control.Budget{MaxAttempts: in.MaxAttempts, WallClockSeconds: in.WallClockSeconds}
		}
		task, err := api.CreateTask(request)
		if err != nil {
			return nil, outcomeFor(err), nil
		}
		return nil, OutcomeOutput{Executed: true,
			Detail: fmt.Sprintf("created task %s for %s (state %s)", task.ID, task.Project, task.State)}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "request_verification",
		Description: "Ask the control plane to verify a candidate artifact against the project's frozen acceptance policy, in a clean container. Allowed directly: this applies the PLANE's oracle, which the caller cannot influence.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in TaskRef) (*mcp.CallToolResult, OutcomeOutput, error) {
		res, err := api.VerifyTask(in.TaskID)
		if err != nil {
			return nil, outcomeFor(err), nil
		}
		if res.Verified {
			return nil, OutcomeOutput{Executed: true, Detail: "verified: " + res.Detail}, nil
		}
		return nil, OutcomeOutput{Executed: true,
			Detail: fmt.Sprintf("rejected (%s): %s", res.Reason, res.Detail), Reason: string(res.Reason)}, nil
	})

	// --- proposal-tier tools ----------------------------------------------------
	//
	// Each of these calls the plane and reports back what it decided. For this
	// caller they all come back as proposals — the tool exists so the Guild Master
	// can ASK, and the honest answer is "a human has been asked on your behalf".

	proposalTools := []struct {
		name, description string
		call              func(in NoteRef) (string, error)
	}{
		{
			name:        "request_dispatch",
			description: "Ask for a task to be dispatched — one headless attempt in an isolated worktree. Consequential (it spends budget and starts a container), so this is recorded as a proposal for a human to confirm.",
			call: func(in NoteRef) (string, error) {
				_, err := api.DispatchTask(in.TaskID)
				return "dispatch", err
			},
		},
		{
			name:        "request_retry",
			description: "Ask for a rejected task to be retried as a fresh attempt. Consequential, so this is recorded as a proposal for a human to confirm.",
			call: func(in NoteRef) (string, error) {
				_, err := api.RetryTask(in.TaskID, control.RetryRequest{})
				return "retry", err
			},
		},
		{
			name:        "request_replan",
			description: "Ask for a rejected task to be replanned with a revised objective (supply it as the note). Consequential, so this is recorded as a proposal for a human to confirm.",
			call: func(in NoteRef) (string, error) {
				_, err := api.ReplanTask(in.TaskID, control.ReplanRequest{Objective: in.Note})
				return "replan", err
			},
		},
		{
			name:        "request_cancel",
			description: "Ask for a task to be cancelled. Consequential — it destroys another agent's in-flight work — so this is recorded as a proposal for a human to confirm.",
			call: func(in NoteRef) (string, error) {
				_, err := api.CancelTask(in.TaskID)
				return "cancel", err
			},
		},
		{
			name:        "request_integration",
			description: "Ask for a verified, approved task to be landed (rebase onto the target, re-verify the merged result, compare-and-swap). Consequential, so this is recorded as a proposal for a human to confirm. Approval itself is never available to this caller.",
			call: func(in NoteRef) (string, error) {
				_, err := api.IntegrateTask(in.TaskID)
				return "integration", err
			},
		},
	}
	for _, tool := range proposalTools {
		call := tool.call
		mcp.AddTool(server, &mcp.Tool{Name: tool.name, Description: tool.description},
			func(ctx context.Context, req *mcp.CallToolRequest, in NoteRef) (*mcp.CallToolResult, OutcomeOutput, error) {
				what, err := call(in)
				if err != nil {
					return nil, outcomeFor(err), nil
				}
				// Reached only if the plane executed it directly — which for an agent
				// caller means the authority table was changed. Reported honestly
				// rather than assumed impossible.
				return nil, OutcomeOutput{Executed: true, Detail: what + " executed directly"}, nil
			})
	}
}

// summarise flattens a Task for the agent view.
func summarise(t control.Task) TaskSummary {
	return TaskSummary{
		ID: t.ID, Project: t.Project, State: string(t.State),
		Objective: t.Objective, BaseSHA: t.BaseSHA, CreatedAt: t.CreatedAt,
	}
}

// outcomeFor turns a plane error into an outcome the agent can reason about.
//
// A proposal is NOT reported as a success. An agent that believed it had
// cancelled a job when it had only asked would go on reasoning from a false
// premise, which is worse than being told plainly that a human must confirm.
func outcomeFor(err error) OutcomeOutput {
	reason, refused := control.Rejected(err)
	out := OutcomeOutput{Executed: false, Detail: err.Error()}
	if refused {
		out.Reason = string(reason)
		if reason == control.ReasonProposalRecorded {
			out.Detail = "not executed — recorded as a proposal for a human to confirm: " + err.Error()
		}
	}
	return out
}

// errResult renders a read failure as MCP tool-level error text.
func errResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: strings.TrimSpace(err.Error())}},
	}
}
