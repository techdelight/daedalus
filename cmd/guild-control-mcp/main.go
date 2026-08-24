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
	Objective string `json:"objective" jsonschema:"ONE SENTENCE saying what to accomplish and for whom. If it takes a paragraph it is a milestone, not a task — file several tasks instead"`
	// Deliverables are what will EXIST when the task is done (#95).
	//
	// The field exists because of what was arriving without it: milestone-sized
	// paragraphs in `objective`, filed by this very tool, with nothing on them a
	// person could check off. Reported as "a big blob of text about what to do for
	// a milestone with no clear deliverables".
	//
	// Optional in the shape and pressed for in the description, which is the same
	// choice made for programme and rationale — a task that names none should be
	// visibly thin rather than impossible to file.
	Deliverables []string `json:"deliverables,omitempty" jsonschema:"what will EXIST when this is done — one short line each, each naming something a person can point at (a file, a command that runs, a flag that works, a page that renders). The reviewer checks the change against this list item by item"`
	// Programme and Rationale are what this work is FOR (#88).
	//
	// Without them every task the Guild Master filed was an orphan: the seat whose
	// whole job is noticing what projects have in common could list programmes,
	// propose one, amend and dissolve one — and could not attach a single task to
	// any of them. The plane has carried both fields since Sprint 66 and a human's
	// CLI has passed them since; only the agent's tool did not, which is the same
	// shape as #82 and #85.
	//
	// Both optional, deliberately. A Task with no stated reason should be VISIBLY
	// unattributed rather than impossible to file — requiring them would only make
	// an agent invent a programme to satisfy a field.
	Programme string `json:"programme,omitempty" jsonschema:"optional: the programme this work serves — an id (PR-3) or its name. Must already exist; use list_programmes"`
	Rationale string `json:"rationale,omitempty" jsonschema:"optional: why this work is worth doing, in one or two sentences. Recorded as YOURS, and a reviewer later judges the change against it"`
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
	// Deliverables are what the Task said would EXIST when it was done. Read back
	// so an agent looking at existing work can see the shape it should be writing
	// — a tool that accepts a field and never shows it back teaches nothing.
	Deliverables []string `json:"deliverables,omitempty"`
	BaseSHA      string   `json:"baseSha"`
	CreatedAt    string   `json:"createdAt"`
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

// SteerRef names a RUNNING job and what to tell it.
type SteerRef struct {
	JobID       string `json:"jobId" jsonschema:"the running job id, e.g. J-7"`
	Instruction string `json:"instruction" jsonschema:"what the worker should do differently, in plain language"`
}

// BoardOutput is the cross-project programme board as an agent sees it.
// ProgrammeRef names a programme by id or by name.
type ProgrammeRef struct {
	Programme string `json:"programme" jsonschema:"the programme id (PR-3) or its name"`
}

// ProgrammeLine is the agent-facing view of a programme. No repository paths and
// no host anything — the same rule the board follows.
type ProgrammeLine struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Projects    []string `json:"projects,omitempty"`
}

// ProgrammesOutput is every programme the plane holds.
type ProgrammesOutput struct {
	Programmes []ProgrammeLine `json:"programmes"`
}

// ProposeProgrammeInput is a programme the Guild Master thinks should exist.
type ProposeProgrammeInput struct {
	Name        string   `json:"name" jsonschema:"a short name for the programme"`
	Description string   `json:"description" jsonschema:"what this programme is FOR, in one or two sentences"`
	Projects    []string `json:"projects,omitempty" jsonschema:"the projects it draws on"`
}

// AmendProgrammeInput changes a programme that already exists.
//
// Every field except the reference is optional, and an omitted field is LEFT
// ALONE rather than blanked. The agent is describing a change, not restating the
// whole programme, and a tool that silently dropped the projects because the
// caller only wanted to fix a description would be a data-loss bug wearing the
// shape of an update.
//
// The merge happens here, at proposal time, so the human confirming it sees the
// finished programme in the argument rather than a patch they have to apply in
// their head.
type AmendProgrammeInput struct {
	Programme   string   `json:"programme" jsonschema:"the programme id (PR-3) or its name"`
	Name        string   `json:"name,omitempty" jsonschema:"a new name, if the name is what is wrong"`
	Description string   `json:"description,omitempty" jsonschema:"what this programme is FOR, restated"`
	Projects    []string `json:"projects,omitempty" jsonschema:"the full project list, replacing the old one"`
	// Deps is the declared ORDER between projects. It was missing from the first
	// version of this tool, which left the agent able to propose which projects a
	// programme draws on and not the order they go in — an odd half, since
	// noticing that one project's work has to land before another's is exactly
	// the cross-project sight this agent exists for.
	//
	// It declares a PLAN and gates nothing; what makes a landing wait is a
	// Task→Task dependency, which is tiered separately and for a stronger reason
	// (an agent that could declare what must happen before a Task is graded could
	// declare it satisfied). So proposing an order here cannot become a gate.
	Deps []ProgrammeEdgeInput `json:"deps,omitempty" jsonschema:"the full declared project order, replacing the old one"`
}

