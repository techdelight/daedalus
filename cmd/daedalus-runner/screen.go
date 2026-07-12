// Copyright (C) 2026 Techdelight BV

package main

import "bytes"

// This file gives the scrollback ring a screen-aware replay. The plain
// ring (ringbuffer.go) hands a newly-attached client the last N raw
// bytes. That is fine for ordinary scrolling output, but it does not
// reconstruct a *one-shot full-screen* UI — Claude's "trust this folder?"
// dialog, the `--resume` picker — that was drawn once and then idles.
// Such a screen is established by a clear or an alternate-screen switch,
// and everything the user currently sees is the bytes from that boundary
// onward. Replaying from the boundary instead of from the ring's oldest
// byte reproduces the live screen for every viewer — first, second,
// same-size, or reattached — without touching the shared PTY, forcing a
// SIGWINCH, or depending on the app choosing to repaint (Backlog #38).
//
// This is a deliberately small heuristic, not a terminal emulator: it
// anchors on the last screen-establishing boundary and replays verbatim.
// It reconstructs clear/alt-screen-anchored dialogs exactly; it does not
// reconstruct SGR/cursor state set *before* the boundary (a full VT
// emulator — Option C in docs/runner-repaint-design.md — would). When no
// boundary is present in the retained scrollback it falls back to the raw
// snapshot, so it is never worse than the plain ring.

// Screen-establishing byte sequences we anchor on.
var (
	// Entering the alternate screen clears the alt buffer, so a screen
	// drawn there is fully reconstructed by replaying from the switch.
	altScreenEnter = [][]byte{
		[]byte("\x1b[?1049h"), // modern (save cursor + alt buffer)
		[]byte("\x1b[?1047h"), // alt buffer
		[]byte("\x1b[?47h"),   // legacy alt buffer
	}
	altScreenLeave = [][]byte{
		[]byte("\x1b[?1049l"),
		[]byte("\x1b[?1047l"),
		[]byte("\x1b[?47l"),
	}
	// Full-screen clears on the primary buffer.
	screenClears = [][]byte{
		[]byte("\x1b[2J"), // erase entire display
		[]byte("\x1b[3J"), // erase display + scrollback
		[]byte("\x1bc"),   // RIS — full terminal reset
	}
)

// ScreenSnapshot returns the scrollback trimmed to the most recent
// screen-establishing boundary, so replaying it into a fresh terminal
// reproduces the runner's current screen. Falls back to the full raw
// snapshot when no boundary is retained.
func (r *ringBuffer) ScreenSnapshot() []byte {
	snap := r.Snapshot()
	if len(snap) == 0 {
		return snap
	}
	return snap[lastScreenStart(snap):]
}

// lastScreenStart returns the offset in b from which replaying reproduces
// the current screen. If the alternate screen is currently active, that
// is the last alt-screen entry (which cleared the alt buffer). Otherwise
// it is the last primary-screen clear. Returns 0 — replay everything —
// when neither is present, matching the plain ring's behaviour.
func lastScreenStart(b []byte) int {
	lastEnter := lastIndexAny(b, altScreenEnter)
	lastLeave := lastIndexAny(b, altScreenLeave)
	// Alt-screen active iff its most recent entry is more recent than any
	// exit. Anchor on the entry so the client also switches into the alt
	// buffer before the reconstructed draw.
	if lastEnter > lastLeave {
		return lastEnter
	}
	if idx := lastIndexAny(b, screenClears); idx >= 0 {
		return idx
	}
	return 0
}

// lastIndexAny returns the greatest start index at which any of needles
// occurs in b, or -1 if none do.
func lastIndexAny(b []byte, needles [][]byte) int {
	best := -1
	for _, n := range needles {
		if i := bytes.LastIndex(b, n); i > best {
			best = i
		}
	}
	return best
}
