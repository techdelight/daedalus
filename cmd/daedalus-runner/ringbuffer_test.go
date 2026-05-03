// Copyright (C) 2026 Techdelight BV

package main

import (
	"bytes"
	"sync"
	"testing"
)

func TestRingBuffer_BelowCapacity(t *testing.T) {
	r := newRingBuffer(16)
	r.Append([]byte("hello"))
	r.Append([]byte(" world"))

	got := r.Snapshot()
	if !bytes.Equal(got, []byte("hello world")) {
		t.Errorf("Snapshot = %q, want %q", got, "hello world")
	}
}

func TestRingBuffer_ExactlyCapacity(t *testing.T) {
	r := newRingBuffer(11)
	r.Append([]byte("hello world"))

	got := r.Snapshot()
	if !bytes.Equal(got, []byte("hello world")) {
		t.Errorf("Snapshot = %q, want %q", got, "hello world")
	}
}

func TestRingBuffer_OverflowSingleAppend(t *testing.T) {
	r := newRingBuffer(5)
	r.Append([]byte("hello world"))

	got := r.Snapshot()
	if !bytes.Equal(got, []byte("world")) {
		t.Errorf("Snapshot = %q, want last 5 bytes %q", got, "world")
	}
}

func TestRingBuffer_OverflowAcrossAppends(t *testing.T) {
	r := newRingBuffer(5)
	r.Append([]byte("abc"))
	r.Append([]byte("defg"))

	got := r.Snapshot()
	if !bytes.Equal(got, []byte("cdefg")) {
		t.Errorf("Snapshot = %q, want %q", got, "cdefg")
	}
}

func TestRingBuffer_RepeatedOverflow(t *testing.T) {
	r := newRingBuffer(4)
	r.Append([]byte("aaa"))
	r.Append([]byte("bbb"))
	r.Append([]byte("ccc"))

	got := r.Snapshot()
	if !bytes.Equal(got, []byte("bccc")) {
		t.Errorf("Snapshot = %q, want %q", got, "bccc")
	}
}

func TestRingBuffer_ZeroCapacity(t *testing.T) {
	r := newRingBuffer(0)
	r.Append([]byte("anything"))

	if got := r.Snapshot(); got != nil {
		t.Errorf("Snapshot of zero-cap buffer = %v, want nil", got)
	}
}

func TestRingBuffer_EmptyAppend(t *testing.T) {
	r := newRingBuffer(8)
	r.Append(nil)
	r.Append([]byte{})
	r.Append([]byte("ok"))

	got := r.Snapshot()
	if !bytes.Equal(got, []byte("ok")) {
		t.Errorf("Snapshot = %q, want %q", got, "ok")
	}
}

func TestRingBuffer_SnapshotIsCopy(t *testing.T) {
	r := newRingBuffer(8)
	r.Append([]byte("hello"))
	snap := r.Snapshot()
	snap[0] = 'X' // mutate
	if got := r.Snapshot(); got[0] != 'h' {
		t.Errorf("internal buffer was mutated by caller")
	}
}

func TestRingBuffer_ConcurrentAppend(t *testing.T) {
	r := newRingBuffer(1024)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				r.Append([]byte("x"))
			}
		}()
	}
	wg.Wait()
	if got := len(r.Snapshot()); got != 800 {
		t.Errorf("Snapshot length = %d, want 800", got)
	}
}
