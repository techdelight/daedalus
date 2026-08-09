// Copyright (C) 2026 Techdelight BV

package control

import (
	"errors"
	"testing"
)

// graphPlane builds a Service over several projects with a passing verifier, so
// Tasks can be driven all the way to `integrated` and actually satisfy
// dependencies.
func graphPlane(t *testing.T, projects ...string) (*Service, *Store, map[string]string) {
	t.Helper()
	dirs := map[string]string{}
	for _, p := range projects {
		dirs[p] = gitRepo(t)
	}
	svc, _, store := newService(t, mapResolver(dirs),
		StubRunner{Result: ExecSuccess, WriteFile: true}, nil, StubVerifyRunner{Pass: true})
	svc.SetSchedulerLimits(SchedulerLimits{Global: 8, PerProject: 4})
	return svc, store, dirs
}

// land drives a Task all the way to `integrated`, which is what satisfies a
// dependency.
func land(t *testing.T, svc *Service, id string) {
	t.Helper()
	if _, err := svc.DispatchTask(id); err != nil {
		t.Fatalf("dispatch %s: %v", id, err)
	}
	if _, err := svc.VerifyTask(id); err != nil {
		t.Fatalf("verify %s: %v", id, err)
	}
	if _, err := svc.IntegrateTask(id); err != nil {
		t.Fatalf("integrate %s: %v", id, err)
	}
}

// --- cycles are refused at CREATION ---------------------------------------------

// TestGraph_CycleRefusedAtCreation is the constraint that matters most: a cycle
// found at dispatch time is a wedged graph somebody has to unpick; refused at
// creation it is a validation error, caught while the author still remembers why.
func TestGraph_CycleRefusedAtCreation(t *testing.T) {
	svc, store, _ := graphPlane(t, "alpha", "beta", "gamma")
	a, _ := svc.CreateTask(CreateTaskRequest{Project: "alpha", Objective: "a"})
	b, _ := svc.CreateTask(CreateTaskRequest{Project: "beta", Objective: "b"})
	c, _ := svc.CreateTask(CreateTaskRequest{Project: "gamma", Objective: "c"})

	// A → B → C is fine.
	if _, err := svc.AddDependency(a.ID, b.ID); err != nil {
		t.Fatalf("a depends on b: %v", err)
	}
	if _, err := svc.AddDependency(b.ID, c.ID); err != nil {
		t.Fatalf("b depends on c: %v", err)
	}
	// C → A would close the loop, and must be refused NOW.
	_, err := svc.AddDependency(c.ID, a.ID)
	var cycle *ErrDependencyCycle
	if !errors.As(err, &cycle) {
		t.Fatalf("closing the loop = %v, want *ErrDependencyCycle", err)
	}
	if len(cycle.Path) < 2 {
		t.Errorf("the cycle should name its path, got %v", cycle.Path)
	}
	// The edge was NOT recorded: a refused cycle must leave no trace.
	deps, _ := store.DependenciesOf(c.ID)
	if len(deps) != 0 {
		t.Errorf("a refused cycle left an edge behind: %v", deps)
	}
	// A self-edge is refused too, as invalid rather than cyclic.
	if _, err := svc.AddDependency(a.ID, a.ID); !errors.Is(err, ErrDependencyInvalid) {
		t.Errorf("self-dependency = %v, want ErrDependencyInvalid", err)
	}
	// And an unknown Task cannot be depended on.
	if _, err := svc.AddDependency(a.ID, "T-404"); !errors.Is(err, ErrNotFound) {
		t.Errorf("dependency on an unknown task = %v, want ErrNotFound", err)
	}
}

// --- blocking and waking --------------------------------------------------------

// TestGraph_BlockedUntilDependencyLands: a Task with an unmet dependency is
// `blocked`, is never admitted, and becomes runnable when the dependency LANDS —
// not merely when it is verified.
func TestGraph_BlockedUntilDependencyLands(t *testing.T) {
	svc, store, _ := graphPlane(t, "alpha", "beta")
	upstream, _ := svc.CreateTask(CreateTaskRequest{Project: "alpha", Objective: "the prerequisite"})
	downstream, _ := svc.CreateTask(CreateTaskRequest{Project: "beta", Objective: "the dependent"})

	if _, err := svc.AddDependency(downstream.ID, upstream.ID); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	// Declaring the dependency blocks it immediately.
	got, _ := store.GetTask(downstream.ID)
	if got.State != StateBlocked {
		t.Fatalf("dependent = %s, want blocked", got.State)
	}
	// The scheduler will not admit it.
	_, err := svc.DispatchTask(downstream.ID)
	if err == nil {
		t.Fatal("a blocked task must not be dispatchable")
	}
	if !errors.Is(err, ErrWrongState) {
		t.Errorf("dispatch of a blocked task = %v, want a state refusal", err)
	}

	// Verified is NOT enough — the work exists but has not landed, and a dependent
	// running against it would be building on something that may still be rejected.
	if _, err := svc.DispatchTask(upstream.ID); err != nil {
		t.Fatalf("dispatch upstream: %v", err)
	}
	if _, err := svc.VerifyTask(upstream.ID); err != nil {
		t.Fatalf("verify upstream: %v", err)
	}
	svc.wakeDependentsLocked(t, upstream.ID)
	if got, _ := store.GetTask(downstream.ID); got.State != StateBlocked {
		t.Errorf("dependent = %s after upstream merely VERIFIED, want still blocked", got.State)
	}

	// Landing it wakes the dependent.
	if _, err := svc.IntegrateTask(upstream.ID); err != nil {
		t.Fatalf("integrate upstream: %v", err)
	}
	if got, _ := store.GetTask(downstream.ID); got.State != StatePlanned {
		t.Fatalf("dependent = %s after upstream landed, want planned", got.State)
	}
	if _, err := svc.DispatchTask(downstream.ID); err != nil {
		t.Errorf("the woken dependent should be dispatchable: %v", err)
	}
}

