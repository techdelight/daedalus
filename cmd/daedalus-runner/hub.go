// Copyright (C) 2026 Techdelight BV

package main

import (
	"io"
	"log"
	"sync"

	"github.com/techdelight/daedalus/internal/runproto"
)

// Default per-client outbound channel size. Each frame is one entry.
// Picked to be big enough to absorb typical bursts (`ls -laR`-class
// output) without backpressuring the PTY reader. If a slow client
// fills the channel, the hub drops it (closes its send channel and
// removes it on the next event-loop turn) rather than stalling
// everyone.
const defaultClientQueue = 256

// Hub is the single coordinator inside daedalus-runner. All mutations
// of shared state — the client set, the negotiated PTY size, the
// scrollback — happen on the Hub's goroutine, so no locks are needed
// for that state. External components communicate via channels.
type Hub struct {
	// PTY stdin: bytes typed by clients are written here. Hub owns
	// the writer; outside callers deliver input via Input().
	ptyIn io.Writer

	// PTY size setter. Called whenever the negotiated min(cols, rows)
	// across attached clients changes. Real implementation wraps
	// pty.Setsize; tests pass a stub that records calls.
	setSize func(cols, rows int) error

	scroll *ringBuffer

	// Inbound channels from the world.
	add        chan *Client
	remove     chan *Client
	fromPty    chan []byte        // PTY → hub
	input      chan []byte        // client → PTY
	resize     chan resizeRequest // client size change
	runnerExit chan int           // runner exited; broadcast and stop

	// Hub-internal state.
	clients map[*Client]struct{}
	cols    int
	rows    int

	// initCols/initRows are the PTY dimensions applied once when Run
	// starts, before any client attaches, so the agent renders into a
	// real terminal instead of the 0x0 default creack/pty leaves. Zero
	// on either axis disables the behaviour.
	initCols int
	initRows int

	stopOnce sync.Once
	stopped  chan struct{}
}

// resizeRequest tells the hub a specific client wants its window
// dimensions updated. The hub recomputes the negotiated min and
// calls setSize when it changes.
type resizeRequest struct {
	client *Client
	cols   int
	rows   int
}

// NewHub constructs a Hub. ptyIn is the writable side of the runner's
// PTY (typically *os.File from creack/pty). setSize is invoked when
// the negotiated min size across all attached clients changes; pass
// nil to disable resize forwarding (useful in tests). initialCols and
// initialRows size the PTY once at startup, before any client attaches;
// pass 0 on either axis to leave the PTY at its creack/pty default.
func NewHub(ptyIn io.Writer, setSize func(cols, rows int) error, scrollbackBytes, initialCols, initialRows int) *Hub {
	return &Hub{
		ptyIn:      ptyIn,
		setSize:    setSize,
		scroll:     newRingBuffer(scrollbackBytes),
		initCols:   initialCols,
		initRows:   initialRows,
		add:        make(chan *Client),
		remove:     make(chan *Client),
		fromPty:    make(chan []byte, 64),
		input:      make(chan []byte, 64),
		resize:     make(chan resizeRequest, 16),
		runnerExit: make(chan int, 1),
		clients:    make(map[*Client]struct{}),
		stopped:    make(chan struct{}),
	}
}

// Run drives the event loop. Returns when the runner has exited and
// all clients have been notified, or when Stop is called.
func (h *Hub) Run() {
	h.applyInitialSize()
	for {
		select {
		case <-h.stopped:
			h.shutdown(0)
			return
		case code := <-h.runnerExit:
			h.shutdown(code)
			return
		case data := <-h.fromPty:
			h.scroll.Append(data)
			h.broadcast(runproto.NewOutput(data))
		case data := <-h.input:
			if _, err := h.ptyIn.Write(data); err != nil {
				log.Printf("daedalus-runner: pty stdin write: %v", err)
			}
		case c := <-h.add:
			h.clients[c] = struct{}{}
			// Send initial hello with the reconstructed current screen +
			// size. ScreenSnapshot trims scrollback to the last screen
			// boundary so a one-shot full-screen dialog (trust prompt,
			// --resume picker) renders for this client even if attaching
			// provokes no resize/repaint — Backlog #38. See screen.go.
			c.send(runproto.NewHello(h.scroll.ScreenSnapshot(), h.cols, h.rows))
		case c := <-h.remove:
			h.unregister(c)
		case rq := <-h.resize:
			if _, ok := h.clients[rq.client]; !ok {
				continue // client was already removed
			}
			rq.client.cols = rq.cols
			rq.client.rows = rq.rows
			h.recomputeSize()
		}
	}
}

