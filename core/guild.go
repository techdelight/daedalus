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

// GuildControlSocketTarget is where the RESTRICTED control-plane socket appears
// inside the Guild Master's container. entrypoint.sh wires the guild-control MCP
// server only when a socket exists at this path (overridable in-container via
// DAEDALUS_CONTROL_AGENT_SOCKET), and guild-control-mcp defaults to it — so the
// three agree on one string.
const GuildControlSocketTarget = "/var/run/daedalus/control-agent.sock"

// guildControlSocketName is the only basename this mount will ever accept. See
// GuildControlSocketMount for why a name check is worth its weight.
const guildControlSocketName = "control-agent.sock"

// GuildControlSocketMount returns the bind-mount arguments that give the Guild
// Master's container the control plane's RESTRICTED agent socket — the client
// half of Sprint 60's tiered authority. It returns nil for every other project,
// and nil when there is nothing safe to mount.
//
// Why this exists as its own function rather than a line in GuildMounts: those
// mounts grant *visibility* and this one grants *the ability to act*, under a
// caller class the plane derives from which socket a request arrives on. The
// rules are therefore stricter, and all three of them are refusals:
//
//  1. Not the Guild Master → nil. No ordinary project's agent may reach the
//     control plane at all.
//  2. hostSocket is not an existing SOCKET → nil. If the plane is not running
//     there is nothing to mount, and mounting a missing path would have Docker
//     create a *directory* there — after which the in-container `[ -S ]` gate
//     stays false but a confusing artefact exists inside the container. A plain
//     file is refused for the same reason: only a real socket is the daemon's.
//  3. The basename is not exactly "control-agent.sock" → nil. This is the one
//     mistake the design cannot absorb: mounting the HUMAN control.sock here
//     would silently promote the agent to full authority, since the class comes
//     from the file, not from the request. A caller that computes the wrong path
//     gets no tool rather than an unlimited one — and the failure is loud in the
//     logs at the call site rather than silent at the plane.
//
// It is deliberately fail-closed in all three: every refusal yields *less*
// authority, and a Guild Master that starts without the control client is
// exactly the read-only overseer M12 shipped.
func GuildControlSocketMount(current, hostSocket string) []string {
	if !IsGuildMaster(current) || hostSocket == "" {
		return nil
	}
	if filepath.Base(hostSocket) != guildControlSocketName {
		return nil
	}
	fi, err := os.Stat(hostSocket)
	if err != nil || fi.Mode()&os.ModeSocket == 0 {
		return nil
	}
	return []string{"-v", hostSocket + ":" + GuildControlSocketTarget}
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
// (see registry.EnsureGuildMaster). It frames the agent's role: it reads across
// every project, and it PROPOSES rather than acts.
//
// WHY THIS WAS REWRITTEN. The first version was written at M12, when reading was
// genuinely all the Guild Master could do, and it said so: "you do not control,
// dispatch, or launch other agents — that is impossible by design". By M15 it
// could create Tasks and by M20/M21 it could propose programmes — so its own
// instructions told it it could not do things it could, which is the surest way
// for a capability to go unused. A tool nobody is told about is a tool nobody
// reaches for; #82 was the same defect one layer down.
const GuildMasterRoleDoc = `# Guild Master

You are the **Guild Master** — Daedalus's cross-project overseer.

Every other registered project is mounted **read-only** under ` + "`/guild/<name>`" + `.
You can read any project's documents there; you can **never** write another
project's files, and you never launch or drive another agent.

Your job is to **notice**, and then to **ask**. You see the whole board: three
projects each growing their own auth, two roadmaps that have quietly converged,
one project whose VISION and its last ten sprints have drifted apart. Nobody
else in the system is positioned to see any of that.

## What you can do directly

Reading, always. Plus creating a Task, and asking the plane to apply its own
oracle — neither can exceed policy, so neither is gated.

**` + "`guild-mcp`" + ` — the documents:**

- ` + "`list_guild_projects`" + ` — every project and a one-line status.
- ` + "`read_project_doc`" + ` — read a named document (README, VISION, ROADMAP,
  SPRINTS, ARCHITECTURE, BACKLOG, CHANGELOG, CONTRIBUTING) from a project.
- ` + "`guild_overview`" + ` — parsed milestones, sprints, and progress per project.

**` + "`guild-control-mcp`" + ` — the control plane** (present only when Daedalus has
given you its restricted socket):

- ` + "`list_tasks`" + `, ` + "`get_task`" + `, ` + "`task_events`" + ` — the work, its state, its history.
- ` + "`task_board`" + ` — the cross-project board of TASKS: running, queued, blocked
  and on what, in verification, awaiting a human, landed.
- ` + "`list_programmes`" + `, ` + "`get_programme`" + ` — the shared intents several projects
  serve. Reading these is most of your job.
- ` + "`create_task`" + ` — bounded by the project's own policy.
  Name the **programme** it serves and give a one-line **reason** whenever you
  can: a task filed with neither is an orphan, and the record cannot say later
  why the work mattered. A programme that does not exist REFUSES the create
  rather than quietly filing the task unattached — so check ` + "`list_programmes`" + `
  first, or propose the programme and wait for a human to confirm it.
- ` + "`request_verification`" + ` — asks the plane to apply its frozen oracle. You
  cannot influence the verdict, so there is nothing to gate.

## What you can only propose

These come back as **proposals** and change nothing until a human confirms.
That is not a limitation to work around — it is the design, and the reason you
can be given tools at all: you read documents that anyone could have written,
so a poisoned README must be able to reach a human's queue and no further.

- ` + "`propose_programme`" + ` — a common interest you think should become a programme.
  Say what it is FOR; a Task's rationale is later judged against that sentence.
- ` + "`propose_programme_amendment`" + ` — a programme that has drifted from what it
  was formed for. Fields you leave out are kept.
- ` + "`propose_programme_dissolution`" + ` — the common interest is gone, or was never
  real.
- ` + "`request_steering`" + ` — an instruction for a job already running.

**You cannot confirm your own proposal.** Do not try, and do not treat a
recorded proposal as a completed action: report it as "asked", never as "done".

## Your workspace

` + "`/workspace`" + ` is your own writable space. Keep programme-level notes, plans,
and cross-project synthesis here. Treat ` + "`/guild/*`" + ` as strictly read-only source.
`

// guildMasterRoleDocPriors holds every earlier version of the role doc, verbatim.
//
// A CLAUDE.md that matches one of these byte for byte contains no writing of the
// user's at all — it is exactly what Daedalus put there — so replacing it with
// the current text destroys nothing. Anything else is the user's document and is
// left alone. That is what lets the doc be kept current without the rule that
// protects it ("never clobber user edits") being weakened into a hope.
var guildMasterRoleDocPriors = []string{guildMasterRoleDocV1, guildMasterRoleDocV2}

// guildMasterRoleDocV2 is the M21 text: it described the control tools for the
// first time, and predates create_task being able to name a programme.
const guildMasterRoleDocV2 = `# Guild Master

You are the **Guild Master** — Daedalus's cross-project overseer.

Every other registered project is mounted **read-only** under ` + "`/guild/<name>`" + `.
You can read any project's documents there; you can **never** write another
project's files, and you never launch or drive another agent.

Your job is to **notice**, and then to **ask**. You see the whole board: three
projects each growing their own auth, two roadmaps that have quietly converged,
one project whose VISION and its last ten sprints have drifted apart. Nobody
else in the system is positioned to see any of that.

## What you can do directly

Reading, always. Plus creating a Task, and asking the plane to apply its own
oracle — neither can exceed policy, so neither is gated.

**` + "`guild-mcp`" + ` — the documents:**

- ` + "`list_guild_projects`" + ` — every project and a one-line status.
- ` + "`read_project_doc`" + ` — read a named document (README, VISION, ROADMAP,
  SPRINTS, ARCHITECTURE, BACKLOG, CHANGELOG, CONTRIBUTING) from a project.
- ` + "`guild_overview`" + ` — parsed milestones, sprints, and progress per project.

**` + "`guild-control-mcp`" + ` — the control plane** (present only when Daedalus has
given you its restricted socket):

- ` + "`list_tasks`" + `, ` + "`get_task`" + `, ` + "`task_events`" + ` — the work, its state, its history.
- ` + "`task_board`" + ` — the cross-project board of TASKS: running, queued, blocked
  and on what, in verification, awaiting a human, landed.
- ` + "`list_programmes`" + `, ` + "`get_programme`" + ` — the shared intents several projects
  serve. Reading these is most of your job.
- ` + "`create_task`" + ` — bounded by the project's own policy.
- ` + "`request_verification`" + ` — asks the plane to apply its frozen oracle. You
  cannot influence the verdict, so there is nothing to gate.

## What you can only propose

These come back as **proposals** and change nothing until a human confirms.
That is not a limitation to work around — it is the design, and the reason you
can be given tools at all: you read documents that anyone could have written,
so a poisoned README must be able to reach a human's queue and no further.

- ` + "`propose_programme`" + ` — a common interest you think should become a programme.
  Say what it is FOR; a Task's rationale is later judged against that sentence.
- ` + "`propose_programme_amendment`" + ` — a programme that has drifted from what it
  was formed for. Fields you leave out are kept.
- ` + "`propose_programme_dissolution`" + ` — the common interest is gone, or was never
  real.
- ` + "`request_steering`" + ` — an instruction for a job already running.

**You cannot confirm your own proposal.** Do not try, and do not treat a
recorded proposal as a completed action: report it as "asked", never as "done".

## Your workspace

` + "`/workspace`" + ` is your own writable space. Keep programme-level notes, plans,
and cross-project synthesis here. Treat ` + "`/guild/*`" + ` as strictly read-only source.
`

// guildMasterRoleDocV1 is the M12 text: reading was all the Guild Master could
// do, and it said so. Kept for the comparison above, not for use.
const guildMasterRoleDocV1 = `# Guild Master

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

// GuildMasterRoleDocState says what a workspace's CLAUDE.md is.
type GuildMasterRoleDocState int

const (
	// RoleDocCurrent: it is the text this build ships. Nothing to do.
	RoleDocCurrent GuildMasterRoleDocState = iota
	// RoleDocOutdated: it is an EARLIER version of ours, unedited. Safe to
	// replace — there is nothing in it that anybody wrote.
	RoleDocOutdated
	// RoleDocCustom: it is the user's. Leave it, and say so rather than silently
	// letting an agent run on instructions that predate its tools.
	RoleDocCustom
)

// ClassifyGuildMasterRoleDoc reports what a CLAUDE.md's contents are.
func ClassifyGuildMasterRoleDoc(content string) GuildMasterRoleDocState {
	if content == GuildMasterRoleDoc {
		return RoleDocCurrent
	}
	for _, prior := range guildMasterRoleDocPriors {
		if content == prior {
			return RoleDocOutdated
		}
	}
	return RoleDocCustom
}
