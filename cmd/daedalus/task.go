// Copyright (C) 2026 Techdelight BV

package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/control"
)

// manageTasks dispatches
// `daedalus task <create|list|status|dispatch|verify|retry|replan|events|cancel>`.
//
// As of Sprint 55 the CLI is a THIN CLIENT of the daedalus-control daemon: it
// obtains a client via control.EnsureRunning (auto-spawning the daemon, exactly
// like `daedalus coordinator`), and the daemon is the single owner of
// control.db. The command handlers take a control.TaskAPI so they are identical
// whether driven by the live socket client or, in tests, an in-process Service.
//
// A command refused by control-plane policy (over budget, attempts exhausted, …)
// returns a *control.RejectionError, which main.go maps to exit code 3 — so a
// script can tell "the plane said no" from "something broke" (§6).
func manageTasks(cfg *core.Config) error {
	args := cfg.TaskArgs
	if len(args) == 0 {
		printTaskUsage()
		return fmt.Errorf("task: subcommand required (create|list|status|dispatch|verify|retry|replan|events|cancel)")
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printTaskUsage()
		return nil
	}

	client, err := controlClient(cfg)
	if err != nil {
		return err
	}
	return runTaskCommand(client, args)
}

// runTaskCommand routes a task subcommand against any TaskAPI. Split out so tests
// drive it with an in-process Service instead of the socket client.
func runTaskCommand(api control.TaskAPI, args []string) error {
	switch args[0] {
	case "create":
		return taskCreate(api, args[1:])
	case "list", "ls":
		return taskList(api, args[1:])
	case "status", "show":
		return taskStatus(api, args[1:])
	case "dispatch", "run":
		return taskDispatch(api, args[1:])
	case "verify":
		return taskVerify(api, args[1:])
	case "retry":
		return taskRetry(api, args[1:])
	case "replan":
		return taskReplan(api, args[1:])
	case "events", "log":
		return taskEvents(api, args[1:])
	case "cancel":
		return taskCancel(api, args[1:])
	default:
		return fmt.Errorf("task: unknown subcommand %q\n%s daedalus task <create|list|status|dispatch|verify|retry|replan|events|cancel>", args[0], color.Cyan("Hint:"))
	}
}

// controlClient returns a live client to the control daemon, spawning it if
// needed (ssh-agent style). The daemon binary lives next to the daedalus binary.
func controlClient(cfg *core.Config) (control.TaskAPI, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating data dir: %w", err)
	}
	return control.EnsureRunning(control.DefaultLayout(cfg.DataDir, cfg.ScriptDir))
}

