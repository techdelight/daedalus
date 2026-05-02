// Copyright (C) 2026 Techdelight BV

package main

import (
	"fmt"
	"strings"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/foreman"
	"github.com/techdelight/daedalus/internal/mcpclient"
	"github.com/techdelight/daedalus/internal/programme"
	"github.com/techdelight/daedalus/internal/registry"
)

// manageProgrammes handles the "programmes" subcommand for managing
// multi-project programme definitions.
func manageProgrammes(cfg *core.Config) error {
	store := programme.New(cfg.ProgrammesDir())
	args := cfg.ProgrammesArgs

	if len(args) == 0 || args[0] == "list" {
		return listProgrammes(store)
	}

	switch args[0] {
	case "show":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus programmes show <name>")
		}
		reg := registry.NewRegistry(cfg.RegistryPath())
		if err := reg.Init(); err != nil {
			return fmt.Errorf("initializing registry: %w", err)
		}
		client := mcpclient.New()
		return showProgramme(store, args[1], reg, client)
	case "create":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus programmes create <name>")
		}
		return createProgramme(store, args[1])
	case "add-project":
		if len(args) < 3 {
			return fmt.Errorf("usage: daedalus programmes add-project <programme> <project>")
		}
		return addProjectToProgramme(store, args[1], args[2])
	case "add-dep":
		if len(args) < 4 {
			return fmt.Errorf("usage: daedalus programmes add-dep <programme> <upstream> <downstream>")
		}
		return addDepToProgramme(store, args[1], args[2], args[3])
	case "remove":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus programmes remove <name>")
		}
		return removeProgramme(store, args[1])
	case "cascade":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus programmes cascade <programme> [--dry-run]")
		}
		progName := args[1]
		dryRun := len(args) >= 3 && args[2] == "--dry-run"
		return cascadeProgramme(store, progName, dryRun)
	default:
		return fmt.Errorf("unknown programmes command %q\n%s available: list, show, create, add-project, add-dep, remove, cascade", args[0], color.Cyan("Hint:"))
	}
}

// listProgrammes prints all programme definitions in a table.
func listProgrammes(store *programme.Store) error {
	progs, err := store.List()
	if err != nil {
		return fmt.Errorf("listing programmes: %w", err)
	}

	if len(progs) == 0 {
		fmt.Println("No programmes defined. Use 'daedalus programmes create <name>' to create one.")
		return nil
	}

	nameW := 4
	for _, p := range progs {
		if len(p.Name) > nameW {
			nameW = len(p.Name)
		}
	}

	fmt.Printf("%-*s  %-10s  %s\n", nameW, color.Bold("NAME"), color.Bold("PROJECTS"), color.Bold("DEPS"))
	fmt.Printf("%-*s  %-10s  %s\n", nameW, strings.Repeat("-", nameW), "--------", "----")
	for _, p := range progs {
		fmt.Printf("%-*s  %-10d  %d\n", nameW, p.Name, len(p.Projects), len(p.Deps))
	}
	return nil
}

