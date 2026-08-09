// Copyright (C) 2026 Techdelight BV

package web

import (
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/activity"
	"github.com/techdelight/daedalus/internal/agentstate"
	"github.com/techdelight/daedalus/internal/auth"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/control"
	"github.com/techdelight/daedalus/internal/docker"
	"github.com/techdelight/daedalus/internal/executor"
	"github.com/techdelight/daedalus/internal/platform"
	"github.com/techdelight/daedalus/internal/registry"
)

// dialControlPlane returns a control-plane client when the daemon is already
// listening on its socket, and nil otherwise. It deliberately does not use
// control.EnsureRunning: the CLI may spawn the daemon on demand, a web page must
// not.
func dialControlPlane(cfg *core.Config) control.TaskAPI {
	sock := control.DefaultSocketPath(cfg.DataDir)
	conn, err := net.DialTimeout("unix", sock, 300*time.Millisecond)
	if err != nil {
		return nil
	}
	_ = conn.Close()
	return control.NewClient(sock)
}

// renderIndexHTML injects the version into the served index.html title. Kept
// as a pure function so the substitution contract is unit-testable without
// booting the server.
func renderIndexHTML(raw []byte, version string) string {
	return strings.Replace(string(raw), ">Daedalus<", ">Daedalus ["+version+"]<", 1)
}

// WebServer holds the dependencies shared by the topic handlers
// (projects.go, dashboard.go, roadmap.go, programmes.go, terminal.go).
// Each handler file owns its routes and JSON shapes.
type WebServer struct {
	registry         *registry.Registry
	docker           *docker.Docker
	executor         executor.Executor
	cfg              *core.Config
	observer         agentstate.Observer
	activityResolver *activity.Resolver
	// control is a client of the control plane, or nil when it is not running.
	// The dashboard never spawns the daemon (see approvals.go).
	control control.TaskAPI
}

// NewWebServerForTest creates a WebServer with injected dependencies.
// Intended for integration tests that need to exercise handlers end-to-end.
func NewWebServerForTest(reg *registry.Registry, d *docker.Docker, exec executor.Executor, cfg *core.Config) *WebServer {
	observer := agentstate.NewContainerObserver(exec)
	detectors := activity.NewDetectorRegistry()
	detectors.Register("claude", activity.NewClaudeCodeDetector())
	return &WebServer{
		registry:         reg,
		docker:           d,
		executor:         exec,
		cfg:              cfg,
		observer:         observer,
		activityResolver: activity.NewResolver(observer, detectors),
	}
}

// Run starts the web UI HTTP server.
func Run(cfg *core.Config) error {
	exec := &executor.RealExecutor{}
	reg := registry.NewRegistry(cfg.RegistryPath())
	if err := reg.Init(); err != nil {
		return fmt.Errorf("initializing registry: %w", err)
	}
	// Ensure the built-in Guild Master is present at server startup so the guild
	// view and project list always include it. Best-effort: a failure is logged
	// but must not stop the server from booting.
	if err := reg.EnsureGuildMaster(cfg.GuildMasterDir()); err != nil {
		log.Printf("ensure guild master: %v", err)
	}
	docker := docker.NewDocker(exec, filepath.Join(cfg.ScriptDir, "docker-compose.yml"))

	observer := agentstate.NewContainerObserver(exec)
	detectors := activity.NewDetectorRegistry()
	detectors.Register("claude", activity.NewClaudeCodeDetector())
	actResolver := activity.NewResolver(observer, detectors)

	ws := &WebServer{
		registry:         reg,
		docker:           docker,
		executor:         exec,
		cfg:              cfg,
		observer:         observer,
		activityResolver: actResolver,
		// Attach to the control plane only if it is ALREADY listening. Opening a
		// dashboard must not spawn a daemon as a side effect.
		control: dialControlPlane(cfg),
	}

	mux := http.NewServeMux()
	ws.RegisterRoutes(mux)

	// Serve static files (embedded in binary)
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return fmt.Errorf("setting up static files: %w", err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Root serves index.html with the version injected into the title.
	version := core.ReadVersion()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		data, err := staticFiles.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, "index.html not found", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, err := w.Write([]byte(renderIndexHTML(data, version))); err != nil {
			log.Printf("write index.html: %v", err)
		}
	})

	// Authentication
	var handler http.Handler = mux
	if cfg.Auth {
		token := cfg.AuthToken
		if token == "" {
			var err error
			token, err = auth.EnsureToken(cfg.ScriptDir)
			if err != nil {
				return fmt.Errorf("setting up authentication: %w", err)
			}
		}
		expiry := cfg.AuthExpiry
		if expiry == 0 {
			expiry = 24
		}
		mux.HandleFunc("/login", auth.LoginHandler(token, expiry))
		handler = auth.Middleware(token, expiry, mux)
		fmt.Printf("Authentication enabled (session expiry: %dh)\n", expiry)
		fmt.Printf("Access token: %s\n", color.Bold(token))
	}

	if cfg.WSL2Detected {
		fmt.Printf("%s binding to 0.0.0.0 instead of 127.0.0.1\n", color.Yellow("WSL2 detected:"))
		if ip := platform.WSL2IPAddress(); ip != "" {
			fmt.Printf("Open in Windows browser: http://%s:%s\n", ip, strings.Split(cfg.WebAddr, ":")[1])
		}
	}
	fmt.Printf("Starting web UI at http://%s\n", cfg.WebAddr)
	return http.ListenAndServe(cfg.WebAddr, handler)
}

