// Copyright (C) 2026 Techdelight BV

package main

import (
	"errors"
	"io"
	"log"
	"net"

	"github.com/techdelight/daedalus/internal/runproto"
)

// serveConn drives one accepted Unix-socket connection. It registers
// a Client with the hub, runs reader and writer goroutines bridging
// the wire protocol to hub channels, and cleans up on disconnect.
//
// The function blocks until the connection is fully torn down.
func serveConn(conn net.Conn, hub *Hub) {
	defer conn.Close()

	c := NewClient()
	hub.Add(c)
	defer hub.Remove(c)

	enc := runproto.NewEncoder(conn)
	dec := runproto.NewDecoder(conn)

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for m := range c.Out() {
			if err := enc.Encode(m); err != nil {
				// Most likely the client disconnected; nothing to do
				// other than stop. The hub will see the closed channel
				// once we return and Remove fires.
				return
			}
		}
	}()

	for {
		m, err := dec.Decode()
		if err != nil {
			if !isExpectedDisconnect(err) {
				log.Printf("daedalus-runner: client decode: %v", err)
			}
			break
		}
		switch m.Type {
		case runproto.TypeInput:
			if len(m.Data) > 0 {
				hub.Input(m.Data)
			}
		case runproto.TypeResize:
			hub.Resize(c, m.Cols, m.Rows)
		case runproto.TypeDetach:
			// Client requested a clean shutdown of its connection.
			return
		default:
			// Server-bound types from a client (output, hello, exit,
			// runner-event) are protocol violations — log and drop.
			log.Printf("daedalus-runner: unexpected client frame type %q", m.Type)
		}
	}

	// Closing the connection causes the writer goroutine's Encode to
	// fail and return; wait for it so we don't leak.
	conn.Close()
	<-writerDone
}

// isExpectedDisconnect reports whether err is one of the unsurprising
// terminations of a client connection (clean EOF, peer closed, our
// own close after a Detach).
func isExpectedDisconnect(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	return false
}
