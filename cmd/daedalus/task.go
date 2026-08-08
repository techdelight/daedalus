// Copyright (C) 2026 Techdelight BV

package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/control"
)

// manageTasks dispatches `daedalus task <create|list|status|dispatch|cancel>`.
//
// As of Sprint 55 the CLI is a THIN CLIENT of the daedalus-control daemon: it
// obtains a client via control.EnsureRunning (auto-spawning the daemon, exactly
// like `daedalus coordinator`), and the daemon is the single owner of
// control.db. The command handlers take a control.TaskAPI so they are identical
// whether driven by the live socket client or, in tests, an in-process Service.
func manageTasks(cfg *core.Config) error {
	args := cfg.TaskArgs
	if len(args) == 0 {
		printTaskUsage()
		return fmt.Errorf("task: subcommand required (create|list|status|dispatch|cancel)")
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
	case "cancel":
		return taskCancel(api, args[1:])
	default:
		return fmt.Errorf("task: unknown subcommand %q\n%s daedalus task <create|list|status|dispatch|cancel>", args[0], color.Cyan("Hint:"))
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
// [--acceptance <ref>]`. Project resolution + the Git-native base_sha capture
// now happen server-side (the daemon resolves through the trusted registry).
func taskCreate(api control.TaskAPI, args []string) error {
	var req control.CreateTaskRequest
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
		default:
			return fmt.Errorf("task create: unknown flag %q\n%s usage: daedalus task create --project <name> --objective <text> [--acceptance <ref>]", args[i], color.Cyan("Hint:"))
		}
	}
	if req.Project == "" {
		return fmt.Errorf("task create: --project is required")
	}
	if req.Objective == "" {
		return fmt.Errorf("task create: --objective is required")
	}
	t, err := api.CreateTask(req)
	if err != nil {
		return err
	}
	fmt.Printf("%s created task %s for project %s (base %s, state %s)\n",
		color.Green("OK:"), color.Bold(t.ID), t.Project, shortSHA(t.BaseSHA), t.State)
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
	fmt.Println(color.Bold("daedalus task") + " — host-side control-plane tasks (Milestone 13)")
	fmt.Println()
	fmt.Printf("%s daedalus task <command>\n", color.Bold("Usage:"))
	fmt.Println()
	fmt.Println(color.Bold("Commands:"))
	fmt.Println("  create --project <name> --objective <text> [--acceptance <ref>]")
	fmt.Println("                       Create a task for a registered Git project (captures base_sha)")
	fmt.Println("  list                 List all tasks (id, project, state, objective)")
	fmt.Println("  status <id>          Show a task with its jobs and artifacts")
	fmt.Println("  dispatch <id>        Run one headless Job attempt (isolated worktree; success → candidate)")
	fmt.Println("  cancel <id>          Cancel a task (legal transition to cancelled)")
	fmt.Println()
	fmt.Println("The CLI talks to the daedalus-control daemon over <data-dir>/.daedalus/control.sock,")
	fmt.Println("auto-starting it if needed. See docs/control-plane.md.")
}
