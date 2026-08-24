// Copyright (C) 2026 Techdelight BV

// Command daedalus-control is the host-side control-plane daemon (Milestone 13,
// Sprint 55). It owns the SQLite control store and exposes an HTTP-over-Unix-
// socket API for the `daedalus task` CLI (and, later, the Guild Master client) to
// create, list, inspect, dispatch, and cancel Tasks. Being the single owner of
// the DB avoids a second writer.
//
// Usage:
//
//	daedalus-control [--socket <path>] [--data-dir <dir>] [--pid-file <path>]
//
// On start and on a periodic tick it reconciles desired (DB) state against
// observed reality (worktrees + coordinator sessions), repairing post-crash
// divergence with idempotent, deterministically-named side-effects.
//
// Signal handling: SIGINT/SIGTERM trigger a graceful HTTP shutdown, then unlink
// the socket and pidfile.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/config"
	"github.com/techdelight/daedalus/internal/control"
	"github.com/techdelight/daedalus/internal/coordinator"
	"github.com/techdelight/daedalus/internal/executor"
	"github.com/techdelight/daedalus/internal/programme"
	"github.com/techdelight/daedalus/internal/registry"
)

type daemonConfig struct {
	socket      string
	agentSocket string
	dataDir     string
	pidFile     string
}

// reconcileEvery is the periodic reconcile cadence.
const reconcileEvery = 30 * time.Second

