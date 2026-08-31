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
//	                                                       → 422 over-budget (refused by policy)
//	GET    /tasks                                         → 200 []Task
//	GET    /tasks/{id}                                    → 200 StatusView
//	                                                       → 404 unknown id
//	POST   /tasks/{id}/dispatch                           → 200 DispatchResult
//	                                                       → 422 attempts/concurrency budget
//	POST   /tasks/{id}/verify                             → 200 VerifyResult
//	                                                       → 422 review-cycle budget
//	POST   /tasks/{id}/retry      body: RetryRequest      → 200 RetryResult
//	POST   /tasks/{id}/reverify   body: ReverifyRequest   → 200 ReverifyResult
//	POST   /tasks/{id}/checks     body: AmendChecksRequest → 200 Task
//	POST   /tasks/{id}/budget     body: AmendBudgetRequest → 200 Task
//	                                                       → 422 attempts budget
//	POST   /tasks/{id}/replan     body: ReplanRequest     → 200 Task
//	POST   /tasks/{id}/refine     body: RefineRequest     → 200 Task (#91)
//	GET    /tasks/{id}/events                             → 200 []Event  (read-only;
//	                                                          no write verb exists)
//	POST   /tasks/{id}/review                             → 200 ReviewResult
//	POST   /tasks/{id}/approve    body: {note}            → 200 Task (approved)
//	POST   /tasks/{id}/reject     body: {note}            → 200 Task (rejected)
//	POST   /tasks/{id}/integrate                          → 200 IntegrationResult
//	                                                       → 422 approval/review/merge refusal
//	GET    /approvals                                     → 200 []Task awaiting a human
//	POST   /tasks/{id}/dependencies body: {dependsOn}     → 200 DependencyEdge
//	                                                       → 409 cycle / invalid
//	GET    /tasks/{id}/dependencies                       → 200 DependencyView
//	GET    /operations                                    → 200 {operations: []OperationView}
//	                                                          (which states admit which
//	                                                          operation — #95 item 1)
//	GET    /status                                        → 200 PlaneStatus
//	GET    /board                                         → 200 BoardView
//	POST   /jobs/{id}/steer     body: {instruction}       → 200 SteeringEvent
//	                                                       → 400 empty instruction
//	                                                       → 422 not steerable
//	GET    /jobs/{id}/steering                            → 200 []SteeringEvent
//	DELETE /steering/{id}                                 → 200 SteeringEvent
//	                                                          (withdrawn before delivery)
//	GET    /targets                                       → 200 []TargetView
//	GET    /targets/lag                                   → 200 []TargetLag (#89)
//	GET    /proposals[?state=pending]                     → 200 []Proposal
//	POST   /proposals/{id}/confirm  body: {note}          → 200 Proposal (and the
//	                                                          operation executes)
//	POST   /proposals/{id}/deny     body: {note}          → 200 Proposal
//	POST   /targets/{project}/sync                        → 200 Target (human resync)
//	DELETE /tasks/{id}                                    → 200 Task (cancelled)
//	                                                       → 404 unknown id
//	GET    /programmes                                    → 200 []Programme
//	POST   /programmes            body: ProgrammeRequest  → 201 Programme
//	GET    /programmes/{id}                               → 200 Programme
//	POST   /programmes/{id}       body: ProgrammeRequest  → 200 Programme (amended)
//	DELETE /programmes/{id}                               → 200 {"deleted": id}
//	                                                       → 409 still has tasks
//	GET    /programmes/{id}/status                        → 200 ProgrammeStatus
//
// Errors surface as {"error": "..."} with an appropriate status. A *policy
// refusal* additionally carries {"reason": "<RejectionReason>"} with status 422,
// so a client can tell "the plane said no" from "something broke" — the wire
// half of §6's "the plane can reject".
//
// A refusal that leaves an entity somewhere ALSO carries {"remedies": ["retry",
// …]} — the operations available from the state it was refused in, computed from
// the operation table rather than written into the message (#95 item 2). A client
// renders them; it never has to guess which of them the plane would accept.

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
//
// The daemon builds ONE Server PER LISTENER, each wrapping the same Service
// through a different caller scope. That is what makes the caller identity
// unforgeable: the class is decided by which socket accepted the connection, not
// by anything in the request (see caller.go).
type Server struct {
	api TaskAPI
}