// applyInitialSize sizes the PTY to the configured startup dimensions
// once, before the event loop accepts any client. Without it the agent
// renders into the 0x0 terminal creack/pty leaves until the first client
// negotiates a size, so a one-shot startup prompt (e.g. Claude's "trust
// this folder?" dialog) draws into a void and never repaints. A zero
// initCols/initRows (as tests pass) disables the behaviour.
func (h *Hub) applyInitialSize() {
	if h.initCols <= 0 || h.initRows <= 0 || h.setSize == nil {
		return
	}
	h.cols, h.rows = h.initCols, h.initRows
	if err := h.setSize(h.initCols, h.initRows); err != nil {
		log.Printf("daedalus-runner: initial pty setsize(%d,%d): %v", h.initCols, h.initRows, err)
	}
}

// Stop signals the event loop to exit cleanly. Idempotent.
func (h *Hub) Stop() {
	h.stopOnce.Do(func() { close(h.stopped) })
}

// Add registers a connected client and triggers the hello frame.
func (h *Hub) Add(c *Client) { h.add <- c }

// Remove deregisters a client (typically called when its socket closes).
func (h *Hub) Remove(c *Client) { h.remove <- c }

// FromPty queues PTY output bytes for broadcast.
func (h *Hub) FromPty(data []byte) { h.fromPty <- data }

// Input queues bytes from a client to write to the PTY's stdin.
func (h *Hub) Input(data []byte) { h.input <- data }

// Resize queues a client size change so the hub can recompute the
// negotiated window size and forward it to the PTY.
func (h *Hub) Resize(c *Client, cols, rows int) {
	h.resize <- resizeRequest{client: c, cols: cols, rows: rows}
}

// RunnerExited tells the hub the runner subprocess has exited. The
// hub broadcasts an exit frame to all clients and then stops.
func (h *Hub) RunnerExited(code int) {
	select {
	case h.runnerExit <- code:
	default: // already signalled; ignore
	}
}

// broadcast sends m to every attached client. Slow clients (whose
// outbound channel is already full) are dropped — the alternative is
// stalling the whole pipe behind one client's network.
func (h *Hub) broadcast(m runproto.Message) {
	for c := range h.clients {
		if !c.trySend(m) {
			log.Printf("daedalus-runner: dropping slow client")
			h.unregister(c)
		}
	}
}

// unregister tears down a client: remove from the set, close its
// outbound channel so its writer goroutine exits.
func (h *Hub) unregister(c *Client) {
	if _, ok := h.clients[c]; !ok {
		return
	}
	delete(h.clients, c)
	c.close()
	h.recomputeSize()
}

// recomputeSize picks the smallest non-zero (cols, rows) across all
// attached clients and forwards it to the PTY. If no client has
// reported a size yet, leaves the PTY at its current size.
func (h *Hub) recomputeSize() {
	cols, rows := 0, 0
	for c := range h.clients {
		if c.cols > 0 && (cols == 0 || c.cols < cols) {
			cols = c.cols
		}
		if c.rows > 0 && (rows == 0 || c.rows < rows) {
			rows = c.rows
		}
	}
	if cols == 0 || rows == 0 {
		return // no resize until at least one client provides dimensions
	}
	if cols == h.cols && rows == h.rows {
		return // unchanged
	}
	h.cols, h.rows = cols, rows
	if h.setSize != nil {
		if err := h.setSize(cols, rows); err != nil {
			log.Printf("daedalus-runner: pty setsize(%d,%d): %v", cols, rows, err)
		}
	}
}

// shutdown broadcasts the exit frame and closes every client's send
// channel so their writer goroutines drain and exit.
func (h *Hub) shutdown(code int) {
	h.broadcast(runproto.NewExit(code))
	for c := range h.clients {
		c.close()
	}
	h.clients = nil
}

// Client is the hub's view of one connected UI: an outbound channel
// for frames the hub wants the writer goroutine to send, plus the
// most recent reported window dimensions.
type Client struct {
	out  chan runproto.Message
	cols int
	rows int

	closeOnce sync.Once
	closed    chan struct{}
}

// NewClient returns a Client with a default-size outbound queue.
func NewClient() *Client {
	return &Client{
		out:    make(chan runproto.Message, defaultClientQueue),
		closed: make(chan struct{}),
	}
}

// Out returns the channel the writer goroutine should drain to ship
// frames out over the socket. It is closed once the hub releases this
// client (either on shutdown or because it was too slow).
func (c *Client) Out() <-chan runproto.Message { return c.out }

// Closed reports the channel that fires once the client has been
// released. Useful for the reader goroutine to bail without polling.
func (c *Client) Closed() <-chan struct{} { return c.closed }

// send delivers m without dropping. Used for hello where we'd rather
// the client see the scrollback than race with the queue size.
func (c *Client) send(m runproto.Message) {
	select {
	case c.out <- m:
	case <-c.closed:
	}
}

// trySend attempts a non-blocking send; returns false if the queue is
// full, signalling the hub that this client is too slow.
func (c *Client) trySend(m runproto.Message) bool {
	select {
	case c.out <- m:
		return true
	default:
		return false
	}
}

// close releases the client. Idempotent.
func (c *Client) close() {
	c.closeOnce.Do(func() {
		close(c.closed)
		close(c.out)
	})
}
