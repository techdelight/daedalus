// Copyright (C) 2026 Techdelight BV

package main

import "fmt"

// Session manages a tmux session.
type Session struct {
	Executor Executor
	Name     string
}

// NewSession creates a Session with the given executor and session name.
func NewSession(exec Executor, name string) *Session {
	return &Session{Executor: exec, Name: name}
}

// Exists returns true if the tmux session already exists.
func (s *Session) Exists() bool {
	err := s.Executor.Run("tmux", "has-session", "-t", s.Name)
	return err == nil
}

// Create creates a new detached tmux session.
func (s *Session) Create() error {
	return s.Executor.Run("tmux", "new-session", "-d", "-s", s.Name)
}

// SendKeys sends a command string to the tmux session.
func (s *Session) SendKeys(cmd string) error {
	return s.Executor.Run("tmux", "send-keys", "-t", s.Name, cmd, "Enter")
}

// Attach replaces the current process with tmux attach-session.
func (s *Session) Attach() error {
	return s.Executor.Exec("tmux", "attach-session", "-t", s.Name)
}

// TmuxAvailable checks if tmux is installed.
func TmuxAvailable(exec Executor) bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// PrintAttachHint prints the reattach hint to stdout.
func (s *Session) PrintAttachHint(binaryName string) {
	fmt.Printf("Starting tmux session '%s'...\n", s.Name)
	fmt.Printf("  (Detach with Ctrl-B d, reattach with: %s %s)\n", binaryName, s.Name)
}