// NewServer wraps a TaskAPI (normally a *Service) for the HUMAN socket.
func NewServer(api TaskAPI) *Server { return &Server{api: api} }

// NewServerForCaller wraps a Service for one caller class. The returned Server
// serves the same routes; what differs is the authority behind them.
func NewServerForCaller(svc *Service, caller Caller) *Server {
	return &Server{api: svc.WithCaller(caller)}
}

// Handler wires the task routes (Go 1.22+ method+path patterns).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /tasks", s.handleCreate)
	mux.HandleFunc("GET /tasks", s.handleList)
	mux.HandleFunc("GET /tasks/{id}", s.handleStatus)
	mux.HandleFunc("POST /tasks/{id}/dispatch", s.handleDispatch)
	mux.HandleFunc("POST /tasks/{id}/verify", s.handleVerify)
	mux.HandleFunc("POST /tasks/{id}/retry", s.handleRetry)
	mux.HandleFunc("POST /tasks/{id}/reverify", s.handleReverify)
	mux.HandleFunc("POST /tasks/{id}/checks", s.handleAmendChecks)
	mux.HandleFunc("POST /tasks/{id}/budget", s.handleAmendBudget)
	mux.HandleFunc("POST /tasks/{id}/replan", s.handleReplan)
	mux.HandleFunc("POST /tasks/{id}/refine", s.handleRefine)
	// GET only, deliberately: the event log has no mutation route because it has
	// no mutation operation (§6). Any other verb on this path falls through to the
	// mux's 405.
	mux.HandleFunc("GET /tasks/{id}/events", s.handleEvents)
	mux.HandleFunc("POST /tasks/{id}/review", s.handleReview)
	mux.HandleFunc("POST /tasks/{id}/approve", s.handleApprove)
	mux.HandleFunc("POST /tasks/{id}/reject", s.handleRejectApproval)
	mux.HandleFunc("POST /tasks/{id}/integrate", s.handleIntegrate)
	mux.HandleFunc("GET /approvals", s.handlePendingApprovals)
	mux.HandleFunc("POST /tasks/{id}/dependencies", s.handleAddDependency)
	mux.HandleFunc("GET /tasks/{id}/dependencies", s.handleTaskDependencies)
	mux.HandleFunc("GET /operations", s.handleOperations)
	mux.HandleFunc("GET /status", s.handlePlaneStatus)
	mux.HandleFunc("GET /board", s.handleBoard)
	mux.HandleFunc("POST /jobs/{id}/steer", s.handleSteerJob)
	mux.HandleFunc("GET /jobs/{id}/steering", s.handleJobSteering)
	mux.HandleFunc("DELETE /steering/{id}", s.handleCancelSteering)
	mux.HandleFunc("GET /targets", s.handleTargets)
	mux.HandleFunc("GET /proposals", s.handleListProposals)
	mux.HandleFunc("POST /proposals/{id}/confirm", s.handleConfirmProposal)
	mux.HandleFunc("POST /proposals/{id}/deny", s.handleDenyProposal)
	mux.HandleFunc("GET /targets/lag", s.handleTargetLags)
	mux.HandleFunc("POST /targets/{project}/sync", s.handleSyncTarget)
	mux.HandleFunc("DELETE /tasks/{id}", s.handleCancel)
	// Programmes (M20): the shared intent Tasks serve.
	mux.HandleFunc("GET /programmes", s.handleListProgrammes)
	mux.HandleFunc("POST /programmes", s.handleCreateProgramme)
	mux.HandleFunc("GET /programmes/{id}", s.handleGetProgramme)
	mux.HandleFunc("POST /programmes/{id}", s.handleUpdateProgramme)
	mux.HandleFunc("DELETE /programmes/{id}", s.handleDeleteProgramme)
	mux.HandleFunc("GET /programmes/{id}/status", s.handleProgrammeStatus)
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
		writeError(w, StatusFor(err), err)
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
		writeError(w, StatusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleDispatch(w http.ResponseWriter, r *http.Request) {
	res, err := s.api.DispatchTask(r.PathValue("id"))
	if err != nil {
		writeError(w, StatusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	var req VerifyRequest
	// An empty body is an ordinary verification; a waiver has to be asked for.
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	res, err := s.api.VerifyTask(r.PathValue("id"), req)
	if err != nil {
		writeError(w, StatusFor(err), err)
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
		writeError(w, StatusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleAmendChecks(w http.ResponseWriter, r *http.Request) {
	var req AmendChecksRequest
	// NOT optional: an empty body here would mean "clear every check", which is a
	// destructive reading of a missing field. Clearing is spelled with an explicit
	// empty list.
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.api.AmendTaskChecks(r.PathValue("id"), req)
	if err != nil {
		writeError(w, StatusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleReverify(w http.ResponseWriter, r *http.Request) {
	var req ReverifyRequest
	// An empty body is a replay; only malformed JSON is an error.
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	res, err := s.api.ReverifyTask(r.PathValue("id"), req)
	if err != nil {
		writeError(w, StatusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleRefine arms a Task to continue from its existing artifact (#91).
func (s *Server) handleRefine(w http.ResponseWriter, r *http.Request) {
	var req RefineRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.api.RefineTask(r.PathValue("id"), req)
	if err != nil {
		writeError(w, StatusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleReplan(w http.ResponseWriter, r *http.Request) {
	var req ReplanRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.api.ReplanTask(r.PathValue("id"), req)
	if err != nil {
		writeError(w, StatusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.api.TaskEvents(r.PathValue("id"))
	if err != nil {
		writeError(w, StatusFor(err), err)
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

// decodeJSON requires a body, unlike decodeOptionalJSON. Used where a missing
// field would have a destructive default — amending checks with an absent list
// would mean "clear them all", which nobody would intend by omission.
func decodeJSON(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	return nil
}

// handleAmendBudget raises a Task's frozen limits (#95 item 4). The service
// refuses an agent outright — the socket a request arrived on is what says which
// it is, so the check cannot live here.
func (s *Server) handleAmendBudget(w http.ResponseWriter, r *http.Request) {
	var req AmendBudgetRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.api.AmendTaskBudget(r.PathValue("id"), req)
	if err != nil {
		writeError(w, StatusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	res, err := s.api.ReviewTask(r.PathValue("id"))
	if err != nil {
		writeError(w, StatusFor(err), err)
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
		writeError(w, StatusFor(err), err)
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
		writeError(w, StatusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleIntegrate(w http.ResponseWriter, r *http.Request) {
	var req IntegrateRequest
	// An empty body means "land it, leave my branch alone" — the default.
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	res, err := s.api.IntegrateTask(r.PathValue("id"), req)
	if err != nil {
		writeError(w, StatusFor(err), err)
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

// dependencyRequest declares one graph edge.
type dependencyRequest struct {
	DependsOn string `json:"dependsOn"`
}

func (s *Server) handleAddDependency(w http.ResponseWriter, r *http.Request) {
	var req dependencyRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	edge, err := s.api.AddDependency(r.PathValue("id"), req.DependsOn)
	if err != nil {
		writeError(w, StatusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, edge)
}

func (s *Server) handleTaskDependencies(w http.ResponseWriter, r *http.Request) {
	view, err := s.api.TaskDependencies(r.PathValue("id"))
	if err != nil {
		writeError(w, StatusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handlePlaneStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.api.PlaneStatus()
	if err != nil {
		writeError(w, StatusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleBoard(w http.ResponseWriter, r *http.Request) {
	view, err := s.api.ProgrammeBoard()
	if err != nil {
		writeError(w, StatusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// steerRequest carries a typed instruction for a running Job.
//
// The ISSUER is deliberately absent: it comes from the listener this request
// arrived on (caller.go), never from the body. A client that could name its own
// issuer could name "human", which would make the provenance on every steering row
// an assertion by the party it exists to constrain.
type steerRequest struct {
	Instruction string `json:"instruction"`
}

func (s *Server) handleSteerJob(w http.ResponseWriter, r *http.Request) {
	var req steerRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	steer, err := s.api.SteerJob(r.PathValue("id"), req.Instruction)
	if err != nil {
		writeError(w, StatusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, steer)
}

func (s *Server) handleJobSteering(w http.ResponseWriter, r *http.Request) {
	steers, err := s.api.JobSteering(r.PathValue("id"))
	if err != nil {
		writeError(w, StatusFor(err), err)
		return
	}
	if steers == nil {
		steers = []SteeringEvent{}
	}
	writeJSON(w, http.StatusOK, steers)
}

func (s *Server) handleCancelSteering(w http.ResponseWriter, r *http.Request) {
	steer, err := s.api.CancelSteering(r.PathValue("id"))
	if err != nil {
		writeError(w, StatusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, steer)
}

func (s *Server) handleTargets(w http.ResponseWriter, r *http.Request) {
	targets, err := s.api.ProjectTargets()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if targets == nil {
		targets = []TargetView{}
	}
	writeJSON(w, http.StatusOK, targets)
}

// handleTargetLags reports where the integration target trails a checkout (#89).
func (s *Server) handleTargetLags(w http.ResponseWriter, r *http.Request) {
	lags, err := s.api.TargetLags()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if lags == nil {
		lags = []TargetLag{}
	}
	writeJSON(w, http.StatusOK, lags)
}

func (s *Server) handleSyncTarget(w http.ResponseWriter, r *http.Request) {
	t, err := s.api.SyncTarget(r.PathValue("project"))
	if err != nil {
		writeError(w, StatusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleListProposals(w http.ResponseWriter, r *http.Request) {
	proposals, err := s.api.ListProposals(ProposalState(r.URL.Query().Get("state")))
	if err != nil {
		writeError(w, StatusFor(err), err)
		return
	}
	if proposals == nil {
		proposals = []Proposal{}
	}
	writeJSON(w, http.StatusOK, proposals)
}

func (s *Server) handleConfirmProposal(w http.ResponseWriter, r *http.Request) {
	s.resolveProposal(w, r, true)
}

func (s *Server) handleDenyProposal(w http.ResponseWriter, r *http.Request) {
	s.resolveProposal(w, r, false)
}

func (s *Server) resolveProposal(w http.ResponseWriter, r *http.Request, confirm bool) {
	var req approvalRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	p, err := s.api.ResolveProposal(r.PathValue("id"), confirm, req.Note)
	if err != nil {
		writeError(w, StatusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	t, err := s.api.CancelTask(r.PathValue("id"))
	if err != nil {
		writeError(w, StatusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// --- programmes (M20) -----------------------------------------------------------

func (s *Server) handleListProgrammes(w http.ResponseWriter, r *http.Request) {
	progs, err := s.api.ListProgrammes()
	if err != nil {
		writeError(w, StatusFor(err), err)
		return
	}
	if progs == nil {
		progs = []Programme{}
	}
	writeJSON(w, http.StatusOK, progs)
}

func (s *Server) handleGetProgramme(w http.ResponseWriter, r *http.Request) {
	p, err := s.api.GetProgramme(r.PathValue("id"))
	if err != nil {
		writeError(w, StatusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleProgrammeStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.api.ProgrammeStatusFor(r.PathValue("id"))
	if err != nil {
		writeError(w, StatusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleCreateProgramme(w http.ResponseWriter, r *http.Request) {
	var req ProgrammeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("%w: %v", ErrInvalidRequest, err))
		return
	}
	p, err := s.api.CreateProgramme(req)
	if err != nil {
		writeError(w, StatusFor(err), err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleUpdateProgramme(w http.ResponseWriter, r *http.Request) {
	var req ProgrammeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("%w: %v", ErrInvalidRequest, err))
		return
	}
	p, err := s.api.UpdateProgramme(r.PathValue("id"), req)
	if err != nil {
		writeError(w, StatusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleDeleteProgramme(w http.ResponseWriter, r *http.Request) {
	if err := s.api.DeleteProgramme(r.PathValue("id")); err != nil {
		writeError(w, StatusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": r.PathValue("id")})
}

// StatusFor maps domain errors to HTTP status codes so the client can recover
// the original sentinel via the status + error envelope.
//
// Exported because the Web UI serves the same operations under /api/control and
// must give the browser the answer the plane gave, not a re-derivation of it. It
// is the same function on both sides of the socket, so a refusal cannot mean one
// thing to the CLI and another to a page.
func StatusFor(err error) int {
	var notGit *ErrNotGitRepo
	var rejected *RejectionError
	var cycle *ErrDependencyCycle
	var remote *RemoteError
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
	// A dependency cycle or a malformed edge belongs here rather than in the 400
	// group below: the request is well-formed, and whether it is acceptable depends
	// on the state of the GRAPH, not on how it was written. The same edge is legal
	// before an intervening one is added and illegal after, which is a conflict with
	// existing state — exactly what 409 means — not malformed input.
	case errors.As(err, &cycle), errors.Is(err, ErrDependencyInvalid):
		return http.StatusConflict
	case errors.Is(err, ErrConflict),
		errors.Is(err, ErrIllegalTransition), errors.Is(err, ErrWrongState):
		return http.StatusConflict
	case errors.As(err, &notGit), errors.Is(err, ErrInvalidRequest):
		// 400: the request itself is malformed (an empty steering instruction, a
		// non-Git project). The caller fixes it by asking differently, which is not
		// true of a 409 state conflict or a 422 policy refusal.
		return http.StatusBadRequest
	// An answer that already came from a plane: relay its status rather than
	// inventing one. Last of the specific cases because it is the only one that
	// carries a status of its own — everything above is a Go error being assigned
	// a status for the first time.
	case errors.As(err, &remote):
		return remote.Status
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
	body := map[string]any{"error": err.Error()}
	var rejected *RejectionError
	if errors.As(err, &rejected) {
		// `error` stays the full human string; `reason` + `message` carry the parts
		// the client needs to rebuild the typed error without double-prefixing it.
		body["reason"] = string(rejected.Reason)
		body["message"] = rejected.Message
	}
	// `remedies` is what a surface renders as buttons rather than as prose (#95
	// item 2). It rides alongside the human sentence rather than replacing it: the
	// sentence explains WHY one of them is the right choice, which no list can, and
	// the list is what makes the availability checkable instead of a guess.
	if ops := RemediesFor(err); len(ops) > 0 {
		names := make([]string, 0, len(ops))
		for _, op := range ops {
			names = append(names, op.Surface())
		}
		body["remedies"] = names
	}
	_ = json.NewEncoder(w).Encode(body)
}

// RemediesFor pulls the computed way-out list off whichever typed refusal carries
// it. THREE shapes exist and none is redundant: a StateError is a 409 about the
// entity's state, a RejectionError is a 422 about policy — an exhausted budget is
// the second while a wrongly-timed retry is the first — and a RemoteError is
// either of them rebuilt on the far side of a socket.
//
// Exported because the Web UI relays this API rather than serving it, and a
// refusal that arrived over the socket must render exactly as one raised
// in-process. This function is the single place that knows where remedies live.
func RemediesFor(err error) []Operation {
	var se *StateError
	if errors.As(err, &se) {
		return se.Remedies
	}
	var rej *RejectionError
	if errors.As(err, &rej) {
		return rej.Remedies
	}
	var remote *RemoteError
	if errors.As(err, &remote) {
		return remote.Remedies
	}
	return nil
}

// handleOperations serves the operation → states table (#95 item 1).
//
// It takes no lock and touches no store: the table is a compile-time property of
// the binary, not plane state. That is the whole reason a surface can stop
// restating it — the Ledger's COMMANDS entries used to carry their own
// `states: [...]` in JavaScript, which nothing in Go could see and nothing could
// check.
func (s *Server) handleOperations(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"operations": OperationCatalogue()})
}

// DefaultSocketPath returns the HUMAN daemon socket path under a data dir,
// mirroring the coordinator's convention.
func DefaultSocketPath(dataDir string) string {
	return filepath.Join(dataDir, ".daedalus", "control.sock")
}

// AgentSocketPath returns the RESTRICTED socket path — the one an agent client
// (guild-control-mcp) connects through.
//
// It is a separate file, not a flag or a header, because the file is what gets
// mounted into a container: the Guild Master's container receives this socket and
// never the human one, so the caller class is decided by the mount namespace
// before any request is written. Peer credentials could not do this job — the
// socket is srwxr-xr-x and the agent runs as the same uid as the human — so the
// split IS the mechanism rather than a convenience on top of one.
func AgentSocketPath(dataDir string) string {
	return filepath.Join(dataDir, ".daedalus", "control-agent.sock")
}
