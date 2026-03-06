// Copyright (C) 2026 Techdelight BV

package session

import (
	"errors"
	"testing"

	"github.com/techdelight/daedalus/internal/executor"
)

func TestSessionExists_True(t *testing.T) {
	mock := executor.NewMockExecutor()
	session := NewSession(mock, "claude-test")

	if !session.Exists() {
		t.Error("Exists() = false, want true")
	}

	call := mock.FindCall("tmux")
	if call == nil {
		t.Fatal("expected tmux call")
	}
	expected := []string{"has-session", "-t", "claude-test"}
	for i, a := range expected {
		if call.Args[i] != a {
			t.Errorf("arg[%d] = %q, want %q", i, call.Args[i], a)
		}
	}
}

func TestSessionExists_False(t *testing.T) {
	mock := executor.NewMockExecutor()
	mock.Results["tmux"] = executor.MockResult{Err: errors.New("exit 1")}

	session := NewSession(mock, "claude-test")

	if session.Exists() {
		t.Error("Exists() = true, want false")
	}
}

func TestSessionCreate(t *testing.T) {
	mock := executor.NewMockExecutor()
	session := NewSession(mock, "claude-test")

	err := session.Create()
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	call := mock.FindCall("tmux")
	if call == nil {
		t.Fatal("expected tmux call")
	}
	expected := []string{"new-session", "-d", "-s", "claude-test"}
	for i, a := range expected {
		if call.Args[i] != a {
			t.Errorf("arg[%d] = %q, want %q", i, call.Args[i], a)
		}
	}
}

func TestSessionSendKeys(t *testing.T) {
	mock := executor.NewMockExecutor()
	session := NewSession(mock, "claude-test")

	err := session.SendKeys("echo hello")
	if err != nil {
		t.Fatalf("SendKeys failed: %v", err)
	}

	calls := mock.FindCalls("tmux")
	if len(calls) != 1 {
		t.Fatalf("expected 1 tmux call, got %d", len(calls))
	}
	call := calls[0]
	expected := []string{"send-keys", "-t", "claude-test", "echo hello", "Enter"}
	if len(call.Args) != len(expected) {
		t.Fatalf("args len = %d, want %d", len(call.Args), len(expected))
	}
	for i, a := range expected {
		if call.Args[i] != a {
			t.Errorf("arg[%d] = %q, want %q", i, call.Args[i], a)
		}
	}
}

func TestSessionAttach(t *testing.T) {
	mock := executor.NewMockExecutor()
	session := NewSession(mock, "claude-test")

	err := session.Attach()
	if err != nil {
		t.Fatalf("Attach failed: %v", err)
	}

	call := mock.FindCall("exec:tmux")
	if call == nil {
		t.Fatal("expected exec:tmux call")
	}
	expected := []string{"attach-session", "-t", "claude-test"}
	for i, a := range expected {
		if call.Args[i] != a {
			t.Errorf("arg[%d] = %q, want %q", i, call.Args[i], a)
		}
	}
}

func TestTmuxAvailable_True(t *testing.T) {
	mock := executor.NewMockExecutor()
	if !TmuxAvailable(mock) {
		t.Error("TmuxAvailable = false, want true")
	}
}

func TestTmuxAvailable_False(t *testing.T) {
	mock := executor.NewMockExecutor()
	mock.Results["lookpath:tmux"] = executor.MockResult{Err: errors.New("not found")}

	if TmuxAvailable(mock) {
		t.Error("TmuxAvailable = true, want false")
	}
}
