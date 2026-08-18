// Copyright (C) 2026 Techdelight BV

package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/control"
)

// manageTasks dispatches `daedalus task <subcommand>` (see printTaskUsage).
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
		return fmt.Errorf("task: subcommand required (create|list|status|dispatch|verify|review|approve|reject|integrate|approvals|proposals|depends|steer|board|target|retry|reverify|checks|replan|events|cancel)")
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
	case "reverify", "regrade":
		return taskReverify(api, args[1:])
	case "checks":
		return taskChecks(api, args[1:])
	case "replan":
		return taskReplan(api, args[1:])
	case "events", "log":
		return taskEvents(api, args[1:])
	case "review":
		return taskReview(api, args[1:])
	case "approve":
		return taskApprove(api, args[1:])
	case "reject":
		return taskReject(api, args[1:])
	case "integrate", "land":
		return taskIntegrate(api, args[1:])
	case "approvals", "pending":
		return taskApprovals(api, args[1:])
	case "target":
		return taskTarget(api, args[1:])
	case "proposals":
		return taskProposals(api, args[1:])
	case "depends", "dependencies":
		return taskDepends(api, args[1:])
	case "steer":
		return taskSteer(api, args[1:])
	case "board":
		return taskBoard(api, args[1:])
	case "cancel":
		return taskCancel(api, args[1:])
	default:
		return fmt.Errorf("task: unknown subcommand %q\n%s daedalus task <create|list|status|dispatch|verify|review|approve|reject|integrate|approvals|proposals|depends|steer|board|target|retry|reverify|checks|replan|events|cancel>", args[0], color.Cyan("Hint:"))
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
		case "--check", "-c":
			// Repeatable. This is the per-task half of the oracle: verify.json says
			// what the PROJECT requires, --check says what THIS task must deliver.
			if i+1 >= len(args) {
				return fmt.Errorf("--check requires a command, e.g. --check 'go test ./internal/api -run TestPagination'")
			}
			i++
			req.Checks = append(req.Checks, args[i])
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
			return fmt.Errorf("task create: unknown flag %q\n%s usage: daedalus task create --project <name> --objective <text> [--check <cmd>]... [--acceptance <note>] [--wall-clock <s>] [--max-attempts <n>] [--max-review-cycles <n>] [--concurrency <n>]", args[i], color.Cyan("Hint:"))
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
	if len(t.Checks) > 0 {
		fmt.Printf("     %s (run after the project's frozen policy, in the verifier):\n", color.Bold("task checks"))
		for _, c := range t.Checks {
			fmt.Printf("       - %s\n", c)
		}
	} else {
		fmt.Printf("     %s no task checks — it will be graded by the project policy alone,\n", color.Dim("note:"))
		fmt.Printf("           which cannot tell whether THIS objective was delivered. Add one with --check.\n")
	}
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
	// The plane-wide picture first: with several Jobs able to run at once, "what
	// is actually running" is no longer implied by the task list.
	status, statusErr := api.PlaneStatus()
	if statusErr == nil {
		fmt.Printf("%s %d/%s running", color.Bold("Plane:"), status.GlobalRunning, limitText(status.Limits.Global))
		if len(status.ProjectRunning) > 0 {
			parts := make([]string, 0, len(status.ProjectRunning))
			for project, n := range status.ProjectRunning {
				parts = append(parts, fmt.Sprintf("%s=%d", project, n))
			}
			sort.Strings(parts)
			fmt.Printf("  (per project: %s; limit %s)", strings.Join(parts, " "), limitText(status.Limits.PerProject))
		}
		if len(status.Waiting) > 0 {
			fmt.Printf("  %s %s", color.Yellow("queued:"), strings.Join(status.Waiting, " "))
		}
		fmt.Println()
		fmt.Println()
	}

	queued := map[string]int{}
	if statusErr == nil {
		for i, id := range status.Waiting {
			queued[id] = i + 1
		}
	}
	fmt.Printf("%-6s  %-18s  %-18s  %s\n", color.Bold("ID"), color.Bold("PROJECT"), color.Bold("STATE"), color.Bold("OBJECTIVE"))
	fmt.Printf("%-6s  %-18s  %-18s  %s\n", "------", "------------------", "------------------", "---------")
	for _, t := range tasks {
		state := string(t.State)
		// A Task queued for capacity is NOT the same as one that is working, and
		// with parallelism the difference is the thing an operator most needs to
		// see: `planned` alone would hide that the plane has already said "not yet".
		if pos, waiting := queued[t.ID]; waiting {
			state = fmt.Sprintf("%s (queued #%d)", t.State, pos)
		}
		fmt.Printf("%-6s  %-18s  %-18s  %s\n", t.ID, truncate(t.Project, 18), truncate(state, 18), truncate(t.Objective, 50))
	}
	return nil
}

