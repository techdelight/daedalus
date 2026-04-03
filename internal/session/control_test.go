// Copyright (C) 2026 Techdelight BV

package session

import (
	"io"
	"strings"
	"testing"
)

type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }

// --- Parser Tests ---

func TestParseControlLine_Output(t *testing.T) {
	msg := ParseControlLine("%output %42 hello world")
	if msg.Type != MsgOutput {
		t.Fatalf("Type = %v, want MsgOutput", msg.Type)
	}
	if msg.PaneID != "%42" {
		t.Errorf("PaneID = %q, want %%42", msg.PaneID)
	}
	if msg.Content != "hello world" {
		t.Errorf("Content = %q, want %q", msg.Content, "hello world")
	}
}

func TestParseControlLine_OutputMinimal(t *testing.T) {
	msg := ParseControlLine("%output %0")
	if msg.Type != MsgOutput {
		t.Fatalf("Type = %v, want MsgOutput", msg.Type)
	}
	// No content — pane ID only
	if msg.PaneID != "%0" {
		t.Errorf("PaneID = %q", msg.PaneID)
	}
}

func TestParseControlLine_Begin(t *testing.T) {
	msg := ParseControlLine("%begin 1234567890 1 0")
	if msg.Type != MsgBegin {
		t.Fatalf("Type = %v, want MsgBegin", msg.Type)
	}
	if msg.CmdID != "1234567890" {
		t.Errorf("CmdID = %q, want %q", msg.CmdID, "1234567890")
	}
}

func TestParseControlLine_End(t *testing.T) {
	msg := ParseControlLine("%end 1234567890 0")
	if msg.Type != MsgEnd {
		t.Fatalf("Type = %v, want MsgEnd", msg.Type)
	}
	if msg.CmdID != "1234567890" {
		t.Errorf("CmdID = %q", msg.CmdID)
	}
	if msg.Code != "0" {
		t.Errorf("Code = %q, want %q", msg.Code, "0")
	}
}

func TestParseControlLine_EndNonZero(t *testing.T) {
	msg := ParseControlLine("%end 999 1")
	if msg.Code != "1" {
		t.Errorf("Code = %q, want %q", msg.Code, "1")
	}
}

func TestParseControlLine_Error(t *testing.T) {
	msg := ParseControlLine("%error 555 session not found")
	if msg.Type != MsgError {
		t.Fatalf("Type = %v, want MsgError", msg.Type)
	}
	if msg.CmdID != "555" {
		t.Errorf("CmdID = %q", msg.CmdID)
	}
	if msg.Content != "session not found" {
		t.Errorf("Content = %q, want %q", msg.Content, "session not found")
	}
}

func TestParseControlLine_LayoutChange(t *testing.T) {
	msg := ParseControlLine("%layout-change @1 abc1 120,40")
	if msg.Type != MsgLayoutChange {
		t.Fatalf("Type = %v, want MsgLayoutChange", msg.Type)
	}
	if msg.PaneID != "@1" {
		t.Errorf("PaneID = %q", msg.PaneID)
	}
	// Content is the remainder after window ID (layout + dimensions)
	if msg.Content != "abc1 120,40" {
		t.Errorf("Content = %q, want %q", msg.Content, "abc1 120,40")
	}
}

func TestParseControlLine_SessionChanged(t *testing.T) {
	msg := ParseControlLine("%session-changed $1 my-session")
	if msg.Type != MsgSessionChanged {
		t.Fatalf("Type = %v, want MsgSessionChanged", msg.Type)
	}
	if msg.Content != "$1 my-session" {
		t.Errorf("Content = %q", msg.Content)
	}
}

func TestParseControlLine_WindowRenamed(t *testing.T) {
	msg := ParseControlLine("%window-renamed @0 new-name")
	if msg.Type != MsgWindowRenamed {
		t.Fatalf("Type = %v, want MsgWindowRenamed", msg.Type)
	}
	if msg.PaneID != "@0" {
		t.Errorf("PaneID = %q", msg.PaneID)
	}
	if msg.Content != "new-name" {
		t.Errorf("Content = %q", msg.Content)
	}
}

func TestParseControlLine_PaneModeChanged(t *testing.T) {
	msg := ParseControlLine("%pane-mode-changed %0")
	if msg.Type != MsgPaneModeChanged {
		t.Fatalf("Type = %v, want MsgPaneModeChanged", msg.Type)
	}
	if msg.PaneID != "%0" {
		t.Errorf("PaneID = %q", msg.PaneID)
	}
}

func TestParseControlLine_Unknown(t *testing.T) {
	msg := ParseControlLine("%something-new data here")
	if msg.Type != MsgUnknown {
		t.Fatalf("Type = %v, want MsgUnknown", msg.Type)
	}
	if msg.Content != "%something-new data here" {
		t.Errorf("Content = %q", msg.Content)
	}
}

