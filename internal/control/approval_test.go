// Copyright (C) 2026 Techdelight BV

package control

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// --- the structural half: no worker can approve --------------------------------

// TestApproval_IsPlaneAuthority is the load-bearing structural claim. Every edge
// on the approval/integration tail must be absent from the worker table, so a
// worker-driven request cannot approve, integrate, or walk any part of the gate.
func TestApproval_IsPlaneAuthority(t *testing.T) {
	gate := []struct{ from, to State }{
		{StateVerified, StateApprovalRequired},
		{StateApprovalRequired, StateApproved},
		{StateApprovalRequired, StateRejected},
		{StateApproved, StateIntegrated},
		{StateApproved, StateRejected},
	}
	for _, edge := range gate {
		if !CanTransition(edge.from, edge.to) {
			t.Errorf("the plane should be able to drive %s → %s", edge.from, edge.to)
		}
		if WorkerCanTransition(edge.from, edge.to) {
			t.Errorf("a WORKER must not be able to drive %s → %s", edge.from, edge.to)
		}
	}
	// And no worker-reachable edge lands anywhere on the tail at all.
	tail := map[State]bool{
		StateApprovalRequired: true, StateApproved: true, StateIntegrated: true, StateVerified: true,
	}
	for _, from := range AllStates() {
		for _, to := range AllStates() {
			if tail[to] && WorkerCanTransition(from, to) {
				t.Errorf("worker-reachable edge into the governance tail: %s → %s", from, to)
			}
		}
	}
}

// TestApproval_StoreRefusesWorkerDrivenApproval checks the same thing one layer
// down, where it is actually enforced.
func TestApproval_StoreRefusesWorkerDrivenApproval(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.CreateTask(NewTask{Project: "p", Objective: "o", BaseSHA: "sha"}, StatePlanned); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, to := range []State{StateQueued, StateWorking} {
		mustTransition(t, s, "T-1", to, false)
	}
	mustTransition(t, s, "T-1", StateCandidate, true)
	mustTransition(t, s, "T-1", StateVerifying, false)
	mustTransition(t, s, "T-1", StateVerified, false)
	mustTransition(t, s, "T-1", StateApprovalRequired, false)

	if _, err := s.TransitionTask("T-1", StateApproved, true, "worker self-approval"); !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("worker approval_required→approved err = %v, want ErrIllegalTransition", err)
	}
	got, _ := s.GetTask("T-1")
	if got.State != StateApprovalRequired {
		t.Errorf("state = %q after a refused worker approval, want approval_required", got.State)
	}
}

// --- opt-in policy -------------------------------------------------------------

func TestApproval_NotRequired_AutoApprovesWithAReason(t *testing.T) {
	repo := gitRepo(t)
	svc, store, task := verifiedTask(t, repo, StubVerifyRunner{Pass: true}, "a.txt")
	// No policy source: approval is opt-in, so this project does not need one.
	if got, _ := store.GetTask(task.ID); got.State != StateVerified {
		t.Fatalf("precondition: task should rest at verified, got %s", got.State)
	}
	if _, err := svc.IntegrateTask(task.ID, IntegrateRequest{}); err != nil {
		t.Fatalf("IntegrateTask: %v", err)
	}
	// The audit trail says WHY no human was asked, rather than silently skipping.
	events, _ := store.ListEventsForTask(task.ID)
	if !hasNote(events, "no human approval is required") {
		t.Error("the log should record why no human approved")
	}
	// The note must not assert what a policy "said": when the governance file
	// cannot be read, no policy said anything, and this is the one gate where the
	// log is the only evidence a human was or was not involved.
	if hasNote(events, "by policy") {
		t.Error("the auto-approval note must not claim a policy declared something")
	}
	// The edges were walked, not skipped.
	var path []State
	for _, e := range events {
		if e.EntityType == "task" && e.To != "" {
			path = append(path, e.To)
		}
	}
	if !containsSequence(path, []State{StateVerified, StateApprovalRequired, StateApproved, StateIntegrated}) {
		t.Errorf("task did not walk the full gate: %v", path)
	}
}

