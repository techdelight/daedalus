// Copyright (C) 2026 Techdelight BV

package control

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// Budget is the per-Task resource envelope the control plane governs
// (docs/guild-master-plan.md §6, "Budgets are enforced host-side"). It is
// captured at Task create and stored authoritatively on the Task row, so it
// cannot drift and no agent can edit the envelope that bounds its own work.
//
// The §6 split between what is *enforced* and what is merely *policy* is
// deliberate and load-bearing, so it is encoded in the field grouping below:
//
//   - STRONGLY ENFORCEABLE (the plane enforces these itself, host-side):
//     wall-clock, max-attempts, max-review-cycles, concurrency.
//   - POLICY IN THE PLANE, NOT ENFORCED: turns, tokens, cost. These depend on
//     *runner-dependent measurement* — Daedalus takes process exit as the Job
//     boundary and never sees an agent's turn/token accounting — so they are
//     recorded, surfaced, and honoured by whoever can measure them. Nothing in
//     Daedalus stops a Job that exceeds them. Stated plainly here and in
//     docs/control-plane.md so the guarantee is never oversold.
//
// A zero value on any axis means "unbounded / not set" (not "zero allowed").
type Budget struct {
	// --- strongly enforceable, host-side ---

	// WallClockSeconds bounds a single Job's run. The plane races the runner
	// against this deadline and classifies an overrun as execution_result=timeout.
	WallClockSeconds int `json:"wallClockSeconds"`
	// MaxAttempts bounds how many Jobs a Task may ever create (the whole chain,
	// including retries and replans — a replan does not launder the counter).
	MaxAttempts int `json:"maxAttempts"`
	// MaxReviewCycles bounds how many times a Task may enter `verifying`.
	MaxReviewCycles int `json:"maxReviewCycles"`
	// Concurrency optionally caps how many of a project's Jobs may run at once on
	// behalf of THIS Task. 0 means "no Task-level cap — the scheduler decides",
	// which is the normal case: per-project concurrency is the operator's setting
	// (SchedulerLimits.PerProject), and this axis exists only for a Task that
	// wants to be stricter than its project. The tighter of the two binds.
	Concurrency int `json:"concurrency"`

	// --- policy in the plane, NOT enforced (runner-dependent measurement) ---

	// MaxTurns is advisory: Daedalus cannot count an agent's turns.
	MaxTurns int `json:"maxTurns,omitempty"`
	// MaxTokens is advisory: Daedalus cannot count an agent's tokens.
	MaxTokens int `json:"maxTokens,omitempty"`
	// MaxCostCents is advisory: Daedalus cannot price an agent's run.
	MaxCostCents int `json:"maxCostCents,omitempty"`
}

// DefaultBudget is the built-in envelope used when neither a project override
// nor an explicit request narrows it. One hour per attempt, three attempts,
// three review cycles, and no Task-level concurrency cap (the scheduler's
// per-project limit applies instead).
func DefaultBudget() Budget {
	return Budget{
		WallClockSeconds: 3600,
		MaxAttempts:      3,
		MaxReviewCycles:  3,
		// Concurrency is deliberately UNSET. Until Sprint 61 it defaulted to 1,
		// which was the one-Job-per-project invariant written into the budget; now
		// that the scheduler owns per-project concurrency, a Task-level default of 1
		// would silently override the operator's limit and make parallelism
		// impossible to switch on. Old behaviour is preserved by
		// DefaultSchedulerLimits().PerProject, which is where it belongs.
		Concurrency: 0,
	}
}

// EnforcedAxes names the axes the plane actually enforces, for docs/CLI output.
// Kept next to the struct so the honest split cannot drift apart from the code.
func EnforcedAxes() []string {
	return []string{"wall-clock", "max-attempts", "max-review-cycles", "concurrency"}
}

// PolicyOnlyAxes names the axes that are policy only — recorded and surfaced,
// never enforced, because Daedalus cannot measure them (§6).
func PolicyOnlyAxes() []string {
	return []string{"turns", "tokens", "cost"}
}

