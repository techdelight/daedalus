// Copyright (C) 2026 Techdelight BV

// Package coordinator owns the host-side lifecycle of runner-attached
// containers. It is the role tmux used to play in the legacy stack
// (the "where does this session live, and how do I reach it" layer)
// rebuilt around daedalus-runner Unix sockets — tmux is not involved.
//
// One Coordinator instance manages many sessions. A Session is one
// project's running container plus the host-visible path of its
// daedalus-runner socket. Multiple UI surfaces (CLI, Web, future TUI)
// dial that socket through internal/runclient; the coordinator does
// not own the attach side.
//
// Scope of this first slice: in-process, in-memory, single host. No
// daemon, no persistence, no IPC. A second process started against
// the same data dir will not see sessions tracked by the first.
// Persistence and cross-process discovery are deliberate follow-ups.
package coordinator

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/executor"
)

// Session describes one running runner-attached project container.
// Returned by Start, Get, and List. SocketPath is the host-side path
// callers pass to runclient.Dial. JSON tags exist so the daemon HTTP
// surface (daemon.go) can marshal Session directly to clients.
type Session struct {
	ProjectName   string    `json:"project_name"`
	ContainerName string    `json:"container_name"`
	SocketPath    string    `json:"socket_path"`
	StartedAt     time.Time `json:"started_at"`
}

// Options configures a Coordinator. Executor and ComposeFile are
// required; SocketWait defaults to 30s when zero.
type Options struct {
	Executor    executor.Executor
	ComposeFile string
	SocketWait  time.Duration
}

// Coordinator tracks the live runner sessions on this host.
type Coordinator struct {
	exec        executor.Executor
	composeFile string
	socketWait  time.Duration
	pollEvery   time.Duration

	mu       sync.Mutex
	sessions map[string]*Session
}

// New constructs a Coordinator. Panics if Executor or ComposeFile is
// missing — both are required for any Start call to succeed and a
// nil-check at every Start would just defer the failure.
func New(opts Options) *Coordinator {
	if opts.Executor == nil {
		panic("coordinator: Executor is required")
	}
	if opts.ComposeFile == "" {
		panic("coordinator: ComposeFile is required")
	}
	wait := opts.SocketWait
	if wait == 0 {
		wait = 30 * time.Second
	}
	return &Coordinator{
		exec:        opts.Executor,
		composeFile: opts.ComposeFile,
		socketWait:  wait,
		pollEvery:   100 * time.Millisecond,
		sessions:    make(map[string]*Session),
	}
}

// ErrAlreadyRunning is returned by Start when the coordinator already
// tracks a live session for the same project.
var ErrAlreadyRunning = errors.New("coordinator: session already running")

// ErrNotFound is returned by Get and Stop when no session is tracked
// for the given project name.
var ErrNotFound = errors.New("coordinator: no session for project")

// Start launches the project container with daedalus-runner as its
// entrypoint, waits for the runner socket to appear, and records the
// session. Returns ErrAlreadyRunning if a session for this project is
// already tracked.
//
// Lifecycle:
//   - mkdir + cleanup of the socket directory (stale sockets block bind);
//   - `docker compose -f <file> run --rm --detach --name <ctr> claude`
//     with DAEDALUS_RUNNER=1 and DAEDALUS_SOCKET set in the env, so
//     entrypoint.sh dispatches to /usr/local/bin/daedalus-runner;
//   - poll for the host-visible socket file until it appears or the
//     SocketWait deadline expires (the container may still be coming up
//     after `run --detach` returns).
func (c *Coordinator) Start(cfg *core.Config) (*Session, error) {
	name := cfg.ProjectName
	containerName := cfg.ContainerName()

	c.mu.Lock()
	if _, exists := c.sessions[name]; exists {
		c.mu.Unlock()
		return nil, ErrAlreadyRunning
	}
	c.mu.Unlock()

	sockPath := cfg.RunnerSocketPath()
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o755); err != nil {
		return nil, fmt.Errorf("creating socket directory: %w", err)
	}
	_ = os.Remove(sockPath) // stale socket from a previous run blocks bind

	env := composeEnv(cfg)
	args := []string{
		"compose", "-f", c.composeFile,
		"run", "--rm", "--detach", "--name", containerName,
		"claude",
	}
	if err := c.exec.RunWithEnv(env, "docker", args...); err != nil {
		return nil, fmt.Errorf("docker compose run --detach: %w", err)
	}

	if err := waitForSocket(sockPath, c.socketWait, c.pollEvery); err != nil {
		return nil, err
	}

	sess := &Session{
		ProjectName:   name,
		ContainerName: containerName,
		SocketPath:    sockPath,
		StartedAt:     time.Now(),
	}
	c.mu.Lock()
	c.sessions[name] = sess
	c.mu.Unlock()
	return sess, nil
}

