// Copyright (C) 2026 Techdelight BV

package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/techdelight/daedalus/internal/control"
)

// THE LEDGER, IN A REAL BROWSER.
//
// Every other test in this package asserts on the JavaScript as TEXT: that a
// field is rendered somewhere, that a flag is not read page-wide. Those catch
// deletion and drift and cannot catch a page that throws on load, a selector
// that matches nothing, or a button that is disabled when it should not be —
// which is most of what a UI gets wrong.
//
// This one serves the real page over the real routes, against a FAKE CONTROL
// PLANE, and drives it with Playwright. The plane is faked rather than run
// because the behaviour under test is the page's: which entries lock while a
// command is out, what a review looks like when it is rendered, whether an
// address opens the entry it names. A real plane would add Docker, an agent and
// several minutes to answer questions about a div.
//
// SKIPPED unless RUN_PLAYWRIGHT_LEDGER=1 and a python with playwright is on
// PLAYWRIGHT_PYTHON, because neither is a reasonable thing to require of
// `go test ./...` — the same bargain playwright_test.go already makes.
func TestLedgerInABrowser(t *testing.T) {
	if os.Getenv("RUN_PLAYWRIGHT_LEDGER") != "1" {
		t.Skip("RUN_PLAYWRIGHT_LEDGER not set — skipping the browser test")
	}
	py := os.Getenv("PLAYWRIGHT_PYTHON")
	if py == "" {
		t.Skip("PLAYWRIGHT_PYTHON not set — skipping the browser test")
	}
	// The driver lives in the repo; the env var is an override for iterating on
	// a copy without editing the committed one.
	script := os.Getenv("PLAYWRIGHT_SCRIPT")
	if script == "" {
		script = filepath.Join("..", "..", "e2e", "ledger.py")
	}

	srv := ledgerTestServer(t)
	defer srv.Close()

	cmd := exec.Command(py, script)
	cmd.Env = append(os.Environ(), "LEDGER_URL="+srv.URL)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("the browser run failed: %v", err)
	}
}

// ledgerTestServer serves the page and the control routes over a scripted plane.
func ledgerTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	ws, _ := setupWebTest(t)
	ws.control = newScriptedPlane()

	mux := http.NewServeMux()
	ws.RegisterRoutes(mux)
	RegisterAppRoutes(mux, "test")
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	return httptest.NewServer(mux)
}

// --- the scripted plane -----------------------------------------------------

// scriptedPlane is a control.TaskAPI with fixed answers and one deliberately
// SLOW operation.
//
// The slow review is the whole point of the fixture: the complaint this suite
// exists to check ("the ledger locks all buttons for all tasks if I start a
// review") is only observable while a command is outstanding, and a fake that
// answered instantly would never show it.
type scriptedPlane struct {
	control.TaskAPI // everything not overridden is unused and must never be called

	mu sync.Mutex
	// reviewing is the task ids whose review is currently in flight, so
	// TaskStatus can report the operation the way the real plane does.
	reviewing map[string]bool
	// adopted is the projects whose branch this fake has been asked to move, so
	// the adoption rows change after the button is pressed (see Adoptions).
	adopted map[string]bool
	refined []control.RefineRequest
	created []control.CreateTaskRequest
}

func newScriptedPlane() *scriptedPlane {
	return &scriptedPlane{reviewing: map[string]bool{}, adopted: map[string]bool{}}
}

const slowReview = 2500 * time.Millisecond

func (p *scriptedPlane) ProgrammeBoard() (control.BoardView, error) {
	return control.BoardView{
		Columns: []control.BoardColumn{
			{Key: "awaiting_approval", Title: "Awaiting your seal", Cards: []control.BoardCard{
				{TaskID: "T-1", Project: "app", Objective: "Add cursor pagination to GET /items", State: "approval_required"},
				{TaskID: "T-2", Project: "app", Objective: "Rename the theming tokens", State: "approval_required"},
			}},
			{Key: "landed", Title: "Landed", Cards: []control.BoardCard{
				{TaskID: "T-3", Project: "docs", Objective: "Write the pagination guide", State: "integrated"},
			}},
		},
		Plane:            control.PlaneStatus{GlobalRunning: 0},
		PendingApprovals: 2,
	}, nil
}

