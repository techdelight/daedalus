// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strings"
	"time"
)

// Typed steering (docs/guild-master-plan.md M17).
//
// Steering is "tell the worker something else while it is running". The plan
// DEMOTES it, and the reason is worth restating at the top of the file that
// implements it: for a short Job, cancel-plus-redispatch with a corrected
// objective achieves the same thing with machinery that already exists. This
// milestone therefore does not try to make steering powerful. It tries to make it
// TYPED, AUDITED and HONEST — the three things an ad-hoc terminal injection can
// never be.
//
// THE INVARIANT, and it is the whole reason this is safe to build at all:
//
//	Steering changes what the worker is TOLD. It never changes what counts as DONE.
//
// A steered Job still reaches `candidate` and is still independently verified
// against the acceptance policy frozen at the plane-owned target (M15). Nothing in
// this file touches `legalTransitions`, `workerReachable`, the acceptance hash, the
// base sha, or the budget — and TestSteering_ChangesNothingThatDecidesDone asserts
// exactly that. If steering could influence acceptance it would re-open M14's and
// M15's entire argument through a new door, which is precisely the door a
// "just let the operator nudge it" feature tends to open.
//
// PROVENANCE. The issuer is the transport-derived caller class (caller.go), never
// a request field, for the same reason the actor label is: a client that can name
// its own issuer can name "human". Agent callers are proposal-tier like every
// other consequential op — an instruction injected into running work is at least
// as consequential as cancelling it.
//
// HONEST FAILURE is the load-bearing constraint. A steering op that reports
// success without delivering is worse than one that refuses, because it leaves an
// operator believing they redirected a Job that never heard them. So delivery has
// an explicit recorded state, and "the runner cannot accept steering" is a
// first-class outcome (`undeliverable`) rather than an error swallowed on the way
// out.

// DeliveryState is what actually happened to a steering instruction.
//
// FIVE states, where Sprint 63's brief named four. `cancelled` is the addition,
// and it is deliberate: a human withdrawing an instruction and a newer instruction
// replacing it are different facts, and collapsing the first into `superseded`
// would make the log say something that did not happen. The brief's four are the
// DELIVERY axis; cancellation is a human act on an undelivered instruction, and it
// gets its own word.
type DeliveryState string

const (
	// SteerPending is recorded and accepted by a runner that has taken custody of
	// it, but NOT yet known to have reached the worker. It is deliberately not
	// called "queued": the plane does not know when, or whether, the runner's next
	// boundary will arrive.
	SteerPending DeliveryState = "pending"
	// SteerDelivered means the instruction reached the Job at a boundary the runner
	// supports. This is the only state that claims the worker was actually told.
	SteerDelivered DeliveryState = "delivered"
	// SteerUndeliverable means it did NOT reach the worker and will not: there is
	// no steering boundary for this runner, or the handoff failed. The instruction
	// is recorded, the failure is recorded, and nothing pretends otherwise.
	SteerUndeliverable DeliveryState = "undeliverable"
	// SteerSuperseded means a newer instruction replaced this one before it was
	// delivered.
	SteerSuperseded DeliveryState = "superseded"
	// SteerCancelled means a human withdrew it before it was delivered.
	SteerCancelled DeliveryState = "cancelled"
)

// validDeliveryStates is the closed set persisted for steering.state.
var validDeliveryStates = map[DeliveryState]bool{
	SteerPending: true, SteerDelivered: true, SteerUndeliverable: true,
	SteerSuperseded: true, SteerCancelled: true,
}

// IsValidDeliveryState reports whether d is a known delivery state.
func IsValidDeliveryState(d DeliveryState) bool { return validDeliveryStates[d] }

// Settled reports whether this state is final (no further delivery outcome is
// possible). Only `pending` is unsettled.
func (d DeliveryState) Settled() bool { return d != SteerPending }