// limitText renders a scheduler limit, where 0 means unbounded.
func limitText(limit int) string {
	if limit <= 0 {
		return "∞"
	}
	return fmt.Sprintf("%d", limit)
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
	if len(t.Checks) > 0 {
		fmt.Printf("%s %d, appended to the project policy at verify\n", color.Bold("Task checks:"), len(t.Checks))
		for _, c := range t.Checks {
			fmt.Printf("  - %s\n", c)
		}
	}
	if t.AcceptanceRef != "" {
		fmt.Printf("%s %s (note only — not executed; see --check)\n", color.Bold("Acceptance:"), t.AcceptanceRef)
	}
	fmt.Printf("%s  %s\n", color.Bold("Base SHA:"), t.BaseSHA)
	fmt.Printf("%s    %s\n", color.Bold("Budget:"), t.Budget)
	fmt.Printf("%s   %s\n", color.Bold("Created:"), t.CreatedAt)

	if deps := view.Dependencies; len(deps.DependsOn) > 0 || len(deps.Dependents) > 0 {
		if len(deps.DependsOn) > 0 {
			fmt.Printf("%s %s\n", color.Bold("Depends on:"), strings.Join(deps.DependsOn, " "))
		}
		if len(deps.Dependents) > 0 {
			fmt.Printf("%s %s\n", color.Bold("Blocks:"), strings.Join(deps.Dependents, " "))
		}
		renderDependencyStatus(deps.Status)
	}

	sc := view.Scheduling
	switch {
	case sc.Running:
		fmt.Printf("%s  running (project %d/%s, plane %d/%s)\n", color.Bold("Schedule:"),
			sc.ProjectRunning, limitText(sc.Limits.PerProject), sc.GlobalRunning, limitText(sc.Limits.Global))
	case sc.QueuedForCapacity:
		fmt.Printf("%s  %s (position %d; project %d/%s, plane %d/%s)\n", color.Bold("Schedule:"),
			color.Yellow("queued for capacity"), sc.QueuePosition,
			sc.ProjectRunning, limitText(sc.Limits.PerProject), sc.GlobalRunning, limitText(sc.Limits.Global))
	default:
		fmt.Printf("%s  not running (project %d/%s, plane %d/%s)\n", color.Bold("Schedule:"),
			sc.ProjectRunning, limitText(sc.Limits.PerProject), sc.GlobalRunning, limitText(sc.Limits.Global))
	}

	fmt.Printf("\n%s (%d)\n", color.Bold("Jobs:"), len(view.Jobs))
	for _, jv := range view.Jobs {
		j := jv.Job
		result := string(j.ExecutionResult)
		if result == "" {
			result = "—"
		}
		fmt.Printf("  %s  state=%s  runner=%s  result=%s  snapshot=%s\n",
			j.ID, j.State, orDash(j.Runner), result, shortSHA(j.OutputSnapshot))
		// The Job's own log, when it has one (#77). Its own line rather than another
		// field on the one above, because a path is long and this is the thing a
		// person reading `task status` after a failure has actually come for.
		if j.LogPath != "" {
			fmt.Printf("    %s %s\n", color.Bold("log:"), j.LogPath)
		}
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
	const usage = "usage: daedalus task verify <id> [--ignore-result]"
	if len(args) < 1 {
		return fmt.Errorf("%s", usage)
	}
	var req control.VerifyRequest
	for _, a := range args[1:] {
		switch a {
		case "--ignore-result":
			req.IgnoreResult = true
		default:
			return fmt.Errorf("task verify: unknown flag %q\n%s %s", a, color.Cyan("Hint:"), usage)
		}
	}
	res, err := api.VerifyTask(args[0], req)
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
	if res.Waived {
		// Said in this order on purpose: the finding first, the override second. An
		// operator who waives a check should read what they waived.
		fmt.Printf("%s task %s FAILED verification [%s] — %s\n",
			color.Yellow("Reject:"), args[0], res.Reason, res.Detail)
		fmt.Printf("%s the result was WAIVED — task %s is now awaiting your approval.\n",
			color.Yellow("Waived:"), args[0])
		fmt.Println("     it is NOT verified and never will be: the rejection and the waiver both stand")
		fmt.Println("     on the record, and the artifact keeps verify=fail. What changed is that you")
		fmt.Println("     are answerable for it rather than the oracle.")
		fmt.Printf("     approve it with `daedalus task approve %s`\n", args[0])
		return nil
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
		// Named only where it can actually apply. The integrity gate and the
		// null-agent floor are findings about the artifact, and re-verification
		// refuses them — offering it there would advertise a door that is locked.
		if res.Reason != control.ReasonIntegrityGate && res.Reason != control.ReasonNullAgentFloor {
			fmt.Printf("     if the VERDICT was wrong rather than the work, re-grade this same artifact with `daedalus task reverify %s`\n", args[0])
		}
	}
	return nil
}

// taskChecks implements `task checks <id> [--set <cmd>]... | --clear`: show or
// replace a Task's per-task acceptance checks.
func taskChecks(api control.TaskAPI, args []string) error {
	const usage = "usage: daedalus task checks <id> [--set <cmd>]... [--clear]"
	if len(args) < 1 {
		return fmt.Errorf("%s", usage)
	}
	id := args[0]
	var req control.AmendChecksRequest
	amend, clear := false, false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--set":
			if i+1 >= len(args) {
				return fmt.Errorf("--set requires a command")
			}
			i++
			req.Checks = append(req.Checks, args[i])
			amend = true
		case "--clear":
			clear, amend = true, true
		default:
			return fmt.Errorf("task checks: unknown flag %q\n%s %s", args[i], color.Cyan("Hint:"), usage)
		}
	}
	if clear && len(req.Checks) > 0 {
		return fmt.Errorf("task checks: --clear and --set are mutually exclusive")
	}
	if !amend {
		view, err := api.TaskStatus(id)
		if err != nil {
			return err
		}
		t := view.Task
		if len(t.Checks) == 0 {
			fmt.Printf("task %s has no per-task checks — it is graded by the project policy alone\n", id)
			return nil
		}
		fmt.Printf("%s task %s, run after the project's frozen policy:\n", color.Bold("Task checks:"), id)
		for _, c := range t.Checks {
			fmt.Printf("  - %s\n", c)
		}
		return nil
	}
	t, err := api.AmendTaskChecks(id, req)
	if err != nil {
		if errors.Is(err, control.ErrNotFound) {
			return fmt.Errorf("task %q not found", id)
		}
		return err
	}
	fmt.Printf("%s task %s now carries %d check(s); the amendment and its before→after are in `task events`\n",
		color.Green("OK:"), id, len(t.Checks))
	for _, c := range t.Checks {
		fmt.Printf("  - %s\n", c)
	}
	fmt.Println("     a re-verification after an amendment costs a review cycle: the oracle changed,")
	fmt.Println("     so it is a new grading rather than a replay")
	return nil
}