// taskCreate implements `task create --project <name> --objective <text>
// [--acceptance <ref>] [budget flags]`. Project resolution + the Git-native
// base_sha capture now happen server-side (the daemon resolves through the
// trusted registry).
//
// The budget flags may only NARROW the project's ceiling; asking for more is
// refused with `over_budget` (exit 3). Raising a ceiling is a host-side act —
// edit <data-dir>/control/budgets.json.
func taskCreate(api control.TaskAPI, args []string) error {
	var req control.CreateTaskRequest
	var budget control.Budget
	var budgetSet bool
	// intFlag reads the value of an integer budget flag, marking the budget as
	// requested so an unspecified budget stays nil (inherit everything).
	intFlag := func(i *int, name string, dst *int) error {
		if *i+1 >= len(args) {
			return fmt.Errorf("%s requires a number", name)
		}
		*i++
		n, err := strconv.Atoi(args[*i])
		if err != nil || n < 0 {
			return fmt.Errorf("%s requires a non-negative number, got %q", name, args[*i])
		}
		*dst = n
		budgetSet = true
		return nil
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project", "-p":
			if i+1 >= len(args) {
				return fmt.Errorf("--project requires a project name")
			}
			i++
			req.Project = args[i]
		case "--objective", "-o":
			if i+1 >= len(args) {
				return fmt.Errorf("--objective requires text")
			}
			i++
			req.Objective = args[i]
		case "--acceptance", "-a":
			if i+1 >= len(args) {
				return fmt.Errorf("--acceptance requires a reference")
			}
			i++
			req.Acceptance = args[i]
		case "--wall-clock":
			if err := intFlag(&i, "--wall-clock", &budget.WallClockSeconds); err != nil {
				return err
			}
		case "--max-attempts":
			if err := intFlag(&i, "--max-attempts", &budget.MaxAttempts); err != nil {
				return err
			}
		case "--max-review-cycles":
			if err := intFlag(&i, "--max-review-cycles", &budget.MaxReviewCycles); err != nil {
				return err
			}
		case "--concurrency":
			if err := intFlag(&i, "--concurrency", &budget.Concurrency); err != nil {
				return err
			}
		default:
			return fmt.Errorf("task create: unknown flag %q\n%s usage: daedalus task create --project <name> --objective <text> [--acceptance <ref>] [--wall-clock <s>] [--max-attempts <n>] [--max-review-cycles <n>] [--concurrency <n>]", args[i], color.Cyan("Hint:"))
		}
	}
	if req.Project == "" {
		return fmt.Errorf("task create: --project is required")
	}
	if req.Objective == "" {
		return fmt.Errorf("task create: --objective is required")
	}
	if budgetSet {
		req.Budget = &budget
	}
	t, err := api.CreateTask(req)
	if err != nil {
		return err
	}
	fmt.Printf("%s created task %s for project %s (base %s, state %s)\n",
		color.Green("OK:"), color.Bold(t.ID), t.Project, shortSHA(t.BaseSHA), t.State)
	fmt.Printf("     budget: %s\n", t.Budget)
	return nil
}

// taskList implements `task list`.
func taskList(api control.TaskAPI, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("task list: unexpected argument %q", args[0])
	}
	tasks, err := api.ListTasks()
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		fmt.Println("No tasks. Create one with `daedalus task create --project <name> --objective <text>`.")
		return nil
	}
	fmt.Printf("%-6s  %-18s  %-16s  %s\n", color.Bold("ID"), color.Bold("PROJECT"), color.Bold("STATE"), color.Bold("OBJECTIVE"))
	fmt.Printf("%-6s  %-18s  %-16s  %s\n", "------", "------------------", "----------------", "---------")
	for _, t := range tasks {
		fmt.Printf("%-6s  %-18s  %-16s  %s\n", t.ID, truncate(t.Project, 18), t.State, truncate(t.Objective, 50))
	}
	return nil
}

// taskStatus implements `task status <id>`: the task plus its jobs and artifacts.
func taskStatus(api control.TaskAPI, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: daedalus task status <id>")
	}
	view, err := api.TaskStatus(args[0])
	if err != nil {
		if errors.Is(err, control.ErrNotFound) {
			return fmt.Errorf("task %q not found", args[0])
		}
		return err
	}
	t := view.Task
	fmt.Printf("%s      %s\n", color.Bold("Task:"), t.ID)
	fmt.Printf("%s   %s\n", color.Bold("Project:"), t.Project)
	fmt.Printf("%s     %s\n", color.Bold("State:"), t.State)
	fmt.Printf("%s %s\n", color.Bold("Objective:"), t.Objective)
	if t.AcceptanceRef != "" {
		fmt.Printf("%s %s\n", color.Bold("Acceptance:"), t.AcceptanceRef)
	}
	fmt.Printf("%s  %s\n", color.Bold("Base SHA:"), t.BaseSHA)
	fmt.Printf("%s    %s\n", color.Bold("Budget:"), t.Budget)
	fmt.Printf("%s   %s\n", color.Bold("Created:"), t.CreatedAt)

	fmt.Printf("\n%s (%d)\n", color.Bold("Jobs:"), len(view.Jobs))
	for _, jv := range view.Jobs {
		j := jv.Job
		result := string(j.ExecutionResult)
		if result == "" {
			result = "—"
		}
		fmt.Printf("  %s  state=%s  runner=%s  result=%s  snapshot=%s\n",
			j.ID, j.State, orDash(j.Runner), result, shortSHA(j.OutputSnapshot))
		for _, a := range jv.Artifacts {
			fmt.Printf("    %s  head=%s  branch=%s  verify=%s  review=%s\n",
				a.ID, shortSHA(a.HeadSHA), orDash(a.Branch), a.Verify, a.Review)
		}
	}
	if len(view.Jobs) == 0 {
		fmt.Println("  (none — dispatch one with `daedalus task dispatch " + t.ID + "`)")
	}
	return nil
}