// SteeringEvent is one typed instruction aimed at one running Job.
//
// It is PLANE-OWNED STATE, like the dependency edge and the integration target: a
// worker cannot forge one, replay one, or read one it was not given. That is what
// separates this from writing into a terminal.
type SteeringEvent struct {
	ID          string `json:"id"` // e.g. "S-1"
	TaskID      string `json:"taskId"`
	JobID       string `json:"jobId"`
	Instruction string `json:"instruction"`
	// IssuedBy is the caller CLASS, derived from the transport (caller.go). Never
	// supplied by the request.
	IssuedBy    CallerClass   `json:"issuedBy"`
	State       DeliveryState `json:"state"`
	Detail      string        `json:"detail,omitempty"`
	CreatedAt   string        `json:"createdAt"`
	UpdatedAt   string        `json:"updatedAt"`
	DeliveredAt string        `json:"deliveredAt,omitempty"`
}

// --- the runner-specific seam ---------------------------------------------------

// SteerTarget names the Job an instruction is aimed at, with everything a runner
// adapter could need to find it. No caller-supplied paths: every field is derived
// by the plane from its own state.
type SteerTarget struct {
	SteeringID  string
	TaskID      string
	JobID       string
	Project     string
	Runner      string
	WorktreeDir string
}

// SteeringDeliverer hands an instruction to a running Job at the next boundary the
// runner actually supports.
//
// It is OPTIONAL on an AgentRunner, and the option is the point: §9 requires the
// authority path (Task/Job/Artifact, verify, reconcile) to stay runner-agnostic,
// and steering delivery is the one genuinely runner-specific piece in the whole
// control plane. So it lives behind a seam the same way AgentRunner and
// VerifyRunner do, an AgentRunner is asked whether it implements it, and a runner
// that does not is not a broken runner — it is a runner with no steering boundary,
// which the plane records as such.
//
// THE CONTRACT, stated exactly, because everything honest about this feature rests
// on it:
//
//   - return nil ONLY when the instruction reached the Job at a supported
//     boundary. nil means `delivered`, and `delivered` is a claim about the
//     worker, not about the runner's inbox.
//   - return ErrSteeringDeferred when custody was taken but the boundary has not
//     arrived yet. The instruction stays `pending` — honestly undelivered — until
//     the adapter calls Service.ConfirmSteeringDelivery.
//   - return ErrSteeringUnsupported when this runner has no steering boundary at
//     all. That is not an error condition; it is an answer.
//   - return any other error when the handoff was attempted and failed.
//
// Never return nil for "I wrote it somewhere the agent may or may not read." That
// is the failure mode this whole design exists to prevent.
type SteeringDeliverer interface {
	DeliverSteering(ctx context.Context, target SteerTarget, instruction string) error
}

var (
	// ErrSteeringUnsupported reports that a runner has no steering boundary.
	ErrSteeringUnsupported = errors.New("control: this runner has no supported steering boundary")
	// ErrSteeringDeferred reports that a runner took custody of an instruction but
	// has not yet reached the boundary at which it can hand it over.
	ErrSteeringDeferred = errors.New("control: steering accepted, awaiting the runner's next boundary")
	// ErrInvalidRequest is malformed input — an empty instruction, and similar. It
	// maps to HTTP 400: the caller can fix it by asking differently, which is not
	// true of a state conflict or a policy refusal.
	ErrInvalidRequest = errors.New("control: invalid request")
)

// steeringDeliveryTimeout bounds how long the plane waits for a runner to answer.
//
// It is short on purpose. The question being asked is "can you take this now?",
// not "run to completion" — a deliverer that needs to wait for a boundary answers
// ErrSteeringDeferred immediately rather than blocking. A runner that neither
// answers nor defers within this window has told us nothing, and the honest
// recording of "told us nothing" is `undeliverable`.
const steeringDeliveryTimeout = 10 * time.Second

// steerableStates are the Job states in which an instruction could conceivably
// reach a worker: something is executing, or is waiting to be told something.
//
// Everything else is refused rather than recorded-and-dropped. A steer aimed at a
// `candidate` Job would be an instruction to a process that has already exited,
// and recording it as undeliverable would bury a caller mistake in the log instead
// of answering it.
var steerableStates = map[State]bool{
	StateWorking: true, StateInputRequired: true,
}