func TestParseControlLine_NoPercent(t *testing.T) {
	msg := ParseControlLine("regular output line")
	if msg.Type != MsgUnknown {
		t.Fatalf("Type = %v, want MsgUnknown", msg.Type)
	}
	if msg.Content != "regular output line" {
		t.Errorf("Content = %q", msg.Content)
	}
}

func TestParseControlLine_EmptyString(t *testing.T) {
	msg := ParseControlLine("")
	if msg.Type != MsgUnknown {
		t.Fatalf("Type = %v, want MsgUnknown", msg.Type)
	}
	if msg.Content != "" {
		t.Errorf("Content = %q, want empty", msg.Content)
	}
}

func TestParseControlLine_PercentOnly(t *testing.T) {
	msg := ParseControlLine("%")
	if msg.Type != MsgUnknown {
		t.Fatalf("Type = %v, want MsgUnknown for bare %%", msg.Type)
	}
}

func TestParseControlLine_OutputWithSpecialChars(t *testing.T) {
	msg := ParseControlLine(`%output %0 \033[31mred text\033[0m`)
	if msg.Type != MsgOutput {
		t.Fatalf("Type = %v, want MsgOutput", msg.Type)
	}
	if msg.Content != `\033[31mred text\033[0m` {
		t.Errorf("Content = %q", msg.Content)
	}
}

func TestParseControlLine_OutputWithSpaces(t *testing.T) {
	msg := ParseControlLine("%output %5 line with   multiple   spaces")
	if msg.Content != "line with   multiple   spaces" {
		t.Errorf("Content = %q, want preserved spaces", msg.Content)
	}
}

// --- ControlMessage Type Table Test ---

func TestParseControlLine_AllTypes(t *testing.T) {
	tests := []struct {
		line string
		want ControlMessageType
	}{
		{"%output %0 data", MsgOutput},
		{"%begin 1 0", MsgBegin},
		{"%end 1 0", MsgEnd},
		{"%error 1 fail", MsgError},
		{"%layout-change @0 layout 80,24", MsgLayoutChange},
		{"%session-changed $0 name", MsgSessionChanged},
		{"%window-renamed @0 name", MsgWindowRenamed},
		{"%pane-mode-changed %0", MsgPaneModeChanged},
		{"%unknown-type", MsgUnknown},
		{"plain text", MsgUnknown},
		{"", MsgUnknown},
	}
	for _, tc := range tests {
		msg := ParseControlLine(tc.line)
		if msg.Type != tc.want {
			t.Errorf("ParseControlLine(%q).Type = %v, want %v", tc.line, msg.Type, tc.want)
		}
	}
}

// --- shellQuote tests ---

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "'hello'"},
		{"hello world", "'hello world'"},
		{"it's", "'it'\\''s'"},
		{"", "''"},
	}
	for _, tc := range tests {
		got := shellQuote(tc.input)
		if got != tc.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// --- ControlSession tests (using mock pipes) ---

func TestControlSession_ReadMessage(t *testing.T) {
	r, w := io.Pipe()
	cs := NewControlSession("test", nopWriteCloser{}, r, nil)

	go func() {
		w.Write([]byte("%output %0 hello\n"))
		w.Close()
	}()

	msg, err := cs.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage() error = %v", err)
	}
	if msg.Type != MsgOutput {
		t.Errorf("Type = %v, want MsgOutput", msg.Type)
	}
	if msg.Content != "hello" {
		t.Errorf("Content = %q, want %q", msg.Content, "hello")
	}
}

func TestControlSession_SendCommand(t *testing.T) {
	r, w := io.Pipe()
	cs := NewControlSession("test", w, strings.NewReader(""), nil)

	go func() {
		buf := make([]byte, 256)
		n, _ := r.Read(buf)
		got := string(buf[:n])
		if got != "list-windows\n" {
			t.Errorf("sent = %q, want %q", got, "list-windows\n")
		}
		r.Close()
	}()

	if err := cs.SendCommand("list-windows"); err != nil {
		t.Fatalf("SendCommand() error = %v", err)
	}
}

func TestControlSession_SendKeys(t *testing.T) {
	r, w := io.Pipe()
	cs := NewControlSession("mysess", w, strings.NewReader(""), nil)

	go func() {
		buf := make([]byte, 256)
		n, _ := r.Read(buf)
		got := string(buf[:n])
		if !strings.Contains(got, "send-keys -t mysess") {
			t.Errorf("sent = %q, want to contain 'send-keys -t mysess'", got)
		}
		r.Close()
	}()

	if err := cs.SendKeys("ls"); err != nil {
		t.Fatalf("SendKeys() error = %v", err)
	}
}

