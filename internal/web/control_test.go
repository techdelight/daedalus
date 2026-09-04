// Copyright (C) 2026 Techdelight BV

package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/techdelight/daedalus/internal/control"
)

// fakeControl is a TaskAPI double exposing only what the approvals surface uses.
type fakeControl struct {
	control.TaskAPI // the rest is unused and must never be called
	pending         []control.Task
	programmes      []control.Programme
	progErr         error
	approved        []string
	rejected        []string
	notes           []string
	err             error
}

// The approvals queue resolves each task's programme to a NAME (M21), so this
// double answers the list call too. progErr is separate from err on purpose: a
// programme list that cannot be read must not empty the approval queue, which is
// what TestApprovals_ProgrammeUnreadable asserts.
func (f *fakeControl) ListProgrammes() ([]control.Programme, error) {
	return f.programmes, f.progErr
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
	mux.HandleFunc("GET /api/control/approvals", ws.handleApprovals)
	mux.HandleFunc("POST /api/control/tasks/{id}/approve", ws.handleApproveTask)
	mux.HandleFunc("POST /api/control/tasks/{id}/reject", ws.handleRejectTask)
	return mux
}

// TestApprovals_NoControlPlane: with no daemon running the view must say so
// rather than render an empty queue, which would read as "nothing to approve".
func TestApprovals_NoControlPlane(t *testing.T) {
	ws := &WebServer{}
	rec := httptest.NewRecorder()
	approvalsMux(ws).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/control/approvals", nil))

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
	approvalsMux(ws).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/control/approvals", nil))

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

// M21: the person holding the seal is shown what the work is FOR.
//
// The reviewer agent has been handed the objective, the rationale and the
// programme since Sprint 67; this queue handed the human an objective and a base
// SHA. The asymmetry is the bug — a rationale is recorded so that a human can
// weigh it, and it is recorded WITH ITS AUTHOR so an agent-drafted reason does
// not read as the operator's own.
func TestApprovals_CarryProgrammeAndRationale(t *testing.T) {
	fake := &fakeControl{
		pending: []control.Task{{
			ID: "T-1", Project: "app", Objective: "add dark mode",
			State: control.StateApprovalRequired, ProgrammeID: "PR-1",
			Rationale: "three projects grew their own theming", RationaleBy: control.CallerAgent,
		}},
		programmes: []control.Programme{
			{ID: "PR-1", Name: "fluency", Description: "one way to theme, everywhere"},
		},
	}
	rec := httptest.NewRecorder()
	approvalsMux(&WebServer{control: fake}).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/api/control/approvals", nil))

	var got approvalsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Tasks) != 1 {
		t.Fatalf("got %d tasks, want 1", len(got.Tasks))
	}
	row := got.Tasks[0]
	if row.ProgrammeID != "PR-1" || row.Programme != "fluency" {
		t.Errorf("programme = %q/%q, want PR-1/fluency", row.ProgrammeID, row.Programme)
	}
	// The description travels too: "what this is for" is the sentence a rationale
	// is judged against, and an operator who has to go and look it up elsewhere is
	// an operator deciding without it.
	if row.ProgrammeFor != "one way to theme, everywhere" {
		t.Errorf("programmeFor = %q, want the programme's description", row.ProgrammeFor)
	}
	if row.Rationale == "" || row.RationaleBy != string(control.CallerAgent) {
		t.Errorf("rationale = %q by %q, want the reason and its author",
			row.Rationale, row.RationaleBy)
	}
}

// A programme list that cannot be read must cost the queue a NAME, never a ROW.
// The gate is the thing that must not vanish: an approval that disappears because
// a lookup failed reads as "nothing needs you", which is the one answer this
// surface must never give wrongly.
func TestApprovals_ProgrammeUnreadable(t *testing.T) {
	fake := &fakeControl{
		pending: []control.Task{{
			ID: "T-1", Project: "app", Objective: "add dark mode",
			State: control.StateApprovalRequired, ProgrammeID: "PR-1",
			Rationale: "still recorded", RationaleBy: control.CallerHuman,
		}},
		progErr: errors.New("programmes table is unreadable"),
	}
	rec := httptest.NewRecorder()
	approvalsMux(&WebServer{control: fake}).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/api/control/approvals", nil))

	var got approvalsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Available || len(got.Tasks) != 1 {
		t.Fatalf("got %+v, want the queue to survive an unreadable programme list", got)
	}
	if got.Tasks[0].Programme != "" || got.Tasks[0].ProgrammeID != "PR-1" {
		t.Errorf("want the id with no name, got %q/%q",
			got.Tasks[0].ProgrammeID, got.Tasks[0].Programme)
	}
	if got.Tasks[0].Rationale != "still recorded" {
		t.Error("the rationale comes from the Task and must not depend on the programme lookup")
	}
}

