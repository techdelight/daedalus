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
	"github.com/techdelight/daedalus/internal/control"
	"github.com/techdelight/daedalus/internal/executor"
	"github.com/techdelight/daedalus/internal/registry"
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

	// If a *live* session already exists, report ErrAlreadyRunning so the
	// caller attaches instead of double-starting. Get reconciles liveness,
	// so a stale session whose container died out-of-band is reaped here
	// and we fall through to start a fresh one — rather than wrongly
	// claiming it's already running and stranding the caller.
	if _, ok := c.Get(name); ok {
		return nil, ErrAlreadyRunning
	}

	sockPath := cfg.RunnerSocketPath()
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o755); err != nil {
		return nil, fmt.Errorf("creating socket directory: %w", err)
	}
	_ = os.Remove(sockPath) // stale socket from a previous run blocks bind

	// Create the host dirs backing the runner's bind mounts (skill catalog,
	// .daedalus, shared Claude/Maven caches, per-project tools) so Docker
	// does not create them root-owned. Best-effort; a real problem surfaces
	// as a start/mount error with a clearer cause.
	for _, d := range core.RunnerVolumeHostDirs(cfg) {
		_ = os.MkdirAll(d, 0o755)
	}

	// Permission preflight (Sprint 43 item 2 — the top risk). The image runs
	// its `claude` user at the uid it was BUILT with (CLAUDE_UID); the host
	// dirs above are created at the uid the coordinator runs as now. If those
	// differ — an image built by another user or in CI, run here — the
	// container can't write the shared caches / tools and the session dies with
	// a cryptic "Permission denied". Say so plainly instead. Non-fatal: some
	// setups (e.g. matching group perms) may still work, and blocking a launch
	// on a heuristic would be worse than a warning.
	if buildUID, ok := core.ReadBuildUID(cfg.DataDir); ok {
		if runUID := os.Getuid(); buildUID != runUID {
			log.Printf("coordinator: WARNING image was built as uid %d but this coordinator runs as uid %d; "+
				"the container's claude user (uid %d) may be unable to write the shared caches / tools dirs "+
				"created as uid %d. If the session fails with \"Permission denied\", rebuild as the current "+
				"user: `daedalus --build`.", buildUID, runUID, buildUID, runUID)
		}
	}

	// Reap a stale container under this name before creating a new one.
	// `docker compose run --rm --detach` does NOT auto-remove the container
	// (--rm is ineffective when detached), so a prior run's container lingers
	// after it stops and its name collides with `--name` here. `docker rm`
	// without -f only removes a stopped leftover and harmlessly refuses a
	// running container — so an actively running container (a foreign tmux
	// session, or a live runner) is never killed, and that case still
	// surfaces as the usual name-conflict error. Best-effort: the common
	// "No such container" is expected and ignored.
	_ = c.exec.Run("docker", "rm", containerName)

	env := composeEnv(cfg)
	// NOTE: no `--rm`. `docker compose run --rm --detach` removes the
	// container the moment the detached run call returns, so the runner is
	// torn down before it can bind its socket (waitForSocket then times
	// out). The coordinator owns the container lifecycle itself instead:
	// Stop removes it, and the stale-container reap above clears any
	// leftover before the next Start.
	// -p pins the compose project. Without it compose names the project after the
	// directory holding the compose file — which is the VERSIONED install dir — so
	// every upgrade minted a new project and leaked its `_default` network until
	// Docker ran out of subnets and nothing could start. See core.ComposeProject.
	args := []string{"compose", "-p", core.ComposeProject, "-f", c.composeFile, "run", "--detach"}
	// The DAEDALUS_* vars must reach the CONTAINER, so they are -e flags,
	// not process env: docker-compose.yml interpolates none of them, so
	// composeEnv alone would leave the entrypoint on the classic path.
	for _, kv := range runnerContainerEnv(cfg) {
		args = append(args, "-e", kv)
	}
	// Bind mounts the runner container needs — skill catalog, .daedalus
	// progress dir, shared Claude/Maven caches (#37/#21), per-project tools
	// (#27). These were absent on the coordinator path before (Backlog #55):
	// only the legacy path called BuildExtraArgs.
	args = append(args, core.RunnerVolumeArgs(cfg)...)
	// A project directory that is a LINKED WORKTREE — every control-plane Job is
	// one — needs the repository it points at, or git inside the container is not
	// merely absent but fatally broken. See worktreeGitMountArgs.
	args = append(args, worktreeGitMountArgs(cfg)...)
	// The Guild Master additionally gets every OTHER registered project's
	// directory mounted read-only at /guild/<name> (Sprint 53) — the
	// cross-project visibility that gives it its purpose. This is a launch-time
	// snapshot of the registry: a project registered later appears on the Guild
	// Master's next launch. Normal projects get no /guild mounts.
	if core.IsGuildMaster(name) {
		args = append(args, guildMountArgs(cfg)...)
		// …and, when the control plane is up, the RESTRICTED agent socket, which
		// is what lets the Guild Master act at all (as a proposal-tier caller).
		// The env var is set only alongside a real mount so the in-container gate
		// and the mount can never disagree.
		if mount := guildControlMountArgs(cfg); len(mount) > 0 {
			args = append(args, mount...)
			args = append(args, "-e", "DAEDALUS_CONTROL_AGENT_SOCKET="+core.GuildControlSocketTarget)
		}
	}
	args = append(args, "--name", containerName, "claude")
	log.Printf("coordinator: starting runner for %q (container %s, image %s)", name, containerName, cfg.Image())
	if err := c.exec.RunWithEnv(env, "docker", args...); err != nil {
		log.Printf("coordinator: `docker compose run` for %q failed: %v", name, err)
		return nil, fmt.Errorf("docker compose run --detach: %w", err)
	}

	if err := waitForSocket(sockPath, c.socketWait, c.pollEvery); err != nil {
		// The container started but never bound the runner socket in time —
		// almost always the runner exited early (bad env, adapter crash,
		// claude erroring out). Point the operator at the container's own
		// logs, which hold the real cause; the container is left in place
		// (no --rm) precisely so it can be inspected.
		log.Printf("coordinator: runner socket for %q did not appear at %s within %s; "+
			"the container likely exited — inspect with `docker logs %s`: %v",
			name, sockPath, c.socketWait, containerName, err)
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
	log.Printf("coordinator: runner for %q ready (container %s, socket %s)", name, containerName, sockPath)
	return sess, nil
}

