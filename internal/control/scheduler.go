// Copyright (C) 2026 Techdelight BV

package control

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// The scheduler (docs/guild-master-plan.md M16, §5).
//
// WHAT ACTUALLY CHANGED IN SPRINT 61, stated precisely, because the answer is
// smaller than it looks and the shape of it decides everything else:
//
//   - A TASK still has at most one Job in flight. The state machine guarantees it
//     — a Task is dispatchable only from planned/queued/rejected, and dispatching
//     moves it to `working` — so nothing about a Task's own lifecycle is
//     concurrent, and every per-Task singular lookup (candidateJob, firstArtifact,
//     jobInState) stays correct without change.
//   - What was forbidden and is now allowed is several TASKS being active on one
//     project. That is the unit of parallelism, and lifting it is a one-line
//     removal in CreateTask.
//   - The service lock was ALREADY released across the long calls (unlockedDuring,
//     Sprint 58), so two dispatches genuinely overlap the moment two Tasks exist.
//     No execution machinery is added here.
//
// THE TRAP, named because it is easy to get wrong: `withClaim` is keyed by TASK.
// While only one Task per project could be active, "one claim per Task" and "one
// Job per project" were the same sentence. They are now different: N Tasks on one
// project hold N independent claims, so the claim set does NOT limit project
// concurrency and never did — it prevents two operations racing on ONE Task. The
// per-project limit has to come from somewhere else, and that somewhere is this
// file. Building the scheduler on the claim set would have produced a limiter
// that silently never fires.

// SchedulerLimits bounds how much work may be in flight at once.
type SchedulerLimits struct {
	// Global caps running Jobs across every project — the host's capacity, since
	// each Job is a container. 0 means unbounded.
	Global int `json:"global,omitempty"`
	// PerProject caps running Jobs within one project. 0 means unbounded, and the
	// per-Task budget's Concurrency axis still applies on top.
	PerProject int `json:"perProject,omitempty"`
}

// DefaultSchedulerLimits is deliberately conservative: parallelism is opt-in.
//
// Lifting one-active-Job-per-project changes what the plane CAN do; it should not
// silently change what an existing installation DOES do. An operator who has not
// asked for parallelism keeps the behaviour they had — one Job per project — and
// raises it when they mean to.
func DefaultSchedulerLimits() SchedulerLimits {
	return SchedulerLimits{Global: 4, PerProject: 1}
}

// admissionRequest is what the scheduler decides about.
type admissionRequest struct {
	taskID  string
	project string
	// projectRunning and globalRunning are the observed counts, read from the
	// store under the service lock so they cannot drift between the count and the
	// decision.
	projectRunning int
	globalRunning  int
	// taskConcurrency is the per-Task budget axis (0 = unset).
	taskConcurrency int
}

// Scheduler admits Jobs subject to the global, per-project and per-Task limits,
// and keeps admission FAIR: a Task that has been waiting longer is admitted
// before a newer one.
//
// Fairness is not decoration. Without it, a project that dispatches in a tight
// loop starves a Task that asked first — and because a refusal is a typed
// rejection the caller retries, so the starved Task would retry forever while
// newer work sails past it. The rule is simple and deterministic: when capacity
// is available, only the OLDEST waiter may take it.
type Scheduler struct {
	mu     sync.Mutex
	limits SchedulerLimits

	// waiting records the order in which Tasks were first refused for capacity,
	// keyed by task id. A Task is removed when it is admitted, cancelled, finishes,
	// or lets its lease lapse — see Forget and expireLocked.
	waiting map[string]waitTicket
	nextSeq int64

	ticketTTL    time.Duration
	maxPassovers int
	now          func() time.Time
}

// waitTicket is a Task's place in the queue.
//
// It is a LEASE, not a permanent reservation. Fairness without liveness is a
// deadlock: a Task refused for capacity keeps its place while sitting in
// `planned`, and nothing wakes it — dispatch is synchronous, so the queue only
// advances if a human re-issues dispatch for that exact Task. One abandoned
// dispatch attempt would otherwise brick a project's parallelism, and an
// abandoned GLOBAL waiter would stall every project at once. So a ticket has to
// be renewed by its owner re-asking, and expires if it is not.
type waitTicket struct {
	seq     int64
	project string
	// renewedAt is when the owner last asked. A ticket older than ticketTTL is
	// swept: the wall-clock backstop for a queue nobody is pushing on.
	renewedAt time.Time
	// passovers counts how many times ANOTHER Task was refused because of this
	// ticket. It is the fast path: under contention the queue heals in a few
	// attempts rather than waiting out the TTL, because the Tasks being blocked
	// are exactly the ones retrying.
	passovers int
}

