// Copyright (C) 2026 Techdelight BV

package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/control"
)

// The control-plane HTTP surface — everything the Ledger drives, and everything
// `daedalus task` can do.
//
// It started (Sprint 59) as a read-mostly approvals panel with two writes. It is
// now the whole operation set, because a surface that can show you a refused
// task but not retry it sends you to a terminal to finish the thought — and the
// terminal and the page then disagree about where decisions get made.
//
// THE SHAPE. Every route here is one call on control.TaskAPI, under /api/control,
// and the paths mirror the daemon's own (daemon.go's wire table) one for one.
// That is deliberate: the Web UI is a CLIENT of control.sock exactly like the
// CLI, it gains no authority the CLI does not have, and there is no place in
// between for the two to drift.
//
// WHY NOT A REVERSE PROXY. Forwarding /api/control/* straight to the socket
// would be a third of the code and is the wrong shape twice over. It would relay
// routes nobody here has considered — every future daemon route exposed to the
// browser the day it is written, which is the fail-OPEN direction authority.go
// spends its header warning against. And the caller class would still be human
// (the socket decides that), so "the plane grew an operation" would silently
// become "the page can do it". An explicit handler per operation means a new
// daemon route reaches the browser only when somebody writes it down.
//
// WHAT THIS DOES NOT DO. It never spawns the daemon: a dashboard that starts a
// control plane because somebody opened a tab would be a surprising side effect,
// so when the plane is not running the views say so and offer nothing to click.
// And it holds no authority of its own — the "Guild Master cannot approve its own
// work" property rests on the socket boundary (caller.go), not on this file.
//
// A NOTE ON EXPOSURE. These routes are as consequential as the CLI: they cancel
// Jobs and land code. They inherit the Web UI's own posture — `--auth` and
// whatever address it is bound to — and nothing here narrows it. Two of these
// writes (approve, reject) already shipped, so the boundary is not new, but it is
// now wide, and a Daedalus web server reachable from a network is a control plane
// reachable from that network.

// controlRedialEvery bounds how often a web server with no control client tries
// to find one. Short enough that starting the plane heals the page within a
// poll or two; long enough that a plane which is genuinely down does not cost a
// 300ms dial on every request from every open tab.
const controlRedialEvery = 3 * time.Second

// controlClient returns the control-plane client, or nil when the plane is not
// reachable. Never spawns the daemon — it only looks for one.
//
// The dial is retried, and that is the whole point of this function existing.
// The client used to be established exactly once, when the web server booted:
// start `daedalus web` before `daedalus task list` and the field stayed nil for
// the process's entire life, so the Ledger reported the plane missing long after
// it was running and only a restart of the web server could fix it. The CLI
// never had this problem because every invocation dials afresh.
//
// A non-nil client is NOT re-dialled, and does not need to be: control.Client
// opens a connection per request, so a daemon that restarts on the same socket
// is picked up again without anything here noticing. The only broken direction
// was nil forever, which is the one this repairs.
func (ws *WebServer) controlClient() control.TaskAPI {
	ws.controlMu.Lock()
	defer ws.controlMu.Unlock()

	if ws.control != nil {
		return ws.control
	}
	// No dialer means a WebServer someone built by hand (tests do). Looking for a
	// plane it was never told how to reach would be guesswork.
	if ws.controlDial == nil {
		return nil
	}
	if time.Now().Before(ws.controlRetry) {
		return nil
	}
	ws.controlRetry = time.Now().Add(controlRedialEvery)
	ws.control = ws.controlDial()
	return ws.control
}

// --- the two request shapes -------------------------------------------------
//
// A COLLECTION the page polls answers "unreachable" in the body, at 200, because
// the page has to render something either way and "nothing is running" and "I
// could not ask" must not look alike. A SINGLE-ENTITY read or a WRITE answers
// 503, because it is only ever reached from a collection that already loaded —
// a nil client there is genuinely exceptional and the page should say so loudly.

// unavailable is the header every collection response carries.
type unavailable struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

