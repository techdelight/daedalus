// Copyright (C) 2026 Techdelight BV

package runner

// CopilotAdapter targets the GitHub Copilot CLI (`copilot`). Like
// claude, the container is the trust boundary, so `--allow-all` is
// always emitted. Copilot has no debug flag today, so opts.Debug is
// silently ignored.
type CopilotAdapter struct{}

const (
	copilotBinary    = "/usr/local/bin/copilot"
	copilotConfigDir = "/home/claude/.copilot"
)

func (CopilotAdapter) Name() string { return "copilot" }

func (CopilotAdapter) Command(opts LaunchOptions) (string, []string, []string) {
	args := []string{"--allow-all"}
	if opts.Resume != "" {
		args = append(args, "--resume", opts.Resume)
	}
	if opts.Prompt != "" {
		args = append(args, "-p", opts.Prompt)
	}
	env := []string{"COPILOT_HOME=" + copilotConfigDir}
	return copilotBinary, args, env
}
