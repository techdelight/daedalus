// Copyright (C) 2026 Techdelight BV

package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/coordinator"
	"github.com/techdelight/daedalus/internal/runclient"

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

// WebSocket keepalive tuning (#29). The server pings every pingPeriod; a live
// browser auto-responds with a pong (handled at the protocol level, no client
// JS needed), which refreshes the read deadline. A client that goes silent —
// a mobile Wi-Fi/cellular handoff or a backgrounded, throttled tab — stops
// ponging, the deadline expires, ReadMessage returns an error, and the relay
// ends cleanly (the underlying session survives; the browser reconnects).
const (
	wsPongWait   = 60 * time.Second
	wsPingPeriod = (wsPongWait * 9) / 10
	wsWriteWait  = 10 * time.Second
)

// enableKeepalive arms the read deadline + pong handler and starts a ping
// ticker. Returns a stop function the handler defers so the ping goroutine
// ends with the connection. Call once, right after newSafeConn, before the
// relay's read loop starts.
func (c *safeConn) enableKeepalive() (stop func()) {
	_ = c.Conn.SetReadDeadline(time.Now().Add(wsPongWait))
	c.Conn.SetPongHandler(func(string) error {
		return c.Conn.SetReadDeadline(time.Now().Add(wsPongWait))
	})
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(wsPingPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				// WriteControl may be called concurrently with WriteMessage
				// (gorilla guarantees this), so it needs no safeConn mutex.
				if err := c.Conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteWait)); err != nil {
					return
				}
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
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
	Type string `json:"type"`
}

// enterKey is the byte a terminal sends when Enter is pressed. The relay
// writes it to the runner PTY directly.
const enterKey = "\r"

// isEnterMsg reports whether data is the mobile Send button's enter signal.
//
// Enter travels as its own frame rather than as a \r appended to the text.
// Claude Code reads a chunk of text with a trailing newline as a paste and
// inserts a line break instead of submitting, so the submit has to arrive as
// a write of its own. Frames are delivered in order on one connection, so
// the text is always applied first.
func isEnterMsg(data []byte) bool {
	var m wsMsg
	return json.Unmarshal(data, &m) == nil && m.Type == "enter"
}

// handleTerminal asks the coordinator daemon for the session's runner socket,
// dials it via runclient, and bridges the WebSocket through runnerRelay.
//
// The daemon is the source of truth for "does a runner session exist for this
// project", so a stale socket file left over from a crashed prior container no
// longer produces a misleading 200. Auto-spawns the daemon if not already
// running (ssh-agent style), mirroring the CLI launch in
// cmd/daedalus/launch.go.
func (ws *WebServer) handleTerminal(w http.ResponseWriter, r *http.Request) {
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
		// CLI launch (launchProject). startRunnerSession writes the HTTP error
		// itself and returns nil on failure, before any upgrade.
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
	defer conn.enableKeepalive()()

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
// registry entry the same way the CLI launch does, so the web can start a
// runner session on its own — no separate CLI launch required first.
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

	// A container under this name that is not a coordinator session (e.g. a
	// stray `docker run`) would collide with the coordinator's `docker compose
	// run --name`, failing Start with a cryptic docker error. Detect it and say
	// so plainly.
	container := projCfg.ContainerName()
	if running, err := ws.docker.IsContainerRunning(container); err != nil {
		log.Printf("runner start %s: checking container %s: %v", name, container, err)
		http.Error(w, fmt.Sprintf("checking existing container: %v", err), http.StatusInternalServerError)
		return nil
	} else if running {
		http.Error(w, fmt.Sprintf("container %q is already running outside the runner path — stop it first with `daedalus stop %s` or `docker stop %s`, then attach", container, name, container), http.StatusConflict)
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
