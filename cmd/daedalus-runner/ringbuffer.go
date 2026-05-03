// Copyright (C) 2026 Techdelight BV

package main

import "sync"

// ringBuffer is a fixed-capacity byte buffer that retains the most
// recent N bytes appended to it. New bytes overwrite the oldest when
// capacity is reached. Used for scrollback: when a UI client connects,
// the hub replays the buffer's contents in the initial hello frame so
// the user sees the runner's recent output rather than a blank screen.
type ringBuffer struct {
	mu   sync.Mutex
	buf  []byte // size == capacity once filled; smaller while filling
	cap  int
	full bool
	head int // next write position
}

// newRingBuffer allocates a ringBuffer with the given byte capacity.
// A capacity of 0 disables scrollback (Snapshot always returns nil).
func newRingBuffer(capacity int) *ringBuffer {
	return &ringBuffer{cap: capacity}
}

// Append copies p into the buffer, evicting the oldest bytes if the
// new content overflows capacity.
func (r *ringBuffer) Append(p []byte) {
	if r.cap == 0 || len(p) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	// Fast path: tail still fits.
	if !r.full && r.head+len(p) <= r.cap {
		if r.buf == nil {
			r.buf = make([]byte, r.cap)
		}
		copy(r.buf[r.head:], p)
		r.head += len(p)
		if r.head == r.cap {
			r.full = true
			r.head = 0
		}
		return
	}

	if r.buf == nil {
		r.buf = make([]byte, r.cap)
	}

	// If p is larger than the buffer, only the last cap bytes can fit.
	if len(p) >= r.cap {
		copy(r.buf, p[len(p)-r.cap:])
		r.head = 0
		r.full = true
		return
	}

	// Wrap-around write.
	r.full = true
	n := copy(r.buf[r.head:], p)
	if n < len(p) {
		copy(r.buf, p[n:])
	}
	r.head = (r.head + len(p)) % r.cap
}

// Snapshot returns a copy of the buffer's contents in chronological
// order (oldest byte first, newest last). The returned slice is owned
// by the caller and can be mutated freely.
func (r *ringBuffer) Snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cap == 0 || r.buf == nil {
		return nil
	}
	if !r.full {
		out := make([]byte, r.head)
		copy(out, r.buf[:r.head])
		return out
	}
	out := make([]byte, r.cap)
	n := copy(out, r.buf[r.head:])
	copy(out[n:], r.buf[:r.head])
	return out
}
