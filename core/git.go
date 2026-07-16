// Copyright (C) 2026 Techdelight BV

package core

import "strings"

// ParseGitHEAD extracts the branch name from the contents of a .git/HEAD
// file.
//
// HEAD holds either a symbolic ref while on a branch ("ref: refs/heads/main")
// or a raw commit SHA when detached. Returns "" for the detached case, for a
// ref outside refs/heads/, and for anything unparseable: callers show a
// branch name or nothing at all, never a bare SHA dressed up as a branch.
//
// Pure by design: reading the file — and resolving the .git indirection that
// worktrees and submodules use — is the caller's problem.
func ParseGitHEAD(content string) string {
	rest, ok := strings.CutPrefix(strings.TrimSpace(content), "ref:")
	if !ok {
		return ""
	}
	branch, ok := strings.CutPrefix(strings.TrimSpace(rest), "refs/heads/")
	if !ok {
		return ""
	}
	return strings.TrimSpace(branch)
}