const planeDownReason = "the daedalus-control daemon is not running; start it with `daedalus control start`"

// collection serves a polled list, reporting unreachability as data.
func (ws *WebServer) collection(w http.ResponseWriter, empty func(unavailable) any, load func(control.TaskAPI) (any, error)) {
	api := ws.controlClient()
	if api == nil {
		writeControlJSON(w, http.StatusOK, empty(unavailable{Reason: planeDownReason}))
		return
	}
	v, err := load(api)
	if err != nil {
		writeControlJSON(w, http.StatusOK, empty(unavailable{
			Reason: "could not reach the control plane: " + err.Error(),
		}))
		return
	}
	writeControlJSON(w, http.StatusOK, v)
}

// act runs one control-plane operation and relays its answer verbatim: the
// plane's status code, and — for a policy refusal — the machine-readable reason
// alongside the human sentence, so the page can render a refusal AS a refusal
// rather than as a failure.
func (ws *WebServer) act(w http.ResponseWriter, fn func(control.TaskAPI) (any, error)) {
	api := ws.controlClient()
	if api == nil {
		writeControlJSON(w, http.StatusServiceUnavailable,
			map[string]string{"error": planeDownReason})
		return
	}
	v, err := fn(api)
	if err != nil {
		writeControlError(w, err)
		return
	}
	writeControlJSON(w, http.StatusOK, v)
}

