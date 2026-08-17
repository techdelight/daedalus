// Copyright (C) 2026 Techdelight BV

package control

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// --- the log records every kind of decision -----------------------------------

func TestTaskEvents_RecordsTheWholeChain(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, StubVerifyRunner{Pass: false})
	svc.SetBudgetSource(StaticBudget(Budget{MaxAttempts: 2, MaxReviewCycles: 5, Concurrency: 1}))

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil { // job + artifact + transitions
		t.Fatalf("Dispatch: %v", err)
	}
	if _, err := svc.VerifyTask(task.ID); err != nil { // verification → rejection
		t.Fatalf("Verify: %v", err)
	}
	if _, err := svc.RetryTask(task.ID, RetryRequest{}); err != nil { // governance + attempt 2
		t.Fatalf("Retry: %v", err)
	}
	if _, err := svc.VerifyTask(task.ID); err != nil { // second rejection
		t.Fatalf("Verify 2: %v", err)
	}
	// The third attempt is over the 2-attempt budget: a refusal, on the record.
	var rej *RejectionError
	if _, err := svc.RetryTask(task.ID, RetryRequest{}); !errors.As(err, &rej) {
		t.Fatalf("precondition: the third attempt should be refused, got %v", err)
	}

	events, err := svc.TaskEvents(task.ID)
	if err != nil {
		t.Fatalf("TaskEvents: %v", err)
	}

	// Every entity beneath the task is represented, in one ordered stream.
	seenEntity := map[string]bool{}
	for _, e := range events {
		seenEntity[e.EntityType] = true
	}
	for _, want := range []string{"task", "job", "artifact"} {
		if !seenEntity[want] {
			t.Errorf("event stream is missing %s events", want)
		}
	}
	// Every kind the sprint asks for is present: transitions, a rejection, a
	// budget decision, a governance act.
	for _, kind := range []string{EventCreated, EventTransition, EventRejection, EventBudget, EventGovernance} {
		if !hasKind(events, kind) {
			t.Errorf("event stream is missing kind %q", kind)
		}
	}
	// The rejection carries a machine-readable reason.
	if !hasEvent(events, EventRejection, ReasonVerifyFailed) {
		t.Error("the verification rejection should carry reason verify_failed")
	}
	if !hasEvent(events, EventBudget, ReasonAttemptsExhausted) {
		t.Error("the budget refusal should carry reason attempts_exhausted")
	}
	// Seq is strictly increasing — the log is ordered by insertion, not by clock.
	for i := 1; i < len(events); i++ {
		if events[i].Seq <= events[i-1].Seq {
			t.Fatalf("event seq not increasing at %d: %d <= %d", i, events[i].Seq, events[i-1].Seq)
		}
	}
	// Actors are drawn from the known set, and a human-initiated op is labelled.
	valid := map[string]bool{ActorPlane: true, ActorWorker: true, ActorHuman: true, ActorSystem: true}
	human := false
	for _, e := range events {
		if !valid[e.Actor] {
			t.Errorf("unknown actor %q on event %d", e.Actor, e.Seq)
		}
		if e.Actor == ActorHuman {
			human = true
		}
	}
	if !human {
		t.Error("the retry was a human-initiated op and should be labelled as such")
	}
	// The service view equals the store view (no filtering sleight of hand).
	fromStore, _ := store.ListEventsForTask(task.ID)
	if len(fromStore) != len(events) {
		t.Errorf("service returned %d events, store has %d", len(events), len(fromStore))
	}
}

func TestTaskEvents_UnknownTask(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)
	if _, err := svc.TaskEvents("T-404"); !errors.Is(err, ErrNotFound) {
		t.Errorf("TaskEvents(unknown) err = %v, want ErrNotFound", err)
	}
}

// TestEvents_AppendOnly_HistoryIsNeverRewritten asserts the behavioural half of
// "immutable through the API": every operation only ever ADDS to the log, and no
// earlier row changes — not even when a later decision contradicts an earlier
// one.
func TestEvents_AppendOnly_HistoryIsNeverRewritten(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, StubVerifyRunner{Pass: false})

	task, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	before, _ := store.ListEvents()

	// A rejection, a refused illegal transition, a retry, a cancel.
	if _, err := svc.VerifyTask(task.ID); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if _, err := store.TransitionTask(task.ID, StateVerified, true, "worker self-verify"); err == nil {
		t.Fatal("precondition: a worker must not be able to reach verified")
	}
	if _, err := svc.RetryTask(task.ID, RetryRequest{}); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if _, err := svc.CancelTask(task.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	after, _ := store.ListEvents()
	if len(after) < len(before) {
		t.Fatalf("the log shrank: %d → %d", len(before), len(after))
	}
	// The earlier rows are byte-identical: the prefix is untouched.
	for i := range before {
		if after[i] != before[i] {
			t.Errorf("event %d was rewritten:\n before %+v\n after  %+v", i, before[i], after[i])
		}
	}
	// The rejected illegal transition wrote nothing at all.
	for _, e := range after[len(before):] {
		if e.To == StateVerified {
			t.Errorf("a refused transition leaked into the log: %+v", e)
		}
	}
}