// withDefaults fills every unset (zero) axis of b from fallback. Used to layer
// request → project override → built-in default.
func (b Budget) withDefaults(fallback Budget) Budget {
	if b.WallClockSeconds == 0 {
		b.WallClockSeconds = fallback.WallClockSeconds
	}
	if b.MaxAttempts == 0 {
		b.MaxAttempts = fallback.MaxAttempts
	}
	if b.MaxReviewCycles == 0 {
		b.MaxReviewCycles = fallback.MaxReviewCycles
	}
	if b.Concurrency == 0 {
		b.Concurrency = fallback.Concurrency
	}
	if b.MaxTurns == 0 {
		b.MaxTurns = fallback.MaxTurns
	}
	if b.MaxTokens == 0 {
		b.MaxTokens = fallback.MaxTokens
	}
	if b.MaxCostCents == 0 {
		b.MaxCostCents = fallback.MaxCostCents
	}
	return b
}

// String renders a budget compactly for CLI output and event notes.
func (b Budget) String() string {
	concurrency := "scheduler"
	if b.Concurrency > 0 {
		concurrency = fmt.Sprintf("%d", b.Concurrency)
	}
	return fmt.Sprintf("wall-clock=%ds attempts=%d review-cycles=%d concurrency=%s",
		b.WallClockSeconds, b.MaxAttempts, b.MaxReviewCycles, concurrency)
}

// axes enumerates a budget's fields as (name, value) pairs, in a fixed order, so
// validation and ceiling comparison cannot drift out of sync by forgetting an
// axis. A new field added to Budget must be added here too.
func (b Budget) axes() []struct {
	name  string
	value int
} {
	return []struct {
		name  string
		value int
	}{
		{"wallClockSeconds", b.WallClockSeconds},
		{"maxAttempts", b.MaxAttempts},
		{"maxReviewCycles", b.MaxReviewCycles},
		{"concurrency", b.Concurrency},
		{"maxTurns", b.MaxTurns},
		{"maxTokens", b.MaxTokens},
		{"maxCostCents", b.MaxCostCents},
	}
}

// invalidAxis names the first axis with a negative value, if any.
//
// Why this exists, stated plainly: 0 means "unbounded / inherit" everywhere in
// this file, and every enforcement site guards `budget.X > 0`. A NEGATIVE value
// therefore reads as unbounded at every one of them — so an unvalidated `-1`
// would be a *wider* budget than any number a caller could legitimately ask for.
// Negative is not a budget; it is invalid input, and it is rejected as such at
// every boundary a value can enter through: a create request (Service.
// resolveBudget), the host-side policy file (LoadBudgetPolicy), and — belt and
// braces, for a hand-edited database — the row scan (Budget.sanitized).
func (b Budget) invalidAxis() (string, bool) {
	for _, a := range b.axes() {
		if a.value < 0 {
			return a.name, true
		}
	}
	return "", false
}

// sanitized replaces any negative axis with fallback's value for that axis. It
// is the last line of defence for a value that reached the store without passing
// validation (a hand-edited control.db, a row from a future/older writer): a
// negative must never silently become "unbounded".
func (b Budget) sanitized(fallback Budget) Budget {
	if _, bad := b.invalidAxis(); !bad {
		return b
	}
	for i, a := range b.axes() {
		if a.value >= 0 {
			continue
		}
		switch i {
		case 0:
			b.WallClockSeconds = fallback.WallClockSeconds
		case 1:
			b.MaxAttempts = fallback.MaxAttempts
		case 2:
			b.MaxReviewCycles = fallback.MaxReviewCycles
		case 3:
			b.Concurrency = fallback.Concurrency
		case 4:
			b.MaxTurns = fallback.MaxTurns
		case 5:
			b.MaxTokens = fallback.MaxTokens
		case 6:
			b.MaxCostCents = fallback.MaxCostCents
		}
	}
	return b
}

// exceededBy reports whether requested is a *widening* of b (the ceiling) on some axis,
// naming the first offending axis. A ceiling of 0 means unbounded on that axis,
// so nothing can widen it; a requested 0 means "inherit", never "unlimited"; a
// requested NEGATIVE is invalid and is reported as a widening here too, because
// it reads as unbounded at every enforcement site (see invalidAxis) — callers
// should reject it earlier with ReasonInvalidBudget, and this is the backstop if
// one forgets.
//
// This is the mechanism behind §6's "budget too high → REJECTED": a request may
// only ever narrow the project's envelope, never raise it. Raising it means
// editing the host-side policy file — no request over control.sock can do it,
// which is only true because Service.resolveBudget applies this check server-
// side rather than trusting the CLI's own flag validation.
func (b Budget) exceededBy(requested Budget) (string, bool) {
	ceiling := b.axes()
	for i, a := range requested.axes() {
		permitted := ceiling[i].value
		if a.value < 0 {
			return a.name, true
		}
		if permitted > 0 && a.value > permitted {
			return a.name, true
		}
	}
	return "", false
}

