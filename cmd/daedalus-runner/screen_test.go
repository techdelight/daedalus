// Copyright (C) 2026 Techdelight BV

package main

import (
	"bytes"
	"testing"
)

func TestLastScreenStart(t *testing.T) {
	const (
		clr    = "\x1b[2J"
		clr3   = "\x1b[3J"
		ris    = "\x1bc"
		altOn  = "\x1b[?1049h"
		altOff = "\x1b[?1049l"
	)
	tests := []struct {
		name string
		in   string
		want int // byte offset lastScreenStart should return
	}{
		{
			name: "no boundary replays everything",
			in:   "just some scrolling output\r\nmore\r\n",
			want: 0,
		},
		{
			name: "anchors on the clear",
			in:   "old junk before" + clr + "DIALOG",
			want: len("old junk before"),
		},
		{
			name: "anchors on the LAST clear",
			in:   clr + "first screen" + clr + "second screen",
			want: len(clr + "first screen"),
		},
		{
			name: "erase-with-scrollback counts as a clear",
			in:   "noise" + clr3 + "DIALOG",
			want: len("noise"),
		},
		{
			name: "RIS full reset counts as a clear",
			in:   "noise" + ris + "DIALOG",
			want: len("noise"),
		},
		{
			name: "active alt-screen anchors on the enter, not a later clear",
			in:   "primary" + altOn + clr + "ALT DIALOG",
			want: len("primary"),
		},
		{
			name: "left alt-screen falls back to primary clear",
			in:   altOn + "alt stuff" + altOff + "back on primary" + clr + "DIALOG",
			want: len(altOn + "alt stuff" + altOff + "back on primary"),
		},
		{
			name: "left alt-screen with no later clear replays everything",
			in:   "before" + altOn + "alt" + altOff + "after",
			want: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := lastScreenStart([]byte(tc.in)); got != tc.want {
				t.Fatalf("lastScreenStart = %d, want %d\n(trimmed = %q)", got, tc.want, tc.in[min(got, len(tc.in)):])
			}
		})
	}
}

// TestScreenSnapshotReconstructsDialog is the property that matters for
// #38: after a one-shot dialog is drawn, a freshly attached client's
// snapshot both begins at a screen boundary and contains the dialog, so
// rendering it reproduces the screen — regardless of what came before.
func TestScreenSnapshotReconstructsDialog(t *testing.T) {
	r := newRingBuffer(1 << 16)
	// A busy session, then a one-shot alt-screen dialog drawn once.
	r.Append([]byte("$ ls -la\r\ntons of earlier output\r\n"))
	dialog := "\x1b[?1049h\x1b[2J\x1b[HTRUST-PROMPT cols=80 rows=24"
	r.Append([]byte(dialog))

	snap := r.ScreenSnapshot()

	if !bytes.HasPrefix(snap, []byte("\x1b[?1049h")) {
		t.Fatalf("snapshot must start at the alt-screen boundary; got prefix %q", head(snap, 12))
	}
	if !bytes.Contains(snap, []byte("TRUST-PROMPT cols=80 rows=24")) {
		t.Fatalf("snapshot must contain the dialog; got %q", snap)
	}
	if bytes.Contains(snap, []byte("earlier output")) {
		t.Fatalf("snapshot should not carry pre-clear scrollback; got %q", snap)
	}
}

// TestScreenSnapshotFallsBackToRaw confirms the heuristic is never worse
// than the plain ring: with no boundary, the full scrollback is returned.
func TestScreenSnapshotFallsBackToRaw(t *testing.T) {
	r := newRingBuffer(1 << 16)
	out := []byte("plain scrolling output\r\nno clears here\r\n")
	r.Append(out)
	if got := r.ScreenSnapshot(); !bytes.Equal(got, out) {
		t.Fatalf("expected full raw snapshot with no boundary; got %q", got)
	}
}

func head(b []byte, n int) []byte {
	if len(b) < n {
		return b
	}
	return b[:n]
}
