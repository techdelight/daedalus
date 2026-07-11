// Copyright (C) 2026 Techdelight BV

// Command daedalus-coordinator is the host-side daemon that owns the
// lifecycle of runner-attached project containers and exposes a small
// HTTP-over-Unix-socket API for UI processes (CLI, TUI, Web) to
// discover and manage sessions.
//
// Usage:
//
//	daedalus-coordinator [--socket <path>] [--compose <file>]
//	                     [--data-dir <dir>] [--pid-file <path>]
//
// When run under systemd or launchd, no flags are typically required —
// defaults are read from the same config.json the CLI uses. The
// `daedalus coordinator start` subcommand spawns this binary detached
// and passes --pid-file so it can stop/status it later.
//
// Signal handling: SIGINT and SIGTERM trigger a graceful HTTP shutdown,
// then unlink the socket and pidfile before exiting.
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
	"github.com/techdelight/daedalus/internal/coordinator"
	"github.com/techdelight/daedalus/internal/executor"
)

type daemonConfig struct {
	socket  string
	compose string
	dataDir string
	pidFile string
}

func main() {
	log.SetPrefix("daedalus-coordinator: ")
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
		cfg.socket = coordinator.DefaultSocketPath(cfg.dataDir)
	}
	if cfg.compose == "" {
		cfg.compose = filepath.Join(scriptDir, "docker-compose.yml")
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

	coord := coordinator.New(coordinator.Options{
		Executor:    &executor.RealExecutor{},
		ComposeFile: cfg.compose,
	})
	server := coordinator.NewServer(coord)

	// Clean any stale socket file — a prior crashed instance would
	// leave one and block bind. Matches Coordinator's own runner-socket
	// hygiene.
	_ = os.Remove(cfg.socket)
	l, err := net.Listen("unix", cfg.socket)
	if err != nil {
		fatalf("listen on %s: %v", cfg.socket, err)
	}

	httpSrv := &http.Server{
		Handler:      server.Handler(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("listening on %s (compose=%s)", cfg.socket, cfg.compose)
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

func parseFlags() daemonConfig {
	var cfg daemonConfig
	flag.StringVar(&cfg.socket, "socket", "", "Unix socket path (default: <DataDir>/.daedalus/coordinator.sock)")
	flag.StringVar(&cfg.compose, "compose", "", "Path to docker-compose.yml (default: <ScriptDir>/docker-compose.yml)")
	flag.StringVar(&cfg.dataDir, "data-dir", "", "Base data directory (default: from config.json, then <ScriptDir>/.cache)")
	flag.StringVar(&cfg.pidFile, "pid-file", "", "Write PID to this file on startup, remove on exit")
	flag.Parse()
	return cfg
}

// resolveScriptDir returns the directory containing the daemon binary,
// following symlinks (so a symlinked `daedalus-coordinator` still
// resolves to the real install PREFIX). Matches the trick
// internal/config uses so both binaries agree on their install layout.
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