// wakeDependentsLocked calls the wake path under the service lock (test helper).
func (s *Service) wakeDependentsLocked(t *testing.T, taskID string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wakeDependents(taskID)
}

// TestGraph_Diamond: A and B both depend on ROOT; LEAF depends on both. LEAF must
// wait for the LAST of them, not the first.
func TestGraph_Diamond(t *testing.T) {
	svc, store, _ := graphPlane(t, "root", "left", "right", "leaf")
	root, _ := svc.CreateTask(CreateTaskRequest{Project: "root", Objective: "root"})
	left, _ := svc.CreateTask(CreateTaskRequest{Project: "left", Objective: "left"})
	right, _ := svc.CreateTask(CreateTaskRequest{Project: "right", Objective: "right"})
	leaf, _ := svc.CreateTask(CreateTaskRequest{Project: "leaf", Objective: "leaf"})

	for _, edge := range [][2]string{
		{left.ID, root.ID}, {right.ID, root.ID},
		{leaf.ID, left.ID}, {leaf.ID, right.ID},
	} {
		if _, err := svc.AddDependency(edge[0], edge[1]); err != nil {
			t.Fatalf("%s depends on %s: %v", edge[0], edge[1], err)
		}
	}
	for _, id := range []string{left.ID, right.ID, leaf.ID} {
		if got, _ := store.GetTask(id); got.State != StateBlocked {
			t.Fatalf("%s = %s, want blocked", id, got.State)
		}
	}

	land(t, svc, root.ID)
	// Both arms wake; the leaf does not.
	for _, id := range []string{left.ID, right.ID} {
		if got, _ := store.GetTask(id); got.State != StatePlanned {
			t.Errorf("%s = %s after root landed, want planned", id, got.State)
		}
	}
	if got, _ := store.GetTask(leaf.ID); got.State != StateBlocked {
		t.Fatalf("leaf = %s after root landed, want still blocked", got.State)
	}

	land(t, svc, left.ID)
	// ONE arm is not enough.
	if got, _ := store.GetTask(leaf.ID); got.State != StateBlocked {
		t.Fatalf("leaf = %s after ONE arm landed, want still blocked", got.State)
	}
	land(t, svc, right.ID)
	if got, _ := store.GetTask(leaf.ID); got.State != StatePlanned {
		t.Errorf("leaf = %s after BOTH arms landed, want planned", got.State)
	}
}

// --- unsatisfiable dependencies --------------------------------------------------

// TestGraph_FailedDependencyBlocksItsDependents: failure is an OUTCOME, not a
// decision. The dependent stays blocked and visible, so a human can retry the
// failed work as a new Task and keep it.
func TestGraph_FailedDependencyBlocksItsDependents(t *testing.T) {
	svc, store, _ := graphPlane(t, "alpha", "beta")
	upstream, _ := svc.CreateTask(CreateTaskRequest{Project: "alpha", Objective: "will fail"})
	downstream, _ := svc.CreateTask(CreateTaskRequest{Project: "beta", Objective: "dependent"})
	if _, err := svc.AddDependency(downstream.ID, upstream.ID); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}

	// Fail the upstream Task.
	if _, err := store.TransitionTask(upstream.ID, StateFailed, false, "test: upstream failed"); err != nil {
		t.Fatalf("failing upstream: %v", err)
	}
	svc.wakeDependentsLocked(t, upstream.ID)

	got, _ := store.GetTask(downstream.ID)
	if got.State != StateBlocked {
		t.Errorf("dependent = %s, want blocked — a failed prerequisite did not happen", got.State)
	}
	// And the reason is legible: unsatisfiable, not merely unmet.
	view, err := svc.TaskDependencies(downstream.ID)
	if err != nil {
		t.Fatalf("TaskDependencies: %v", err)
	}
	if len(view.Status.Unsatisfiable) != 1 || view.Status.Unsatisfiable[0] != upstream.ID {
		t.Errorf("Unsatisfiable = %v, want [%s]", view.Status.Unsatisfiable, upstream.ID)
	}
	if view.Status.Ready() {
		t.Error("a dependent of a failed task must not be Ready")
	}
}

