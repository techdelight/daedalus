// Copyright (C) 2026 Techdelight BV

package web

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/agentstate"
	"github.com/techdelight/daedalus/internal/auth"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/docker"
	"github.com/techdelight/daedalus/internal/executor"
	"github.com/techdelight/daedalus/internal/foreman"
	"github.com/techdelight/daedalus/internal/mcpclient"
	"github.com/techdelight/daedalus/internal/platform"
	"github.com/techdelight/daedalus/internal/progress"
	"github.com/techdelight/daedalus/internal/programme"
	"github.com/techdelight/daedalus/internal/registry"
	"github.com/techdelight/daedalus/internal/session"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

// WebServer holds dependencies for the web UI HTTP handlers.
type WebServer struct {
	registry *registry.Registry
	docker   *docker.Docker
	executor executor.Executor
	cfg      *core.Config
	observer agentstate.Observer
	foreman  *foreman.Foreman
}

// NewWebServerForTest creates a WebServer with injected dependencies.
// Intended for integration tests that need to exercise handlers end-to-end.
func NewWebServerForTest(reg *registry.Registry, d *docker.Docker, exec executor.Executor, cfg *core.Config) *WebServer {
	return &WebServer{
		registry: reg,
		docker:   d,
		executor: exec,
		cfg:      cfg,
		observer: agentstate.NewContainerObserver(exec),
	}
}

// HandleListProjects is the exported handler for GET /api/projects.
func (ws *WebServer) HandleListProjects(w http.ResponseWriter, r *http.Request) {
	ws.handleListProjects(w, r)
}

// HandleStartProject is the exported handler for POST /api/projects/{name}/start.
func (ws *WebServer) HandleStartProject(w http.ResponseWriter, r *http.Request) {
	ws.handleStartProject(w, r)
}

// HandleStopProject is the exported handler for POST /api/projects/{name}/stop.
func (ws *WebServer) HandleStopProject(w http.ResponseWriter, r *http.Request) {
	ws.handleStopProject(w, r)
}

// HandleRenameProject is the exported handler for POST /api/projects/{name}/rename.
func (ws *WebServer) HandleRenameProject(w http.ResponseWriter, r *http.Request) {
	ws.handleRenameProject(w, r)
}

// HandleSendEnter is the exported handler for POST /api/projects/{name}/enter.
func (ws *WebServer) HandleSendEnter(w http.ResponseWriter, r *http.Request) {
	ws.handleSendEnter(w, r)
}

// HandleDashboard is the exported handler for GET /api/projects/{name}/dashboard.
func (ws *WebServer) HandleDashboard(w http.ResponseWriter, r *http.Request) {
	ws.handleDashboard(w, r)
}

// HandleRoadmap is the exported handler for GET /api/projects/{name}/roadmap.
func (ws *WebServer) HandleRoadmap(w http.ResponseWriter, r *http.Request) {
	ws.handleRoadmap(w, r)
}

// HandleAgentState is the exported handler for GET /api/projects/{name}/state.
func (ws *WebServer) HandleAgentState(w http.ResponseWriter, r *http.Request) {
	ws.handleAgentState(w, r)
}

// renameRequest is the JSON body for the rename endpoint.
type renameRequest struct {
	NewName string `json:"newName"`
}

// dashboardJSON is the JSON representation of a project dashboard.
type dashboardJSON struct {
	Name           string `json:"name"`
	Directory      string `json:"directory"`
	Target         string `json:"target"`
	Running        bool   `json:"running"`
	ProgressPct    int    `json:"progressPct"`
	Vision         string `json:"vision"`
	ProjectVersion string `json:"projectVersion"`
	SessionCount   int    `json:"sessionCount"`
	TotalTimeSec   int    `json:"totalTimeSec"`
	LastUsed       string `json:"lastUsed"`
	Created        string `json:"created"`
}

// roadmapJSON is the JSON response for the roadmap endpoint.
type roadmapJSON struct {
	Sprints []core.Sprint `json:"sprints"`
}