// taskReverify implements `task reverify <id> [--amended]`: re-grade a rejected
// Task's EXISTING artifact, with no new Job and no attempt spent.
func taskReverify(api control.TaskAPI, args []string) error {
	const usage = "usage: daedalus task reverify <id> [--amended]"
	if len(args) < 1 {
		return fmt.Errorf("%s", usage)
	}
	id := args[0]
	var req control.ReverifyRequest
	for _, a := range args[1:] {
		switch a {
		case "--amended":
			req.Amended = true
		default:
			return fmt.Errorf("task reverify: unknown flag %q\n%s %s", a, color.Cyan("Hint:"), usage)
		}
	}
	res, err := api.ReverifyTask(id, req)
	if err != nil {
		if errors.Is(err, control.ErrNotFound) {
			return fmt.Errorf("task %q not found", id)
		}
		return err
	}
	fmt.Printf("%s task %s: setting aside the %s verdict and re-grading the same artifact\n",
		color.Cyan("Re-verify:"), id, res.PreviousReason)
	if res.Rebased {
		fmt.Printf("     rebased onto %s — the acceptance policy was re-frozen there, so this verdict is\n", shortSHA(res.BaseSHA))
		fmt.Println("     under a policy the artifact did not originally face (recorded in `task events`)")
	}
	v := res.Verify
	if v.Verified {
		fmt.Printf("%s task %s VERIFIED — job %s → %s\n", color.Green("OK:"), id, v.Job.ID, v.Job.State)
		if v.Artifact != nil {
			fmt.Printf("     artifact %s verify=%s\n", v.Artifact.ID, v.Artifact.Verify)
		}
		return nil
	}
	fmt.Printf("%s task %s REJECTED again [%s] — %s\n", color.Yellow("Reject:"), id, v.Reason, v.Detail)
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

// taskReview implements `task review <id>`: the independent reviewer pass over a
// verified artifact, which gates integration when a reviewer is configured.
func taskReview(api control.TaskAPI, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: daedalus task review <id>")
	}
	res, err := api.ReviewTask(args[0])
	if err != nil {
		if errors.Is(err, control.ErrNotFound) {
			return fmt.Errorf("task %q not found", args[0])
		}
		return err
	}
	cycles := fmt.Sprintf("%d", res.Cycles)
	if res.MaxCycle > 0 {
		cycles = fmt.Sprintf("%d/%d", res.Cycles, res.MaxCycle)
	}
	if res.Passed {
		fmt.Printf("%s task %s passed independent review (pass %s) — %s\n",
			color.Green("OK:"), args[0], cycles, res.Detail)
		return nil
	}
	fmt.Printf("%s task %s REJECTED by independent review [%s] (pass %s) — %s\n",
		color.Yellow("Reject:"), args[0], res.Reason, cycles, res.Detail)
	fmt.Printf("     retry with `daedalus task retry %s`, or replan with `daedalus task replan %s --objective <text>`\n", args[0], args[0])
	return nil
}

// taskApprove implements `task approve <id> [--note <text>]`.
func taskApprove(api control.TaskAPI, args []string) error {
	id, note, err := idAndNote("approve", args)
	if err != nil {
		return err
	}
	t, err := api.ApproveTask(id, note)
	if err != nil {
		if errors.Is(err, control.ErrNotFound) {
			return fmt.Errorf("task %q not found", id)
		}
		return err
	}
	fmt.Printf("%s task %s approved (state %s)\n", color.Green("OK:"), color.Bold(t.ID), t.State)
	fmt.Printf("     land it with `daedalus task integrate %s`\n", t.ID)
	return nil
}

