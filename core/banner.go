// Copyright (C) 2026 Techdelight BV

package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ReadVersion reads the VERSION file from scriptDir and returns the trimmed string.
// Returns "unknown" if the file cannot be read.
func ReadVersion(scriptDir string) string {
	data, err := os.ReadFile(filepath.Join(scriptDir, "VERSION"))
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(data))
}

// PrintBanner displays the Techdelight logo, version, and build timestamp.
func PrintBanner(scriptDir string) {
	logo, err := os.ReadFile(filepath.Join(scriptDir, "logo.txt"))
	if err == nil {
		fmt.Println(strings.TrimRight(string(logo), "\n"))
	}

	version, err := os.ReadFile(filepath.Join(scriptDir, "VERSION"))
	if err == nil {
		fmt.Printf("Version: %s\n", strings.TrimSpace(string(version)))
	}

	exe, err := os.Executable()
	if err == nil {
		info, err := os.Stat(exe)
		if err == nil {
			fmt.Printf("Build:   %s\n", info.ModTime().Format("2006-01-02 15:04:05"))
		}
	}

	fmt.Println()
}