// taskDispatch implements `task dispatch <id>`: run one headless Job attempt.
func taskDispatch(api control.TaskAPI, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: daedalus task dispatch <id>")
	}
	res, err := api.DispatchTask(args[0])
	if err != nil {
		if errors.Is(err, control.ErrNotFound) {
			return fmt.Errorf("task %q not found", args[0])
		}
		return fmt.Errorf("dispatching task %s: %w", args[0], err)
	}
	j := res.Job
	fmt.Printf("%s dispatched task %s → job %s ended %s (state %s, snapshot %s)\n",
		color.Green("OK:"), args[0], color.Bold(j.ID), string(j.ExecutionResult), j.State, shortSHA(j.OutputSnapshot))
	if res.Artifact != nil {
		a := res.Artifact
		fmt.Printf("     candidate artifact %s on branch %s (head %s, verify %s)\n",
			color.Bold(a.ID), a.Branch, shortSHA(a.HeadSHA), a.Verify)
	}
	return nil
}

// taskVerify implements `task verify <id>`: the plane-owned verify pass over a
// candidate job (test-integrity gate → verifier → verified | rejected).
func taskVerify(api control.TaskAPI, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: daedalus task verify <id>")
	}
	res, err := api.VerifyTask(args[0])
	if err != nil {
		if errors.Is(err, control.ErrNotFound) {
			return fmt.Errorf("task %q not found", args[0])
		}
		return fmt.Errorf("verifying task %s: %w", args[0], err)
	}
	if res.GateTouched {
		fmt.Printf("%s task %s REJECTED by the integrity gate [%s] (verifier not called)\n",
			color.Yellow("Gate:"), args[0], res.Reason)
		fmt.Printf("     job %s → %s; edits to frozen acceptance files: %s\n",
			res.Job.ID, res.Job.State, strings.Join(res.TouchedFiles, ", "))
		return nil
	}
	if res.Verified {
		fmt.Printf("%s task %s VERIFIED — job %s → %s (%s)\n",
			color.Green("OK:"), args[0], color.Bold(res.Job.ID), res.Job.State, res.Detail)
		if res.Artifact != nil {
			fmt.Printf("     artifact %s verify=%s\n", res.Artifact.ID, res.Artifact.Verify)
		}
		return nil
	}
	by := "the verifier"
	if !res.VerifierCalled {
		// Pre-verifier: null-agent floor, stale base, or policy-hash drift.
		by = "the control plane"
	}
	fmt.Printf("%s task %s REJECTED by %s [%s] — job %s → %s (%s)\n",
		color.Yellow("Reject:"), args[0], by, res.Reason, res.Job.ID, res.Job.State, res.Detail)
	if res.Reason == control.ReasonStaleBase {
		// Deliberately not a one-liner to copy-paste: the tip may have moved because
		// a Job moved it, and --rebase re-freezes the acceptance oracle there. Look
		// before you leap. (The plane refuses the unsafe case regardless.)
		fmt.Println("     the project tip moved. Inspect it first — `--rebase` re-freezes the acceptance policy at that commit:")
		fmt.Printf("       git -C <project> log -1 ; then `daedalus task retry %s --rebase`\n", args[0])
	} else {
		fmt.Printf("     retry with `daedalus task retry %s`, or replan with `daedalus task replan %s --objective <text>`\n", args[0], args[0])
	}
	return nil
}

