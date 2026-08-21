// Copyright (C) 2026 Techdelight BV

package tui

import (
	"errors"
	"testing"

	"github.com/techdelight/daedalus/internal/control"

	tea "github.com/charmbracelet/bubbletea"
)

// fakeControl is a TaskAPI double exposing only what the approvals view uses.
type fakeControl struct {
	control.TaskAPI
	pending    []control.Task
	programmes []control.Programme
	approved   []string
	rejected   []string
	err        error
}

// ListProgrammes resolves a Task's programme id to a name for the queue (M21).
// It deliberately ignores f.err: a programme list that cannot be read must cost
// the row a name and never the row itself.
func (f *fakeControl) ListProgrammes() ([]control.Programme, error) {
	return f.programmes, nil
}

func (f *fakeControl) PlaneStatus() (control.PlaneStatus, error) {
	return control.PlaneStatus{
		Limits:        control.SchedulerLimits{Global: 4, PerProject: 2},
		GlobalRunning: 1,
	}, f.err
}

func (f *fakeControl) PendingApprovals() ([]control.Task, error) { return f.pending, f.err }

// ProgrammeBoard is the M17 board the view now renders above the queue. The
// double implements it explicitly rather than falling through to the embedded nil
// TaskAPI, which would panic — the panic is the correct signal that a view started
// calling something the double does not answer.
func (f *fakeControl) ProgrammeBoard() (control.BoardView, error) {
	if f.err != nil {
		return control.BoardView{}, f.err
	}
	return control.BoardView{Columns: []control.BoardColumn{
		{Key: "queued", Title: "Queued", Cards: nil},
		{Key: "blocked", Title: "Blocked", Cards: []control.BoardCard{
			{TaskID: "T-7", Project: "app", Objective: "wait", State: "blocked"},
		}},
	}}, nil
}

func (f *fakeControl) ApproveTask(id, _ string) (control.Task, error) {
	if f.err != nil {
		return control.Task{}, f.err
	}
	f.approved = append(f.approved, id)
	return control.Task{ID: id, State: control.StateApproved}, nil
}

func (f *fakeControl) RejectApproval(id, _ string) (control.Task, error) {
	if f.err != nil {
		return control.Task{}, f.err
	}
	f.rejected = append(f.rejected, id)
	return control.Task{ID: id, State: control.StateRejected}, nil
}

func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// M21: the row under the cursor says what the work is FOR.
//
// The reviewer is handed the objective, the rationale and the programme; this
// queue handed the human the objective. The author of the reason is shown with
// it, because "the agent said this was worth doing" and "you said so" are
// different pieces of evidence and the queue must not blur them.
func TestApprovals_ShowIntentUnderTheCursor(t *testing.T) {
	fake := &fakeControl{
		pending: []control.Task{
			{ID: "T-1", Project: "app", Objective: "add dark mode", ProgrammeID: "PR-1",
				Rationale: "three projects grew their own theming", RationaleBy: control.CallerAgent},
			{ID: "T-2", Project: "api", Objective: "fix the leak"},
		},
		programmes: []control.Programme{{ID: "PR-1", Name: "fluency"}},
	}
	msg := loadApprovals(fake)().(approvalsLoadedMsg)
	if msg.rows[0].programme != "fluency" {
		t.Errorf("programme = %q, want the NAME resolved from PR-1", msg.rows[0].programme)
	}

	m := tuiModel{control: fake, approving: true}
	updated, _ := m.Update(msg)
	view := updated.(tuiModel).viewApprovals()
	for _, want := range []string{"for: fluency", "three projects grew their own theming", "(agent)"} {
		if !contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}

	// Move to the task with no programme and no reason. The absence is STATED:
	// deciding on the objective alone is a fact about the decision, and a blank
	// space says nothing at all.
	moved, _ := updated.(tuiModel).Update(key("j"))
	view = moved.(tuiModel).viewApprovals()
	if !contains(view, "no programme, no recorded reason") {
		t.Errorf("an unattributed task should say so:\n%s", view)
	}
}

// An unresolvable programme id must still appear. Showing "PR-3" is worse than
// showing "fluency" and far better than showing nothing, which would report a
// task that serves a programme as one that serves none.
func TestApprovals_UnknownProgrammeShowsItsID(t *testing.T) {
	fake := &fakeControl{pending: []control.Task{
		{ID: "T-1", Project: "app", Objective: "x", ProgrammeID: "PR-9"},
	}}
	msg := loadApprovals(fake)().(approvalsLoadedMsg)
	if msg.rows[0].programme != "PR-9" {
		t.Errorf("programme = %q, want the id to stand in for the missing name", msg.rows[0].programme)
	}
}

