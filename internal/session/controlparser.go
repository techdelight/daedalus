// Copyright (C) 2026 Techdelight BV

package session

import "strings"

// ControlMessageType identifies the kind of tmux control mode message.
type ControlMessageType int

const (
	// MsgUnknown is an unrecognised message.
	MsgUnknown ControlMessageType = iota
	// MsgOutput carries pane output data (%output pane-id data).
	MsgOutput
	// MsgBegin marks the start of a command response (%begin cmd-id flags).
	MsgBegin
	// MsgEnd marks the end of a command response (%end cmd-id code).
	MsgEnd
	// MsgError signals a command failure (%error cmd-id message).
	MsgError
	// MsgLayoutChange signals a window resize (%layout-change win-id layout w,h).
	MsgLayoutChange
	// MsgSessionChanged signals a session change (%session-changed id name).
	MsgSessionChanged
	// MsgWindowRenamed signals a window rename (%window-renamed win-id name).
	MsgWindowRenamed
	// MsgPaneModeChanged signals a pane mode change (%pane-mode-changed pane-id).
	MsgPaneModeChanged
)

// ControlMessage is a parsed tmux control mode message.
type ControlMessage struct {
	Type    ControlMessageType
	PaneID  string // for MsgOutput
	CmdID   string // for MsgBegin, MsgEnd, MsgError
	Code    string // exit code for MsgEnd ("0" = success)
	Content string // output data, error message, or raw remainder
}

// ParseControlLine parses a single line from tmux control mode output.
// Lines that don't start with '%' are treated as command response body
// and returned as MsgUnknown with the full line in Content.
func ParseControlLine(line string) ControlMessage {
	if !strings.HasPrefix(line, "%") {
		return ControlMessage{Type: MsgUnknown, Content: line}
	}

	// Split: %type field1 field2 ...rest
	parts := strings.SplitN(line, " ", 3)
	msgType := parts[0]

	switch msgType {
	case "%output":
		if len(parts) < 2 {
			return ControlMessage{Type: MsgOutput, Content: line}
		}
		content := ""
		if len(parts) >= 3 {
			content = parts[2]
		}
		return ControlMessage{
			Type:    MsgOutput,
			PaneID:  parts[1],
			Content: content,
		}

	case "%begin":
		if len(parts) < 2 {
			return ControlMessage{Type: MsgBegin, Content: line}
		}
		cmdID := parts[1]
		return ControlMessage{
			Type:  MsgBegin,
			CmdID: cmdID,
		}

	case "%end":
		if len(parts) < 3 {
			return ControlMessage{Type: MsgEnd, Content: line}
		}
		return ControlMessage{
			Type:  MsgEnd,
			CmdID: parts[1],
			Code:  parts[2],
		}

	case "%error":
		if len(parts) < 3 {
			return ControlMessage{Type: MsgError, Content: line}
		}
		return ControlMessage{
			Type:    MsgError,
			CmdID:   parts[1],
			Content: parts[2],
		}

	case "%layout-change":
		content := ""
		if len(parts) >= 3 {
			content = parts[2]
		}
		return ControlMessage{
			Type:    MsgLayoutChange,
			PaneID:  safeIndex(parts, 1),
			Content: content,
		}

	case "%session-changed":
		return ControlMessage{
			Type:    MsgSessionChanged,
			Content: strings.TrimPrefix(line, "%session-changed "),
		}

	case "%window-renamed":
		return ControlMessage{
			Type:    MsgWindowRenamed,
			PaneID:  safeIndex(parts, 1),
			Content: safeIndex(parts, 2),
		}

	case "%pane-mode-changed":
		return ControlMessage{
			Type:   MsgPaneModeChanged,
			PaneID: safeIndex(parts, 1),
		}

	default:
		return ControlMessage{Type: MsgUnknown, Content: line}
	}
}

func safeIndex(parts []string, i int) string {
	if i < len(parts) {
		return parts[i]
	}
	return ""
}

// UnescapeOutput decodes tmux control mode octal escapes in %output data.
// tmux escapes bytes as \OOO (three-digit octal) and backslashes as \\.
func UnescapeOutput(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) {
			if s[i+1] == '\\' {
				b.WriteByte('\\')
				i += 2
				continue
			}
			// Octal escape: \OOO (exactly 3 digits)
			if i+3 < len(s) && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
				val := (s[i+1]-'0')*64 + (s[i+2]-'0')*8 + (s[i+3] - '0')
				b.WriteByte(val)
				i += 4
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func isOctal(c byte) bool {
	return c >= '0' && c <= '7'
}