func main() {
	log.SetPrefix("daedalus-control: ")
	log.SetFlags(log.LstdFlags | log.LUTC)

	cfg := parseFlags()

	scriptDir, err := resolveScriptDir()
	if err != nil {
		fatalf("resolve script dir: %v", err)
	}
	appCfg, err := config.LoadAppConfig(scriptDir)
	if err != nil {
		fatalf("load app config from %s: %v", scriptDir, err)
	}
	if cfg.dataDir == "" {
		if appCfg.DataDir != nil && *appCfg.DataDir != "" {
			cfg.dataDir = *appCfg.DataDir
		} else {
			cfg.dataDir = filepath.Join(scriptDir, ".cache")
		}
	}
	if cfg.socket == "" {
		cfg.socket = control.DefaultSocketPath(cfg.dataDir)
	}
	if cfg.agentSocket == "" {
		cfg.agentSocket = control.AgentSocketPath(cfg.dataDir)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.socket), 0o755); err != nil {
		fatalf("create socket directory: %v", err)
	}
	if cfg.pidFile != "" {
		if err := os.MkdirAll(filepath.Dir(cfg.pidFile), 0o755); err != nil {
			fatalf("create pid directory: %v", err)
		}
		if err := os.WriteFile(cfg.pidFile, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644); err != nil {
			fatalf("write pidfile: %v", err)
		}
		defer os.Remove(cfg.pidFile)
	}

	// Store (single owner of control.db).
	store, err := control.Open(filepath.Join(cfg.dataDir, "control.db"))
	if err != nil {
		fatalf("open control db: %v", err)
	}
	defer store.Close()

	// Adopt programmes that were written before the plane owned them (M20).
	//
	// It runs on EVERY start and is idempotent by name, which is what makes that
	// safe: a definition already adopted is skipped, so there is no migration
	// flag to get wrong and no one-shot step to forget on a fresh install. The
	// file store keeps its files — nothing is deleted — but the plane is now the
	// single answer to "what programmes exist", so an edit to a file after this
	// point is an edit to a copy nobody reads.
	if defs, err := programme.New(filepath.Join(cfg.dataDir, "programmes")).List(); err != nil {
		log.Printf("control: reading legacy programmes: %v (continuing with none)", err)
	} else if n, err := store.ImportProgrammes(defs); err != nil {
		log.Printf("control: importing legacy programmes: %v", err)
	} else if n > 0 {
		log.Printf("control: adopted %d programme definition(s) from %s", n,
			filepath.Join(cfg.dataDir, "programmes"))
	}

	// Project resolution through the trusted registry.
	reg := registry.NewRegistry(filepath.Join(cfg.dataDir, "projects.json"))
	if err := reg.Init(); err != nil {
		log.Printf("registry init: %v (continuing)", err)
	}
	resolver := control.RegistryResolver{Reg: reg}
	worktrees := control.NewWorktreeManager(cfg.dataDir)

	// Runner: real coordinator/Docker adapter by default; a Docker-free stub when
	// DAEDALUS_CONTROL_FAKE_RUNNER is set (=fail forces a failed outcome) so the
	// end-to-end flow is exercisable without a container runtime.
	runner := selectRunner(filepath.Join(scriptDir, "daedalus"), cfg.dataDir)

	// Verifier: the REAL clean-verifier container (M14, Sprint 57) by default — it
	// checks out the artifact's head_sha into a fresh clean worktree and runs the
	// frozen policy.checks in the project's pinned image. DAEDALUS_CONTROL_FAKE_VERIFY
	// selects the Docker-free stub (=fail forces a rejection) so host tests and the
	// M13/M14 verify scripts stay Docker-free. The test-integrity gate + null-agent
	// floor run BEFORE the verifier regardless, in the control plane.
	verifier := selectVerifier()

	// Session observer: wrap the coordinator client. When the coordinator is
	// down, HasSession errors and reconcile treats liveness as unverifiable
	// (leaves the job alone) rather than wrongly failing it.
	sessions := coordinatorSessions{client: coordinator.NewClient(coordinator.DefaultSocketPath(cfg.dataDir))}

	svc := control.NewService(store, resolver, worktrees, runner, verifier, sessions)

	// Per-job logs (#77) live under the data dir, so the service needs to know it.
	// Without this every Job's output reaches only the shared control.log, keyed by
	// nothing, which is the state that made a failed Job undiagnosable.
	svc.SetDataDir(cfg.dataDir)

	// Governance budgets (M15): per-project ceilings from a HOST-SIDE policy file
	// under the data dir — never from a project checkout, so an agent cannot raise
	// the envelope that bounds its own work. Re-read per lookup, so an operator's
	// edit applies to the next task without a daemon restart; a missing or
	// malformed file degrades to the built-in defaults.
	budgetPath := control.DefaultBudgetPolicyPath(cfg.dataDir)
	svc.SetPolicySource(control.NewFileBudgetPolicy(budgetPath))
	log.Printf("budget policy: %s (enforced: %v; policy-only, not enforced: %v)",
		budgetPath, control.EnforcedAxes(), control.PolicyOnlyAxes())

	// Scheduler limits come from the same host-side governance file. Absent, the
	// defaults preserve the pre-Sprint-61 behaviour of one Job per project, so
	// lifting the invariant does not silently change an existing installation.
	limits := control.DefaultSchedulerLimits()
	if policy, err := control.LoadBudgetPolicy(budgetPath); err == nil {
		limits = policy.SchedulerLimitsFor()
	} else {
		log.Printf("scheduler limits: %s unreadable (%v) — using defaults", budgetPath, err)
	}
	svc.SetSchedulerLimits(limits)
	log.Printf("scheduler: global=%d per-project=%d", limits.Global, limits.PerProject)

	// Image-digest pin: capture the project image's sha256 digest so the verifier
	// runs against the exact environment the artifact was authored against. Only
	// wired when the real verifier is active — under DAEDALUS_CONTROL_FAKE_VERIFY
	// there is no Docker, so digest capture is skipped to keep create/verify
	// Docker-free.
	if os.Getenv("DAEDALUS_CONTROL_FAKE_VERIFY") == "" {
		svc.SetImageDigester(dockerImageDigester{exec: &executor.RealExecutor{}, reg: reg, imagePrefix: defaultImagePrefix})
	}

	// Independent reviewer (M20, Sprint 67): a REAL one now — a separate agent
	// that reads the artifact's diff against what the Task promised and reports.
	// DAEDALUS_CONTROL_FAKE_REVIEW still selects the Docker-free stub so the
	// no-Docker smoke can exercise the recording path.
	//
	// Review stays OPT-IN by being an explicit operation (`daedalus task review`)
	// rather than by being absent: since M20 a reviewer gates nothing, so wiring
	// one costs a project nothing until somebody asks for a reading.
	if v := os.Getenv("DAEDALUS_CONTROL_FAKE_REVIEW"); v != "" {
		pass := v != "fail"
		log.Printf("WARNING using stub reviewer (DAEDALUS_CONTROL_FAKE_REVIEW=%s) — no real review happens", v)
		svc.SetReviewRunner(control.StubReviewRunner{Pass: pass})
	} else {
		svc.SetReviewRunner(control.AgentReviewer{
			Exec:    &executor.RealExecutor{},
			BinPath: filepath.Join(scriptDir, "daedalus"),
			DataDir: cfg.dataDir,
		})
	}

	// Reconcile on boot.
	if rep, err := svc.Reconcile(); err != nil {
		log.Printf("boot reconcile: %v", err)
	} else {
		log.Printf("boot reconcile: checked=%d failed-vanished=%v removed-orphans=%v skipped-unverified=%d empty-candidates=%v",
			rep.CheckedActive, rep.FailedVanished, rep.RemovedOrphans, rep.SkippedUnverified,
			rep.RecoveredEmptyCandidates)
	}

	// TWO LISTENERS, one per caller class. Which socket a connection arrives on
	// IS the caller identity (control.Caller): the human CLI/Web/TUI use
	// control.sock, and an agent client (guild-control-mcp) is given only
	// control-agent.sock inside its container. Peer credentials cannot make this
	// distinction — same uid — so the split is the mechanism.
	server := control.NewServerForCaller(svc, control.Human())
	_ = os.Remove(cfg.socket)
	l, err := net.Listen("unix", cfg.socket)
	if err != nil {
		fatalf("listen on %s: %v", cfg.socket, err)
	}
	httpSrv := &http.Server{Handler: server.Handler(), ReadTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}

	agentServer := control.NewServerForCaller(svc, control.Agent())
	_ = os.Remove(cfg.agentSocket)
	agentListener, err := net.Listen("unix", cfg.agentSocket)
	if err != nil {
		fatalf("listen on %s: %v", cfg.agentSocket, err)
	}
	// The agent socket is mode 0660: the mount namespace is the real boundary
	// (only the Guild Master's container gets this file), but there is no reason
	// to leave it world-writable on the host either.
	if err := os.Chmod(cfg.agentSocket, 0o660); err != nil {
		log.Printf("chmod agent socket: %v (continuing)", err)
	}
	agentSrv := &http.Server{Handler: agentServer.Handler(), ReadTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	defer os.Remove(cfg.agentSocket)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Periodic reconcile ticker.
	go func() {
		t := time.NewTicker(reconcileEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if rep, err := svc.Reconcile(); err != nil {
					log.Printf("reconcile tick: %v", err)
				} else if len(rep.FailedVanished) > 0 || len(rep.RemovedOrphans) > 0 ||
					len(rep.RecoveredEmptyCandidates) > 0 {
					log.Printf("reconcile tick: failed-vanished=%v removed-orphans=%v empty-candidates=%v",
						rep.FailedVanished, rep.RemovedOrphans, rep.RecoveredEmptyCandidates)
				}
			}
		}
	}()

	errCh := make(chan error, 2)
	go func() {
		log.Printf("listening on %s (human) and %s (agent) (data-dir=%s)", cfg.socket, cfg.agentSocket, cfg.dataDir)
		errCh <- httpSrv.Serve(l)
	}()
	go func() { errCh <- agentSrv.Serve(agentListener) }()

	select {
	case <-ctx.Done():
		log.Printf("received signal, shutting down")
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("serve error: %v", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	if err := agentSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("agent shutdown: %v", err)
	}
	_ = os.Remove(cfg.socket)
	_ = os.Remove(cfg.agentSocket)
	log.Printf("stopped")
}