// taskReject implements `task reject <id> [--note <text>]`.
func taskReject(api control.TaskAPI, args []string) error {
	id, note, err := idAndNote("reject", args)
	if err != nil {
		return err
	}
	t, err := api.RejectApproval(id, note)
	if err != nil {
		if errors.Is(err, control.ErrNotFound) {
			return fmt.Errorf("task %q not found", id)
		}
		return err
	}
	fmt.Printf("%s task %s rejected at the approval gate (state %s)\n", color.Yellow("Reject:"), color.Bold(t.ID), t.State)
	fmt.Printf("     retry with `daedalus task retry %s`, or replan with `daedalus task replan %s --objective <text>`\n", t.ID, t.ID)
	return nil
}

// idAndNote parses `<id> [--note <text>]` for the approve/reject commands.
func idAndNote(cmd string, args []string) (string, string, error) {
	if len(args) < 1 {
		return "", "", fmt.Errorf("usage: daedalus task %s <id> [--note <text>]", cmd)
	}
	id, note := args[0], ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--note", "-m":
			if i+1 >= len(args) {
				return "", "", fmt.Errorf("--note requires text")
			}
			i++
			note = args[i]
		default:
			return "", "", fmt.Errorf("task %s: unknown flag %q\n%s usage: daedalus task %s <id> [--note <text>]",
				cmd, args[i], color.Cyan("Hint:"), cmd)
		}
	}
	return id, note, nil
}

// taskIntegrate implements `task integrate <id> [--into-branch]`: the race-safe landing
// transaction (rebase onto the target → re-verify the MERGED result → CAS).
func taskIntegrate(api control.TaskAPI, args []string) error {
	const usage = "usage: daedalus task integrate <id> [--into-branch]"
	if len(args) < 1 {
		return fmt.Errorf("%s", usage)
	}
	var req control.IntegrateRequest
	for _, a := range args[1:] {
		switch a {
		case "--into-branch":
			req.IntoBranch = true
		default:
			return fmt.Errorf("task integrate: unknown flag %q\n%s %s", a, color.Cyan("Hint:"), usage)
		}
	}
	res, err := api.IntegrateTask(args[0], req)
	if err != nil {
		if errors.Is(err, control.ErrNotFound) {
			return fmt.Errorf("task %q not found", args[0])
		}
		return err
	}
	fmt.Printf("%s task %s INTEGRATED — target %s → %s\n",
		color.Green("OK:"), color.Bold(args[0]), shortSHA(res.PreviousTarget), color.Bold(shortSHA(res.NewTarget)))
	fmt.Printf("     landed as %s (the artifact rebased onto the target and re-verified in that form)\n", shortSHA(res.MergedSHA))
	if res.Attempts > 1 {
		fmt.Printf("     took %d attempts — the target moved under us and the transaction recomputed\n", res.Attempts)
	}
	switch {
	case res.BranchAdvanced:
		fmt.Printf("     %s %s\n", color.Green("branch:"), res.BranchNote)
	case res.BranchNote != "":
		// The landing SUCCEEDED; only the courtesy did not. Said in that order, so
		// nobody reads a yellow line as "my code did not land".
		fmt.Printf("     %s %s\n", color.Yellow("branch:"), res.BranchNote)
	default:
		// The default path, and the answer to "I integrated it, where is my code?".
		// The plane lands on its own ref precisely so it never touches a working
		// tree; that is a good default and a surprising one, so it is spelled out.
		fmt.Printf("     your branch was NOT changed — the landed commit is at refs/daedalus/target.\n")
		fmt.Printf("     adopt it with `git merge --ff-only refs/daedalus/target`, or pass --into-branch next time\n")
	}
	return nil
}

// taskApprovals implements `task approvals`: everything awaiting a human.
func taskApprovals(api control.TaskAPI, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("task approvals: unexpected argument %q", args[0])
	}
	tasks, err := api.PendingApprovals()
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		fmt.Println("Nothing is awaiting approval.")
		return nil
	}
	fmt.Printf("%-6s  %-18s  %s\n", color.Bold("ID"), color.Bold("PROJECT"), color.Bold("OBJECTIVE"))
	fmt.Printf("%-6s  %-18s  %s\n", "------", "------------------", "---------")
	for _, t := range tasks {
		fmt.Printf("%-6s  %-18s  %s\n", t.ID, truncate(t.Project, 18), truncate(t.Objective, 50))
	}
	fmt.Println()
	fmt.Println("Approve with `daedalus task approve <id>`, reject with `daedalus task reject <id>`.")
	return nil
}

