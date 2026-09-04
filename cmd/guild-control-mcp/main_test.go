// Copyright (C) 2026 Techdelight BV

package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/control"
)

// The first tests this command has ever had, and they cover the one function in
// it that can destroy something.
//
// An amendment from the Guild Master is a PROPOSAL, so a human sees the result
// before it takes effect — but they see the finished programme, not a patch, and
// a field the merge dropped is a field that reads as deliberately emptied. The
// rule is that an omitted field is kept.

func programme() control.Programme {
	return control.Programme{
		ID: "PR-1", Name: "fluency", Description: "one way to theme, everywhere",
		Projects: []string{"app", "other"},
		Deps:     []core.DependencyEdge{{Upstream: "other", Downstream: "app"}},
	}
}

func TestMergeProgramme_AnOmittedFieldIsKept(t *testing.T) {
	cur := programme()
	got := mergeProgramme(cur, AmendProgrammeInput{
		Programme:   "fluency",
		Description: "one way to theme, everywhere — and one place to change it",
	})

	if got.Description == cur.Description {
		t.Error("the field that WAS supplied should have changed")
	}
	// The three that were not supplied. This is the data-loss case: an agent
	// fixing a sentence must not empty the programme.
	if got.Name != "fluency" {
		t.Errorf("name = %q, want it kept", got.Name)
	}
	if len(got.Projects) != 2 {
		t.Errorf("projects = %v, want both kept", got.Projects)
	}
	if len(got.Deps) != 1 {
		t.Errorf("deps = %v, want the declared order kept", got.Deps)
	}
}

// A name that is only whitespace is not a name. Treating it as one would rename
// a programme to nothing on a caller's stray space, and the name is what a
// person types at the CLI.
func TestMergeProgramme_BlankStringsAreNotAmendments(t *testing.T) {
	cur := programme()
	got := mergeProgramme(cur, AmendProgrammeInput{Programme: "fluency", Name: "   "})
	if got.Name != "fluency" {
		t.Errorf("name = %q, want the whitespace ignored", got.Name)
	}
}

// The Guild Master can now propose the ORDER as well as the membership. It was
// missing from the first version of the tool, which left the agent able to say
// which projects a programme draws on and not which of them goes first —
// noticing exactly that is what cross-project sight is for.
func TestMergeProgramme_CanAmendTheDeclaredOrder(t *testing.T) {
	cur := programme()
	got := mergeProgramme(cur, AmendProgrammeInput{
		Programme: "fluency",
		Deps: []ProgrammeEdgeInput{
			{Upstream: "app", Downstream: "other"},
			{Upstream: "other", Downstream: "third"},
		},
	})
	if len(got.Deps) != 2 {
		t.Fatalf("deps = %v, want both edges", got.Deps)
	}
	if got.Deps[0].Upstream != "app" || got.Deps[0].Downstream != "other" {
		t.Errorf("first edge = %+v, want app → other", got.Deps[0])
	}
	// Supplying deps must not disturb the membership.
	if len(got.Projects) != 2 {
		t.Errorf("projects = %v, want them untouched by a deps amendment", got.Projects)
	}
}

// An EMPTY list is an amendment and an ABSENT one is not. Collapsing the two
// would make "remove every declared edge" unexpressible, and the difference is
// the reason this takes a parsed struct rather than a map of raw JSON.
func TestMergeProgramme_AnEmptyListClearsAndNilKeeps(t *testing.T) {
	cur := programme()

	cleared := mergeProgramme(cur, AmendProgrammeInput{
		Programme: "fluency", Deps: []ProgrammeEdgeInput{}, Projects: []string{},
	})
	if len(cleared.Deps) != 0 || len(cleared.Projects) != 0 {
		t.Errorf("an explicit empty list should clear: deps=%v projects=%v",
			cleared.Deps, cleared.Projects)
	}

	kept := mergeProgramme(cur, AmendProgrammeInput{Programme: "fluency"})
	if len(kept.Deps) != 1 || len(kept.Projects) != 2 {
		t.Errorf("an absent list should keep: deps=%v projects=%v", kept.Deps, kept.Projects)
	}
}

