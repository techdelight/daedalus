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
	"sync"
	"syscall"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/docker"
	"github.com/techdelight/daedalus/internal/executor"
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
}

// NewWebServerForTest creates a WebServer with injected dependencies.
// Intended for integration tests that need to exercise handlers end-to-end.
func NewWebServerForTest(reg *registry.Registry, d *docker.Docker, exec executor.Executor, cfg *core.Config) *WebServer {
	return &WebServer{
		registry: reg,
		docker:   d,
		executor: exec,
		cfg:      cfg,
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
	core.PrintBanner(cfg.ScriptDir)
	exec := &executor.RealExecutor{}
	reg := registry.NewRegistry(cfg.RegistryPath())
	if err := reg.Init(); err != nil {
		return fmt.Errorf("initializing registry: %w", err)
	}
	docker := docker.NewDocker(exec, filepath.Join(cfg.ScriptDir, "docker-compose.yml"))

	ws := &WebServer{
		registry: reg,
		docker:   docker,
		executor: exec,
		cfg:      cfg,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects", ws.handleListProjects)
	mux.HandleFunc("POST /api/projects/{name}/start", ws.handleStartProject)
	mux.HandleFunc("POST /api/projects/{name}/stop", ws.handleStopProject)
	mux.HandleFunc("GET /api/projects/{name}/terminal", ws.handleTerminal)

	// Serve static files (embedded in binary)
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return fmt.Errorf("setting up static files: %w", err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Root serves index.html
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		data, err := staticFiles.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, "index.html not found", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})

	fmt.Printf("Starting web UI at http://%s\n", cfg.WebAddr)
	return http.ListenAndServe(cfg.WebAddr, mux)
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
		ProjectDir:  entry.Directory,
		ScriptDir:   ws.cfg.ScriptDir,
		DataDir:     ws.cfg.DataDir,
		Target:      entry.Target,
		ImagePrefix: ws.cfg.ImagePrefix,
	}

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

	claudeArgs := core.BuildClaudeArgs(projCfg)
	dockerCmd := ws.docker.ComposeRunCommand(projCfg.ContainerName(), claudeArgs, nil)
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
