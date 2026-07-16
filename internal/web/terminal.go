// Copyright (C) 2026 Techdelight BV

package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/coordinator"
	"github.com/techdelight/daedalus/internal/runclient"
	"github.com/techdelight/daedalus/internal/session"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// safeConn serializes writes to a WebSocket.
//
// gorilla/websocket permits only one concurrent writer, and each relay used
// to satisfy that by having exactly one goroutine write while the other only
// read. The branch watcher breaks that assumption: it pushes from its own
// goroutine alongside the relay's output pump. Embedding the connection and
// overriding WriteMessage puts every existing write behind the mutex without
// touching the call sites. Reads stay direct — there is still one reader.
type safeConn struct {
	*websocket.Conn
	mu sync.Mutex
}

func newSafeConn(conn *websocket.Conn) *safeConn {
	return &safeConn{Conn: conn}
}

func (c *safeConn) WriteMessage(messageType int, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Conn.WriteMessage(messageType, data)
}

// startBranchWatch begins pushing branch changes for the attached session and
// returns a stop function to be deferred by the handler, so the watcher's
// lifetime is exactly the session's.
func startBranchWatch(conn *safeConn, projectDir string) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	go watchBranch(ctx, conn, projectDir, branchFallbackInterval)
	return cancel
}

type resizeMsg struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

type wsMsg struct {
	Type  string `json:"type"`
	Lines int    `json:"lines,omitempty"`
}

type scrollbackResponse struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

func (ws *WebServer) handleTerminal(w http.ResponseWriter, r *http.Request) {
	// Route to control or runner mode based on ?mode=...
	switch r.URL.Query().Get("mode") {
	case "control":
		ws.handleTerminalControl(w, r)
		return
	case "runner":
		ws.handleTerminalRunner(w, r)
		return
	}

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

	sessName := core.TmuxSessionFor(ws.cfg.TmuxPrefix, name)
	sess := session.NewSession(ws.executor, sessName)
	if !sess.Exists() {
		http.Error(w, fmt.Sprintf("no tmux session for project %q", name), http.StatusNotFound)
		return
	}

	rawConn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed for %s: %v", name, err)
		return
	}
	conn := newSafeConn(rawConn)
	defer conn.Close()

	ptmx, cmd, err := startPTY(sessName)
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("Failed to attach: %v", err)))
		return
	}
	defer cleanupPTY(cmd, ptmx)

	defer startBranchWatch(conn, entry.Directory)()

	var wg sync.WaitGroup
	wg.Add(2)
	go relayPTYToWebSocket(&wg, ptmx, conn, name)
	go relayWebSocketToPTY(&wg, conn, ptmx)
	wg.Wait()
}

// handleTerminalControl is the control-mode alternative to handleTerminal.
// It uses tmux -C for structured I/O instead of a raw PTY relay.
// Activated by ?mode=control on the terminal WebSocket endpoint. The
// reader/writer goroutines and the FIFO response queue live in
// controlRelay (see control_relay.go).
func (ws *WebServer) handleTerminalControl(w http.ResponseWriter, r *http.Request) {
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

	sessName := core.TmuxSessionFor(ws.cfg.TmuxPrefix, name)
	sess := session.NewSession(ws.executor, sessName)
	if !sess.Exists() {
		http.Error(w, fmt.Sprintf("no tmux session for project %q", name), http.StatusNotFound)
		return
	}

	rawConn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed for %s: %v", name, err)
		return
	}
	conn := newSafeConn(rawConn)
	defer conn.Close()

	cs, err := session.StartControlSession(sessName)
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("Failed to start control mode: %v", err)))
		return
	}
	defer cs.Close()

	defer startBranchWatch(conn, entry.Directory)()

	// Capture visible pane content before starting the relay so the
	// terminal is populated immediately on connect — no reader contention
	// because the relay goroutines have not started yet.
	if content, err := cs.CaptureVisible(); err == nil && content != "" {
		conn.WriteMessage(websocket.BinaryMessage, []byte(content))
	}

	newControlRelay(cs, conn, sessName, name).Run()
}

