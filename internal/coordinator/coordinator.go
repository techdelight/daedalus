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
// The package supports two deployment modes:
//   - in-process, in-memory (no Options.SessionsFile): a single Go
//     process holds all state; a second process against the same data
//     dir sees no shared sessions.
//   - daemon-mode (Options.SessionsFile set): sessions are persisted
//     to a JSON file and reconciled against `docker ps` on New so a
//     restarted daemon inherits still-live sessions and forgets dead
//     ones. This is what cmd/daedalus-coordinator uses.
//
// The daemon also exposes an HTTP-over-Unix-socket API (daemon.go),
// which a Go client (client.go) speaks to.
package coordinator

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
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

	// SessionsFile, if set, is the path to a JSON file where the
	// coordinator persists its session map. On New, the file is read
	// and each recorded session is reconciled against `docker ps` —
	// only sessions whose container is still running are kept.
	// Subsequent Start/Stop calls rewrite the file atomically. Empty
	// means in-memory only (the default before item 2 of Sprint 40).
	SessionsFile string
}

// Coordinator tracks the live runner sessions on this host.
type Coordinator struct {
	exec         executor.Executor
	composeFile  string
	socketWait   time.Duration
	pollEvery    time.Duration
	sessionsFile string

	mu       sync.Mutex
	sessions map[string]*Session
}

// New constructs a Coordinator. Panics if Executor or ComposeFile is
// missing — both are required for any Start call to succeed and a
// nil-check at every Start would just defer the failure.
//
// When Options.SessionsFile is set, New reads it and reconciles the
// recorded sessions against `docker ps`. Load or reconcile errors are
// logged but do not stop New from returning — a corrupt persistence
// file must not prevent the daemon from starting.
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
	c := &Coordinator{
		exec:         opts.Executor,
		composeFile:  opts.ComposeFile,
		socketWait:   wait,
		pollEvery:    100 * time.Millisecond,
		sessionsFile: opts.SessionsFile,
		sessions:     make(map[string]*Session),
	}
	if c.sessionsFile != "" {
		if err := c.loadAndReconcile(); err != nil {
			log.Printf("coordinator: sessions.json load/reconcile: %v (starting with empty state)", err)
		}
	}
	return c
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
//   - `docker compose -f <file> run --rm --detach -e DAEDALUS_RUNNER=1
//     -e DAEDALUS_SOCKET=... --name <ctr> claude`, so entrypoint.sh
//     dispatches to /usr/local/bin/daedalus-runner. The DAEDALUS_* vars
//     must be -e flags — docker-compose.yml references none of them, so
//     the docker CLI's process env alone never reaches the container;
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
	args := []string{"compose", "-f", c.composeFile, "run", "--rm", "--detach"}
	// The DAEDALUS_* vars must reach the CONTAINER, so they are -e flags,
	// not process env: docker-compose.yml interpolates none of them, so
	// composeEnv alone would leave the entrypoint on the classic path.
	for _, kv := range runnerContainerEnv(cfg) {
		args = append(args, "-e", kv)
	}
	args = append(args, "--name", containerName, "claude")
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
	c.persistLocked()
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
	c.persistLocked()
	c.mu.Unlock()
	return nil
}

// containerSocketPath is where daedalus-runner binds its Unix socket
// INSIDE the container. The compose mount `${CACHE_DIR}:/home/claude`
// maps it to the host path cfg.RunnerSocketPath(), which Start polls.
const containerSocketPath = "/home/claude/.daedalus/runner.sock"

// composeEnv builds the process environment for the `docker` CLI. These
// vars are consumed by docker-compose for ${VAR} interpolation inside
// docker-compose.yml (IMAGE, the volume mounts, RUNNER) — they do NOT
// automatically reach the container. Anything the *container* must see
// has to be an explicit `-e` flag on `docker compose run`; see
// runnerContainerEnv.
func composeEnv(cfg *core.Config) []string {
	return []string{
		"PROJECT_DIR=" + cfg.ProjectDir,
		"CACHE_DIR=" + cfg.CacheDir(),
		"TARGET=" + cfg.Target,
		"IMAGE=" + cfg.Image(),
		"RUNNER=" + core.ResolveRunnerName(cfg),
	}
}