// taskTarget implements `task target [<project> --sync]`: show the plane-owned
// integration targets, or resync one to the checkout's HEAD.
func taskTarget(api control.TaskAPI, args []string) error {
	if len(args) == 0 {
		targets, err := api.ProjectTargets()
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			fmt.Println("No integration targets yet — one is adopted when a project's first task is created.")
			return nil
		}
		fmt.Printf("%-30s  %-10s  %s\n", color.Bold("PROJECT(S)"), color.Bold("TARGET"), color.Bold("REPOSITORY"))
		fmt.Printf("%-30s  %-10s  %s\n", "------------------------------", "----------", "----------")
		for _, t := range targets {
			who := strings.Join(t.Projects, ", ")
			if who == "" {
				who = "—"
			}
			fmt.Printf("%-30s  %-10s  %s\n", truncate(who, 30), shortSHA(t.SHA), t.RepoPath)
			if len(t.Projects) > 1 {
				// Sharing is surprising unless it is said out loud.
				fmt.Printf("%s these projects share one merge queue (same repository)\n", color.Cyan("     note:"))
			}
		}
		fmt.Println()
		fmt.Println("The target is the commit tasks are based on and graded against. Only a completed")
		fmt.Println("integration advances it; `--sync` adopts the checkout's HEAD by hand.")
		return nil
	}
	project := args[0]
	sync := false
	for _, a := range args[1:] {
		switch a {
		case "--sync":
			sync = true
		default:
			return fmt.Errorf("task target: unknown flag %q\n%s usage: daedalus task target [<project> --sync]", a, color.Cyan("Hint:"))
		}
	}
	if !sync {
		return fmt.Errorf("usage: daedalus task target [<project> --sync]")
	}
	t, err := api.SyncTarget(project)
	if err != nil {
		return err
	}
	fmt.Printf("%s integration target for %s is now %s\n", color.Green("OK:"), color.Bold(project), shortSHA(t.SHA))
	fmt.Println("     future tasks are based on, and graded against, that commit")
	return nil
}

// taskProposals implements `task proposals [list|confirm <id>|deny <id>]` — the
// human end of the tiered-authority flow. An agent that asks for a consequential
// operation gets a proposal; this is where a person decides.
func taskProposals(api control.TaskAPI, args []string) error {
	action := "list"
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "list":
		state := control.ProposalPending
		if len(args) > 1 {
			if args[1] == "--all" {
				state = ""
			} else {
				return fmt.Errorf("task proposals list: unknown flag %q\n%s usage: daedalus task proposals list [--all]", args[1], color.Cyan("Hint:"))
			}
		}
		proposals, err := api.ListProposals(state)
		if err != nil {
			return err
		}
		if len(proposals) == 0 {
			fmt.Println("No proposals awaiting a decision.")
			return nil
		}
		fmt.Printf("%-5s  %-22s  %-8s  %-10s  %s\n",
			color.Bold("ID"), color.Bold("OPERATION"), color.Bold("TASK"),
			color.Bold("BY"), color.Bold("STATE"))
		fmt.Printf("%-5s  %-22s  %-8s  %-10s  %s\n", "-----", "----------------------", "--------", "----------", "-----")
		for _, p := range proposals {
			fmt.Printf("%-5s  %-22s  %-8s  %-10s  %s\n",
				p.ID, truncate(p.Operation, 22), orDash(p.TaskID), p.ProposedBy, p.State)
			if p.Argument != "" {
				fmt.Printf("       %s\n", truncate(p.Argument, 90))
			}
		}
		fmt.Println()
		fmt.Println("Confirm with `daedalus task proposals confirm <id>`, deny with `... deny <id>`.")
		fmt.Println("Confirming EXECUTES the operation as you; denying does nothing at all.")
		return nil

	case "confirm", "deny":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus task proposals %s <id> [--note <text>]", action)
		}
		id, note, err := idAndNote("proposals "+action, args[1:])
		if err != nil {
			return err
		}
		p, err := api.ResolveProposal(id, action == "confirm", note)
		if err != nil {
			if errors.Is(err, control.ErrNotFound) {
				return fmt.Errorf("proposal %q not found", id)
			}
			return err
		}
		if action == "deny" {
			fmt.Printf("%s proposal %s denied — %s was NOT performed\n",
				color.Yellow("Denied:"), color.Bold(p.ID), p.Operation)
			return nil
		}
		fmt.Printf("%s proposal %s confirmed — %s performed as you\n",
			color.Green("OK:"), color.Bold(p.ID), p.Operation)
		return nil

	default:
		return fmt.Errorf("task proposals: unknown action %q\n%s usage: daedalus task proposals [list [--all] | confirm <id> | deny <id>]",
			action, color.Cyan("Hint:"))
	}
}

