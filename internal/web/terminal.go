// Copyright (C) 2026 Techdelight BV

package web

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/techdelight/daedalus/internal/session"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

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

type wsMsg struct {
	Type  string `json:"type"`
	Lines int    `json:"lines,omitempty"`
}

type scrollbackResponse struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

func (ws *WebServer) handleTerminal(w http.ResponseWriter, r *http.Request) {
	// Route to control mode if ?mode=control is set
	if r.URL.Query().Get("mode") == "control" {
		ws.handleTerminalControl(w, r)
		return
	}

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

// handleTerminalControl is the control-mode alternative to handleTerminal.
// It uses tmux -C for structured I/O instead of a raw PTY relay.
// Activated by ?mode=control on the terminal WebSocket endpoint. The
// reader/writer goroutines and the FIFO response queue live in
// controlRelay (see control_relay.go).
func (ws *WebServer) handleTerminalControl(w http.ResponseWriter, r *http.Request) {
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

	sessName := "claude-" + name
	sess := session.NewSession(ws.executor, sessName)
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

	cs, err := session.StartControlSession(sessName)
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("Failed to start control mode: %v", err)))
		return
	}
	defer cs.Close()

	// Capture visible pane content before starting the relay so the
	// terminal is populated immediately on connect — no reader contention
	// because the relay goroutines have not started yet.
	if content, err := cs.CaptureVisible(); err == nil && content != "" {
		conn.WriteMessage(websocket.BinaryMessage, []byte(content))
	}

	newControlRelay(cs, conn, sessName, name).Run()
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
