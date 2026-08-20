// Copyright (C) 2026 Techdelight BV

package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/control"
	"github.com/techdelight/daedalus/internal/mcpclient"
	"github.com/techdelight/daedalus/internal/registry"
)

// The `programmes` subcommand — multi-project programme definitions.
//
// SPRINT 66: these talk to the CONTROL PLANE, not to files under
// `<data-dir>/programmes`. A programme is authoritative state now, for the same
// reason every other row is: a Task points at one, and a definition whose only
// identity is a filename can be renamed out from under the work that serves it.
// The daemon adopts any pre-existing definitions on start, once and idempotently,
// so nothing anybody wrote is lost.
//
// The visible consequence, and it is worth knowing rather than discovering:
// programme commands now need the control daemon. The CLI spawns one on demand —
// the same `EnsureRunning` every `daedalus task` command uses — so in practice
// this is invisible here. It is NOT invisible in the Web UI, which deliberately
// never spawns a daemon and therefore reports the plane as unreachable instead.

// manageProgrammes handles the "programmes" subcommand.
func manageProgrammes(cfg *core.Config) error {
	api, err := controlClient(cfg)
	if err != nil {
		return err
	}
	args := cfg.ProgrammesArgs

	if len(args) == 0 || args[0] == "list" {
		return listProgrammes(api)
	}

	switch args[0] {
	case "show":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus programmes show <name|id>")
		}
		reg := registry.NewRegistry(cfg.RegistryPath())
		if err := reg.Init(); err != nil {
			return fmt.Errorf("initializing registry: %w", err)
		}
		return showProgramme(api, args[1], reg, mcpclient.New())
	case "status":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus programmes status <name|id>")
		}
		return programmeStatus(api, args[1])
	case "create":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus programmes create <name> [description...]")
		}
		return createProgramme(api, args[1], strings.Join(args[2:], " "))
	case "add-project":
		if len(args) < 3 {
			return fmt.Errorf("usage: daedalus programmes add-project <programme> <project>")
		}
		return addProjectToProgramme(api, args[1], args[2])
	case "add-dep":
		if len(args) < 4 {
			return fmt.Errorf("usage: daedalus programmes add-dep <programme> <upstream> <downstream>")
		}
		return addDepToProgramme(api, args[1], args[2], args[3])
	case "remove":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus programmes remove <name|id>")
		}
		return removeProgramme(api, args[1])
	default:
		return fmt.Errorf("unknown programmes command %q\n%s available: list, show, status, create, add-project, add-dep, remove",
			args[0], color.Cyan("Hint:"))
	}
}

// findProgramme resolves what a person typed — an id or a name — to a programme.
// Both are accepted because a person types the name and a client stores the id,
// and a CLI that took only one of them would disagree with the API.
func findProgramme(api control.TaskAPI, ref string) (control.Programme, error) {
	if p, err := api.GetProgramme(ref); err == nil {
		return p, nil
	}
	progs, err := api.ListProgrammes()
	if err != nil {
		return control.Programme{}, err
	}
	for _, p := range progs {
		if p.Name == ref {
			return p, nil
		}
	}
	return control.Programme{}, fmt.Errorf("%w: no programme %q", control.ErrNotFound, ref)
}

// listProgrammes prints every programme.
func listProgrammes(api control.TaskAPI) error {
	progs, err := api.ListProgrammes()
	if err != nil {
		return fmt.Errorf("listing programmes: %w", err)
	}
	if len(progs) == 0 {
		fmt.Println("No programmes. Form one with 'daedalus programmes create <name> <what it is for>'.")
		return nil
	}
	nameW := 4
	for _, p := range progs {
		if len(p.Name) > nameW {
			nameW = len(p.Name)
		}
	}
	fmt.Printf("%-6s  %-*s  %-9s  %-5s  %s\n", color.Bold("ID"), nameW, color.Bold("NAME"),
		color.Bold("PROJECTS"), color.Bold("DEPS"), color.Bold("FOR"))
	fmt.Printf("%-6s  %-*s  %-9s  %-5s  %s\n", "------", nameW, strings.Repeat("-", nameW),
		"--------", "----", "---")
	for _, p := range progs {
		fmt.Printf("%-6s  %-*s  %-9d  %-5d  %s\n", p.ID, nameW, p.Name, len(p.Projects), len(p.Deps),
			truncate(p.Description, 48))
	}
	return nil
}