// bind decodes a JSON request body into out, treating an ABSENT body as an
// empty one. Most of these requests are all-optional flags, and "POST with no
// body" is the natural way a page asks for the default.
func bind(r *http.Request, out any) error {
	if r.Body == nil {
		return nil
	}
	err := json.NewDecoder(r.Body).Decode(out)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func writeControlJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeControlError mirrors the daemon's own envelope, using the daemon's own
// mapping. A caller on the far side of the browser can therefore branch on
// exactly what a caller on the far side of the socket branches on.
func writeControlError(w http.ResponseWriter, err error) {
	body := map[string]string{"error": err.Error()}
	var rejected *control.RejectionError
	if errors.As(err, &rejected) {
		body["reason"] = string(rejected.Reason)
		body["message"] = rejected.Message
	}
	writeControlJSON(w, control.StatusFor(err), body)
}

// badRequest answers a body the page got wrong, before the plane is troubled.
func badRequest(w http.ResponseWriter, msg string) {
	writeControlJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
}

// --- collections ------------------------------------------------------------

// approvalsResponse is the JSON shape the Ledger consumes.
type approvalsResponse struct {
	unavailable
	Tasks []approvalTask `json:"tasks"`
}

// approvalTask is one task awaiting a human decision.
//
// M21: it carries the PROGRAMME and the RATIONALE, and that is the whole point
// of the milestone rather than a field or two more. `AgentReviewer` is handed the
// diff, the objective, the rationale and the programme (`review.go`); this
// struct used to hand the human an objective and a SHA. The party that reports
// was being shown more of the intent than the party with the authority to act,
// which inverts what the rationale was recorded for.
type approvalTask struct {
	ID        string `json:"id"`
	Project   string `json:"project"`
	Objective string `json:"objective"`
	BaseSHA   string `json:"baseSha"`
	State     string `json:"state"`
	CreatedAt string `json:"createdAt"`
	// Programme is resolved to a NAME here rather than passed through as an id.
	// The id is what the Task stores and what a client should key on; a person
	// deciding whether to seal something has no use for "PR-3".
	ProgrammeID  string `json:"programmeId,omitempty"`
	Programme    string `json:"programme,omitempty"`
	ProgrammeFor string `json:"programmeFor,omitempty"`
	Rationale    string `json:"rationale,omitempty"`
	RationaleBy  string `json:"rationaleBy,omitempty"`
}

// handleApprovals serves the queue of tasks awaiting a human decision.
func (ws *WebServer) handleApprovals(w http.ResponseWriter, r *http.Request) {
	ws.collection(w,
		func(u unavailable) any { return approvalsResponse{unavailable: u, Tasks: []approvalTask{}} },
		func(api control.TaskAPI) (any, error) {
			tasks, err := api.PendingApprovals()
			if err != nil {
				return nil, err
			}
			// One list call, not one lookup per task: the queue is short and the
			// programmes are fewer still, and a failure to read them must not empty
			// the approval queue — a gate that vanishes because a name could not be
			// resolved is a worse bug than a gate with no name on it.
			names := map[string]control.Programme{}
			if progs, perr := api.ListProgrammes(); perr == nil {
				for _, p := range progs {
					names[p.ID] = p
				}
			}
			resp := approvalsResponse{unavailable: unavailable{Available: true}, Tasks: []approvalTask{}}
			for _, t := range tasks {
				row := approvalTask{
					ID: t.ID, Project: t.Project, Objective: t.Objective,
					BaseSHA: t.BaseSHA, State: string(t.State), CreatedAt: t.CreatedAt,
					ProgrammeID: t.ProgrammeID, Rationale: t.Rationale,
					RationaleBy: string(t.RationaleBy),
				}
				if p, ok := names[t.ProgrammeID]; ok {
					row.Programme, row.ProgrammeFor = p.Name, p.Description
				}
				resp.Tasks = append(resp.Tasks, row)
			}
			return resp, nil
		})
}

// planeStatusResponse is the concurrency picture: with several Jobs able to run
// at once, "what is running" is no longer implied by the task list.
type planeStatusResponse struct {
	unavailable
	GlobalRunning  int            `json:"globalRunning"`
	GlobalLimit    int            `json:"globalLimit"`
	PerProjectLmt  int            `json:"perProjectLimit"`
	ProjectRunning map[string]int `json:"projectRunning"`
	Waiting        []string       `json:"waiting"`
	// Per programme (M22). Reporting only — the scheduler admits on the global and
	// per-project limits and knows nothing about programmes.
	ProgrammeRunning map[string]int `json:"programmeRunning,omitempty"`
	ProgrammeWaiting map[string]int `json:"programmeWaiting,omitempty"`
}

// handlePlaneStatus serves the scheduler's view of what is running and queued.
func (ws *WebServer) handlePlaneStatus(w http.ResponseWriter, r *http.Request) {
	empty := func(u unavailable) any {
		return planeStatusResponse{unavailable: u, ProjectRunning: map[string]int{}, Waiting: []string{}}
	}
	ws.collection(w, empty, func(api control.TaskAPI) (any, error) {
		st, err := api.PlaneStatus()
		if err != nil {
			return nil, err
		}
		resp := planeStatusResponse{
			unavailable:   unavailable{Available: true},
			GlobalRunning: st.GlobalRunning, GlobalLimit: st.Limits.Global,
			PerProjectLmt:  st.Limits.PerProject,
			ProjectRunning: st.ProjectRunning, Waiting: st.Waiting,
			ProgrammeRunning: st.ProgrammeRunning, ProgrammeWaiting: st.ProgrammeWaiting,
		}
		if resp.ProjectRunning == nil {
			resp.ProjectRunning = map[string]int{}
		}
		if resp.Waiting == nil {
			resp.Waiting = []string{}
		}
		return resp, nil
	})
}

// boardResponse is the cross-project programme board (M17), flattened for the
// Ledger.
//
// It is served from the SAME control client as the approvals queue and derived
// from the same control-plane state — there is no board store, so this view can
// never disagree with the CLI's.
type boardResponse struct {
	unavailable
	Columns          []boardColumn `json:"columns"`
	GlobalRunning    int           `json:"globalRunning"`
	GlobalLimit      int           `json:"globalLimit"`
	PendingApprovals int           `json:"pendingApprovals"`
	PendingProposals int           `json:"pendingProposals"`
	// Running and waiting Jobs per programme (M22), carried on the board because
	// the Ledger already polls it — a second poll for two numbers would be a second
	// thing that can be stale in a different way from the first.
	ProgrammeRunning map[string]int `json:"programmeRunning,omitempty"`
	ProgrammeWaiting map[string]int `json:"programmeWaiting,omitempty"`
}

type boardColumn struct {
	Key   string      `json:"key"`
	Title string      `json:"title"`
	Cards []boardCard `json:"cards"`
}

type boardCard struct {
	TaskID            string   `json:"taskId"`
	Project           string   `json:"project"`
	Objective         string   `json:"objective"`
	State             string   `json:"state"`
	BlockedOn         []string `json:"blockedOn,omitempty"`
	Unsatisfiable     []string `json:"unsatisfiable,omitempty"`
	QueuedForCapacity bool     `json:"queuedForCapacity,omitempty"`
	Steering          string   `json:"steering,omitempty"`
}

// handleBoard serves the programme board.
func (ws *WebServer) handleBoard(w http.ResponseWriter, r *http.Request) {
	ws.collection(w,
		func(u unavailable) any { return boardResponse{unavailable: u, Columns: []boardColumn{}} },
		func(api control.TaskAPI) (any, error) {
			view, err := api.ProgrammeBoard()
			if err != nil {
				return nil, err
			}
			resp := boardResponse{
				unavailable: unavailable{Available: true}, Columns: []boardColumn{},
				GlobalRunning:    view.Plane.GlobalRunning,
				GlobalLimit:      view.Plane.Limits.Global,
				PendingApprovals: view.PendingApprovals,
				PendingProposals: view.PendingProposals,
				ProgrammeRunning: view.Plane.ProgrammeRunning,
				ProgrammeWaiting: view.Plane.ProgrammeWaiting,
			}
			for _, col := range view.Columns {
				out := boardColumn{Key: col.Key, Title: col.Title, Cards: []boardCard{}}
				for _, c := range col.Cards {
					out.Cards = append(out.Cards, boardCard{
						TaskID: c.TaskID, Project: c.Project, Objective: c.Objective,
						State: c.State, BlockedOn: c.BlockedOn, Unsatisfiable: c.Unsatisfiable,
						QueuedForCapacity: c.QueuedForCapacity, Steering: c.Steering,
					})
				}
				resp.Columns = append(resp.Columns, out)
			}
			return resp, nil
		})
}

// tasksResponse is every Task the plane holds, terminal ones included.
//
// The board deliberately shows only what is in flight, which is right for a
// board and wrong for an archive: a Task that landed last week is exactly the
// one an operator wants to read the record of. This is `daedalus task list`.
type tasksResponse struct {
	unavailable
	Tasks []control.Task `json:"tasks"`
}

func (ws *WebServer) handleControlTasks(w http.ResponseWriter, r *http.Request) {
	ws.collection(w,
		func(u unavailable) any { return tasksResponse{unavailable: u, Tasks: []control.Task{}} },
		func(api control.TaskAPI) (any, error) {
			tasks, err := api.ListTasks()
			if err != nil {
				return nil, err
			}
			if tasks == nil {
				tasks = []control.Task{}
			}
			return tasksResponse{unavailable: unavailable{Available: true}, Tasks: tasks}, nil
		})
}

// proposalsResponse is the queue of agent-proposed operations awaiting a human.
type proposalsResponse struct {
	unavailable
	Proposals []control.Proposal `json:"proposals"`
}

func (ws *WebServer) handleProposals(w http.ResponseWriter, r *http.Request) {
	// An empty ?state= means "every proposal", matching `task proposals list --all`.
	state := control.ProposalState(r.URL.Query().Get("state"))
	ws.collection(w,
		func(u unavailable) any { return proposalsResponse{unavailable: u, Proposals: []control.Proposal{}} },
		func(api control.TaskAPI) (any, error) {
			list, err := api.ListProposals(state)
			if err != nil {
				return nil, err
			}
			if list == nil {
				list = []control.Proposal{}
			}
			return proposalsResponse{unavailable: unavailable{Available: true}, Proposals: list}, nil
		})
}

// targetsResponse is the integration targets and the projects sharing each.
type targetsResponse struct {
	unavailable
	Targets []control.TargetView `json:"targets"`
}

func (ws *WebServer) handleTargets(w http.ResponseWriter, r *http.Request) {
	ws.collection(w,
		func(u unavailable) any { return targetsResponse{unavailable: u, Targets: []control.TargetView{}} },
		func(api control.TaskAPI) (any, error) {
			targets, err := api.ProjectTargets()
			if err != nil {
				return nil, err
			}
			if targets == nil {
				targets = []control.TargetView{}
			}
			return targetsResponse{unavailable: unavailable{Available: true}, Targets: targets}, nil
		})
}

// --- single-entity reads ----------------------------------------------------

func (ws *WebServer) handleTaskStatus(w http.ResponseWriter, r *http.Request) {
	ws.act(w, func(api control.TaskAPI) (any, error) { return api.TaskStatus(r.PathValue("id")) })
}

func (ws *WebServer) handleTaskEvents(w http.ResponseWriter, r *http.Request) {
	ws.act(w, func(api control.TaskAPI) (any, error) {
		events, err := api.TaskEvents(r.PathValue("id"))
		if err != nil {
			return nil, err
		}
		if events == nil {
			events = []control.Event{}
		}
		return events, nil
	})
}

func (ws *WebServer) handleTaskDependencies(w http.ResponseWriter, r *http.Request) {
	ws.act(w, func(api control.TaskAPI) (any, error) { return api.TaskDependencies(r.PathValue("id")) })
}

func (ws *WebServer) handleJobSteering(w http.ResponseWriter, r *http.Request) {
	ws.act(w, func(api control.TaskAPI) (any, error) {
		events, err := api.JobSteering(r.PathValue("id"))
		if err != nil {
			return nil, err
		}
		if events == nil {
			events = []control.SteeringEvent{}
		}
		return events, nil
	})
}

// --- the lifecycle ----------------------------------------------------------

func (ws *WebServer) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req control.CreateTaskRequest
	if err := bind(r, &req); err != nil {
		badRequest(w, "malformed task: "+err.Error())
		return
	}
	api := ws.controlClient()
	if api == nil {
		writeControlJSON(w, http.StatusServiceUnavailable, map[string]string{"error": planeDownReason})
		return
	}
	task, err := api.CreateTask(req)
	if err != nil {
		writeControlError(w, err)
		return
	}
	// 201 like the daemon's own create, so the two agree on what happened.
	writeControlJSON(w, http.StatusCreated, task)
}