func (p *scriptedPlane) PendingApprovals() ([]control.Task, error) {
	return []control.Task{p.task("T-1"), p.task("T-2")}, nil
}

func (p *scriptedPlane) ListProposals(control.ProposalState) ([]control.Proposal, error) {
	return nil, nil
}

func (p *scriptedPlane) ListProgrammes() ([]control.Programme, error) { return nil, nil }

func (p *scriptedPlane) ProjectTargets() ([]control.TargetView, error) { return nil, nil }

func (p *scriptedPlane) TargetLags() ([]control.TargetLag, error) { return nil, nil }

// One project with landed work its branch has not taken, and one that is up to
// date — the two shapes the Landed column has to tell apart. `docs` holds two
// landed tasks and offers ONE adoption, which is the property a per-task
// rendering would break.
//
// STATEFUL, unlike most of this fixture: once `docs` has been adopted it reports
// itself as adopted. The page draws only the rows the plane marks PENDING, so
// what happens to a row when its work is taken — it stops offering the plate,
// and then leaves the column — is a behaviour a fixed answer could not show.
func (p *scriptedPlane) Adoptions() ([]control.Adoption, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	docs := control.Adoption{
		Project: "docs", Branch: "main", TargetSHA: "ccccccccccc33333", HeadSHA: "bbbbbbbbbbb22222",
		Behind: 2, Waiting: []string{"T-3", "T-4"}, Adoptable: true, Pending: true,
		Note: "main is 2 commits behind the landed commit ccccccc",
	}
	if p.adopted["docs"] {
		docs = control.Adoption{
			Project: "docs", Branch: "main", TargetSHA: "ccccccccccc33333", HeadSHA: "ccccccccccc33333",
			Adopted: true,
			Note:    "main is already at the landed commit ccccccc",
		}
	}
	return []control.Adoption{
		docs,
		// Up to date, and therefore NOT pending: the Ledger draws no row for it and
		// `daedalus task adopt` is where "up to date" is said in full.
		{
			Project: "app", Branch: "development", TargetSHA: "aaaaaaaaaaa11111", HeadSHA: "aaaaaaaaaaa11111",
			Adopted: true,
			Note:    "development is already at the landed commit aaaaaaa",
		},
	}, nil
}

func (p *scriptedPlane) AdoptLanded(project string) (control.AdoptionResult, error) {
	p.mu.Lock()
	p.adopted[project] = true
	p.mu.Unlock()
	return control.AdoptionResult{
		Project: project, Branch: "main", TargetSHA: "ccccccccccc33333",
		Adopted: true, Note: "main fast-forwarded to ccccccc",
	}, nil
}

func (p *scriptedPlane) ListTasks() ([]control.Task, error) {
	return []control.Task{p.task("T-1"), p.task("T-2"), p.task("T-3")}, nil
}

func (p *scriptedPlane) TaskEvents(string) ([]control.Event, error) { return nil, nil }

func (p *scriptedPlane) TaskDependencies(string) (control.DependencyView, error) {
	return control.DependencyView{}, nil
}

func (p *scriptedPlane) known(id string) bool {
	return id == "T-1" || id == "T-2" || id == "T-3" || id == "T-9"
}

func (p *scriptedPlane) task(id string) control.Task {
	t := control.Task{
		ID: id, Project: "app", State: control.StateApprovalRequired,
		Objective: "Add cursor pagination to GET /items",
		BaseSHA:   "aaaaaaaaaaaa1111", Budget: control.DefaultBudget(),
	}
	switch id {
	case "T-1":
		t.Deliverables = []string{
			"GET /items accepts ?cursor= and ?limit=",
			"the response carries nextCursor when more rows remain",
			"docs/pagination.md describes the cursor format",
		}
	case "T-2":
		t.Objective = "Rename the theming tokens"
	case "T-3":
		t.Project = "docs"
		t.Objective = "Write the pagination guide"
		t.State = control.StateIntegrated
	}
	return t
}