// PolicySource resolves a project's governance policy: its budget ceiling and
// whether it requires human approval. It is the injectable seam for per-project
// overrides (tests inject a fake; the daemon injects FileBudgetPolicy). A nil
// PolicySource on the Service means DefaultBudget() and no approval requirement.
type PolicySource interface {
	BudgetFor(project string) Budget
	RequiresApproval(project string) bool
}

// BudgetPolicy is the on-disk, HOST-SIDE budget configuration: a default
// envelope plus per-project overrides.
//
// It lives under the Daedalus data dir — deliberately NOT in the project
// repository. A budget read from an agent-writable checkout would let an agent
// raise its own ceiling by committing a file, which is precisely the authority
// inversion §5 forbids ("a controlling agent must not be able to edit the state
// that determines whether its own work is valid").
//
// Example <data-dir>/control/budgets.json:
//
//	{
//	  "default":  {"wallClockSeconds": 1800, "maxAttempts": 2},
//	  "projects": {"big-app": {"wallClockSeconds": 7200, "maxAttempts": 5}}
//	}
//
// Unset (zero) fields inherit: project override → default → DefaultBudget().
type BudgetPolicy struct {
	Default  Budget            `json:"default"`
	Projects map[string]Budget `json:"projects"`
	// Approval declares which projects require a human approval before a verified
	// artifact may land (Sprint 59). It shares this file rather than getting its
	// own because it is the same kind of thing — host-side governance an agent
	// cannot edit — and because an operator should find all of it in one place.
	// Absent means opt-out: governance stays opt-in per §9.
	Approval ApprovalPolicy `json:"approval,omitempty"`
	// Concurrency is the scheduler's global and per-project limits (Sprint 61).
	// Absent keeps DefaultSchedulerLimits — one Job per project, as before — so
	// lifting the invariant changes what the plane CAN do without changing what an
	// existing installation DOES do.
	Concurrency SchedulerLimits `json:"concurrency,omitempty"`
}

// SchedulerLimitsFor returns the configured limits, falling back to the defaults
// for any axis the operator left unset.
func (p BudgetPolicy) SchedulerLimitsFor() SchedulerLimits {
	limits, def := p.Concurrency, DefaultSchedulerLimits()
	if limits.Global == 0 {
		limits.Global = def.Global
	}
	if limits.PerProject == 0 {
		limits.PerProject = def.PerProject
	}
	return limits
}

// RequiresApproval implements PolicySource.
func (p BudgetPolicy) RequiresApproval(project string) bool { return p.Approval.RequiredFor(project) }

// BudgetFor implements PolicySource: the project override layered over the
// policy default layered over the built-in default. Sanitized last, so a policy
// that reached memory without passing LoadBudgetPolicy still cannot produce a
// negative (= unbounded) axis.
func (p BudgetPolicy) BudgetFor(project string) Budget {
	base := p.Default.withDefaults(DefaultBudget())
	if override, ok := p.Projects[project]; ok {
		return override.withDefaults(base).sanitized(DefaultBudget())
	}
	return base.sanitized(DefaultBudget())
}

// LoadBudgetPolicy reads a BudgetPolicy from path. A missing file is not an
// error — it yields an empty policy, i.e. DefaultBudget() for every project. A
// negative axis anywhere in the file IS an error: it would read as "unbounded"
// at every enforcement site, so a typo must not silently disable a budget.
func LoadBudgetPolicy(path string) (BudgetPolicy, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return BudgetPolicy{}, nil
	}
	if err != nil {
		return BudgetPolicy{}, fmt.Errorf("reading budget policy %s: %w", path, err)
	}
	var p BudgetPolicy
	if err := json.Unmarshal(data, &p); err != nil {
		return BudgetPolicy{}, fmt.Errorf("parsing budget policy %s: %w", path, err)
	}
	if axis, bad := p.Default.invalidAxis(); bad {
		return BudgetPolicy{}, fmt.Errorf("budget policy %s: default.%s is negative; 0 means unbounded, negative is invalid", path, axis)
	}
	for project, b := range p.Projects {
		if axis, bad := b.invalidAxis(); bad {
			return BudgetPolicy{}, fmt.Errorf("budget policy %s: projects.%s.%s is negative; 0 means unbounded, negative is invalid", path, project, axis)
		}
	}
	return p, nil
}

