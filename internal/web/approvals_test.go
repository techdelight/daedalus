// Copyright (C) 2026 Techdelight BV

package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/techdelight/daedalus/internal/control"
)

// fakeControl is a TaskAPI double exposing only what the approvals surface uses.
type fakeControl struct {
	control.TaskAPI // the rest is unused and must never be called
	pending         []control.Task
	approved        []string
	rejected        []string
	notes           []string
	err             error
}

func (f *fakeControl) PendingApprovals() ([]control.Task, error) {
	return f.pending, f.err
}

func (f *fakeControl) ApproveTask(id, note string) (control.Task, error) {
	if f.err != nil {
		return control.Task{}, f.err
	}
	f.approved = append(f.approved, id)
	f.notes = append(f.notes, note)
	return control.Task{ID: id, State: control.StateApproved}, nil
}

func (f *fakeControl) RejectApproval(id, note string) (control.Task, error) {
	if f.err != nil {
		return control.Task{}, f.err
	}
	f.rejected = append(f.rejected, id)
	f.notes = append(f.notes, note)
	return control.Task{ID: id, State: control.StateRejected}, nil
}

func approvalsMux(ws *WebServer) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/approvals", ws.handleApprovals)
	mux.HandleFunc("POST /api/approvals/{id}/approve", ws.handleApproveTask)
	mux.HandleFunc("POST /api/approvals/{id}/reject", ws.handleRejectTask)
	return mux
}

// TestApprovals_NoControlPlane: with no daemon running the view must say so
// rather than render an empty queue, which would read as "nothing to approve".
func TestApprovals_NoControlPlane(t *testing.T) {
	ws := &WebServer{}
	rec := httptest.NewRecorder()
	approvalsMux(ws).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/approvals", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got approvalsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Available {
		t.Error("available should be false with no control plane")
	}
	if got.Reason == "" {
		t.Error("the response should explain why the queue is unavailable")
	}
	if got.Tasks == nil {
		t.Error("tasks should be an empty array, not null (the UI iterates it)")
	}
}

func TestApprovals_ListsPending(t *testing.T) {
	fake := &fakeControl{pending: []control.Task{
		{ID: "T-1", Project: "app", Objective: "add dark mode", State: control.StateApprovalRequired},
		{ID: "T-2", Project: "api", Objective: "fix the leak", State: control.StateApprovalRequired},
	}}
	ws := &WebServer{control: fake}
	rec := httptest.NewRecorder()
	approvalsMux(ws).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/approvals", nil))

	var got approvalsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Available || len(got.Tasks) != 2 {
		t.Fatalf("got %+v, want 2 available tasks", got)
	}
	if got.Tasks[0].ID != "T-1" || got.Tasks[1].Project != "api" {
		t.Errorf("tasks not carried through: %+v", got.Tasks)
	}
}

func TestApprovals_ApproveAndReject(t *testing.T) {
	fake := &fakeControl{}
	mux := approvalsMux(&WebServer{control: fake})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/approvals/T-1/approve",
		strings.NewReader(`{"note":"ship it"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("approve status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(fake.approved) != 1 || fake.approved[0] != "T-1" {
		t.Errorf("approved = %v, want [T-1]", fake.approved)
	}
	if len(fake.notes) != 1 || fake.notes[0] != "ship it" {
		t.Errorf("notes = %v, want [ship it]", fake.notes)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/approvals/T-2/reject", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("reject status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(fake.rejected) != 1 || fake.rejected[0] != "T-2" {
		t.Errorf("rejected = %v, want [T-2]", fake.rejected)
	}
}

// TestApprovals_PolicyRefusalIsNot500 preserves the control plane's own status
// semantics through the web layer: a refusal is 422, not a server error.
func TestApprovals_PolicyRefusalIsNot500(t *testing.T) {
	fake := &fakeControl{err: &control.RejectionError{
		Reason: control.ReasonApprovalRequired, Message: "nope"}}
	mux := approvalsMux(&WebServer{control: fake})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/approvals/T-1/approve", nil))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}

// TestApprovals_DecisionsNeedTheControlPlane: without a daemon the decisions must
// fail loudly rather than silently doing nothing.
func TestApprovals_DecisionsNeedTheControlPlane(t *testing.T) {
	mux := approvalsMux(&WebServer{})
	for _, path := range []string{"/api/approvals/T-1/approve", "/api/approvals/T-1/reject"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("POST %s = %d, want 503", path, rec.Code)
		}
	}
}
