// Copyright (C) 2026 Techdelight BV

package control

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	// Concurrency bounds how many of a project's Jobs may be running at once
	// (queued/working/input_required). V1 is 1; M16 raises it.
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
// three review cycles, one Job at a time (V1's one-active-Job-per-project rule).
func DefaultBudget() Budget {
	return Budget{
		WallClockSeconds: 3600,
		MaxAttempts:      3,
		MaxReviewCycles:  3,
		Concurrency:      1,
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
	return fmt.Sprintf("wall-clock=%ds attempts=%d review-cycles=%d concurrency=%d",
		b.WallClockSeconds, b.MaxAttempts, b.MaxReviewCycles, b.Concurrency)
}

// exceededBy reports whether requested is a *widening* of b (the ceiling) on some axis,
// naming the first offending axis. A ceiling of 0 means unbounded on that axis,
// so nothing can widen it; a requested 0 means "inherit", never "unlimited".
// This is the mechanism behind §6's "budget too high → REJECTED": a request may
// only ever narrow the project's envelope, never raise it. Raising it is a
// host-side act (edit the budget policy file), deliberately outside the reach of
// anything that talks to control.sock.
func (b Budget) exceededBy(requested Budget) (string, bool) {
	type axis struct {
		name            string
		want, permitted int
	}
	for _, a := range []axis{
		{"wallClockSeconds", requested.WallClockSeconds, b.WallClockSeconds},
		{"maxAttempts", requested.MaxAttempts, b.MaxAttempts},
		{"maxReviewCycles", requested.MaxReviewCycles, b.MaxReviewCycles},
		{"concurrency", requested.Concurrency, b.Concurrency},
		{"maxTurns", requested.MaxTurns, b.MaxTurns},
		{"maxTokens", requested.MaxTokens, b.MaxTokens},
		{"maxCostCents", requested.MaxCostCents, b.MaxCostCents},
	} {
		if a.permitted > 0 && a.want > a.permitted {
			return a.name, true
		}
	}
	return "", false
}

// BudgetSource resolves the ceiling budget for a project. It is the injectable
// seam for per-project overrides (tests inject a fake; the daemon injects
// FileBudgetPolicy). A nil BudgetSource on the Service means DefaultBudget().
type BudgetSource interface {
	BudgetFor(project string) Budget
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
}

// BudgetFor implements BudgetSource: the project override layered over the
// policy default layered over the built-in default.
func (p BudgetPolicy) BudgetFor(project string) Budget {
	base := p.Default.withDefaults(DefaultBudget())
	if override, ok := p.Projects[project]; ok {
		return override.withDefaults(base)
	}
	return base
}

// LoadBudgetPolicy reads a BudgetPolicy from path. A missing file is not an
// error — it yields an empty policy, i.e. DefaultBudget() for every project.
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
	return p, nil
}

// FileBudgetPolicy is the BudgetSource the daemon uses: it re-reads the policy
// file on every lookup, so an operator's edit takes effect on the next task
// without restarting the daemon. A missing or malformed file degrades to the
// built-in defaults rather than failing a create — a governance file that cannot
// be parsed must not be able to take the control plane down.
type FileBudgetPolicy struct{ Path string }

// BudgetFor implements BudgetSource.
func (f FileBudgetPolicy) BudgetFor(project string) Budget {
	p, err := LoadBudgetPolicy(f.Path)
	if err != nil {
		return DefaultBudget()
	}
	return p.BudgetFor(project)
}

// StaticBudget is a BudgetSource returning one fixed ceiling for every project
// (tests, and any caller with no policy file).
type StaticBudget Budget

// BudgetFor implements BudgetSource.
func (s StaticBudget) BudgetFor(string) Budget { return Budget(s) }

// DefaultBudgetPolicyPath is where the daemon looks for the host-side policy:
// <data-dir>/control/budgets.json, alongside the job worktrees and never inside
// a project checkout.
func DefaultBudgetPolicyPath(dataDir string) string {
	return filepath.Join(dataDir, "control", "budgets.json")
}