func TestControlSession_CapturePane(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	cs := NewControlSession("test", stdinW, stdoutR, nil)

	// Consume the command sent to stdin
	go func() {
		buf := make([]byte, 512)
		stdinR.Read(buf) // capture-pane command
		stdinR.Close()
	}()

	// Simulate tmux response
	go func() {
		stdoutW.Write([]byte("%begin 123 0\n"))
		stdoutW.Write([]byte("line one\n"))
		stdoutW.Write([]byte("line two\n"))
		stdoutW.Write([]byte("%end 123 0\n"))
		stdoutW.Close()
	}()

	content, err := cs.CapturePane(100)
	if err != nil {
		t.Fatalf("CapturePane() error = %v", err)
	}
	if content != "line one\nline two" {
		t.Errorf("CapturePane() = %q, want %q", content, "line one\nline two")
	}
}

func TestControlSession_CapturePane_Error(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	cs := NewControlSession("test", stdinW, stdoutR, nil)

	go func() {
		buf := make([]byte, 512)
		stdinR.Read(buf)
		stdinR.Close()
	}()

	go func() {
		stdoutW.Write([]byte("%error 123 session not found\n"))
		stdoutW.Close()
	}()

	_, err := cs.CapturePane(100)
	if err == nil {
		t.Fatal("CapturePane() expected error")
	}
	if !strings.Contains(err.Error(), "session not found") {
		t.Errorf("error = %v, want 'session not found'", err)
	}
}

func TestControlSession_CaptureVisible(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	cs := NewControlSession("test", stdinW, stdoutR, nil)

	go func() {
		buf := make([]byte, 512)
		n, _ := stdinR.Read(buf)
		cmd := string(buf[:n])
		// CaptureVisible should NOT include -S flag (no scrollback depth)
		if strings.Contains(cmd, "-S") {
			t.Errorf("CaptureVisible command includes -S flag: %s", cmd)
		}
		stdinR.Close()
	}()

	go func() {
		stdoutW.Write([]byte("%begin 123 0\n"))
		stdoutW.Write([]byte("visible content\n"))
		stdoutW.Write([]byte("%end 123 0\n"))
		stdoutW.Close()
	}()

	content, err := cs.CaptureVisible()
	if err != nil {
		t.Fatalf("CaptureVisible() error = %v", err)
	}
	if !strings.Contains(content, "visible content") {
		t.Errorf("content = %q, want 'visible content'", content)
	}
}

func TestControlSession_CaptureVisible_Error(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	cs := NewControlSession("test", stdinW, stdoutR, nil)

	go func() {
		buf := make([]byte, 512)
		stdinR.Read(buf)
		stdinR.Close()
	}()

	go func() {
		stdoutW.Write([]byte("%error 123 pane not found\n"))
		stdoutW.Close()
	}()

	_, err := cs.CaptureVisible()
	if err == nil {
		t.Fatal("CaptureVisible() expected error")
	}
	if !strings.Contains(err.Error(), "pane not found") {
		t.Errorf("error = %v, want 'pane not found'", err)
	}
}

func TestControlSession_Close_NilCmd(t *testing.T) {
	cs := NewControlSession("test", nopWriteCloser{}, strings.NewReader(""), nil)
	if err := cs.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

// --- UnescapeOutput tests ---

func TestUnescapeOutput(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// ESC (octal 033 = 0x1B)
		{`\033[32m`, "\x1b[32m"},
		// CR (octal 015 = 0x0D)
		{`\015`, "\r"},
		// LF (octal 012 = 0x0A)
		{`\012`, "\n"},
		// Combined: ESC + text + CR + LF
		{`\033[1mhello\033[0m\015\012`, "\x1b[1mhello\x1b[0m\r\n"},
		// Escaped backslash
		{`hello\\world`, `hello\world`},
		// No escapes
		{"plain text", "plain text"},
		// Empty
		{"", ""},
		// Octal for space (040 = 0x20)
		{`\040`, " "},
		// Multiple consecutive escapes
		{`\033\033`, "\x1b\x1b"},
		// Partial escape at end (not 3 digits) — left as-is
		{`\03`, `\03`},
		// Backslash followed by non-octal
		{`\n`, `\n`},
		// Real tmux output sample
		{`\033[32m●\033[1C\033[39m\033[1mWrite\033[22m`, "\x1b[32m●\x1b[1C\x1b[39m\x1b[1mWrite\x1b[22m"},
	}
	for _, tc := range tests {
		got := UnescapeOutput(tc.input)
		if got != tc.want {
			t.Errorf("UnescapeOutput(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// --- safeIndex tests ---

func TestSafeIndex(t *testing.T) {
	parts := []string{"a", "b", "c"}
	if safeIndex(parts, 0) != "a" {
		t.Error("safeIndex(0) failed")
	}
	if safeIndex(parts, 2) != "c" {
		t.Error("safeIndex(2) failed")
	}
	if safeIndex(parts, 5) != "" {
		t.Error("safeIndex(5) should return empty")
	}
	if safeIndex(nil, 0) != "" {
		t.Error("safeIndex(nil, 0) should return empty")
	}
}