// projectJSON is the JSON representation of a project for the REST API.
type projectJSON struct {
	Name         string `json:"name"`
	Directory    string `json:"directory"`
	Target       string `json:"target"`
	LastUsed     string `json:"lastUsed"`
	Running      bool   `json:"running"`
	SessionCount int    `json:"sessionCount"`
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

	ws := &WebServer{
		registry: reg,
		docker:   docker,
		executor: exec,
		cfg:      cfg,
		observer: observer,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects", ws.handleListProjects)
	mux.HandleFunc("POST /api/projects/{name}/start", ws.handleStartProject)
	mux.HandleFunc("POST /api/projects/{name}/stop", ws.handleStopProject)
	mux.HandleFunc("POST /api/projects/{name}/rename", ws.handleRenameProject)
	mux.HandleFunc("POST /api/projects/{name}/enter", ws.handleSendEnter)
	mux.HandleFunc("GET /api/projects/{name}/dashboard", ws.handleDashboard)
	mux.HandleFunc("GET /api/projects/{name}/roadmap", ws.handleRoadmap)
	mux.HandleFunc("GET /api/projects/{name}/state", ws.handleAgentState)
	mux.HandleFunc("GET /api/projects/{name}/terminal", ws.handleTerminal)
	mux.HandleFunc("GET /api/foreman/status", ws.handleForemanStatus)
	mux.HandleFunc("POST /api/foreman/start", ws.handleForemanStart)
	mux.HandleFunc("POST /api/foreman/stop", ws.handleForemanStop)
	mux.HandleFunc("GET /api/programmes", ws.handleListProgrammes)
	mux.HandleFunc("POST /api/programmes", ws.handleCreateProgramme)
	mux.HandleFunc("GET /api/programmes/{name}", ws.handleGetProgramme)
	mux.HandleFunc("PUT /api/programmes/{name}", ws.handleUpdateProgramme)
	mux.HandleFunc("DELETE /api/programmes/{name}", ws.handleDeleteProgramme)

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
		w.Write([]byte(html))
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

// handleListProjects returns all registered projects with their running status.
func (ws *WebServer) handleListProjects(w http.ResponseWriter, r *http.Request) {
	entries, err := ws.registry.GetProjectEntries()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	projects := make([]projectJSON, 0, len(entries))
	for _, e := range entries {
		containerName := "claude-run-" + e.Name
		running, err := ws.docker.IsContainerRunning(containerName)
		if err != nil {
			log.Printf("Docker status check failed for %s: %v", e.Name, err)
		}
		projects = append(projects, projectJSON{
			Name:         e.Name,
			Directory:    e.Entry.Directory,
			Target:       e.Entry.Target,
			LastUsed:     e.Entry.LastUsed,
			Running:      running,
			SessionCount: len(e.Entry.Sessions),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(projects)
}

// handleStartProject starts a project's container and tmux session.
func (ws *WebServer) handleStartProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	entry, found, err := ws.registry.GetProject(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, fmt.Sprintf("project %q not found", name), http.StatusNotFound)
		return
	}

	projCfg := &core.Config{
		ProjectName: name,
		ScriptDir:   ws.cfg.ScriptDir,
		DataDir:     ws.cfg.DataDir,
		ImagePrefix: ws.cfg.ImagePrefix,
	}
	core.ApplyRegistryEntry(projCfg, entry)

	if err := docker.SetupCacheDir(projCfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	running, err := ws.docker.IsContainerRunning(projCfg.ContainerName())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if running {
		http.Error(w, fmt.Sprintf("project %q is already running", name), http.StatusConflict)
		return
	}

	if !ws.docker.ImageExists(projCfg.Image()) {
		http.Error(w, fmt.Sprintf("image %s not found — run daedalus --build %s first", projCfg.Image(), name), http.StatusPreconditionFailed)
		return
	}

	sess := session.NewSession(ws.executor, projCfg.TmuxSession())
	if !sess.Exists() {
		if err := sess.Create(); err != nil {
			http.Error(w, fmt.Sprintf("creating tmux session: %v", err), http.StatusInternalServerError)
			return
		}
	}

	var displayArgs []string
	if projCfg.Display {
		displayArgs, _ = platform.DisplayArgs(
			os.Getenv("DISPLAY"),
			os.Getenv("WAYLAND_DISPLAY"),
			os.Getenv("XDG_RUNTIME_DIR"),
		)
	}
	extraArgs := core.BuildExtraArgs(projCfg, displayArgs, nil)

	claudeArgs := core.BuildRunnerArgs(projCfg)
	dockerCmd := ws.docker.ComposeRunCommand(projCfg.ContainerName(), claudeArgs, extraArgs)
	tmuxCmd := core.BuildTmuxCommand(projCfg, dockerCmd)

	if err := sess.SendKeys(tmuxCmd); err != nil {
		http.Error(w, fmt.Sprintf("sending command to tmux: %v", err), http.StatusInternalServerError)
		return
	}

	if err := ws.registry.TouchProject(name); err != nil {
		log.Printf("Failed to update timestamp for %s: %v", name, err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "started", "project": name})
}

// handleStopProject stops a project's container.
func (ws *WebServer) handleStopProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	_, found, err := ws.registry.GetProject(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, fmt.Sprintf("project %q not found", name), http.StatusNotFound)
		return
	}

	containerName := "claude-run-" + name
	if _, err := ws.executor.Output("docker", "stop", containerName); err != nil {
		http.Error(w, fmt.Sprintf("stopping container: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "stopped", "project": name})
}

// handleSendEnter sends an Enter keypress to a project's tmux session.
func (ws *WebServer) handleSendEnter(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	_, found, err := ws.registry.GetProject(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, fmt.Sprintf("project %q not found", name), http.StatusNotFound)
		return
	}

	sess := session.NewSession(ws.executor, "claude-"+name)
	if !sess.Exists() {
		http.Error(w, fmt.Sprintf("no tmux session for project %q", name), http.StatusNotFound)
		return
	}

	if err := ws.executor.Run("tmux", "send-keys", "-t", "claude-"+name, "Enter"); err != nil {
		http.Error(w, fmt.Sprintf("sending Enter to tmux: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleRenameProject renames a project.
func (ws *WebServer) handleRenameProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	_, found, err := ws.registry.GetProject(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, fmt.Sprintf("project %q not found", name), http.StatusNotFound)
		return
	}

	var req renameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := core.ValidateProjectName(req.NewName); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	containerName := "claude-run-" + name
	running, err := ws.docker.IsContainerRunning(containerName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if running {
		http.Error(w, fmt.Sprintf("project %q is running — stop it before renaming", name), http.StatusConflict)
		return
	}

	if err := ws.registry.RenameProject(name, req.NewName); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "renamed", "oldName": name, "newName": req.NewName})
}

// handleRoadmap returns parsed roadmap sprints for a project.
func (ws *WebServer) handleRoadmap(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	entry, found, err := ws.registry.GetProject(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, fmt.Sprintf("project %q not found", name), http.StatusNotFound)
		return
	}

	roadmapPath := filepath.Join(entry.Directory, "ROADMAP.md")
	data, err := os.ReadFile(roadmapPath)
	if err != nil {
		if os.IsNotExist(err) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(roadmapJSON{Sprints: []core.Sprint{}})
			return
		}
		http.Error(w, fmt.Sprintf("reading roadmap: %v", err), http.StatusInternalServerError)
		return
	}

	sprints := core.ParseRoadmap(string(data))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(roadmapJSON{Sprints: sprints})
}

// handleDashboard returns dashboard data for a single project.
func (ws *WebServer) handleDashboard(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	entry, found, err := ws.registry.GetProject(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, fmt.Sprintf("project %q not found", name), http.StatusNotFound)
		return
	}

	containerName := "claude-run-" + name
	running, err := ws.docker.IsContainerRunning(containerName)
	if err != nil {
		log.Printf("Docker status check failed for %s: %v", name, err)
	}

	// Read progress data from .daedalus/progress.json in the project directory.
	// This is written by the project-mgmt-mcp server inside the container
	// and visible on the host via the bind mount.
	progData, _ := progress.Read(entry.Directory)

	totalTimeSec := 0
	for _, s := range entry.Sessions {
		totalTimeSec += s.Duration
	}

	progressPct := entry.ProgressPct
	vision := entry.Vision
	projectVersion := entry.ProjectVersion

	// Prefer progress file data over registry data (more current)
	if progData.ProgressPct > 0 {
		progressPct = progData.ProgressPct
	}
	if progData.Vision != "" {
		vision = progData.Vision
	}
	if progData.ProjectVersion != "" {
		projectVersion = progData.ProjectVersion
	}

	dash := dashboardJSON{
		Name:           name,
		Directory:      entry.Directory,
		Target:         entry.Target,
		Running:        running,
		ProgressPct:    progressPct,
		Vision:         vision,
		ProjectVersion: projectVersion,
		SessionCount:   len(entry.Sessions),
		TotalTimeSec:   totalTimeSec,
		LastUsed:       entry.LastUsed,
		Created:        entry.Created,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dash)
}

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type resizeMsg struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// handleAgentState returns the agent state for a project.
func (ws *WebServer) handleAgentState(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	_, found, err := ws.registry.GetProject(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, fmt.Sprintf("project %q not found", name), http.StatusNotFound)
		return
	}
	containerName := "claude-run-" + name
	state := ws.observer.GetState(containerName)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"state": string(state)})
}

