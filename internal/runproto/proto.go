// Copyright (C) 2026 Techdelight BV

// Package runproto defines the JSON-line wire protocol spoken between
// daedalus-runner (inside a project's container) and any client UI on
// the host (CLI, TUI, Web bridge). See ARCHITECTURE.md for the layered
// runner-adapter / daedalus-runner / UI stack.
//
// Frames are newline-delimited JSON values written over a single Unix
// socket connection. The connection is full-duplex; both sides read and
// write independently.
package runproto

// Type identifies the kind of a Message.
type Type string

const (
	// Server → client.
	TypeHello       Type = "hello"        // first frame on connect: scrollback + initial size
	TypeOutput      Type = "output"       // PTY output bytes
	TypeRunnerEvent Type = "runner-event" // structured event surfaced by a runner-adapter
	TypeExit        Type = "exit"         // runner subprocess exited

	// Client → server.
	TypeInput  Type = "input"  // bytes to write to the runner's PTY stdin
	TypeResize Type = "resize" // client's window dimensions
	TypeDetach Type = "detach" // graceful disconnect signal
)

// Message is the wire envelope. Exactly one set of payload fields is
// populated based on Type. encoding/json base64-encodes []byte fields
// automatically, which gives us a JSON-safe channel for raw PTY bytes.
type Message struct {
	Type Type `json:"t"`

	// Output, Input.
	Data []byte `json:"data,omitempty"`

	// Hello.
	Scrollback []byte `json:"scrollback,omitempty"`

	// Hello, Resize.
	Cols int `json:"cols,omitempty"`
	Rows int `json:"rows,omitempty"`

	// Exit. Pointer so a real exit code of 0 survives `omitempty`.
	Code *int `json:"code,omitempty"`

	// RunnerEvent.
	Event     string         `json:"event,omitempty"`
	EventData map[string]any `json:"eventData,omitempty"`
}

// NewHello constructs the initial server-side frame sent to a client on
// connect: the scrollback buffer to repaint and the runner's current
// window size. Either of cols/rows may be 0 if no client has set a size
// yet (the runner uses a default until the first resize).
func NewHello(scrollback []byte, cols, rows int) Message {
	return Message{Type: TypeHello, Scrollback: scrollback, Cols: cols, Rows: rows}
}

// NewOutput wraps PTY output bytes for broadcast to attached clients.
func NewOutput(data []byte) Message {
	return Message{Type: TypeOutput, Data: data}
}

// NewRunnerEvent surfaces a structured event from a runner-adapter
// (e.g. tool_use, stop, error). The eventData map is adapter-specific
// and forwarded verbatim to clients.
func NewRunnerEvent(event string, data map[string]any) Message {
	return Message{Type: TypeRunnerEvent, Event: event, EventData: data}
}

// NewExit announces that the runner subprocess has exited with the
// given status code. Sent once per session; clients should disconnect
// after observing it.
func NewExit(code int) Message {
	return Message{Type: TypeExit, Code: &code}
}

// NewInput wraps bytes typed by a client for delivery to the runner's
// PTY stdin. With multi-writer fan-in, input from all clients is
// interleaved in arrival order on the server.
func NewInput(data []byte) Message {
	return Message{Type: TypeInput, Data: data}
}

// NewResize reports a client's terminal dimensions. The server picks
// the smallest of all attached clients' cols/rows when sizing the PTY.
func NewResize(cols, rows int) Message {
	return Message{Type: TypeResize, Cols: cols, Rows: rows}
}

// NewDetach signals a graceful client disconnect. The server uses this
// to log a clean detach rather than treat connection close as abrupt.
func NewDetach() Message {
	return Message{Type: TypeDetach}
}