// Get returns the tracked session for the given project name. The
// boolean is false when no session is tracked; callers that want an
// error should compare against ErrNotFound from Stop instead.
func (c *Coordinator) Get(name string) (*Session, bool) {
	c.mu.Lock()
	s, ok := c.sessions[name]
	c.mu.Unlock()
	if !ok {
		return nil, false
	}

	// Reconcile lazily. A container that died out-of-band — a crash, a
	// manual `docker kill`, a host hiccup — leaves a stale session, because
	// full reconciliation only runs at startup (loadAndReconcile). Handing
	// that session back gives the caller a socket path that no longer
	// accepts connections, which is exactly the "dial: no such file" /
	// stale-attach failure. Verify the container is still running; if not,
	// drop the session so the caller falls back to starting a fresh one.
	running, err := c.dockerRunningContainers()
	if err != nil {
		// Can't verify (docker unavailable): prefer returning the session
		// over falsely reaping a live one.
		return s, true
	}
	if !running[s.ContainerName] {
		c.mu.Lock()
		// Re-check under lock: only reap if it's still the same session we
		// looked up, so a concurrent Start that replaced it isn't dropped.
		if cur, still := c.sessions[name]; still && cur.ContainerName == s.ContainerName {
			delete(c.sessions, name)
			c.persistLocked()
		}
		c.mu.Unlock()
		log.Printf("coordinator: reaped stale session %q — container %s is no longer running", name, s.ContainerName)
		return nil, false
	}
	return s, true
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
	// Remove the stopped container. Since Start no longer passes `--rm`
	// (it breaks the detached runner), the coordinator reaps the container
	// itself so a stopped session doesn't linger and block the next Start
	// on its name. Best-effort: a missing container is fine.
	_ = c.exec.Run("docker", "rm", sess.ContainerName)

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
	// Gate the guild-mcp server to the Guild Master only: entrypoint.sh adds the
	// guild-mcp MCP entry to the agent's config solely when this env is set, so a
	// normal project's agent never gets cross-project read tools.
	if core.IsGuildMaster(cfg.ProjectName) {
		kv = append(kv, "DAEDALUS_GUILD_MASTER=1")
	}
	return kv
}

