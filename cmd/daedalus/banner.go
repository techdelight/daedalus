// Copyright (C) 2026 Techdelight BV

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/techdelight/daedalus/core"
)

// printBanner displays the Techdelight logo, version, and build timestamp.
func printBanner(scriptDir string) {
	logo, err := os.ReadFile(scriptDir + "/logo.txt")
	if err == nil {
		fmt.Println(strings.TrimRight(string(logo), "\n"))
	}

	fmt.Printf("Version: %s\n", core.Version)

	exe, err := os.Executable()
	if err == nil {
		info, err := os.Stat(exe)
		if err == nil {
			fmt.Printf("Build:   %s\n", info.ModTime().Format("2006-01-02 15:04:05"))
		}
	}

	fmt.Println()
}
