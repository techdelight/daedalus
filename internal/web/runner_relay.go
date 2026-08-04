// Copyright (C) 2026 Techdelight BV

package web

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"sync"

	"github.com/techdelight/daedalus/internal/runclient"

	"github.com/gorilla/websocket"
)

// runnerRelay bridges a daedalus-runner Unix-socket connection (via
// runclient) and a browser WebSocket — the only terminal relay the web UI
// uses.
//
// One reader goroutine drains runner output into binary WebSocket frames;
// one writer goroutine reads WebSocket frames and forwards bytes as PTY
// input or translates resize JSON into runproto resize frames. Both stop
// when either side closes.
type runnerRelay struct {
	rc   *runclient.Conn
	conn *safeConn
	name string // project name, used in log messages
}

func newRunnerRelay(rc *runclient.Conn, conn *safeConn, projectName string) *runnerRelay {
	return &runnerRelay{rc: rc, conn: conn, name: projectName}
}

// Run blocks until both directions of the relay have terminated.
func (r *runnerRelay) Run() {
	var wg sync.WaitGroup
	wg.Add(2)
	go r.readRunner(&wg)
	go r.readWebSocket(&wg)
	wg.Wait()
}

// readRunner forwards runner output (including the hello-frame scrollback
// replay that runclient.Conn surfaces as the first bytes from Read) to
// the WebSocket as binary frames.
func (r *runnerRelay) readRunner(wg *sync.WaitGroup) {
	defer wg.Done()
	buf := make([]byte, 4096)
	for {
		n, err := r.rc.Read(buf)
		if n > 0 {
			if werr := r.conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				log.Printf("runner read error for %s: %v", r.name, err)
			}
			r.conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			return
		}
	}
}

// readWebSocket translates browser → runner traffic. JSON resize messages
// become runproto resize frames; everything else (binary frames, plain
// text not parseable as a resizeMsg) is forwarded as PTY input bytes.
func (r *runnerRelay) readWebSocket(wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		msgType, data, err := r.conn.ReadMessage()
		if err != nil {
			// runclient.Conn.Detach is best-effort; if the socket is
			// already gone the runner just sees EOF on its read side,
			// which is fine.
			_ = r.rc.Detach()
			return
		}
		switch msgType {
		case websocket.TextMessage:
			// Must precede the forward-as-input fallthrough below, or the
			// control message itself would be typed into the pane.
			if isEnterMsg(data) {
				if _, err := r.rc.Write([]byte(enterKey)); err != nil {
					return
				}
				continue
			}
			var rm resizeMsg
			if json.Unmarshal(data, &rm) == nil && rm.Type == "resize" && rm.Cols > 0 && rm.Rows > 0 {
				if err := r.rc.Resize(int(rm.Cols), int(rm.Rows)); err != nil {
					log.Printf("runner resize for %s: %v", r.name, err)
					return
				}
				continue
			}
			if _, err := r.rc.Write(data); err != nil {
				return
			}
		case websocket.BinaryMessage:
			if _, err := r.rc.Write(data); err != nil {
				return
			}
		}
	}
}
