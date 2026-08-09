// Copyright (C) 2026 Techdelight BV

package web

import (
	"encoding/json"
	"net/http"

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

// controlClient returns the control-plane client, or nil when the plane is not
// reachable. Never spawns the daemon.
func (ws *WebServer) controlClient() control.TaskAPI {
	if ws.control != nil {
		return ws.control
	}
	return nil
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
