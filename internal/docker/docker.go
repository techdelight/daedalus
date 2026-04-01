// Copyright (C) 2026 Techdelight BV

package docker

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/executor"
)

// Docker manages Docker operations.
type Docker struct {
	Executor    executor.Executor
	ComposeFile string
}

// NewDocker creates a Docker with the given executor and compose file path.
func NewDocker(exec executor.Executor, composeFile string) *Docker {
	return &Docker{Executor: exec, ComposeFile: composeFile}
}

// IsContainerRunning checks if a container with the given name is running.
func (d *Docker) IsContainerRunning(name string) (bool, error) {
	out, err := d.Executor.Output("docker", "ps", "--format", "{{.Names}}")
	if err != nil {
		return false, fmt.Errorf("checking running containers: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == name {
			return true, nil
		}
	}
	return false, nil
}

// ImageExists checks if a Docker image exists locally.
func (d *Docker) ImageExists(image string) bool {
	_, err := d.Executor.Output("docker", "image", "inspect", image)
	return err == nil
}

// Build builds a Docker image with the given target stage.
func (d *Docker) Build(target, image, uid, contextDir string) error {
	fmt.Printf("Building %s (target: %s)...\n", image, target)
	return d.Executor.Run("docker", "build",
		"--target", target,
		"--build-arg", "CLAUDE_UID="+uid,
		"-t", image,
		contextDir,
	)
}

// ComposeRun executes a docker compose run command with environment variables
// scoped to the child process (no os.Setenv pollution).
// Delegates to ComposeRunCommand for arg construction (#20).
// When logFile is non-empty, container stdout/stderr are teed to the file.
func (d *Docker) ComposeRun(containerName string, env map[string]string, claudeArgs []string, extraArgs []string, logFile string) error {
	envSlice := make([]string, 0, len(env))
	for k, v := range env {
		envSlice = append(envSlice, k+"="+v)
	}
	cmdArgs := d.ComposeRunCommand(containerName, claudeArgs, extraArgs)

	if logFile == "" {
		return d.Executor.RunWithEnv(envSlice, cmdArgs[0], cmdArgs[1:]...)
	}

	f, err := os.Create(logFile)
	if err != nil {
		return fmt.Errorf("creating container log file: %w", err)
	}
	defer f.Close()

	c := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	c.Env = append(os.Environ(), envSlice...)
	c.Stdin = os.Stdin
	c.Stdout = io.MultiWriter(os.Stdout, f)
	c.Stderr = io.MultiWriter(os.Stderr, f)
	return c.Run()
}

// ComposeRunCommand returns the full docker compose command as a slice
// (for embedding in tmux send-keys). Env vars are exported separately
// by BuildTmuxCommand for compose interpolation.
func (d *Docker) ComposeRunCommand(containerName string, claudeArgs []string, extraArgs []string) []string {
	args := []string{"docker", "compose", "-f", d.ComposeFile, "run", "--rm", "--name", containerName}
	args = append(args, extraArgs...)
	args = append(args, "claude")
	args = append(args, claudeArgs...)
	return args
}

// SetupCacheDir ensures the per-project cache directory exists.
func SetupCacheDir(cfg *core.Config) error {
	cacheDir := cfg.CacheDir()
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			return fmt.Errorf("creating cache directory: %w", err)
		}
	}
	return nil
}

// SetupProjectDirs ensures bind-mounted project directories exist on the host
// before Docker runs. Without this, Docker creates missing mount sources as
// root:root, making them unwritable by the unprivileged container user.
func SetupProjectDirs(cfg *core.Config) error {
	dirs := []string{
		cfg.ProjectDir + "/.daedalus",
		cfg.ProjectDir + "/.claude/skills",
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	return nil
}
