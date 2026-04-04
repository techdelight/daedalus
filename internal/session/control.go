// Copyright (C) 2026 Techdelight BV

package session

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// ControlSession manages a tmux session via control mode (-C).
// It provides structured message I/O instead of raw PTY bytes.
type ControlSession struct {
	name   string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	mu     sync.Mutex // guards stdin writes
}

// NewControlSession creates a ControlSession from pre-built pipes.
// Used by tests to inject mock I/O without spawning a real tmux process.
func NewControlSession(name string, stdin io.WriteCloser, stdout io.Reader, cmd *exec.Cmd) *ControlSession {
	return &ControlSession{
		name:   name,
		cmd:    cmd,
		stdin:  stdin,
		reader: bufio.NewReader(stdout),
	}
}

// StartControlSession spawns `tmux -C attach-session -t <name>` and returns
// a ControlSession for sending commands and reading messages.
// The caller must call Close() when done.
func StartControlSession(name string) (*ControlSession, error) {
	cmd := exec.Command("tmux", "-C", "attach-session", "-t", name)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("creating stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		return nil, fmt.Errorf("starting tmux control mode: %w", err)
	}

	return &ControlSession{
		name:   name,
		cmd:    cmd,
		stdin:  stdin,
		reader: bufio.NewReader(stdout),
	}, nil
}

// ReadMessage reads and parses the next control mode message.
// Blocks until a line is available or the connection closes.
func (cs *ControlSession) ReadMessage() (ControlMessage, error) {
	line, err := cs.reader.ReadString('\n')
	if err != nil {
		return ControlMessage{}, err
	}
	line = strings.TrimRight(line, "\r\n")
	return ParseControlLine(line), nil
}

// SendCommand sends a raw tmux command string.
// The command is written to stdin followed by a newline.
func (cs *ControlSession) SendCommand(command string) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	_, err := fmt.Fprintf(cs.stdin, "%s\n", command)
	return err
}

// SendKeys sends keystrokes to the session's pane.
func (cs *ControlSession) SendKeys(keys string) error {
	return cs.SendCommand(fmt.Sprintf("send-keys -t %s %s", cs.name, ShellQuote(keys)))
}

// CapturePane requests the last n lines of pane scrollback.
// Returns the captured content by reading messages until %end.
func (cs *ControlSession) CapturePane(lines int) (string, error) {
	cmd := fmt.Sprintf("capture-pane -t %s -p -e -S -%d", cs.name, lines)
	if err := cs.SendCommand(cmd); err != nil {
		return "", fmt.Errorf("sending capture-pane: %w", err)
	}
	return cs.readCommandResponse()
}

// CaptureVisible captures only the currently visible pane content.
func (cs *ControlSession) CaptureVisible() (string, error) {
	cmd := fmt.Sprintf("capture-pane -t %s -p -e", cs.name)
	if err := cs.SendCommand(cmd); err != nil {
		return "", fmt.Errorf("sending capture-pane: %w", err)
	}
	return cs.readCommandResponse()
}

// ResizeWindow resizes the session window.
func (cs *ControlSession) ResizeWindow(cols, rows int) error {
	return cs.SendCommand(fmt.Sprintf("resize-window -t %s -x %d -y %d", cs.name, cols, rows))
}

// Close terminates the control mode connection and waits for tmux to exit.
func (cs *ControlSession) Close() error {
	cs.mu.Lock()
	cs.stdin.Close()
	cs.mu.Unlock()
	if cs.cmd != nil {
		return cs.cmd.Wait()
	}
	return nil
}

// readCommandResponse reads lines until a %end or %error message, collecting
// content lines between %begin and %end as the response body.
func (cs *ControlSession) readCommandResponse() (string, error) {
	var lines []string
	inResponse := false

	for {
		msg, err := cs.ReadMessage()
		if err != nil {
			return "", fmt.Errorf("reading response: %w", err)
		}

		switch msg.Type {
		case MsgBegin:
			inResponse = true
		case MsgEnd:
			return strings.Join(lines, "\r\n"), nil
		case MsgError:
			return "", fmt.Errorf("tmux error: %s", msg.Content)
		case MsgOutput:
			// Ignore pane output during command response
		default:
			if inResponse {
				lines = append(lines, msg.Content)
			}
		}
	}
}

// ShellQuote wraps a string in single quotes for tmux send-keys.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
