// Copyright (C) 2026 Techdelight BV

package web

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/session"

	"github.com/gorilla/websocket"
)

// controlRelay bridges a tmux ControlSession and a WebSocket. It owns a
// per-connection FIFO queue of expected response types so that %begin/%end
// blocks coming back from tmux can be matched to the commands that
// produced them.
//
// The reader goroutine is the sole consumer of tmux output, which avoids
// the data race that arose when multiple goroutines called ReadMessage()
// on the same bufio.Reader.
type controlRelay struct {
	cs       *session.ControlSession
	conn     *websocket.Conn
	sessName string // tmux session name, used for capture/resize commands
	name     string // project name, used in log messages

	pendingMu    sync.Mutex
	pendingTypes []string
}

func newControlRelay(cs *session.ControlSession, conn *websocket.Conn, sessName, projectName string) *controlRelay {
	return &controlRelay{
		cs:       cs,
		conn:     conn,
		sessName: sessName,
		name:     projectName,
	}
}

// Run starts the reader and writer goroutines and blocks until both stop.
func (r *controlRelay) Run() {
	var wg sync.WaitGroup
	wg.Add(2)
	go r.readTmux(&wg)
	go r.readWebSocket(&wg)
	wg.Wait()
}

// sendTracked sends a tmux command and enqueues the expected response type
// atomically, keeping the queue synchronised with the command stream.
// responseType "" means the response should be silently consumed (e.g.
// resize-window, send-keys).
func (r *controlRelay) sendTracked(command, responseType string) error {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	if err := r.cs.SendCommand(command); err != nil {
		return err
	}
	r.pendingTypes = append(r.pendingTypes, responseType)
	return nil
}

func (r *controlRelay) dequeueType() string {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	if len(r.pendingTypes) == 0 {
		return ""
	}
	t := r.pendingTypes[0]
	r.pendingTypes = r.pendingTypes[1:]
	return t
}

// readTmux forwards %output to the WebSocket and matches %begin/%end
// command-response blocks to the pendingTypes queue. On %layout-change
// (after a resize) it auto-issues a capture-pane to refresh the client.
func (r *controlRelay) readTmux(wg *sync.WaitGroup) {
	defer wg.Done()
	var (
		inResponse    bool
		responseLines []string
	)
	for {
		msg, err := r.cs.ReadMessage()
		if err != nil {
			r.conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			return
		}
		switch msg.Type {
		case session.MsgOutput:
			decoded := session.UnescapeOutput(msg.Content)
			if err := r.conn.WriteMessage(websocket.BinaryMessage, []byte(decoded)); err != nil {
				return
			}
		case session.MsgBegin:
			inResponse = true
			responseLines = responseLines[:0]
		case session.MsgEnd:
			if inResponse {
				respType := r.dequeueType()
				if respType != "" {
					content := strings.Join(responseLines, "\r\n")
					resp, _ := json.Marshal(scrollbackResponse{
						Type:    respType,
						Content: content,
					})
					if err := r.conn.WriteMessage(websocket.TextMessage, resp); err != nil {
						return
					}
				}
			}
			inResponse = false
		case session.MsgError:
			r.dequeueType()
			inResponse = false
		case session.MsgLayoutChange:
			cmd := fmt.Sprintf("capture-pane -t %s -p -e", r.sessName)
			if err := r.sendTracked(cmd, "live-capture-response"); err != nil {
				log.Printf("post-resize capture error for %s: %v", r.name, err)
			}
		case session.MsgUnknown:
			if inResponse {
				responseLines = append(responseLines, msg.Content)
			}
		}
	}
}

// readWebSocket dispatches client messages to tmux. JSON control messages
// (resize/scrollback/live-capture) become the matching tmux commands;
// everything else is treated as keystrokes for the active pane.
func (r *controlRelay) readWebSocket(wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		msgType, data, err := r.conn.ReadMessage()
		if err != nil {
			return
		}
		switch msgType {
		case websocket.TextMessage:
			if r.dispatchTextMessage(data) {
				return
			}
		case websocket.BinaryMessage:
			if err := r.sendKeys(data); err != nil {
				return
			}
		}
	}
}

// dispatchTextMessage handles a single text message from the WebSocket.
// Returns true if the writer goroutine should stop (fatal send error).
func (r *controlRelay) dispatchTextMessage(data []byte) bool {
	var m wsMsg
	if json.Unmarshal(data, &m) != nil {
		// Non-JSON text — treat as keystrokes.
		return r.sendKeys(data) != nil
	}
	switch m.Type {
	case "resize":
		var rm resizeMsg
		if err := json.Unmarshal(data, &rm); err != nil {
			log.Printf("invalid resize message for %s: %v", r.name, err)
			return false
		}
		if rm.Cols > 0 && rm.Rows > 0 {
			cmd := fmt.Sprintf("resize-window -t %s -x %d -y %d", r.sessName, int(rm.Cols), int(rm.Rows))
			if err := r.sendTracked(cmd, ""); err != nil {
				log.Printf("ResizeWindow error for %s: %v", r.name, err)
			}
		}
	case "scrollback":
		lines := m.Lines
		if lines <= 0 {
			lines = 500
		}
		cmd := fmt.Sprintf("capture-pane -t %s -p -e -S -%d", r.sessName, lines)
		if err := r.sendTracked(cmd, "scrollback-response"); err != nil {
			log.Printf("CapturePane error for %s: %v", r.name, err)
		}
	case "live-capture":
		cmd := fmt.Sprintf("capture-pane -t %s -p -e", r.sessName)
		if err := r.sendTracked(cmd, "live-capture-response"); err != nil {
			log.Printf("CaptureVisible error for %s: %v", r.name, err)
		}
	default:
		return r.sendKeys(data) != nil
	}
	return false
}

// sendKeys ships the given bytes as keystrokes to the active pane via
// core.BuildControlSendKeys, which keeps the resulting tmux command on
// a single line even when data contains newlines.
func (r *controlRelay) sendKeys(data []byte) error {
	cmd := core.BuildControlSendKeys(r.sessName, string(data))
	if err := r.sendTracked(cmd, ""); err != nil {
		log.Printf("SendKeys error for %s: %v", r.name, err)
		return err
	}
	return nil
}
