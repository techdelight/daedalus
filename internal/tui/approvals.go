// Copyright (C) 2026 Techdelight BV

package tui

import (
	"fmt"
	"net"
	"time"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/control"

	tea "github.com/charmbracelet/bubbletea"
)

// The pending-approvals view: the TUI half of the control plane's human approval
// gate (docs/control-plane.md).
//
// Like the Web surface it is a CLIENT of control.sock with no authority the CLI
// lacks, and it never spawns the control daemon — a dashboard that started a
// daemon because somebody pressed a key would be a surprising side effect. With
// no daemon running the view says so rather than showing an empty queue, because
// "nothing needs you" and "I could not ask" are very different answers.

// approvalRow is one task awaiting a human decision.
type approvalRow struct {
	id        string
	project   string
	objective string
}

// approvalsLoadedMsg carries the result of a refresh, plus the plane's
// concurrency picture — with several Jobs able to run at once, "what is running"
// is no longer implied by the project list.
type approvalsLoadedMsg struct {
	rows      []approvalRow
	available bool
	reason    string
	plane     planeLine
}

// planeLine is the one-line concurrency summary.
type planeLine struct {
	known         bool
	globalRunning int
	globalLimit   int
	waiting       int
}

// approvalDecidedMsg is the outcome of an approve/reject keypress.
type approvalDecidedMsg struct {
	msg string
	err error
}

// dialControlPlane returns a control client if the daemon is ALREADY listening,
// and nil otherwise. Deliberately not control.EnsureRunning.
func dialControlPlane(cfg *core.Config) control.TaskAPI {
	if cfg == nil {
		return nil
	}
	sock := control.DefaultSocketPath(cfg.DataDir)
	conn, err := net.DialTimeout("unix", sock, 300*time.Millisecond)
	if err != nil {
		return nil
	}
	_ = conn.Close()
	return control.NewClient(sock)
}

// loadApprovals fetches the pending-approval queue.
func loadApprovals(api control.TaskAPI) tea.Cmd {
	return func() tea.Msg {
		if api == nil {
			return approvalsLoadedMsg{
				available: false,
				reason:    "daedalus-control is not running (start it with `daedalus task list`)",
			}
		}
		tasks, err := api.PendingApprovals()
		if err != nil {
			return approvalsLoadedMsg{available: false, reason: err.Error()}
		}
		rows := make([]approvalRow, 0, len(tasks))
		for _, t := range tasks {
			rows = append(rows, approvalRow{id: t.ID, project: t.Project, objective: t.Objective})
		}
		msg := approvalsLoadedMsg{rows: rows, available: true}
		if st, err := api.PlaneStatus(); err == nil {
			msg.plane = planeLine{
				known: true, globalRunning: st.GlobalRunning,
				globalLimit: st.Limits.Global, waiting: len(st.Waiting),
			}
		}
		return msg
	}
}

// decideApproval approves or rejects a task.
func decideApproval(api control.TaskAPI, id string, approve bool) tea.Cmd {
	return func() tea.Msg {
		if api == nil {
			return approvalDecidedMsg{err: fmt.Errorf("daedalus-control is not running")}
		}
		var (
			t   control.Task
			err error
		)
		if approve {
			t, err = api.ApproveTask(id, "approved from the TUI")
		} else {
			t, err = api.RejectApproval(id, "rejected from the TUI")
		}
		if err != nil {
			return approvalDecidedMsg{err: err}
		}
		verb := "approved"
		if !approve {
			verb = "rejected"
		}
		return approvalDecidedMsg{msg: fmt.Sprintf("Task %s %s (state %s)", t.ID, verb, t.State)}
	}
}

// updateApprovals handles keys while the approvals view is open.
func (m tuiModel) updateApprovals(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		m.approving = false
		m.statusMsg = ""
		return m, nil

	case "j", "down":
		if m.approvalCursor < len(m.approvals)-1 {
			m.approvalCursor++
		}
		return m, nil

	case "k", "up":
		if m.approvalCursor > 0 {
			m.approvalCursor--
		}
		return m, nil

	case "r":
		return m, loadApprovals(m.control)

	case "a", "enter":
		if len(m.approvals) == 0 {
			return m, nil
		}
		return m, decideApproval(m.control, m.approvals[m.approvalCursor].id, true)

	case "x":
		if len(m.approvals) == 0 {
			return m, nil
		}
		return m, decideApproval(m.control, m.approvals[m.approvalCursor].id, false)
	}
	return m, nil
}

// viewApprovals renders the queue.
func (m tuiModel) viewApprovals() string {
	var b []byte
	add := func(s string) { b = append(b, s...) }

	add(titleStyle.Render("  Awaiting approval"))
	add("\n")
	if m.plane.known {
		limit := "∞"
		if m.plane.globalLimit > 0 {
			limit = fmt.Sprintf("%d", m.plane.globalLimit)
		}
		line := fmt.Sprintf("  plane: %d/%s jobs running", m.plane.globalRunning, limit)
		if m.plane.waiting > 0 {
			line += fmt.Sprintf(", %d queued for capacity", m.plane.waiting)
		}
		add(helpStyle.Render(line))
		add("\n")
	}
	add("\n")
	if !m.approvalsAvailable {
		add(normalStyle.Render("  The control plane is not reachable."))
		add("\n")
		if m.approvalsReason != "" {
			add(helpStyle.Render("  " + m.approvalsReason))
			add("\n")
		}
		add("\n")
		add(helpStyle.Render("  [r]efresh  [q] back"))
		return string(b)
	}
	if len(m.approvals) == 0 {
		add(normalStyle.Render("  Nothing is awaiting approval."))
		add("\n\n")
		add(helpStyle.Render("  [r]efresh  [q] back"))
		return string(b)
	}
	for i, row := range m.approvals {
		prefix := "   "
		if i == m.approvalCursor {
			prefix = " > "
		}
		line := fmt.Sprintf("%s%-6s  %-16s  %s", prefix, row.id, truncateTUI(row.project, 16), truncateTUI(row.objective, 46))
		if i == m.approvalCursor {
			add(selectedStyle.Render(line))
		} else {
			add(normalStyle.Render(line))
		}
		add("\n")
	}
	add("\n")
	add(helpStyle.Render("  [a]pprove  [x] reject  [r]efresh  [q] back"))
	return string(b)
}

// truncateTUI shortens s to at most n runes for table display.
func truncateTUI(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