func (ws *WebServer) handleDispatchTask(w http.ResponseWriter, r *http.Request) {
	ws.act(w, func(api control.TaskAPI) (any, error) { return api.DispatchTask(r.PathValue("id")) })
}

func (ws *WebServer) handleVerifyTask(w http.ResponseWriter, r *http.Request) {
	var req control.VerifyRequest
	if err := bind(r, &req); err != nil {
		badRequest(w, "malformed verify request: "+err.Error())
		return
	}
	ws.act(w, func(api control.TaskAPI) (any, error) { return api.VerifyTask(r.PathValue("id"), req) })
}

func (ws *WebServer) handleRetryTask(w http.ResponseWriter, r *http.Request) {
	var req control.RetryRequest
	if err := bind(r, &req); err != nil {
		badRequest(w, "malformed retry request: "+err.Error())
		return
	}
	ws.act(w, func(api control.TaskAPI) (any, error) { return api.RetryTask(r.PathValue("id"), req) })
}

func (ws *WebServer) handleReverifyTask(w http.ResponseWriter, r *http.Request) {
	var req control.ReverifyRequest
	if err := bind(r, &req); err != nil {
		badRequest(w, "malformed reverify request: "+err.Error())
		return
	}
	ws.act(w, func(api control.TaskAPI) (any, error) { return api.ReverifyTask(r.PathValue("id"), req) })
}