func TestApproval_Required_BlocksIntegrationUntilApproved(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil, StubVerifyRunner{Pass: true})
	svc.SetPolicySource(StaticPolicy{Budget: DefaultBudget(), Approval: true})

	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	res, err := svc.VerifyTask(task.ID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !res.Verified {
		t.Fatalf("precondition: expected a verified artifact")
	}
	// Verification parks it in the queue a human can see.
	got, _ := store.GetTask(task.ID)
	if got.State != StateApprovalRequired {
		t.Fatalf("task state = %q, want approval_required", got.State)
	}
	pending, err := svc.PendingApprovals()
	if err != nil || len(pending) != 1 || pending[0].ID != task.ID {
		t.Fatalf("PendingApprovals = (%v, %v), want [%s]", pending, err, task.ID)
	}

	// Integration is refused with a typed reason until a human acts.
	_, err = svc.IntegrateTask(task.ID, IntegrateRequest{})
	var rej *RejectionError
	if !errors.As(err, &rej) || rej.Reason != ReasonApprovalRequired {
		t.Fatalf("IntegrateTask before approval = %v, want an approval_required refusal", err)
	}
	if after, _ := store.GetTask(task.ID); after.State != StateApprovalRequired {
		t.Errorf("a refused integration changed the state to %q", after.State)
	}

	// Approve, then it lands.
	approved, err := svc.ApproveTask(task.ID, "looks good")
	if err != nil {
		t.Fatalf("ApproveTask: %v", err)
	}
	if approved.State != StateApproved {
		t.Errorf("state = %q, want approved", approved.State)
	}
	if _, err := svc.IntegrateTask(task.ID, IntegrateRequest{}); err != nil {
		t.Fatalf("IntegrateTask after approval: %v", err)
	}
	final, _ := store.GetTask(task.ID)
	if final.State != StateIntegrated {
		t.Errorf("state = %q, want integrated", final.State)
	}
	// The approval is attributed and carries the note.
	events, _ := store.ListEventsForTask(task.ID)
	if !hasEventKind(events, EventApproval, ActorHuman) {
		t.Error("the approval should be recorded as a human-actor approval event")
	}
	if !hasNote(events, "looks good") {
		t.Error("the approver's note should be in the log")
	}
}

func TestApproval_Reject_FeedsTheRetryLadder(t *testing.T) {
	repo := gitRepo(t)
	svc, _, store := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil, StubVerifyRunner{Pass: true})
	svc.SetPolicySource(StaticPolicy{Budget: DefaultBudget(), Approval: true})

	task, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if _, err := svc.VerifyTask(task.ID); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	got, err := svc.RejectApproval(task.ID, "not what we wanted")
	if err != nil {
		t.Fatalf("RejectApproval: %v", err)
	}
	if got.State != StateRejected {
		t.Errorf("state = %q, want rejected", got.State)
	}
	if job, _ := s2job(t, store, task.ID); job.State != StateRejected {
		t.Errorf("job state = %q, want rejected", job.State)
	}
	// It is retryable and replannable — the Sprint-58 ladder still applies.
	if _, err := svc.ReplanTask(task.ID, ReplanRequest{Objective: "try it differently"}); err != nil {
		t.Errorf("replan after a human rejection: %v", err)
	}
	events, _ := store.ListEventsForTask(task.ID)
	if !hasEvent(events, EventApproval, ReasonApprovalRejected) {
		t.Error("the rejection should carry the approval_rejected reason")
	}
}