// taskRetry implements `task retry <id> [--rebase]`: a fresh Job on a rejected
// task, with the attempt counter advanced and the budget re-checked. The prior
// attempts are preserved — `task status` still shows the whole Job chain.
func taskRetry(api control.TaskAPI, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: daedalus task retry <id> [--rebase]")
	}
	id := args[0]
	var req control.RetryRequest
	for _, a := range args[1:] {
		switch a {
		case "--rebase":
			req.Rebase = true
		default:
			return fmt.Errorf("task retry: unknown flag %q\n%s usage: daedalus task retry <id> [--rebase]", a, color.Cyan("Hint:"))
		}
	}
	res, err := api.RetryTask(id, req)
	if err != nil {
		if errors.Is(err, control.ErrNotFound) {
			return fmt.Errorf("task %q not found", id)
		}
		return err
	}
	if res.Rebased {
		fmt.Printf("%s task %s rebased onto %s (acceptance policy re-frozen there)\n",
			color.Cyan("Rebase:"), id, shortSHA(res.BaseSHA))
	}
	j := res.Dispatch.Job
	fmt.Printf("%s retried task %s — attempt %s → job %s ended %s (state %s, snapshot %s)\n",
		color.Green("OK:"), id, attemptOf(res), color.Bold(j.ID), string(j.ExecutionResult), j.State, shortSHA(j.OutputSnapshot))
	if res.Dispatch.Artifact != nil {
		a := res.Dispatch.Artifact
		fmt.Printf("     candidate artifact %s on branch %s (head %s, verify %s)\n",
			color.Bold(a.ID), a.Branch, shortSHA(a.HeadSHA), a.Verify)
		fmt.Printf("     verify it with `daedalus task verify %s`\n", id)
	}
	return nil
}

// attemptOf renders "2/3" (or "2" when attempts are unbounded).
func attemptOf(res control.RetryResult) string {
	if res.MaxAttempts <= 0 {
		return fmt.Sprintf("%d", res.Attempt)
	}
	return fmt.Sprintf("%d/%d", res.Attempt, res.MaxAttempts)
}

// taskReplan implements `task replan <id> --objective <text>`: a rejected task
// returns to `planned` with a revised objective. The Job chain — and therefore
// the attempt counter — is preserved, so replanning never buys extra attempts.
func taskReplan(api control.TaskAPI, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: daedalus task replan <id> --objective <text>")
	}
	id := args[0]
	var req control.ReplanRequest
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--objective", "-o":
			if i+1 >= len(args) {
				return fmt.Errorf("--objective requires text")
			}
			i++
			req.Objective = args[i]
		default:
			return fmt.Errorf("task replan: unknown flag %q\n%s usage: daedalus task replan <id> --objective <text>", args[i], color.Cyan("Hint:"))
		}
	}
	if req.Objective == "" {
		return fmt.Errorf("task replan: --objective is required")
	}
	t, err := api.ReplanTask(id, req)
	if err != nil {
		if errors.Is(err, control.ErrNotFound) {
			return fmt.Errorf("task %q not found", id)
		}
		return err
	}
	fmt.Printf("%s task %s replanned → %s\n", color.Green("OK:"), color.Bold(t.ID), t.State)
	fmt.Printf("     objective: %s\n", t.Objective)
	fmt.Printf("     dispatch it with `daedalus task dispatch %s`\n", t.ID)
	return nil
}

// taskEvents implements `task events <id>`: the control-plane-managed event log
// for a task and everything beneath it. Read-only — there is no command that
// writes, amends, or deletes an event, because the API exposes no such operation
// (immutable *through the API*, NOT cryptographically tamper-proof).
func taskEvents(api control.TaskAPI, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: daedalus task events <id>")
	}
	events, err := api.TaskEvents(args[0])
	if err != nil {
		if errors.Is(err, control.ErrNotFound) {
			return fmt.Errorf("task %q not found", args[0])
		}
		return err
	}
	if len(events) == 0 {
		fmt.Printf("No events for task %s.\n", args[0])
		return nil
	}
	fmt.Printf("%-5s  %-20s  %-13s  %-9s  %-13s  %s\n",
		color.Bold("SEQ"), color.Bold("AT"), color.Bold("KIND"), color.Bold("ENTITY"),
		color.Bold("ACTOR"), color.Bold("CHANGE"))
	for _, e := range events {
		fmt.Printf("%-5d  %-20s  %-13s  %-9s  %-13s  %s\n",
			e.Seq, e.At, e.Kind, e.EntityID, e.Actor, eventChange(e))
		if e.Note != "" {
			fmt.Printf("       %s\n", truncate(e.Note, 100))
		}
	}
	fmt.Printf("\n%d event(s). Control-plane-managed and immutable through the API; not cryptographically tamper-proof.\n", len(events))
	return nil
}

