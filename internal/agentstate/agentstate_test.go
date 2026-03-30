// Copyright (C) 2026 Techdelight BV

package agentstate

import (
	"fmt"
	"testing"

	"github.com/techdelight/daedalus/internal/executor"
)

func TestContainerObserver_Running(t *testing.T) {
	// Arrange
	mock := executor.NewMockExecutor()
	mock.Results["docker"] = executor.MockResult{Output: "running\n", Err: nil}
	observer := NewContainerObserver(mock)

	// Act
	state := observer.GetState("claude-run-myproject")

	// Assert
	if state != StateRunning {
		t.Errorf("expected StateRunning, got %q", state)
	}
}

func TestContainerObserver_DockerError(t *testing.T) {
	// Arrange
	mock := executor.NewMockExecutor()
	mock.Results["docker"] = executor.MockResult{Output: "", Err: fmt.Errorf("no such container")}
	observer := NewContainerObserver(mock)

	// Act
	state := observer.GetState("claude-run-myproject")

	// Assert — docker errors return Unknown, not Stopped, to avoid masking failures
	if state != StateUnknown {
		t.Errorf("expected StateUnknown, got %q", state)
	}
}

func TestContainerObserver_Exited(t *testing.T) {
	// Arrange
	mock := executor.NewMockExecutor()
	mock.Results["docker"] = executor.MockResult{Output: "exited\n", Err: nil}
	observer := NewContainerObserver(mock)

	// Act
	state := observer.GetState("claude-run-myproject")

	// Assert
	if state != StateStopped {
		t.Errorf("expected StateStopped, got %q", state)
	}
}

func TestStateConstants(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{StateUnknown, "unknown"},
		{StateIdle, "idle"},
		{StateRunning, "running"},
		{StateStopped, "stopped"},
		{StateError, "error"},
	}
	for _, tc := range tests {
		if string(tc.state) != tc.want {
			t.Errorf("State %v = %q, want %q", tc.state, string(tc.state), tc.want)
		}
	}
}

func TestContainerObserver_Paused(t *testing.T) {
	mock := executor.NewMockExecutor()
	mock.Results["docker"] = executor.MockResult{Output: "paused\n", Err: nil}
	observer := NewContainerObserver(mock)

	state := observer.GetState("claude-run-myproject")

	if state != StateIdle {
		t.Errorf("expected StateIdle for paused container, got %q", state)
	}
}

func TestContainerObserver_UnknownStatus(t *testing.T) {
	mock := executor.NewMockExecutor()
	mock.Results["docker"] = executor.MockResult{Output: "restarting\n", Err: nil}
	observer := NewContainerObserver(mock)

	state := observer.GetState("claude-run-myproject")

	if state != StateUnknown {
		t.Errorf("expected StateUnknown for unrecognized status, got %q", state)
	}
}
