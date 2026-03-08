// Copyright (C) 2026 Techdelight BV

package core

import (
	"fmt"
	"os"
	"strings"
)

// Version is set at compile time via -ldflags:
//
//	go build -ldflags "-X github.com/techdelight/daedalus/core.Version=0.5.2"
var Version = "unknown"

// ReadVersion returns the compile-time version baked into the binary.
func ReadVersion() string {
	return Version
}

// PrintBanner displays the Techdelight logo, version, and build timestamp.
func PrintBanner(scriptDir string) {
	logo, err := os.ReadFile(scriptDir + "/logo.txt")
	if err == nil {
		fmt.Println(strings.TrimRight(string(logo), "\n"))
	}

	fmt.Printf("Version: %s\n", Version)

	exe, err := os.Executable()
	if err == nil {
		info, err := os.Stat(exe)
		if err == nil {
			fmt.Printf("Build:   %s\n", info.ModTime().Format("2006-01-02 15:04:05"))
		}
	}

	fmt.Println()
}
