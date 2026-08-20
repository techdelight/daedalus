// Copyright (C) 2026 Techdelight BV

package control

import (
	"errors"
	"strings"
	"testing"

	"github.com/techdelight/daedalus/core"
)

// TestProgramme_IdentityIsTheIDNotTheName is the reason the plane owns
// programmes at all.
//
// The file-backed store keyed a programme by its filename, so renaming one broke
// every reference to it — silently, because nothing held a reference the store
// could check. Here the Task stores the ID, so a rename is just a rename. This is
// the same defect Sprint 59 fixed for the integration target by keying it to
// something an editor cannot move.
func TestProgramme_IdentityIsTheIDNotTheName(t *testing.T) {
	st := openTestStore(t)

	p, err := st.CreateProgramme(Programme{Name: "learn-languages", Description: "get fluent"})
	if err != nil {
		t.Fatalf("CreateProgramme: %v", err)
	}
	if !strings.HasPrefix(p.ID, "PR-") {
		t.Errorf("id = %q, want a PR- prefix (P- belongs to proposals)", p.ID)
	}
	task := seedTaskForProgramme(t, st, "app", p.ID)

	p.Name = "language-learning"
	if _, err := st.UpdateProgramme(p); err != nil {
		t.Fatalf("UpdateProgramme: %v", err)
	}

	got, err := st.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.ProgrammeID != p.ID {
		t.Errorf("after a rename the task points at %q, want %q — the reference must survive",
			got.ProgrammeID, p.ID)
	}
	tasks, err := st.TasksForProgramme(p.ID)
	if err != nil || len(tasks) != 1 {
		t.Errorf("TasksForProgramme after rename = %d tasks (%v), want 1", len(tasks), err)
	}
}

