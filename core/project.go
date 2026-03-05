// Copyright (C) 2026 Techdelight BV

package core

// CurrentRegistryVersion is the latest registry schema version.
const CurrentRegistryVersion = 2

// RegistryData is the top-level JSON structure for the project registry.
type RegistryData struct {
	Version  int                     `json:"version"`
	Projects map[string]ProjectEntry `json:"projects"`
}

// SessionRecord tracks a single agent session.
type SessionRecord struct {
	ID       string `json:"id"`
	Started  string `json:"started"`
	Ended    string `json:"ended,omitempty"`
	Duration int    `json:"duration,omitempty"` // seconds
	ResumeID string `json:"resumeId,omitempty"`
}

// ProjectEntry holds metadata for a registered project.
type ProjectEntry struct {
	Directory    string            `json:"directory"`
	Target       string            `json:"target"`
	Created      string            `json:"created"`
	LastUsed     string            `json:"lastUsed"`
	DefaultFlags map[string]string `json:"defaultFlags,omitempty"`
	Sessions     []SessionRecord   `json:"sessions,omitempty"`
}

// ProjectInfo holds a project name alongside its registry entry.
type ProjectInfo struct {
	Name  string
	Entry ProjectEntry
}