// selectRunner returns the real coordinator/Docker runner, or a Docker-free stub
// when DAEDALUS_CONTROL_FAKE_RUNNER is set (test/dev only).
func selectRunner(daedalusBin, dataDir string) control.AgentRunner {
	if v := os.Getenv("DAEDALUS_CONTROL_FAKE_RUNNER"); v != "" {
		res := control.ExecSuccess
		if v == "fail" {
			res = control.ExecFailed
		}
		// `empty` = a successful run that makes NO change (head == base) so the
		// null-agent floor is demonstrable; otherwise a success writes a marker.
		write := res == control.ExecSuccess && v != "empty"
		// DAEDALUS_CONTROL_FAKE_RUNNER_MARKER lets a smoke choose the file the stub
		// writes (e.g. a *_test.go name to exercise the integrity gate).
		marker := os.Getenv("DAEDALUS_CONTROL_FAKE_RUNNER_MARKER")
		log.Printf("WARNING using fake runner (DAEDALUS_CONTROL_FAKE_RUNNER=%s) — no real agent runs", v)
		return control.StubRunner{Result: res, WriteFile: write, MarkerName: marker}
	}
	return control.CoordinatorRunner{Exec: &executor.RealExecutor{}, BinPath: daedalusBin, DataDir: dataDir}
}