// taskDepends implements `task depends <id> [--on <other-id>]`: declare or show a
// cross-project dependency.
//
// A cycle is refused HERE, at declaration — not discovered later at dispatch,
// where it would be a wedged graph somebody has to unpick.
func taskDepends(api control.TaskAPI, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: daedalus task depends <id> [--on <other-id>]")
	}
	id := args[0]
	var on string
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--on":
			if i+1 >= len(args) {
				return fmt.Errorf("--on requires a task id")
			}
			i++
			on = args[i]
		default:
			return fmt.Errorf("task depends: unknown flag %q\n%s usage: daedalus task depends <id> [--on <other-id>]", args[i], color.Cyan("Hint:"))
		}
	}
	if on != "" {
		edge, err := api.AddDependency(id, on)
		if err != nil {
			if errors.Is(err, control.ErrNotFound) {
				return fmt.Errorf("task %q or %q not found", id, on)
			}
			return err
		}
		fmt.Printf("%s task %s now depends on %s\n", color.Green("OK:"), color.Bold(edge.TaskID), color.Bold(edge.DependsOn))
		fmt.Println("     it will not be dispatched until that task has landed")
		return nil
	}

	view, err := api.TaskDependencies(id)
	if err != nil {
		if errors.Is(err, control.ErrNotFound) {
			return fmt.Errorf("task %q not found", id)
		}
		return err
	}
	if len(view.DependsOn) == 0 && len(view.Dependents) == 0 {
		fmt.Printf("Task %s has no dependencies and nothing depends on it.\n", id)
		return nil
	}
	if len(view.DependsOn) > 0 {
		fmt.Printf("%s %s\n", color.Bold("Depends on:"), strings.Join(view.DependsOn, " "))
	}
	if len(view.Dependents) > 0 {
		fmt.Printf("%s %s\n", color.Bold("Blocks:    "), strings.Join(view.Dependents, " "))
	}
	renderDependencyStatus(view.Status)
	return nil
}

// renderDependencyStatus prints why a Task is or is not runnable.
func renderDependencyStatus(status control.DependencyStatus) {
	switch {
	case status.Ready():
		fmt.Printf("%s every dependency has landed\n", color.Green("Ready:"))
	case len(status.Unsatisfiable) > 0:
		fmt.Printf("%s waiting on %s, which can never complete (failed or cancelled)\n",
			color.Red("Stuck:"), strings.Join(status.Unsatisfiable, " "))
		// Deliberately does NOT suggest retrying the upstream: `failed` is terminal
		// and a dependency edge cannot be removed, so a retry would not rescue this
		// task. Cancel-and-recreate is the only route out, and saying anything
		// else would be advice that cannot work.
		fmt.Println("     this cannot be rescued in place — cancel this task and recreate it")
		if len(status.Unmet) > 0 {
			fmt.Printf("     also waiting on %s\n", strings.Join(status.Unmet, " "))
		}
	default:
		fmt.Printf("%s waiting on %s\n", color.Yellow("Blocked:"), strings.Join(status.Unmet, " "))
	}
}

// taskSteer implements `task steer <job-id> [--instruction <text>]` and
// `task steer --withdraw <steering-id>`.
//
// The output is deliberately blunt about UNDELIVERABLE. An operator who steered a
// Job and was told "OK" would go away believing they had redirected it; the whole
// value of typed steering over shouting into a terminal is that this command can
// say "recorded, NOT delivered" and mean it.
func taskSteer(api control.TaskAPI, args []string) error {
	const usage = "usage: daedalus task steer <job-id> [--instruction <text>] | daedalus task steer --withdraw <steering-id>"
	if len(args) < 1 {
		return fmt.Errorf("%s", usage)
	}
	if args[0] == "--withdraw" {
		if len(args) < 2 {
			return fmt.Errorf("--withdraw requires a steering id\n%s %s", color.Cyan("Hint:"), usage)
		}
		steer, err := api.CancelSteering(args[1])
		if err != nil {
			if errors.Is(err, control.ErrNotFound) {
				return fmt.Errorf("steering %q not found", args[1])
			}
			return err
		}
		fmt.Printf("%s steering %s withdrawn before delivery\n", color.Green("OK:"), color.Bold(steer.ID))
		return nil
	}

	jobID := args[0]
	var instruction string
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--instruction", "--say":
			if i+1 >= len(args) {
				return fmt.Errorf("--instruction requires text")
			}
			i++
			instruction = args[i]
		default:
			return fmt.Errorf("task steer: unknown flag %q\n%s %s", args[i], color.Cyan("Hint:"), usage)
		}
	}

	if instruction == "" {
		steers, err := api.JobSteering(jobID)
		if err != nil {
			if errors.Is(err, control.ErrNotFound) {
				return fmt.Errorf("job %q not found", jobID)
			}
			return err
		}
		if len(steers) == 0 {
			fmt.Printf("Job %s has not been steered.\n", jobID)
			return nil
		}
		fmt.Printf("%-6s  %-14s  %-8s  %s\n",
			color.Bold("ID"), color.Bold("DELIVERY"), color.Bold("ISSUER"), color.Bold("INSTRUCTION"))
		for _, s := range steers {
			fmt.Printf("%-6s  %-14s  %-8s  %s\n",
				s.ID, renderDelivery(s.State), s.IssuedBy, truncate(s.Instruction, 60))
			if s.Detail != "" {
				fmt.Printf("        %s\n", truncate(s.Detail, 90))
			}
		}
		return nil
	}

	steer, err := api.SteerJob(jobID, instruction)
	if err != nil {
		if errors.Is(err, control.ErrNotFound) {
			return fmt.Errorf("job %q not found", jobID)
		}
		return err
	}
	switch steer.State {
	case control.SteerDelivered:
		fmt.Printf("%s steering %s DELIVERED to job %s\n", color.Green("OK:"), color.Bold(steer.ID), jobID)
	case control.SteerPending:
		fmt.Printf("%s steering %s recorded; the runner has it and will hand it over at its next boundary\n",
			color.Yellow("Pending:"), color.Bold(steer.ID))
		fmt.Println("     it has NOT reached the worker yet — `daedalus task steer " + jobID + "` shows the outcome")
	default:
		fmt.Printf("%s steering %s recorded but NOT DELIVERED (%s)\n",
			color.Red("Undelivered:"), color.Bold(steer.ID), steer.State)
		fmt.Printf("     %s\n", steer.Detail)
		fmt.Println("     the job was not told anything. To change its direction, cancel it and dispatch again")
		fmt.Println("     with a revised objective (`daedalus task replan <task-id> --objective <text>`).")
	}
	fmt.Println("     steering changes what the worker is told; it never changes what counts as done —")
	fmt.Println("     the result is still verified against the frozen acceptance policy.")
	return nil
}

