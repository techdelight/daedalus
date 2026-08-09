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
	pending  []control.Task
	approved []string
	rejected []string
	err      error
}

func (f *fakeControl) PlaneStatus() (control.PlaneStatus, error) {
	return control.PlaneStatus{
		Limits:        control.SchedulerLimits{Global: 4, PerProject: 2},
		GlobalRunning: 1,
	}, f.err
}

func (f *fakeControl) PendingApprovals() ([]control.Task, error) { return f.pending, f.err }

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