// defaultImagePrefix mirrors config.ParseArgs' default; the digester needs it to
// name a project's image without a full per-project Config.
const defaultImagePrefix = "techdelight/claude-runner"

// selectVerifier returns the real clean-verifier container by default; a
// Docker-free stub when DAEDALUS_CONTROL_FAKE_VERIFY is set (=fail forces a
// rejection) so host tests and the M13/M14 verify scripts stay Docker-free.
func selectVerifier() control.VerifyRunner {
	if v := os.Getenv("DAEDALUS_CONTROL_FAKE_VERIFY"); v != "" {
		pass := v != "fail"
		log.Printf("WARNING using stub verifier (DAEDALUS_CONTROL_FAKE_VERIFY=%s) — no clean-container checks run", v)
		return control.StubVerifyRunner{Pass: pass}
	}
	return control.CleanVerifier{Exec: &executor.RealExecutor{}, Policy: control.DefaultVerifierEnvPolicy()}
}

// dockerImageDigester resolves a project's built image to an immutable sha256
// digest via `docker image inspect`. Best-effort: a missing image or docker
// error yields "" (not fatal) — the verifier then reports "no pinned image".
type dockerImageDigester struct {
	exec        executor.Executor
	reg         *registry.Registry
	imagePrefix string
}

// Digest implements control.ImageDigester.
func (d dockerImageDigester) Digest(project string) (string, error) {
	entry, ok, err := d.reg.GetProject(project)
	if err != nil || !ok {
		return "", nil // unknown project → no digest, not fatal
	}
	cfg := &core.Config{ProjectName: project, ImagePrefix: d.imagePrefix, Target: entry.Target}
	image := cfg.Image()
	// .Id is the image's local content digest (sha256:...), stable for a built
	// image even without a registry RepoDigest.
	out, err := d.exec.Output("docker", "image", "inspect", "--format", "{{.Id}}", image)
	if err != nil {
		return "", nil // image not built yet → capture later
	}
	return strings.TrimSpace(out), nil
}

// coordinatorSessions adapts the coordinator client to control.SessionObserver.
// Kept in the daemon (not internal/control) so the control package stays free of
// a coordinator dependency.
type coordinatorSessions struct{ client *coordinator.Client }

func (c coordinatorSessions) HasSession(project string) (bool, error) {
	return c.sessionExists(project)
}

// HasSessionForJob implements control.JobSessionObserver — the question reconcile
// actually needs answered once several Jobs can share a project.
//
// No coordinator change was needed. CoordinatorRunner launches each Job as
// `daedalus <JobProjectName(jobID)> …`, and the coordinator keys sessions by that
// name, so the per-Job session has existed under exactly this key since M13; the
// control plane was simply asking about the project instead.
func (c coordinatorSessions) HasSessionForJob(jobID string) (bool, error) {
	return c.sessionExists(control.JobProjectName(jobID))
}

func (c coordinatorSessions) sessionExists(name string) (bool, error) {
	_, err := c.client.Get(name)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, coordinator.ErrNotFound) {
		return false, nil
	}
	return false, err // transport error → caller treats as unverifiable
}

func parseFlags() daemonConfig {
	var cfg daemonConfig
	flag.StringVar(&cfg.socket, "socket", "", "human Unix socket path (default: <DataDir>/.daedalus/control.sock)")
	flag.StringVar(&cfg.agentSocket, "agent-socket", "", "restricted agent Unix socket path (default: <DataDir>/.daedalus/control-agent.sock)")
	flag.StringVar(&cfg.dataDir, "data-dir", "", "Base data directory (default: from config.json, then <ScriptDir>/.cache)")
	flag.StringVar(&cfg.pidFile, "pid-file", "", "Write PID to this file on startup, remove on exit")
	flag.Parse()
	return cfg
}

func resolveScriptDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Dir(exe))
}

func fatalf(format string, args ...any) {
	log.Printf("fatal: "+format, args...)
	os.Exit(1)
}
