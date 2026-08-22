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
//
// It carries the PROGRAMME and the RATIONALE as well as the objective (M21). The
// reviewer agent is given all three before it writes a word; this queue used to
// give the person who actually decides only the last one.
type approvalRow struct {
	id        string
	project   string
	objective string
	programme string // the programme's NAME, resolved; empty when it serves none
	rationale string
	// rationaleBy is the caller class that authored the reason. It is shown, not
	// hidden, because a reason an agent drafted is weaker evidence of intent than
	// one a human wrote, and the person deciding is the one who should weigh that.
	rationaleBy string
}

// approvalsLoadedMsg carries the result of a refresh, plus the plane's
// concurrency picture — with several Jobs able to run at once, "what is running"
// is no longer implied by the project list.
type approvalsLoadedMsg struct {
	rows      []approvalRow
	available bool
	reason    string
	plane     planeLine
	// board is the cross-project column summary (M17). It is derived from the same
	// control-plane state as the queue below it, so the two can never disagree.
	board []boardLine
}

// boardLine is one programme-board column, reduced to what fits on a TUI row.
type boardLine struct {
	// key is the plane's stable column key (board.go). Kept alongside the title
	// because one column — the landed one — is worth saying more about than its
	// count, and matching on a human-readable title would break the moment one is
	// reworded.
	key   string
	title string
	count int
	// detail names the first few tasks, so a column is not just a number: "3
	// blocked" without saying which three is a prompt to go and look somewhere
	// else, which is what the board exists to avoid.
	detail string
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
				reason:    "daedalus-control is not running (start it with `daedalus control start`)",
			}
		}
		tasks, err := api.PendingApprovals()
		if err != nil {
			return approvalsLoadedMsg{available: false, reason: err.Error()}
		}
		// One list call for the names, and a failure to read them costs a name
		// rather than the queue: an approval that vanished because a lookup failed
		// would read as "nothing needs you".
		names := map[string]string{}
		if progs, perr := api.ListProgrammes(); perr == nil {
			for _, p := range progs {
				names[p.ID] = p.Name
			}
		}
		rows := make([]approvalRow, 0, len(tasks))
		for _, t := range tasks {
			row := approvalRow{
				id: t.ID, project: t.Project, objective: t.Objective,
				rationale: t.Rationale, rationaleBy: string(t.RationaleBy),
			}
			if t.ProgrammeID != "" {
				row.programme = names[t.ProgrammeID]
				if row.programme == "" {
					row.programme = t.ProgrammeID
				}
			}
			rows = append(rows, row)
		}
		msg := approvalsLoadedMsg{rows: rows, available: true}
		if st, err := api.PlaneStatus(); err == nil {
			msg.plane = planeLine{
				known: true, globalRunning: st.GlobalRunning,
				globalLimit: st.Limits.Global, waiting: len(st.Waiting),
			}
		}
		if view, err := api.ProgrammeBoard(); err == nil {
			msg.board = summariseBoard(view)
		}
		return msg
	}
}

// summariseBoard reduces the board to one line per column.
//
// EVERY column is kept, including the empty ones: a column that disappeared when
// it emptied would make "nothing is blocked" indistinguishable from "the board
// forgot about blocking".
func summariseBoard(view control.BoardView) []boardLine {
	out := make([]boardLine, 0, len(view.Columns))
	for _, col := range view.Columns {
		line := boardLine{key: col.Key, title: col.Title, count: len(col.Cards)}
		for i, c := range col.Cards {
			if i == 3 {
				line.detail += fmt.Sprintf(" +%d more", len(col.Cards)-3)
				break
			}
			if i > 0 {
				line.detail += " "
			}
			line.detail += c.TaskID
		}
		out = append(out, line)
	}
	return out
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

	add(titleStyle.Render("  Programme board"))
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
	for _, line := range m.board {
		row := fmt.Sprintf("  %-22s %3d", line.title, line.count)
		if line.detail != "" {
			row += "  " + line.detail
		}
		add(normalStyle.Render(row))
		add("\n")
		// "Landed" is the one column that reads as a finished job and is not one:
		// the plane lands on its own ref, so the work is nowhere the reader's branch
		// can see it. The CLI says so when it lands; this board said the word and
		// stopped there, which is exactly how "it landed and nothing changed"
		// happens. In the plane's own words, so the surfaces cannot differ.
		//
		// UNDER THE COLUMN IT EXPLAINS, and unconditionally (RV-8). It used to be
		// emitted after the whole loop and gated on `landed > 0`, and both were
		// wrong: the note printed under the LAST column rather than under Landed,
		// and the gate never fired because the board has no recency window — every
		// task ever integrated stays in the column, so on any project with history
		// the count is never zero. The gate was justified as avoiding a permanent
		// footnote and produced one, two rows away from its subject.
		if line.key == control.BoardLanded {
			add(helpStyle.Render("      " + control.LandedNote))
			add("\n")
		}
	}
	if len(m.board) > 0 {
		add("\n")
	}
	add(titleStyle.Render("  Awaiting approval"))
	add("\n")
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
		// Why this work exists, under the row the cursor is on. Under one row and
		// not all of them: a queue where every entry is three lines is a queue you
		// scroll rather than read, and the intent matters at the moment of
		// deciding, which is where the cursor is.
		if i == m.approvalCursor {
			if row.programme != "" {
				add(helpStyle.Render("      for: " + truncateTUI(row.programme, 60)))
				add("\n")
			}
			if row.rationale != "" {
				by := ""
				if row.rationaleBy != "" {
					by = "  (" + row.rationaleBy + ")"
				}
				add(helpStyle.Render("      reason: " + truncateTUI(row.rationale, 56) + by))
				add("\n")
			}
			if row.programme == "" && row.rationale == "" {
				// Stated rather than left blank: deciding on the objective alone is a
				// fact about the decision, not an empty field.
				add(helpStyle.Render("      no programme, no recorded reason"))
				add("\n")
			}
		}
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