// --- service operations ---------------------------------------------------------

// SteerJob records and attempts to deliver a typed instruction to a running Job,
// as a HUMAN caller.
func (s *Service) SteerJob(jobID, instruction string) (SteeringEvent, error) {
	return s.steerJob(Human(), jobID, instruction)
}

// steerJob is SteerJob with an explicit caller identity.
//
// The order is deliberate: RECORD FIRST, then attempt delivery. An instruction
// that was issued is a fact regardless of whether it arrived, and a design that
// only wrote a row on success would lose exactly the events an operator most needs
// — the ones that did not get through.
func (s *Service) steerJob(caller Caller, jobID, instruction string) (SteeringEvent, error) {
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		return SteeringEvent{}, fmt.Errorf("%w: a steering instruction cannot be empty", ErrInvalidRequest)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.store.GetJob(jobID)
	if err != nil {
		return SteeringEvent{}, err
	}
	if !steerableStates[job.State] {
		return SteeringEvent{}, s.refuse("job", jobID, EventSteering, ReasonNotSteerable, fmt.Sprintf(
			"job %s is %s; only a working or input_required job can be steered", jobID, job.State))
	}
	task, err := s.store.GetTask(job.TaskID)
	if err != nil {
		return SteeringEvent{}, err
	}

	// A newer instruction replaces an older undelivered one, in one transaction:
	// never two pending instructions for a Job ("what is this worker being told"
	// must not have two answers), and never zero because the replacement failed
	// after the supersede had already committed.
	steer, err := s.store.ReplacePendingSteering(task.ID, jobID, instruction, CallerClass(caller.String()))
	if err != nil {
		return SteeringEvent{}, err
	}
	return s.deliverSteering(steer, task, job), nil
}

