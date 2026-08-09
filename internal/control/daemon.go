// Copyright (C) 2026 Techdelight BV

package control

// HTTP-over-Unix-socket surface for the daedalus-control daemon, modelled on
// internal/coordinator/daemon.go. The daemon is the single owner of the SQLite
// store; every UI (the `daedalus task` CLI today) talks to it as a client so
// there is never a second writer.
//
// Wire format:
//
//	POST   /tasks                 body: CreateTaskRequest → 201 Task
//	                                                       → 400 not-git / bad input
//	                                                       → 409 active task exists
//	                                                       → 422 over-budget (refused by policy)
//	GET    /tasks                                         → 200 []Task
//	GET    /tasks/{id}                                    → 200 StatusView
//	                                                       → 404 unknown id
//	POST   /tasks/{id}/dispatch                           → 200 DispatchResult
//	                                                       → 422 attempts/concurrency budget
//	POST   /tasks/{id}/verify                             → 200 VerifyResult
//	                                                       → 422 review-cycle budget
//	POST   /tasks/{id}/retry      body: RetryRequest      → 200 RetryResult
//	                                                       → 422 attempts budget
//	POST   /tasks/{id}/replan     body: ReplanRequest     → 200 Task
//	GET    /tasks/{id}/events                             → 200 []Event  (read-only;
//	                                                          no write verb exists)
//	POST   /tasks/{id}/review                             → 200 ReviewResult
//	POST   /tasks/{id}/approve    body: {note}            → 200 Task (approved)
//	POST   /tasks/{id}/reject     body: {note}            → 200 Task (rejected)
//	POST   /tasks/{id}/integrate                          → 200 IntegrationResult
//	                                                       → 422 approval/review/merge refusal
//	GET    /approvals                                     → 200 []Task awaiting a human
//	GET    /targets                                       → 200 []Target
//	POST   /targets/{project}/sync                        → 200 Target (human resync)
//	DELETE /tasks/{id}                                    → 200 Task (cancelled)
//	                                                       → 404 unknown id
//
// Errors surface as {"error": "..."} with an appropriate status. A *policy
// refusal* additionally carries {"reason": "<RejectionReason>"} with status 422,
// so a client can tell "the plane said no" from "something broke" — the wire
// half of §6's "the plane can reject".

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Server exposes a TaskAPI (a *Service) over HTTP. It holds no state of its own.
type Server struct {
	api TaskAPI
}

// NewServer wraps a TaskAPI (normally a *Service).
func NewServer(api TaskAPI) *Server { return &Server{api: api} }

// Handler wires the task routes (Go 1.22+ method+path patterns).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /tasks", s.handleCreate)
	mux.HandleFunc("GET /tasks", s.handleList)
	mux.HandleFunc("GET /tasks/{id}", s.handleStatus)
	mux.HandleFunc("POST /tasks/{id}/dispatch", s.handleDispatch)
	mux.HandleFunc("POST /tasks/{id}/verify", s.handleVerify)
	mux.HandleFunc("POST /tasks/{id}/retry", s.handleRetry)
	mux.HandleFunc("POST /tasks/{id}/replan", s.handleReplan)
	// GET only, deliberately: the event log has no mutation route because it has
	// no mutation operation (§6). Any other verb on this path falls through to the
	// mux's 405.
	mux.HandleFunc("GET /tasks/{id}/events", s.handleEvents)
	mux.HandleFunc("POST /tasks/{id}/review", s.handleReview)
	mux.HandleFunc("POST /tasks/{id}/approve", s.handleApprove)
	mux.HandleFunc("POST /tasks/{id}/reject", s.handleRejectApproval)
	mux.HandleFunc("POST /tasks/{id}/integrate", s.handleIntegrate)
	mux.HandleFunc("GET /approvals", s.handlePendingApprovals)
	mux.HandleFunc("GET /targets", s.handleTargets)
	mux.HandleFunc("POST /targets/{project}/sync", s.handleSyncTarget)
	mux.HandleFunc("DELETE /tasks/{id}", s.handleCancel)
	return mux
}