func (ws *WebServer) handleAmendChecks(w http.ResponseWriter, r *http.Request) {
	var req control.AmendChecksRequest
	if err := bind(r, &req); err != nil {
		badRequest(w, "malformed checks: "+err.Error())
		return
	}
	// Nil and empty mean the same thing to the plane — clear them — so the page
	// does not need a separate "clear" verb.
	if req.Checks == nil {
		req.Checks = []string{}
	}
	ws.act(w, func(api control.TaskAPI) (any, error) { return api.AmendTaskChecks(r.PathValue("id"), req) })
}

func (ws *WebServer) handleReplanTask(w http.ResponseWriter, r *http.Request) {
	var req control.ReplanRequest
	if err := bind(r, &req); err != nil {
		badRequest(w, "malformed replan request: "+err.Error())
		return
	}
	ws.act(w, func(api control.TaskAPI) (any, error) { return api.ReplanTask(r.PathValue("id"), req) })
}

func (ws *WebServer) handleReviewTask(w http.ResponseWriter, r *http.Request) {
	ws.act(w, func(api control.TaskAPI) (any, error) { return api.ReviewTask(r.PathValue("id")) })
}

func (ws *WebServer) handleIntegrateTask(w http.ResponseWriter, r *http.Request) {
	var req control.IntegrateRequest
	if err := bind(r, &req); err != nil {
		badRequest(w, "malformed integrate request: "+err.Error())
		return
	}
	ws.act(w, func(api control.TaskAPI) (any, error) { return api.IntegrateTask(r.PathValue("id"), req) })
}

