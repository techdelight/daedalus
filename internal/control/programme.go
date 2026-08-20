// Copyright (C) 2026 Techdelight BV

package control

// Programmes — the shared intent several projects serve (M20, Sprint 66).
//
// WHY THIS MOVED INTO THE PLANE. The control plane's unit of governance was the
// repository: `Task.Project` is a registry name, the integration target is keyed
// by canonical repo path, and every authoritative row is organised around a
// checkout. A programme is by definition the thing that is NOT a repo, and it
// lived as JSON files under `<data-dir>/programmes` that `internal/control` had
// never once read. Two notions, both called "programme", with nothing joining
// them: a file-backed list of projects and project→project edges, and a board of
// Tasks named `ProgrammeBoard` that has no idea the first exists.
//
// The join is the point. A Task carried a free-text objective and nothing else,
// so "why is this work worth doing" existed only in whoever asked for it. With a
// programme the plane owns, a Task can point at one, and the record can answer
// what a piece of work was in service of — years later, when nobody remembers.
//
// WHY THE PLANE OWNS IT RATHER THAN THE FILES. Identity. A file-backed programme
// has no identity beyond its filename: rename the file and every Task pointing at
// it dangles, silently. That is the same class of defect as keying the
// integration target by project name — which Sprint 59 fixed by keying it to
// something that cannot be edited out from under the thing referring to it. A
// programme gets an id, the id is what a Task stores, and the name is free to
// change.
//
// WHAT WAS DELIBERATELY KEPT. `Deps` — the project→project edges from the file
// store — survive the import rather than being dropped, because they are somebody's
// declared ordering and destroying user data on a migration is not a trade this
// makes. But they are DECLARATIVE and gate nothing: the graph that actually
// blocks work is `task_dependencies` (Sprint 62), Task→Task, and the roll-up in
// ProgrammeStatus reads that one. Two edge kinds is one more than ideal; the
// honest description is that one is a plan and the other is enforcement, and only
// the second has teeth.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/techdelight/daedalus/core"
)

// Programme is a shared intent that several projects serve.
type Programme struct {
	// ID is the stable identity a Task points at — "PR-1". Prefixed PR and not P
	// because proposals already own P, and two id spaces that differ only by
	// context are a trap for whoever reads the event log.
	ID string `json:"id"`
	// Name is human-facing and MUTABLE. It is unique, because it is what a person
	// types at the CLI, but nothing stores it as a reference.
	Name string `json:"name"`
	// Description is the programme's statement of purpose, in the words of whoever
	// formed it. There is deliberately no second "intent" field: one free-text
	// field that means "what this is for" is clearer than two that overlap.
	Description string `json:"description,omitempty"`
	// Projects are the registry names this programme draws on.
	Projects []string `json:"projects"`
	// Deps are declared project→project ordering, imported from the file store.
	// They order a plan; they do not gate execution — see the header note.
	Deps      []core.DependencyEdge `json:"deps,omitempty"`
	CreatedAt string                `json:"createdAt"`
	UpdatedAt string                `json:"updatedAt"`
}

// ErrProgrammeInUse is returned when deleting a programme Tasks still point at.
var ErrProgrammeInUse = errors.New("control: programme still has tasks")

// --- store ------------------------------------------------------------------

