// Copyright (C) 2026 Techdelight BV

package progress

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Data represents the progress.json file format.
type Data struct {
	ProgressPct    int    `json:"progressPct,omitempty"`
	Vision         string `json:"vision,omitempty"`
	ProjectVersion string `json:"projectVersion,omitempty"`
	Message        string `json:"message,omitempty"`
}

// Read reads progress data from the .daedalus/progress.json file in the given directory.
// Returns zero-value Data if the file does not exist.
func Read(projectDir string) (Data, error) {
	path := filepath.Join(projectDir, ".daedalus", "progress.json")
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Data{}, nil
		}
		return Data{}, fmt.Errorf("reading progress: %w", err)
	}
	var d Data
	if err := json.Unmarshal(b, &d); err != nil {
		return Data{}, fmt.Errorf("parsing progress: %w", err)
	}
	return d, nil
}

// Write writes progress data to .daedalus/progress.json in the given directory.
// Creates the .daedalus/ directory if it doesn't exist.
func Write(projectDir string, d Data) error {
	dir := filepath.Join(projectDir, ".daedalus")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating .daedalus directory: %w", err)
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling progress: %w", err)
	}
	b = append(b, '\n')
	path := filepath.Join(dir, "progress.json")
	if err := os.WriteFile(path, b, 0644); err != nil {
		return fmt.Errorf("writing progress: %w", err)
	}
	return nil
}

// Update reads the current progress, applies the given non-zero/non-empty fields, and writes it back.
func Update(projectDir string, pct int, vision, projectVersion, message string) error {
	d, err := Read(projectDir)
	if err != nil {
		return err
	}
	if pct > 0 {
		if pct > 100 {
			pct = 100
		}
		d.ProgressPct = pct
	}
	if vision != "" {
		d.Vision = vision
	}
	if projectVersion != "" {
		d.ProjectVersion = projectVersion
	}
	if message != "" {
		d.Message = message
	}
	return Write(projectDir, d)
}