// guildMountArgs reads the registry (the daemon has the same DataDir) and turns
// it into the Guild Master's read-only /guild/<name> mounts. It is only ever
// called for the Guild Master. A registry read failure is logged and yields no
// mounts rather than failing the launch — the Guild Master still starts, just
// without cross-project visibility this run.
func guildMountArgs(cfg *core.Config) []string {
	reg := registry.NewRegistry(cfg.RegistryPath())
	projects, err := reg.GetProjectEntries()
	if err != nil {
		log.Printf("coordinator: guild mounts: reading registry: %v (continuing with none)", err)
		return nil
	}
	return core.GuildMounts(cfg.ProjectName, projects)
}

// worktreeGitMountArgs returns the bind mounts that make git usable when the
// project directory is a linked worktree, or nil for an ordinary checkout.
//
// Every control-plane Job runs in one, and without these the container gets a
// checkout whose `.git` names a host path it cannot see — so every git command
// is fatal, which is strictly worse than having no git at all: the checkout looks
// like a repository, and an agent that opens it concludes it cannot work. That is
// not hypothetical. A Job reported exactly it, exited 0 having written nothing,
// and was rejected on the null-agent floor — a correct verdict that said nothing
// about the cause.
//
// A failure to write the pointer file is logged and the launch continues, in the
// same spirit as the Guild Master's missing socket: it degrades to the behaviour
// that shipped until now rather than refusing to run the Job at all.
func worktreeGitMountArgs(cfg *core.Config) []string {
	wt, ok := core.ReadLinkedWorktree(cfg.ProjectDir)
	if !ok {
		return nil // an ordinary checkout already has its .git in the mount
	}
	pointer := cfg.WorktreeGitFilePath()
	if err := os.MkdirAll(filepath.Dir(pointer), 0o755); err != nil {
		log.Printf("coordinator: git mounts: creating %s: %v (the container gets no git)", filepath.Dir(pointer), err)
		return nil
	}
	if err := os.WriteFile(pointer, []byte(wt.Pointer()), 0o644); err != nil {
		log.Printf("coordinator: git mounts: writing %s: %v (the container gets no git)", pointer, err)
		return nil
	}
	log.Printf("coordinator: %q is a linked worktree — mounting %s read-only at %s so git works inside",
		cfg.ProjectName, wt.CommonDir, core.ContainerGitCommon)
	return core.WorktreeGitMounts(wt, pointer)
}

// guildControlMountArgs returns the bind mount for the control plane's
// restricted agent socket, or nil with a logged explanation.
//
// The absence is worth a log line rather than silence: without it the Guild
// Master starts as a read-only overseer, the guild-control tools are simply
// missing from its agent's config, and the difference is otherwise invisible
// until someone asks the Guild Master to create a task and it says it cannot.
// The usual cause is that the control plane is not running — `daedalus
// guild-master` starts it first, but a direct coordinator call need not have.
func guildControlMountArgs(cfg *core.Config) []string {
	sock := control.AgentSocketPath(cfg.DataDir)
	mount := core.GuildControlSocketMount(cfg.ProjectName, sock)
	if len(mount) == 0 {
		log.Printf("coordinator: no control-plane agent socket at %s — the Guild Master "+
			"starts WITHOUT the guild-control tools (read-only overseer). Start the plane "+
			"with `daedalus control start` and relaunch to give it the client.", sock)
	}
	return mount
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
