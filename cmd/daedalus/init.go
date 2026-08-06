// Copyright (C) 2026 Techdelight BV

package main

import (
	"fmt"
	"os"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
)

// runInit is the first-run entry point: it gets a freshly-installed user from
// "installed" to "first session".
//
// It scaffolds the required project docs into a directory (default: cwd) by
// reusing core.ScaffoldDocs — the same conformant skeletons `docs scaffold`
// writes, so a new project starts with a valid roadmap arc — then prints a short
// getting-started guide. It is idempotent: existing docs are skipped (never
// clobbered) unless --force, and re-running only reprints the guide.
//
// Flag/arg parsing mirrors scaffoldDocs: an optional single directory, --force
// (seeded from the global parser, which owns the flag), and --no-scaffold to
// print guidance only.
func runInit(cfg *core.Config) error {
	dir := ""
	noScaffold := false
	force := cfg.Force
	for _, a := range cfg.InitArgs {
		switch {
		case a == "--no-scaffold":
			noScaffold = true
		case a == "--force":
			force = true
		case len(a) > 0 && a[0] == '-':
			return fmt.Errorf("unknown flag %q\n%s daedalus init [--force] [--no-scaffold] [dir]", a, color.Cyan("Hint:"))
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

	if noScaffold {
		fmt.Printf("%s skipping doc scaffold (--no-scaffold)\n", color.Dim("—"))
	} else {
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
	}

	printGettingStarted(dir)
	return nil
}

// printGettingStarted prints a tight, scannable guide to a new user's next
// steps: register & start a project, reattach, the two dashboards, and the docs
// gate. dir is woven into the register-and-start example so the guide points at
// the directory just initialised.
func printGettingStarted(dir string) {
	fmt.Println()
	fmt.Println(color.Bold("Getting started with Daedalus"))
	fmt.Println("Hands-off AI coding: your agent runs autonomously in a locked-down")
	fmt.Println("Docker container that can only touch the project directory.")
	fmt.Println()
	fmt.Println(color.Bold("Next steps:"))
	fmt.Printf("  1. Register & start a project   %s\n", color.Cyan("daedalus <name> "+dir))
	fmt.Printf("  2. Reattach after detaching     %s\n", color.Cyan("daedalus <name>"))
	fmt.Printf("  3. Manage projects in a TUI     %s\n", color.Cyan("daedalus tui"))
	fmt.Printf("  4. Or a web dashboard           %s\n", color.Cyan("daedalus web"))
	fmt.Printf("  5. Check your project docs      %s\n", color.Cyan("daedalus docs lint"))
	fmt.Println()
	fmt.Printf("Detach a running session with %s; multiple UIs can attach in parallel.\n", color.Bold("Ctrl-D"))
	fmt.Printf("Full command reference: %s\n", color.Cyan("daedalus --help"))
}