// showProgramme prints a programme with per-project progress aggregation.
func showProgramme(api control.TaskAPI, ref string, reg *registry.Registry, client *mcpclient.Client) error {
	p, err := findProgramme(api, ref)
	if err != nil {
		return err
	}

	fmt.Printf("%s  %s  (%s)\n", color.Bold("Programme:"), p.Name, p.ID)
	if p.Description != "" {
		fmt.Printf("%s  %s\n", color.Bold("For:"), p.Description)
	}
	fmt.Printf("%s  %d\n", color.Bold("Projects:"), len(p.Projects))
	fmt.Printf("%s  %d\n\n", color.Bold("Dependencies:"), len(p.Deps))

	if len(p.Deps) > 0 {
		fmt.Println(color.Bold("Declared project order:"))
		for _, d := range p.Deps {
			fmt.Printf("  %s → %s\n", d.Upstream, d.Downstream)
		}
		// Said plainly, because the two graphs are easy to confuse: this one orders
		// a plan, and `task depends` is the one that actually blocks a landing.
		fmt.Printf("  %s\n\n", color.Dim("(declared order — what gates landing is the task graph; see `programmes status`)"))
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

// programmeStatus prints the roll-up: the work serving a programme, and what it
// is waiting on.
func programmeStatus(api control.TaskAPI, ref string) error {
	p, err := findProgramme(api, ref)
	if err != nil {
		return err
	}
	st, err := api.ProgrammeStatusFor(p.ID)
	if err != nil {
		return err
	}
	fmt.Printf("%s  %s  (%s)\n", color.Bold("Programme:"), st.Programme.Name, st.Programme.ID)
	if st.Programme.Description != "" {
		fmt.Printf("%s  %s\n", color.Bold("For:"), st.Programme.Description)
	}
	fmt.Printf("%s  %d task(s) — %d open, %d landed\n\n", color.Bold("Work:"), len(st.Tasks), st.Open, st.Landed)

	if len(st.Tasks) == 0 {
		fmt.Println("Nothing serves this programme yet. `daedalus task create --programme " +
			st.Programme.Name + " …`")
		return nil
	}
	fmt.Printf("  %-6s  %-16s  %-18s  %s\n", color.Bold("TASK"), color.Bold("PROJECT"),
		color.Bold("STATE"), color.Bold("OBJECTIVE"))
	for _, t := range st.Tasks {
		fmt.Printf("  %-6s  %-16s  %-18s  %s\n", t.ID, truncate(t.Project, 16), t.State, truncate(t.Objective, 44))
		if t.Rationale != "" {
			fmt.Printf("          %s %s\n", color.Dim("for:"), truncate(t.Rationale, 70))
		}
	}

	// The part a per-project view cannot show: work this programme waits on that
	// nobody put in it.
	if len(st.External) > 0 {
		fmt.Printf("\n%s\n", color.Bold("Waiting on work outside this programme:"))
		for _, e := range st.External {
			mark := color.Yellow("unmet")
			if e.Satisfied {
				mark = color.Green("landed")
			}
			where := e.Project
			if e.Programme != "" {
				where += " · " + e.Programme
			} else if e.Project != "" {
				where += " · " + color.Dim("no programme")
			}
			fmt.Printf("  %s → %s  [%s]  %s  %s\n", e.TaskID, e.DependsOn, e.State, mark, where)
		}
	}
	return nil
}

// createProgramme forms a programme.
func createProgramme(api control.TaskAPI, name, description string) error {
	p, err := api.CreateProgramme(control.ProgrammeRequest{Name: name, Description: description})
	if err != nil {
		return fmt.Errorf("creating programme: %w", err)
	}
	fmt.Printf("%s formed programme %s (%s)\n", color.Green("OK:"), p.Name, p.ID)
	if p.Description == "" {
		fmt.Printf("%s say what it is for: `daedalus programmes create` takes a description, "+
			"and a programme with no stated purpose cannot tell you later whether it was worth it.\n",
			color.Cyan("Hint:"))
	}
	return nil
}

// addProjectToProgramme adds a project, skipping one already present.
func addProjectToProgramme(api control.TaskAPI, ref, projectName string) error {
	p, err := findProgramme(api, ref)
	if err != nil {
		return err
	}
	for _, existing := range p.Projects {
		if existing == projectName {
			fmt.Printf("%s %s is already in %s\n", color.Yellow("Info:"), projectName, p.Name)
			return nil
		}
	}
	p.Projects = append(p.Projects, projectName)
	if _, err := api.UpdateProgramme(p.ID, programmeRequestFrom(p)); err != nil {
		return fmt.Errorf("adding project: %w", err)
	}
	fmt.Printf("%s added %s to %s\n", color.Green("OK:"), projectName, p.Name)
	return nil
}

// addDepToProgramme declares that downstream follows upstream.
func addDepToProgramme(api control.TaskAPI, ref, upstream, downstream string) error {
	p, err := findProgramme(api, ref)
	if err != nil {
		return err
	}
	p.Deps = append(p.Deps, core.DependencyEdge{Upstream: upstream, Downstream: downstream})
	if _, err := api.UpdateProgramme(p.ID, programmeRequestFrom(p)); err != nil {
		return fmt.Errorf("adding dependency: %w", err)
	}
	fmt.Printf("%s %s → %s in %s\n", color.Green("OK:"), upstream, downstream, p.Name)
	fmt.Printf("%s this declares an ORDER, it does not gate anything. To make a landing wait, "+
		"use `daedalus task depends <id> --on <other>`.\n", color.Cyan("Note:"))
	return nil
}

// removeProgramme dissolves a programme.
func removeProgramme(api control.TaskAPI, ref string) error {
	p, err := findProgramme(api, ref)
	if err != nil {
		return err
	}
	if err := api.DeleteProgramme(p.ID); err != nil {
		if errors.Is(err, control.ErrProgrammeInUse) {
			return fmt.Errorf("%w\n%s the tasks serving it record it as their reason; "+
				"dissolving it would erase that. Move or cancel them first (`daedalus programmes status %s`)",
				err, color.Cyan("Hint:"), p.Name)
		}
		return fmt.Errorf("removing programme: %w", err)
	}
	fmt.Printf("%s dissolved %s\n", color.Green("OK:"), p.Name)
	return nil
}

func programmeRequestFrom(p control.Programme) control.ProgrammeRequest {
	return control.ProgrammeRequest{
		Name: p.Name, Description: p.Description, Projects: p.Projects, Deps: p.Deps,
	}
}
