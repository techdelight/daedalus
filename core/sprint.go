// Copyright (C) 2026 Techdelight BV

package core

// SprintStatus represents the status of a sprint item.
type SprintStatus string

const (
	StatusPending    SprintStatus = ""
	StatusDone       SprintStatus = "Done"
	StatusInProgress SprintStatus = "In Progress"
)

// SprintItem represents a single item in a sprint.
type SprintItem struct {
	Number      int          `json:"number"`
	Description string       `json:"description"`
	Status      SprintStatus `json:"status"`
}

// Sprint represents a parsed sprint from a ROADMAP.md file.
type Sprint struct {
	Number    int          `json:"number"`
	Title     string       `json:"title"`
	Version   string       `json:"version,omitempty"`
	Goal      string       `json:"goal,omitempty"`
	Items     []SprintItem `json:"items"`
	IsCurrent bool         `json:"isCurrent,omitempty"`
}
