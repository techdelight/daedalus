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
//	GET    /tasks                                         → 200 []Task
//	GET    /tasks/{id}                                    → 200 StatusView
//	                                                       → 404 unknown id
//	POST   /tasks/{id}/dispatch                           → 200 DispatchResult
//	DELETE /tasks/{id}                                    → 200 Task (cancelled)
//	                                                       → 404 unknown id
//
// Errors surface as {"error": "..."} with an appropriate status.

import (
	"encoding/json"
	"errors"
	"fmt"
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
	switch {
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	case errors.As(err, &activeErr), errors.Is(err, ErrConflict), errors.Is(err, ErrIllegalTransition):
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

func writeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// DefaultSocketPath returns the daemon socket path under a data dir, mirroring
// the coordinator's convention.
func DefaultSocketPath(dataDir string) string {
	return filepath.Join(dataDir, ".daedalus", "control.sock")
}
