// Copyright (C) 2026 Techdelight BV

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/control"
	"github.com/techdelight/daedalus/internal/registry"
)

// manageTasks dispatches `daedalus task <create|list|status|cancel>`, the
// human-driven control-plane CLI (Sprint 54 / M13, docs/control-plane.md). It
// drives the SQLite control store in-process — there is no daemon, socket,
// worktree, or execution in this sprint; those land in Sprint 55.
func manageTasks(cfg *core.Config) error {
	args := cfg.TaskArgs
	if len(args) == 0 {
		printTaskUsage()
		return fmt.Errorf("task: subcommand required (create|list|status|cancel)")
	}
	switch args[0] {
	case "create":
		return taskCreate(cfg, args[1:])
	case "list", "ls":
		return taskList(cfg, args[1:])
	case "status", "show":
		return taskStatus(cfg, args[1:])
	case "cancel":
		return taskCancel(cfg, args[1:])
	case "help", "--help", "-h":
		printTaskUsage()
		return nil
	default:
		return fmt.Errorf("task: unknown subcommand %q\n%s daedalus task <create|list|status|cancel>", args[0], color.Cyan("Hint:"))
	}
}

// openControlStore opens the control DB under the data dir, creating the data
// dir if needed. Callers must Close the returned store.
func openControlStore(cfg *core.Config) (*control.Store, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating data dir: %w", err)
	}
	return control.Open(cfg.ControlDBPath())
}

// taskCreate implements `task create --project <name> --objective <text>
// [--acceptance <ref>]`.
func taskCreate(cfg *core.Config, args []string) error {
	var project, objective, acceptance string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project", "-p":
			if i+1 >= len(args) {
				return fmt.Errorf("--project requires a project name")
			}
			i++
			project = args[i]
		case "--objective", "-o":
			if i+1 >= len(args) {
				return fmt.Errorf("--objective requires text")
			}
			i++
			objective = args[i]
		case "--acceptance", "-a":
			if i+1 >= len(args) {
				return fmt.Errorf("--acceptance requires a reference")
			}
			i++
			acceptance = args[i]
		default:
			return fmt.Errorf("task create: unknown flag %q\n%s usage: daedalus task create --project <name> --objective <text> [--acceptance <ref>]", args[i], color.Cyan("Hint:"))
		}
	}
	if project == "" {
		return fmt.Errorf("task create: --project is required")
	}
	if objective == "" {
		return fmt.Errorf("task create: --objective is required")
	}

	// Resolve the project through the trusted registry (never trust a
	// caller-supplied path) and read its directory.
	reg := registry.NewRegistry(cfg.RegistryPath())
	if err := reg.Init(); err != nil {
		return fmt.Errorf("initializing registry: %w", err)
	}
	entry, ok, err := reg.GetProject(project)
	if err != nil {
		return fmt.Errorf("reading registry: %w", err)
	}
	if !ok {
		return fmt.Errorf("task create: project %q is not registered\n%s register it first with `daedalus %s <dir>`", project, color.Cyan("Hint:"), project)
	}

	// Git-native: the project must be a Git repo; capture the base_sha from HEAD.
	baseSHA, err := control.ReadHeadSHA(entry.Directory)
	if err != nil {
		return fmt.Errorf("task create: %w", err)
	}

	store, err := openControlStore(cfg)
	if err != nil {
		return err
	}
	defer store.Close()

	t, err := store.CreateTask(project, objective, acceptance, baseSHA, control.StatePlanned)
	if err != nil {
		return err
	}
	fmt.Printf("%s created task %s for project %s (base %s, state %s)\n",
		color.Green("OK:"), color.Bold(t.ID), t.Project, shortSHA(t.BaseSHA), t.State)
	return nil
}

// taskList implements `task list`.
func taskList(cfg *core.Config, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("task list: unexpected argument %q", args[0])
	}
	store, err := openControlStore(cfg)
	if err != nil {
		return err
	}
	defer store.Close()

	tasks, err := store.ListTasks()
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
func taskStatus(cfg *core.Config, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: daedalus task status <id>")
	}
	id := args[0]
	store, err := openControlStore(cfg)
	if err != nil {
		return err
	}
	defer store.Close()

	t, err := store.GetTask(id)
	if err != nil {
		if errors.Is(err, control.ErrNotFound) {
			return fmt.Errorf("task %q not found", id)
		}
		return err
	}
	fmt.Printf("%s      %s\n", color.Bold("Task:"), t.ID)
	fmt.Printf("%s   %s\n", color.Bold("Project:"), t.Project)
	fmt.Printf("%s     %s\n", color.Bold("State:"), t.State)
	fmt.Printf("%s %s\n", color.Bold("Objective:"), t.Objective)
	if t.AcceptanceRef != "" {
		fmt.Printf("%s %s\n", color.Bold("Acceptance:"), t.AcceptanceRef)
	}
	fmt.Printf("%s  %s\n", color.Bold("Base SHA:"), t.BaseSHA)
	fmt.Printf("%s   %s\n", color.Bold("Created:"), t.CreatedAt)

	jobs, err := store.ListJobsForTask(t.ID)
	if err != nil {
		return err
	}
	fmt.Printf("\n%s (%d)\n", color.Bold("Jobs:"), len(jobs))
	for _, j := range jobs {
		result := string(j.ExecutionResult)
		if result == "" {
			result = "—"
		}
		fmt.Printf("  %s  state=%s  runner=%s  result=%s  snapshot=%s\n",
			j.ID, j.State, orDash(j.Runner), result, shortSHA(j.OutputSnapshot))
		arts, err := store.ListArtifactsForJob(j.ID)
		if err != nil {
			return err
		}
		for _, a := range arts {
			fmt.Printf("    %s  head=%s  branch=%s  verify=%s  review=%s\n",
				a.ID, shortSHA(a.HeadSHA), orDash(a.Branch), a.Verify, a.Review)
		}
	}
	if len(jobs) == 0 {
		fmt.Println("  (none — no execution in this sprint; jobs land in Sprint 55)")
	}
	return nil
}

// taskCancel implements `task cancel <id>` via a legal transition to cancelled.
func taskCancel(cfg *core.Config, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: daedalus task cancel <id>")
	}
	id := args[0]
	store, err := openControlStore(cfg)
	if err != nil {
		return err
	}
	defer store.Close()

	t, err := store.TransitionTask(id, control.StateCancelled, false, "cancelled via CLI")
	if err != nil {
		if errors.Is(err, control.ErrNotFound) {
			return fmt.Errorf("task %q not found", id)
		}
		return fmt.Errorf("cancelling task %s: %w", id, err)
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
	fmt.Println(color.Bold("daedalus task") + " — human-driven control-plane tasks (Sprint 54 / M13)")
	fmt.Println()
	fmt.Printf("%s daedalus task <command>\n", color.Bold("Usage:"))
	fmt.Println()
	fmt.Println(color.Bold("Commands:"))
	fmt.Println("  create --project <name> --objective <text> [--acceptance <ref>]")
	fmt.Println("                       Create a task for a registered Git project (captures base_sha)")
	fmt.Println("  list                 List all tasks (id, project, state, objective)")
	fmt.Println("  status <id>          Show a task with its jobs and artifacts")
	fmt.Println("  cancel <id>          Cancel a task (legal transition to cancelled)")
	fmt.Println()
	fmt.Println("State lives host-side in " + filepath.Join("<data-dir>", "control.db") + ". No execution yet —")
	fmt.Println("no daemon, worktree, or verifier; see docs/control-plane.md for the V1 scope boundary.")
}
