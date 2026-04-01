// Copyright (C) 2026 Techdelight BV

package session

import (
	"testing"
)

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
