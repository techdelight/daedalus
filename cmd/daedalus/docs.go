// Copyright (C) 2026 Techdelight BV

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
)

// manageDocs handles the "docs" subcommand: checks that a project's documents
// keep the conventions the dashboard arc is parsed from.
func manageDocs(cfg *core.Config) error {
	args := cfg.DocsArgs
	if len(args) == 0 || args[0] == "help" {
		printDocsUsage()
		return nil
	}

	switch args[0] {
	case "lint":
		return lintDocs(args[1:])
	case "scaffold":
		return scaffoldDocs(args[1:], cfg.Force)
	default:
		return fmt.Errorf("unknown docs command %q\n%s daedalus docs help", args[0], color.Cyan("Hint:"))
	}
}

// lintDocs runs the document checks over a project directory.
//
// It gathers everything the arc is built from — the strict-format heading
// checks that catch what the parsers silently drop, then the cross-file
// consistency checks over what they parsed — and reports it as one list. The
// exit code is the gate: any error fails; with --ci a warning fails too.
func lintDocs(args []string) error {
	ci := false
	dir := ""
	for _, a := range args {
		switch {
		case a == "--ci" || a == "--strict":
			ci = true
		case len(a) > 0 && a[0] == '-':
			return fmt.Errorf("unknown flag %q\n%s daedalus docs lint [--ci] [dir]", a, color.Cyan("Hint:"))
		case dir == "":
			dir = a
		default:
			return fmt.Errorf("too many arguments; expected at most one directory")
		}
	}
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolving working directory: %w", err)
		}
		dir = wd
	}

	roadmap, hasRoadmap, err := readDoc(dir, "ROADMAP.md")
	if err != nil {
		return err
	}
	sprintsDoc, hasSprints, err := readDoc(dir, "SPRINTS.md")
	if err != nil {
		return err
	}
	if !hasRoadmap && !hasSprints {
		fmt.Printf("%s no ROADMAP.md or SPRINTS.md in %s; nothing to lint\n", color.Dim("—"), dir)
		return nil
	}

	// Sprints live in SPRINTS.md, falling back to ROADMAP.md for projects that
	// never split them out — the same precedence the readers use.
	sprintSource := sprintsDoc
	if !hasSprints {
		sprintSource = roadmap
	}

	milestones := core.ParseMilestones(roadmap)
	sprints := core.ParseSprints(sprintSource)

	var findings []core.Finding
	// Heading checks first: they explain an entry that is missing downstream,
	// which a consistency check over the parsed result could never mention.
	if hasRoadmap {
		findings = append(findings, core.LintHeadings("ROADMAP.md", roadmap)...)
	}
	if hasSprints {
		findings = append(findings, core.LintHeadings("SPRINTS.md", sprintsDoc)...)
	}
	findings = append(findings, core.ValidateDocs(milestones, sprints)...)

	return reportFindings(findings, ci)
}

// scaffoldDocs writes conformant skeletons for the required project documents
// into a directory, mirroring lintDocs's flag/arg parsing: an optional single
// directory (default: cwd) and a --force flag.
//
// It exists so a new project starts with a valid roadmap arc instead of an empty
// tree: the ROADMAP.md and SPRINTS.md it writes already pass `daedalus docs
// lint`. An existing file is left untouched (and reported as skipped) unless
// --force is given, so re-running it never clobbers hand-written docs.
//
// force is seeded from the global flag parser, which owns --force (prune uses it
// too); a --force sitting after the subcommand is honoured there and never
// reaches these args, so accepting it here as well only keeps the local parse
// self-describing.
func scaffoldDocs(args []string, force bool) error {
	dir := ""
	for _, a := range args {
		switch {
		case a == "--force":
			force = true
		case len(a) > 0 && a[0] == '-':
			return fmt.Errorf("unknown flag %q\n%s daedalus docs scaffold [--force] [dir]", a, color.Cyan("Hint:"))
		case dir == "":
			dir = a
		default:
			return fmt.Errorf("too many arguments; expected at most one directory")
		}
	}
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolving working directory: %w", err)
		}
		dir = wd
	}

	created, skipped, err := core.ScaffoldDocs(dir, force)
	if err != nil {
		return err
	}

	for _, name := range created {
		fmt.Printf("%s %s\n", color.Green("✓"), name)
	}
	for _, name := range skipped {
		fmt.Printf("%s %s (exists; use --force to overwrite)\n", color.Dim("—"), name)
	}

	fmt.Printf("\n%d created, %d skipped in %s\n", len(created), len(skipped), dir)
	fmt.Printf("%s run %s to check them\n", color.Cyan("Hint:"), color.Bold("daedalus docs lint"))
	return nil
}

// readDoc reads one document from dir. A missing file is not an error — it is
// simply absent, the same convention the parsers and readers keep.
func readDoc(dir, name string) (content string, present bool, err error) {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("reading %s: %w", name, err)
	}
	return string(data), true, nil
}

// reportFindings prints the findings, errors before warnings, and returns a
// non-nil error when the gate should fail: any error, or — under --ci — any
// warning. The returned error carries the count so the caller's exit is 1.
func reportFindings(findings []core.Finding, ci bool) error {
	if len(findings) == 0 {
		fmt.Printf("%s documents are consistent\n", color.Green("✓"))
		return nil
	}

	// Errors first, then warnings; order within a severity is preserved so the
	// output still reads as a walk through the documents.
	sort.SliceStable(findings, func(i, j int) bool {
		return findings[i].Severity == core.SeverityError && findings[j].Severity != core.SeverityError
	})

	var errs, warns int
	for _, f := range findings {
		label := color.Yellow("warning")
		if f.Severity == core.SeverityError {
			label = color.Red("error")
			errs++
		} else {
			warns++
		}
		fmt.Printf("%s %s: %s\n", label, f.Doc, f.Message)
	}

	fmt.Printf("\n%d error(s), %d warning(s)\n", errs, warns)

	if errs > 0 {
		return fmt.Errorf("%d error(s) in project documents", errs)
	}
	if ci && warns > 0 {
		return fmt.Errorf("%d warning(s) in project documents (--ci)", warns)
	}
	return nil
}

func printDocsUsage() {
	fmt.Println(`daedalus docs — check project documents against the dashboard-arc format

Usage:
  daedalus docs lint [--ci] [dir]       Check ROADMAP.md and SPRINTS.md in dir
                                        (default: current directory)
  daedalus docs scaffold [--force] [dir] Write conformant doc skeletons into dir
                                        (default: current directory)

Flags:
  --ci     (lint) Treat warnings as failures too (exit non-zero on any finding)
  --force  (scaffold) Overwrite documents that already exist

Exit status of lint is 0 when the documents are consistent, non-zero on an error
(or, with --ci, on any warning) — so it can gate a commit, a session, or CI.
scaffold writes the required-doc set (skipping any that already exist) so a fresh
project passes docs lint out of the box.`)
}