// runnerContainerEnv returns the KEY=VALUE pairs that must be injected
// INTO the container so entrypoint.sh dispatches to daedalus-runner
// instead of exec'ing claude directly. They are passed as
// `docker compose run -e KEY=VALUE` flags — deliberately NOT via
// composeEnv: that slice is only the docker CLI's process environment,
// which compose uses solely for ${VAR} interpolation, and
// docker-compose.yml references none of these. Passing them as -e is
// what actually reaches the container (and, unlike interpolation, is
// assertable in a unit test).
func runnerContainerEnv(cfg *core.Config) []string {
	kv := []string{
		"DAEDALUS_RUNNER=1",
		"DAEDALUS_SOCKET=" + containerSocketPath,
	}
	if cfg.Debug {
		kv = append(kv, "DAEDALUS_DEBUG=1")
	}
	if cfg.Resume != "" {
		kv = append(kv, "DAEDALUS_RESUME="+cfg.Resume)
	}
	if cfg.Prompt != "" {
		kv = append(kv, "DAEDALUS_PROMPT="+cfg.Prompt)
	}
	return kv
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

// loadAndReconcile reads the sessions file and drops any recorded
// session whose container is not currently reported by `docker ps`.
// Missing file → clean slate (not an error). Corrupt JSON → treated as
// clean slate with a returned error so the caller can log. Docker-ps
// failure is fatal to reconciliation: we prefer "start empty" over
// "inherit possibly-dead state" when we can't verify.
func (c *Coordinator) loadAndReconcile() error {
	data, err := os.ReadFile(c.sessionsFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", c.sessionsFile, err)
	}
	var stored []Session
	if err := json.Unmarshal(data, &stored); err != nil {
		return fmt.Errorf("parse %s: %w", c.sessionsFile, err)
	}
	if len(stored) == 0 {
		return nil
	}

	running, err := c.dockerRunningContainers()
	if err != nil {
		return fmt.Errorf("reconcile via `docker ps`: %w", err)
	}

	c.mu.Lock()
	kept := 0
	for i := range stored {
		s := stored[i]
		if running[s.ContainerName] {
			// Take the address of the loop-local copy so entries don't
			// alias each other.
			c.sessions[s.ProjectName] = &s
			kept++
		}
	}
	// If anything was dropped, rewrite sessions.json now so a crash
	// before the next Start/Stop doesn't reintroduce the dead entry
	// on the following boot.
	if kept < len(stored) {
		c.persistLocked()
	}
	c.mu.Unlock()
	log.Printf("coordinator: loaded %d session(s) from %s (%d dropped as no longer running)",
		kept, c.sessionsFile, len(stored)-kept)
	return nil
}

// dockerRunningContainers returns a set of currently-running container
// names as reported by `docker ps --format {{.Names}}`. Called during
// reconciliation only.
func (c *Coordinator) dockerRunningContainers() (map[string]bool, error) {
	out, err := c.exec.Output("docker", "ps", "--format", "{{.Names}}")
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names[line] = true
		}
	}
	return names, nil
}

// persistLocked writes the current sessions map to sessionsFile using
// a temp file + atomic rename. Called with c.mu held. A write failure
// is logged, not returned — losing durability on one edge is better
// than aborting a Start/Stop the caller already committed to.
func (c *Coordinator) persistLocked() {
	if c.sessionsFile == "" {
		return
	}
	out := make([]Session, 0, len(c.sessions))
	for _, s := range c.sessions {
		out = append(out, *s)
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		log.Printf("coordinator: marshal sessions: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.sessionsFile), 0o755); err != nil {
		log.Printf("coordinator: mkdir for sessions.json: %v", err)
		return
	}
	tmp := c.sessionsFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		log.Printf("coordinator: write %s: %v", tmp, err)
		return
	}
	if err := os.Rename(tmp, c.sessionsFile); err != nil {
		log.Printf("coordinator: rename %s → %s: %v", tmp, c.sessionsFile, err)
		_ = os.Remove(tmp)
	}
}