func (ws *WebServer) handleCancelTask(w http.ResponseWriter, r *http.Request) {
	ws.act(w, func(api control.TaskAPI) (any, error) { return api.CancelTask(r.PathValue("id")) })
}

// handleApproveTask records a human approval.
func (ws *WebServer) handleApproveTask(w http.ResponseWriter, r *http.Request) {
	ws.decideApproval(w, r, true)
}

// handleRejectTask records a human rejection at the approval gate.
func (ws *WebServer) handleRejectTask(w http.ResponseWriter, r *http.Request) {
	ws.decideApproval(w, r, false)
}

func (ws *WebServer) decideApproval(w http.ResponseWriter, r *http.Request, approve bool) {
	var body struct {
		Note string `json:"note"`
	}
	// An absent body is a decision without a note.
	if err := bind(r, &body); err != nil {
		badRequest(w, "malformed decision: "+err.Error())
		return
	}
	id := r.PathValue("id")
	ws.act(w, func(api control.TaskAPI) (any, error) {
		if approve {
			return api.ApproveTask(id, body.Note)
		}
		return api.RejectApproval(id, body.Note)
	})
}

// --- the graph, steering, proposals and targets -----------------------------

func (ws *WebServer) handleAddDependency(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DependsOn string `json:"dependsOn"`
	}
	if err := bind(r, &req); err != nil {
		badRequest(w, "malformed dependency: "+err.Error())
		return
	}
	ws.act(w, func(api control.TaskAPI) (any, error) {
		return api.AddDependency(r.PathValue("id"), req.DependsOn)
	})
}

func (ws *WebServer) handleSteerJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Instruction string `json:"instruction"`
	}
	if err := bind(r, &req); err != nil {
		badRequest(w, "malformed instruction: "+err.Error())
		return
	}
	ws.act(w, func(api control.TaskAPI) (any, error) {
		return api.SteerJob(r.PathValue("id"), req.Instruction)
	})
}

func (ws *WebServer) handleCancelSteering(w http.ResponseWriter, r *http.Request) {
	ws.act(w, func(api control.TaskAPI) (any, error) { return api.CancelSteering(r.PathValue("id")) })
}

func (ws *WebServer) handleResolveProposal(w http.ResponseWriter, r *http.Request, confirm bool) {
	var body struct {
		Note string `json:"note"`
	}
	if err := bind(r, &body); err != nil {
		badRequest(w, "malformed decision: "+err.Error())
		return
	}
	id := r.PathValue("id")
	ws.act(w, func(api control.TaskAPI) (any, error) { return api.ResolveProposal(id, confirm, body.Note) })
}