// The Guild Master can finally say what its work is FOR (#88).
//
// Every task it filed was an orphan until this: the plane has carried a
// programme and a rationale on CreateTaskRequest since Sprint 66, a human's CLI
// has passed them since, and the agent's own tool dropped both on the floor.
// Same shape as #82 and #85 — the plane supports it and the agent cannot reach
// it — so the pass-through is tested rather than assumed.
func TestCreateTaskRequest_CarriesTheProgrammeAndTheReason(t *testing.T) {
	got := createTaskRequest(CreateTaskInput{
		Project: "app", Objective: "unify the theming",
		Programme: "fluency", Rationale: "three projects grew their own",
	})
	if got.Programme != "fluency" {
		t.Errorf("programme = %q, want it passed through UNRESOLVED for the plane to match", got.Programme)
	}
	if got.Rationale != "three projects grew their own" {
		t.Errorf("rationale = %q, want it carried", got.Rationale)
	}
	if got.Project != "app" || got.Objective != "unify the theming" {
		t.Errorf("the original fields must survive: %+v", got)
	}
}

// Both fields are optional. A task with no stated reason should be visibly
// unattributed rather than impossible to file — requiring them would only make
// an agent invent a programme to satisfy a field.
func TestCreateTaskRequest_ProgrammeAndReasonAreOptional(t *testing.T) {
	got := createTaskRequest(CreateTaskInput{Project: "app", Objective: "x"})
	if got.Programme != "" || got.Rationale != "" {
		t.Errorf("nothing should be invented: %+v", got)
	}
}

// An all-zero Budget is not "no budget" to the plane — it reads as a request for
// zero attempts. So one is attached only when the agent actually narrowed
// something; otherwise a task is filed that can never run.
func TestCreateTaskRequest_BudgetOnlyWhenNarrowed(t *testing.T) {
	if got := createTaskRequest(CreateTaskInput{Project: "app", Objective: "x"}); got.Budget != nil {
		t.Errorf("no narrowing should mean no budget, got %+v", got.Budget)
	}
	got := createTaskRequest(CreateTaskInput{Project: "app", Objective: "x", MaxAttempts: 2})
	if got.Budget == nil || got.Budget.MaxAttempts != 2 {
		t.Errorf("a narrowed budget must reach the plane: %+v", got.Budget)
	}
}

// THE GUILD MASTER CAN SAY WHAT A TASK WILL PRODUCE (#95).
//
// The tasks it filed were milestone-sized paragraphs in `objective` with nothing
// on them anybody could check off — reported by the operator reading them as "a
// big blob of text about what to do for a milestone with no clear deliverables".
// The plane carries the list; this is the pass-through that lets the agent fill
// it, tested rather than assumed for the reason #88's was.
func TestCreateTaskRequest_CarriesTheDeliverables(t *testing.T) {
	got := createTaskRequest(CreateTaskInput{
		Project: "app", Objective: "add a --since flag to task list",
		Deliverables: []string{
			"`daedalus task list --since 7d` runs and filters by age",
			"--since appears in the man page",
		},
	})
	if len(got.Deliverables) != 2 {
		t.Fatalf("deliverables = %v, want both carried to the plane", got.Deliverables)
	}
	if got.Deliverables[1] != "--since appears in the man page" {
		t.Errorf("order and content must survive: %v", got.Deliverables)
	}
}

// Optional, like the programme and the reason. A thin task should be visibly
// thin rather than impossible to file — and the create is answered with advice
// saying so, which is the part that actually teaches.
func TestCreateTaskRequest_DeliverablesAreOptional(t *testing.T) {
	if got := createTaskRequest(CreateTaskInput{Project: "app", Objective: "x"}); got.Deliverables != nil {
		t.Errorf("nothing should be invented: %+v", got.Deliverables)
	}
}