func TestApprovals_ApproveAndReject(t *testing.T) {
	fake := &fakeControl{}
	mux := approvalsMux(&WebServer{control: fake})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/control/tasks/T-1/approve",
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
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/control/tasks/T-2/reject", nil))
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
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/control/tasks/T-1/approve", nil))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}

// TestApprovals_DecisionsNeedTheControlPlane: without a daemon the decisions must
// fail loudly rather than silently doing nothing.
func TestApprovals_DecisionsNeedTheControlPlane(t *testing.T) {
	mux := approvalsMux(&WebServer{})
	for _, path := range []string{"/api/control/tasks/T-1/approve", "/api/control/tasks/T-1/reject"} {
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
	mux.HandleFunc("GET /api/control/status", (&WebServer{control: fake}).handlePlaneStatus)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/control/status", nil))
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
	mux.HandleFunc("GET /api/control/status", (&WebServer{}).handlePlaneStatus)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/control/status", nil))

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
	mux.HandleFunc("GET /api/control/board", ws.handleBoard)
	return mux
}

// TestBoard_NoControlPlane: same rule as the approvals panel. An empty board and
// an unreachable board are different answers, and only one of them is reassuring.
func TestBoard_NoControlPlane(t *testing.T) {
	ws := &WebServer{}
	rec := httptest.NewRecorder()
	boardMux(ws).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/control/board", nil))

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
	boardMux(ws).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/control/board", nil))

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

// --- the whole operation set ----------------------------------------------------

// webRoutes maps every control.TaskAPI operation to the route the Web UI serves
// it on. The Ledger is meant to do what `daedalus task` can do, and this table is
// where that claim is written down.
//
// It is keyed by METHOD NAME and checked against the interface by reflection, so
// adding an operation to the control plane and forgetting the web surface fails
// here rather than being discovered by an operator who reached for it. An
// operation that should deliberately NOT be reachable from a browser belongs in
// this table with an empty route and a reason — there are none today.
var webRoutes = map[string]struct{ method, path string }{
	"PlaneStatus":        {http.MethodGet, "/api/control/status"},
	"ProgrammeBoard":     {http.MethodGet, "/api/control/board"},
	"PendingApprovals":   {http.MethodGet, "/api/control/approvals"},
	"ListTasks":          {http.MethodGet, "/api/control/tasks"},
	"ListProposals":      {http.MethodGet, "/api/control/proposals"},
	"ProjectTargets":     {http.MethodGet, "/api/control/targets"},
	"RefineTask":         {http.MethodPost, "/api/control/tasks/{id}/refine"},
	"TargetLags":         {http.MethodGet, "/api/control/targets/lag"},
	"Adoptions":          {http.MethodGet, "/api/control/adoptions"},
	"AdoptLanded":        {http.MethodPost, "/api/control/adoptions/app"},
	"ListProgrammes":     {http.MethodGet, "/api/control/programmes"},
	"GetProgramme":       {http.MethodGet, "/api/control/programmes/PR-1"},
	"ProgrammeStatusFor": {http.MethodGet, "/api/control/programmes/PR-1/status"},
	"CreateProgramme":    {http.MethodPost, "/api/control/programmes"},
	"UpdateProgramme":    {http.MethodPost, "/api/control/programmes/PR-1"},
	"DeleteProgramme":    {http.MethodDelete, "/api/control/programmes/PR-1"},
	"TaskStatus":         {http.MethodGet, "/api/control/tasks/T-1"},
	"TaskEvents":         {http.MethodGet, "/api/control/tasks/T-1/events"},
	"TaskDependencies":   {http.MethodGet, "/api/control/tasks/T-1/dependencies"},
	"JobSteering":        {http.MethodGet, "/api/control/jobs/J-1/steering"},
	"CreateTask":         {http.MethodPost, "/api/control/tasks"},
	"DispatchTask":       {http.MethodPost, "/api/control/tasks/T-1/dispatch"},
	"VerifyTask":         {http.MethodPost, "/api/control/tasks/T-1/verify"},
	"RetryTask":          {http.MethodPost, "/api/control/tasks/T-1/retry"},
	"ReverifyTask":       {http.MethodPost, "/api/control/tasks/T-1/reverify"},
	"AmendTaskChecks":    {http.MethodPost, "/api/control/tasks/T-1/checks"},
	"AmendTaskBudget":    {http.MethodPost, "/api/control/tasks/T-1/budget"},
	"ReplanTask":         {http.MethodPost, "/api/control/tasks/T-1/replan"},
	"ReviewTask":         {http.MethodPost, "/api/control/tasks/T-1/review"},
	"ApproveTask":        {http.MethodPost, "/api/control/tasks/T-1/approve"},
	"RejectApproval":     {http.MethodPost, "/api/control/tasks/T-1/reject"},
	"IntegrateTask":      {http.MethodPost, "/api/control/tasks/T-1/integrate"},
	"CancelTask":         {http.MethodDelete, "/api/control/tasks/T-1"},
	"AddDependency":      {http.MethodPost, "/api/control/tasks/T-1/dependencies"},
	"SteerJob":           {http.MethodPost, "/api/control/jobs/J-1/steer"},
	"CancelSteering":     {http.MethodDelete, "/api/control/steering/S-1"},
	"ResolveProposal":    {http.MethodPost, "/api/control/proposals/P-1/confirm"},
	"SyncTarget":         {http.MethodPost, "/api/control/targets/app/sync"},
}

// TestControlSurface_CoversEveryOperation DERIVES the requirement from the
// control plane's own interface: every operation the CLI can drive must be
// reachable from the Ledger, and every route claimed here must actually be
// registered.
//
// The second half matters as much as the first. A route in the table that the
// server does not serve would make this test a statement about a map literal.
func TestControlSurface_CoversEveryOperation(t *testing.T) {
	api := reflect.TypeOf((*control.TaskAPI)(nil)).Elem()
	mux := http.NewServeMux()
	(&WebServer{}).RegisterRoutes(mux)

	for i := 0; i < api.NumMethod(); i++ {
		name := api.Method(i).Name
		route, listed := webRoutes[name]
		if !listed {
			t.Errorf("control.TaskAPI.%s has no entry in webRoutes: the plane grew an "+
				"operation the Ledger cannot reach. Add the route, or list it with an "+
				"empty path and a reason.", name)
			continue
		}
		if route.path == "" {
			continue // deliberately unreachable from a browser
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(route.method, route.path, nil))
		// No control plane is wired here, so a served route answers 503 (or 200 for
		// a polled collection). Only an UNROUTED path reaches the mux's own 404.
		if rec.Code == http.StatusNotFound {
			t.Errorf("%s %s (for %s) is not registered: %s",
				route.method, route.path, name, strings.TrimSpace(rec.Body.String()))
		}
	}
	// And nothing in the table is for an operation that no longer exists.
	for name := range webRoutes {
		if _, ok := api.MethodByName(name); !ok {
			t.Errorf("webRoutes lists %q, which is not a control.TaskAPI operation", name)
		}
	}
}

// --- the generic act() path ------------------------------------------------------

// recorder is a TaskAPI double for the lifecycle routes. Each method records
// what it was asked and returns errs[method] if set, so one fake covers both the
// "was the body carried" and the "was the refusal relayed" questions.
type recorder struct {
	control.TaskAPI
	gotID   string
	verify  control.VerifyRequest
	retry   control.RetryRequest
	requery control.ReverifyRequest
	replan  control.ReplanRequest
	checks  control.AmendChecksRequest
	land    control.IntegrateRequest
	create  control.CreateTaskRequest
	dep     string
	steer   string
	confirm bool
	note    string
	err     error
	budget  control.AmendBudgetRequest
}

func (r *recorder) DispatchTask(id string) (control.DispatchResult, error) {
	r.gotID = id
	return control.DispatchResult{Job: control.Job{ID: "J-1", TaskID: id}}, r.err
}
func (r *recorder) VerifyTask(id string, req control.VerifyRequest) (control.VerifyResult, error) {
	r.gotID, r.verify = id, req
	return control.VerifyResult{Verified: true}, r.err
}
func (r *recorder) RetryTask(id string, req control.RetryRequest) (control.RetryResult, error) {
	r.gotID, r.retry = id, req
	return control.RetryResult{Attempt: 2}, r.err
}
func (r *recorder) ReverifyTask(id string, req control.ReverifyRequest) (control.ReverifyResult, error) {
	r.gotID, r.requery = id, req
	return control.ReverifyResult{Amended: req.Amended}, r.err
}
func (r *recorder) ReplanTask(id string, req control.ReplanRequest) (control.Task, error) {
	r.gotID, r.replan = id, req
	return control.Task{ID: id, Objective: req.Objective}, r.err
}
func (r *recorder) AmendTaskBudget(id string, req control.AmendBudgetRequest) (control.Task, error) {
	r.gotID, r.budget = id, req
	return control.Task{ID: id, Budget: control.Budget{MaxAttempts: req.MaxAttempts}}, r.err
}

func (r *recorder) AmendTaskChecks(id string, req control.AmendChecksRequest) (control.Task, error) {
	r.gotID, r.checks = id, req
	return control.Task{ID: id, Checks: req.Checks}, r.err
}
func (r *recorder) IntegrateTask(id string, req control.IntegrateRequest) (control.IntegrationResult, error) {
	r.gotID, r.land = id, req
	return control.IntegrationResult{MergedSHA: "abc123"}, r.err
}
func (r *recorder) CancelTask(id string) (control.Task, error) {
	r.gotID = id
	return control.Task{ID: id, State: control.StateCancelled}, r.err
}
func (r *recorder) ReviewTask(id string) (control.ReviewResult, error) {
	r.gotID = id
	return control.ReviewResult{Passed: true}, r.err
}
func (r *recorder) CreateTask(req control.CreateTaskRequest) (control.Task, error) {
	r.create = req
	return control.Task{ID: "T-7", Project: req.Project, Objective: req.Objective}, r.err
}
func (r *recorder) AddDependency(id, on string) (control.DependencyEdge, error) {
	r.gotID, r.dep = id, on
	return control.DependencyEdge{TaskID: id, DependsOn: on}, r.err
}
func (r *recorder) SteerJob(id, instruction string) (control.SteeringEvent, error) {
	r.gotID, r.steer = id, instruction
	return control.SteeringEvent{ID: "S-1", JobID: id, Instruction: instruction}, r.err
}
func (r *recorder) CancelSteering(id string) (control.SteeringEvent, error) {
	r.gotID = id
	return control.SteeringEvent{ID: id}, r.err
}
func (r *recorder) ResolveProposal(id string, confirm bool, note string) (control.Proposal, error) {
	r.gotID, r.confirm, r.note = id, confirm, note
	return control.Proposal{ID: id}, r.err
}
func (r *recorder) SyncTarget(project string) (control.Target, error) {
	r.gotID = project
	return control.Target{}, r.err
}

func controlMux(api control.TaskAPI) *http.ServeMux {
	mux := http.NewServeMux()
	(&WebServer{control: api}).RegisterRoutes(mux)
	return mux
}

func post(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestControlWrites_CarryTheirArguments: the flags are the whole difference
// between these operations and their defaults — `retry --rebase` re-freezes the
// acceptance oracle, `verify --ignore-result` waives a failing verdict — so a
// body that silently does not arrive would be a UI that lies about what it did.
func TestControlWrites_CarryTheirArguments(t *testing.T) {
	fake := &recorder{}
	mux := controlMux(fake)

	if rec := post(t, mux, http.MethodPost, "/api/control/tasks/T-1/verify",
		`{"ignoreResult":true}`); rec.Code != http.StatusOK {
		t.Fatalf("verify = %d: %s", rec.Code, rec.Body)
	} else if !fake.verify.IgnoreResult {
		t.Error("verify: ignoreResult did not reach the plane — a waiver silently became an ordinary verify")
	}

	post(t, mux, http.MethodPost, "/api/control/tasks/T-1/retry", `{"rebase":true}`)
	if !fake.retry.Rebase {
		t.Error("retry: rebase did not reach the plane")
	}

	post(t, mux, http.MethodPost, "/api/control/tasks/T-1/reverify", `{"amended":true}`)
	if !fake.requery.Amended {
		t.Error("reverify: amended did not reach the plane")
	}

	post(t, mux, http.MethodPost, "/api/control/tasks/T-1/replan", `{"objective":"do it differently"}`)
	if fake.replan.Objective != "do it differently" {
		t.Errorf("replan objective = %q", fake.replan.Objective)
	}

	post(t, mux, http.MethodPost, "/api/control/tasks/T-1/checks", `{"checks":["go test ./..."]}`)
	if len(fake.checks.Checks) != 1 || fake.checks.Checks[0] != "go test ./..." {
		t.Errorf("checks = %v", fake.checks.Checks)
	}

	post(t, mux, http.MethodPost, "/api/control/tasks/T-1/integrate", `{"intoBranch":true}`)
	if !fake.land.IntoBranch {
		t.Error("integrate: intoBranch did not reach the plane")
	}

	post(t, mux, http.MethodPost, "/api/control/tasks/T-1/dependencies", `{"dependsOn":"T-2"}`)
	if fake.dep != "T-2" {
		t.Errorf("dependsOn = %q", fake.dep)
	}

	post(t, mux, http.MethodPost, "/api/control/jobs/J-1/steer", `{"instruction":"use the other library"}`)
	if fake.steer != "use the other library" || fake.gotID != "J-1" {
		t.Errorf("steer = %q on %q", fake.steer, fake.gotID)
	}

	post(t, mux, http.MethodPost, "/api/control/proposals/P-1/deny", `{"note":"no"}`)
	if fake.confirm || fake.note != "no" {
		t.Errorf("deny: confirm=%v note=%q, want false / \"no\"", fake.confirm, fake.note)
	}
	post(t, mux, http.MethodPost, "/api/control/proposals/P-1/confirm", "")
	if !fake.confirm {
		t.Error("confirm did not reach the plane as a confirmation")
	}
}

// TestControlWrites_AbsentBodyIsTheDefault: every flag on these routes is
// optional, so "POST with nothing" must mean "the default", not "malformed".
func TestControlWrites_AbsentBodyIsTheDefault(t *testing.T) {
	fake := &recorder{}
	mux := controlMux(fake)
	for _, path := range []string{
		"/api/control/tasks/T-1/verify",
		"/api/control/tasks/T-1/retry",
		"/api/control/tasks/T-1/reverify",
		"/api/control/tasks/T-1/integrate",
	} {
		if rec := post(t, mux, http.MethodPost, path, ""); rec.Code != http.StatusOK {
			t.Errorf("POST %s with no body = %d, want 200: %s", path, rec.Code, rec.Body)
		}
	}
	if fake.verify.IgnoreResult || fake.retry.Rebase || fake.requery.Amended || fake.land.IntoBranch {
		t.Error("an absent body set a flag: the safe default is not the default")
	}
}

// TestControlCreate_Answers201 keeps the Web UI and the daemon agreeing about
// what a create is.
func TestControlCreate_Answers201(t *testing.T) {
	fake := &recorder{}
	rec := post(t, controlMux(fake), http.MethodPost, "/api/control/tasks",
		`{"project":"app","objective":"add dark mode","checks":["go vet ./..."]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d, want 201: %s", rec.Code, rec.Body)
	}
	if fake.create.Project != "app" || fake.create.Objective != "add dark mode" {
		t.Errorf("create request = %+v", fake.create)
	}
	if len(fake.create.Checks) != 1 {
		t.Errorf("per-task checks did not reach the plane: %v", fake.create.Checks)
	}
}

// TestControlWrites_RelayThePlanesOwnAnswer: a refusal must arrive as a refusal.
//
// 422 carries the machine-readable reason, so the page can say "the plane
// declined, and here is why" instead of "something went wrong". And a 409 state
// conflict — which reaches the client as a plain envelope with no reason code —
// must not be flattened into a 500: "you cannot retry a task that was never
// rejected" is the plane working, not the plane broken.
func TestControlWrites_RelayThePlanesOwnAnswer(t *testing.T) {
	refused := &recorder{err: &control.RejectionError{
		Reason: control.ReasonOverBudget, Message: "no attempts left"}}
	rec := post(t, controlMux(refused), http.MethodPost, "/api/control/tasks/T-1/retry", "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("refusal = %d, want 422", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["reason"] != string(control.ReasonOverBudget) {
		t.Errorf("reason = %q, want %q — the page cannot tell a refusal from a failure without it",
			body["reason"], control.ReasonOverBudget)
	}
	if body["message"] != "no attempts left" {
		t.Errorf("message = %q", body["message"])
	}

	conflict := &recorder{err: &control.RemoteError{
		Status: http.StatusConflict, Msg: "control: task T-1 is not rejected"}}
	rec = post(t, controlMux(conflict), http.MethodPost, "/api/control/tasks/T-1/retry", "")
	if rec.Code != http.StatusConflict {
		t.Errorf("state conflict = %d, want 409 — a plane saying no was reported as a plane breaking", rec.Code)
	}
}

// TestControlCollections_UnreachableIsNotEmpty extends the rule the approvals
// panel established to every list the Ledger polls.
func TestControlCollections_UnreachableIsNotEmpty(t *testing.T) {
	mux := http.NewServeMux()
	(&WebServer{}).RegisterRoutes(mux)
	for _, path := range []string{
		"/api/control/tasks", "/api/control/proposals", "/api/control/targets",
	} {
		rec := post(t, mux, http.MethodGet, path, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, rec.Code)
		}
		var got struct {
			Available bool   `json:"available"`
			Reason    string `json:"reason"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if got.Available || got.Reason == "" {
			t.Errorf("GET %s reported an empty list rather than an unreachable plane: %s", path, rec.Body)
		}
	}
}

// TestControlWrites_NeedTheControlPlane: with no daemon the commands must fail
// loudly rather than appear to have worked.
func TestControlWrites_NeedTheControlPlane(t *testing.T) {
	mux := http.NewServeMux()
	(&WebServer{}).RegisterRoutes(mux)
	for _, r := range []struct{ method, path string }{
		{http.MethodPost, "/api/control/tasks/T-1/dispatch"},
		{http.MethodPost, "/api/control/tasks"},
		{http.MethodDelete, "/api/control/tasks/T-1"},
		{http.MethodPost, "/api/control/jobs/J-1/steer"},
	} {
		rec := post(t, mux, r.method, r.path, "")
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d, want 503", r.method, r.path, rec.Code)
		}
	}
}

// TestLedger_ExplainsThatLandingMovesNoBranch pins the Ledger's half of one
// answer given on three surfaces.
//
// The plane lands on refs/daedalus/target, which nobody checks out, so a branch
// never moves on its own. The CLI has said so since --into-branch existed; the
// Ledger said "Landed <sha> onto the target." and marked the entry `landed`,
// which is true and reads as "it is in my branch now". The page is JavaScript and
// cannot call into Go, so this is the only thing standing between three surfaces
// and three different answers: the transient message must render the sentence the
// plane sends, and the entry note must be the plane's constant, character for
// character.
func TestLedger_ExplainsThatLandingMovesNoBranch(t *testing.T) {
	src, err := staticFiles.ReadFile("static/control.js")
	if err != nil {
		t.Fatalf("reading the Ledger: %v", err)
	}
	js := string(src)

	// The field the page reads must be the field the plane marshals.
	body, err := json.Marshal(control.IntegrationResult{
		BranchAdvice: control.BranchAdviceFor(false, ""),
	})
	if err != nil {
		t.Fatalf("marshalling a landing: %v", err)
	}
	if !strings.Contains(string(body), `"branchAdvice"`) {
		t.Fatalf("an integration is sent to the Ledger without the explanation: %s", body)
	}
	// The CODE, not the identifier (RV-8). `strings.Contains(js, "branchAdvice")`
	// was satisfied by a COMMENT — the string appears three times in control.js and
	// twice of those are prose, so deleting the actual render left this green. That
	// is the third substring-match test in this repository to pass for the wrong
	// reason; assert on a fragment that only the working code contains.
	if !strings.Contains(js, "r.branchAdvice ||") {
		t.Error("the Ledger reports a landing without rendering the plane's branchAdvice, so an " +
			"operator is told `landed` and left to discover their branch never moved")
	}

	// And for an entry that is ALREADY landed, where the page has only the state
	// to go on: the same sentence the TUI board prints, from the same constant.
	if !strings.Contains(js, control.LandedNote) {
		t.Errorf("the Ledger has drifted from control.LandedNote — update LANDED_NOTE in "+
			"internal/web/static/control.js to say, exactly:\n\t%s", control.LandedNote)
	}
	// The LANDED COLUMN carries it too (RV-8's last note, #79's remaining half).
	// The entry explains a landing, but the gap #79 names is somebody concluding
	// FROM A GLANCE that the work is in their checkout — and a glance never
	// reaches the entry. Asserted on the section footnote's own call, not on the
	// constant's name, for the reason two assertions in this very test had to be
	// rewritten: an identifier is satisfied by prose.
	if !strings.Contains(js, "BOARD_LANDED ? LANDED_NOTE") {
		t.Error("the Landed column renders no footnote, so a reader scanning the list still " +
			"sees `landed` and nothing about where the work actually is")
	}

	// …and that the constant is USED. Pinning the text proves only that it is
	// declared: remove the line that appends it and the assertion above still
	// passes, which is the same hole as the one fixed just above.
	if !strings.Contains(js, "LANDED_NOTE)") {
		t.Error("LANDED_NOTE is declared but never rendered — a landed entry says the word and " +
			"explains nothing, which is the gap this change exists to close")
	}

	// The preview fixture carries the sentence too, and nothing pinned it (RV-8):
	// the next reword of LandedNote would leave the design preview showing text no
	// surface produces. It is not operator-facing, so this is a cheap guard rather
	// than a load-bearing one — but "three surfaces saying nearly the same thing"
	// is this change's own argument, and the fixture was the unpinned fourth.
	preview, err := os.ReadFile(filepath.Join("static", "control-preview.html"))
	if err != nil {
		t.Skipf("preview not readable: %v", err)
	}
	if !strings.Contains(string(preview), control.LandedNote) {
		t.Errorf("control-preview.html has drifted from control.LandedNote; it should say, exactly:\n\t%s",
			control.LandedNote)
	}
}

// A COMMAND MUST LOCK ONE ENTRY, NOT THE PAGE.
//
// Reported plainly: "the ledger locks all buttons for all tasks if I start a
// review, until I refresh the page". It did. `busy` was one boolean for the
// whole surface, every plate on every entry read it, and renderBoard returned
// early while it was raised — so a review, which is minutes of a container
// reading a diff, made the entire Ledger read-only and stopped the board
// repainting. Nothing about the plane asks for that: it serialises per task.
//
// This is JavaScript and Go cannot run it, so what follows asserts on the
// source. Two of the three are DERIVED rather than spelled out — the disable
// rule is checked against every `.disabled =` in the file, so a new button
// wired to a page-wide flag fails this even though nobody updated the test.
func TestLedger_ACommandLocksOnlyTheEntryItActsOn(t *testing.T) {
	src, err := staticFiles.ReadFile("static/control.js")
	if err != nil {
		t.Fatalf("reading the Ledger: %v", err)
	}
	js := string(src)

	// EVERY plate's disabled state must be decided per entry. Derived: find each
	// assignment to `.disabled` and require the entry to be part of the answer.
	// An enumerated check ("the file contains isBusy(current.id)") would stay
	// green beside a second button disabled from something page-wide.
	disable := regexp.MustCompile(`\.disabled\s*=\s*([^;\n]+)`)
	found := disable.FindAllStringSubmatch(js, -1)
	if len(found) == 0 {
		t.Error("no button on the Ledger is ever disabled while its command is in flight, so a " +
			"second click sends a second command against work that is already running")
	}
	for _, m := range found {
		if !strings.Contains(m[1], "isBusy(") {
			t.Errorf("a Ledger button is disabled from `%s`, which is not scoped to an entry — "+
				"one slow command will grey out plates on tasks it has nothing to do with", strings.TrimSpace(m[1]))
		}
	}

	// The board must keep repainting while a command is out. The only thing that
	// may freeze the entry is `awaiting` — an operator mid-confirmation, which is
	// an interaction to protect. A request in flight is not.
	if strings.Contains(js, "awaiting || busy") {
		t.Error("renderBoard still stops on an in-flight command, so the board freezes for the " +
			"whole of a review — the one operation slow enough to be worth watching")
	}

	// And the claim must be per entry rather than a flag. `inflight[id]` is the
	// working code; the old `busy = true` is the shape this replaced.
	if !strings.Contains(js, "inflight[id] = {") {
		t.Error("the Ledger no longer records which ENTRY a command was sent for, so it cannot " +
			"tell a busy task from a busy page")
	}
	if regexp.MustCompile(`\bbusy\s*=\s*(true|false)\b`).MatchString(js) {
		t.Error("a page-wide busy flag is back in control.js; a command must claim the entry it " +
			"acts on and nothing else")
	}
}

// THE LEDGER IS A PLACE YOU CAN GO BACK TO.
//
// It was a div the Guild Hall toggled: no address, so no bookmark, no second
// tab, no link to send, and a reload put you back on the project list. The page
// picks its view from location.pathname now, and this asserts the server agrees
// — every path the app claims must answer with the app rather than a 404, which
// is the difference between a link that works and a link that looks broken.
//
// Derived from the page's OWN constant: LEDGER_PATH is read out of control.js,
// so changing it there and not here fails, which is the drift this would
// otherwise be blind to.
func TestLedger_HasItsOwnURL(t *testing.T) {
	src, err := staticFiles.ReadFile("static/control.js")
	if err != nil {
		t.Fatalf("reading the Ledger: %v", err)
	}
	m := regexp.MustCompile(`const LEDGER_PATH = '([^']+)'`).FindStringSubmatch(string(src))
	if m == nil {
		t.Fatal("control.js declares no LEDGER_PATH, so the Ledger has no address to be served at")
	}
	ledger := m[1]

	mux := http.NewServeMux()
	RegisterAppRoutes(mux, "9.9.9")
	server := httptest.NewServer(mux)
	defer server.Close()

	// Every declared app path, turned into a URL somebody could type. A new
	// AppPaths entry is covered by this without touching the test.
	var served []string
	for _, p := range AppPaths {
		served = append(served, urlForPattern(p))
	}
	// …plus the two the Ledger specifically promises: the board, and one entry.
	// Without these the loop above would pass over an empty list.
	want := []string{ledger, ledger + "/T-18"}
	for _, w := range want {
		if !slices.Contains(served, w) {
			t.Errorf("nothing serves %s, so that link answers 404 and the Ledger is unreachable "+
				"by address (AppPaths serves %v)", w, served)
		}
	}

	for _, path := range append(served, want...) {
		resp, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s answered %d — a reloaded or shared link lands on an error page",
				path, resp.StatusCode)
			continue
		}
		// The APP, not merely a 200. `id="control-view"` is the Ledger's own root
		// element in index.html, so this fails if the route starts serving
		// something else.
		if !strings.Contains(string(body), `id="control-view"`) {
			t.Errorf("GET %s served something that is not the app", path)
		}
	}
}

// urlForPattern turns a ServeMux pattern into a URL a person could type:
// "GET /{$}" is "/", and any {wildcard} takes a sample entry id.
func urlForPattern(p string) string {
	path := strings.TrimPrefix(p, "GET ")
	path = strings.ReplaceAll(path, "{$}", "")
	for {
		i := strings.Index(path, "{")
		if i < 0 {
			break
		}
		j := strings.Index(path[i:], "}")
		if j < 0 {
			break
		}
		path = path[:i] + "T-18" + path[i+j+1:]
	}
	return path
}

// A REVIEW IS SOMETHING TO ACT ON, NOT SOMETHING TO READ THROUGH.
//
// Reported plainly: "the reviews are big walls of text, so hard to read that I
// have to feed it back to daedalus to make sense of it". The data was never the
// problem — the plane has sent severity, file, line, what and why since M20 —
// the rendering was: the reasoning paragraph first, then every field of every
// finding at full length, in the order the reviewer happened to write them.
//
// The first assertion is DERIVED from the Go struct: every field a Finding
// marshals must be rendered by the page. A field added to the shape and shown
// nowhere is invisible to the only person the review is for.
func TestLedger_RendersAReviewToBeActedOn(t *testing.T) {
	src, err := staticFiles.ReadFile("static/control.js")
	if err != nil {
		t.Fatalf("reading the Ledger: %v", err)
	}
	js := string(src)

	ft := reflect.TypeOf(control.Finding{})
	for i := 0; i < ft.NumField(); i++ {
		tag := strings.Split(ft.Field(i).Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		if !strings.Contains(js, "f."+tag) {
			t.Errorf("the plane sends Finding.%s as %q and the Ledger renders it nowhere, so the "+
				"operator never sees it", ft.Field(i).Name, tag)
		}
	}

	// The verdict, as counts, before any of the prose. This is the line the
	// decision is actually made on.
	if !strings.Contains(js, "' blocking'") {
		t.Error("a review no longer opens with how many blocking findings it has, so the size of " +
			"the problem can only be got by reading all of it")
	}

	// Progressive disclosure, and specifically for the two things that were in
	// the way: the reasoning, and each finding's why/fix. <details> rather than a
	// click handler, so it survives a poll repaint and opens with a keyboard.
	if !strings.Contains(js, "ledger-reasoning") {
		t.Error("the reviewer's reasoning is not in its own collapsible section — it was the " +
			"longest thing on the screen and the first thing on it")
	}
	if strings.Count(js, "createElement('details')") < 2 {
		t.Error("findings and reasoning are not behind disclosures, so a five-finding review is " +
			"still a page of prose")
	}
}

// A TASK SAYS WHAT IT WILL PRODUCE, ON THE SURFACE WHERE IT IS CREATED AND READ.
//
// "The tasks provided to the ledger are a big blob of text about what to do for
// a milestone with no clear deliverables." The plane carries the list now; this
// asserts the Ledger both COLLECTS it (the new-entry form) and SHOWS it (the
// entry window). A field the page can send and never renders is a field nobody
// ever discovers is empty.
func TestLedger_CollectsAndShowsDeliverables(t *testing.T) {
	js, err := staticFiles.ReadFile("static/control.js")
	if err != nil {
		t.Fatalf("reading the Ledger: %v", err)
	}
	html, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		t.Fatalf("reading the page: %v", err)
	}

	// The field the page sends must be the field the plane unmarshals.
	body, err := json.Marshal(control.CreateTaskRequest{
		Project: "app", Objective: "x", Deliverables: []string{"y"},
	})
	if err != nil {
		t.Fatalf("marshalling a create: %v", err)
	}
	if !strings.Contains(string(body), `"deliverables"`) {
		t.Fatalf("a create is sent to the plane without deliverables: %s", body)
	}

	if !strings.Contains(string(html), `id="new-deliverables"`) {
		t.Error("the new-entry form has no deliverables field, so the surface most people create " +
			"tasks from cannot say what a task will produce")
	}
	// The CODE, not the identifier: `strings.Contains(js, "deliverables")` is
	// satisfied by a comment, which is how three earlier tests in this package
	// passed for the wrong reason.
	if !strings.Contains(string(js), "req.deliverables = deliverables") {
		t.Error("the form collects deliverables and never sends them")
	}
	if !strings.Contains(string(js), "task.deliverables") {
		t.Error("the entry window never renders a task's deliverables, so nobody reading an entry " +
			"— before dispatch or at the approval gate — can see what it promised")
	}
}

// TestLedgerCommandsNameRealOperations is the derived web-surface test for #95
// item 1: every command plate in the Ledger names a plane operation the plane
// actually has, and no plate carries its own list of states any more.
//
// THE DEFECT IT REPLACES. Each COMMANDS entry used to hold `states: [...]` — a
// third copy of which operations a state admits, in JavaScript, where nothing in
// Go could see it. It could not let anything illegal through, and that is not the
// failure that happened: what happened is a command that WAS legal and was never
// offered, which an operator cannot tell apart from a rule. `task refine` shipped
// with #91 and the Ledger could not reach it at all; the plane widened `review`
// to `rejected` and the page went on hiding it.
//
// A test of the same shape found the missing `budget` route the day it was
// written, which is why this one is here rather than a comment asking people to
// remember.
func TestLedgerCommandsNameRealOperations(t *testing.T) {
	src, err := staticFiles.ReadFile("static/control.js")
	if err != nil {
		t.Fatalf("reading the Ledger: %v", err)
	}
	js := string(src)

	known := map[string]bool{}
	for _, op := range control.OperationCatalogue() {
		known[op.Key] = true
	}

	// `key: 'retry-rebase', op: 'retry',` — every plate's declared operation.
	plates := regexp.MustCompile(`key: '([a-z-]+)', op: '([a-z-]+)'`).FindAllStringSubmatch(js, -1)
	if len(plates) == 0 {
		t.Fatal("no command plates found in control.js; this test cannot find what to check")
	}
	seen := map[string]bool{}
	for _, p := range plates {
		key, op := p[1], p[2]
		if !known[op] {
			t.Errorf("the Ledger's %q plate performs operation %q, which the plane does not have. "+
				"Either the operation was renamed and the page was not, or the plate is unreachable.",
				key, op)
		}
		seen[op] = true
	}

	// THE OTHER DIRECTION, which is the one that actually went wrong: an operation
	// the plane offers and the Ledger has no way to reach. That is a dead end for
	// anyone driving from the page, and `refine` was exactly this for weeks.
	for op := range known {
		if !seen[op] {
			t.Errorf("the plane offers %q and the Ledger has no plate for it — an operator on "+
				"this page cannot reach it, which is a dead end however legal the operation is", op)
		}
	}

	// And no plate may carry its own state list again. The regexp is deliberately
	// narrow: the phrase appears in this file's own commentary explaining why it
	// was removed, and a match on the comment would make the test unfailable.
	if regexp.MustCompile(`\n\s*states: \[`).MatchString(js) {
		t.Error("a command plate has its own `states: [...]` again. Availability comes from " +
			"GET /api/control/operations; a copy here is the third opinion #95 removed")
	}
}

// TestLedgerOperationsEndpointDoesNotNeedThePlane pins the deployment property
// the command plates depend on.
//
// The plates are how an operator escapes a stuck task. If the endpoint that
// decides which of them to show were proxied to the daemon, they would vanish
// exactly when the plane was unhappy — which is when they are most needed and
// when a page showing nothing is least distinguishable from a task with nothing
// available. The table is compiled into this binary, so it is served from here.
func TestLedgerOperationsEndpointDoesNotNeedThePlane(t *testing.T) {
	// A WebServer with NO control client at all: nothing to dial, nothing to fail.
	ws := &WebServer{}
	rec := httptest.NewRecorder()
	ws.handleOperations(rec, httptest.NewRequest(http.MethodGet, "/api/control/operations", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("operations = %d with no plane behind it, want 200: %s", rec.Code, rec.Body)
	}
	var body struct {
		Operations []control.OperationView `json:"operations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding: %v (%s)", err, rec.Body)
	}
	if len(body.Operations) != len(control.AllOperations()) {
		t.Errorf("served %d operations, the plane has %d",
			len(body.Operations), len(control.AllOperations()))
	}
	for _, op := range body.Operations {
		if op.Key == "" {
			t.Error("an operation reached the page with no key, so no plate can match it")
		}
		// `cancel` is the one operation available from every active state, so an
		// empty list anywhere is a marshalling fault rather than a real answer.
		if len(op.States) == 0 {
			t.Errorf("operation %q reached the page admitting no states at all", op.Key)
		}
	}
}

// --- the Landed column's adoption rows -------------------------------------------

// fakeAdopting is a TaskAPI double for the adoption surface: one project behind
// its target holding SEVERAL landed tasks, and one already up to date.
type fakeAdopting struct {
	control.TaskAPI
	adopted []string
	err     error
}

func (f *fakeAdopting) Adoptions() ([]control.Adoption, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []control.Adoption{
		{
			Project: "app", Branch: "development", TargetSHA: "ccccccccccc33333",
			HeadSHA: "aaaaaaaaaaa11111", Behind: 3, Waiting: []string{"T-1", "T-2", "T-3"},
			Adoptable: true, Note: "development is 3 commits behind the landed commit ccccccc",
		},
		{
			Project: "docs", Branch: "main", TargetSHA: "bbbbbbbbbbb22222",
			HeadSHA: "bbbbbbbbbbb22222", Adopted: true,
			Note: "main is already at the landed commit bbbbbbb",
		},
	}, nil
}

func (f *fakeAdopting) AdoptLanded(project string) (control.AdoptionResult, error) {
	if f.err != nil {
		return control.AdoptionResult{}, f.err
	}
	f.adopted = append(f.adopted, project)
	return control.AdoptionResult{
		Project: project, Branch: "development", TargetSHA: "ccccccccccc33333",
		Adopted: true, Note: "development fast-forwarded to ccccccc",
	}, nil
}

func adoptionMux(ws *WebServer) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/control/adoptions", ws.handleAdoptions)
	mux.HandleFunc("POST /api/control/adoptions/{project}", ws.handleAdoptLanded)
	return mux
}

// TestAdoptions_OneRowPerProjectReachesThePage. The Landed column's whole claim
// is that the unit is a PROJECT: three tasks landed into `app` and the page is
// handed one row for it, carrying the branch, the target and the gap.
func TestAdoptions_OneRowPerProjectReachesThePage(t *testing.T) {
	ws := &WebServer{control: &fakeAdopting{}}
	rec := httptest.NewRecorder()
	adoptionMux(ws).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/control/adoptions", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var got adoptionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Available || len(got.Adoptions) != 2 {
		t.Fatalf("adoptions = %+v, want two projects from a reachable plane", got)
	}
	first := got.Adoptions[0]
	if first.Project != "app" || first.Branch == "" || first.TargetSHA == "" || first.Behind != 3 {
		t.Errorf("row = %+v; it must name the branch that would move, the target commit "+
			"and how far behind it is", first)
	}
	if len(first.Waiting) != 3 {
		t.Errorf("row carries %d landed tasks, want 3 — one row for all of them",
			len(first.Waiting))
	}
	// The second is up to date, and says so rather than being absent or offered.
	if !got.Adoptions[1].Adopted || got.Adoptions[1].Adoptable {
		t.Errorf("row = %+v; a project already at its target has nothing to adopt",
			got.Adoptions[1])
	}
	if got.Adoptions[1].Note == "" {
		t.Error("a row with nothing to do still needs a sentence — silence is what this answers")
	}
}

// TestAdoptions_UnreachableIsNotEmpty: the same distinction every polled
// collection draws. "No project is behind" and "I could not ask" must not look
// alike, or an operator trusts the wrong one.
func TestAdoptions_UnreachableIsNotEmpty(t *testing.T) {
	ws := &WebServer{}
	rec := httptest.NewRecorder()
	adoptionMux(ws).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/control/adoptions", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got adoptionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Available || got.Reason == "" {
		t.Errorf("response = %+v; an unreachable plane must say so", got)
	}
	if got.Adoptions == nil {
		t.Error("adoptions should be an empty array, not null (the page iterates it)")
	}
}

// TestAdopt_ActionCarriesTheProjectAndThePlanesNote.
func TestAdopt_ActionCarriesTheProjectAndThePlanesNote(t *testing.T) {
	fake := &fakeAdopting{}
	ws := &WebServer{control: fake}
	rec := httptest.NewRecorder()
	adoptionMux(ws).ServeHTTP(rec,
		httptest.NewRequest(http.MethodPost, "/api/control/adoptions/app", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if len(fake.adopted) != 1 || fake.adopted[0] != "app" {
		t.Fatalf("the plane was asked to adopt %v, want [app]", fake.adopted)
	}
	var res control.AdoptionResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !res.Adopted || res.Note == "" {
		t.Errorf("result = %+v; the note is what the operator reads, on the success path too", res)
	}
}

// TestAdopt_RefusalRelaysTheNote: a dirty tree comes back as a REFUSAL with the
// plane's sentence, not as a failure the page has to invent words for.
func TestAdopt_RefusalRelaysTheNote(t *testing.T) {
	note := "development has uncommitted changes — left untouched; commit or stash, then " +
		"`git merge --ff-only refs/daedalus/target`"
	ws := &WebServer{control: &fakeAdopting{err: &control.RejectionError{
		Reason: control.ReasonBranchNotAdvanced, Message: note, Entity: "app"}}}
	rec := httptest.NewRecorder()
	adoptionMux(ws).ServeHTTP(rec,
		httptest.NewRequest(http.MethodPost, "/api/control/adoptions/app", nil))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 — a refusal is not a failure", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["reason"] != string(control.ReasonBranchNotAdvanced) {
		t.Errorf("reason = %v, want %q", body["reason"], control.ReasonBranchNotAdvanced)
	}
	if got, _ := body["message"].(string); got != note {
		t.Errorf("message = %q, want the plane's own note:\n\t%s", got, note)
	}
}

// TestAdopt_NeedsTheControlPlane: a write with no plane answers 503 loudly
// rather than pretending the branch moved.
func TestAdopt_NeedsTheControlPlane(t *testing.T) {
	ws := &WebServer{}
	rec := httptest.NewRecorder()
	adoptionMux(ws).ServeHTTP(rec,
		httptest.NewRequest(http.MethodPost, "/api/control/adoptions/app", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

// TestLedger_LandedColumnOffersAdoptionPerProject asserts the page half: the
// Landed column is where the rows go, one action per project, and a project with
// nothing to adopt is not handed a button.
//
// This is JavaScript and Go cannot run it, so it asserts on the source — and on
// fragments only the working code contains, because three tests in this file
// have already had to be rewritten after passing against a COMMENT.
func TestLedger_LandedColumnOffersAdoptionPerProject(t *testing.T) {
	src, err := staticFiles.ReadFile("static/control.js")
	if err != nil {
		t.Fatalf("reading the Ledger: %v", err)
	}
	js := string(src)

	// The rows are drawn INTO the landed column, not into a section of their own:
	// the question they answer is the one that column raises.
	if !strings.Contains(js, "if (col.key === BOARD_LANDED) {") {
		t.Error("the Landed column no longer receives the adoption rows, so the column that " +
			"reads as finished says nothing about whether the work is in anybody's branch")
	}
	// One row per project, from a per-project read.
	if !strings.Contains(js, "get('/adoptions')") {
		t.Error("the Ledger never asks which projects have landed work to adopt")
	}
	if !strings.Contains(js, "adoptions.forEach(") {
		t.Error("the adoptions are fetched and never rendered")
	}
	// The action, and the plane's own note on the way back.
	if !strings.Contains(js, "send('POST', '/adoptions/' + enc(project)") {
		t.Error("the Adopt plate does not reach the control-plane endpoint")
	}
	if !strings.Contains(js, "r.note || 'Done.'") {
		t.Error("the Ledger drops the plane's note on the success path, which is exactly the " +
			"silence the note is filled to prevent")
	}
	// And a project that is already at its target is told so instead of being
	// offered an action that would do nothing.
	if !strings.Contains(js, "'Nothing to adopt — this branch already has the landed work.'") {
		t.Error("a project with nothing to adopt is not told so; if it is also still offered " +
			"an Adopt plate, the page is inviting a no-op")
	}
	// The plate is gated on the plane's own "could this move", not on the page's
	// reading of it — so a diverged branch and a detached HEAD are told what to do
	// instead of handed a button that could only be refused.
	if !regexp.MustCompile(`if \(a\.adoptable\) \{`).MatchString(js) {
		t.Error("the Adopt plate is not gated on whether the plane says the branch could move")
	}
	if !strings.Contains(js, "'Nothing here can be wound forward — see the note above.'") {
		t.Error("a branch that cannot be wound forward is left with an empty command row, " +
			"which reads as a page that failed rather than as an answer")
	}
}