// deliverSteering attempts the handoff and settles the row. s.mu must be held; it
// is held on return (the runner call happens with the lock released, as every
// other long call in this package does).
//
// It never returns an error: the outcome IS the delivery state, and a caller that
// had to inspect both an error and a state could get the two out of step.
func (s *Service) deliverSteering(steer SteeringEvent, task Task, job Job) SteeringEvent {
	deliverer, ok := steeringDeliverer(s.runner)
	if !ok {
		return s.settleSteering(steer, SteerUndeliverable, fmt.Sprintf(
			"the %s runner has no steering boundary; the instruction was recorded but NOT delivered", job.Runner))
	}

	target := SteerTarget{
		SteeringID: steer.ID, TaskID: task.ID, JobID: job.ID,
		Project: task.Project, Runner: job.Runner,
	}
	if s.worktrees != nil {
		target.WorktreeDir = s.worktrees.Path(job.ID)
	}

	var err error
	s.unlockedDuring(func() {
		timeout := s.steerTimeout
		if timeout <= 0 {
			timeout = steeringDeliveryTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		// RACED against the deadline, not merely handed it. A context is a request;
		// an adapter that ignores it would otherwise block this goroutine forever
		// and steeringDeliveryTimeout would bound nothing it claims to bound. This
		// is the same shape runUnderWallClock uses for the wall-clock budget, and
		// for the same reason: the plane's verdict must not depend on the runner
		// cooperating. The channel is buffered so an adapter that answers after the
		// deadline neither blocks nor leaks.
		//
		// The honest limit is identical too, and worth stating rather than implying:
		// this bounds how long the PLANE waits, not how long the adapter runs. A
		// deliverer that never returns keeps its goroutine until the process exits.
		// What the timeout guarantees is that the caller gets an answer and the row
		// is settled — and `undeliverable` is the truthful record of an adapter that
		// told us nothing in time.
		done := make(chan error, 1)
		go func() { done <- deliverer.DeliverSteering(ctx, target, steer.Instruction) }()
		select {
		case err = <-done:
		case <-ctx.Done():
			err = fmt.Errorf("the runner did not answer within %s: %w", timeout, ctx.Err())
		}
	})

	switch {
	case err == nil:
		return s.settleSteering(steer, SteerDelivered, "delivered at a supported runner boundary")
	case errors.Is(err, ErrSteeringDeferred):
		// Custody taken, boundary not yet reached. The row stays `pending` — which
		// is the honest answer — until the adapter confirms.
		return steer
	case errors.Is(err, ErrSteeringUnsupported):
		return s.settleSteering(steer, SteerUndeliverable, fmt.Sprintf(
			"the %s runner has no steering boundary; the instruction was recorded but NOT delivered", job.Runner))
	default:
		return s.settleSteering(steer, SteerUndeliverable, "the handoff failed: "+err.Error())
	}
}

// settleSteering moves a steering row to a final state, logging the outcome.
//
// The CAS can legitimately lose. Delivery runs with s.mu released, so a second
// steer (or a human withdrawal) can settle this row as `superseded`/`cancelled`
// while the handoff is still in flight — after which this outcome is no longer
// the row's to write. When that happens the caller is told what the RECORD says,
// re-read from the store, never the optimistic value this function was about to
// write: a return value that contradicts the log is the one thing this subsystem
// cannot afford.
func (s *Service) settleSteering(steer SteeringEvent, to DeliveryState, detail string) SteeringEvent {
	settled, err := s.store.SettleSteering(steer.ID, to, EventMeta{Kind: EventSteering}, detail)
	if err == nil {
		return settled
	}
	log.Printf("control: settling steering %s as %s: %v", steer.ID, to, err)
	if current, readErr := s.store.GetSteering(steer.ID); readErr == nil {
		return current
	}
	return steer
}

// ConfirmSteeringDelivery is the callback a runner adapter uses to report that a
// deferred instruction finally reached the Job at its boundary.
//
// It is deliberately NOT on TaskAPI and has no daemon route: it is an in-process
// call from a runner adapter, not something a client may assert. A client that
// could declare its own instruction delivered could manufacture the one claim this
// whole subsystem exists to make trustworthy.
func (s *Service) ConfirmSteeringDelivery(steerID, detail string) (SteeringEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if detail == "" {
		detail = "delivered at a supported runner boundary"
	}
	return s.store.SettleSteering(steerID, SteerDelivered, EventMeta{Kind: EventSteering}, detail)
}

// CancelSteering withdraws an undelivered instruction, as a HUMAN caller.
func (s *Service) CancelSteering(steerID string) (SteeringEvent, error) {
	return s.cancelSteering(Human(), steerID)
}

// cancelSteering is CancelSteering with an explicit caller identity.
//
// Only a `pending` instruction can be cancelled. A delivered one cannot be
// un-said, and pretending otherwise — recording `cancelled` over `delivered` —
// would make the log describe a world in which the worker was never told.
func (s *Service) cancelSteering(caller Caller, steerID string) (SteeringEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur, err := s.store.GetSteering(steerID)
	if err != nil {
		return SteeringEvent{}, err
	}
	if cur.State.Settled() {
		return SteeringEvent{}, fmt.Errorf("%w: steering %s is already %s and cannot be withdrawn",
			ErrWrongState, steerID, cur.State)
	}
	return s.store.SettleSteering(steerID, SteerCancelled,
		EventMeta{Kind: EventSteering, Actor: caller.Actor()}, "withdrawn before delivery")
}

// JobSteering returns every instruction aimed at a Job, oldest first.
func (s *Service) JobSteering(jobID string) ([]SteeringEvent, error) {
	if _, err := s.store.GetJob(jobID); err != nil {
		return nil, err
	}
	return s.store.ListSteeringForJob(jobID)
}

// TaskSteering returns every instruction aimed at any of a Task's Jobs.
func (s *Service) TaskSteering(taskID string) ([]SteeringEvent, error) {
	if _, err := s.store.GetTask(taskID); err != nil {
		return nil, err
	}
	return s.store.ListSteeringForTask(taskID)
}

// steeringDeliverer resolves an AgentRunner to a SteeringDeliverer, if it is one.
//
// The typed-nil guard is the same one jobObserver needs, and for the same reason:
// an interface holding a nil pointer whose method set satisfies SteeringDeliverer
// asserts SUCCESSFULLY and panics on first use. Here the consequence would be
// specifically bad — a panic on the delivery path, recovered by net/http, leaving
// a steering row `pending` forever with no outcome recorded.
func steeringDeliverer(runner AgentRunner) (SteeringDeliverer, bool) {
	if runner == nil {
		return nil, false
	}
	deliverer, ok := runner.(SteeringDeliverer)
	if !ok || deliverer == nil {
		return nil, false
	}
	if v := reflect.ValueOf(deliverer); v.Kind() == reflect.Ptr && v.IsNil() {
		return nil, false
	}
	return deliverer, true
}

// --- store -----------------------------------------------------------------------

// ReplacePendingSteering supersedes every pending instruction for a Job and
// records the replacement, in ONE transaction.
//
// It is one operation because the two halves are one fact. They used to be two
// calls — supersede, then create — and the comment on the first promised that
// there is never a window in which two instructions are pending for one Job. It
// bought that property by permitting the opposite window: a failure after the
// supersede committed and before the insert did left the Job with ZERO pending
// instructions, having silently discarded a valid one the operator believed was
// still standing. Both windows are gone here; the invariant is now a property of
// the transaction rather than of the order of two writes.
func (s *Store) ReplacePendingSteering(taskID, jobID, instruction string, by CallerClass) (SteeringEvent, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return SteeringEvent{}, err
	}
	defer tx.Rollback()

	if err := s.supersedePendingTx(tx, jobID); err != nil {
		return SteeringEvent{}, err
	}

	id, err := nextID(tx, "steering", "S")
	if err != nil {
		return SteeringEvent{}, err
	}
	now := s.now()
	steer := SteeringEvent{
		ID: id, TaskID: taskID, JobID: jobID, Instruction: instruction,
		IssuedBy: by, State: SteerPending, CreatedAt: now, UpdatedAt: now,
	}
	if _, err := tx.Exec(
		`INSERT INTO steering (id, task_id, job_id, instruction, issued_by, state, detail, created_at, updated_at, delivered_at)
		 VALUES (?, ?, ?, ?, ?, ?, '', ?, ?, '')`,
		steer.ID, steer.TaskID, steer.JobID, steer.Instruction,
		string(steer.IssuedBy), string(steer.State), steer.CreatedAt, steer.UpdatedAt,
	); err != nil {
		return SteeringEvent{}, fmt.Errorf("recording steering: %w", err)
	}
	if err := s.logEvent(tx, "job", jobID, "", "",
		EventMeta{Kind: EventSteering, Actor: string(by)}, string(by),
		fmt.Sprintf("steering %s issued for %s: %s", steer.ID, jobID, instruction)); err != nil {
		return SteeringEvent{}, err
	}
	// Also on the TASK, so `daedalus task events` — the view an operator actually
	// reads — shows that the work was redirected. A steering record only the Job
	// knows about is a record nobody finds.
	if err := s.logEvent(tx, "task", taskID, "", "",
		EventMeta{Kind: EventSteering, Actor: string(by)}, string(by),
		fmt.Sprintf("steering %s issued for job %s", steer.ID, jobID)); err != nil {
		return SteeringEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return SteeringEvent{}, err
	}
	return steer, nil
}

