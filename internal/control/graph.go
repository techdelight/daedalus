// Copyright (C) 2026 Techdelight BV

package control

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
)

// The cross-project task graph (docs/guild-master-plan.md M16).
//
// A Task may depend on other Tasks, in any project. A Task with unmet
// dependencies is `blocked` and the scheduler never admits it; satisfying the
// last one returns it to `planned`, where it competes for capacity normally.
//
// TWO PROPERTIES THAT ARE NOT NEGOTIABLE, and both are structural:
//
//  1. The edge is PLANE-OWNED STATE. It lives in control.db and is never read
//     from a file in a project checkout. An agent that could declare its own
//     dependencies could declare them satisfied — and M15's whole
//     acceptance-oracle argument would be re-opened through a side door, since
//     "what must happen before this is graded" is exactly as load-bearing as
//     "what grades it".
//  2. CYCLES ARE REFUSED AT CREATION, never detected at dispatch. A cycle found
//     at dispatch time is a wedged graph that somebody has to unpick; refused at
//     creation it is a validation error, caught by whoever wrote it, while they
//     still remember why.

// DependencyEdge is one Task→Task dependency.
type DependencyEdge struct {
	TaskID    string `json:"taskId"`
	DependsOn string `json:"dependsOn"`
	CreatedAt string `json:"createdAt"`
}

// ErrDependencyCycle is returned when adding an edge would close a cycle.
type ErrDependencyCycle struct {
	TaskID    string
	DependsOn string
	Path      []string
}

func (e *ErrDependencyCycle) Error() string {
	return fmt.Sprintf("control: %s depending on %s would create a cycle (%s); dependencies must form a DAG",
		e.TaskID, e.DependsOn, joinPath(e.Path))
}

func joinPath(path []string) string {
	out := ""
	for i, id := range path {
		if i > 0 {
			out += " → "
		}
		out += id
	}
	return out
}

