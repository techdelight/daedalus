# Skill Catalog

This directory contains shared skills (Claude Code slash commands) that are
available to all Daedalus projects.

## Format

Each skill is a markdown file (`.md`). The first heading line is used as the
skill description when listing the catalog.

Example skill file (`my-skill.md`):

```markdown
# My Skill

Describe what the skill does here. Claude Code will use this as the prompt
when the user invokes `/my-skill`.

## Instructions

1. Step one
2. Step two
```

## Managing Skills

From inside a project container, use the MCP tools:

- `list_skills` — browse available skills
- `read_skill` — view a skill's content
- `install_skill` — install a skill for the current project
- `create_skill` — publish a new skill to the catalog

From the host, use `daedalus skills` to manage the catalog directly.
