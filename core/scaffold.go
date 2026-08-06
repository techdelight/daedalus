// Copyright (C) 2026 Techdelight BV

package core

import (
	"fmt"
	"os"
	"path/filepath"
)

// docTemplates holds the starter body for every document in RequiredDocs(),
// keyed by filename. This is the single source of truth for scaffolded output:
// the ROADMAP.md and SPRINTS.md bodies here are written to satisfy the same
// strict-format checks `daedalus docs lint` enforces (see scaffold_test.go,
// which scaffolds these, re-parses them, and asserts ValidateDocs +
// LintHeadings find nothing), so a fresh project starts with a valid arc.
//
// Which files exist is not decided here — ScaffoldDocs iterates RequiredDocs()
// so there is one list of documents, not two — but every filename it names must
// have an entry below or the scaffold fails loudly rather than writing a
// partial set.
var docTemplates = map[string]string{
	"README.md": `# Project Name

One-line description of what this project is and who it is for.

## Quick Start

` + "```bash" + `
# The shortest path from clone to running.
` + "```" + `

## Usage

Describe the main ways to use the project.

## Documentation

- [VISION.md](VISION.md) — why this project exists
- [ARCHITECTURE.md](ARCHITECTURE.md) — how it is built
- [ROADMAP.md](ROADMAP.md) — the milestone arc
- [SPRINTS.md](SPRINTS.md) — the current sprint and history
- [BACKLOG.md](BACKLOG.md) — unscheduled work
- [CHANGELOG.md](CHANGELOG.md) — released changes
- [CONTRIBUTING.md](CONTRIBUTING.md) — how to contribute
`,

	"VISION.md": `# Vision

## Problem

What problem does this project solve, and why does it matter?

## Target Users

Who is this for?

## Success Metrics

How will we know it is working?

## Non-Goals

What this project deliberately does not do.

## Principles

The guiding constraints and principles that shape decisions here.
`,

	"ARCHITECTURE.md": `# Architecture

## Overview

A high-level description of how the system is put together.

## Modules

| Module | Responsibility |
|--------|----------------|
| _tbd_  | _tbd_          |

## Dependencies

External systems, services, and libraries this project relies on.
`,

	"ROADMAP.md": `# Roadmap

The strategic milestone arc — where this project is going. Milestones move from
Planned to In Progress to Done; sprints that execute them live in ` + "`SPRINTS.md`" + `.

## Milestones

### Milestone 1: Project Foundations (In Progress)

The initial, foundational body of work for this project. Replace this
description with the real first milestone, and add further milestones below as
the arc takes shape. Exactly one milestone should be marked (In Progress).

## Phasing

` + "```" + `
M1 (In Progress)
Project Foundations
` + "```" + `

## Current Focus

**Milestone 1: Project Foundations.** This is the milestone under way now.
Summarise what it delivers and why it is the current focus; the sprint driving
it is the current sprint in ` + "`SPRINTS.md`" + `.
`,

	"BACKLOG.md": `# Backlog

Unscheduled, unprioritised work items. Add rows as ideas arrive; promote an item
into a sprint in ` + "`SPRINTS.md`" + ` when it is scheduled.

| # | Item |
|---|------|
| 1 | Replace this row with the first backlog item |
`,

	"SPRINTS.md": `# Sprints

The execution record — the sprint under way now, and the ones already shipped.
Each sprint links to the ` + "`ROADMAP.md`" + ` milestone it advances.

## Current Sprint

### Sprint 1: Project Setup

Goal: describe the aim of the first sprint — the concrete work that moves
Milestone 1 forward.

Milestone: 1

| # | Item | Status |
|---|------|--------|
| 1 | Replace this row with the first work item |  |

## Sprint History

_No sprints yet._
`,

	"CHANGELOG.md": `# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

- Initial project scaffold.
`,

	"CONTRIBUTING.md": `# Contributing

## Getting Started

How to set up a development environment.

## Workflow

Branching, commits, and how changes are integrated into the default branch.

## Tests

Tests are first-class, not optional. Describe how to run them.

## Definition of Done

What "done" means for a change in this project.
`,
}

// ScaffoldDocs writes a conformant skeleton for each RequiredDocs() document
// into dir, creating dir if it does not exist.
//
// A document that already exists is left untouched and returned in skipped,
// never overwritten — unless force is set, in which case it is rewritten from
// the template and counted as created. created and skipped are returned in
// RequiredDocs() order, so the caller can print a summary that reads down the
// canonical set.
//
// The ROADMAP.md and SPRINTS.md skeletons satisfy the structured-docs contract:
// `daedalus docs lint --ci` reports nothing on freshly scaffolded output.
func ScaffoldDocs(dir string, force bool) (created []string, skipped []string, err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("creating %s: %w", dir, err)
	}

	for _, doc := range RequiredDocs() {
		body, ok := docTemplates[doc.Filename]
		if !ok {
			// A required document with no template is a programming error: the
			// two lists have drifted. Fail rather than write a partial set.
			return created, skipped, fmt.Errorf("no scaffold template for required document %q", doc.Filename)
		}

		path := filepath.Join(dir, doc.Filename)
		if !force {
			if _, statErr := os.Stat(path); statErr == nil {
				skipped = append(skipped, doc.Filename)
				continue
			} else if !os.IsNotExist(statErr) {
				return created, skipped, fmt.Errorf("checking %s: %w", doc.Filename, statErr)
			}
		}

		if writeErr := os.WriteFile(path, []byte(body), 0o644); writeErr != nil {
			return created, skipped, fmt.Errorf("writing %s: %w", doc.Filename, writeErr)
		}
		created = append(created, doc.Filename)
	}

	return created, skipped, nil
}