// Get returns the tracked session for the given project name. The
// boolean is false when no session is tracked; callers that want an
// error should compare against ErrNotFound from Stop instead.
func (c *Coordinator) Get(name string) (*Session, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.sessions[name]
	return s, ok
}

// List returns a snapshot of all tracked sessions, ordered by start
// time (oldest first). The returned slice is independent of internal
// state; mutating it is safe.
func (c *Coordinator) List() []Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Session, 0, len(c.sessions))
	for _, s := range c.sessions {
		out = append(out, *s)
	}
	// Stable order so callers don't need to re-sort.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].StartedAt.Before(out[j-1].StartedAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Stop tells docker to stop the container, then removes the session
// from the in-memory map. Returns ErrNotFound if no session is tracked.
//
// The session is only removed if `docker stop` succeeds. If docker
// fails, the session stays in the map so callers can still find it
// via Get/List and retry Stop — losing track of a half-stopped
// container would leave a zombie no one can reach.
func (c *Coordinator) Stop(name string) error {
	c.mu.Lock()
	sess, ok := c.sessions[name]
	c.mu.Unlock()
	if !ok {
		return ErrNotFound
	}

	// Local copy: the docker call is unlocked so concurrent List/Get
	// keep seeing the session while the stop is in flight.
	if err := c.exec.Run("docker", "stop", sess.ContainerName); err != nil {
		return fmt.Errorf("docker stop %s: %w", sess.ContainerName, err)
	}

	c.mu.Lock()
	delete(c.sessions, name)
	c.mu.Unlock()
	return nil
}

// composeEnv builds the env-var slice the runner-detached compose run
// needs. PROJECT_DIR / CACHE_DIR / TARGET / IMAGE / RUNNER mirror the
// regular compose path; DAEDALUS_RUNNER + DAEDALUS_SOCKET tell the
// container's entrypoint to launch daedalus-runner instead of the
// runner CLI directly. DAEDALUS_DEBUG / RESUME / PROMPT are forwarded
// when set so the runner can wire them into the agent.
func composeEnv(cfg *core.Config) []string {
	env := []string{
		"PROJECT_DIR=" + cfg.ProjectDir,
		"CACHE_DIR=" + cfg.CacheDir(),
		"TARGET=" + cfg.Target,
		"IMAGE=" + cfg.Image(),
		"RUNNER=" + core.ResolveRunnerName(cfg),
		"DAEDALUS_RUNNER=1",
		"DAEDALUS_SOCKET=/home/claude/.daedalus/runner.sock",
	}
	if cfg.Debug {
		env = append(env, "DAEDALUS_DEBUG=1")
	}
	if cfg.Resume != "" {
		env = append(env, "DAEDALUS_RESUME="+cfg.Resume)
	}
	if cfg.Prompt != "" {
		env = append(env, "DAEDALUS_PROMPT="+cfg.Prompt)
	}
	return env
}

// waitForSocket polls for path to appear on disk. The runner-detached
// `docker compose run` returns once the container ID is known; the
// daedalus-runner inside still has a brief window before binding.
func waitForSocket(path string, timeout, poll time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("daedalus-runner socket %s did not appear within %s", path, timeout)
		}
		time.Sleep(poll)
	}
}
