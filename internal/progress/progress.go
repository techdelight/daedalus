// Copyright (C) 2026 Techdelight BV

package progress

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/techdelight/daedalus/core"
)

// Data is a project's read-side state: its version, its vision tagline, and how
// far the current sprint has progressed.
//
// Historically this was the shape of a `.daedalus/progress.json` file that the
// in-container agent wrote through self-report MCP tools (report_progress /
// set_vision / set_version). As of Backlog #52 those tools are gone and every
// field is *derived* from the project's own files on each Read — read state no
// longer depends on the agent remembering to report it. The struct shape is
// kept unchanged so its consumers did not have to churn; only the source did.
type Data struct {
	ProgressPct    int    `json:"progressPct,omitempty"`
	Vision         string `json:"vision,omitempty"`
	ProjectVersion string `json:"projectVersion,omitempty"`
	// Message no longer has a source — it was the agent's free-form self-report
	// note. It is retained (always empty) so the JSON shape consumers decode is
	// unchanged.
	Message string `json:"message,omitempty"`
}

// Read derives a project's state from its files:
//
//   - ProjectVersion — the trimmed contents of VERSION.
//   - Vision — the trimmed contents of VISION.md.
//   - ProgressPct — the current sprint's completion, as the ratio of Done items
//     to total items in SPRINTS.md (falling back to a legacy ROADMAP.md).
//
// Every source is optional: an absent file yields that field's zero value
// rather than an error, because a half-populated project is a normal, valid
// state. Read only errors on a genuine I/O failure (e.g. an unreadable file
// that does exist).
func Read(projectDir string) (Data, error) {
	var d Data

	version, err := readTrimmed(filepath.Join(projectDir, "VERSION"))
	if err != nil {
		return Data{}, err
	}
	d.ProjectVersion = version

	vision, err := readTrimmed(filepath.Join(projectDir, "VISION.md"))
	if err != nil {
		return Data{}, err
	}
	d.Vision = vision

	pct, err := currentSprintPct(projectDir)
	if err != nil {
		return Data{}, err
	}
	d.ProgressPct = pct

	return d, nil
}

// currentSprintPct is the completion percentage of the current sprint, taken
// from the ratio of Done items to total items. It is 0 when there is no current
// sprint, or the current sprint has no items — an itemless sprint has nothing to
// measure, not zero progress to invent.
func currentSprintPct(projectDir string) (int, error) {
	content, err := readSprintsContent(projectDir)
	if err != nil {
		return 0, err
	}
	if content == "" {
		return 0, nil
	}
	for _, s := range core.ParseSprints(content) {
		if !s.IsCurrent {
			continue
		}
		done, total := core.SprintProgress(s)
		if total == 0 {
			return 0, nil
		}
		return done * 100 / total, nil
	}
	return 0, nil
}

// readTrimmed returns the trimmed contents of a file, or "" if it does not
// exist. A file that exists but cannot be read is a real error.
func readTrimmed(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}
	return strings.TrimSpace(string(b)), nil
}

// readSprintsContent reads SPRINTS.md, falling back to a legacy ROADMAP.md that
// still holds sprints. Returns "" when neither exists.
func readSprintsContent(projectDir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(projectDir, "SPRINTS.md"))
	if err == nil {
		return string(b), nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("reading SPRINTS.md: %w", err)
	}
	b, err = os.ReadFile(filepath.Join(projectDir, "ROADMAP.md"))
	if err == nil {
		return string(b), nil
	}
	if os.IsNotExist(err) {
		return "", nil
	}
	return "", fmt.Errorf("reading ROADMAP.md: %w", err)
}