// TestProgramme_DeleteRefusesWhileTasksServeIt: dissolving a programme its Tasks
// point at would reproduce exactly the dangling reference the file store had, and
// the record would answer "what was this in service of" with an id resolving to
// nothing.
func TestProgramme_DeleteRefusesWhileTasksServeIt(t *testing.T) {
	st := openTestStore(t)
	p, err := st.CreateProgramme(Programme{Name: "fluency"})
	if err != nil {
		t.Fatal(err)
	}
	seedTaskForProgramme(t, st, "app", p.ID)

	err = st.DeleteProgramme(p.ID)
	if !errors.Is(err, ErrProgrammeInUse) {
		t.Fatalf("DeleteProgramme = %v, want ErrProgrammeInUse", err)
	}

	// With nothing serving it, it dissolves.
	empty, err := st.CreateProgramme(Programme{Name: "abandoned"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteProgramme(empty.ID); err != nil {
		t.Errorf("DeleteProgramme on an empty programme = %v, want nil", err)
	}
}

// TestImportProgrammes_IsIdempotentByName: the daemon runs this on EVERY start,
// which is only safe if a second run is a no-op. That property is what removes
// the need for a migration flag — and a migration flag is the thing that gets
// forgotten on a fresh install.
func TestImportProgrammes_IsIdempotentByName(t *testing.T) {
	st := openTestStore(t)
	defs := []core.Programme{
		{Name: "fluency", Description: "get fluent", Projects: []string{"langlearn", "snowball"},
			Deps: []core.DependencyEdge{{Upstream: "snowball", Downstream: "langlearn"}}},
		{Name: "health"},
		{Name: "   "}, // no name: skipped rather than stored as an unnameable row
	}

	n, err := st.ImportProgrammes(defs)
	if err != nil {
		t.Fatalf("ImportProgrammes: %v", err)
	}
	if n != 2 {
		t.Errorf("imported %d, want 2 (the nameless one is skipped)", n)
	}
	again, err := st.ImportProgrammes(defs)
	if err != nil {
		t.Fatalf("second ImportProgrammes: %v", err)
	}
	if again != 0 {
		t.Errorf("second import adopted %d more, want 0 — every daemon start would duplicate them", again)
	}
	progs, err := st.ListProgrammes()
	if err != nil {
		t.Fatal(err)
	}
	if len(progs) != 2 {
		t.Fatalf("%d programmes after two imports, want 2", len(progs))
	}
	// Everything somebody wrote survives the move, including the project→project
	// edges. Dropping them would be destroying user data on a migration.
	if len(progs[0].Projects) != 2 || len(progs[0].Deps) != 1 {
		t.Errorf("imported %+v, want its projects and deps carried over", progs[0])
	}
}

// TestTaskRationale_AuthorshipComesFromTheTransport is the property that makes
// "the rationale is the human's own words" checkable rather than hoped for.
//
// The caller class is derived from the socket the request arrived on and is never
// in the request, so an agent-drafted reason is visibly the agent's. The point is
// not to refuse it — an agent may well draft a good one — but to stop it reading
// as the operator's a year later.
func TestTaskRationale_AuthorshipComesFromTheTransport(t *testing.T) {
	svc, _ := newProgrammeService(t)
	prog, err := svc.CreateProgramme(ProgrammeRequest{Name: "fluency"})
	if err != nil {
		t.Fatal(err)
	}

	human, err := svc.createTask(Human(), CreateTaskRequest{
		Project: "app", Objective: "do the thing",
		Programme: "fluency", Rationale: "it unblocks the daily review",
	})
	if err != nil {
		t.Fatalf("createTask (human): %v", err)
	}
	if human.ProgrammeID != prog.ID {
		t.Errorf("programme = %q, want %q resolved from the NAME", human.ProgrammeID, prog.ID)
	}
	if human.RationaleBy != CallerHuman {
		t.Errorf("rationaleBy = %q, want %q", human.RationaleBy, CallerHuman)
	}

	agent, err := svc.createTask(Agent(), CreateTaskRequest{
		Project: "app", Objective: "do another thing", Rationale: "I think this is needed",
	})
	if err != nil {
		t.Fatalf("createTask (agent): %v", err)
	}
	if agent.RationaleBy != CallerAgent {
		t.Errorf("rationaleBy = %q, want %q — an agent's reason must not read as the operator's",
			agent.RationaleBy, CallerAgent)
	}

	// No rationale, no authorship: an unattributed Task reads as unattributed
	// rather than as "authored by a human who wrote nothing".
	silent, err := svc.createTask(Human(), CreateTaskRequest{Project: "app", Objective: "third"})
	if err != nil {
		t.Fatal(err)
	}
	if silent.RationaleBy != "" {
		t.Errorf("rationaleBy = %q on a task with no rationale, want empty", silent.RationaleBy)
	}
}

// TestCreateTask_RefusesAnUnknownProgramme: a dangling pointer is the failure the
// file store had by construction, and accepting one here would import it.
func TestCreateTask_RefusesAnUnknownProgramme(t *testing.T) {
	svc, _ := newProgrammeService(t)
	_, err := svc.createTask(Human(), CreateTaskRequest{
		Project: "app", Objective: "do the thing", Programme: "no-such-programme",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("createTask with an unknown programme = %v, want ErrNotFound", err)
	}
	// An EMPTY reference is not an error: a Task serving no programme is normal,
	// and requiring one would make people invent programmes to satisfy a field.
	if _, err := svc.createTask(Human(), CreateTaskRequest{Project: "app", Objective: "x"}); err != nil {
		t.Errorf("createTask with no programme = %v, want it accepted", err)
	}
}

// TestProgrammeStatus_ReportsEdgesLeavingTheProgramme is the roll-up's whole
// reason for existing.
//
// A dependency INSIDE the programme is already visible in each Task's own view.
// One that leaves it is not visible anywhere else — the two Tasks are in
// different projects — and a programme that looks fully staffed while blocked on
// work nobody put in it is exactly what an operator needs told.
func TestProgrammeStatus_ReportsEdgesLeavingTheProgramme(t *testing.T) {
	svc, st := newProgrammeService(t)
	prog, err := svc.CreateProgramme(ProgrammeRequest{Name: "fluency", Description: "get fluent"})
	if err != nil {
		t.Fatal(err)
	}

	inside := seedTaskForProgramme(t, st, "app", prog.ID)
	alsoInside := seedTaskForProgramme(t, st, "app", prog.ID)
	outside := seedTaskForProgramme(t, st, "other", "")

	if _, err := st.AddDependency(inside.ID, alsoInside.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDependency(inside.ID, outside.ID); err != nil {
		t.Fatal(err)
	}

	status, err := svc.ProgrammeStatusFor(prog.ID)
	if err != nil {
		t.Fatalf("ProgrammeStatusFor: %v", err)
	}
	if len(status.Tasks) != 2 {
		t.Fatalf("%d tasks in the programme, want 2", len(status.Tasks))
	}
	if status.Open != 2 || status.Landed != 0 {
		t.Errorf("open=%d landed=%d, want 2/0", status.Open, status.Landed)
	}
	if len(status.External) != 1 {
		t.Fatalf("%d external edges, want exactly 1 — the internal one must not be reported: %+v",
			len(status.External), status.External)
	}
	ext := status.External[0]
	if ext.TaskID != inside.ID || ext.DependsOn != outside.ID {
		t.Errorf("external edge = %s → %s, want %s → %s", ext.TaskID, ext.DependsOn, inside.ID, outside.ID)
	}
	if ext.Project != "other" {
		t.Errorf("external project = %q, want %q", ext.Project, "other")
	}
	if ext.Programme != "" {
		t.Errorf("external programme = %q, want empty — that task serves none", ext.Programme)
	}
	if ext.Satisfied {
		t.Error("an unlanded dependency reported as satisfied; only `integrated` counts")
	}
}

// --- helpers ------------------------------------------------------------------

// newProgrammeService wires a Service whose projects resolve to real git repos,
// which is what task creation needs (it captures a base commit).
func newProgrammeService(t *testing.T) (*Service, *Store) {
	t.Helper()
	resolver := mapResolver{"app": gitRepo(t), "other": gitRepo(t)}
	svc, _, store := newService(t, resolver, StubRunner{}, nil)
	return svc, store
}

// seedTaskForProgramme inserts a Task serving programmeID (or none, if empty).
func seedTaskForProgramme(t *testing.T, st *Store, project, programmeID string) Task {
	t.Helper()
	task, err := st.CreateTask(NewTask{
		Project: project, Objective: "objective for " + project, BaseSHA: "base",
		Budget: DefaultBudget(), ProgrammeID: programmeID,
	}, StatePlanned)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return task
}

// --- the Guild Master's path (#82) ----------------------------------------------

// TestProgrammeProposal_AgentAsksAndAHumanConfirms is the end-to-end path that
// M20 claimed and did not build.
//
// The operations were tiered and then had no case in executeProposal, so a
// confirmed proposal fell to the default and failed closed: "names an operation
// this plane cannot execute". Correct for an unknown op, and a dead end for one
// the authority table had already promised. Tiering reserves authority over
// something that has to exist.
func TestProgrammeProposal_AgentAsksAndAHumanConfirms(t *testing.T) {
	svc, store := newProgrammeService(t)
	agent := svc.WithCaller(Agent())

	// An agent asking gets a proposal, not a programme.
	_, err := agent.CreateProgramme(ProgrammeRequest{
		Name: "fluency", Description: "get conversational by spring", Projects: []string{"app"},
	})
	reason, refused := Rejected(err)
	if !refused || reason != ReasonProposalRecorded {
		t.Fatalf("agent CreateProgramme = %v, want a recorded proposal", err)
	}
	if progs, _ := store.ListProgrammes(); len(progs) != 0 {
		t.Fatalf("%d programmes exist already; the agent's request executed", len(progs))
	}

	pending, err := store.ListProposals(ProposalPending)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending proposals = %d (%v), want 1", len(pending), err)
	}
	if pending[0].Operation != OpFormProgramme {
		t.Errorf("operation = %q, want %q", pending[0].Operation, OpFormProgramme)
	}

	// A human confirms, and the programme exists — with the description intact.
	// The description is the point: it is what a Task's rationale is later judged
	// against, so an encoding that lost it would quietly hollow out the feature.
	if _, err := svc.ResolveProposal(pending[0].ID, true, "agreed"); err != nil {
		t.Fatalf("confirming the proposal: %v", err)
	}
	progs, err := store.ListProgrammes()
	if err != nil || len(progs) != 1 {
		t.Fatalf("programmes after confirmation = %d (%v), want 1", len(progs), err)
	}
	if progs[0].Name != "fluency" || progs[0].Description != "get conversational by spring" {
		t.Errorf("programme = %+v, want the name and description carried through", progs[0])
	}
	if len(progs[0].Projects) != 1 || progs[0].Projects[0] != "app" {
		t.Errorf("projects = %v, want them carried through", progs[0].Projects)
	}
}

// Denying one leaves nothing behind — the proposal is a request, not a
// half-applied change.
func TestProgrammeProposal_DeniedLeavesNothing(t *testing.T) {
	svc, store := newProgrammeService(t)
	agent := svc.WithCaller(Agent())
	_, _ = agent.CreateProgramme(ProgrammeRequest{Name: "fluency"})

	pending, _ := store.ListProposals(ProposalPending)
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(pending))
	}
	if _, err := svc.ResolveProposal(pending[0].ID, false, "not yet"); err != nil {
		t.Fatalf("denying: %v", err)
	}
	if progs, _ := store.ListProgrammes(); len(progs) != 0 {
		t.Errorf("%d programmes after a DENIED proposal, want 0", len(progs))
	}
}

// TestProgrammeProposal_AmendAndDissolveRoundTrip: the entity column carries the
// programme id and the argument carries the request, and both operations have to
// survive that round trip or the confirmation fails on a human's click.
func TestProgrammeProposal_AmendAndDissolveRoundTrip(t *testing.T) {
	svc, store := newProgrammeService(t)
	agent := svc.WithCaller(Agent())
	prog, err := svc.CreateProgramme(ProgrammeRequest{Name: "fluency", Description: "old"})
	if err != nil {
		t.Fatal(err)
	}

	_, _ = agent.UpdateProgramme(prog.ID, ProgrammeRequest{Name: "fluency", Description: "new: with a colon"})
	pending, _ := store.ListProposals(ProposalPending)
	if len(pending) != 1 || pending[0].Operation != OpAmendProgramme {
		t.Fatalf("pending = %+v, want one amend proposal", pending)
	}
	if pending[0].TaskID != prog.ID {
		t.Errorf("proposal entity = %q, want the programme id %q", pending[0].TaskID, prog.ID)
	}
	if _, err := svc.ResolveProposal(pending[0].ID, true, ""); err != nil {
		t.Fatalf("confirming the amend: %v", err)
	}
	got, _ := store.GetProgramme(prog.ID)
	// The colon matters: a "split on the first separator" encoding would have
	// truncated this, which is exactly why the argument is JSON.
	if got.Description != "new: with a colon" {
		t.Errorf("description = %q, want it intact through the proposal round trip", got.Description)
	}

	// Dissolve, and the refusal still applies: a confirming human cannot wave a
	// programme away from under the Tasks that record it as their reason.
	seedTaskForProgramme(t, store, "app", prog.ID)
	_ = agent.DeleteProgramme(prog.ID)
	pending, _ = store.ListProposals(ProposalPending)
	if len(pending) != 1 || pending[0].Operation != OpDissolveProgramme {
		t.Fatalf("pending = %+v, want one dissolve proposal", pending)
	}
	if _, err := svc.ResolveProposal(pending[0].ID, true, ""); err == nil {
		t.Error("confirming a dissolve of a programme with tasks succeeded; the refusal must survive confirmation")
	}
	if _, err := store.GetProgramme(prog.ID); err != nil {
		t.Errorf("the programme was dissolved anyway: %v", err)
	}
}

// A malformed argument must fail the confirmation rather than execute a
// half-understood request on the confirming human's authority.
func TestDecodeProgrammeArgument_RefusesWhatItCannotRead(t *testing.T) {
	for _, arg := range []string{"", "not json", `{"description":"no name"}`, `{"name":"  "}`} {
		if _, err := decodeProgrammeArgument(arg); err == nil {
			t.Errorf("decodeProgrammeArgument(%q) = nil error, want a refusal", arg)
		}
	}
	req, err := decodeProgrammeArgument(encodeProgrammeArgument(
		ProgrammeRequest{Name: "a", Description: "b: c", Projects: []string{"x"}}))
	if err != nil || req.Name != "a" || req.Description != "b: c" || len(req.Projects) != 1 {
		t.Errorf("round trip = %+v (%v), want it exact", req, err)
	}
}