// TestGraph_CancelledDependencyDoesNotStrandItsDependents: cancellation is a
// human DECIDING the work will not happen, so its dependents can never become
// runnable. Leaving them blocked forever is the stranding; the decision
// propagates instead — transitively.
func TestGraph_CancelledDependencyDoesNotStrandItsDependents(t *testing.T) {
	svc, store, _ := graphPlane(t, "alpha", "beta", "gamma")
	upstream, _ := svc.CreateTask(CreateTaskRequest{Project: "alpha", Objective: "will be cancelled"})
	middle, _ := svc.CreateTask(CreateTaskRequest{Project: "beta", Objective: "middle"})
	leaf, _ := svc.CreateTask(CreateTaskRequest{Project: "gamma", Objective: "leaf"})
	if _, err := svc.AddDependency(middle.ID, upstream.ID); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	if _, err := svc.AddDependency(leaf.ID, middle.ID); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}

	if _, err := svc.CancelTask(upstream.ID); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	// The decision reached BOTH levels: nothing is left waiting on work that will
	// never be done.
	for _, id := range []string{middle.ID, leaf.ID} {
		got, _ := store.GetTask(id)
		if got.State != StateCancelled {
			t.Errorf("%s = %s, want cancelled — a dependent of cancelled work must not be stranded", id, got.State)
		}
		if IsActive(got.State) {
			t.Errorf("%s is still active", id)
		}
	}
	// The reason is on the record for each.
	events, _ := store.ListEventsForTask(leaf.ID)
	if !hasNote(events, "can never be satisfied") {
		t.Error("the propagated cancellation should say why")
	}
}

// --- the wake path's liveness backstop -------------------------------------------

// TestGraph_ReconcileWakesAMissedDependent applies Sprint 61's lesson: a wake
// that only ever happens on an event is missed when the process dies mid-event.
// Reconcile must make it eventually true regardless — free capacity becoming
// usable without human intervention.
func TestGraph_ReconcileWakesAMissedDependent(t *testing.T) {
	svc, store, _ := graphPlane(t, "alpha", "beta")
	upstream, _ := svc.CreateTask(CreateTaskRequest{Project: "alpha", Objective: "prerequisite"})
	downstream, _ := svc.CreateTask(CreateTaskRequest{Project: "beta", Objective: "dependent"})
	if _, err := svc.AddDependency(downstream.ID, upstream.ID); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}

	// Land the upstream Task at the STORE level, bypassing the event wake path
	// entirely — exactly what a crash between the landing and the wake looks like.
	for _, to := range []State{StateQueued, StateWorking, StateCandidate, StateVerifying,
		StateVerified, StateApprovalRequired, StateApproved, StateIntegrated} {
		if _, err := store.TransitionTask(upstream.ID, to, false, "test: bypassing the wake"); err != nil {
			t.Fatalf("→%s: %v", to, err)
		}
	}
	if got, _ := store.GetTask(downstream.ID); got.State != StateBlocked {
		t.Fatalf("precondition: the dependent should still be blocked, got %s", got.State)
	}

	rep, err := svc.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(rep.DependencyStateChanged) != 1 || rep.DependencyStateChanged[0] != downstream.ID {
		t.Fatalf("DependencyStateChanged = %v, want [%s]", rep.DependencyStateChanged, downstream.ID)
	}
	if got, _ := store.GetTask(downstream.ID); got.State != StatePlanned {
		t.Errorf("dependent = %s, want planned — a missed wake must self-heal", got.State)
	}
	// Idempotent: a second pass changes nothing.
	rep2, _ := svc.Reconcile()
	if len(rep2.DependencyStateChanged) != 0 {
		t.Errorf("second pass changed %v, want nothing", rep2.DependencyStateChanged)
	}
}

// TestGraph_DependencyEdgesArePlaneOwned: the edge lives in the control database
// and nowhere else. There is no reader that takes it from a project checkout, and
// no worker-reachable transition into or out of `blocked`.
func TestGraph_DependencyEdgesArePlaneOwned(t *testing.T) {
	// A worker can never move a Task into or out of `blocked`; if it could, an
	// agent could declare itself unblocked, and "what must happen before this is
	// graded" is as load-bearing as "what grades it".
	for _, from := range AllStates() {
		if WorkerCanTransition(from, StateBlocked) {
			t.Errorf("worker-reachable edge into blocked: %s → blocked", from)
		}
		if WorkerCanTransition(StateBlocked, from) {
			t.Errorf("worker-reachable edge out of blocked: blocked → %s", from)
		}
	}
	// The plane can, from planned and back.
	if !CanTransition(StatePlanned, StateBlocked) || !CanTransition(StateBlocked, StatePlanned) {
		t.Error("the plane should be able to move planned ↔ blocked")
	}
}
