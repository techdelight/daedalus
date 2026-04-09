// Copyright (C) 2026 Techdelight BV

package activity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/techdelight/daedalus/core"
)

const (
	activityFile       = ".daedalus/activity.json"
	stalenessThreshold = 30 * time.Second
)

// activityRecord is the JSON format written by hooks inside the container.
type activityRecord struct {
	State  string `json:"state"`
	Detail string `json:"detail"`
	TS     string `json:"ts"`
}

// ClaudeCodeDetector reads .daedalus/activity.json to determine
// whether Claude Code is busy or idle inside a running container.
type ClaudeCodeDetector struct {
	now func() time.Time
}

// NewClaudeCodeDetector creates a detector with the real clock.
func NewClaudeCodeDetector() *ClaudeCodeDetector {
	return &ClaudeCodeDetector{now: time.Now}
}

// Detect reads the activity file and returns the runner's activity state.
// If the file is missing, unreadable, or stale, it defaults to idle.
func (d *ClaudeCodeDetector) Detect(projectDir string) core.ActivityInfo {
	path := filepath.Join(projectDir, activityFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return core.ActivityInfo{State: core.ActivityIdle}
	}

	var rec activityRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return core.ActivityInfo{State: core.ActivityIdle}
	}

	ts, err := time.Parse(time.RFC3339, rec.TS)
	if err != nil {
		return core.ActivityInfo{State: core.ActivityIdle}
	}

	if d.now().Sub(ts) > stalenessThreshold {
		return core.ActivityInfo{
			State:     core.ActivityIdle,
			UpdatedAt: rec.TS,
			Detail:    rec.Detail,
		}
	}

	state := core.ActivityIdle
	if rec.State == "busy" {
		state = core.ActivityBusy
	}

	return core.ActivityInfo{
		State:     state,
		UpdatedAt: rec.TS,
		Detail:    rec.Detail,
	}
}