func (p *scriptedPlane) TaskStatus(id string) (control.StatusView, error) {
	// An id the plane does not hold must answer like one. The fixture used to
	// invent a Task for any string, which silently made the "a link to an entry
	// that does not exist says so" check unreachable — a fake that never says no
	// tests only the happy half of a page that has to handle both.
	if !p.known(id) {
		return control.StatusView{}, control.ErrNotFound
	}
	p.mu.Lock()
	running := p.reviewing[id]
	p.mu.Unlock()

	view := control.StatusView{Task: p.task(id)}
	if running {
		view.Scheduling.Operation = "review"
	}
	// T-2 carries the outcome of a review that COULD NOT BE OBTAINED — the shape
	// `reviewUnavailable` produces. It is in the fixture because it is the case
	// the collapsing rendering gets wrong if nothing guards it: one concern, no
	// file, no fix, and the reason is the entire content.
	if id == "T-2" {
		view.Reviews = []control.Review{{
			ID: "RV-2", TaskID: "T-2", Passed: false,
			Reasoning: "The review did not produce a verdict. This says nothing about the " +
				"change; it says the reviewer could not be made to report on it.",
			Findings: []control.Finding{{
				Severity: control.SeverityConcern,
				What:     "no review judgement was produced",
				Why: "the reviewing agent exited with an error: exit status 1 " +
					"(its output is in /data/.daedalus/reviews/J-9.log)",
			}},
		}}
	}
	if id == "T-1" {
		view.Reviews = []control.Review{{
			ID: "RV-1", TaskID: "T-1", Reviewer: "claude", Passed: false,
			Reasoning: "I read the diff against the objective and its three deliverables, then " +
				"checked the handler and its tests. Two of the three are present.",
			Findings: []control.Finding{
				{Severity: control.SeverityBlocking, File: "internal/api/items.go", Line: 88,
					What: "the cursor is decoded without a length bound",
					Why:  "a long cursor allocates without limit and can exhaust the process",
					Fix:  "reject a cursor over 256 bytes before decoding it"},
				{Severity: control.SeverityConcern, File: "internal/api/items.go", Line: 140,
					What: "nextCursor is omitted on the last page",
					Why:  "a client cannot tell the end of a list from a failed request",
					Fix:  "send an explicit null so the field is always present"},
				{Severity: control.SeverityNote, File: "docs/pagination.md",
					What: "the guide does not mention the limit ceiling",
					Why:  "callers will discover it as a rejected request"},
			},
		}}
	}
	return view, nil
}

func (p *scriptedPlane) ReviewTask(id string) (control.ReviewResult, error) {
	p.mu.Lock()
	p.reviewing[id] = true
	p.mu.Unlock()
	time.Sleep(slowReview)
	p.mu.Lock()
	delete(p.reviewing, id)
	p.mu.Unlock()
	return control.ReviewResult{Task: p.task(id), Passed: false, Reviewer: "claude"}, nil
}

func (p *scriptedPlane) RefineTask(id string, req control.RefineRequest) (control.Task, error) {
	// T-2 REFUSES, and that is the fixture for #95: a command the plane offers
	// from this state and declines on other grounds. What the browser checks is
	// that the operator is left holding a list of things that WILL work — not a
	// reason code and a full stop, which is what an evening of dead ends looked
	// like from this side of the screen.
	if id == "T-2" {
		return control.Task{}, &control.RejectionError{
			Reason:        control.ReasonAttemptsExhausted,
			Message:       "task T-2 has used all 3 of its attempt(s)",
			Entity:        id,
			RemedySubject: id,
			RemedyState:   control.StateApprovalRequired,
			// The REAL rule, not a list typed here. A fixture that spelled out the
			// remedies would have gone on passing after the plane stopped offering
			// `refine` — and offering `refine` from an exhausted budget is precisely
			// the bug this fixture caught, because refine spends an attempt too.
			Remedies: control.RemediesForExhaustedAttempts(control.StateApprovalRequired),
		}
	}
	p.mu.Lock()
	p.refined = append(p.refined, req)
	p.mu.Unlock()
	t := p.task(id)
	t.State = control.StatePlanned
	return t, nil
}

func (p *scriptedPlane) CreateTask(req control.CreateTaskRequest) (control.Task, error) {
	p.mu.Lock()
	p.created = append(p.created, req)
	p.mu.Unlock()
	t := p.task("T-9")
	t.Objective = req.Objective
	t.Deliverables = req.Deliverables
	t.State = control.StatePlanned
	return t, nil
}

func (p *scriptedPlane) ApproveTask(id, _ string) (control.Task, error) {
	t := p.task(id)
	t.State = control.StateApproved
	return t, nil
}
