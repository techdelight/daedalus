// Copyright (C) 2026 Techdelight BV

package main

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// parseGitHubURL checks if input is a GitHub URL or shorthand (owner/repo).
// Returns the clone URL, extracted repo name, and whether it matched.
func parseGitHubURL(input string) (cloneURL, repoName string, ok bool) {
	// Full URL: https://github.com/owner/repo or https://github.com/owner/repo.git
	if strings.HasPrefix(input, "https://github.com/") || strings.HasPrefix(input, "http://github.com/") {
		u, err := url.Parse(input)
		if err != nil {
			return "", "", false
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) < 2 {
			return "", "", false
		}
		repo := strings.TrimSuffix(parts[1], ".git")
		cloneURL = "https://github.com/" + parts[0] + "/" + parts[1]
		if !strings.HasSuffix(cloneURL, ".git") {
			cloneURL += ".git"
		}
		return cloneURL, repo, true
	}
	// Shorthand: owner/repo (exactly one slash, no dots or colons)
	if strings.Count(input, "/") == 1 && !strings.Contains(input, ":") && !strings.Contains(input, ".") {
		parts := strings.Split(input, "/")
		if parts[0] != "" && parts[1] != "" {
			return "https://github.com/" + input + ".git", parts[1], true
		}
	}
	return "", "", false
}

// cloneGitRepo clones a git repository to the specified directory.
func cloneGitRepo(repoURL, targetDir string) error {
	if _, err := os.Stat(targetDir); err == nil {
		// Directory already exists — assume it's a previous clone
		fmt.Printf("Directory '%s' already exists, skipping clone.\n", targetDir)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(targetDir), 0755); err != nil {
		return fmt.Errorf("creating projects directory: %w", err)
	}
	fmt.Printf("Cloning %s...\n", repoURL)
	cmd := exec.Command("git", "clone", repoURL, targetDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cloning repository: %w", err)
	}
	return nil
}
