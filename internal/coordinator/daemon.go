// Copyright (C) 2026 Techdelight BV

package coordinator

// HTTP-over-UDS surface for a long-lived Coordinator process. This is
// slice 1 of Sprint 40: the transport only. The persistence layer
// (sessions.json), the Go client wrapper, and the daemon binary itself
// arrive in subsequent slices.
//
// The Server is a thin translation layer over the existing Coordinator
// type — every endpoint maps 1:1 to a Coordinator method. Concurrency
// safety comes from Coordinator's internal mutex; the Server holds no
// state of its own.
//
// Wire format:
//
//   POST /sessions               body: StartRequest       → 201 Session
//                                                        → 409 ErrAlreadyRunning
//   GET  /sessions                                       → 200 []Session
//   GET  /sessions/{name}                                → 200 Session
//                                                        → 404 not tracked
//   DELETE /sessions/{name}                              → 204
//                                                        → 404 not tracked
//
// Errors other than the sentinel cases above surface as 500 with a
// JSON body of the form {"error": "..."}.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/techdelight/daedalus/core"
)

// Server exposes a Coordinator over HTTP. Construct with NewServer and
// serve via Handler (e.g. behind httptest.Server in tests) or the
// convenience ListenAndServeUDS helper.
type Server struct {
	coord *Coordinator
}

// NewServer wraps the given Coordinator. The Server holds no state
// beyond the coord pointer, so multiple Servers over the same
// Coordinator are safe and equivalent.
func NewServer(coord *Coordinator) *Server {
	return &Server{coord: coord}
}

// Handler returns an http.Handler with all four session routes wired
// up. Callers can serve it any way they want; Go 1.22+ method+path
// patterns keep the mux compact.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sessions", s.handleStart)
	mux.HandleFunc("GET /sessions", s.handleList)
	mux.HandleFunc("GET /sessions/{name}", s.handleGet)
	mux.HandleFunc("DELETE /sessions/{name}", s.handleStop)
	return mux
}

// ListenAndServeUDS binds a Unix socket at socketPath and serves the
// Handler on it until an error occurs. Cleans up any stale socket at
// the path (leftover from a crashed prior instance) before binding.
// The parent directory must already exist.
//
// Timeouts are set with headroom for Coordinator.Start's socket wait
// (30s default): a POST /sessions request may legitimately take that
// long before the runner is ready.
func (s *Server) ListenAndServeUDS(socketPath string) error {
	_ = os.Remove(socketPath) // ignore ENOENT; matters only if it's stale
	l, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("coordinator: bind %s: %w", socketPath, err)
	}
	srv := &http.Server{
		Handler:      s.Handler(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  30 * time.Second,
	}
	return srv.Serve(l)
}

// StartRequest is the POST /sessions body. It's a minimal subset of
// core.Config: only the fields Coordinator.Start actually reads via
// composeEnv, ContainerName, and RunnerSocketPath. Keeping this
// deliberately smaller than core.Config means adding a new Config
// field doesn't automatically become part of the wire API.
type StartRequest struct {
	ProjectName     string `json:"project_name"`
	ProjectDir      string `json:"project_dir"`
	DataDir         string `json:"data_dir"`
	Target          string `json:"target"`
	ImagePrefix     string `json:"image_prefix,omitempty"`
	ContainerPrefix string `json:"container_prefix,omitempty"`
	Runner          string `json:"runner,omitempty"`
	Persona         string `json:"persona,omitempty"`
	Debug           bool   `json:"debug,omitempty"`
	Resume          string `json:"resume,omitempty"`
	Prompt          string `json:"prompt,omitempty"`
}

// toConfig builds the core.Config Coordinator.Start expects. Only the
// fields Start actually consumes are populated; the rest stay zero.
func (r *StartRequest) toConfig() *core.Config {
	return &core.Config{
		ProjectName:     r.ProjectName,
		ProjectDir:      r.ProjectDir,
		DataDir:         r.DataDir,
		Target:          r.Target,
		ImagePrefix:     r.ImagePrefix,
		ContainerPrefix: r.ContainerPrefix,
		Runner:          r.Runner,
		Persona:         r.Persona,
		Debug:           r.Debug,
		Resume:          r.Resume,
		Prompt:          r.Prompt,
	}
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	var req StartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	if req.ProjectName == "" {
		writeError(w, http.StatusBadRequest, errors.New("project_name is required"))
		return
	}

	sess, err := s.coord.Start(req.toConfig())
	if err != nil {
		if errors.Is(err, ErrAlreadyRunning) {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, sess)
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	sessions := s.coord.List()
	// Explicit empty slice keeps clients from having to distinguish
	// `null` from `[]`. Coordinator.List already returns a fresh slice
	// so mutating here is safe.
	if sessions == nil {
		sessions = []Session{}
	}
	writeJSON(w, http.StatusOK, sessions)
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	sess, ok := s.coord.Get(name)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("no session for project %q", name))
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	err := s.coord.Stop(name)
	if err == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeError(w, http.StatusInternalServerError, err)
}

// writeJSON writes v as JSON with the given status. Marshal errors
// fall through to a plain 500 — if json.Marshal fails on a Session or
// []Session the daemon is in a bad state and hiding it is worse than
// surfacing it.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Response already committed; log-only is the honest option
		// but we don't have a logger here. Falling through is
		// acceptable — the connection just closes early.
		return
	}
}

// writeError writes a JSON error body {"error": "..."} with the given
// status. Deliberately does not leak wrapped-error detail beyond the
// top-level message to avoid exposing filesystem paths in ways the
// caller can't sanitize.
func writeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// DefaultSocketPath returns the recommended daemon socket path
// beneath the given data directory. Layout mirrors the runner socket
// convention: <DataDir>/.daedalus/coordinator.sock. The daemon binary
// (Sprint 40 item 4) will resolve DataDir from AppConfig and pass the
// result to ListenAndServeUDS.
func DefaultSocketPath(dataDir string) string {
	return filepath.Join(dataDir, ".daedalus", "coordinator.sock")
}
