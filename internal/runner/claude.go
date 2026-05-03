// Copyright (C) 2026 Techdelight BV

package runner

// ClaudeAdapter targets the Claude Code CLI (`claude`). The container
// image installs the binary at the canonical path below; running with
// `--dangerously-skip-permissions` is always safe because the container
// itself is the trust boundary.
type ClaudeAdapter struct{}

const (
	claudeBinary    = "/opt/claude/bin/claude"
	claudeConfigDir = "/home/claude/.claude-config"
)

func (ClaudeAdapter) Name() string { return "claude" }

func (ClaudeAdapter) Command(opts LaunchOptions) (string, []string, []string) {
	args := []string{"--dangerously-skip-permissions"}
	if opts.Debug {
		args = append(args, "--debug")
	}
	if opts.Resume != "" {
		args = append(args, "--resume", opts.Resume)
	}
	if opts.Prompt != "" {
		args = append(args, "--print", "--verbose", "-p", opts.Prompt)
	}
	env := []string{"CLAUDE_CONFIG_DIR=" + claudeConfigDir}
	return claudeBinary, args, env
}
