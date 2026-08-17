// Copyright (C) 2026 Techdelight BV

package control

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	// Pure-Go SQLite driver. Release builds set CGO_ENABLED=0
	// (.github/workflows/release.yml), so the cgo mattn/go-sqlite3 driver is not
	// an option — modernc.org/sqlite keeps the control DB cgo-free.
	_ "modernc.org/sqlite"
)

// timeFormat matches core.NowUTC so timestamps across the codebase are
// byte-comparable (lexical order == chronological order for ISO 8601 UTC).
const timeFormat = "2006-01-02T15:04:05Z"

// Clock returns the current time. Injected so tests are deterministic (§1 of the
// sprint brief: avoid Date.now()-style nondeterminism).
type Clock func() time.Time

// ErrConflict is returned when an atomic state transition affects zero rows —
// the row was absent or its state was not the expected `from` (optimistic
// concurrency: an illegal or stale transition changes nothing and errors).
var ErrConflict = errors.New("control: state transition conflict (row missing or state changed)")

// ErrIllegalTransition is returned when a requested move is not permitted by the
// state machine for the given actor (worker vs control plane).
var ErrIllegalTransition = errors.New("control: illegal state transition")

// ErrNotFound is returned when a task/job/artifact id does not exist.
var ErrNotFound = errors.New("control: not found")

// ErrWrongState is returned when an operation's *state precondition* is not met
// — retrying a task that was never rejected, verifying one that is not a
// candidate, and so on. It is a state conflict like ErrConflict and
// ErrIllegalTransition (the daemon maps all three to HTTP 409), but it is raised
// by a service-level guard before any transition is attempted, rather than by the
// store's optimistic UPDATE.
var ErrWrongState = errors.New("control: operation not allowed in the task's current state")

// Actor labels who drove an event, recorded in the append-only event log.
//
// Honest caveat (§6): the actor is a *label of a request's origin*, not an
// authenticated identity. Today the only thing that can reach control.sock is
// the human `daedalus task` CLI, so ActorHuman marks the operations a person
// explicitly asked for (cancel / retry / replan / rebase) as distinct from
// decisions the plane made on its own. When an agent client joins (Sprint 60,
// `guild-control-mcp`) the caller identity must be carried from the transport and
// checked — a label alone must never be read as proof of who acted, and it
// grants no authority: transitions are gated by the two tables in model.go.
//
// The store never *decides* an actor: it records the one its caller supplies via
// EventMeta, defaulting to plane/worker from the byWorker flag. Service.
// callerActor is the single place the identity is chosen, so Sprint 60 has one
// seam to thread rather than a hunt through the SQL layer.
const (
	ActorPlane  = "control-plane"
	ActorWorker = "worker"
	ActorHuman  = "human"  // an operation a person explicitly requested
	ActorAgent  = "agent"  // an operation an agent client requested (Sprint 60)
	ActorSystem = "system" // creation, etc.
)

// Event kinds — the "typed" in "typed event" (§6). Every row carries one.
const (
	EventCreated      = "created"      // an entity was inserted
	EventTransition   = "transition"   // a state change
	EventBudget       = "budget"       // a budget decision (usually a refusal)
	EventRejection    = "rejection"    // a request refused, or an artifact rejected
	EventVerification = "verification" // a verification outcome
	EventGovernance   = "governance"   // retry / replan / rebase and similar acts
	EventIntegration  = "integration"  // a target advance / integration transaction step
	EventApproval     = "approval"     // a human approve or reject
	EventReview       = "review"       // an independent reviewer pass
	EventProposal     = "proposal"     // an agent proposed a consequential operation
	EventSchedule     = "schedule"     // a scheduler admission decision
	EventGraph        = "graph"        // a dependency edge or blocked/ready move
	EventSteering     = "steering"     // a typed instruction issued at a running Job
)

// EventMeta annotates an event row. Kind defaults to EventTransition, Reason is
// the machine-readable rejection reason (empty when not a rejection), and Actor
// overrides the label otherwise derived from byWorker.
//
// Actor is a LABEL ONLY. Authority comes from the byWorker flag and the two
// transition tables in model.go; setting Actor never widens what a transition
// may do.
type EventMeta struct {
	Kind   string
	Reason RejectionReason
	Actor  string
}

// Store is a thin, durable layer over a SQLite database holding the control
// plane's *desired* state (§5/§6): tasks, jobs, artifacts, and an append-only
// events log. It is not an ORM — every method maps to explicit SQL.
type Store struct {
	db    *sql.DB
	clock Clock
}

// Option configures a Store at Open time.
type Option func(*Store)

// WithClock injects a deterministic clock (tests).
func WithClock(c Clock) Option {
	return func(s *Store) { s.clock = c }
}

// Open opens (creating if needed) the control DB at path and runs the
// idempotent migration. Callers must Close the returned Store.
func Open(path string, opts ...Option) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening control db: %w", err)
	}
	// SQLite is single-writer; one connection avoids "database is locked" under
	// the CLI's serial access pattern and keeps transactions simple.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, clock: func() time.Time { return time.Now().UTC() }}
	for _, o := range opts {
		o(s)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("enabling foreign keys: %w", err)
	}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// now returns the current time formatted as an ISO 8601 UTC string.
func (s *Store) now() string { return s.clock().UTC().Format(timeFormat) }