// ProgrammeEdgeInput is one project→project ordering: downstream follows upstream.
type ProgrammeEdgeInput struct {
	Upstream   string `json:"upstream" jsonschema:"the project whose work comes first"`
	Downstream string `json:"downstream" jsonschema:"the project that follows it"`
}

// DissolveProgrammeInput names a programme that should stop existing.
type DissolveProgrammeInput struct {
	Programme string `json:"programme" jsonschema:"the programme id (PR-3) or its name"`
}

type BoardOutput struct {
	Columns          []BoardColumnLine `json:"columns"`
	GlobalRunning    int               `json:"globalRunning"`
	PendingApprovals int               `json:"pendingApprovals"`
	PendingProposals int               `json:"pendingProposals"`
}

// BoardColumnLine is one column with its cards.
type BoardColumnLine struct {
	Key   string          `json:"key"`
	Title string          `json:"title"`
	Cards []BoardCardLine `json:"cards"`
}

// BoardCardLine is one task on the board. Note what is absent: no repository
// path. The queue id is the OPAQUE Sprint-60 identity, which tells this caller
// which projects serialize against each other and nothing about host layout — and
// the stripping happens in the plane, not here, so this tool cannot leak what it
// is never sent.
type BoardCardLine struct {
	TaskID        string   `json:"taskId"`
	Project       string   `json:"project"`
	State         string   `json:"state"`
	Objective     string   `json:"objective"`
	QueueID       string   `json:"queueId,omitempty"`
	BlockedOn     []string `json:"blockedOn,omitempty"`
	Unsatisfiable []string `json:"unsatisfiable,omitempty"`
	Steering      string   `json:"steering,omitempty"`
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
		Description: "Create a task: state WHAT should be accomplished for a project, WHAT WILL EXIST when it is done, and — if it serves one — the programme it is for and why. One task is one thing to deliver: a milestone becomes several tasks, not one long objective. The control plane resolves the project and the programme, pins the base commit, freezes the acceptance policy and applies the project's budget ceiling — none of which this tool can influence. Allowed directly, because it cannot exceed policy.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in CreateTaskInput) (*mcp.CallToolResult, OutcomeOutput, error) {
		// The programme reference is passed through UNRESOLVED. The plane matches it
		// against control.db by id or name and stores its own canonical id, so the
		// Task can only ever point at a programme that exists — and a reference that
		// resolves to nothing REFUSES the create rather than filing an orphan that
		// reads as attached to whoever wrote the request.
		//
		// Deliberately not resolved here first: this tool would then be a second
		// place that decides what a programme reference means, and the two would
		// drift. findProgramme exists for the tools that must show a programme; this
		// one only has to name it.
		task, err := api.CreateTask(createTaskRequest(in))
		if err != nil {
			return nil, outcomeFor(err), nil
		}
		// The detail names the programme when there is one, because an agent that
		// cannot see the link landed cannot tell a resolved reference from a dropped
		// one — and "silently unattached" is the failure this whole field exists to
		// end. RationaleBy is NOT echoed: it comes from the socket, and telling the
		// agent it was recorded as the agent's teaches it nothing it could change.
		detail := fmt.Sprintf("created task %s for %s (state %s)", task.ID, task.Project, task.State)
		if task.ProgrammeID != "" {
			detail += ", serving programme " + task.ProgrammeID
		}
		// The SHAPE of what was just filed, told back to the agent that filed it.
		// Not a refusal: the plane does not get to decide that a long objective is
		// wrong. But an agent that receives "created T-14" and nothing else has no
		// way to learn that it keeps filing milestones, and this is the only moment
		// it is holding the context needed to split one.
		if advice := control.ObjectiveAdvice(task.Objective, task.Deliverables); advice != "" {
			detail += ". Worth reconsidering — " + advice
		}
		return nil, OutcomeOutput{Executed: true, Detail: detail}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "request_verification",
		Description: "Ask the control plane to verify a candidate artifact against the project's frozen acceptance policy, in a clean container. Allowed directly: this applies the PLANE's oracle, which the caller cannot influence.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in TaskRef) (*mcp.CallToolResult, OutcomeOutput, error) {
		res, err := api.VerifyTask(in.TaskID, control.VerifyRequest{})
		if err != nil {
			return nil, outcomeFor(err), nil
		}
		if res.Verified {
			return nil, OutcomeOutput{Executed: true, Detail: "verified: " + res.Detail}, nil
		}
		return nil, OutcomeOutput{Executed: true,
			Detail: fmt.Sprintf("rejected (%s): %s", res.Reason, res.Detail), Reason: string(res.Reason)}, nil
	})

	// THE BOARD IS A BOARD OF TASKS, and its name used to say otherwise (#86).
	// `programme_board` predates programmes being a thing the plane owns, and once
	// they were, an agent had two tools whose names promised the same subject and
	// which shared no data at all: this one groups Tasks by whose move it is, and
	// `list_programmes` lists the shared intents. The collision cost a whole
	// backlog entry's worth of confusion (#82 had to say in as many words that
	// `programme_board` is a red herring), so the tool is now called `task_board`.
	//
	// The old name is kept, registered to the same handler and marked deprecated,
	// because a Guild Master's CLAUDE.md may name it and a rename that breaks a
	// running agent's instructions is a rename that gets reverted.
	board := func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, BoardOutput, error) {
		view, err := api.ProgrammeBoard()
		if err != nil {
			return errResult(err), BoardOutput{}, nil
		}
		out := BoardOutput{
			GlobalRunning:    view.Plane.GlobalRunning,
			PendingApprovals: view.PendingApprovals,
			PendingProposals: view.PendingProposals,
			Columns:          make([]BoardColumnLine, 0, len(view.Columns)),
		}
		for _, col := range view.Columns {
			line := BoardColumnLine{Key: col.Key, Title: col.Title, Cards: make([]BoardCardLine, 0, len(col.Cards))}
			for _, c := range col.Cards {
				line.Cards = append(line.Cards, BoardCardLine{
					TaskID: c.TaskID, Project: c.Project, State: c.State, Objective: c.Objective,
					QueueID: c.QueueID, BlockedOn: c.BlockedOn, Unsatisfiable: c.Unsatisfiable,
					Steering: c.Steering,
				})
			}
			out.Columns = append(out.Columns, line)
		}
		return nil, out, nil
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "task_board",
		Description: "The cross-project board of TASKS: what is running, queued, blocked (and on what), in verification, awaiting approval, and what landed. Read-only. Repository paths are never included; tasks sharing a queueId will serialize against each other at integration. For the shared intents several projects serve, use list_programmes instead.",
	}, board)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "programme_board",
		Description: "DEPRECATED — the old name for task_board, kept so existing instructions keep working. It is the cross-project board of TASKS and has nothing to do with programmes. Use task_board.",
	}, board)

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
				_, err := api.IntegrateTask(in.TaskID, control.IntegrateRequest{})
				return "integration", err
			},
		},
	}
	// Steering takes two values rather than a task and a note, so it is registered
	// on its own rather than through the loop above. It is proposal-tier for the
	// sharpest version of the §6 reason: this agent reads project-controlled
	// documents, and an instruction injected into a human's RUNNING job is at least
	// as consequential as cancelling it — and rather more subtle, because the job
	// carries on and the change of direction shows up only in the log.
	// --- programmes (#82) -----------------------------------------------------
	//
	// The Guild Master's whole job is noticing what projects have in common, and
	// until these existed it could not see a programme at all — the operations
	// were tiered in `agentAuthority` and reachable from nowhere. Reading is free;
	// forming, amending and dissolving are proposals, because a programme is a
	// statement about what the work is FOR and a human agreeing is what turns a
	// noticing into one.
	//
	// The tool that used to be called `programme_board` is now `task_board` (#86),
	// so the two names above this line no longer promise the same subject.
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_programmes",
		Description: "The programmes the control plane holds: the shared intents that several projects serve. Read-only, allowed directly — noticing what projects have in common is what this agent is for.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, ProgrammesOutput, error) {
		progs, err := api.ListProgrammes()
		if err != nil {
			return errResult(err), ProgrammesOutput{}, nil
		}
		out := ProgrammesOutput{Programmes: make([]ProgrammeLine, 0, len(progs))}
		for _, p := range progs {
			out.Programmes = append(out.Programmes, ProgrammeLine{
				ID: p.ID, Name: p.Name, Description: p.Description, Projects: p.Projects,
			})
		}
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_programme",
		Description: "One programme, with what it is for and the projects it draws on. Read-only.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in ProgrammeRef) (*mcp.CallToolResult, ProgrammeLine, error) {
		p, err := findProgramme(api, in.Programme)
		if err != nil {
			return errResult(err), ProgrammeLine{}, nil
		}
		return nil, ProgrammeLine{
			ID: p.ID, Name: p.Name, Description: p.Description, Projects: p.Projects,
		}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "propose_programme",
		Description: "Propose that several projects' common interest should become a programme. NOT executed: it is recorded for a human to confirm, because forming a programme is a statement about what the work is for. Say what it is FOR in the description — that is the part a task's rationale will later be judged against.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in ProposeProgrammeInput) (*mcp.CallToolResult, OutcomeOutput, error) {
		_, err := api.CreateProgramme(control.ProgrammeRequest{
			Name: in.Name, Description: in.Description, Projects: in.Projects,
		})
		if err != nil {
			// The expected path: proposal_recorded, which outcomeFor renders as "not
			// executed — recorded as a proposal". An agent reaching this tool should
			// never see Executed=true; if it ever does, the authority table changed.
			return nil, outcomeFor(err), nil
		}
		return nil, OutcomeOutput{Executed: true,
			Detail: "programme created (this caller was not treated as an agent)"}, nil
	})

	// Amend and dissolve. Both were tiered when programmes landed and both had a
	// case in `executeProposal` from #82 — they were reachable from a human's CLI
	// and from nowhere else, so the Guild Master could propose a programme into
	// existence and then never say a word about it again. That is the wrong half
	// to build: noticing that a programme has drifted from what it was formed for
	// is the same act as noticing it should exist.
	mcp.AddTool(server, &mcp.Tool{
		Name:        "propose_programme_amendment",
		Description: "Propose a change to a programme that already exists — usually because what it is FOR has been overtaken by what the work turned out to be. NOT executed: it is recorded for a human to confirm. Fields you leave out are kept as they are.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in AmendProgrammeInput) (*mcp.CallToolResult, OutcomeOutput, error) {
		cur, err := findProgramme(api, in.Programme)
		if err != nil {
			return errResult(err), OutcomeOutput{}, nil
		}
		if _, err := api.UpdateProgramme(cur.ID, mergeProgramme(cur, in)); err != nil {
			return nil, outcomeFor(err), nil
		}
		return nil, OutcomeOutput{Executed: true,
			Detail: "programme amended (this caller was not treated as an agent)"}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "propose_programme_dissolution",
		Description: "Propose that a programme should stop existing, because the common interest that formed it is gone or was never real. NOT executed: it is recorded for a human to confirm. A programme that still has tasks pointing at it will be refused even after confirmation — those tasks record it as their reason, and dissolving it would erase that.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in DissolveProgrammeInput) (*mcp.CallToolResult, OutcomeOutput, error) {
		cur, err := findProgramme(api, in.Programme)
		if err != nil {
			return errResult(err), OutcomeOutput{}, nil
		}
		// The proposal carries the programme id and nothing else — `callerScope`
		// builds its argument as "dissolve <id>", with no room for a reason. So this
		// tool does not offer one: a `why` field that the record silently dropped
		// would be worse than the absent field, because the agent would believe it
		// had explained itself to whoever has to decide.
		if err := api.DeleteProgramme(cur.ID); err != nil {
			return nil, outcomeFor(err), nil
		}
		return nil, OutcomeOutput{Executed: true,
			Detail: "programme dissolved (this caller was not treated as an agent)"}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "request_steering",
		Description: "Ask for a typed instruction to be delivered to a RUNNING job. Consequential, so this is recorded as a proposal for a human to confirm. Steering changes what the worker is told; it never changes what counts as done — the result is still independently verified against the frozen acceptance policy. Delivery is not guaranteed: a runner with no steering boundary records the instruction as undeliverable.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in SteerRef) (*mcp.CallToolResult, OutcomeOutput, error) {
		steer, err := api.SteerJob(in.JobID, in.Instruction)
		if err != nil {
			return nil, outcomeFor(err), nil
		}
		// Reached only if the plane executed it directly. Even then the honest answer
		// depends on the DELIVERY state, not on the absence of an error: "recorded"
		// is not "the worker was told".
		return nil, OutcomeOutput{Executed: true,
			Detail: fmt.Sprintf("steering %s recorded for %s; delivery: %s", steer.ID, in.JobID, steer.State)}, nil
	})

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

