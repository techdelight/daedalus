// Copyright (C) 2026 Techdelight BV

package web

import (
	"fmt"
	"html"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
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

// renderIndexHTML injects the version into the served index.html title AND onto
// every local asset URL. Kept as a pure function so the substitution contract is
// unit-testable without booting the server.
//
// The asset stamp is not cosmetic. `//go:embed static/*` bakes the CSS and JS
// into the binary, and an embed.FS reports a ZERO modtime — so http.FileServer
// sends neither Last-Modified nor ETag, the browser has no validator to
// revalidate against, and it falls back to heuristic caching. An upgraded binary
// then serves new markup against a cached script, which is indistinguishable
// from "the fix did nothing" — and cost exactly one round of that. Stamping
// ?v=<version> makes an upgrade a different URL.
//
// Only `/static/…` is stamped: a CDN URL carries its version in its own path and
// must be left alone.
func renderIndexHTML(raw []byte, version string) string {
	out := strings.Replace(string(raw), ">Daedalus<", ">Daedalus ["+version+"]<", 1)
	// The page carries the build that served it, so a surface can say which code
	// it is. `version` alone cannot: it is release granularity, and this project
	// routinely lands dozens of commits under one unreleased number — which is how
	// an operator came to cancel a Task over a button that had not been written
	// yet, while correctly believing they were "on the latest version".
	build := core.BuildID()
	out = strings.Replace(out, "</head>",
		`<meta name="daedalus-build" content="`+html.EscapeString(build)+`"></head>`, 1)
	// Stamping with the BUILD and not the version, for the same reason: a rebuilt
	// binary at 0.54.0 served byte-identical URLs, so a browser kept a cached
	// script across exactly the change it was meant to pick up.
	stamp := "?v=" + url.QueryEscape(build)
	for _, attr := range []string{`href="`, `src="`} {
		out = stampAssets(out, attr+"/static/", stamp)
	}
	return out
}

// stampAssets appends stamp to the end of every quoted URL that begins with
// prefix. Written as a scan rather than a regexp so it cannot match across a
// quote and rewrite something that is not a URL.
func stampAssets(html, prefix, stamp string) string {
	var b strings.Builder
	rest := html
	for {
		i := strings.Index(rest, prefix)
		if i < 0 {
			b.WriteString(rest)
			return b.String()
		}
		b.WriteString(rest[:i+len(prefix)])
		rest = rest[i+len(prefix):]
		j := strings.IndexByte(rest, '"')
		if j < 0 { // malformed markup: leave the remainder exactly as it was
			b.WriteString(rest)
			return b.String()
		}
		path := rest[:j]
		b.WriteString(path)
		// An href that already carries a query keeps it; nothing here does today,
		// and appending a second `?` would break the one that did.
		if !strings.Contains(path, "?") {
			b.WriteString(stamp)
		}
		b.WriteString(`"`)
		rest = rest[j+1:]
	}
}

// WebServer holds the dependencies shared by the topic handlers
// (projects.go, dashboard.go, roadmap.go, control.go, terminal.go).
// Each handler file owns its routes and JSON shapes.
type WebServer struct {
	registry         *registry.Registry
	docker           *docker.Docker
	executor         executor.Executor
	cfg              *core.Config
	observer         agentstate.Observer
	activityResolver *activity.Resolver
	// control is a client of the control plane, or nil when it is not running.
	// The dashboard never spawns the daemon (see control.go).
	control control.TaskAPI
	// controlDial re-establishes that client when it is nil, and is what keeps a
	// web server started BEFORE the plane from being deaf to it for its whole
	// life. Nil in tests that build a WebServer literal, which then keep the old
	// behaviour of never dialling at all.
	controlDial  func() control.TaskAPI
	controlMu    sync.Mutex
	controlRetry time.Time
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
		control:     dialControlPlane(cfg),
		controlDial: func() control.TaskAPI { return dialControlPlane(cfg) },
	}

	mux := http.NewServeMux()
	ws.RegisterRoutes(mux)

	// Serve static files (embedded in binary)
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return fmt.Errorf("setting up static files: %w", err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	RegisterAppRoutes(mux, core.ReadVersion())

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

// AppPaths are every URL that serves the single-page app itself, as ServeMux
// patterns. One list, because it is also what says which addresses a person may
// type, bookmark or reload — a route missing here is a 404 on a link somebody
// was given, and that is not visible from the page's own code.
//
// THE LEDGER HAS AN ADDRESS, and this is where it gets one. It was a div the
// Guild Hall toggled: it could not be bookmarked, opened in a second tab, sent
// to anybody, or reloaded — a refresh landed you back on the project list.
// `/ledger` is the board and `/ledger/T-18` is the board with that entry held
// open.
//
// Every one of them serves the SAME index.html. The page is one document and
// the route is read on the client (control.js), which is what lets Back and
// Forward move between the Hall and the Ledger without a round trip. The
// server's only job here is to answer these paths with the app instead of a
// 404, which is the whole of what makes reloading a deep link work.
var AppPaths = []string{
	"GET /{$}",
	"GET /ledger",
	"GET /ledger/{entry}",
}

// RegisterAppRoutes serves index.html, with the version injected into the title,
// at every path in AppPaths.
func RegisterAppRoutes(mux *http.ServeMux, version string) {
	index := func(w http.ResponseWriter, r *http.Request) {
		data, err := staticFiles.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, "index.html not found", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, err := w.Write([]byte(renderIndexHTML(data, version))); err != nil {
			log.Printf("write index.html: %v", err)
		}
	}
	for _, p := range AppPaths {
		mux.HandleFunc(p, index)
	}
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

	// control.go — the control plane, in one namespace. These mirror the daemon's
	// own routes (control/daemon.go) one for one: the Ledger can do what
	// `daedalus task` can do, through the same client, with the same authority.
	// They were three scattered paths (/api/approvals, /api/board,
	// /api/plane-status) while the surface was read-mostly; a whole operation set
	// spread the same way would have been a URL space nobody could hold in mind.
	//
	// Reads the page polls. Each answers "I could not ask" as data rather than as
	// an empty list, because those are different facts about the same plane.
	// What is actually running. Cheap, unauthenticated-adjacent (it sits behind
	// the same middleware as everything else) and the answer to "is this page the
	// code we just changed".
	mux.HandleFunc("GET /api/version", handleBuildVersion)

	mux.HandleFunc("GET /api/control/operations", ws.handleOperations)
	mux.HandleFunc("GET /api/control/status", ws.handlePlaneStatus)
	mux.HandleFunc("GET /api/control/board", ws.handleBoard)
	mux.HandleFunc("GET /api/control/approvals", ws.handleApprovals)
	mux.HandleFunc("GET /api/control/tasks", ws.handleControlTasks)
	mux.HandleFunc("GET /api/control/proposals", ws.handleProposals)
	mux.HandleFunc("GET /api/control/targets", ws.handleTargets)
	mux.HandleFunc("POST /api/control/tasks/{id}/refine", ws.handleRefineTask)
	mux.HandleFunc("GET /api/control/targets/lag", ws.handleTargetLags)
	mux.HandleFunc("GET /api/control/programmes", ws.handleListProgrammes)

	// Reads about one entity.
	mux.HandleFunc("GET /api/control/tasks/{id}", ws.handleTaskStatus)
	mux.HandleFunc("GET /api/control/tasks/{id}/events", ws.handleTaskEvents)
	mux.HandleFunc("GET /api/control/tasks/{id}/dependencies", ws.handleTaskDependencies)
	mux.HandleFunc("GET /api/control/jobs/{id}/steering", ws.handleJobSteering)

	// The lifecycle: create → dispatch → verify → approve → integrate, plus the
	// ladder out of a rejection (retry, reverify, replan, checks) and the exits.
	mux.HandleFunc("POST /api/control/tasks", ws.handleCreateTask)
	mux.HandleFunc("POST /api/control/tasks/{id}/dispatch", ws.handleDispatchTask)
	mux.HandleFunc("POST /api/control/tasks/{id}/verify", ws.handleVerifyTask)
	mux.HandleFunc("POST /api/control/tasks/{id}/retry", ws.handleRetryTask)
	mux.HandleFunc("POST /api/control/tasks/{id}/reverify", ws.handleReverifyTask)
	mux.HandleFunc("POST /api/control/tasks/{id}/checks", ws.handleAmendChecks)
	mux.HandleFunc("POST /api/control/tasks/{id}/budget", ws.handleAmendBudget)
	mux.HandleFunc("POST /api/control/tasks/{id}/replan", ws.handleReplanTask)
	mux.HandleFunc("POST /api/control/tasks/{id}/review", ws.handleReviewTask)
	mux.HandleFunc("POST /api/control/tasks/{id}/approve", ws.handleApproveTask)
	mux.HandleFunc("POST /api/control/tasks/{id}/reject", ws.handleRejectTask)
	mux.HandleFunc("POST /api/control/tasks/{id}/integrate", ws.handleIntegrateTask)
	mux.HandleFunc("DELETE /api/control/tasks/{id}", ws.handleCancelTask)

	// The graph, steering, proposals, and the integration target.
	mux.HandleFunc("POST /api/control/tasks/{id}/dependencies", ws.handleAddDependency)
	mux.HandleFunc("POST /api/control/jobs/{id}/steer", ws.handleSteerJob)
	mux.HandleFunc("DELETE /api/control/steering/{id}", ws.handleCancelSteering)
	mux.HandleFunc("POST /api/control/proposals/{id}/confirm", ws.handleConfirmProposal)
	mux.HandleFunc("POST /api/control/proposals/{id}/deny", ws.handleDenyProposal)
	mux.HandleFunc("POST /api/control/targets/{project}/sync", ws.handleSyncTarget)

	// Programmes (M20): the shared intent Tasks serve. These replace the
	// file-backed /api/programmes CRUD, so exactly one thing answers "what
	// programmes exist".
	mux.HandleFunc("GET /api/control/programmes/{id}", ws.handleGetProgramme)
	mux.HandleFunc("GET /api/control/programmes/{id}/status", ws.handleProgrammeStatus)
	mux.HandleFunc("POST /api/control/programmes", ws.handleCreateProgramme)
	mux.HandleFunc("POST /api/control/programmes/{id}", ws.handleUpdateProgramme)
	mux.HandleFunc("DELETE /api/control/programmes/{id}", ws.handleDeleteProgramme)

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

}