// Ticket liveness bounds. Both exist because they cover different situations:
// passovers heal a busy queue quickly, the TTL heals a quiet one at all.
const (
	// defaultTicketTTL is how long a ticket survives without its owner re-asking.
	defaultTicketTTL = 2 * time.Minute
	// defaultMaxPassovers is how many times a ticket may block another Task before
	// it is treated as abandoned.
	defaultMaxPassovers = 3
)

// NewScheduler returns a scheduler with the given limits.
func NewScheduler(limits SchedulerLimits) *Scheduler {
	return &Scheduler{
		limits:       limits,
		waiting:      map[string]waitTicket{},
		ticketTTL:    defaultTicketTTL,
		maxPassovers: defaultMaxPassovers,
		now:          func() time.Time { return time.Now() },
	}
}

// SetTicketLiveness overrides the ticket lease bounds and clock. Tests use it to
// make expiry deterministic; the daemon leaves the defaults.
func (s *Scheduler) SetTicketLiveness(ttl time.Duration, maxPassovers int, now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ttl > 0 {
		s.ticketTTL = ttl
	}
	if maxPassovers > 0 {
		s.maxPassovers = maxPassovers
	}
	if now != nil {
		s.now = now
	}
}

// Limits returns the configured limits (for status views).
func (s *Scheduler) Limits() SchedulerLimits {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.limits
}

// SetLimits replaces the limits (the daemon applies the host-side policy).
func (s *Scheduler) SetLimits(l SchedulerLimits) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limits = l
}

// admit decides whether a Task may start a Job now.
//
// Returns nil to admit. On refusal the Task is recorded as waiting (so it keeps
// its place in line) and a typed *RejectionError is returned — never a silent
// drop, because a scheduler that quietly declines is indistinguishable from one
// that is broken.
func (s *Scheduler) admit(req admissionRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Sweep lapsed leases first, so a Task that walked away cannot hold capacity
	// hostage. THE INVARIANT THIS RESTORES: free capacity must eventually become
	// usable without human intervention.
	s.expireLocked()

	// The per-Task budget axis is a ceiling the Task itself declared; the
	// scheduler's per-project limit is the operator's. Both apply, and the
	// tightest wins — which is what finally makes the budget's `concurrency` axis
	// fire at all (it could not, while only one Job per project was possible:
	// Sprint 58 audit finding 11).
	if limit, exceeded := tightestLimit(req.projectRunning, s.limits.PerProject, req.taskConcurrency); exceeded {
		return s.refuseLocked(req, ReasonConcurrencyExceeded, fmt.Sprintf(
			"project %q already has %d job(s) running (limit %d)", req.project, req.projectRunning, limit))
	}
	if s.limits.Global > 0 && req.globalRunning >= s.limits.Global {
		return s.refuseLocked(req, ReasonSchedulerSaturated, fmt.Sprintf(
			"the control plane already has %d job(s) running (global limit %d)", req.globalRunning, s.limits.Global))
	}

	// Capacity exists. FAIRNESS: it belongs to the oldest LIVE waiter, not to
	// whoever asked most recently — and not to one that has stopped asking.
	if older, ok := s.blockingWaiterLocked(req); ok {
		return s.refuseLocked(req, ReasonQueuedBehind, fmt.Sprintf(
			"task %s has been waiting longer for capacity; %s yields to it", older, req.taskID))
	}

	delete(s.waiting, req.taskID) // admitted: it no longer waits
	return nil
}

// tightestLimit returns the binding limit and whether it is already reached.
// A limit of 0 means unbounded on that axis.
func tightestLimit(running int, limits ...int) (int, bool) {
	binding := 0
	for _, l := range limits {
		if l <= 0 {
			continue
		}
		if binding == 0 || l < binding {
			binding = l
		}
	}
	if binding == 0 {
		return 0, false
	}
	return binding, running >= binding
}