// createTaskRequest turns the agent's input into a plane request.
//
// Pure and separate so the pass-through can be tested: the fields this drops are
// invisible at runtime, and "the programme silently did not reach the plane" is
// exactly the failure that made every Guild-Master task an orphan for two
// milestones.
//
// A budget is attached only when the agent narrowed something. An all-zero Budget
// is not "no budget" to the plane — it is a request for zero attempts — so
// sending one unconditionally would file tasks that can never run.
func createTaskRequest(in CreateTaskInput) control.CreateTaskRequest {
	req := control.CreateTaskRequest{
		Project: in.Project, Objective: in.Objective, Deliverables: in.Deliverables,
		Programme: in.Programme, Rationale: in.Rationale,
	}
	if in.MaxAttempts > 0 || in.WallClockSeconds > 0 {
		req.Budget = &control.Budget{
			MaxAttempts: in.MaxAttempts, WallClockSeconds: in.WallClockSeconds,
		}
	}
	return req
}

// mergeProgramme applies an amendment to the programme as it stands, and is the
// reason an omitted field means "leave it" rather than "clear it".
//
// Read-then-write, deliberately, and pure so the rule can be tested: the whole
// risk in this tool is a caller who wanted to fix a description and silently
// lost the project list. The window between the read and the human's
// confirmation is real, and it is the same window every proposal has — what a
// human confirms is what the agent proposed, and the plane refuses the result if
// it has since become invalid.
//
// An empty slice and an absent one are NOT the same: `[]` clears the list, nil
// keeps it. That distinction is the whole reason this takes the parsed input
// rather than a map.
func mergeProgramme(cur control.Programme, in AmendProgrammeInput) control.ProgrammeRequest {
	next := control.ProgrammeRequest{
		Name: cur.Name, Description: cur.Description, Projects: cur.Projects, Deps: cur.Deps,
	}
	if strings.TrimSpace(in.Name) != "" {
		next.Name = in.Name
	}
	if strings.TrimSpace(in.Description) != "" {
		next.Description = in.Description
	}
	if in.Projects != nil {
		next.Projects = in.Projects
	}
	if in.Deps != nil {
		next.Deps = make([]core.DependencyEdge, 0, len(in.Deps))
		for _, d := range in.Deps {
			next.Deps = append(next.Deps, core.DependencyEdge{
				Upstream: d.Upstream, Downstream: d.Downstream,
			})
		}
	}
	return next
}

// findProgramme resolves what the agent typed — an id or a name — to a
// programme.
//
// Both are accepted for the same reason the CLI accepts both: a person types the
// name and a client stores the id, and a surface that took only one of them
// would disagree with every other surface about what a programme reference is.
// It lives here, once, because three tools need it and three copies of a
// fallback is how they drift.
func findProgramme(api control.TaskAPI, ref string) (control.Programme, error) {
	if p, err := api.GetProgramme(ref); err == nil {
		return p, nil
	}
	progs, err := api.ListProgrammes()
	if err != nil {
		return control.Programme{}, err
	}
	for _, p := range progs {
		if p.Name == ref {
			return p, nil
		}
	}
	return control.Programme{}, fmt.Errorf("no programme %q — list_programmes shows what exists", ref)
}

// summarise flattens a Task for the agent view.
func summarise(t control.Task) TaskSummary {
	return TaskSummary{
		ID: t.ID, Project: t.Project, State: string(t.State),
		Objective: t.Objective, Deliverables: t.Deliverables,
		BaseSHA: t.BaseSHA, CreatedAt: t.CreatedAt,
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
