// Copyright (C) 2026 Techdelight BV

package runproto

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

func TestRoundtrip_Output(t *testing.T) {
	in := NewOutput([]byte{0x1b, '[', '3', '1', 'm', 'h', 'i'})

	var buf bytes.Buffer
	if err := NewEncoder(&buf).Encode(in); err != nil {
		t.Fatalf("Encode: %v", err)
	}

	out, err := NewDecoder(&buf).Decode()
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if out.Type != TypeOutput {
		t.Errorf("Type = %q, want %q", out.Type, TypeOutput)
	}
	if !bytes.Equal(out.Data, in.Data) {
		t.Errorf("Data = %x, want %x", out.Data, in.Data)
	}
}

func TestRoundtrip_Hello(t *testing.T) {
	scroll := []byte("previous output\r\n")
	in := NewHello(scroll, 120, 40)

	var buf bytes.Buffer
	if err := NewEncoder(&buf).Encode(in); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out, err := NewDecoder(&buf).Decode()
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if out.Type != TypeHello {
		t.Errorf("Type = %q, want %q", out.Type, TypeHello)
	}
	if !bytes.Equal(out.Scrollback, scroll) {
		t.Errorf("Scrollback = %q, want %q", out.Scrollback, scroll)
	}
	if out.Cols != 120 || out.Rows != 40 {
		t.Errorf("size = (%d,%d), want (120,40)", out.Cols, out.Rows)
	}
}

func TestRoundtrip_Exit_ZeroCode(t *testing.T) {
	// Exit code 0 is the success case. omitempty on a non-pointer int
	// would drop it from the JSON; using *int preserves the zero.
	in := NewExit(0)

	var buf bytes.Buffer
	if err := NewEncoder(&buf).Encode(in); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out, err := NewDecoder(&buf).Decode()
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if out.Code == nil {
		t.Fatal("Code is nil; should be a pointer to 0")
	}
	if *out.Code != 0 {
		t.Errorf("*Code = %d, want 0", *out.Code)
	}
}

func TestRoundtrip_Exit_NonZeroCode(t *testing.T) {
	in := NewExit(42)
	var buf bytes.Buffer
	if err := NewEncoder(&buf).Encode(in); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out, err := NewDecoder(&buf).Decode()
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if out.Code == nil || *out.Code != 42 {
		t.Errorf("Code = %v, want pointer to 42", out.Code)
	}
}

func TestRoundtrip_RunnerEvent(t *testing.T) {
	in := NewRunnerEvent("tool_use", map[string]any{"name": "Read", "path": "/etc/hosts"})

	var buf bytes.Buffer
	if err := NewEncoder(&buf).Encode(in); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out, err := NewDecoder(&buf).Decode()
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if out.Event != "tool_use" {
		t.Errorf("Event = %q, want %q", out.Event, "tool_use")
	}
	if out.EventData["name"] != "Read" || out.EventData["path"] != "/etc/hosts" {
		t.Errorf("EventData = %v", out.EventData)
	}
}

func TestRoundtrip_InputResizeDetach(t *testing.T) {
	frames := []Message{
		NewInput([]byte("ls\r")),
		NewResize(80, 24),
		NewDetach(),
	}

	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	for _, f := range frames {
		if err := enc.Encode(f); err != nil {
			t.Fatalf("Encode %s: %v", f.Type, err)
		}
	}

	dec := NewDecoder(&buf)
	for i, want := range frames {
		got, err := dec.Decode()
		if err != nil {
			t.Fatalf("Decode #%d: %v", i, err)
		}
		if got.Type != want.Type {
			t.Errorf("frame %d: Type = %q, want %q", i, got.Type, want.Type)
		}
	}
}

func TestNewlineFraming(t *testing.T) {
	// The wire format is one JSON value per line. Verify the bytes look
	// like that — a downstream tool parsing line-by-line should work.
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	for i := 0; i < 3; i++ {
		if err := enc.Encode(NewOutput([]byte{byte('a' + i)})); err != nil {
			t.Fatal(err)
		}
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3:\n%s", len(lines), buf.String())
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, `{"t":"output"`) {
			t.Errorf("line %d does not look like a single JSON message: %q", i, line)
		}
	}
}

func TestDecoder_EOF(t *testing.T) {
	dec := NewDecoder(strings.NewReader(""))
	if _, err := dec.Decode(); !errors.Is(err, io.EOF) {
		t.Errorf("err = %v, want io.EOF", err)
	}
}

func TestDecoder_Malformed(t *testing.T) {
	dec := NewDecoder(strings.NewReader("not json\n"))
	_, err := dec.Decode()
	if err == nil {
		t.Fatal("Decode of malformed input returned nil error")
	}
	if errors.Is(err, io.EOF) {
		t.Errorf("err is io.EOF for malformed input, want a JSON syntax error")
	}
}

func TestEncoder_ConcurrentEncode(t *testing.T) {
	// Verify that concurrent Encode calls don't interleave bytes within
	// a single frame. After all writes finish, every line in the buffer
	// must parse as one Message.
	var buf bytes.Buffer
	enc := NewEncoder(&buf)

	const goroutines = 16
	const perGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			payload := []byte{byte('A' + id%26)}
			for i := 0; i < perGoroutine; i++ {
				if err := enc.Encode(NewOutput(payload)); err != nil {
					t.Errorf("goroutine %d: Encode: %v", id, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	dec := NewDecoder(&buf)
	count := 0
	for {
		_, err := dec.Decode()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Decode after %d frames: %v", count, err)
		}
		count++
	}
	if want := goroutines * perGoroutine; count != want {
		t.Errorf("decoded %d frames, want %d", count, want)
	}
}

func TestRoundtrip_EmptyOutput(t *testing.T) {
	// Output with empty Data is still a valid frame (e.g. keep-alive
	// or framing test). encoding/json renders []byte{} as "" which is
	// fine; nil Data marshals as omitted.
	in := NewOutput([]byte{})

	var buf bytes.Buffer
	if err := NewEncoder(&buf).Encode(in); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out, err := NewDecoder(&buf).Decode()
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if out.Type != TypeOutput {
		t.Errorf("Type = %q, want %q", out.Type, TypeOutput)
	}
}