// handleForemanStatus returns the current Foreman state.
func (ws *WebServer) handleForemanStatus(w http.ResponseWriter, r *http.Request) {
	if ws.foreman == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(core.ForemanStatus{State: core.ForemanIdle, Message: "not configured"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ws.foreman.Status())
}

// handleForemanStart starts the Foreman for a given programme.
func (ws *WebServer) handleForemanStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Programme string `json:"programme"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Programme == "" {
		http.Error(w, "request body must include \"programme\" field", http.StatusBadRequest)
		return
	}

	// Create Foreman if needed
	if ws.foreman == nil {
		progStore := programme.New(ws.cfg.ProgrammesDir())
		mcpClient := mcpclient.New()
		obs := foreman.NewDefaultObserver(ws.observer)
		cfg := core.ForemanConfig{Programme: req.Programme, PollSeconds: 30}
		ws.foreman = foreman.New(cfg, progStore, ws.registry, mcpClient, obs)
	}

	if err := ws.foreman.Start(); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "started", "programme": req.Programme})
}

// handleForemanStop stops the Foreman.
func (ws *WebServer) handleForemanStop(w http.ResponseWriter, r *http.Request) {
	if ws.foreman == nil {
		http.Error(w, "foreman is not running", http.StatusConflict)
		return
	}
	ws.foreman.Stop()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
}

// handleListProgrammes returns all programmes.
func (ws *WebServer) handleListProgrammes(w http.ResponseWriter, r *http.Request) {
	store := programme.New(ws.cfg.ProgrammesDir())
	progs, err := store.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if progs == nil {
		progs = []core.Programme{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(progs)
}

// handleCreateProgramme creates a new programme.
func (ws *WebServer) handleCreateProgramme(w http.ResponseWriter, r *http.Request) {
	var p core.Programme
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	store := programme.New(ws.cfg.ProgrammesDir())
	if err := store.Create(p); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(p)
}

// handleGetProgramme returns a single programme by name.
func (ws *WebServer) handleGetProgramme(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	store := programme.New(ws.cfg.ProgrammesDir())
	p, err := store.Read(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(p)
}

// handleUpdateProgramme updates an existing programme.
func (ws *WebServer) handleUpdateProgramme(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var p core.Programme
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	p.Name = name
	store := programme.New(ws.cfg.ProgrammesDir())
	if err := store.Update(p); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(p)
}

// handleDeleteProgramme deletes a programme by name.
func (ws *WebServer) handleDeleteProgramme(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	store := programme.New(ws.cfg.ProgrammesDir())
	if err := store.Remove(name); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "name": name})
}

func (ws *WebServer) handleTerminal(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	_, found, err := ws.registry.GetProject(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, fmt.Sprintf("project %q not found", name), http.StatusNotFound)
		return
	}

	sess := session.NewSession(ws.executor, "claude-"+name)
	if !sess.Exists() {
		http.Error(w, fmt.Sprintf("no tmux session for project %q", name), http.StatusNotFound)
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed for %s: %v", name, err)
		return
	}
	defer conn.Close()

	ptmx, cmd, err := startPTY("claude-" + name)
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("Failed to attach: %v", err)))
		return
	}
	defer cleanupPTY(cmd, ptmx)

	var wg sync.WaitGroup
	wg.Add(2)
	go relayPTYToWebSocket(&wg, ptmx, conn, name)
	go relayWebSocketToPTY(&wg, conn, ptmx)
	wg.Wait()
}

func startPTY(sessionName string) (*os.File, *exec.Cmd, error) {
	cmd := exec.Command("tmux", "attach-session", "-t", sessionName)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, nil, err
	}
	return ptmx, cmd, nil
}

func cleanupPTY(cmd *exec.Cmd, ptmx *os.File) {
	if cmd.Process != nil {
		cmd.Process.Signal(syscall.SIGHUP)
	}
	ptmx.Close()
	cmd.Wait()
}

func relayPTYToWebSocket(wg *sync.WaitGroup, ptmx *os.File, conn *websocket.Conn, name string) {
	defer wg.Done()
	buf := make([]byte, 4096)
	for {
		n, err := ptmx.Read(buf)
		if err != nil {
			if err != io.EOF {
				log.Printf("PTY read error for %s: %v", name, err)
			}
			conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			return
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
			return
		}
	}
}

func relayWebSocketToPTY(wg *sync.WaitGroup, conn *websocket.Conn, ptmx *os.File) {
	defer wg.Done()
	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			return
		}

		switch msgType {
		case websocket.TextMessage:
			var msg resizeMsg
			if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" && msg.Cols > 0 && msg.Rows > 0 {
				pty.Setsize(ptmx, &pty.Winsize{Rows: msg.Rows, Cols: msg.Cols})
				continue
			}
			ptmx.Write(data)
		case websocket.BinaryMessage:
			ptmx.Write(data)
		}
	}
}
