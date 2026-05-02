// Copyright (C) 2026 Techdelight BV

package main

import (
	"fmt"
	"strings"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
)

// manageRunners handles the "runners" subcommand for listing and showing
// built-in runner profiles.
func manageRunners(cfg *core.Config) error {
	args := cfg.RunnersArgs

	if len(args) == 0 || args[0] == "list" {
		return listRunners()
	}

	switch args[0] {
	case "show":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus runners show <name>")
		}
		return showRunner(args[1])
	default:
		return fmt.Errorf("unknown runners command %q\n%s available: list, show", args[0], color.Cyan("Hint:"))
	}
}

// listRunners prints all built-in runner profiles.
func listRunners() error {
	nameW := 4
	for _, name := range core.BuiltinRunnerNames() {
		if len(name) > nameW {
			nameW = len(name)
		}
	}
	fmt.Printf("%-*s  %s\n", nameW, color.Bold("NAME"), color.Bold("BINARY"))
	fmt.Printf("%-*s  %s\n", nameW, strings.Repeat("-", nameW), "------")
	for _, name := range core.BuiltinRunnerNames() {
		profile, _ := core.LookupBuiltinRunner(name)
		fmt.Printf("%-*s  %s\n", nameW, name, profile.BinaryPath)
	}
	return nil
}

// showRunner prints the details of a built-in runner profile.
func showRunner(name string) error {
	if !core.IsBuiltinRunner(name) {
		return fmt.Errorf("unknown runner %q — valid runners: %s", name, strings.Join(core.ValidRunnerNames(), ", "))
	}
	profile, _ := core.LookupBuiltinRunner(name)
	fmt.Printf("%s %s\n", color.Bold("Name:"), profile.Name)
	fmt.Printf("%s %s\n", color.Bold("Binary:"), profile.BinaryPath)
	if profile.DebugFlag != "" {
		fmt.Printf("%s %s\n", color.Bold("Debug flag:"), profile.DebugFlag)
	}
	if profile.ResumeFlag != "" {
		fmt.Printf("%s %s\n", color.Bold("Resume flag:"), profile.ResumeFlag)
	}
	return nil
}