func TestApproval_Guards(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)
	task, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"}) // planned

	if _, err := svc.ApproveTask(task.ID, ""); !errors.Is(err, ErrWrongState) {
		t.Errorf("approving a planned task = %v, want ErrWrongState", err)
	}
	if _, err := svc.RejectApproval(task.ID, ""); !errors.Is(err, ErrWrongState) {
		t.Errorf("rejecting a planned task = %v, want ErrWrongState", err)
	}
	if _, err := svc.ApproveTask("T-404", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("approving an unknown task = %v, want ErrNotFound", err)
	}
}

func TestApproval_ApproveIsIdempotent(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo},
		StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil, StubVerifyRunner{Pass: true})
	svc.SetPolicySource(StaticPolicy{Budget: DefaultBudget(), Approval: true})
	task, _ := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if _, err := svc.VerifyTask(task.ID); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if _, err := svc.ApproveTask(task.ID, ""); err != nil {
		t.Fatalf("first approve: %v", err)
	}
	again, err := svc.ApproveTask(task.ID, "")
	if err != nil {
		t.Errorf("approving twice should be a no-op, got %v", err)
	}
	if again.State != StateApproved {
		t.Errorf("state = %q, want approved", again.State)
	}
}

func TestApprovalPolicy_PerProject(t *testing.T) {
	p := ApprovalPolicy{Default: false, Projects: map[string]bool{"guarded": true, "free": false}}
	tests := []struct {
		project string
		want    bool
	}{
		{"guarded", true},
		{"free", false},
		{"unknown", false},
	}
	for _, tc := range tests {
		if got := p.RequiredFor(tc.project); got != tc.want {
			t.Errorf("RequiredFor(%q) = %v, want %v", tc.project, got, tc.want)
		}
	}
	// Default true flips the unknown case — governance is opt-in per project, but
	// an operator can opt the whole installation in.
	all := ApprovalPolicy{Default: true}
	if !all.RequiredFor("anything") {
		t.Error("a default-true policy should require approval everywhere")
	}
}

// TestApprovalPolicy_ReadFromTheGovernanceFile: the approval flag shares the
// host-side budgets file, so an operator finds all governance in one place — and
// so a project cannot exempt itself by committing something.
func TestApprovalPolicy_ReadFromTheGovernanceFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/budgets.json"
	body := `{"default":{"maxAttempts":2},"approval":{"default":false,"projects":{"app":true}}}`
	if err := writeFileForTestNoT(dir, "budgets.json", body); err != nil {
		t.Fatal(err)
	}
	p, err := LoadBudgetPolicy(path)
	if err != nil {
		t.Fatalf("LoadBudgetPolicy: %v", err)
	}
	if !p.RequiresApproval("app") {
		t.Error("app should require approval")
	}
	if p.RequiresApproval("other") {
		t.Error("other should not require approval")
	}
	if p.BudgetFor("app").MaxAttempts != 2 {
		t.Error("the budget half of the file should still parse")
	}
	// The file source exposes both halves.
	var src PolicySource = NewFileBudgetPolicy(path)
	if !src.RequiresApproval("app") || src.BudgetFor("app").MaxAttempts != 2 {
		t.Error("FileBudgetPolicy should serve both budget and approval")
	}
}

// --- helpers -------------------------------------------------------------------

func hasEventKind(events []Event, kind, actor string) bool {
	for _, e := range events {
		if e.Kind == kind && e.Actor == actor {
			return true
		}
	}
	return false
}

// containsSequence reports whether `want` appears in order (not necessarily
// contiguously) within `got`.
func containsSequence(got, want []State) bool {
	i := 0
	for _, g := range got {
		if i < len(want) && g == want[i] {
			i++
		}
	}
	return i == len(want)
}