// THE AGENT CAN REACH THE OPERATIONS THE AUTHORITY TABLE TIERS FOR IT (#82's
// lesson, applied to #79b).
//
// `list_adoptions` is TierAllowed and `adopt_landed` is TierProposal, and a tier
// is a reservation of authority over something that has to EXIST. #82 is on the
// backlog because three operations were tiered, documented in a milestone's
// deliverable list and marked done while no agent surface could reach any of
// them. So the registration is asserted rather than assumed: this is the test
// that fails if the tools are ever dropped while the tiers stay.
func TestRegisterControlTools_TheAdoptionToolsExist(t *testing.T) {
	session := connect(t, stubAdopting{})
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	tools := map[string]bool{}
	for _, tool := range listed.Tools {
		tools[tool.Name] = true
	}
	for _, want := range []string{"list_adoptions", "request_adoption"} {
		if !tools[want] {
			t.Errorf("no %s tool: the operation is tiered for this caller and reachable from nowhere it can stand", want)
		}
	}
}

// What the agent actually receives: one row per project, the branch, the gap and
// the tasks waiting in it — and NO host path. /adoptions is granted to this
// caller on exactly that basis (see authority.go), so the tool that renders it
// is where the promise is kept or broken.
func TestListAdoptions_OneRowPerProjectAndNothingAboutTheHost(t *testing.T) {
	session := connect(t, stubAdopting{list: []control.Adoption{{
		Project: "app", Projects: []string{"app", "docs"}, Branch: "main",
		TargetSHA: "ccccccccccc33333", HeadSHA: "bbbbbbbbbbb22222",
		Behind: 2, Waiting: []string{"T-3", "T-4"}, Adoptable: true, Pending: true,
		Note: "main is 2 commits behind the landed commit ccccccc",
	}}})
	res, err := session.CallTool(context.Background(),
		&mcp.CallToolParams{Name: "list_adoptions"})
	if err != nil {
		t.Fatalf("CallTool(list_adoptions): %v", err)
	}
	if res.IsError {
		t.Fatalf("list_adoptions answered an error: %+v", res.Content)
	}
	body, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var got AdoptionsOutput
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Adoptions) != 1 {
		t.Fatalf("adoptions = %+v, want one row for the one checkout", got.Adoptions)
	}
	a := got.Adoptions[0]
	if a.Branch != "main" || a.Behind != 2 || len(a.Waiting) != 2 || !a.Pending {
		t.Errorf("row = %+v; the agent needs the branch, the gap and what is waiting in it", a)
	}
	if len(a.Projects) != 2 {
		t.Errorf("projects = %v; a shared checkout must name both, or one project's landing reads as lost",
			a.Projects)
	}
	if a.Note == "" {
		t.Error("the plane's own sentence is missing, which is the part that can be repeated to a human")
	}
	// AND NOTHING ELSE. The fields are listed rather than the paths excluded,
	// because the failure this guards against is a future one: somebody widening
	// the view with the checkout directory that keyed the row, which is exactly
	// the kind of field that gets added for debugging and stays. An agent's view
	// of the guild's filesystem should have to be argued for, not arrived at.
	allowed := map[string]bool{
		"project": true, "projects": true, "branch": true, "behind": true,
		"waiting": true, "pending": true, "adopted": true, "adoptable": true, "note": true,
	}
	var raw []map[string]any
	if err := json.Unmarshal(body, &struct{ Adoptions *[]map[string]any }{&raw}); err != nil {
		t.Fatal(err)
	}
	for key := range raw[0] {
		if !allowed[key] {
			t.Errorf("the adoption view has grown a %q field; it carries a project, a branch and a gap, "+
				"and the authority table grants this read on that basis", key)
		}
	}
}