func TestApprovals_LoadAndRender(t *testing.T) {
	fake := &fakeControl{pending: []control.Task{
		{ID: "T-1", Project: "app", Objective: "add dark mode"},
		{ID: "T-2", Project: "api", Objective: "fix the leak"},
	}}
	msg := loadApprovals(fake)().(approvalsLoadedMsg)
	if !msg.available || len(msg.rows) != 2 {
		t.Fatalf("loadApprovals = %+v, want 2 available rows", msg)
	}

	m := tuiModel{control: fake, approving: true}
	updated, _ := m.Update(msg)
	m = updated.(tuiModel)
	view := m.viewApprovals()
	for _, want := range []string{"T-1", "add dark mode", "T-2", "[a]pprove", "[x] reject"} {
		if !contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
	// The M17 board sits above the queue. The EMPTY column has to be there too:
	// "nothing is queued" is an answer, and a column that vanishes when it empties
	// reads as a missing feature.
	if len(msg.board) != 2 {
		t.Fatalf("board summary = %+v, want both columns including the empty one", msg.board)
	}
	for _, want := range []string{"Programme board", "Queued", "Blocked", "T-7"} {
		if !contains(view, want) {
			t.Errorf("view missing board content %q:\n%s", want, view)
		}
	}
}

// TestApprovals_UnavailableIsNotEmpty: an unreachable control plane must say so,
// not render as "nothing awaiting approval".
func TestApprovals_UnavailableIsNotEmpty(t *testing.T) {
	msg := loadApprovals(nil)().(approvalsLoadedMsg)
	if msg.available {
		t.Fatal("a nil control client should report unavailable")
	}
	m := tuiModel{approving: true}
	updated, _ := m.Update(msg)
	view := updated.(tuiModel).viewApprovals()
	if !contains(view, "not reachable") {
		t.Errorf("the view should explain the plane is unreachable:\n%s", view)
	}
	if contains(view, "Nothing is awaiting approval") {
		t.Error("an unreachable plane must not be rendered as an empty queue")
	}
}

func TestApprovals_ApproveAndRejectKeys(t *testing.T) {
	fake := &fakeControl{pending: []control.Task{
		{ID: "T-1", Project: "app"}, {ID: "T-2", Project: "api"},
	}}
	m := tuiModel{control: fake, approving: true}
	updated, _ := m.Update(loadApprovals(fake)())
	m = updated.(tuiModel)

	// [a] approves the row under the cursor.
	_, cmd := m.updateApprovals(key("a"))
	if cmd == nil {
		t.Fatal("[a] should issue a decision command")
	}
	if res, ok := cmd().(approvalDecidedMsg); !ok || res.err != nil {
		t.Fatalf("approve = %+v, want a successful decision", res)
	}
	if len(fake.approved) != 1 || fake.approved[0] != "T-1" {
		t.Errorf("approved = %v, want [T-1]", fake.approved)
	}

	// Move down, then [x] rejects.
	moved, _ := m.updateApprovals(tea.KeyMsg{Type: tea.KeyDown})
	m = moved.(tuiModel)
	_, cmd = m.updateApprovals(key("x"))
	if cmd == nil {
		t.Fatal("[x] should issue a decision command")
	}
	cmd()
	if len(fake.rejected) != 1 || fake.rejected[0] != "T-2" {
		t.Errorf("rejected = %v, want [T-2]", fake.rejected)
	}
}

func TestApprovals_DecisionErrorSurfaces(t *testing.T) {
	fake := &fakeControl{err: errors.New("refused by policy")}
	cmd := decideApproval(fake, "T-1", true)
	msg, ok := cmd().(approvalDecidedMsg)
	if !ok || msg.err == nil {
		t.Fatalf("decideApproval = %+v, want an error", msg)
	}
	m := tuiModel{control: fake}
	updated, _ := m.Update(msg)
	if !contains(updated.(tuiModel).statusMsg, "refused by policy") {
		t.Errorf("the failure should reach the status line: %q", updated.(tuiModel).statusMsg)
	}
}

func TestApprovals_QuitReturnsToBrowse(t *testing.T) {
	m := tuiModel{approving: true}
	updated, _ := m.updateApprovals(key("q"))
	if updated.(tuiModel).approving {
		t.Error("[q] should close the approvals view")
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && stringsIndex(haystack, needle) >= 0)
}

func stringsIndex(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
