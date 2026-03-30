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
