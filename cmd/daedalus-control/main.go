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
	"syscall"
	"time"

	"github.com/techdelight/daedalus/internal/config"
	"github.com/techdelight/daedalus/internal/control"
	"github.com/techdelight/daedalus/internal/coordinator"
	"github.com/techdelight/daedalus/internal/executor"
	"github.com/techdelight/daedalus/internal/registry"
)

type daemonConfig struct {
	socket  string
	dataDir string
	pidFile string
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
	runner := selectRunner(filepath.Join(scriptDir, "daedalus"))

	// Verifier: the real clean-verifier container lands in Sprint 57 (M14). Until
	// then this is a stub that passes by default; DAEDALUS_CONTROL_FAKE_VERIFY=fail
	// forces a rejection. The test-integrity gate runs BEFORE the verifier
	// regardless, in the control plane.
	verifier := selectVerifier()

	// Session observer: wrap the coordinator client. When the coordinator is
	// down, HasSession errors and reconcile treats liveness as unverifiable
	// (leaves the job alone) rather than wrongly failing it.
	sessions := coordinatorSessions{client: coordinator.NewClient(coordinator.DefaultSocketPath(cfg.dataDir))}

	svc := control.NewService(store, resolver, worktrees, runner, verifier, sessions)

	// Reconcile on boot.
	if rep, err := svc.Reconcile(); err != nil {
		log.Printf("boot reconcile: %v", err)
	} else {
		log.Printf("boot reconcile: checked=%d failed-vanished=%v removed-orphans=%v skipped-unverified=%d",
			rep.CheckedActive, rep.FailedVanished, rep.RemovedOrphans, rep.SkippedUnverified)
	}

	server := control.NewServer(svc)
	_ = os.Remove(cfg.socket)
	l, err := net.Listen("unix", cfg.socket)
	if err != nil {
		fatalf("listen on %s: %v", cfg.socket, err)
	}
	httpSrv := &http.Server{Handler: server.Handler(), ReadTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}

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
				} else if len(rep.FailedVanished) > 0 || len(rep.RemovedOrphans) > 0 {
					log.Printf("reconcile tick: failed-vanished=%v removed-orphans=%v", rep.FailedVanished, rep.RemovedOrphans)
				}
			}
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("listening on %s (data-dir=%s)", cfg.socket, cfg.dataDir)
		errCh <- httpSrv.Serve(l)
	}()

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
	_ = os.Remove(cfg.socket)
	log.Printf("stopped")
}

// selectRunner returns the real coordinator/Docker runner, or a Docker-free stub
// when DAEDALUS_CONTROL_FAKE_RUNNER is set (test/dev only).
func selectRunner(daedalusBin string) control.AgentRunner {
	if v := os.Getenv("DAEDALUS_CONTROL_FAKE_RUNNER"); v != "" {
		res := control.ExecSuccess
		if v == "fail" {
			res = control.ExecFailed
		}
		// DAEDALUS_CONTROL_FAKE_RUNNER_MARKER lets a smoke choose the file the stub
		// writes (e.g. a *_test.go name to exercise the integrity gate).
		marker := os.Getenv("DAEDALUS_CONTROL_FAKE_RUNNER_MARKER")
		log.Printf("WARNING using fake runner (DAEDALUS_CONTROL_FAKE_RUNNER=%s) — no real agent runs", v)
		return control.StubRunner{Result: res, WriteFile: res == control.ExecSuccess, MarkerName: marker}
	}
	return control.CoordinatorRunner{Exec: &executor.RealExecutor{}, BinPath: daedalusBin}
}

// selectVerifier returns the stub verifier (the real clean-verifier container is
// Sprint 57). It passes by default; DAEDALUS_CONTROL_FAKE_VERIFY=fail forces a
// rejection so the rejected/retry path is demonstrable without Docker.
func selectVerifier() control.VerifyRunner {
	pass := os.Getenv("DAEDALUS_CONTROL_FAKE_VERIFY") != "fail"
	if !pass {
		log.Printf("WARNING stub verifier set to FAIL (DAEDALUS_CONTROL_FAKE_VERIFY=fail)")
	}
	return control.StubVerifyRunner{Pass: pass}
}

// coordinatorSessions adapts the coordinator client to control.SessionObserver.
// Kept in the daemon (not internal/control) so the control package stays free of
// a coordinator dependency.
type coordinatorSessions struct{ client *coordinator.Client }

func (c coordinatorSessions) HasSession(project string) (bool, error) {
	_, err := c.client.Get(project)
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
	flag.StringVar(&cfg.socket, "socket", "", "Unix socket path (default: <DataDir>/.daedalus/control.sock)")
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