// renderDelivery colours a delivery state so the one that matters stands out.
func renderDelivery(state control.DeliveryState) string {
	switch state {
	case control.SteerDelivered:
		return color.Green(string(state))
	case control.SteerUndeliverable:
		return color.Red(string(state))
	case control.SteerPending:
		return color.Yellow(string(state))
	default:
		return string(state)
	}
}

// taskBoard implements `task board`: the cross-project programme view.
//
// It is a projection of the same rows every other command reads — there is no
// board state — so it can never disagree with `task list`, `task approvals` or the
// event log.
func taskBoard(api control.TaskAPI, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("task board: unexpected argument %q\n%s usage: daedalus task board", args[0], color.Cyan("Hint:"))
	}
	view, err := api.ProgrammeBoard()
	if err != nil {
		return err
	}
	fmt.Println(color.Bold("Programme board") + " — every project the control plane knows about")
	limit := limitText(view.Plane.Limits.Global)
	fmt.Printf("  %d/%s jobs running", view.Plane.GlobalRunning, limit)
	if n := len(view.Plane.Waiting); n > 0 {
		fmt.Printf(", %d queued for capacity", n)
	}
	fmt.Printf("  ·  %d awaiting approval  ·  %d proposals pending\n", view.PendingApprovals, view.PendingProposals)
	fmt.Println()

	total := 0
	for _, col := range view.Columns {
		total += len(col.Cards)
		fmt.Printf("%s (%d)\n", color.Bold(col.Title), len(col.Cards))
		if len(col.Cards) == 0 {
			// Printed rather than skipped: "nothing is blocked" is an answer.
			fmt.Println("  —")
			continue
		}
		for _, c := range col.Cards {
			fmt.Printf("  %-6s  %-16s  %s\n", c.TaskID, truncate(c.Project, 16), truncate(c.Objective, 52))
			if len(c.BlockedOn) > 0 {
				fmt.Printf("          waiting on %s\n", strings.Join(c.BlockedOn, " "))
			}
			if len(c.Unsatisfiable) > 0 {
				fmt.Printf("          %s %s can never complete\n", color.Red("stuck:"), strings.Join(c.Unsatisfiable, " "))
			}
			if c.QueuedForCapacity {
				fmt.Println("          holding a place in line for a free slot")
			}
			if c.Steering != "" {
				fmt.Printf("          steering %s\n", c.Steering)
			}
		}
	}
	fmt.Println()
	if total == 0 {
		fmt.Println("No tasks yet. Create one with `daedalus task create --project <name> --objective <text>`.")
		return nil
	}
	fmt.Printf("%d task(s) across %d project(s). Tasks sharing a merge queue serialize at integration.\n",
		total, len(view.Projects))
	return nil
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
	fmt.Println("  create --project <name> --objective <text> [--check <cmd>]... [--acceptance <note>]")
	fmt.Println("         [--wall-clock <s>] [--max-attempts <n>] [--max-review-cycles <n>] [--concurrency <n>]")
	fmt.Println("                       Create a task for a registered Git project (captures base_sha,")
	fmt.Println("                       freezes the acceptance policy, and pins the budget)")
	fmt.Println("                       --check adds a PER-TASK command the verifier must also pass —")
	fmt.Println("                       repeatable, human-only, appended to the project policy (never")
	fmt.Println("                       replacing it). --acceptance is a free-text note and is NOT run")
	fmt.Println("  list                 List all tasks (id, project, state, objective)")
	fmt.Println("  status <id>          Show a task with its budget, jobs and artifacts")
	fmt.Println("  dispatch <id>        Run one headless Job attempt (isolated worktree; success → candidate)")
	fmt.Println("  verify <id> [--ignore-result]")
	fmt.Println("                       Verify a candidate (integrity gate → verifier → verified | rejected).")
	fmt.Println("                       --ignore-result WAIVES a failing result: the verifier still runs, the")
	fmt.Println("                       failure is still recorded, the artifact still reads verify=fail — and")
	fmt.Println("                       the task moves to the approval gate on YOUR authority. It never marks")
	fmt.Println("                       anything verified, and an agent may not ask for it")
	fmt.Println("  retry <id> [--rebase]")
	fmt.Println("                       Retry a rejected task as a fresh Job (attempt counter advanced;")
	fmt.Println("                       --rebase re-pins it to the project tip after a stale-base rejection)")
	fmt.Println("  checks <id> [--set <cmd>]... [--clear]")
	fmt.Println("                       Show or REPLACE a task's per-task acceptance checks. A check written")
	fmt.Println("                       before the work exists can be wrong, and a wrong one can never pass;")
	fmt.Println("                       amending is human-only, recorded with its before→after, and withdraws")
	fmt.Println("                       the free re-verify (the oracle changed, so it is a new grading)")
	fmt.Println("  reverify <id> [--amended]")
	fmt.Println("                       Re-grade a rejected task's EXISTING artifact — no new Job, no attempt")
	fmt.Println("                       spent. For when the VERDICT was wrong rather than the work: a verifier")
	fmt.Println("                       that never ran its check, or a policy that failed on an advisory")
	fmt.Println("                       finding. --amended re-pins to the project tip first, re-freezing the")
	fmt.Println("                       acceptance policy, for when the ORACLE was what needed fixing.")
	fmt.Println("                       Refused for the integrity gate and the null-agent floor: those are")
	fmt.Println("                       findings about the artifact, and re-grading them would be an appeal")
	fmt.Println("  replan <id> --objective <text>")
	fmt.Println("                       Return a rejected task to planned with a revised objective")
	fmt.Println("  events <id>          Show the control-plane-managed event log for a task")
	fmt.Println("  review <id>          Run the independent reviewer over a verified artifact")
	fmt.Println("  approve <id> [--note <text>]")
	fmt.Println("                       Approve a verified task for integration (human authority)")
	fmt.Println("  reject <id> [--note <text>]")
	fmt.Println("                       Reject at the approval gate (feeds retry/replan)")
	fmt.Println("  integrate <id> [--into-branch]")
	fmt.Println("                       Land it: rebase onto the target, re-verify the MERGED result,")
	fmt.Println("                       then compare-and-swap the plane-owned target ref. Your branch is")
	fmt.Println("                       NOT moved — the commit lands on refs/daedalus/target, which nobody")
	fmt.Println("                       checks out. --into-branch also fast-forwards the checkout's current")
	fmt.Println("                       branch, refusing on a detached HEAD, a dirty tree, or a diverged")
	fmt.Println("                       branch; a refusal there never unlands the work")
	fmt.Println("  approvals            List everything awaiting a human decision")
	fmt.Println("  depends <id> [--on <other-id>]")
	fmt.Println("                       Show or declare a cross-project dependency; a blocked task")
	fmt.Println("                       is never dispatched until its dependencies have landed")
	fmt.Println("  steer <job-id> [--instruction <text>]")
	fmt.Println("                       Show, or issue, a typed instruction for a RUNNING job. Recorded")
	fmt.Println("                       with its delivery state; `undeliverable` means the worker was")
	fmt.Println("                       NOT told. Add --withdraw <steering-id> to pull an undelivered one")
	fmt.Println("  board                Cross-project programme board: running, queued, blocked (and on")
	fmt.Println("                       what), in verification, awaiting approval, landed")
	fmt.Println("  proposals [list [--all] | confirm <id> | deny <id>]")
	fmt.Println("                       Consequential operations an AGENT asked for; confirming")
	fmt.Println("                       executes them as you, denying does nothing")
	fmt.Println("  target [<project> --sync]")
	fmt.Println("                       Show the plane-owned integration targets, or resync one by hand")
	fmt.Println("  cancel <id>          Cancel a task (legal transition to cancelled)")
	fmt.Println()
	fmt.Println(color.Bold("Integration:"))
	fmt.Println("  Each project has a plane-owned target commit that ONLY a completed integration")
	fmt.Println("  advances. Tasks are based on it and their acceptance policy is frozen at it, so")
	fmt.Println("  rewriting the repository's branches cannot influence how work is graded.")
	fmt.Println()
	fmt.Println(color.Bold("Verification:"))
	fmt.Println("  `.daedalus/verify.json` is the PROJECT's bar, frozen at base_sha: it answers")
	fmt.Println("  \"does this artifact still meet the standing bar\", not \"did this task deliver")
	fmt.Println("  what it promised\" — it was written before the task existed. `--check` at create")
	fmt.Println("  is where the second question gets a machine-checkable answer.")
	fmt.Println()
	fmt.Println(color.Bold("Steering:"))
	fmt.Println("  Steering changes what a worker is TOLD; it never changes what counts as done.")
	fmt.Println("  A steered job still reaches `candidate` and is still verified against the frozen")
	fmt.Println("  acceptance policy. Delivery depends on the runner: one with no steering boundary")
	fmt.Println("  records `undeliverable` rather than reporting a success that did not happen.")
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
