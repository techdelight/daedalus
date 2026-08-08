// Copyright (C) 2026 Techdelight BV

package control

import (
	"database/sql"
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

// ErrActiveTaskExists is returned by CreateTask when the project already has a
// non-terminal task (the "one active Task/Job per project" invariant, §5).
type ErrActiveTaskExists struct {
	Project    string
	ExistingID string
	State      State
}

func (e *ErrActiveTaskExists) Error() string {
	return fmt.Sprintf("project %q already has an active task %s (state %s); "+
		"finish or cancel it before creating another", e.Project, e.ExistingID, e.State)
}

// Actor labels who drove an event, recorded in the append-only event log.
const (
	ActorPlane  = "control-plane"
	ActorWorker = "worker"
	ActorSystem = "system" // creation, etc.
)

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
    entity_type TEXT NOT NULL,   -- 'task' | 'job' | 'artifact'
    entity_id   TEXT NOT NULL,
    from_state  TEXT NOT NULL DEFAULT '',
    to_state    TEXT NOT NULL DEFAULT '',
    actor       TEXT NOT NULL,
    note        TEXT NOT NULL DEFAULT '',
    at          TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_entity ON events(entity_type, entity_id);
`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("migrating control db: %w", err)
	}
	// Idempotent column additions for DBs created by an earlier schema. New DBs
	// already have these from the CREATE above, so the ALTER is skipped.
	if err := s.addColumnIfMissing("tasks", "acceptance_hash", "acceptance_hash TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
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

// logEvent appends an immutable row to the events table within tx.
func (s *Store) logEvent(tx *sql.Tx, entityType, entityID string, from, to State, actor, note string) error {
	_, err := tx.Exec(
		`INSERT INTO events (entity_type, entity_id, from_state, to_state, actor, note, at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		entityType, entityID, string(from), string(to), actor, note, s.now(),
	)
	if err != nil {
		return fmt.Errorf("logging event: %w", err)
	}
	return nil
}

// ---- Tasks -------------------------------------------------------------------

// CreateTask inserts a new task in the given initial state (planned or queued)
// and logs a creation event, atomically. It enforces the one-active-task-per-
// project invariant. baseSHA is the git HEAD captured by the caller;
// acceptanceHash freezes the verify policy at that sha (may be "").
func (s *Store) CreateTask(project, objective, acceptanceRef, baseSHA, acceptanceHash string, initial State) (Task, error) {
	if !validState(initial) {
		return Task{}, fmt.Errorf("control: invalid initial state %q", initial)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()

	// One active (non-terminal) task per project.
	if existing, ok, err := activeTaskTx(tx, project); err != nil {
		return Task{}, err
	} else if ok {
		return Task{}, &ErrActiveTaskExists{Project: project, ExistingID: existing.ID, State: existing.State}
	}

	id, err := nextID(tx, "tasks", "T")
	if err != nil {
		return Task{}, err
	}
	now := s.now()
	t := Task{
		ID: id, Project: project, Objective: objective,
		AcceptanceRef: acceptanceRef, AcceptanceHash: acceptanceHash, BaseSHA: baseSHA, State: initial,
		CreatedAt: now, UpdatedAt: now,
	}
	_, err = tx.Exec(
		`INSERT INTO tasks (id, project, objective, acceptance_ref, acceptance_hash, base_sha, state, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Project, t.Objective, t.AcceptanceRef, t.AcceptanceHash, t.BaseSHA, string(t.State), t.CreatedAt, t.UpdatedAt,
	)
	if err != nil {
		return Task{}, fmt.Errorf("inserting task: %w", err)
	}
	if err := s.logEvent(tx, "task", t.ID, "", t.State, ActorSystem, "created"); err != nil {
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

// ActiveTaskForProject returns the project's non-terminal task, if any.
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
	if err := s.logEvent(tx, "task", id, cur.State, to, actor, note); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	cur.State = to
	cur.UpdatedAt = now
	return cur, nil
}

const taskSelect = `SELECT id, project, objective, acceptance_ref, acceptance_hash, base_sha, state, created_at, updated_at FROM tasks`

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanTask(sc rowScanner) (Task, error) {
	var t Task
	var state string
	err := sc.Scan(&t.ID, &t.Project, &t.Objective, &t.AcceptanceRef, &t.AcceptanceHash, &t.BaseSHA, &state, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	t.State = State(state)
	return t, nil
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
	if err := s.logEvent(tx, "job", j.ID, "", j.State, ActorSystem, "created"); err != nil {
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

// TransitionJob atomically moves a job's state, mirroring TransitionTask.
func (s *Store) TransitionJob(id string, to State, byWorker bool, note string) (Job, error) {
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
	if err := s.logEvent(tx, "job", id, cur.State, to, actor, note); err != nil {
		return Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	cur.State = to
	cur.UpdatedAt = now
	return cur, nil
}

const jobSelect = `SELECT id, task_id, base_sha, runner, budget, execution_result, output_snapshot, state, created_at, updated_at FROM jobs`

func scanJob(sc rowScanner) (Job, error) {
	var j Job
	var state, execResult string
	err := sc.Scan(&j.ID, &j.TaskID, &j.BaseSHA, &j.Runner, &j.Budget, &execResult, &j.OutputSnapshot, &state, &j.CreatedAt, &j.UpdatedAt)
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
	if err := s.logEvent(tx, "artifact", a.ID, "", "", ActorSystem, "created"); err != nil {
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

const artifactSelect = `SELECT id, job_id, base_sha, head_sha, branch, verify, review, created_at, updated_at FROM artifacts`

func scanArtifact(sc rowScanner) (Artifact, error) {
	var a Artifact
	var verify, review string
	err := sc.Scan(&a.ID, &a.JobID, &a.BaseSHA, &a.HeadSHA, &a.Branch, &verify, &review, &a.CreatedAt, &a.UpdatedAt)
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

// Event is one row of the append-only control-plane event log.
type Event struct {
	Seq        int64  `json:"seq"`
	EntityType string `json:"entityType"`
	EntityID   string `json:"entityId"`
	From       State  `json:"from"`
	To         State  `json:"to"`
	Actor      string `json:"actor"`
	Note       string `json:"note"`
	At         string `json:"at"`
}

// ListEvents returns the full event log ordered by seq (insertion) ascending.
func (s *Store) ListEvents() ([]Event, error) {
	rows, err := s.db.Query(
		`SELECT seq, entity_type, entity_id, from_state, to_state, actor, note, at FROM events ORDER BY seq ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var from, to string
		if err := rows.Scan(&e.Seq, &e.EntityType, &e.EntityID, &from, &to, &e.Actor, &e.Note, &e.At); err != nil {
			return nil, err
		}
		e.From, e.To = State(from), State(to)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListEventsFor returns the event log for a single entity, ordered by seq.
func (s *Store) ListEventsFor(entityType, entityID string) ([]Event, error) {
	rows, err := s.db.Query(
		`SELECT seq, entity_type, entity_id, from_state, to_state, actor, note, at
		 FROM events WHERE entity_type = ? AND entity_id = ? ORDER BY seq ASC`,
		entityType, entityID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var from, to string
		if err := rows.Scan(&e.Seq, &e.EntityType, &e.EntityID, &from, &to, &e.Actor, &e.Note, &e.At); err != nil {
			return nil, err
		}
		e.From, e.To = State(from), State(to)
		out = append(out, e)
	}
	return out, rows.Err()
}
