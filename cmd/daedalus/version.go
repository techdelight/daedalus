// Copyright (C) 2026 Techdelight BV

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/versions"
)

// manageVersions implements `daedalus version <list|use|rollback|prune>`, the
// switch/rollback/prune surface over the side-by-side versioned install layout.
//
// The install prefix is derived from the running binary (see
// versions.ResolvePrefix), which lives at $PREFIX/versions/<v>/daedalus. Tests
// and advanced setups can override it with DAEDALUS_PREFIX.
func manageVersions(cfg *core.Config) error {
	args := cfg.VersionArgs
	action := "list"
	if len(args) > 0 {
		action = args[0]
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine executable path: %w", err)
	}
	prefix, err := versions.ResolvePrefix(exe)
	if err != nil {
		return err
	}
	layout := versions.Layout{Prefix: prefix}
	binDir := versionBinDir()

	switch action {
	case "list", "ls":
		return versionList(layout)
	case "use", "switch":
		if len(args) < 2 {
			return fmt.Errorf("usage: daedalus version use <version>")
		}
		return versionUse(layout, args[1], binDir)
	case "rollback":
		return versionRollback(layout, binDir)
	case "prune":
		keep, err := parseKeepFlag(args[1:])
		if err != nil {
			return err
		}
		return versionPrune(layout, keep)
	case "help", "--help", "-h":
		printVersionUsage()
		return nil
	default:
		return fmt.Errorf("unknown version action %q\n%s daedalus version <list|use|rollback|prune>", action, color.Cyan("Hint:"))
	}
}

// versionBinDir is the directory holding the PATH symlink. DAEDALUS_BIN_DIR
// overrides the default (~/.local/bin) so tests can point at a fake bin dir.
func versionBinDir() string {
	if d := os.Getenv("DAEDALUS_BIN_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "bin")
}

func versionList(layout versions.Layout) error {
	infos, err := layout.List()
	if err != nil {
		return err
	}
	if len(infos) == 0 {
		fmt.Println("No installed versions found.")
		fmt.Printf("%s this build may be a legacy flat install; reinstall to adopt versioned layout.\n", color.Dim("—"))
		return nil
	}
	fmt.Println(color.Bold("Installed versions:"))
	for _, info := range infos {
		marker := "  "
		label := info.Version
		if info.Current {
			marker = color.Green("* ")
			label = color.Bold(info.Version) + color.Dim(" (current)")
		}
		fmt.Printf("%s%s\n", marker, label)
	}
	if prev := layout.Previous(); prev != "" {
		fmt.Printf("%s previous: %s\n", color.Dim("—"), prev)
	}
	return nil
}

func versionUse(layout versions.Layout, version, binDir string) error {
	ok, err := layout.Use(version, binDir)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Printf("Already on version %s.\n", version)
		return nil
	}
	fmt.Printf("%s switched to version %s\n", color.Green("✓"), color.Bold(version))
	return nil
}

func versionRollback(layout versions.Layout, binDir string) error {
	target, err := layout.Rollback(binDir)
	if err != nil {
		return err
	}
	fmt.Printf("%s rolled back to version %s\n", color.Green("✓"), color.Bold(target))
	return nil
}

func versionPrune(layout versions.Layout, keep int) error {
	removed, err := layout.Prune(keep)
	if err != nil {
		return err
	}
	if len(removed) == 0 {
		fmt.Println("Nothing to prune.")
		return nil
	}
	for _, v := range removed {
		fmt.Printf("%s removed %s\n", color.Dim("—"), v)
	}
	fmt.Printf("Pruned %d version(s); kept current %s.\n", len(removed), layout.Current())
	return nil
}

// parseKeepFlag reads an optional "--keep N" from the prune args. A missing
// flag yields -1, which versions.Prune maps to DefaultPruneKeep.
func parseKeepFlag(args []string) (int, error) {
	keep := -1
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--keep":
			if i+1 >= len(args) {
				return 0, fmt.Errorf("--keep requires a number")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				return 0, fmt.Errorf("--keep requires a non-negative integer, got %q", args[i])
			}
			keep = n
		default:
			return 0, fmt.Errorf("unknown prune flag %q (usage: daedalus version prune [--keep N])", args[i])
		}
	}
	return keep, nil
}

func printVersionUsage() {
	fmt.Println(color.Bold("daedalus version") + " — manage side-by-side installed versions")
	fmt.Println()
	fmt.Printf("%s daedalus version <command>\n", color.Bold("Usage:"))
	fmt.Println()
	fmt.Println(color.Bold("Commands:"))
	fmt.Println("  list                 List installed versions, marking the current one")
	fmt.Println("  use <version>        Switch the active version (records the prior as previous)")
	fmt.Println("  rollback             Switch back to the previous version")
	fmt.Println("  prune [--keep N]     Remove old versions, keeping the last N (default " + strconv.Itoa(versions.DefaultPruneKeep) + ") plus current")
	fmt.Println()
	fmt.Println("The active version is selected via the $PREFIX/current symlink; the PATH")
	fmt.Println("symlink resolves through it, so switching never rewrites your PATH entry.")
}