// migrate creates the schema idempotently (CREATE TABLE IF NOT EXISTS).
func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS tasks (
    seq             INTEGER PRIMARY KEY AUTOINCREMENT,
    id              TEXT NOT NULL UNIQUE,
    project         TEXT NOT NULL,
    objective       TEXT NOT NULL,
    acceptance_ref  TEXT NOT NULL DEFAULT '',
    acceptance_hash TEXT NOT NULL DEFAULT '',
    image_digest    TEXT NOT NULL DEFAULT '',
    budget          TEXT NOT NULL DEFAULT '',
    base_sha        TEXT NOT NULL,
    state           TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tasks_project ON tasks(project);

CREATE TABLE IF NOT EXISTS jobs (
    seq              INTEGER PRIMARY KEY AUTOINCREMENT,
    id               TEXT NOT NULL UNIQUE,
    task_id          TEXT NOT NULL REFERENCES tasks(id),
    base_sha         TEXT NOT NULL,
    runner           TEXT NOT NULL DEFAULT '',
    budget           INTEGER NOT NULL DEFAULT 0,
    execution_result TEXT NOT NULL DEFAULT '',
    output_snapshot  TEXT NOT NULL DEFAULT '',
    log_path         TEXT NOT NULL DEFAULT '',
    state            TEXT NOT NULL,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_jobs_task ON jobs(task_id);

CREATE TABLE IF NOT EXISTS artifacts (
    seq        INTEGER PRIMARY KEY AUTOINCREMENT,
    id         TEXT NOT NULL UNIQUE,
    job_id     TEXT NOT NULL REFERENCES jobs(id),
    base_sha   TEXT NOT NULL,
    head_sha   TEXT NOT NULL,
    branch     TEXT NOT NULL,
    verify     TEXT NOT NULL DEFAULT 'pending',
    review     TEXT NOT NULL DEFAULT 'pending',
    integrated_sha TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_artifacts_job ON artifacts(job_id);

-- Append-only event log. Written in the SAME transaction as the state change
-- that produced it. There are no UPDATE/DELETE paths for this table anywhere in
-- the package — history is immutable through the API (§6: a control-plane-
-- managed event log, not cryptographically tamper-proof).
CREATE TABLE IF NOT EXISTS events (
    seq         INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type TEXT NOT NULL,   -- 'task' | 'job' | 'artifact' | 'project' | 'queue' | 'proposal'
    entity_id   TEXT NOT NULL,
    kind        TEXT NOT NULL DEFAULT '',  -- created|transition|budget|rejection|verification|governance
    reason      TEXT NOT NULL DEFAULT '',  -- RejectionReason, when this event is a rejection
    from_state  TEXT NOT NULL DEFAULT '',
    to_state    TEXT NOT NULL DEFAULT '',
    actor       TEXT NOT NULL,
    note        TEXT NOT NULL DEFAULT '',
    at          TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_entity ON events(entity_type, entity_id);

-- The plane-owned integration target: one row per REPOSITORY, holding the commit
-- the plane considers landed. It is deliberately HOST-SIDE state, not a git ref
-- in the project repository: a Job's worktree shares the repo's refs, so any ref
-- living there is writable by the worker, and this row is what the acceptance
-- oracle is read from. Only a completed integration transaction advances it
-- (an atomic compare-and-swap on the sha column), plus an explicit human resync.
--
-- Keyed by canonical repo path, NOT project name: two registry projects can point
-- at one repository, and per-project rows would give them two uncoordinated merge
-- queues on the same trunk. See CanonicalRepoPath.
CREATE TABLE IF NOT EXISTS integration_targets (
    repo_path  TEXT PRIMARY KEY,
    queue_id   TEXT NOT NULL DEFAULT '',
    sha        TEXT NOT NULL,
    adopted_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_targets_queue ON integration_targets(queue_id);

-- Proposals: consequential operations an AGENT caller asked for and a human must
-- confirm (§6's tiered authority). The originating caller class is recorded on
-- the row, because it is what makes "an agent cannot confirm its own proposal"
-- checkable rather than assumed.
CREATE TABLE IF NOT EXISTS proposals (
    seq          INTEGER PRIMARY KEY AUTOINCREMENT,
    id           TEXT NOT NULL UNIQUE,
    operation    TEXT NOT NULL,
    task_id      TEXT NOT NULL DEFAULT '',
    argument     TEXT NOT NULL DEFAULT '',
    proposed_by  TEXT NOT NULL,
    state        TEXT NOT NULL,
    detail       TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_proposals_state ON proposals(state);

-- The cross-project task graph: Task→Task dependencies, spanning projects.
--
-- PLANE-OWNED STATE, like everything else that decides whether work is valid.
-- The edge is never read from a file in a project checkout: an agent that could
-- declare its own dependencies could declare them satisfied, and M15's entire
-- acceptance-oracle argument would be re-opened through a side door.
CREATE TABLE IF NOT EXISTS task_dependencies (
    task_id    TEXT NOT NULL,
    depends_on TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (task_id, depends_on)
);
CREATE INDEX IF NOT EXISTS idx_deps_dependson ON task_dependencies(depends_on);

-- Typed steering: instructions aimed at a RUNNING Job (M17).
--
-- Plane-owned, like the dependency edge: a worker cannot forge one or replay one,
-- which is the whole difference between this and writing into a terminal. The
-- state column is the honest part — an instruction that never reached the worker
-- is recorded undeliverable, because a steering op that reported success without
-- delivering would leave an operator believing they redirected a Job that never
-- heard them.
--
-- (No backticks in this comment: the schema lives in a Go raw string literal, and
-- a backtick here terminates it. That has cost a build once already.)
--
-- Note what is NOT here: nothing on this table is read by verification. Steering
-- changes what the worker is told, never what counts as done.
CREATE TABLE IF NOT EXISTS steering (
    seq          INTEGER PRIMARY KEY AUTOINCREMENT,
    id           TEXT NOT NULL UNIQUE,
    task_id      TEXT NOT NULL,
    job_id       TEXT NOT NULL,
    instruction  TEXT NOT NULL,
    issued_by    TEXT NOT NULL,
    state        TEXT NOT NULL,
    detail       TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    delivered_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_steering_job ON steering(job_id);
CREATE INDEX IF NOT EXISTS idx_steering_task ON steering(task_id);
-- The project-keyed table this replaces existed only on an unreleased development
-- build (it was added and re-keyed within Sprint 59), so there is no shipped data
-- to migrate.
DROP TABLE IF EXISTS targets;
`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("migrating control db: %w", err)
	}
	// Idempotent column additions for DBs created by an earlier schema. New DBs
	// already have these from the CREATE above, so the ALTER is skipped.
	if err := s.addColumnIfMissing("tasks", "acceptance_hash", "acceptance_hash TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.addColumnIfMissing("tasks", "image_digest", "image_digest TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	// Sprint 58 (governance): the Task's authoritative budget, and the event log's
	// type + machine-readable rejection reason. Additive and idempotent, so a
	// v0.49.0 control.db opens, migrates, and keeps every existing row (legacy
	// tasks read back with DefaultBudget(); legacy events with a derived kind).
	if err := s.addColumnIfMissing("tasks", "task_checks", "task_checks TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.addColumnIfMissing("tasks", "budget", "budget TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.addColumnIfMissing("events", "kind", "kind TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.addColumnIfMissing("events", "reason", "reason TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	// Sprint 59 (integration): the commit an artifact actually landed as, after
	// being rebased onto the target and re-verified in that form.
	if err := s.addColumnIfMissing("artifacts", "integrated_sha", "integrated_sha TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	// Sprint 60: the opaque queue id, so the append-only event log stops carrying
	// absolute host paths as entity ids.
	if err := s.addColumnIfMissing("integration_targets", "queue_id", "queue_id TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.backfillQueueIDs(); err != nil {
		return err
	}
	// #77: where a Job's own output was recorded. Additive with an empty default,
	// which reads correctly for every Job that ran before there was one — those
	// genuinely have no log, and "" is exactly that claim.
	if err := s.addColumnIfMissing("jobs", "log_path", "log_path TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return nil
}

// backfillQueueIDs fills the queue id for rows written before the column existed
// (unreleased Sprint-59 development builds). It is derived from the stored path,
// so it is idempotent and needs no external input.
func (s *Store) backfillQueueIDs() error {
	rows, err := s.db.Query(`SELECT repo_path FROM integration_targets WHERE queue_id = ''`)
	if err != nil {
		return fmt.Errorf("finding targets without a queue id: %w", err)
	}
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return err
		}
		paths = append(paths, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range paths {
		if _, err := s.db.Exec(`UPDATE integration_targets SET queue_id = ? WHERE repo_path = ?`,
			QueueIDFor(p), p); err != nil {
			return fmt.Errorf("backfilling queue id for %s: %w", p, err)
		}
	}
	return nil
}

// addColumnIfMissing adds a column via ALTER TABLE only when it is not already
// present (checked via PRAGMA table_info). SQLite has no ADD COLUMN IF NOT
// EXISTS, so this makes the migration idempotent across daemon restarts.
func (s *Store) addColumnIfMissing(table, column, ddl string) error {
	rows, err := s.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return fmt.Errorf("inspecting %s columns: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid, notnull, pk int
			name, ctype      string
			dfltValue        any
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			return err
		}
		if name == column {
			return nil // already present
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := s.db.Exec("ALTER TABLE " + table + " ADD COLUMN " + ddl); err != nil {
		return fmt.Errorf("adding column %s.%s: %w", table, column, err)
	}
	return nil
}

// nextID allocates the next "<prefix>-<n>" id for a table within tx, using the
// AUTOINCREMENT seq high-water mark so ids are short, sortable, and never
// reused. Called inside the same tx as the insert so the id is race-free under
// the single-writer connection.
func nextID(tx *sql.Tx, table, prefix string) (string, error) {
	// sqlite_sequence tracks the max AUTOINCREMENT value ever used per table,
	// surviving deletes — so ids stay monotonic. Missing row (no inserts yet)
	// means start at 1.
	var n int64
	err := tx.QueryRow(
		`SELECT COALESCE((SELECT seq FROM sqlite_sequence WHERE name = ?), 0)`, table,
	).Scan(&n)
	if err != nil {
		return "", fmt.Errorf("allocating id for %s: %w", table, err)
	}
	return fmt.Sprintf("%s-%d", prefix, n+1), nil
}

// logEvent appends an immutable row to the events table within tx. INSERT is the
// only statement this package ever runs against `events` — there is no update or
// delete path anywhere (asserted by a test), which is exactly what
// "control-plane-managed, immutable through the API" means (§6). It is NOT
// cryptographically tamper-proof: anyone with the DB file can still edit it;
// hash-chaining stays an optional later property.
func (s *Store) logEvent(tx *sql.Tx, entityType, entityID string, from, to State, meta EventMeta, actor, note string) error {
	kind := meta.Kind
	if kind == "" {
		kind = EventTransition
	}
	if meta.Actor != "" {
		actor = meta.Actor
	}
	_, err := tx.Exec(
		`INSERT INTO events (entity_type, entity_id, kind, reason, from_state, to_state, actor, note, at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entityType, entityID, kind, string(meta.Reason), string(from), string(to), actor, note, s.now(),
	)
	if err != nil {
		return fmt.Errorf("logging event: %w", err)
	}
	return nil
}

// LogDecision appends a standalone event that accompanies NO state change — the
// record of a decision the plane made about a request (a budget refusal, a
// policy rejection). Governance that leaves no trace is not governance, so every
// refusal is written even though nothing moved.
func (s *Store) LogDecision(entityType, entityID string, meta EventMeta, note string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.logEvent(tx, entityType, entityID, "", "", meta, ActorPlane, note); err != nil {
		return err
	}
	return tx.Commit()
}

// ---- Tasks -------------------------------------------------------------------

// NewTask describes the caller-supplied fields of a task to insert; the store
// assigns the id, state, and timestamps. Grouped into a struct because the
// governance budget pushed the positional form past readability.
type NewTask struct {
	Project        string
	Objective      string
	AcceptanceRef  string
	BaseSHA        string   // git HEAD captured by the caller
	AcceptanceHash string   // frozen verify policy at BaseSHA (may be "")
	Checks         []string // per-task acceptance commands, appended to the frozen policy
	Budget         Budget   // the resolved governance envelope (§6)
}

// CreateTask inserts a new task in the given initial state (planned or queued)
// and logs a creation event, atomically.
//
// Since Sprint 61 a project may have SEVERAL active Tasks. The old
// one-active-task-per-project refusal is gone: how much runs at once is now the
// scheduler's decision (scheduler.go), taken at DISPATCH — where capacity is
// actually consumed — rather than at create, where it only prevented planning
// ahead. Creating work you cannot run yet is useful; starting it is what needs a
// limit.
func (s *Store) CreateTask(spec NewTask, initial State) (Task, error) {
	if !validState(initial) {
		return Task{}, fmt.Errorf("control: invalid initial state %q", initial)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()

	id, err := nextID(tx, "tasks", "T")
	if err != nil {
		return Task{}, err
	}
	now := s.now()
	t := Task{
		ID: id, Project: spec.Project, Objective: spec.Objective,
		AcceptanceRef: spec.AcceptanceRef, AcceptanceHash: spec.AcceptanceHash,
		Checks: spec.Checks,
		Budget: spec.Budget, BaseSHA: spec.BaseSHA, State: initial,
		CreatedAt: now, UpdatedAt: now,
	}
	budgetJSON, err := json.Marshal(t.Budget)
	if err != nil {
		return Task{}, fmt.Errorf("encoding budget: %w", err)
	}
	checksJSON := ""
	if len(t.Checks) > 0 {
		b, err := json.Marshal(t.Checks)
		if err != nil {
			return Task{}, fmt.Errorf("encoding task checks: %w", err)
		}
		checksJSON = string(b)
	}
	_, err = tx.Exec(
		`INSERT INTO tasks (id, project, objective, acceptance_ref, acceptance_hash, task_checks, budget, base_sha, state, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Project, t.Objective, t.AcceptanceRef, t.AcceptanceHash, checksJSON, string(budgetJSON), t.BaseSHA, string(t.State), t.CreatedAt, t.UpdatedAt,
	)
	if err != nil {
		return Task{}, fmt.Errorf("inserting task: %w", err)
	}
	if err := s.logEvent(tx, "task", t.ID, "", t.State, EventMeta{Kind: EventCreated}, ActorSystem,
		"created ("+t.Budget.String()+")"); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return t, nil
}

// GetTask returns the task with the given id, or ErrNotFound.
func (s *Store) GetTask(id string) (Task, error) {
	return scanTask(s.db.QueryRow(taskSelect+` WHERE id = ?`, id))
}

// SetTaskImageDigest records the pinned image digest on a task (captured at
// create or first verify). Derived environment state, not part of the append-only
// event log, so a plain UPDATE is correct.
func (s *Store) SetTaskImageDigest(id, digest string) (Task, error) {
	res, err := s.db.Exec(
		`UPDATE tasks SET image_digest = ?, updated_at = ? WHERE id = ?`,
		digest, s.now(), id,
	)
	if err != nil {
		return Task{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return Task{}, fmt.Errorf("%w: task %s", ErrNotFound, id)
	}
	return s.GetTask(id)
}

// ListTasks returns all tasks ordered by creation (seq) ascending.
func (s *Store) ListTasks() ([]Task, error) {
	rows, err := s.db.Query(taskSelect + ` ORDER BY seq ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ActiveTaskForProject returns one of the project's non-terminal tasks, if any.
//
// With several Tasks now permitted per project this is "any active task", not
// "the active task" — it survives as a cheap existence check, and callers that
// need them all should use ListTasks.
func (s *Store) ActiveTaskForProject(project string) (Task, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Task{}, false, err
	}
	defer tx.Rollback()
	return activeTaskTx(tx, project)
}

// activeTaskTx finds a non-terminal task for project within tx. Terminal states
// are excluded in SQL so the check is a single indexed scan.
func activeTaskTx(tx *sql.Tx, project string) (Task, bool, error) {
	row := tx.QueryRow(
		taskSelect+` WHERE project = ? AND state NOT IN (?, ?, ?, ?) ORDER BY seq ASC LIMIT 1`,
		project, string(StateIntegrated), string(StateFailed), string(StateCancelled), string(StateExpired),
	)
	t, err := scanTask(row)
	if errors.Is(err, ErrNotFound) {
		return Task{}, false, nil
	}
	if err != nil {
		return Task{}, false, err
	}
	return t, true, nil
}

// TransitionTask atomically moves a task from its current state to `to`,
// rejecting the move if it is illegal for the actor (byWorker) or if the row's
// state is not what the state machine expects (optimistic concurrency: the
// UPDATE carries a WHERE state=<legal-from> so a stale/racing row affects 0
// rows). The state-change event is written in the same transaction; on any
// rejection nothing is written.
func (s *Store) TransitionTask(id string, to State, byWorker bool, note string) (Task, error) {
	return s.TransitionTaskWith(id, to, byWorker, EventMeta{}, note)
}

// TransitionTaskWith is TransitionTask with an explicit event annotation (kind,
// rejection reason, actor label). The annotation affects only what is written to
// the log — legality is still decided by CanTransitionBy.
func (s *Store) TransitionTaskWith(id string, to State, byWorker bool, meta EventMeta, note string) (Task, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()

	cur, err := scanTask(tx.QueryRow(taskSelect+` WHERE id = ?`, id))
	if err != nil {
		return Task{}, err
	}
	if !CanTransitionBy(cur.State, to, byWorker) {
		return Task{}, fmt.Errorf("%w: task %s %s → %s (byWorker=%v)", ErrIllegalTransition, id, cur.State, to, byWorker)
	}
	now := s.now()
	res, err := tx.Exec(
		`UPDATE tasks SET state = ?, updated_at = ? WHERE id = ? AND state = ?`,
		string(to), now, id, string(cur.State),
	)
	if err != nil {
		return Task{}, fmt.Errorf("transitioning task: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Task{}, err
	}
	if n != 1 {
		return Task{}, fmt.Errorf("%w: task %s expected state %s", ErrConflict, id, cur.State)
	}
	actor := ActorPlane
	if byWorker {
		actor = ActorWorker
	}
	if err := s.logEvent(tx, "task", id, cur.State, to, meta, actor, note); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	cur.State = to
	cur.UpdatedAt = now
	return cur, nil
}

// ReplanTask atomically revises a rejected task's objective and moves it back to
// `planned` — the replan half of §6's "retry/replan" ladder. Objective and state
// change together in one transaction so a task can never rest in `planned` with
// the stale objective that was just rejected. The Job chain is untouched:
// attempt history is preserved, never overwritten.
func (s *Store) ReplanTask(id, objective string, meta EventMeta, note string) (Task, error) {
	if objective == "" {
		return Task{}, fmt.Errorf("control: replan requires a new objective")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()

	cur, err := scanTask(tx.QueryRow(taskSelect+` WHERE id = ?`, id))
	if err != nil {
		return Task{}, err
	}
	if !CanTransition(cur.State, StatePlanned) {
		return Task{}, fmt.Errorf("%w: task %s %s → %s (replan)", ErrIllegalTransition, id, cur.State, StatePlanned)
	}
	now := s.now()
	res, err := tx.Exec(
		`UPDATE tasks SET objective = ?, state = ?, updated_at = ? WHERE id = ? AND state = ?`,
		objective, string(StatePlanned), now, id, string(cur.State),
	)
	if err != nil {
		return Task{}, fmt.Errorf("replanning task: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return Task{}, err
	} else if n != 1 {
		return Task{}, fmt.Errorf("%w: task %s expected state %s", ErrConflict, id, cur.State)
	}
	if err := s.logEvent(tx, "task", id, cur.State, StatePlanned, meta, ActorPlane, note); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	cur.Objective = objective
	cur.State = StatePlanned
	cur.UpdatedAt = now
	return cur, nil
}

// RebaseTask re-pins a task to a new base_sha and re-freezes its acceptance hash
// at that commit — the documented remedy for a `stale_base` rejection (§6:
// "rejected, must rebase + re-verify"). It does not change state; the caller
// drives the retry. Recorded as a governance event because re-freezing the
// acceptance oracle is a consequential act: the policy now comes from a *newer*
// commit, so a human asks for it explicitly (`task retry --rebase`).
func (s *Store) RebaseTask(id, baseSHA, acceptanceHash string, meta EventMeta, note string) (Task, error) {
	if baseSHA == "" {
		return Task{}, fmt.Errorf("control: rebase requires a base sha")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()

	cur, err := scanTask(tx.QueryRow(taskSelect+` WHERE id = ?`, id))
	if err != nil {
		return Task{}, err
	}
	now := s.now()
	res, err := tx.Exec(
		`UPDATE tasks SET base_sha = ?, acceptance_hash = ?, updated_at = ? WHERE id = ?`,
		baseSHA, acceptanceHash, now, id,
	)
	if err != nil {
		return Task{}, fmt.Errorf("rebasing task: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return Task{}, fmt.Errorf("%w: task %s", ErrNotFound, id)
	}
	if err := s.logEvent(tx, "task", id, cur.State, cur.State, meta, ActorPlane, note); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	cur.BaseSHA = baseSHA
	cur.AcceptanceHash = acceptanceHash
	cur.UpdatedAt = now
	return cur, nil
}

const taskSelect = `SELECT id, project, objective, acceptance_ref, acceptance_hash, image_digest, task_checks, budget, base_sha, state, created_at, updated_at FROM tasks`

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanTask(sc rowScanner) (Task, error) {
	var t Task
	var state, budget, checks string
	err := sc.Scan(&t.ID, &t.Project, &t.Objective, &t.AcceptanceRef, &t.AcceptanceHash, &t.ImageDigest, &checks, &budget, &t.BaseSHA, &state, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	t.State = State(state)
	// Per-task checks: absent on every row written before this existed, which reads
	// back as no extra checks — the project policy alone, exactly as before.
	if checks != "" {
		var c []string
		if json.Unmarshal([]byte(checks), &c) == nil {
			t.Checks = c
		}
	}
	// A task written before Sprint 58 (or by a migration default) carries no
	// budget; the built-in default is the only honest answer for it. Unparseable
	// JSON degrades the same way rather than making the row unreadable — a
	// governance record must never lock a task out of its own history.
	t.Budget = DefaultBudget()
	if budget != "" {
		var b Budget
		if json.Unmarshal([]byte(budget), &b) == nil {
			// sanitized is the backstop for a row that reached the DB without passing
			// Service.resolveBudget (a hand-edited control.db, an older writer): a
			// negative axis reads as "unbounded" at every enforcement site, so it is
			// replaced with the built-in default rather than honoured.
			t.Budget = b.sanitized(DefaultBudget())
		}
	}
	return t, nil
}

// CountTaskTransitionsTo counts how many times a task has entered state `to`,
// read from the append-only event log — the log is the authority for a task's
// history, so a derived counter cannot drift from the events that justify it.
func (s *Store) CountTaskTransitionsTo(taskID string, to State) (int, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM events WHERE entity_type = 'task' AND entity_id = ? AND to_state = ? AND from_state != ''`,
		taskID, string(to),
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting transitions of %s to %s: %w", taskID, to, err)
	}
	return n, nil
}

// CountReviewRuns returns how many independent-review passes a task has had,
// counted from the event log. Kept separate from CountReviewCycles (verification
// cycles): the same LIMIT applies to both, but they are not summed, so a task
// gets N verifications and N reviews rather than N of the two combined.
func (s *Store) CountReviewRuns(taskID string) (int, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM events WHERE entity_type = 'task' AND entity_id = ? AND kind = ?`,
		taskID, EventReview,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting review runs of %s: %w", taskID, err)
	}
	return n, nil
}

// CountReviewCycles returns how many review cycles a task has actually consumed:
// entries into `verifying`, MINUS the ones that were recovered back to
// `candidate` without a verdict.
//
// The subtraction is the point. Entering `verifying` is not the same as being
// verified: a daemon crash or an aborted verifier leaves the artifact unexamined,
// and because the event log is append-only that entry can never be erased. Left
// uncorrected, a crash would permanently spend a cycle of a budget for work that
// never happened.
func (s *Store) CountReviewCycles(taskID string) (int, error) {
	var entered, recovered int
	err := s.db.QueryRow(
		`SELECT
		   (SELECT COUNT(*) FROM events
		     WHERE entity_type = 'task' AND entity_id = ? AND to_state = ? AND from_state != ''),
		   (SELECT COUNT(*) FROM events
		     WHERE entity_type = 'task' AND entity_id = ? AND from_state = ? AND to_state = ?)`,
		taskID, string(StateVerifying),
		taskID, string(StateVerifying), string(StateCandidate),
	).Scan(&entered, &recovered)
	if err != nil {
		return 0, fmt.Errorf("counting review cycles of %s: %w", taskID, err)
	}
	if n := entered - recovered; n > 0 {
		return n, nil
	}
	return 0, nil
}

// ---- Jobs --------------------------------------------------------------------

// CreateJob inserts a new job for a task in the given initial state and logs a
// creation event, atomically. The task must exist.
func (s *Store) CreateJob(taskID, baseSHA, runner string, budget int, initial State) (Job, error) {
	if !validState(initial) {
		return Job{}, fmt.Errorf("control: invalid initial state %q", initial)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()

	if _, err := scanTask(tx.QueryRow(taskSelect+` WHERE id = ?`, taskID)); err != nil {
		return Job{}, err
	}
	id, err := nextID(tx, "jobs", "J")
	if err != nil {
		return Job{}, err
	}
	now := s.now()
	j := Job{
		ID: id, TaskID: taskID, BaseSHA: baseSHA, Runner: runner, Budget: budget,
		ExecutionResult: ExecNone, State: initial, CreatedAt: now, UpdatedAt: now,
	}
	_, err = tx.Exec(
		`INSERT INTO jobs (id, task_id, base_sha, runner, budget, execution_result, output_snapshot, state, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ID, j.TaskID, j.BaseSHA, j.Runner, j.Budget, string(j.ExecutionResult), j.OutputSnapshot, string(j.State), j.CreatedAt, j.UpdatedAt,
	)
	if err != nil {
		return Job{}, fmt.Errorf("inserting job: %w", err)
	}
	if err := s.logEvent(tx, "job", j.ID, "", j.State, EventMeta{Kind: EventCreated}, ActorSystem, "created"); err != nil {
		return Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	return j, nil
}

// GetJob returns the job with the given id, or ErrNotFound.
func (s *Store) GetJob(id string) (Job, error) {
	return scanJob(s.db.QueryRow(jobSelect+` WHERE id = ?`, id))
}

// ListJobsForTask returns a task's jobs ordered by creation.
func (s *Store) ListJobsForTask(taskID string) ([]Job, error) {
	rows, err := s.db.Query(jobSelect+` WHERE task_id = ? ORDER BY seq ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ListActiveJobs returns all non-terminal jobs across every task, ordered by
// creation (seq). Used by the reconcile loop to compare desired state (these
// jobs believe they are in flight) against observed reality.
func (s *Store) ListActiveJobs() ([]Job, error) {
	rows, err := s.db.Query(
		jobSelect+` WHERE state NOT IN (?, ?, ?, ?) ORDER BY seq ASC`,
		string(StateIntegrated), string(StateFailed), string(StateCancelled), string(StateExpired),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// CountJobsForTask returns how many Jobs a task has ever had — the attempt
// counter the max-attempts budget is enforced against (§6). Counting rows rather
// than keeping a mutable counter is deliberate: the Job chain is append-only, so
// the count cannot be laundered by a retry or a replan.
func (s *Store) CountJobsForTask(taskID string) (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE task_id = ?`, taskID).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting jobs for %s: %w", taskID, err)
	}
	return n, nil
}

// CountRunningJobsForProject returns how many of a project's Jobs are currently
// *running* — occupying a runner slot (queued / working / input_required). It
// deliberately excludes `candidate`, `verifying`, and `rejected`: those are
// non-terminal but idle, awaiting a plane or human decision, and counting them
// would make a legitimate retry look like a concurrency breach.
func (s *Store) CountRunningJobsForProject(project string) (int, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM jobs j JOIN tasks t ON t.id = j.task_id
		 WHERE t.project = ? AND j.state IN (?, ?, ?)`,
		project, string(StateQueued), string(StateWorking), string(StateInputRequired),
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting running jobs for %s: %w", project, err)
	}
	return n, nil
}

// CountRunningJobs returns how many Jobs are running across EVERY project — the
// host-wide figure the scheduler's global limit is checked against, since each
// running Job is a container on this machine.
func (s *Store) CountRunningJobs() (int, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM jobs WHERE state IN (?, ?, ?)`,
		string(StateQueued), string(StateWorking), string(StateInputRequired),
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting running jobs: %w", err)
	}
	return n, nil
}

// SetJobExecutionResult records how a job's run ended plus the committed tree
// snapshot (head_sha), captured even on failure/timeout as a salvage snapshot
// (§5). It does not change job state — that is a separate transition.
func (s *Store) SetJobExecutionResult(id string, result ExecutionResult, outputSnapshot string) (Job, error) {
	if !IsValidExecutionResult(result) {
		return Job{}, fmt.Errorf("control: invalid execution_result %q", result)
	}
	now := s.now()
	res, err := s.db.Exec(
		`UPDATE jobs SET execution_result = ?, output_snapshot = ?, updated_at = ? WHERE id = ?`,
		string(result), outputSnapshot, now, id,
	)
	if err != nil {
		return Job{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return Job{}, fmt.Errorf("%w: job %s", ErrNotFound, id)
	}
	return s.GetJob(id)
}

// SetJobLogPath records where this Job's own output was written (#77). Separate
// from SetJobExecutionResult because the two answer different questions and are
// not always both knowable: a Job that timed out has a log but no outcome its
// runner ever reported, and a Job the reconcile loop fails posthumously has an
// outcome but no log this process wrote. Like SetJobExecutionResult it does not
// touch state.
func (s *Store) SetJobLogPath(id, logPath string) (Job, error) {
	now := s.now()
	res, err := s.db.Exec(
		`UPDATE jobs SET log_path = ?, updated_at = ? WHERE id = ?`, logPath, now, id)
	if err != nil {
		return Job{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return Job{}, fmt.Errorf("%w: job %s", ErrNotFound, id)
	}
	return s.GetJob(id)
}

// TransitionJob atomically moves a job's state, mirroring TransitionTask.
func (s *Store) TransitionJob(id string, to State, byWorker bool, note string) (Job, error) {
	return s.TransitionJobWith(id, to, byWorker, EventMeta{}, note)
}

// TransitionJobWith is TransitionJob with an explicit event annotation, mirroring
// TransitionTaskWith. The annotation is log-only; legality is unaffected.
func (s *Store) TransitionJobWith(id string, to State, byWorker bool, meta EventMeta, note string) (Job, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()

	cur, err := scanJob(tx.QueryRow(jobSelect+` WHERE id = ?`, id))
	if err != nil {
		return Job{}, err
	}
	if !CanTransitionBy(cur.State, to, byWorker) {
		return Job{}, fmt.Errorf("%w: job %s %s → %s (byWorker=%v)", ErrIllegalTransition, id, cur.State, to, byWorker)
	}
	now := s.now()
	res, err := tx.Exec(
		`UPDATE jobs SET state = ?, updated_at = ? WHERE id = ? AND state = ?`,
		string(to), now, id, string(cur.State),
	)
	if err != nil {
		return Job{}, fmt.Errorf("transitioning job: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return Job{}, err
	} else if n != 1 {
		return Job{}, fmt.Errorf("%w: job %s expected state %s", ErrConflict, id, cur.State)
	}
	actor := ActorPlane
	if byWorker {
		actor = ActorWorker
	}
	if err := s.logEvent(tx, "job", id, cur.State, to, meta, actor, note); err != nil {
		return Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	cur.State = to
	cur.UpdatedAt = now
	return cur, nil
}

const jobSelect = `SELECT id, task_id, base_sha, runner, budget, execution_result, output_snapshot, log_path, state, created_at, updated_at FROM jobs`

func scanJob(sc rowScanner) (Job, error) {
	var j Job
	var state, execResult string
	err := sc.Scan(&j.ID, &j.TaskID, &j.BaseSHA, &j.Runner, &j.Budget, &execResult, &j.OutputSnapshot, &j.LogPath, &state, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	j.State = State(state)
	j.ExecutionResult = ExecutionResult(execResult)
	return j, nil
}

// ---- Artifacts ---------------------------------------------------------------

// CreateArtifact inserts a new artifact for a job and logs a creation event,
// atomically. The job must exist. verify/review default to pending.
func (s *Store) CreateArtifact(jobID, baseSHA, headSHA, branch string) (Artifact, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Artifact{}, err
	}
	defer tx.Rollback()

	if _, err := scanJob(tx.QueryRow(jobSelect+` WHERE id = ?`, jobID)); err != nil {
		return Artifact{}, err
	}
	id, err := nextID(tx, "artifacts", "A")
	if err != nil {
		return Artifact{}, err
	}
	now := s.now()
	a := Artifact{
		ID: id, JobID: jobID, BaseSHA: baseSHA, HeadSHA: headSHA, Branch: branch,
		Verify: VerifyPending, Review: ReviewPending, CreatedAt: now, UpdatedAt: now,
	}
	_, err = tx.Exec(
		`INSERT INTO artifacts (id, job_id, base_sha, head_sha, branch, verify, review, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.JobID, a.BaseSHA, a.HeadSHA, a.Branch, string(a.Verify), string(a.Review), a.CreatedAt, a.UpdatedAt,
	)
	if err != nil {
		return Artifact{}, fmt.Errorf("inserting artifact: %w", err)
	}
	if err := s.logEvent(tx, "artifact", a.ID, "", "", EventMeta{Kind: EventCreated}, ActorSystem, "created"); err != nil {
		return Artifact{}, err
	}
	if err := tx.Commit(); err != nil {
		return Artifact{}, err
	}
	return a, nil
}

// GetArtifact returns the artifact with the given id, or ErrNotFound.
func (s *Store) GetArtifact(id string) (Artifact, error) {
	return scanArtifact(s.db.QueryRow(artifactSelect+` WHERE id = ?`, id))
}

// SetArtifactVerify records the independent-verification outcome on an artifact
// (§6). It is derived state, not part of the append-only event log, so a plain
// UPDATE is correct.
func (s *Store) SetArtifactVerify(id string, status VerifyStatus) (Artifact, error) {
	res, err := s.db.Exec(
		`UPDATE artifacts SET verify = ?, updated_at = ? WHERE id = ?`,
		string(status), s.now(), id,
	)
	if err != nil {
		return Artifact{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return Artifact{}, fmt.Errorf("%w: artifact %s", ErrNotFound, id)
	}
	return s.GetArtifact(id)
}

// SetArtifactReview records the independent-review outcome on an artifact (§6).
func (s *Store) SetArtifactReview(id string, status ReviewStatus) (Artifact, error) {
	res, err := s.db.Exec(
		`UPDATE artifacts SET review = ?, updated_at = ? WHERE id = ?`,
		string(status), s.now(), id,
	)
	if err != nil {
		return Artifact{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return Artifact{}, fmt.Errorf("%w: artifact %s", ErrNotFound, id)
	}
	return s.GetArtifact(id)
}

// SetArtifactIntegrated records the commit an artifact landed as — the rebased,
// re-verified result, which is not its original head.
func (s *Store) SetArtifactIntegrated(id, mergedSHA string) (Artifact, error) {
	res, err := s.db.Exec(
		`UPDATE artifacts SET integrated_sha = ?, updated_at = ? WHERE id = ?`,
		mergedSHA, s.now(), id,
	)
	if err != nil {
		return Artifact{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return Artifact{}, fmt.Errorf("%w: artifact %s", ErrNotFound, id)
	}
	return s.GetArtifact(id)
}

// ListArtifactsForJob returns a job's artifacts ordered by creation.
func (s *Store) ListArtifactsForJob(jobID string) ([]Artifact, error) {
	rows, err := s.db.Query(artifactSelect+` WHERE job_id = ? ORDER BY seq ASC`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Artifact
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

const artifactSelect = `SELECT id, job_id, base_sha, head_sha, branch, verify, review, integrated_sha, created_at, updated_at FROM artifacts`

func scanArtifact(sc rowScanner) (Artifact, error) {
	var a Artifact
	var verify, review string
	err := sc.Scan(&a.ID, &a.JobID, &a.BaseSHA, &a.HeadSHA, &a.Branch, &verify, &review, &a.IntegratedSHA, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Artifact{}, ErrNotFound
	}
	if err != nil {
		return Artifact{}, err
	}
	a.Verify = VerifyStatus(verify)
	a.Review = ReviewStatus(review)
	return a, nil
}

// ---- Events ------------------------------------------------------------------

// Event is one row of the control-plane-managed event log (§6): every
// transition, budget decision, rejection, and verification outcome, with the
// actor that drove it. It is append-only and immutable **through the API** — the
// package has no update or delete path for the table — which is NOT the same as
// cryptographically tamper-proof; hash-chaining remains an optional later
// property, and nothing here claims otherwise.
type Event struct {
	Seq        int64           `json:"seq"`
	EntityType string          `json:"entityType"`
	EntityID   string          `json:"entityId"`
	Kind       string          `json:"kind"`
	Reason     RejectionReason `json:"reason,omitempty"`
	From       State           `json:"from"`
	To         State           `json:"to"`
	Actor      string          `json:"actor"`
	Note       string          `json:"note"`
	At         string          `json:"at"`
}

// eventSelect is the single column list every event query shares.
const eventSelect = `SELECT seq, entity_type, entity_id, kind, reason, from_state, to_state, actor, note, at FROM events`

// ListEvents returns the full event log ordered by seq (insertion) ascending.
func (s *Store) ListEvents() ([]Event, error) {
	return s.queryEvents(eventSelect + ` ORDER BY seq ASC`)
}

// ListEventsFor returns the event log for a single entity, ordered by seq.
func (s *Store) ListEventsFor(entityType, entityID string) ([]Event, error) {
	return s.queryEvents(eventSelect+` WHERE entity_type = ? AND entity_id = ? ORDER BY seq ASC`,
		entityType, entityID)
}

// ListEventsForTask returns the whole chain for a task — its own events plus
// those of every Job it ever had and every Artifact those Jobs produced —
// ordered by seq, i.e. exactly the order things happened. This is what
// `daedalus task events <id>` renders.
func (s *Store) ListEventsForTask(taskID string) ([]Event, error) {
	return s.queryEvents(eventSelect+`
		 WHERE (entity_type = 'task' AND entity_id = ?)
		    OR (entity_type = 'job' AND entity_id IN (SELECT id FROM jobs WHERE task_id = ?))
		    OR (entity_type = 'artifact' AND entity_id IN (
		          SELECT id FROM artifacts WHERE job_id IN (SELECT id FROM jobs WHERE task_id = ?)))
		 ORDER BY seq ASC`, taskID, taskID, taskID)
}

// queryEvents runs an event query and scans the rows, deriving a kind for rows
// written before the `kind` column existed (a create has no from-state).
func (s *Store) queryEvents(query string, args ...any) ([]Event, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var from, to, reason string
		if err := rows.Scan(&e.Seq, &e.EntityType, &e.EntityID, &e.Kind, &reason, &from, &to, &e.Actor, &e.Note, &e.At); err != nil {
			return nil, err
		}
		e.From, e.To = State(from), State(to)
		e.Reason = RejectionReason(reason)
		if e.Kind == "" {
			e.Kind = EventTransition
			if from == "" {
				e.Kind = EventCreated
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- Targets -------------------------------------------------------------------

// Target is the plane-owned integration ref for a REPOSITORY: the commit the
// control plane considers landed, and the commit a Task's acceptance policy is
// frozen at (docs/guild-master-plan.md §6).
//
// Keyed by repository rather than project so that several registry projects
// pointing at one checkout share a single merge queue (see CanonicalRepoPath).
type Target struct {
	RepoPath string `json:"repoPath"`
	// QueueID is the opaque id used as the event-log entity id and shown to agent
	// callers, so the append-only log never accumulates host paths (QueueIDFor).
	QueueID   string `json:"queueId"`
	SHA       string `json:"sha"`
	AdoptedAt string `json:"adoptedAt"`
	UpdatedAt string `json:"updatedAt"`
}

// GetTarget returns a project's target, or ErrNotFound if none is adopted yet.
func (s *Store) GetTarget(repoPath string) (Target, error) {
	var t Target
	err := s.db.QueryRow(
		`SELECT repo_path, queue_id, sha, adopted_at, updated_at FROM integration_targets WHERE repo_path = ?`, repoPath,
	).Scan(&t.RepoPath, &t.QueueID, &t.SHA, &t.AdoptedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Target{}, fmt.Errorf("%w: no integration target for repository %q", ErrNotFound, repoPath)
	}
	if err != nil {
		return Target{}, err
	}
	return t, nil
}

// AdoptTarget records a project's first target, or returns the existing one
// unchanged if it already has one. Adoption is trust-on-first-use: the plane has
// no landed state for a new project, so it takes the operator's checkout as the
// starting point — ONCE, before any Job for that project has ever run under the
// plane. From then on only AdvanceTarget (a completed integration) or an explicit
// human SetTarget moves it.
//
// The insert-or-read is a single transaction, so two concurrent first Tasks
// cannot adopt two different commits.
func (s *Store) AdoptTarget(repoPath, sha string) (Target, error) {
	if sha == "" {
		return Target{}, fmt.Errorf("control: cannot adopt an empty target for %q", repoPath)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Target{}, err
	}
	defer tx.Rollback()

	var existing Target
	err = tx.QueryRow(
		`SELECT repo_path, queue_id, sha, adopted_at, updated_at FROM integration_targets WHERE repo_path = ?`, repoPath,
	).Scan(&existing.RepoPath, &existing.QueueID, &existing.SHA, &existing.AdoptedAt, &existing.UpdatedAt)
	if err == nil {
		return existing, nil // already adopted; never silently re-adopt
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Target{}, err
	}

	now := s.now()
	queueID := QueueIDFor(repoPath)
	if _, err := tx.Exec(
		`INSERT INTO integration_targets (repo_path, queue_id, sha, adopted_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		repoPath, queueID, sha, now, now,
	); err != nil {
		return Target{}, fmt.Errorf("adopting target for %s: %w", repoPath, err)
	}
	// The event log carries the OPAQUE id, never the host path: the log is
	// append-only, so a path written here could never be retracted.
	if err := s.logEvent(tx, "queue", queueID, "", "",
		EventMeta{Kind: EventGovernance}, ActorSystem,
		"integration target adopted at "+sha+" (trust-on-first-use)"); err != nil {
		return Target{}, err
	}
	if err := tx.Commit(); err != nil {
		return Target{}, err
	}
	return Target{RepoPath: repoPath, QueueID: queueID, SHA: sha, AdoptedAt: now, UpdatedAt: now}, nil
}

// AdvanceTarget is the compare-and-swap at the end of the integration
// transaction: it moves a project's target from `from` to `to`, and fails with
// ErrConflict if the row no longer holds `from` — meaning another integration
// landed while this one was rebasing and re-verifying, so this transaction's
// merged result is stale and must be recomputed against the new tip.
//
// This is the only mechanism that advances a target as a consequence of work.
func (s *Store) AdvanceTarget(repoPath, from, to, note string) (Target, error) {
	if to == "" {
		return Target{}, fmt.Errorf("control: cannot advance target of %q to an empty sha", repoPath)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Target{}, err
	}
	defer tx.Rollback()

	now := s.now()
	res, err := tx.Exec(
		`UPDATE integration_targets SET sha = ?, updated_at = ? WHERE repo_path = ? AND sha = ?`,
		to, now, repoPath, from,
	)
	if err != nil {
		return Target{}, fmt.Errorf("advancing target for %s: %w", repoPath, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Target{}, err
	}
	if n != 1 {
		return Target{}, fmt.Errorf("%w: target of %s is no longer %s", ErrConflict, repoPath, from)
	}
	queueID := QueueIDFor(repoPath)
	if err := s.logEvent(tx, "queue", queueID, "", "",
		EventMeta{Kind: EventIntegration}, ActorPlane, note); err != nil {
		return Target{}, err
	}
	if err := tx.Commit(); err != nil {
		return Target{}, err
	}
	return Target{RepoPath: repoPath, QueueID: queueID, SHA: to, UpdatedAt: now}, nil
}

// SetTarget re-points a project's target unconditionally — the human resync for
// commits that landed outside the plane (a developer pushing straight to the
// branch). It is deliberately NOT automatic: an automatic follow would hand the
// acceptance oracle back to whoever can write the repository's refs, which is the
// hole the plane-owned target exists to close. Recorded as a governance event
// with the supplied actor.
func (s *Store) SetTarget(repoPath, sha string, meta EventMeta, note string) (Target, error) {
	if sha == "" {
		return Target{}, fmt.Errorf("control: cannot set an empty target for %q", repoPath)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Target{}, err
	}
	defer tx.Rollback()

	now := s.now()
	queueID := QueueIDFor(repoPath)
	res, err := tx.Exec(`UPDATE integration_targets SET sha = ?, queue_id = ?, updated_at = ? WHERE repo_path = ?`, sha, queueID, now, repoPath)
	if err != nil {
		return Target{}, fmt.Errorf("setting target for %s: %w", repoPath, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		if _, err := tx.Exec(
			`INSERT INTO integration_targets (repo_path, queue_id, sha, adopted_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			repoPath, queueID, sha, now, now,
		); err != nil {
			return Target{}, fmt.Errorf("setting target for %s: %w", repoPath, err)
		}
	}
	if err := s.logEvent(tx, "queue", queueID, "", "", meta, ActorPlane, note); err != nil {
		return Target{}, err
	}
	if err := tx.Commit(); err != nil {
		return Target{}, err
	}
	return Target{RepoPath: repoPath, QueueID: queueID, SHA: sha, AdoptedAt: now, UpdatedAt: now}, nil
}

// ListTargets returns every repository's target, ordered by path.
func (s *Store) ListTargets() ([]Target, error) {
	rows, err := s.db.Query(`SELECT repo_path, queue_id, sha, adopted_at, updated_at FROM integration_targets ORDER BY repo_path ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Target
	for rows.Next() {
		var t Target
		if err := rows.Scan(&t.RepoPath, &t.QueueID, &t.SHA, &t.AdoptedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ---- Proposals -----------------------------------------------------------------

// ProposalState is the lifecycle of a human-confirmed proposal.
type ProposalState string

const (
	// ProposalPending is awaiting a human decision.
	ProposalPending ProposalState = "pending"
	// ProposalConfirmed was confirmed by a human and the operation ran.
	ProposalConfirmed ProposalState = "confirmed"
	// ProposalDenied was declined by a human; the operation never ran.
	ProposalDenied ProposalState = "denied"
	// ProposalFailed was confirmed, but the operation itself then failed.
	ProposalFailed ProposalState = "failed"
)

// Proposal is a consequential operation an agent asked for and a human must
// confirm (§6's tiered authority). It is a *record of a request*, never an
// authorisation: nothing executes because a proposal exists.
type Proposal struct {
	ID        string `json:"id"` // e.g. "P-1"
	Operation string `json:"operation"`
	TaskID    string `json:"taskId,omitempty"`
	Argument  string `json:"argument,omitempty"`
	// ProposedBy is the caller CLASS that made the request, derived from the
	// transport (caller.go). It is what makes "an agent cannot confirm its own
	// proposal" a checkable property rather than an assumption.
	ProposedBy CallerClass   `json:"proposedBy"`
	State      ProposalState `json:"state"`
	Detail     string        `json:"detail,omitempty"`
	CreatedAt  string        `json:"createdAt"`
	UpdatedAt  string        `json:"updatedAt"`
}

// CreateProposal records a pending proposal and logs it, atomically.
func (s *Store) CreateProposal(operation, taskID, argument string, by CallerClass) (Proposal, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Proposal{}, err
	}
	defer tx.Rollback()

	id, err := nextID(tx, "proposals", "P")
	if err != nil {
		return Proposal{}, err
	}
	now := s.now()
	p := Proposal{
		ID: id, Operation: operation, TaskID: taskID, Argument: argument,
		ProposedBy: by, State: ProposalPending, CreatedAt: now, UpdatedAt: now,
	}
	if _, err := tx.Exec(
		`INSERT INTO proposals (id, operation, task_id, argument, proposed_by, state, detail, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, '', ?, ?)`,
		p.ID, p.Operation, p.TaskID, p.Argument, string(p.ProposedBy), string(p.State), p.CreatedAt, p.UpdatedAt,
	); err != nil {
		return Proposal{}, fmt.Errorf("inserting proposal: %w", err)
	}
	entityID, entityType := p.TaskID, "task"
	if entityID == "" {
		entityID, entityType = p.ID, "proposal"
	}
	if err := s.logEvent(tx, entityType, entityID, "", "",
		EventMeta{Kind: EventProposal, Actor: string(by)}, string(by),
		fmt.Sprintf("proposal %s: %s (awaiting human confirmation)", p.ID, p.Operation)); err != nil {
		return Proposal{}, err
	}
	if err := tx.Commit(); err != nil {
		return Proposal{}, err
	}
	return p, nil
}

// GetProposal returns a proposal by id, or ErrNotFound.
func (s *Store) GetProposal(id string) (Proposal, error) {
	return scanProposal(s.db.QueryRow(proposalSelect+` WHERE id = ?`, id))
}

// ResolveProposal moves a pending proposal to a terminal state. The transition is
// atomic and optimistic: it only affects a row still in `pending`, so a proposal
// cannot be confirmed twice or confirmed after being denied.
func (s *Store) ResolveProposal(id string, to ProposalState, meta EventMeta, detail string) (Proposal, error) {
	if to == ProposalPending {
		return Proposal{}, fmt.Errorf("control: cannot resolve a proposal back to pending")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Proposal{}, err
	}
	defer tx.Rollback()

	cur, err := scanProposal(tx.QueryRow(proposalSelect+` WHERE id = ?`, id))
	if err != nil {
		return Proposal{}, err
	}
	now := s.now()
	res, err := tx.Exec(
		`UPDATE proposals SET state = ?, detail = ?, updated_at = ? WHERE id = ? AND state = ?`,
		string(to), detail, now, id, string(ProposalPending),
	)
	if err != nil {
		return Proposal{}, fmt.Errorf("resolving proposal: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return Proposal{}, err
	} else if n != 1 {
		return Proposal{}, fmt.Errorf("%w: proposal %s is %s, not pending", ErrConflict, id, cur.State)
	}
	entityID, entityType := cur.TaskID, "task"
	if entityID == "" {
		entityID, entityType = cur.ID, "proposal"
	}
	if err := s.logEvent(tx, entityType, entityID, "", "", meta, ActorHuman,
		fmt.Sprintf("proposal %s (%s) %s: %s", cur.ID, cur.Operation, to, detail)); err != nil {
		return Proposal{}, err
	}
	if err := tx.Commit(); err != nil {
		return Proposal{}, err
	}
	cur.State, cur.Detail, cur.UpdatedAt = to, detail, now
	return cur, nil
}

// MarkProposalFailed records that a CONFIRMED proposal's operation then failed.
//
// It exists because ResolveProposal only moves a row out of `pending` — that
// pending-only CAS is what makes confirmation single-use — so it can never
// record this outcome, and `failed` would be an unreachable state. This moves
// `confirmed → failed` and nothing else, so it cannot be used to re-decide a
// proposal: the human's decision stands, only the outcome is appended.
func (s *Store) MarkProposalFailed(id string, meta EventMeta, detail string) (Proposal, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Proposal{}, err
	}
	defer tx.Rollback()

	cur, err := scanProposal(tx.QueryRow(proposalSelect+` WHERE id = ?`, id))
	if err != nil {
		return Proposal{}, err
	}
	now := s.now()
	res, err := tx.Exec(
		`UPDATE proposals SET state = ?, detail = ?, updated_at = ? WHERE id = ? AND state = ?`,
		string(ProposalFailed), detail, now, id, string(ProposalConfirmed),
	)
	if err != nil {
		return Proposal{}, fmt.Errorf("marking proposal failed: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return Proposal{}, fmt.Errorf("%w: proposal %s is %s, not confirmed", ErrConflict, id, cur.State)
	}
	entityID, entityType := cur.TaskID, "task"
	if entityID == "" {
		entityID, entityType = cur.ID, "proposal"
	}
	if err := s.logEvent(tx, entityType, entityID, "", "", meta, ActorHuman,
		fmt.Sprintf("proposal %s (%s) failed after confirmation: %s", cur.ID, cur.Operation, detail)); err != nil {
		return Proposal{}, err
	}
	if err := tx.Commit(); err != nil {
		return Proposal{}, err
	}
	cur.State, cur.Detail, cur.UpdatedAt = ProposalFailed, detail, now
	return cur, nil
}

// ListProposals returns proposals, optionally filtered to one state.
func (s *Store) ListProposals(state ProposalState) ([]Proposal, error) {
	query, args := proposalSelect+` ORDER BY seq ASC`, []any{}
	if state != "" {
		query = proposalSelect + ` WHERE state = ? ORDER BY seq ASC`
		args = append(args, string(state))
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Proposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

const proposalSelect = `SELECT id, operation, task_id, argument, proposed_by, state, detail, created_at, updated_at FROM proposals`

func scanProposal(sc rowScanner) (Proposal, error) {
	var p Proposal
	var by, state string
	err := sc.Scan(&p.ID, &p.Operation, &p.TaskID, &p.Argument, &by, &state, &p.Detail, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Proposal{}, ErrNotFound
	}
	if err != nil {
		return Proposal{}, err
	}
	p.ProposedBy = parseCallerClass(by)
	p.State = ProposalState(state)
	return p, nil
}