// SettleSteering moves a PENDING instruction to a final delivery state.
//
// The CAS is pending-only, which is what makes an outcome single-use: a delivered
// instruction cannot later be recorded undeliverable, and a cancelled one cannot
// be resurrected by a late runner callback.
func (s *Store) SettleSteering(id string, to DeliveryState, meta EventMeta, detail string) (SteeringEvent, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return SteeringEvent{}, err
	}
	defer tx.Rollback()

	cur, err := s.settleSteeringTx(tx, id, to, meta, detail)
	if err != nil {
		return SteeringEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return SteeringEvent{}, err
	}
	return cur, nil
}

// settleSteeringTx is SettleSteering's body on an existing transaction, so a
// supersede can share one with the insert that replaces it.
func (s *Store) settleSteeringTx(tx *sql.Tx, id string, to DeliveryState, meta EventMeta, detail string) (SteeringEvent, error) {
	if to == SteerPending {
		return SteeringEvent{}, fmt.Errorf("control: cannot settle steering %s back to pending", id)
	}
	if !IsValidDeliveryState(to) {
		return SteeringEvent{}, fmt.Errorf("control: %q is not a delivery state", to)
	}
	cur, err := scanSteering(tx.QueryRow(steeringSelect+` WHERE id = ?`, id))
	if err != nil {
		return SteeringEvent{}, err
	}
	now := s.now()
	delivered := cur.DeliveredAt
	if to == SteerDelivered {
		delivered = now
	}
	res, err := tx.Exec(
		`UPDATE steering SET state = ?, detail = ?, updated_at = ?, delivered_at = ? WHERE id = ? AND state = ?`,
		string(to), detail, now, delivered, id, string(SteerPending),
	)
	if err != nil {
		return SteeringEvent{}, fmt.Errorf("settling steering: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return SteeringEvent{}, err
	} else if n != 1 {
		return SteeringEvent{}, fmt.Errorf("%w: steering %s is %s, not pending", ErrConflict, id, cur.State)
	}
	if err := s.logEvent(tx, "job", cur.JobID, "", "", meta, ActorPlane,
		fmt.Sprintf("steering %s %s: %s", id, to, detail)); err != nil {
		return SteeringEvent{}, err
	}
	cur.State, cur.Detail, cur.UpdatedAt, cur.DeliveredAt = to, detail, now, delivered
	return cur, nil
}

// supersedePendingTx marks every pending instruction for a Job as superseded,
// on the caller's transaction — so the supersede and the replacement that
// justifies it commit together or not at all.
func (s *Store) supersedePendingTx(tx *sql.Tx, jobID string) error {
	rows, err := tx.Query(
		`SELECT id FROM steering WHERE job_id = ? AND state = ? ORDER BY seq ASC`,
		jobID, string(SteerPending))
	if err != nil {
		return fmt.Errorf("finding pending steering for %s: %w", jobID, err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := s.settleSteeringTx(tx, id, SteerSuperseded, EventMeta{Kind: EventSteering},
			"replaced by a newer instruction before it was delivered"); err != nil {
			return err
		}
	}
	return nil
}

// GetSteering returns one instruction by id, or ErrNotFound.
func (s *Store) GetSteering(id string) (SteeringEvent, error) {
	return scanSteering(s.db.QueryRow(steeringSelect+` WHERE id = ?`, id))
}

// ListSteeringForJob returns a Job's instructions, oldest first.
func (s *Store) ListSteeringForJob(jobID string) ([]SteeringEvent, error) {
	return s.querySteering(steeringSelect+` WHERE job_id = ? ORDER BY seq ASC`, jobID)
}

// ListSteeringForTask returns every instruction aimed at any Job of a Task.
func (s *Store) ListSteeringForTask(taskID string) ([]SteeringEvent, error) {
	return s.querySteering(steeringSelect+` WHERE task_id = ? ORDER BY seq ASC`, taskID)
}

func (s *Store) querySteering(query string, args ...any) ([]SteeringEvent, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SteeringEvent
	for rows.Next() {
		steer, err := scanSteering(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, steer)
	}
	return out, rows.Err()
}

const steeringSelect = `SELECT id, task_id, job_id, instruction, issued_by, state, detail, created_at, updated_at, delivered_at FROM steering`

func scanSteering(sc rowScanner) (SteeringEvent, error) {
	var steer SteeringEvent
	var by, state string
	err := sc.Scan(&steer.ID, &steer.TaskID, &steer.JobID, &steer.Instruction,
		&by, &state, &steer.Detail, &steer.CreatedAt, &steer.UpdatedAt, &steer.DeliveredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SteeringEvent{}, ErrNotFound
	}
	if err != nil {
		return SteeringEvent{}, err
	}
	// The issuer is read back through the same fail-closed parse as every other
	// caller class: an unreadable value is an agent, never a human.
	steer.IssuedBy = parseCallerClass(by)
	steer.State = DeliveryState(state)
	return steer, nil
}