// CreateProgramme inserts a programme and returns it with its allocated id.
func (s *Store) CreateProgramme(p Programme) (Programme, error) {
	if strings.TrimSpace(p.Name) == "" {
		return Programme{}, fmt.Errorf("%w: a programme needs a name", ErrInvalidRequest)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Programme{}, err
	}
	defer tx.Rollback()

	id, err := nextID(tx, "programmes", "PR")
	if err != nil {
		return Programme{}, err
	}
	now := s.now()
	p.ID, p.CreatedAt, p.UpdatedAt = id, now, now
	projects, deps, err := encodeProgrammeLists(p)
	if err != nil {
		return Programme{}, err
	}
	if _, err := tx.Exec(
		`INSERT INTO programmes (id, name, description, projects, deps, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Description, projects, deps, p.CreatedAt, p.UpdatedAt,
	); err != nil {
		return Programme{}, fmt.Errorf("inserting programme: %w", err)
	}
	if err := s.logEvent(tx, "programme", p.ID, "", "", EventMeta{Kind: EventCreated}, ActorHuman,
		"programme "+p.Name+" formed"); err != nil {
		return Programme{}, err
	}
	if err := tx.Commit(); err != nil {
		return Programme{}, err
	}
	return p, nil
}

const programmeSelect = `SELECT id, name, description, projects, deps, created_at, updated_at FROM programmes`

func scanProgramme(sc rowScanner) (Programme, error) {
	var p Programme
	var projects, deps string
	err := sc.Scan(&p.ID, &p.Name, &p.Description, &projects, &deps, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Programme{}, ErrNotFound
	}
	if err != nil {
		return Programme{}, err
	}
	// Unparseable lists degrade to empty rather than making the row unreadable —
	// the same rule the budget column follows, and for the same reason: a record
	// must never lock its subject out of its own history.
	if projects != "" {
		_ = json.Unmarshal([]byte(projects), &p.Projects)
	}
	if deps != "" {
		_ = json.Unmarshal([]byte(deps), &p.Deps)
	}
	if p.Projects == nil {
		p.Projects = []string{}
	}
	return p, nil
}

func encodeProgrammeLists(p Programme) (string, string, error) {
	projects := ""
	if len(p.Projects) > 0 {
		b, err := json.Marshal(p.Projects)
		if err != nil {
			return "", "", fmt.Errorf("encoding programme projects: %w", err)
		}
		projects = string(b)
	}
	deps := ""
	if len(p.Deps) > 0 {
		b, err := json.Marshal(p.Deps)
		if err != nil {
			return "", "", fmt.Errorf("encoding programme deps: %w", err)
		}
		deps = string(b)
	}
	return projects, deps, nil
}

// GetProgramme returns a programme by id, or ErrNotFound.
func (s *Store) GetProgramme(id string) (Programme, error) {
	return scanProgramme(s.db.QueryRow(programmeSelect+` WHERE id = ?`, id))
}

// ProgrammeByName returns a programme by its (unique) name, or ErrNotFound.
func (s *Store) ProgrammeByName(name string) (Programme, error) {
	return scanProgramme(s.db.QueryRow(programmeSelect+` WHERE name = ?`, name))
}

// ListProgrammes returns every programme in creation order.
func (s *Store) ListProgrammes() ([]Programme, error) {
	rows, err := s.db.Query(programmeSelect + ` ORDER BY seq ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Programme{}
	for rows.Next() {
		p, err := scanProgramme(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpdateProgramme replaces a programme's mutable fields. The id is not among
// them: it is what Tasks store, and the whole reason the plane owns this.
func (s *Store) UpdateProgramme(p Programme) (Programme, error) {
	projects, deps, err := encodeProgrammeLists(p)
	if err != nil {
		return Programme{}, err
	}
	now := s.now()
	res, err := s.db.Exec(
		`UPDATE programmes SET name = ?, description = ?, projects = ?, deps = ?, updated_at = ? WHERE id = ?`,
		p.Name, p.Description, projects, deps, now, p.ID,
	)
	if err != nil {
		return Programme{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Programme{}, ErrNotFound
	}
	return s.GetProgramme(p.ID)
}

// DeleteProgramme removes a programme, refusing while Tasks still point at it.
//
// The refusal is the point. A deleted programme whose Tasks kept pointing at it
// would reproduce exactly the dangling-reference failure the file store had, and
// the record would answer "what was this in service of" with an id that resolves
// to nothing.
func (s *Store) DeleteProgramme(id string) error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE programme_id = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: %s still has %d task(s)", ErrProgrammeInUse, id, n)
	}
	res, err := s.db.Exec(`DELETE FROM programmes WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return ErrNotFound
	}
	return nil
}

// ImportProgrammes adopts programme definitions that were written before the
// plane owned them, skipping any whose name is already present.
//
// It takes the definitions rather than a directory ON PURPOSE: where they used to
// live is the caller's knowledge (the daemon knows its data dir), and keeping the
// file layout out of this package is what lets the import be tested without a
// filesystem. Idempotent by NAME — running it on every daemon start is a no-op
// after the first, which is what makes it safe to do exactly that.
func (s *Store) ImportProgrammes(defs []core.Programme) (int, error) {
	imported := 0
	for _, d := range defs {
		if strings.TrimSpace(d.Name) == "" {
			continue
		}
		if _, err := s.ProgrammeByName(d.Name); err == nil {
			continue // already adopted
		} else if !errors.Is(err, ErrNotFound) {
			return imported, err
		}
		if _, err := s.CreateProgramme(Programme{
			Name: d.Name, Description: d.Description, Projects: d.Projects, Deps: d.Deps,
		}); err != nil {
			return imported, fmt.Errorf("importing programme %q: %w", d.Name, err)
		}
		imported++
	}
	return imported, nil
}

// SetTaskProgramme points a Task at a programme (or at none, with an empty id).
func (s *Store) SetTaskProgramme(taskID, programmeID string) (Task, error) {
	res, err := s.db.Exec(
		`UPDATE tasks SET programme_id = ?, updated_at = ? WHERE id = ?`,
		programmeID, s.now(), taskID)
	if err != nil {
		return Task{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Task{}, ErrNotFound
	}
	return s.GetTask(taskID)
}

// TasksForProgramme returns the Tasks serving a programme, in creation order.
func (s *Store) TasksForProgramme(id string) ([]Task, error) {
	rows, err := s.db.Query(taskSelect+` WHERE programme_id = ? ORDER BY seq ASC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// --- the roll-up ------------------------------------------------------------

// ProgrammeStatus is a programme seen through the work that serves it.
//
// It is DERIVED, entirely. There is no programme state to store — a programme is
// not in a lifecycle, its Tasks are — so there is nothing here for reconcile to
// repair and nothing that can disagree with the rows it was computed from.
type ProgrammeStatus struct {
	Programme Programme `json:"programme"`
	Tasks     []Task    `json:"tasks"`
	// ByState counts the Tasks serving this programme, so "where is this up to"
	// is one line rather than a scan.
	ByState map[State]int `json:"byState"`
	Open    int           `json:"open"`   // non-terminal Tasks
	Landed  int           `json:"landed"` // Tasks that reached `integrated`
	// External is every dependency edge that LEAVES the programme — work it waits
	// on that nobody put in it.
	//
	// This is the reason the roll-up exists at all. A per-project view cannot show
	// it (the two Tasks are in different projects), and a programme that looks
	// fully staffed while blocked on something outside itself is exactly the
	// situation an operator needs told rather than left to discover.
	External []ExternalDependency `json:"external,omitempty"`
}

// ExternalDependency is one edge from a Task inside a programme to a Task that
// is not in it.
type ExternalDependency struct {
	TaskID    string `json:"taskId"`    // the Task inside the programme
	DependsOn string `json:"dependsOn"` // the Task outside it
	Project   string `json:"project"`   // the outside Task's project
	// Programme is the outside Task's own programme, or "" when it serves none.
	// A dependency on another programme's work is a different fact from a
	// dependency on unattached work, and the difference is what an operator acts on.
	Programme string `json:"programme,omitempty"`
	State     State  `json:"state"`
	// Satisfied uses the same rule the scheduler does: only `integrated` counts,
	// because anything short of it means the work exists but has not landed.
	Satisfied bool `json:"satisfied"`
}

// ProgrammeStatusFor rolls a programme up from the Tasks that serve it.
func (s *Service) ProgrammeStatusFor(id string) (ProgrammeStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	prog, err := s.store.GetProgramme(id)
	if err != nil {
		return ProgrammeStatus{}, err
	}
	tasks, err := s.store.TasksForProgramme(prog.ID)
	if err != nil {
		return ProgrammeStatus{}, err
	}
	out := ProgrammeStatus{Programme: prog, Tasks: tasks, ByState: map[State]int{}}
	inside := map[string]bool{}
	for _, t := range tasks {
		inside[t.ID] = true
	}
	for _, t := range tasks {
		out.ByState[t.State]++
		if IsTerminal(t.State) {
			if t.State == StateIntegrated {
				out.Landed++
			}
		} else {
			out.Open++
		}
		// The EXISTING Task→Task graph, rolled up — deliberately not a second graph
		// at a different node type, which is the mistake this milestone exists to
		// undo. Edges that stay inside the programme are already visible in each
		// Task's own dependency view; only the ones that leave are new information.
		deps, err := s.store.DependenciesOf(t.ID)
		if err != nil {
			return ProgrammeStatus{}, err
		}
		for _, dep := range deps {
			if inside[dep] {
				continue
			}
			other, err := s.store.GetTask(dep)
			if err != nil {
				// A dependency the plane cannot read is still worth reporting: saying
				// nothing would make a blocked programme look unblocked.
				out.External = append(out.External, ExternalDependency{
					TaskID: t.ID, DependsOn: dep, State: "unknown"})
				continue
			}
			out.External = append(out.External, ExternalDependency{
				TaskID: t.ID, DependsOn: dep, Project: other.Project,
				Programme: other.ProgrammeID, State: other.State,
				Satisfied: other.State == StateIntegrated,
			})
		}
	}
	return out, nil
}

// --- service ----------------------------------------------------------------

// ProgrammeRequest is the input to forming or amending a programme.
type ProgrammeRequest struct {
	Name        string                `json:"name"`
	Description string                `json:"description,omitempty"`
	Projects    []string              `json:"projects,omitempty"`
	Deps        []core.DependencyEdge `json:"deps,omitempty"`
}

// CreateProgramme forms a programme.
func (s *Service) CreateProgramme(req ProgrammeRequest) (Programme, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.CreateProgramme(Programme{
		Name: req.Name, Description: req.Description, Projects: req.Projects, Deps: req.Deps,
	})
}

// UpdateProgramme amends a programme's mutable fields.
func (s *Service) UpdateProgramme(id string, req ProgrammeRequest) (Programme, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, err := s.store.GetProgramme(id)
	if err != nil {
		return Programme{}, err
	}
	cur.Name, cur.Description, cur.Projects, cur.Deps = req.Name, req.Description, req.Projects, req.Deps
	return s.store.UpdateProgramme(cur)
}

// DeleteProgramme dissolves a programme, refusing while Tasks still serve it.
func (s *Service) DeleteProgramme(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.DeleteProgramme(id)
}

// ListProgrammes returns every programme.
func (s *Service) ListProgrammes() ([]Programme, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.ListProgrammes()
}

// GetProgramme returns one programme by id.
func (s *Service) GetProgramme(id string) (Programme, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.GetProgramme(id)
}

// resolveProgramme turns what a caller typed into a programme id.
//
// It accepts an id OR a name, because a person types the name and a client
// stores the id, and refusing one of those would make the CLI and the API
// disagree about what a programme reference is. An empty reference is not an
// error: a Task that serves no programme is a normal Task, and forcing one would
// make people invent programmes to satisfy a field.
//
// An unresolvable reference IS an error, and that is the whole point — a dangling
// pointer is the failure the file-backed store had by construction.
func (s *Service) resolveProgramme(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}
	if p, err := s.store.GetProgramme(ref); err == nil {
		return p.ID, nil
	}
	p, err := s.store.ProgrammeByName(ref)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", fmt.Errorf("%w: no programme %q — form one with `daedalus programme new`", ErrNotFound, ref)
		}
		return "", err
	}
	return p.ID, nil
}

// --- proposals --------------------------------------------------------------
//
// A programme operation an AGENT asks for is recorded as a proposal and executed
// on a human's confirmation, so the request has to survive a round trip through
// one TEXT column (`proposals.argument`).
//
// It is JSON, and NOT the "split on the first separator" trick `decodeSteerArgument`
// uses. That trick is sound there for a reason that does not hold here: a Job id
// cannot contain a space, so the split is a guarantee. A programme name can
// contain anything a person types, so borrowing the technique would be borrowing
// the style without the reason — and the failure would be quiet, a name truncated
// at a colon with the rest folded into the description. JSON round-trips exactly,
// and a human confirming the proposal reads it perfectly well.

// encodeProgrammeArgument renders a programme request for a proposal row.
func encodeProgrammeArgument(req ProgrammeRequest) string {
	b, err := json.Marshal(req)
	if err != nil {
		// Cannot happen for these field types; degrade to the name so the proposal
		// is still legible rather than empty.
		return req.Name
	}
	return string(b)
}

// decodeProgrammeArgument reads back what encodeProgrammeArgument wrote.
//
// A malformed argument is an ERROR and not a best-effort guess: this runs at the
// moment a human confirms, and executing a half-understood request on somebody's
// authority is worse than telling them it could not be read.
func decodeProgrammeArgument(argument string) (ProgrammeRequest, error) {
	var req ProgrammeRequest
	if err := json.Unmarshal([]byte(argument), &req); err != nil {
		return ProgrammeRequest{}, fmt.Errorf("%w: the proposal's programme details could not be read: %v",
			ErrInvalidRequest, err)
	}
	if strings.TrimSpace(req.Name) == "" {
		return ProgrammeRequest{}, fmt.Errorf("%w: the proposal names no programme", ErrInvalidRequest)
	}
	return req, nil
}