// TestEvents_NoMutationPathInAPI is the structural guard the sprint asks for:
// the TaskAPI surface exposes exactly one event operation, and it reads.
func TestEvents_NoMutationPathInAPI(t *testing.T) {
	iface := reflect.TypeOf((*TaskAPI)(nil)).Elem()
	var eventMethods []string
	for i := 0; i < iface.NumMethod(); i++ {
		if name := iface.Method(i).Name; strings.Contains(name, "Event") {
			eventMethods = append(eventMethods, name)
		}
	}
	if len(eventMethods) != 1 || eventMethods[0] != "TaskEvents" {
		t.Errorf("TaskAPI event methods = %v, want exactly [TaskEvents] (read-only)", eventMethods)
	}
	// TaskEvents must be a pure reader: (id string) → ([]Event, error).
	m, _ := iface.MethodByName("TaskEvents")
	if m.Type.NumIn() != 1 || m.Type.In(0).Kind() != reflect.String {
		t.Errorf("TaskEvents takes %v, want a single id string", m.Type)
	}
	if m.Type.NumOut() != 2 || m.Type.Out(0) != reflect.TypeOf([]Event{}) {
		t.Errorf("TaskEvents returns %v, want ([]Event, error)", m.Type)
	}
}

// TestEvents_NoMutationSQLInPackage scans the package source for any statement
// that would update or delete event rows. It is a deliberately blunt structural
// test: the guarantee in §6 is "no such operation exists", and the cheapest way
// to keep that true as the package grows is to fail the build's tests the moment
// someone writes one.
func TestEvents_NoMutationSQLInPackage(t *testing.T) {
	// Needles are assembled at runtime so this file does not match itself.
	needles := []string{
		"UPDATE " + "events",
		"DELETE " + "FROM events",
		"DROP " + "TABLE events",
		"DROP " + "TABLE IF EXISTS events",
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package dir: %v", err)
	}
	scanned := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(".", e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		body := strings.ToUpper(string(data))
		scanned++
		for _, needle := range needles {
			if strings.Contains(body, strings.ToUpper(needle)) {
				t.Errorf("%s contains %q — the event log has no mutation path (§6)", e.Name(), needle)
			}
		}
	}
	if scanned == 0 {
		t.Fatal("scanned no source files — the guard is not actually running")
	}
}

// TestEvents_NoMutationRouteOnDaemon asserts the same guarantee at the wire
// level: /tasks/{id}/events answers GET and refuses every write verb.
func TestEvents_NoMutationRouteOnDaemon(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{Result: ExecSuccess, WriteFile: true}, nil)
	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	handler := NewServer(svc).Handler()
	path := "/tasks/" + task.ID + "/events"

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		if rec.Code < 400 {
			t.Errorf("%s %s = %d, want a 4xx — the log takes no writes", method, path, rec.Code)
		}
	}
	// GET works and returns the log.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", path, rec.Code)
	}
	if !strings.Contains(rec.Body.String(), string(EventCreated)) {
		t.Errorf("GET %s body has no created event: %s", path, rec.Body.String())
	}
}

// --- migration from a v0.49.0 database ----------------------------------------

