// Copyright (C) 2026 Techdelight BV

package core

// ActivityState represents the three observable states of a project runner.
type ActivityState string

const (
	ActivityBusy     ActivityState = "busy"
	ActivityIdle     ActivityState = "idle"
	ActivitySleeping ActivityState = "sleeping"
)

// ActivityInfo holds the current activity state and metadata for a project runner.
type ActivityInfo struct {
	State     ActivityState `json:"state"`
	UpdatedAt string        `json:"updatedAt"`
	Detail    string        `json:"detail"`
}