// ListenAndServeUDS binds a Unix socket and serves until error. Cleans a stale
// socket first. WriteTimeout is generous: a dispatch may run a headless agent to
// completion before responding.
func (s *Server) ListenAndServeUDS(socketPath string) error {
	_ = os.Remove(socketPath)
	l, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("control: bind %s: %w", socketPath, err)
	}
	srv := &http.Server{
		Handler:     s.Handler(),
		ReadTimeout: 10 * time.Second,
		// No WriteTimeout: a dispatch legitimately blocks on a headless agent run.
		IdleTimeout: 60 * time.Second,
	}
	return srv.Serve(l)
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	t, err := s.api.CreateTask(req)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.api.ListTasks()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if tasks == nil {
		tasks = []Task{}
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	view, err := s.api.TaskStatus(r.PathValue("id"))
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleDispatch(w http.ResponseWriter, r *http.Request) {
	res, err := s.api.DispatchTask(r.PathValue("id"))
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	res, err := s.api.VerifyTask(r.PathValue("id"))
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleRetry(w http.ResponseWriter, r *http.Request) {
	var req RetryRequest
	// An empty body is a plain retry; only malformed JSON is an error.
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	res, err := s.api.RetryTask(r.PathValue("id"), req)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleReplan(w http.ResponseWriter, r *http.Request) {
	var req ReplanRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.api.ReplanTask(r.PathValue("id"), req)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.api.TaskEvents(r.PathValue("id"))
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	if events == nil {
		events = []Event{}
	}
	writeJSON(w, http.StatusOK, events)
}

// decodeOptionalJSON decodes a request body into v, treating an empty body as
// "leave the zero value" rather than an error.
func decodeOptionalJSON(r *http.Request, v any) error {
	err := json.NewDecoder(r.Body).Decode(v)
	if err == nil || errors.Is(err, io.EOF) {
		return nil
	}
	return fmt.Errorf("invalid request body: %w", err)
}

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	res, err := s.api.ReviewTask(r.PathValue("id"))
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// approvalRequest carries the human's note on an approve/reject decision.
type approvalRequest struct {
	Note string `json:"note,omitempty"`
}

func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) {
	var req approvalRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.api.ApproveTask(r.PathValue("id"), req.Note)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleRejectApproval(w http.ResponseWriter, r *http.Request) {
	var req approvalRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.api.RejectApproval(r.PathValue("id"), req.Note)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleIntegrate(w http.ResponseWriter, r *http.Request) {
	res, err := s.api.IntegrateTask(r.PathValue("id"))
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handlePendingApprovals(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.api.PendingApprovals()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if tasks == nil {
		tasks = []Task{}
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) handleTargets(w http.ResponseWriter, r *http.Request) {
	targets, err := s.api.ProjectTargets()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if targets == nil {
		targets = []Target{}
	}
	writeJSON(w, http.StatusOK, targets)
}

func (s *Server) handleSyncTarget(w http.ResponseWriter, r *http.Request) {
	t, err := s.api.SyncTarget(r.PathValue("project"))
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	t, err := s.api.CancelTask(r.PathValue("id"))
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// statusFor maps domain errors to HTTP status codes so the client can recover
// the original sentinel via the status + error envelope.
func statusFor(err error) int {
	var activeErr *ErrActiveTaskExists
	var notGit *ErrNotGitRepo
	var rejected *RejectionError
	switch {
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	// 422 Unprocessable Content: the request was well-formed and understood, and
	// the plane declined it on policy grounds. Distinct from 409 (state conflict)
	// and 500 (something broke) precisely so a client can tell them apart.
	case errors.As(err, &rejected):
		return http.StatusUnprocessableEntity
	// 409 Conflict: the request is fine, the entity's current state is not — a
	// second active task, a stale/illegal transition, or an unmet state
	// precondition (retrying a task that was never rejected, …).
	case errors.As(err, &activeErr), errors.Is(err, ErrConflict),
		errors.Is(err, ErrIllegalTransition), errors.Is(err, ErrWrongState):
		return http.StatusConflict
	case errors.As(err, &notGit):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError emits the {"error": ...} envelope, plus a machine-readable
// {"reason": ...} when the error is a policy refusal so the client can rebuild
// the typed *RejectionError on the far side of the socket.
func writeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := map[string]string{"error": err.Error()}
	var rejected *RejectionError
	if errors.As(err, &rejected) {
		// `error` stays the full human string; `reason` + `message` carry the parts
		// the client needs to rebuild the typed error without double-prefixing it.
		body["reason"] = string(rejected.Reason)
		body["message"] = rejected.Message
	}
	_ = json.NewEncoder(w).Encode(body)
}

// DefaultSocketPath returns the daemon socket path under a data dir, mirroring
// the coordinator's convention.
func DefaultSocketPath(dataDir string) string {
	return filepath.Join(dataDir, ".daedalus", "control.sock")
}
