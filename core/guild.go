// Copyright (C) 2026 Techdelight BV

package core

import (
	"os"
	"path/filepath"
	"strings"
)

// GuildMountRoot is the in-container directory under which every OTHER
// project's workspace is mounted read-only for the Guild Master, one
// subdirectory per project: /guild/<name>. guild-mcp reads this tree (its root
// is overridable via GUILD_ROOT so tests can point at a fixture).
const GuildMountRoot = "/guild"

// GuildMounts returns the read-only bind-mount arguments that expose every
// OTHER registered project's directory to the Guild Master container at
// /guild/<name>. It is the cross-project visibility that gives the Guild Master
// its purpose: it can read every project, and — because every mount is :ro —
// never write another's files.
//
// It returns nil unless current is the Guild Master: cross-project visibility is
// the Guild Master's alone, so a normal project launch gets no /guild mounts.
// The Guild Master itself is skipped (it does not mount itself), as is any entry
// with an empty directory or one that is not a directory on disk (a stale
// registry row must not fail the launch). <name> is a validated slug already;
// it is sanitised again here (reject empty, ".", "..", or anything with a path
// separator) so a malformed registry key can never escape /guild.
//
// Pure in the sense that matters: it reaches into no global state and is fed its
// project list by the call site. It does stat each candidate directory to skip
// missing ones — the one unavoidable filesystem touch, and exactly what the
// "missing dir skipped" contract requires.
func GuildMounts(current string, projects []ProjectInfo) []string {
	if !IsGuildMaster(current) {
		return nil
	}
	var args []string
	for _, p := range projects {
		if p.Name == current || IsGuildMaster(p.Name) {
			continue // never mount the Guild Master into itself
		}
		if p.Entry.Directory == "" {
			continue
		}
		name := sanitiseGuildMountName(p.Name)
		if name == "" {
			continue // malformed key that could escape /guild
		}
		if fi, err := os.Stat(p.Entry.Directory); err != nil || !fi.IsDir() {
			continue // missing/removed directory — skip, don't fail the launch
		}
		args = append(args, "-v", p.Entry.Directory+":"+GuildMountRoot+"/"+name+":ro")
	}
	return args
}

// sanitiseGuildMountName returns name if it is safe to use as a single path
// segment under /guild, or "" if it is not. Project names are already validated
// slugs (ValidateProjectName forbids path separators), so this is defence in
// depth: it rejects the empty string, "." and "..", anything containing a path
// separator, and anything that does not survive filepath.Base unchanged.
func sanitiseGuildMountName(name string) string {
	if name == "" || name == "." || name == ".." {
		return ""
	}
	if strings.ContainsAny(name, `/\`) {
		return ""
	}
	if filepath.Base(name) != name {
		return ""
	}
	return name
}

// GuildMasterRoleDoc is the CLAUDE.md seeded into the Guild Master's workspace
// on first create (see registry.EnsureGuildMaster). It frames the agent's role:
// a read-only programme overseer that reads /guild/* and drafts programme-level
// plans. It is written once and never clobbers a user's own edits.
const GuildMasterRoleDoc = `# Guild Master

You are the **Guild Master** — Daedalus's read-only programme overseer.

Every other registered project is mounted **read-only** under ` + "`/guild/<name>`" + `.
You can read any project's documents there; you can **never** write another
project's files. You do not control, dispatch, or launch other agents — that is
impossible by design and explicitly out of scope. Your job is *visibility*: read
across the guild and draft programme-level plans and synthesis.

## Tools (the ` + "`guild-mcp`" + ` server)

- ` + "`list_guild_projects`" + ` — every project and a one-line status.
- ` + "`read_project_doc`" + ` — read a named document (README, VISION, ROADMAP,
  SPRINTS, ARCHITECTURE, BACKLOG, CHANGELOG, CONTRIBUTING) from a project.
- ` + "`guild_overview`" + ` — parsed milestones, sprints, and progress per project.

## Your workspace

` + "`/workspace`" + ` is your own writable space. Keep programme-level notes, plans,
and cross-project synthesis here. Treat ` + "`/guild/*`" + ` as strictly read-only source.
`
