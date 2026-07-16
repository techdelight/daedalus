// Copyright (C) 2026 Techdelight BV

package core

import "testing"

func TestParseGitHEAD(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"on a branch", "ref: refs/heads/development\n", "development"},
		{"no trailing newline", "ref: refs/heads/main", "main"},
		{"slashes in branch name", "ref: refs/heads/feat/runner-default-flip\n", "feat/runner-default-flip"},
		{"dashes and digits", "ref: refs/heads/fix-38-repaint\n", "fix-38-repaint"},
		{"leading whitespace", "  ref: refs/heads/main  \n", "main"},

		// Detached HEAD holds a raw SHA. Showing it as a branch would be a
		// lie, so it reads as "no branch".
		{"detached HEAD", "9fceb02d0ae598e95dc970b74767f19372d61af8\n", ""},

		// A ref outside refs/heads/ is not a branch.
		{"tag ref", "ref: refs/tags/v0.39.0\n", ""},
		{"remote ref", "ref: refs/remotes/origin/main\n", ""},

		{"empty", "", ""},
		{"whitespace only", "   \n", ""},
		{"garbage", "not a head file", ""},
		{"ref prefix but no target", "ref:", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseGitHEAD(tt.content); got != tt.want {
				t.Errorf("ParseGitHEAD(%q) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}