// RegisterRoutes wires every API route to its handler. Routes are listed
// here rather than spread across topic files so the URL surface area is
// visible at a glance. Exported so cross-package tests can mount the full
// route table without redeclaring it.
func (ws *WebServer) RegisterRoutes(mux *http.ServeMux) {
	// projects.go
	mux.HandleFunc("GET /api/projects", ws.handleListProjects)
	mux.HandleFunc("POST /api/projects/{name}/stop", ws.handleStopProject)
	mux.HandleFunc("POST /api/projects/{name}/rename", ws.handleRenameProject)

	// dashboard.go
	mux.HandleFunc("GET /api/projects/{name}/dashboard", ws.handleDashboard)
	mux.HandleFunc("GET /api/projects/{name}/state", ws.handleAgentState)
	mux.HandleFunc("GET /api/guild", ws.handleGuild)

	// Control plane: the pending-approvals surface (read + the two human decisions).
	mux.HandleFunc("GET /api/approvals", ws.handleApprovals)
	mux.HandleFunc("GET /api/plane-status", ws.handlePlaneStatus)
	// The cross-project programme board (M17): the same control client, a
	// projection of the same state — no board store to fall out of step.
	mux.HandleFunc("GET /api/board", ws.handleBoard)
	mux.HandleFunc("POST /api/approvals/{id}/approve", ws.handleApproveTask)
	mux.HandleFunc("POST /api/approvals/{id}/reject", ws.handleRejectTask)

	// roadmap.go
	mux.HandleFunc("GET /api/projects/{name}/roadmap", ws.handleRoadmap)
	mux.HandleFunc("GET /api/projects/{name}/sprints", ws.handleRoadmap)
	mux.HandleFunc("GET /api/projects/{name}/backlog", ws.handleBacklog)
	mux.HandleFunc("GET /api/projects/{name}/strategic-roadmap", ws.handleStrategicRoadmap)

	// docs.go
	mux.HandleFunc("GET /api/projects/{name}/docs", ws.handleDocs)
	mux.HandleFunc("GET /api/projects/{name}/vision", ws.handleVision)

	// overview.go — /overview is the project-journey dashboard's single fetch;
	// /milestones serves the arc alone for any caller that wants just it.
	mux.HandleFunc("GET /api/projects/{name}/overview", ws.handleOverview)
	mux.HandleFunc("GET /api/projects/{name}/milestones", ws.handleMilestones)
	mux.HandleFunc("GET /api/projects/{name}/milestone-sprints", ws.handleMilestoneSprints)

	// terminal.go
	mux.HandleFunc("GET /api/projects/{name}/terminal", ws.handleTerminal)

	// programmes.go
	mux.HandleFunc("GET /api/programmes", ws.handleListProgrammes)
	mux.HandleFunc("POST /api/programmes", ws.handleCreateProgramme)
	mux.HandleFunc("GET /api/programmes/{name}", ws.handleGetProgramme)
	mux.HandleFunc("PUT /api/programmes/{name}", ws.handleUpdateProgramme)
	mux.HandleFunc("DELETE /api/programmes/{name}", ws.handleDeleteProgramme)
}