// showProgramme prints programme details with per-project progress aggregation.
func showProgramme(store *programme.Store, name string, reg *registry.Registry, client *mcpclient.Client) error {
	p, err := store.Read(name)
	if err != nil {
		return fmt.Errorf("reading programme: %w", err)
	}

	fmt.Printf("%s  %s\n", color.Bold("Programme:"), p.Name)
	if p.Description != "" {
		fmt.Printf("%s  %s\n", color.Bold("Description:"), p.Description)
	}
	fmt.Printf("%s  %d\n", color.Bold("Projects:"), len(p.Projects))
	fmt.Printf("%s  %d\n\n", color.Bold("Dependencies:"), len(p.Deps))

	if len(p.Deps) > 0 {
		fmt.Println(color.Bold("Dependency Graph:"))
		for _, d := range p.Deps {
			fmt.Printf("  %s → %s\n", d.Upstream, d.Downstream)
		}
		fmt.Println()
	}

	if len(p.Projects) > 0 && reg != nil && client != nil {
		fmt.Println(color.Bold("Project Status:"))
		fmt.Printf("  %-20s  %-8s  %-12s  %s\n", "NAME", "PROGRESS", "VERSION", "SPRINT")
		fmt.Printf("  %-20s  %-8s  %-12s  %s\n", "----", "--------", "-------", "------")
		for _, projName := range p.Projects {
			entry, found, _ := reg.GetProject(projName)
			if !found {
				fmt.Printf("  %-20s  %-8s  %-12s  %s\n", projName, "?", "?", "(not registered)")
				continue
			}
			status, _ := client.GetProjectStatus(projName, entry.Directory)
			pct := fmt.Sprintf("%d%%", status.ProgressPct)
			ver := status.ProjectVersion
			if ver == "" {
				ver = "—"
			}
			sprint := "—"
			if status.CurrentSprint != nil {
				sprint = fmt.Sprintf("Sprint %d", status.CurrentSprint.Number)
			}
			fmt.Printf("  %-20s  %-8s  %-12s  %s\n", projName, pct, ver, sprint)
		}
	}

	return nil
}

// createProgramme creates a new empty programme.
func createProgramme(store *programme.Store, name string) error {
	p := core.Programme{
		Name:     name,
		Projects: []string{},
	}
	if err := store.Create(p); err != nil {
		return fmt.Errorf("creating programme: %w", err)
	}
	fmt.Printf("%s programme '%s' created.\n", color.Green("OK:"), name)
	return nil
}

// addProjectToProgramme adds a project to a programme.
func addProjectToProgramme(store *programme.Store, programmeName, projectName string) error {
	if err := store.AddProject(programmeName, projectName); err != nil {
		return fmt.Errorf("adding project: %w", err)
	}
	fmt.Printf("%s project '%s' added to programme '%s'.\n", color.Green("OK:"), projectName, programmeName)
	return nil
}

// addDepToProgramme adds a dependency edge to a programme.
func addDepToProgramme(store *programme.Store, programmeName, upstream, downstream string) error {
	if err := store.AddDep(programmeName, upstream, downstream); err != nil {
		return fmt.Errorf("adding dependency: %w", err)
	}
	fmt.Printf("%s dependency %s → %s added to programme '%s'.\n", color.Green("OK:"), upstream, downstream, programmeName)
	return nil
}

// removeProgramme deletes a programme by name.
func removeProgramme(store *programme.Store, name string) error {
	if err := store.Remove(name); err != nil {
		return fmt.Errorf("removing programme: %w", err)
	}
	fmt.Printf("%s programme '%s' removed.\n", color.Green("OK:"), name)
	return nil
}

// cascadeProgramme evaluates cascade propagation for a programme and prints the results.
func cascadeProgramme(store *programme.Store, name string, dryRun bool) error {
	p, err := store.Read(name)
	if err != nil {
		return fmt.Errorf("reading programme: %w", err)
	}

	if len(p.Deps) == 0 {
		fmt.Println("No dependencies defined — nothing to cascade.")
		return nil
	}

	if dryRun {
		fmt.Printf("%s Cascade dry-run for programme %q\n\n", color.Yellow("DRY RUN:"), name)
	}

	// Evaluate cascade for each project (simulate all completing)
	seen := make(map[string]bool)
	for _, proj := range p.Projects {
		result := foreman.EvaluateCascade(p, proj, dryRun)
		for _, event := range result.Events {
			key := event.Upstream + "→" + event.Downstream
			if seen[key] {
				continue
			}
			seen[key] = true
			actionColor := color.Green
			if event.Action == "skip" {
				actionColor = color.Dim
			} else if event.Action == "notify" {
				actionColor = color.Yellow
			}
			fmt.Printf("  %s  %s → %s  [%s]\n", actionColor(event.Action), event.Upstream, event.Downstream, string(event.Strategy))
			fmt.Printf("         %s\n", event.Message)
		}
	}

	if dryRun {
		fmt.Printf("\n%s No changes made.\n", color.Yellow("DRY RUN:"))
	}

	return nil
}
