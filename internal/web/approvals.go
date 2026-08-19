// Copyright (C) 2026 Techdelight BV

package web

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/techdelight/daedalus/internal/control"
)

// The pending-approvals surface (Sprint 59, item 5).
//
// This is a read-mostly view over the control plane's existing API plus the two
// human decisions — approve and reject. It deliberately does NOT spawn the
// control daemon: a dashboard that silently starts a daemon because somebody
// opened a browser tab would be a surprising side effect, so when the plane is
// not running the view says so and offers nothing to click.
//
// The Web UI is a CLIENT of control.sock exactly like the CLI. It gains no
// authority the CLI does not have, and — per the §6 note in approval.go — the
// "Guild Master cannot approve its own work" property rests on the socket
// boundary Sprint 60 introduces, not on this surface.

// approvalsResponse is the JSON shape the dashboard consumes.
type approvalsResponse struct {
	// Available is false when no control daemon is reachable; the UI then renders
	// an explanation rather than an empty queue, which would look like "nothing to
	// approve" and be a lie.
	Available bool           `json:"available"`
	Reason    string         `json:"reason,omitempty"`
	Tasks     []approvalTask `json:"tasks"`
}

type approvalTask struct {
	ID        string `json:"id"`
	Project   string `json:"project"`
	Objective string `json:"objective"`
	BaseSHA   string `json:"baseSha"`
	State     string `json:"state"`
	CreatedAt string `json:"createdAt"`
}

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

// planeStatusResponse is the concurrency picture the dashboard shows: with
// several Jobs able to run at once, "what is running" is no longer implied by the
// task list.
type planeStatusResponse struct {
	Available      bool           `json:"available"`
	Reason         string         `json:"reason,omitempty"`
	GlobalRunning  int            `json:"globalRunning"`
	GlobalLimit    int            `json:"globalLimit"`
	PerProjectLmt  int            `json:"perProjectLimit"`
	ProjectRunning map[string]int `json:"projectRunning"`
	Waiting        []string       `json:"waiting"`
}

// handlePlaneStatus serves the scheduler's view of what is running and queued.
func (ws *WebServer) handlePlaneStatus(w http.ResponseWriter, r *http.Request) {
	api := ws.controlClient()
	if api == nil {
		writeApprovalsJSON(w, http.StatusOK, planeStatusResponse{
			Available:      false,
			Reason:         "the daedalus-control daemon is not running",
			ProjectRunning: map[string]int{},
			Waiting:        []string{},
		})
		return
	}
	st, err := api.PlaneStatus()
	if err != nil {
		writeApprovalsJSON(w, http.StatusOK, planeStatusResponse{
			Available:      false,
			Reason:         "could not reach the control plane: " + err.Error(),
			ProjectRunning: map[string]int{},
			Waiting:        []string{},
		})
		return
	}
	resp := planeStatusResponse{
		Available: true, GlobalRunning: st.GlobalRunning,
		GlobalLimit: st.Limits.Global, PerProjectLmt: st.Limits.PerProject,
		ProjectRunning: st.ProjectRunning, Waiting: st.Waiting,
	}
	if resp.ProjectRunning == nil {
		resp.ProjectRunning = map[string]int{}
	}
	if resp.Waiting == nil {
		resp.Waiting = []string{}
	}
	writeApprovalsJSON(w, http.StatusOK, resp)
}

// boardResponse is the cross-project programme board (M17), flattened for the
// dashboard.
//
// It is served from the SAME control client as the approvals queue and derived
// from the same control-plane state — there is no board store, so this view can
// never disagree with the CLI's. Like the approvals panel it reports
// unavailability rather than rendering an empty board, because "nothing is
// running" and "I could not ask" are different answers.
type boardResponse struct {
	Available        bool          `json:"available"`
	Reason           string        `json:"reason,omitempty"`
	Columns          []boardColumn `json:"columns"`
	GlobalRunning    int           `json:"globalRunning"`
	GlobalLimit      int           `json:"globalLimit"`
	PendingApprovals int           `json:"pendingApprovals"`
	PendingProposals int           `json:"pendingProposals"`
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
	api := ws.controlClient()
	if api == nil {
		writeApprovalsJSON(w, http.StatusOK, boardResponse{
			Available: false,
			Reason:    "the daedalus-control daemon is not running",
			Columns:   []boardColumn{},
		})
		return
	}
	view, err := api.ProgrammeBoard()
	if err != nil {
		writeApprovalsJSON(w, http.StatusOK, boardResponse{
			Available: false,
			Reason:    "could not reach the control plane: " + err.Error(),
			Columns:   []boardColumn{},
		})
		return
	}
	resp := boardResponse{
		Available: true, Columns: []boardColumn{},
		GlobalRunning:    view.Plane.GlobalRunning,
		GlobalLimit:      view.Plane.Limits.Global,
		PendingApprovals: view.PendingApprovals,
		PendingProposals: view.PendingProposals,
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
	writeApprovalsJSON(w, http.StatusOK, resp)
}

// handleApprovals serves the queue of tasks awaiting a human decision.
func (ws *WebServer) handleApprovals(w http.ResponseWriter, r *http.Request) {
	api := ws.controlClient()
	if api == nil {
		writeApprovalsJSON(w, http.StatusOK, approvalsResponse{
			Available: false,
			Reason:    "the daedalus-control daemon is not running; start it with `daedalus task list`",
			Tasks:     []approvalTask{},
		})
		return
	}
	tasks, err := api.PendingApprovals()
	if err != nil {
		writeApprovalsJSON(w, http.StatusOK, approvalsResponse{
			Available: false,
			Reason:    "could not reach the control plane: " + err.Error(),
			Tasks:     []approvalTask{},
		})
		return
	}
	resp := approvalsResponse{Available: true, Tasks: []approvalTask{}}
	for _, t := range tasks {
		resp.Tasks = append(resp.Tasks, approvalTask{
			ID: t.ID, Project: t.Project, Objective: t.Objective,
			BaseSHA: t.BaseSHA, State: string(t.State), CreatedAt: t.CreatedAt,
		})
	}
	writeApprovalsJSON(w, http.StatusOK, resp)
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
	api := ws.controlClient()
	if api == nil {
		http.Error(w, `{"error":"the daedalus-control daemon is not running"}`, http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body) // an absent body is a decision without a note

	id := r.PathValue("id")
	var (
		task control.Task
		err  error
	)
	if approve {
		task, err = api.ApproveTask(id, body.Note)
	} else {
		task, err = api.RejectApproval(id, body.Note)
	}
	if err != nil {
		// The control plane's own status semantics are preserved: a policy refusal
		// and a state conflict are not server errors.
		status := http.StatusInternalServerError
		if _, refused := control.Rejected(err); refused {
			status = http.StatusUnprocessableEntity
		}
		writeApprovalsJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeApprovalsJSON(w, http.StatusOK, approvalTask{
		ID: task.ID, Project: task.Project, Objective: task.Objective,
		BaseSHA: task.BaseSHA, State: string(task.State), CreatedAt: task.CreatedAt,
	})
}

func writeApprovalsJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