// AddDependency records that `taskID` depends on `dependsOn`, refusing anything
// that would make the graph invalid.
//
// Refused at creation, not at dispatch: a self-edge, an unknown Task, a
// dependency on a Task that is already terminal-unsatisfiable, and — the one
// that matters — a cycle.
func (s *Store) AddDependency(taskID, dependsOn string) (DependencyEdge, error) {
	if taskID == dependsOn {
		return DependencyEdge{}, fmt.Errorf("%w: task %s cannot depend on itself", ErrDependencyInvalid, taskID)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return DependencyEdge{}, err
	}
	defer tx.Rollback()

	dependent, err := scanTask(tx.QueryRow(taskSelect+` WHERE id = ?`, taskID))
	if err != nil {
		return DependencyEdge{}, fmt.Errorf("dependency references %s: %w", taskID, err)
	}
	if _, err := scanTask(tx.QueryRow(taskSelect+` WHERE id = ?`, dependsOn)); err != nil {
		return DependencyEdge{}, fmt.Errorf("dependency references %s: %w", dependsOn, err)
	}
	// A terminal Task is the one case where an edge could never mean anything: it
	// has already landed (or failed, or been cancelled), so there is no dispatch
	// left to block and no landing left to gate. Recording it would produce exactly
	// the inert-but-displayed edge this sprint exists to remove — and unlike a Task
	// that is merely in flight, no later event can give it force. Refused in the
	// SAME transaction as the insert, so the state cannot change underneath the
	// check.
	if IsTerminal(dependent.State) {
		return DependencyEdge{}, fmt.Errorf(
			"%w: task %s is %s; a terminal task cannot acquire a dependency, because nothing remains that the edge could gate",
			ErrDependencyInvalid, taskID, dependent.State)
	}
	// Cycle check BEFORE the insert: walk from `dependsOn` and see whether we can
	// reach `taskID`. If we can, adding this edge would close a loop.
	if path, cyclic, err := reachesTx(tx, dependsOn, taskID); err != nil {
		return DependencyEdge{}, err
	} else if cyclic {
		return DependencyEdge{}, &ErrDependencyCycle{
			TaskID: taskID, DependsOn: dependsOn, Path: append([]string{taskID}, path...),
		}
	}

	now := s.now()
	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO task_dependencies (task_id, depends_on, created_at) VALUES (?, ?, ?)`,
		taskID, dependsOn, now,
	); err != nil {
		return DependencyEdge{}, fmt.Errorf("adding dependency: %w", err)
	}
	if err := s.logEvent(tx, "task", taskID, "", "",
		EventMeta{Kind: EventGraph}, ActorPlane,
		fmt.Sprintf("depends on %s", dependsOn)); err != nil {
		return DependencyEdge{}, err
	}
	if err := tx.Commit(); err != nil {
		return DependencyEdge{}, err
	}
	return DependencyEdge{TaskID: taskID, DependsOn: dependsOn, CreatedAt: now}, nil
}

// ErrDependencyInvalid is returned for a malformed dependency (self-edge, and
// similar), as distinct from a cycle.
var ErrDependencyInvalid = errors.New("control: invalid dependency")

// reachesTx reports whether `to` is reachable from `from` by following
// dependency edges, returning the path if so. Depth-first with a visited set, so
// an already-cyclic table (which cannot happen through this API, but could
// through a hand-edited database) terminates rather than looping forever.
func reachesTx(tx *sql.Tx, from, to string) ([]string, bool, error) {
	visited := map[string]bool{}
	var walk func(node string, path []string) ([]string, bool, error)
	walk = func(node string, path []string) ([]string, bool, error) {
		if visited[node] {
			return nil, false, nil
		}
		visited[node] = true
		path = append(path, node)
		if node == to {
			return path, true, nil
		}
		rows, err := tx.Query(`SELECT depends_on FROM task_dependencies WHERE task_id = ?`, node)
		if err != nil {
			return nil, false, err
		}
		var next []string
		for rows.Next() {
			var dep string
			if err := rows.Scan(&dep); err != nil {
				rows.Close()
				return nil, false, err
			}
			next = append(next, dep)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, false, err
		}
		for _, dep := range next {
			if found, ok, err := walk(dep, path); err != nil {
				return nil, false, err
			} else if ok {
				return found, true, nil
			}
		}
		return nil, false, nil
	}
	return walk(from, nil)
}

// DependenciesOf returns the Task ids `taskID` depends on.
func (s *Store) DependenciesOf(taskID string) ([]string, error) {
	return s.dependencyIDs(`SELECT depends_on FROM task_dependencies WHERE task_id = ? ORDER BY depends_on`, taskID)
}

// DependentsOf returns the Task ids that depend on `taskID` — the wake list.
func (s *Store) DependentsOf(taskID string) ([]string, error) {
	return s.dependencyIDs(`SELECT task_id FROM task_dependencies WHERE depends_on = ? ORDER BY task_id`, taskID)
}

func (s *Store) dependencyIDs(query, id string) ([]string, error) {
	rows, err := s.db.Query(query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DependencyStatus describes why a Task is (or is not) runnable.
type DependencyStatus struct {
	// Unmet are dependencies that have not completed yet.
	Unmet []string `json:"unmet,omitempty"`
	// Unsatisfiable are dependencies that can never complete — they failed or
	// expired. A Task waiting on one is not going to become runnable by waiting.
	Unsatisfiable []string `json:"unsatisfiable,omitempty"`
}

// Ready reports whether every dependency is satisfied.
func (d DependencyStatus) Ready() bool { return len(d.Unmet) == 0 && len(d.Unsatisfiable) == 0 }

// DependencyStatusFor evaluates a Task's dependencies.
//
// A dependency is SATISFIED only when it reached `integrated` — the point at
// which its work is actually in the trunk. Anything short of that (verified,
// approved) means the work exists but has not landed, and a dependent that ran
// against it would be building on something that may still be rejected.
func (s *Store) DependencyStatusFor(taskID string) (DependencyStatus, error) {
	deps, err := s.DependenciesOf(taskID)
	if err != nil {
		return DependencyStatus{}, err
	}
	var status DependencyStatus
	for _, id := range deps {
		dep, err := s.GetTask(id)
		if err != nil {
			// A dependency whose Task has vanished cannot be satisfied.
			status.Unsatisfiable = append(status.Unsatisfiable, id)
			continue
		}
		switch {
		case dep.State == StateIntegrated:
			// satisfied
		case dep.State == StateFailed || dep.State == StateExpired || dep.State == StateCancelled:
			status.Unsatisfiable = append(status.Unsatisfiable, id)
		default:
			status.Unmet = append(status.Unmet, id)
		}
	}
	return status, nil
}

// --- service-level dependency scheduling ----------------------------------------

// AddDependency declares that a Task depends on another, and blocks it if the
// dependency is not yet satisfied.
//
// The edge is refused at creation for anything that would make the graph invalid
// — a cycle above all — so a caller learns about it while it still remembers why
// it asked, rather than discovering a wedged graph at dispatch time.
func (s *Service) AddDependency(taskID, dependsOn string) (DependencyEdge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	edge, err := s.store.AddDependency(taskID, dependsOn)
	if err != nil {
		return DependencyEdge{}, err
	}
	// Re-evaluate immediately: a Task that just acquired an unmet dependency is
	// not runnable, and leaving it `planned` would let the scheduler admit it.
	if _, err := s.refreshBlockedState(taskID); err != nil {
		log.Printf("control: evaluating dependencies of %s: %v", taskID, err)
	}
	return edge, nil
}

// requireDependenciesLanded refuses to land a Task whose dependencies have not
// themselves landed. s.mu must be held.
//
// This is the enforcement half of the graph, and for a while it did not exist:
// `blocked` gated admission and nothing gated landing, so an edge declared once a
// Task had left `planned` was recorded, rendered in `task depends` and on the
// board, and changed nothing at all. A dependency that is displayed but inert is
// worse than one that was refused, because the board reads as a promise. See the
// long note at the call site in integrateOnce for why landing is the right gate.
func (s *Service) requireDependenciesLanded(task Task) error {
	status, err := s.store.DependencyStatusFor(task.ID)
	if err != nil {
		return err
	}
	if status.Ready() {
		return nil
	}
	// Unsatisfiable is named separately because the two need different actions
	// from an operator: waiting helps in one case and never helps in the other.
	detail := fmt.Sprintf("task %s cannot land yet: waiting on %v", task.ID, status.Unmet)
	if len(status.Unsatisfiable) > 0 {
		detail = fmt.Sprintf("task %s cannot land: %v can never be satisfied (unmet: %v)",
			task.ID, status.Unsatisfiable, status.Unmet)
	}
	return s.refuse("task", task.ID, EventGraph, ReasonDependenciesUnmet, detail)
}

// refreshBlockedState moves a Task between `planned` and `blocked` to match its
// dependencies. s.mu must be held. Returns whether it changed anything.
//
// Only `planned` and `blocked` are touched, because those are the only two states
// this pair of transitions connects. That is NOT the same as saying dependencies
// stop mattering once a Task is running: they gate landing too, which is enforced
// at the integration transaction by requireDependenciesLanded. A Task past
// `planned` simply carries its unmet dependencies rather than wearing them as a
// state — there is no `working`-and-blocked to move it to, and inventing one would
// mean a Task whose worker is mid-flight claiming to be waiting.
func (s *Service) refreshBlockedState(taskID string) (bool, error) {
	task, err := s.store.GetTask(taskID)
	if err != nil {
		return false, err
	}
	if task.State != StatePlanned && task.State != StateBlocked {
		return false, nil
	}
	status, err := s.store.DependencyStatusFor(taskID)
	if err != nil {
		return false, err
	}
	meta := EventMeta{Kind: EventGraph}
	switch {
	case status.Ready() && task.State == StateBlocked:
		_, err := s.store.TransitionTaskWith(taskID, StatePlanned, false, meta,
			"ready: every dependency has landed")
		return err == nil, err
	case !status.Ready() && task.State == StatePlanned:
		note := fmt.Sprintf("blocked on %v", append(append([]string{}, status.Unmet...), status.Unsatisfiable...))
		_, err := s.store.TransitionTaskWith(taskID, StateBlocked, false, meta, note)
		return err == nil, err
	}
	return false, nil
}

// wakeDependents re-evaluates every Task that depends on taskID. s.mu must be
// held.
//
// Called whenever a Task reaches a state that could satisfy or permanently
// un-satisfy a dependency. It is deliberately NOT the only path — see
// Service.Reconcile, which re-evaluates every blocked Task on each pass. Sprint
// 61's lesson applies directly: a wake that only ever happens on an event is a
// wake that is missed when the process dies mid-event, and the invariant is that
// free capacity must become usable without human intervention. The event path is
// the fast one; reconcile is what makes it eventually true regardless.
func (s *Service) wakeDependents(taskID string) {
	dependents, err := s.store.DependentsOf(taskID)
	if err != nil {
		log.Printf("control: finding dependents of %s: %v", taskID, err)
		return
	}
	for _, id := range dependents {
		if _, err := s.refreshBlockedState(id); err != nil {
			log.Printf("control: waking dependent %s of %s: %v", id, taskID, err)
		}
	}
}

// cancelDependentsOf cancels the Tasks that depend on a CANCELLED Task. s.mu must
// be held.
//
// Cancellation is a human deciding the work will not happen. Its dependents can
// therefore never become runnable, and leaving them `blocked` forever is exactly
// the stranding this is meant to avoid — so the decision propagates, transitively,
// with the reason recorded on each.
//
// Note the deliberate asymmetry with FAILURE — and note how small it is. A failed
// dependency also cannot be satisfied, and it is PERMANENT: `failed` is terminal
// with no outgoing edge, and there is no operation to remove a dependency edge, so
// a dependent blocked on failed work cannot be rescued in place either. Leaving it
// `blocked` rather than cancelling it keeps it visible with a legible reason and
// leaves the decision to a person, instead of cascading automatically on an
// outcome nobody chose. The operator's route out is the same one: cancel and
// recreate.
func (s *Service) cancelDependentsOf(taskID string) []string {
	var cancelled []string
	queue := []string{taskID}
	seen := map[string]bool{taskID: true}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		dependents, err := s.store.DependentsOf(current)
		if err != nil {
			log.Printf("control: finding dependents of %s: %v", current, err)
			continue
		}
		for _, id := range dependents {
			if seen[id] {
				continue
			}
			seen[id] = true
			task, err := s.store.GetTask(id)
			if err != nil || IsTerminal(task.State) {
				continue
			}
			if _, err := s.store.TransitionTaskWith(id, StateCancelled, false,
				EventMeta{Kind: EventGraph},
				"cancelled: its dependency "+current+" was cancelled and can never be satisfied"); err != nil {
				log.Printf("control: cancelling dependent %s: %v", id, err)
				continue
			}
			s.sched.Forget(id)
			cancelled = append(cancelled, id)
			queue = append(queue, id)
		}
	}
	return cancelled
}

// DependencyView describes a Task's position in the graph, for status output.
type DependencyView struct {
	DependsOn  []string         `json:"dependsOn,omitempty"`
	Dependents []string         `json:"dependents,omitempty"`
	Status     DependencyStatus `json:"status"`
}

// TaskDependencies returns a Task's graph position.
func (s *Service) TaskDependencies(taskID string) (DependencyView, error) {
	// A Task with no edges and a Task that does not exist both have empty lists;
	// only one of them is a valid answer.
	if _, err := s.store.GetTask(taskID); err != nil {
		return DependencyView{}, err
	}
	deps, err := s.store.DependenciesOf(taskID)
	if err != nil {
		return DependencyView{}, err
	}
	dependents, err := s.store.DependentsOf(taskID)
	if err != nil {
		return DependencyView{}, err
	}
	status, err := s.store.DependencyStatusFor(taskID)
	if err != nil {
		return DependencyView{}, err
	}
	return DependencyView{DependsOn: deps, Dependents: dependents, Status: status}, nil
}