// TestPolicySource_InterfaceShape pins the seam: anything installed as the
// governance policy must answer both questions, so a future implementation
// cannot silently drop the approval half.
func TestPolicySource_InterfaceShape(t *testing.T) {
	iface := reflect.TypeOf((*PolicySource)(nil)).Elem()
	var names []string
	for i := 0; i < iface.NumMethod(); i++ {
		names = append(names, iface.Method(i).Name)
	}
	want := "BudgetFor,RequiresApproval"
	if got := strings.Join(names, ","); got != want {
		t.Errorf("PolicySource methods = %s, want %s", got, want)
	}
	var _ PolicySource = StaticBudget(DefaultBudget())
	var _ PolicySource = StaticPolicy{}
	var _ PolicySource = BudgetPolicy{}
	var _ PolicySource = (*FileBudgetPolicy)(nil)
}

// TestApproval_UnreadablePolicyRequiresAHuman is the regression for the audit's
// blocking finding: the approval gate used to fail OPEN. With a corrupt (or
// half-written) governance file and no last-known-good — the state at daemon boot
// — the plane auto-approved and logged that policy said no human was needed.
// Nothing had said anything.
func TestApproval_UnreadablePolicyRequiresAHuman(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/budgets.json"

	t.Run("never-read policy requires approval", func(t *testing.T) {
		if err := writeFileForTestNoT(dir, "budgets.json", `{"default":{"maxAtt`); err != nil {
			t.Fatal(err)
		}
		src := NewFileBudgetPolicy(path)
		if !src.RequiresApproval("app") {
			t.Error("an unreadable policy with no known-good must REQUIRE approval — 'I don't know' cannot mean 'nobody needs to look'")
		}
		// The budget axis still degrades to a real ceiling: the two axes fail
		// closed in different directions, deliberately.
		if got := src.BudgetFor("app"); got != DefaultBudget() {
			t.Errorf("budget = %+v, want the built-in default", got)
		}
	})

	t.Run("last known-good still wins over the fallback", func(t *testing.T) {
		if err := writeFileForTestNoT(dir, "budgets.json", `{"approval":{"default":false}}`); err != nil {
			t.Fatal(err)
		}
		src := NewFileBudgetPolicy(path)
		if src.RequiresApproval("app") {
			t.Fatal("precondition: a readable opt-out policy should not require approval")
		}
		if err := writeFileForTestNoT(dir, "budgets.json", `{"appro`); err != nil {
			t.Fatal(err)
		}
		if src.RequiresApproval("app") {
			t.Error("a corrupt read should hold the last known-good answer, not flip to requiring approval")
		}
	})

	t.Run("a project requiring approval is not auto-approved when the file breaks", func(t *testing.T) {
		repo := gitRepo(t)
		broken := t.TempDir() + "/budgets.json"
		if err := writeFileForTestNoT(filepath.Dir(broken), "budgets.json", `{"broken`); err != nil {
			t.Fatal(err)
		}
		svc, _, store := newService(t, mapResolver{"app": repo},
			StubRunner{Result: ExecSuccess, WriteFile: true, MarkerName: "a.txt"}, nil, StubVerifyRunner{Pass: true})
		svc.SetPolicySource(NewFileBudgetPolicy(broken))

		task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "x"})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		if _, err := svc.DispatchTask(task.ID); err != nil {
			t.Fatalf("Dispatch: %v", err)
		}
		if _, err := svc.VerifyTask(task.ID); err != nil {
			t.Fatalf("Verify: %v", err)
		}
		// It must be parked for a human, not auto-approved.
		got, _ := store.GetTask(task.ID)
		if got.State != StateApprovalRequired {
			t.Fatalf("state = %q, want approval_required", got.State)
		}
		_, err = svc.IntegrateTask(task.ID, IntegrateRequest{})
		var rej *RejectionError
		if !errors.As(err, &rej) || rej.Reason != ReasonApprovalRequired {
			t.Fatalf("integrate = %v, want an approval_required refusal", err)
		}
		// And nothing in the log claims a policy approved it.
		events, _ := store.ListEventsForTask(task.ID)
		if hasNote(events, "auto-approved") {
			t.Error("an unreadable policy must not produce an auto-approval event")
		}
	})
}
