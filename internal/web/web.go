// Copyright (C) 2026 Techdelight BV

package web

import (
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/activity"
	"github.com/techdelight/daedalus/internal/agentstate"
	"github.com/techdelight/daedalus/internal/auth"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/docker"
	"github.com/techdelight/daedalus/internal/executor"
	"github.com/techdelight/daedalus/internal/foreman"
	"github.com/techdelight/daedalus/internal/platform"
	"github.com/techdelight/daedalus/internal/registry"
)

// WebServer holds the dependencies shared by the topic handlers
// (projects.go, dashboard.go, roadmap.go, foreman.go, programmes.go,
// terminal.go). Each handler file owns its routes and JSON shapes.
type WebServer struct {
	registry         *registry.Registry
	docker           *docker.Docker
	executor         executor.Executor
	cfg              *core.Config
	observer         agentstate.Observer
	activityResolver *activity.Resolver
	foreman          *foreman.Foreman
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
	}

	mux := http.NewServeMux()
	ws.registerRoutes(mux)

	// Serve static files (embedded in binary)
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return fmt.Errorf("setting up static files: %w", err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Root serves index.html with version injected into the title
	version := core.ReadVersion()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		data, err := staticFiles.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, "index.html not found", http.StatusInternalServerError)
			return
		}
		html := strings.Replace(string(data), ">Daedalus<", ">Daedalus ["+version+"]<", 1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, err := w.Write([]byte(html)); err != nil {
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

// registerRoutes wires every API route to its handler. Routes are listed
// here rather than spread across topic files so the URL surface area is
// visible at a glance.
func (ws *WebServer) registerRoutes(mux *http.ServeMux) {
	// projects.go
	mux.HandleFunc("GET /api/projects", ws.handleListProjects)
	mux.HandleFunc("POST /api/projects/{name}/start", ws.handleStartProject)
	mux.HandleFunc("POST /api/projects/{name}/stop", ws.handleStopProject)
	mux.HandleFunc("POST /api/projects/{name}/rename", ws.handleRenameProject)
	mux.HandleFunc("POST /api/projects/{name}/enter", ws.handleSendEnter)

	// dashboard.go
	mux.HandleFunc("GET /api/projects/{name}/dashboard", ws.handleDashboard)
	mux.HandleFunc("GET /api/projects/{name}/state", ws.handleAgentState)
	mux.HandleFunc("GET /api/guild", ws.handleGuild)

	// roadmap.go
	mux.HandleFunc("GET /api/projects/{name}/roadmap", ws.handleRoadmap)
	mux.HandleFunc("GET /api/projects/{name}/sprints", ws.handleRoadmap)
	mux.HandleFunc("GET /api/projects/{name}/backlog", ws.handleBacklog)
	mux.HandleFunc("GET /api/projects/{name}/strategic-roadmap", ws.handleStrategicRoadmap)

	// terminal.go
	mux.HandleFunc("GET /api/projects/{name}/terminal", ws.handleTerminal)

	// foreman.go
	mux.HandleFunc("GET /api/foreman/status", ws.handleForemanStatus)
	mux.HandleFunc("POST /api/foreman/start", ws.handleForemanStart)
	mux.HandleFunc("POST /api/foreman/stop", ws.handleForemanStop)

	// programmes.go
	mux.HandleFunc("GET /api/programmes", ws.handleListProgrammes)
	mux.HandleFunc("POST /api/programmes", ws.handleCreateProgramme)
	mux.HandleFunc("GET /api/programmes/{name}", ws.handleGetProgramme)
	mux.HandleFunc("PUT /api/programmes/{name}", ws.handleUpdateProgramme)
	mux.HandleFunc("DELETE /api/programmes/{name}", ws.handleDeleteProgramme)
}