// eventChange renders an event's state movement plus its rejection reason.
func eventChange(e control.Event) string {
	change := "—"
	switch {
	case e.From != "" && e.To != "":
		change = string(e.From) + " → " + string(e.To)
	case e.To != "":
		change = "→ " + string(e.To)
	}
	if e.Reason != "" {
		change += "  [" + string(e.Reason) + "]"
	}
	return change
}

// taskCancel implements `task cancel <id>` via a legal transition to cancelled.
func taskCancel(api control.TaskAPI, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: daedalus task cancel <id>")
	}
	t, err := api.CancelTask(args[0])
	if err != nil {
		if errors.Is(err, control.ErrNotFound) {
			return fmt.Errorf("task %q not found", args[0])
		}
		return fmt.Errorf("cancelling task %s: %w", args[0], err)
	}
	fmt.Printf("%s task %s cancelled\n", color.Green("OK:"), color.Bold(t.ID))
	return nil
}

// shortSHA abbreviates a git sha for display; returns a dash when empty.
func shortSHA(sha string) string {
	if sha == "" {
		return "—"
	}
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// truncate shortens s to at most n runes, appending an ellipsis when cut.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

func printTaskUsage() {
	fmt.Println(color.Bold("daedalus task") + " — host-side control-plane tasks (Milestones 13–15)")
	fmt.Println()
	fmt.Printf("%s daedalus task <command>\n", color.Bold("Usage:"))
	fmt.Println()
	fmt.Println(color.Bold("Commands:"))
	fmt.Println("  create --project <name> --objective <text> [--acceptance <ref>]")
	fmt.Println("         [--wall-clock <s>] [--max-attempts <n>] [--max-review-cycles <n>] [--concurrency <n>]")
	fmt.Println("                       Create a task for a registered Git project (captures base_sha,")
	fmt.Println("                       freezes the acceptance policy, and pins the budget)")
	fmt.Println("  list                 List all tasks (id, project, state, objective)")
	fmt.Println("  status <id>          Show a task with its budget, jobs and artifacts")
	fmt.Println("  dispatch <id>        Run one headless Job attempt (isolated worktree; success → candidate)")
	fmt.Println("  verify <id>          Verify a candidate (integrity gate → verifier → verified | rejected)")
	fmt.Println("  retry <id> [--rebase]")
	fmt.Println("                       Retry a rejected task as a fresh Job (attempt counter advanced;")
	fmt.Println("                       --rebase re-pins it to the project tip after a stale-base rejection)")
	fmt.Println("  replan <id> --objective <text>")
	fmt.Println("                       Return a rejected task to planned with a revised objective")
	fmt.Println("  events <id>          Show the control-plane-managed event log for a task")
	fmt.Println("  cancel <id>          Cancel a task (legal transition to cancelled)")
	fmt.Println()
	fmt.Println(color.Bold("Budgets:"))
	fmt.Println("  Enforced host-side: wall-clock, max-attempts, max-review-cycles, concurrency.")
	fmt.Println("  Policy only (recorded, NOT enforced — Daedalus cannot measure them): turns, tokens, cost.")
	fmt.Println("  Flags may only narrow a project's ceiling; raise it in <data-dir>/control/budgets.json.")
	fmt.Println("  A command refused by policy exits 3 (distinct from 1 = failure).")
	fmt.Println()
	fmt.Println("The CLI talks to the daedalus-control daemon over <data-dir>/.daedalus/control.sock,")
	fmt.Println("auto-starting it if needed. See docs/control-plane.md.")
}