// TestMigrate_FromV0_49_0Schema opens a database created with the exact
// pre-Sprint-58 schema (no tasks.budget, no events.kind/reason), with rows in it,
// and asserts the additive migration leaves the existing data intact and usable.
func TestMigrate_FromV0_49_0Schema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")

	// 1. Build a v0.49.0 database by hand and put real rows in it.
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	if _, err := legacy.Exec(v049Schema); err != nil {
		t.Fatalf("v0.49.0 schema: %v", err)
	}
	if _, err := legacy.Exec(`
		INSERT INTO tasks (id, project, objective, acceptance_ref, acceptance_hash, image_digest, base_sha, state, created_at, updated_at)
		VALUES ('T-1', 'legacy', 'old objective', '', 'sha256:frozen', 'sha256:img', 'abc123', 'candidate', '2026-08-01T00:00:00Z', '2026-08-01T00:00:00Z');
		INSERT INTO jobs (id, task_id, base_sha, runner, budget, execution_result, output_snapshot, state, created_at, updated_at)
		VALUES ('J-1', 'T-1', 'abc123', 'claude', 0, 'success', 'def456', 'candidate', '2026-08-01T00:00:00Z', '2026-08-01T00:00:00Z');
		INSERT INTO events (entity_type, entity_id, from_state, to_state, actor, note, at)
		VALUES ('task', 'T-1', '', 'planned', 'system', 'created', '2026-08-01T00:00:00Z'),
		       ('task', 'T-1', 'working', 'candidate', 'control-plane', 'job candidate', '2026-08-01T00:00:01Z');
	`); err != nil {
		t.Fatalf("seed legacy rows: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	// 2. Open it with the current code: the migration must be additive.
	s, err := Open(path, WithClock(fixedClock()))
	if err != nil {
		t.Fatalf("Open(v0.49.0 db): %v", err)
	}
	defer s.Close()

	// 3. The old task is readable, with everything it had, plus a default budget.
	task, err := s.GetTask("T-1")
	if err != nil {
		t.Fatalf("GetTask on migrated db: %v", err)
	}
	if task.Objective != "old objective" || task.AcceptanceHash != "sha256:frozen" ||
		task.ImageDigest != "sha256:img" || task.State != StateCandidate {
		t.Errorf("legacy task lost data: %+v", task)
	}
	if task.Budget != DefaultBudget() {
		t.Errorf("legacy budget = %+v, want DefaultBudget", task.Budget)
	}
	// 4. Old events survive and get a derived kind.
	events, err := s.ListEventsForTask("T-1")
	if err != nil {
		t.Fatalf("ListEventsForTask: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("legacy events = %d, want 2", len(events))
	}
	if events[0].Kind != EventCreated {
		t.Errorf("legacy create event kind = %q, want %q (derived)", events[0].Kind, EventCreated)
	}
	if events[1].Kind != EventTransition {
		t.Errorf("legacy transition kind = %q, want %q (derived)", events[1].Kind, EventTransition)
	}
	if events[1].Reason != ReasonNone {
		t.Errorf("legacy event should have no reason, got %q", events[1].Reason)
	}
	// 4b. The legacy job survives the log_path addition (#77) and reads back
	//     claiming no log — which for a Job that ran before there were any is not
	//     a default standing in for the truth, it IS the truth.
	legacyJob, err := s.GetJob("J-1")
	if err != nil {
		t.Fatalf("GetJob on migrated db: %v", err)
	}
	if legacyJob.OutputSnapshot != "def456" || legacyJob.ExecutionResult != ExecSuccess {
		t.Errorf("legacy job lost data: %+v", legacyJob)
	}
	if legacyJob.LogPath != "" {
		t.Errorf("legacy job log path = %q, want \"\" (it never had one)", legacyJob.LogPath)
	}

	// 5. The migrated database still works for new writes, and ids continue from
	//    the existing high-water mark rather than colliding.
	if _, err := s.TransitionTaskWith("T-1", StateRejected, false,
		EventMeta{Kind: EventRejection, Reason: ReasonVerifyFailed}, "post-migration rejection"); err != nil {
		t.Fatalf("transition on migrated db: %v", err)
	}
	if _, err := s.CreateJob("T-1", "abc123", "claude", 60, StateWorking); err != nil {
		t.Fatalf("CreateJob on migrated db: %v", err)
	}
	events, _ = s.ListEventsForTask("T-1")
	if !hasEvent(events, EventRejection, ReasonVerifyFailed) {
		t.Error("a new typed event should be recorded on the migrated db")
	}
	// 6. Re-opening is idempotent (the ALTERs must not run twice).
	s.Close()
	s2, err := Open(path, WithClock(fixedClock()))
	if err != nil {
		t.Fatalf("re-Open migrated db: %v", err)
	}
	defer s2.Close()
	if _, err := s2.GetTask("T-1"); err != nil {
		t.Errorf("GetTask after re-open: %v", err)
	}
}

// v049Schema is the tasks/jobs/artifacts/events schema exactly as v0.49.0 wrote
// it — no tasks.budget, no events.kind, no events.reason.
const v049Schema = `
CREATE TABLE IF NOT EXISTS tasks (
    seq             INTEGER PRIMARY KEY AUTOINCREMENT,
    id              TEXT NOT NULL UNIQUE,
    project         TEXT NOT NULL,
    objective       TEXT NOT NULL,
    acceptance_ref  TEXT NOT NULL DEFAULT '',
    acceptance_hash TEXT NOT NULL DEFAULT '',
    image_digest    TEXT NOT NULL DEFAULT '',
    base_sha        TEXT NOT NULL,
    state           TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);
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
CREATE TABLE IF NOT EXISTS events (
    seq         INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type TEXT NOT NULL,
    entity_id   TEXT NOT NULL,
    from_state  TEXT NOT NULL DEFAULT '',
    to_state    TEXT NOT NULL DEFAULT '',
    actor       TEXT NOT NULL,
    note        TEXT NOT NULL DEFAULT '',
    at          TEXT NOT NULL
);
`

func hasKind(events []Event, kind string) bool {
	for _, e := range events {
		if e.Kind == kind {
			return true
		}
	}
	return false
}