// handleTerminalRunner is the runner-mode alternative to handleTerminal.
// It asks the coordinator daemon for the session's runner socket, dials
// it via runclient, and bridges the WebSocket through runnerRelay.
// Activated by ?mode=runner on the terminal WebSocket endpoint.
//
// The daemon is the source of truth for "does a runner session exist
// for this project", so a stale socket file left over from a crashed
// prior container no longer produces a misleading 200. Auto-spawns
// the daemon if not already running (ssh-agent style), mirroring the
// CLI runner-detached path in cmd/daedalus/launch.go.
func (ws *WebServer) handleTerminalRunner(w http.ResponseWriter, r *http.Request) {
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

	client, err := coordinator.EnsureRunning(coordinator.DefaultLayout(ws.cfg.DataDir, ws.cfg.ScriptDir))
	if err != nil {
		http.Error(w, fmt.Sprintf("coordinator unavailable: %v", err), http.StatusServiceUnavailable)
		return
	}

	sess, err := client.Get(name)
	if errors.Is(err, coordinator.ErrNotFound) {
		// No runner session yet — launch one, so the web is self-sufficient
		// instead of telling the user to start it from the CLI. Mirrors the
		// CLI runner launch (launchProjectViaRunner) and the control-mode
		// start button (handleStartProject). startRunnerSession writes the
		// HTTP error itself and returns nil on failure, before any upgrade.
		if sess = ws.startRunnerSession(w, name, entry, client); sess == nil {
			return
		}
	} else if err != nil {
		http.Error(w, fmt.Sprintf("coordinator error: %v", err), http.StatusInternalServerError)
		return
	}

	rawConn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed for %s: %v", name, err)
		return
	}
	conn := newSafeConn(rawConn)
	defer conn.Close()

	rc, err := runclient.Dial(sess.SocketPath)
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("Failed to attach to runner: %v", err)))
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "runner dial failed"))
		return
	}
	defer rc.Close()

	defer startBranchWatch(conn, entry.Directory)()

	newRunnerRelay(rc, conn, name).Run()
}

// startRunnerSession launches a runner container for the project via the
// coordinator and returns its session. It builds the project config from the
// registry entry the same way the control-mode start button does
// (handleStartProject) and the CLI runner launch does, so the web can start a
// runner session on its own — no `DAEDALUS_USE_RUNNER=1 daedalus <project>`
// step required first.
//
// It writes the HTTP error and returns nil on failure, and must be called
// before the WebSocket upgrade so those errors reach the browser as real
// status codes. Container boot can take a few seconds; the browser's WS
// handshake stays pending until it completes.
func (ws *WebServer) startRunnerSession(w http.ResponseWriter, name string, entry core.ProjectEntry, client *coordinator.Client) *coordinator.Session {
	projCfg := &core.Config{
		ProjectName:     name,
		ScriptDir:       ws.cfg.ScriptDir,
		DataDir:         ws.cfg.DataDir,
		ImagePrefix:     ws.cfg.ImagePrefix,
		ContainerPrefix: ws.cfg.ContainerPrefix,
	}
	core.ApplyRegistryEntry(projCfg, entry)

	if !ws.docker.ImageExists(projCfg.Image()) {
		http.Error(w, fmt.Sprintf("image %s not found — run `daedalus --build %s` first", projCfg.Image(), name), http.StatusPreconditionFailed)
		return nil
	}

	// A container under this name from a non-runner launch (e.g. the tmux
	// control path) is not a coordinator session, so Get above missed it —
	// but its name would collide with the coordinator's `docker compose run
	// --name`, failing Start with a cryptic docker error. Detect it and say
	// so plainly. Mirrors handleStartProject's already-running guard.
	container := projCfg.ContainerName()
	if running, err := ws.docker.IsContainerRunning(container); err != nil {
		log.Printf("runner start %s: checking container %s: %v", name, container, err)
		http.Error(w, fmt.Sprintf("checking existing container: %v", err), http.StatusInternalServerError)
		return nil
	} else if running {
		http.Error(w, fmt.Sprintf("container %q is already running outside the runner path (e.g. started in the tmux/control UI) — stop it first with `daedalus stop %s` or `docker stop %s`, then attach in runner mode", container, name, container), http.StatusConflict)
		return nil
	}

	sess, err := client.Start(projCfg)
	if errors.Is(err, coordinator.ErrAlreadyRunning) {
		// Raced with another attach (or a CLI launch) that started it first;
		// use the existing session rather than failing.
		sess, err = client.Get(name)
	}
	if err != nil {
		// Surface the real reason server-side: a failed WebSocket handshake
		// discards the HTTP body, so without this the operator sees only a
		// closed connection in the browser and nothing in the logs.
		log.Printf("runner start %s: %v", name, err)
		http.Error(w, fmt.Sprintf("failed to start runner session for %q: %v", name, err), http.StatusInternalServerError)
		return nil
	}

	if err := ws.registry.TouchProject(name); err != nil {
		log.Printf("Failed to update timestamp for %s: %v", name, err)
	}
	return sess
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
		if err := cmd.Process.Signal(syscall.SIGHUP); err != nil {
			log.Printf("SIGHUP to PTY process: %v", err)
		}
	}
	if err := ptmx.Close(); err != nil {
		log.Printf("close PTY: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		log.Printf("wait for PTY process: %v", err)
	}
}

func relayPTYToWebSocket(wg *sync.WaitGroup, ptmx *os.File, conn *safeConn, name string) {
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

func relayWebSocketToPTY(wg *sync.WaitGroup, conn *safeConn, ptmx *os.File) {
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
				if err := pty.Setsize(ptmx, &pty.Winsize{Rows: msg.Rows, Cols: msg.Cols}); err != nil {
					log.Printf("PTY setsize: %v", err)
				}
				continue
			}
			if _, err := ptmx.Write(data); err != nil {
				return
			}
		case websocket.BinaryMessage:
			if _, err := ptmx.Write(data); err != nil {
				return
			}
		}
	}
}