func (ws *WebServer) handleConfirmProposal(w http.ResponseWriter, r *http.Request) {
	ws.handleResolveProposal(w, r, true)
}

func (ws *WebServer) handleDenyProposal(w http.ResponseWriter, r *http.Request) {
	ws.handleResolveProposal(w, r, false)
}

func (ws *WebServer) handleSyncTarget(w http.ResponseWriter, r *http.Request) {
	ws.act(w, func(api control.TaskAPI) (any, error) { return api.SyncTarget(r.PathValue("project")) })
}

// --- programmes (M20) -----------------------------------------------------------
//
// These replace the file-backed CRUD that used to live in programmes.go. A
// programme is control-plane state now, so there is exactly one thing that
// answers "what programmes exist" — which was the point of the change rather
// than a consequence of it.
//
// The behaviour difference is worth knowing and is documented: programme CRUD now
// needs the control daemon. The CLI auto-spawns one; this page deliberately never
// does, so when the plane is down the surface says so instead of editing a copy
// nobody reads.

// programmesResponse is the list the page polls.
type programmesResponse struct {
	unavailable
	Programmes []control.Programme `json:"programmes"`
}

func (ws *WebServer) handleListProgrammes(w http.ResponseWriter, r *http.Request) {
	ws.collection(w,
		func(u unavailable) any {
			return programmesResponse{unavailable: u, Programmes: []control.Programme{}}
		},
		func(api control.TaskAPI) (any, error) {
			progs, err := api.ListProgrammes()
			if err != nil {
				return nil, err
			}
			if progs == nil {
				progs = []control.Programme{}
			}
			return programmesResponse{unavailable: unavailable{Available: true}, Programmes: progs}, nil
		})
}

func (ws *WebServer) handleGetProgramme(w http.ResponseWriter, r *http.Request) {
	ws.act(w, func(api control.TaskAPI) (any, error) { return api.GetProgramme(r.PathValue("id")) })
}

func (ws *WebServer) handleProgrammeStatus(w http.ResponseWriter, r *http.Request) {
	ws.act(w, func(api control.TaskAPI) (any, error) { return api.ProgrammeStatusFor(r.PathValue("id")) })
}

func (ws *WebServer) handleCreateProgramme(w http.ResponseWriter, r *http.Request) {
	var req control.ProgrammeRequest
	if err := bind(r, &req); err != nil {
		badRequest(w, "malformed programme: "+err.Error())
		return
	}
	api := ws.controlClient()
	if api == nil {
		writeControlJSON(w, http.StatusServiceUnavailable, map[string]string{"error": planeDownReason})
		return
	}
	p, err := api.CreateProgramme(req)
	if err != nil {
		writeControlError(w, err)
		return
	}
	writeControlJSON(w, http.StatusCreated, p)
}

func (ws *WebServer) handleUpdateProgramme(w http.ResponseWriter, r *http.Request) {
	var req control.ProgrammeRequest
	if err := bind(r, &req); err != nil {
		badRequest(w, "malformed programme: "+err.Error())
		return
	}
	ws.act(w, func(api control.TaskAPI) (any, error) {
		return api.UpdateProgramme(r.PathValue("id"), req)
	})
}

func (ws *WebServer) handleDeleteProgramme(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ws.act(w, func(api control.TaskAPI) (any, error) {
		if err := api.DeleteProgramme(id); err != nil {
			return nil, err
		}
		return map[string]string{"deleted": id}, nil
	})
}

// handleBuildVersion reports the build actually serving this page.
//
// It answers a question that was being answered wrongly: "am I looking at the
// code we just changed". The version alone could not — it is release
// granularity, unchanged across every commit of an unreleased cycle — so a
// correct "I am on the latest version" and a stale surface were indistinguishable
// until this existed.
func handleBuildVersion(w http.ResponseWriter, r *http.Request) {
	writeControlJSON(w, http.StatusOK, core.ReadBuildInfo())
}