// connect registers the real tools on a real server and returns a client session
// talking to it. Registration through the SDK rather than a source grep, because
// what #82 needed was proof the agent can CALL the thing.
func connect(t *testing.T, api control.TaskAPI) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := newServer(api).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).
		Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// stubAdopting answers the two adoption calls: one project behind its branch,
// and a write that comes back as a proposal — what an agent caller actually
// sees.
type stubAdopting struct {
	control.TaskAPI
	list []control.Adoption
	err  error
}

func (s stubAdopting) Adoptions() ([]control.Adoption, error) { return s.list, s.err }

func (s stubAdopting) AdoptLanded(project string) (control.AdoptionResult, error) {
	return control.AdoptionResult{}, &control.RejectionError{
		Reason:  control.ReasonProposalRecorded,
		Message: "recorded as proposal P-4 for a human to confirm",
		Entity:  project,
	}
}

// A proposal is NOT reported as a success. An agent that believed it had moved a
// human's branch would go on reasoning from a false premise — and this one is
// worse than most, because the premise is about a file tree the plane does not
// own.
func TestAdoptionOutcome_AProposalIsNotASuccess(t *testing.T) {
	_, err := stubAdopting{}.AdoptLanded("app")
	out := outcomeFor(err)
	if out.Executed {
		t.Error("a recorded proposal was reported as an executed adoption")
	}
	if out.Reason != string(control.ReasonProposalRecorded) {
		t.Errorf("reason = %q, want %q so the agent can tell 'a human must confirm' from 'refused'",
			out.Reason, control.ReasonProposalRecorded)
	}
	if !strings.Contains(out.Detail, "recorded as a proposal") {
		t.Errorf("detail = %q; it should say plainly that nothing moved", out.Detail)
	}
}

// stubLags is a TaskAPI double answering only TargetLags.
type stubLags struct {
	control.TaskAPI
	lags []control.TargetLag
	err  error
}

func (s stubLags) TargetLags() ([]control.TargetLag, error) { return s.lags, s.err }

// THE AGENT IS TOLD WHEN THE BASE IT JUST FROZE AT IS BEHIND (#89, agent half).
//
// The plane pins a Task to the integration target, and a target that has fallen
// behind changes both the tree the agent works from AND — because the acceptance
// policy is read from the base commit — the oracle the work is graded against. A
// human running `task create` has been warned since #89; the agent's tool said
// nothing, which is #82/#85/#88's shape one turn on.
func TestStaleTargetNote_SaysSoAndOnlyWhenItIsTrue(t *testing.T) {
	behind := control.TargetLag{Project: "app", Behind: 7,
		TargetSHA: "aaaaaaaaaaaa1111", HeadSHA: "bbbbbbbbbbbb2222"}
	current := control.TargetLag{Project: "app"}

	note := staleTargetNote(stubLags{lags: []control.TargetLag{behind}}, "app")
	if note == "" {
		t.Fatal("a target 7 commits behind produced no note, so an agent files against a stale " +
			"base and a policy nobody chose, exactly as before #89")
	}
	for _, want := range []string{"7 commits behind", "acceptance policy", "human"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note does not mention %q: %s", want, note)
		}
	}

	// A target that is current says NOTHING. A warning that fires always is a
	// warning that is read never.
	if note := staleTargetNote(stubLags{lags: []control.TargetLag{current}}, "app"); note != "" {
		t.Errorf("a current target was reported as stale: %s", note)
	}
	// Another project's lag is not this project's.
	if note := staleTargetNote(stubLags{lags: []control.TargetLag{behind}}, "other"); note != "" {
		t.Errorf("another project's lag leaked into this one: %s", note)
	}
	// A plane that cannot answer must not turn a successful create into a scare.
	if note := staleTargetNote(stubLags{err: errors.New("unreachable")}, "app"); note != "" {
		t.Errorf("an unreadable plane produced a note: %s", note)
	}
}
