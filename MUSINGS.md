# Musings

Generative ideas for the future direction of Daedalus as an agent/project/programme orchestration tool. Generated 2026-03-29.

---

## 1. Programme-Level Orchestration: Multi-Project Dependency Graphs

Daedalus already manages multiple projects, but they're independent. What if you could declare *relationships* between projects — "service-api depends on shared-lib" — and have Daedalus orchestrate a build cascade? A change in `shared-lib` triggers its agent, then propagates to downstream projects. This turns Daedalus from a project manager into a **programme manager**, closer to the Daedalus myth: the architect who builds the whole labyrinth, not just one room.

## 2. Agent Swarms: Multiple Agents per Project

Right now it's one container, one agent, one project. But complex work benefits from specialization — one agent writes code, another reviews it, a third writes tests. Daedalus could orchestrate a *team* of persona-scoped agents working on the same codebase, coordinating via a shared branch and a message bus (or just MCP). Think of it as mob programming, except the mob is machines.

## 3. The "Foreman" Agent: Daedalus as an AI-Driven Project Manager

Backlog items 14, 16, 17, and 18 already hint at this: an MCP server that reports progress, ACP integration for real-time agent state, and Daedalus as an MCP client reading roadmaps. Take it further — Daedalus itself becomes an AI agent (the "Foreman") that reads a ROADMAP.md, decomposes it into sprint items, assigns them to worker agents, monitors progress via ACP/MCP, and reports status through the Web UI. The user's role shifts from *operator* to *stakeholder*.

## 4. Session Replay and Post-Mortem Analysis

Daedalus already persists session transcripts. What if you could replay a session — not just read it, but *analyze* it? Feed the transcript to Claude and ask "where did this agent go wrong?" or "what was the most time-consuming step?" This turns session history into a learning tool. Combine with backlog item 13 (project dashboard with time spent and % complete) for a lightweight **agent observability** layer.

## 5. Composable Personas as an "Agent Marketplace"

The persona system (runner + overlay) is already clean. Imagine a public or team-shared persona catalog — like the skill catalog but for entire agent configurations. "The Security Auditor," "The Migration Specialist," "The Documentation Writer." Personas become the unit of sharing, and the skill catalog attaches naturally as the persona's toolkit. This is the difference between shipping a tool and shipping a *craftsperson*.

## 6. Contract-Based Agent Handoffs (CQS for Agents)

The codebase already follows CQS. Apply the same principle to agent orchestration: an agent either *queries* (reads code, investigates, produces a report) or *commands* (writes code, commits, creates PRs). A "reviewer" persona only queries; a "developer" persona only commands. Handoffs between them become explicit contracts — the reviewer produces findings, the developer consumes them. This prevents the common failure mode where an autonomous agent both writes and reviews its own work.

## 7. Time-Boxing and Budget Envelopes

Autonomous agents can burn API credits in a spiral. Daedalus could enforce *time budgets* or *token budgets* per session or per sprint item. "You have 30 minutes and 50k tokens to fix this bug." When the budget runs out, the agent writes a status report and yields. This is how real project management works — constraints breed creativity — and it protects the user's wallet.

## 8. The "Labyrinth" — Sandboxed Experimentation Branches

Daedalus already isolates via Docker. Extend this to *git isolation*: before an agent starts a task, Daedalus creates a worktree branch. The agent works freely. When done, the user (or Foreman) reviews and merges. If the agent went down a dead end, discard the branch with zero cost. This is the labyrinth metaphor made literal — the agent explores, and if it gets lost, you just seal off that corridor.

## 9. Event-Driven Agent Triggers

Right now, a human starts agents. What if Daedalus watched for *events* — a new GitHub issue labeled `agent-ready`, a failing CI pipeline, a cron schedule, a webhook from Slack? The event triggers a project start with a specific persona and prompt. Daedalus becomes a **reactive orchestrator** — the missing bridge between "things that happen" and "agents that respond."

## 10. Cross-Project Knowledge Graph via MCP

The skill catalog MCP server is a start. Imagine every project's MCP server exposing not just progress but *knowledge* — "this project uses PostgreSQL 15," "the auth module was rewritten last week," "the API contract is defined in openapi.yaml." Daedalus aggregates this into a cross-project knowledge graph. When an agent in Project A needs to call an API from Project B, it can discover the contract via MCP without the user explaining it. This is how organizations actually work — institutional knowledge, made machine-readable.

## 11. The "Icarus" Warning System

Named for the myth's other half. When an agent is doing something risky — force-pushing, deleting files, modifying CI config, touching security-sensitive code — Daedalus intercepts and flags it. Not by blocking (the whole point is autonomy), but by surfacing it in the Web UI as a warning with a "revert" button. The agent flies, but Daedalus watches the altitude.

## 12. Portable Project Capsules

A Daedalus project is already a bundle: code + persona + skills + MCP config + session history. Package this as a shareable artifact — a "project capsule" — that a teammate can import and immediately have the same agent environment. This solves the "it works on my agent" problem and makes agent setups reproducible across teams.
