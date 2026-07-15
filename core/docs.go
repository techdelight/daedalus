// Copyright (C) 2026 Techdelight BV

package core

// RequiredDoc describes one document Daedalus expects a project to carry.
// Together the set answers: why the project exists (VISION), what it is and
// how to use it (README), how it is built (ARCHITECTURE), where it is going
// (ROADMAP, BACKLOG, SPRINTS), and what has changed and how to contribute
// (CHANGELOG, CONTRIBUTING).
type RequiredDoc struct {
	Name        string `json:"name"`
	Filename    string `json:"filename"`
	Description string `json:"description"`
}

// RequiredDocs returns the canonical document set in display order.
//
// Pure by design: whether a file is present on disk is the caller's problem
// (see internal/web/docs.go), keeping this package free of I/O. The set is
// hardcoded for now; per-project opt-out is Backlog #53.
//
// A fresh slice is returned on each call so callers cannot mutate the set.
func RequiredDocs() []RequiredDoc {
	return []RequiredDoc{
		{Name: "README", Filename: "README.md", Description: "What the project is and how to use it"},
		{Name: "Vision", Filename: "VISION.md", Description: "Why the project exists"},
		{Name: "Architecture", Filename: "ARCHITECTURE.md", Description: "How the project is built"},
		{Name: "Roadmap", Filename: "ROADMAP.md", Description: "Strategic milestones"},
		{Name: "Backlog", Filename: "BACKLOG.md", Description: "Unscheduled work items"},
		{Name: "Sprints", Filename: "SPRINTS.md", Description: "Sprint execution record"},
		{Name: "Changelog", Filename: "CHANGELOG.md", Description: "Released changes"},
		{Name: "Contributing", Filename: "CONTRIBUTING.md", Description: "How to contribute"},
	}
}