// FileBudgetPolicy is the BudgetSource the daemon uses: it re-reads the policy
// file on every lookup, so an operator's edit takes effect on the next task
// without restarting the daemon.
//
// It FAILS CLOSED. A governance file that cannot be read or parsed must not take
// the control plane down — but it must not *widen* the envelope either, and
// falling back to the built-in default would do exactly that whenever an
// operator's policy is stricter than the default (a non-atomic editor's partial
// write is a real, live widening window). So the last successfully-parsed policy
// is cached and reused; only when no good read has ever happened — the same
// state as a missing file — does it fall back to the built-in default.
type FileBudgetPolicy struct {
	Path string

	mu       sync.Mutex
	last     BudgetPolicy
	haveLast bool
	warned   bool // log the degrade once, not on every lookup
}

// NewFileBudgetPolicy returns a FileBudgetPolicy for path. Use this rather than
// a struct literal: the type carries a mutex and a last-known-good cache.
func NewFileBudgetPolicy(path string) *FileBudgetPolicy { return &FileBudgetPolicy{Path: path} }

// RequiresApproval implements PolicySource, and FAILS CLOSED in the direction
// that matters for an authority gate.
//
// "Fail closed" means something different on each axis, which is why they are not
// one function. For a BUDGET, closed means the narrower envelope: an unreadable
// policy holds the last known-good ceiling, and with none ever read it falls back
// to the built-in default — a real ceiling either way. For APPROVAL, closed means
// REQUIRING A HUMAN. A corrupt or half-written budgets.json at daemon boot must
// never be the reason a change lands unreviewed: the plane does not know whether
// this project needs approval, and "I don't know" has to mean "ask someone".
//
// So: last known-good if there is one; otherwise require approval. The cost of
// being wrong is a human being asked unnecessarily. The cost of the other
// direction is an unapproved change landing while the log claims policy said it
// was fine.
func (f *FileBudgetPolicy) RequiresApproval(project string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := LoadBudgetPolicy(f.Path)
	if err == nil {
		f.last, f.haveLast = p, true
		return p.RequiresApproval(project)
	}
	if f.haveLast {
		return f.last.RequiresApproval(project)
	}
	if !f.warned {
		log.Printf("control: governance policy %s unreadable (%v) and never read successfully — REQUIRING human approval until it parses", f.Path, err)
		f.warned = true
	}
	return true
}

// BudgetFor implements PolicySource.
func (f *FileBudgetPolicy) BudgetFor(project string) Budget {
	f.mu.Lock()
	defer f.mu.Unlock()

	p, err := LoadBudgetPolicy(f.Path)
	if err == nil {
		f.last, f.haveLast, f.warned = p, true, false
		return p.BudgetFor(project)
	}
	if f.haveLast {
		if !f.warned {
			log.Printf("control: budget policy %s unreadable (%v) — holding the last known-good policy", f.Path, err)
			f.warned = true
		}
		return f.last.BudgetFor(project)
	}
	if !f.warned {
		log.Printf("control: budget policy %s unreadable (%v) and never read successfully — using built-in defaults", f.Path, err)
		f.warned = true
	}
	return DefaultBudget()
}

// StaticBudget is a PolicySource returning one fixed ceiling for every project
// and requiring no approval (tests, and any caller with no policy file).
type StaticBudget Budget

// BudgetFor implements PolicySource.
func (s StaticBudget) BudgetFor(string) Budget { return Budget(s) }

// RequiresApproval implements PolicySource: a bare budget requires no approval.
func (StaticBudget) RequiresApproval(string) bool { return false }

// StaticPolicy is a PolicySource with both axes fixed — for tests that need
// approval switched on without a policy file.
type StaticPolicy struct {
	Budget   Budget
	Approval bool
}

// BudgetFor implements PolicySource.
func (p StaticPolicy) BudgetFor(string) Budget { return p.Budget }

// RequiresApproval implements PolicySource.
func (p StaticPolicy) RequiresApproval(string) bool { return p.Approval }

// DefaultBudgetPolicyPath is where the daemon looks for the host-side policy:
// <data-dir>/control/budgets.json, alongside the job worktrees and never inside
// a project checkout.
func DefaultBudgetPolicyPath(dataDir string) string {
	return filepath.Join(dataDir, "control", "budgets.json")
}