// blockingWaiterLocked returns the id of a LIVE Task that has been waiting longer
// than req for capacity req could use, if any. s.mu must be held.
//
// "Capacity it could use" is per project: a Task waiting on project A must not
// block a Task on project B, because freeing A's slot does nothing for B. Only
// the global limit is shared, so a global-saturation waiter blocks everyone.
// It also AGES the tickets it finds: every time a ticket blocks somebody, it
// spends one of its passovers. A ticket whose owner has stopped re-asking runs
// out and is dropped, so a queue under contention heals in a few attempts by the
// very Tasks being blocked, without waiting out the TTL.
func (s *Scheduler) blockingWaiterLocked(req admissionRequest) (string, bool) {
	mine, waitingAlready := s.waiting[req.taskID]
	type candidate struct {
		id  string
		seq int64
	}
	var older []candidate
	for id, ticket := range s.waiting {
		if id == req.taskID {
			continue
		}
		if ticket.project != req.project && ticket.project != globalWaiter {
			continue // a different project's queue is not this Task's competition
		}
		if waitingAlready && ticket.seq >= mine.seq {
			continue // it has been waiting no longer than we have
		}
		older = append(older, candidate{id: id, seq: ticket.seq})
	}
	if len(older) == 0 {
		return "", false
	}
	// Deterministic: the oldest blocks, so the refusal message is stable and a
	// test can assert on it.
	sort.Slice(older, func(i, j int) bool { return older[i].seq < older[j].seq })

	// Spend a passover on every ticket that is holding this request up, and drop
	// any that has now been passed over too often to be considered live.
	var stillBlocking string
	for _, c := range older {
		ticket := s.waiting[c.id]
		ticket.passovers++
		if ticket.passovers > s.maxPassovers {
			delete(s.waiting, c.id)
			continue
		}
		s.waiting[c.id] = ticket
		if stillBlocking == "" {
			stillBlocking = c.id
		}
	}
	if stillBlocking == "" {
		return "", false // every older waiter had lapsed; the capacity is ours
	}
	return stillBlocking, true
}

// expireLocked drops tickets whose owner has not re-asked within the TTL. s.mu
// must be held.
func (s *Scheduler) expireLocked() {
	cutoff := s.now().Add(-s.ticketTTL)
	for id, ticket := range s.waiting {
		if ticket.renewedAt.Before(cutoff) {
			delete(s.waiting, id)
		}
	}
}

// globalWaiter marks a ticket that is waiting on the GLOBAL limit rather than a
// single project's, so it competes with every project rather than one.
const globalWaiter = "*"

// refuseLocked records the Task's place in line and returns the typed rejection.
// s.mu must be held.
func (s *Scheduler) refuseLocked(req admissionRequest, reason RejectionReason, msg string) error {
	project := req.project
	if reason == ReasonSchedulerSaturated {
		project = globalWaiter
	}
	if existing, already := s.waiting[req.taskID]; already {
		// RENEWAL: the owner is still asking, so its lease is refreshed and its
		// passovers reset. Keeping the original seq is what preserves its place —
		// re-asking must not send a Task to the back of the queue.
		existing.renewedAt = s.now()
		existing.passovers = 0
		existing.project = project
		s.waiting[req.taskID] = existing
	} else {
		s.nextSeq++
		s.waiting[req.taskID] = waitTicket{
			seq: s.nextSeq, project: project, renewedAt: s.now(),
		}
	}
	return &RejectionError{Reason: reason, Message: msg, Entity: req.taskID}
}

// Forget drops a Task's place in line — called when it is admitted, cancelled or
// reaches a terminal state, so a Task that will never run cannot block others
// forever by holding the oldest ticket.
func (s *Scheduler) Forget(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.waiting, taskID)
}

// Waiting returns the ids of Tasks currently queued for capacity, oldest first.
// Used by the status views so a queued Task is visibly distinct from a running
// one.
func (s *Scheduler) Waiting() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	type entry struct {
		id  string
		seq int64
	}
	all := make([]entry, 0, len(s.waiting))
	for id, ticket := range s.waiting {
		all = append(all, entry{id: id, seq: ticket.seq})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].seq < all[j].seq })
	ids := make([]string, 0, len(all))
	for _, e := range all {
		ids = append(ids, e.id)
	}
	return ids
}
