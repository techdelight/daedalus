// Copyright (C) 2026 Techdelight BV

package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func (f *fakeControl) PlaneStatus() (control.PlaneStatus, error) {
	return control.PlaneStatus{
		Limits:         control.SchedulerLimits{Global: 4, PerProject: 2},
		GlobalRunning:  2,
		ProjectRunning: map[string]int{"app": 2},
		Waiting:        []string{"T-9"},
	}, f.err
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

// TestPlaneStatus_ReportsRunningAndQueued: with several Jobs able to run at once,
// the dashboard must show what is actually running — and a queued Task must be
// visibly distinct from a working one.
func TestPlaneStatus_ReportsRunningAndQueued(t *testing.T) {
	fake := &fakeControl{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/plane-status", (&WebServer{control: fake}).handlePlaneStatus)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/plane-status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got planeStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Available || got.GlobalRunning != 2 || got.GlobalLimit != 4 || got.PerProjectLmt != 2 {
		t.Errorf("got %+v, want the fake's counts and limits", got)
	}
	if got.ProjectRunning["app"] != 2 {
		t.Errorf("per-project running = %v, want app=2", got.ProjectRunning)
	}
	if len(got.Waiting) != 1 || got.Waiting[0] != "T-9" {
		t.Errorf("waiting = %v, want [T-9] — a queued task must be visible", got.Waiting)
	}
}

// TestPlaneStatus_NoControlPlane: unavailable must not render as "nothing
// running", which is a different and reassuring claim.
func TestPlaneStatus_NoControlPlane(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/plane-status", (&WebServer{}).handlePlaneStatus)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/plane-status", nil))

	var got planeStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Available {
		t.Error("available should be false with no control plane")
	}
	if got.Reason == "" {
		t.Error("the response should explain why")
	}
	if got.ProjectRunning == nil || got.Waiting == nil {
		t.Error("maps and slices must be non-nil so the UI can iterate them")
	}
}

// --- M17: the programme board ---------------------------------------------------

func (f *fakeControl) ProgrammeBoard() (control.BoardView, error) {
	if f.err != nil {
		return control.BoardView{}, f.err
	}
	return control.BoardView{
		Columns: []control.BoardColumn{
			{Key: "queued", Title: "Queued", Cards: nil},
			{Key: "blocked", Title: "Blocked", Cards: []control.BoardCard{
				{TaskID: "T-2", Project: "app", Objective: "wait for T-1",
					State: "blocked", BlockedOn: []string{"T-1"}, Steering: "S-1 (undeliverable)"},
			}},
		},
		Plane:            control.PlaneStatus{GlobalRunning: 2, Limits: control.SchedulerLimits{Global: 4}},
		PendingApprovals: 1,
		PendingProposals: 3,
	}, nil
}

func boardMux(ws *WebServer) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/board", ws.handleBoard)
	return mux
}

// TestBoard_NoControlPlane: same rule as the approvals panel. An empty board and
// an unreachable board are different answers, and only one of them is reassuring.
func TestBoard_NoControlPlane(t *testing.T) {
	ws := &WebServer{}
	rec := httptest.NewRecorder()
	boardMux(ws).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/board", nil))

	var resp struct {
		Available bool   `json:"available"`
		Reason    string `json:"reason"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Available {
		t.Error("available = true with no control daemon")
	}
	if !strings.Contains(resp.Reason, "not running") {
		t.Errorf("reason = %q, want it to explain that the daemon is absent", resp.Reason)
	}
}

// TestBoard_RendersColumnsAndBlockedReasons: the board must carry "blocked, and on
// what" through to the dashboard, and it must keep EMPTY columns — a column that
// disappears when it empties makes "nothing is queued" look like a missing feature.
func TestBoard_RendersColumnsAndBlockedReasons(t *testing.T) {
	ws := &WebServer{control: &fakeControl{}}
	rec := httptest.NewRecorder()
	boardMux(ws).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/board", nil))

	var resp boardResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Available {
		t.Fatal("available = false with a reachable control plane")
	}
	if len(resp.Columns) != 2 {
		t.Fatalf("%d columns, want 2 (including the empty one)", len(resp.Columns))
	}
	if resp.Columns[0].Cards == nil {
		t.Error("an empty column serialised its cards as null, not []")
	}
	card := resp.Columns[1].Cards[0]
	if len(card.BlockedOn) != 1 || card.BlockedOn[0] != "T-1" {
		t.Errorf("blockedOn = %v, want [T-1]", card.BlockedOn)
	}
	if card.Steering != "S-1 (undeliverable)" {
		t.Errorf("steering = %q, want the undeliverable outcome carried through", card.Steering)
	}
	if resp.PendingApprovals != 1 || resp.PendingProposals != 3 {
		t.Errorf("queues = %d approvals / %d proposals, want 1 / 3", resp.PendingApprovals, resp.PendingProposals)
	}
}

// TestControlClient_RedialsUntilThePlaneAppears is the fix for a web server
// started before the control plane.
//
// The client used to be established exactly once, at boot. Start `daedalus web`
// first and the field stayed nil for the process's whole life, so the Ledger
// reported the plane missing long after it was running and only restarting the
// web server could fix it — while the CLI, which dials afresh every invocation,
// never had the problem.
func TestControlClient_RedialsUntilThePlaneAppears(t *testing.T) {
	var dials int
	var found control.TaskAPI // nil until the "plane" starts

	ws := &WebServer{controlDial: func() control.TaskAPI {
		dials++
		return found
	}}

	// The plane is down: looking finds nothing, and the answer is still nil.
	if got := ws.controlClient(); got != nil {
		t.Fatalf("controlClient() = %v with no plane, want nil", got)
	}
	if dials != 1 {
		t.Fatalf("dials = %d, want 1", dials)
	}

	// Immediately after a miss, it does NOT dial again — a plane that is genuinely
	// down must not cost a dial on every request from every open tab.
	if got := ws.controlClient(); got != nil {
		t.Errorf("controlClient() = %v, want nil", got)
	}
	if dials != 1 {
		t.Errorf("dials = %d after an immediate second call, want 1 — the retry is throttled", dials)
	}

	// The operator starts the plane. Once the throttle expires, the next look
	// finds it, and the page heals with no restart.
	found = &fakeControl{}
	ws.controlRetry = time.Now().Add(-time.Second)
	got := ws.controlClient()
	if got == nil {
		t.Fatal("controlClient() = nil after the plane started; the web server is deaf to it")
	}
	if dials != 2 {
		t.Errorf("dials = %d, want 2", dials)
	}

	// And once found it is kept: a live client is not re-dialled, because
	// control.Client opens a connection per request and survives a daemon restart
	// on the same socket by itself.
	ws.controlRetry = time.Now().Add(-time.Second)
	if ws.controlClient() == nil {
		t.Error("a established client should be reused")
	}
	if dials != 2 {
		t.Errorf("dials = %d, want 2 — a live client must not be re-dialled", dials)
	}
}

// TestControlClient_NoDialerNeverDials keeps the hand-built WebServer literals
// the rest of these tests use working: without a dialer there is nothing to
// look for, and guessing would mean reaching into a nil config.
func TestControlClient_NoDialerNeverDials(t *testing.T) {
	if got := (&WebServer{}).controlClient(); got != nil {
		t.Errorf("controlClient() = %v on a bare WebServer, want nil", got)
	}
}
