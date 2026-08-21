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
	"github.com/techdelight/daedalus/internal/logging"
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

// Build builds a Docker image with the given target stage, and reclaims the
// image the build supersedes.
//
// The tag is constant for a given runner+target (Config.Image), so every rebuild
// retags it and leaves the PREVIOUS image carrying no tag at all — a `<none>`
// entry that nothing in daedalus ever removed. Rebuilds are not rare: build.go
// rebuilds automatically whenever the build files' checksum changes. A runner
// image is gigabytes, so this is the most expensive thing the tool leaked.
//
// Reclaiming is best-effort and never fails the build: the image the user asked
// for exists either way, and refusing to return a successful build because a
// cleanup failed would trade a disk cost for a broken command.
func (d *Docker) Build(target, image, uid, contextDir string) error {
	superseded := d.imageID(image)

	fmt.Printf("Building %s (target: %s)...\n", image, target)
	if err := d.Executor.Run("docker", "build",
		"--target", target,
		"--build-arg", "CLAUDE_UID="+uid,
		"-t", image,
		contextDir,
	); err != nil {
		return err
	}

	d.reclaimSuperseded(image, superseded)
	return nil
}

// imageID returns the image id a tag currently resolves to, or "" if there is no
// such image (or docker cannot be asked).
func (d *Docker) imageID(image string) string {
	out, err := d.Executor.Output("docker", "image", "inspect", "-f", "{{.Id}}", image)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// reclaimSuperseded removes the image a rebuild displaced.
//
// Three properties, and the first is the one that would hurt:
//
//   - **Never remove the image just built.** A rebuild that changes nothing
//     resolves to the SAME id (every layer cached), and deleting it because it
//     was also the previous id would delete the working image. Hence the
//     current != superseded guard rather than trusting "we rebuilt, so it moved".
//   - **By id, and without -f.** After the retag the old image is untagged, so
//     removing its id removes only it. If it still carries another tag, or a
//     container still depends on it, docker refuses — which is the desired
//     answer, not an error to force past.
//   - **Scoped to what daedalus made.** Deliberately NOT `docker image prune`,
//     which would reclaim dangling images belonging to everything else on the
//     machine. The tool cleans up after itself and nothing more.
func (d *Docker) reclaimSuperseded(image, superseded string) {
	if superseded == "" {
		return // nothing was there before
	}
	current := d.imageID(image)
	if current == "" || current == superseded {
		return
	}
	// Output rather than Run: a refusal here is expected and benign, and printing
	// docker's complaint would read as a failure of the build that just succeeded.
	if _, err := d.Executor.Output("docker", "rmi", superseded); err != nil {
		logging.Info("superseded image " + shortID(superseded) + " not reclaimed: " + err.Error())
	}
}

// shortID abbreviates a docker image id ("sha256:abcd…") for a log line.
func shortID(id string) string {
	trimmed := strings.TrimPrefix(id, "sha256:")
	if len(trimmed) > 12 {
		return trimmed[:12]
	}
	return trimmed
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

// ComposeRunCommand returns the full docker compose command as a slice.
// Env vars for compose interpolation are supplied separately by ComposeRun.
func (d *Docker) ComposeRunCommand(containerName string, claudeArgs []string, extraArgs []string) []string {
	// -p for the same reason as the coordinator's: an unpinned project name is
	// derived from the versioned install directory and leaks a network per
	// upgrade. See core.ComposeProject.
	args := []string{"docker", "compose", "-p", core.ComposeProject, "-f", d.ComposeFile, "run", "--rm", "--name", containerName}
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
