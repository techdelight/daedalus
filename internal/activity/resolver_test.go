// Copyright (C) 2026 Techdelight BV

package activity

import (
	"testing"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/agentstate"
)

type mockObserver struct {
	state agentstate.State
}

func (m *mockObserver) GetState(containerName string) agentstate.State {
	return m.state
}

type mockDetector struct {
	info core.ActivityInfo
}

func (m *mockDetector) Detect(projectDir string) core.ActivityInfo {
	return m.info
}

func TestResolver_ContainerNotRunning(t *testing.T) {
	tests := []agentstate.State{
		agentstate.StateStopped,
		agentstate.StateUnknown,
		agentstate.StateError,
	}
	for _, containerState := range tests {
		// Arrange
		r := NewResolver(
			&mockObserver{state: containerState},
			&mockDetector{info: core.ActivityInfo{State: core.ActivityBusy}},
		)

		// Act
		info := r.Resolve("claude-run-test", "/tmp/test")

		// Assert — non-running containers are sleeping regardless of detector
		if info.State != core.ActivitySleeping {
			t.Errorf("container %q: got %q, want sleeping", containerState, info.State)
		}
	}
}

func TestResolver_ContainerRunning_DelegatesToDetector(t *testing.T) {
	tests := []struct {
		name     string
		detected core.ActivityInfo
	}{
		{"busy", core.ActivityInfo{State: core.ActivityBusy, Detail: "tool_use"}},
		{"idle", core.ActivityInfo{State: core.ActivityIdle, Detail: "waiting"}},
	}
	for _, tt := range tests {
		// Arrange
		r := NewResolver(
			&mockObserver{state: agentstate.StateRunning},
			&mockDetector{info: tt.detected},
		)

		// Act
		info := r.Resolve("claude-run-test", "/tmp/test")

		// Assert
		if info.State != tt.detected.State {
			t.Errorf("%s: got %q, want %q", tt.name, info.State, tt.detected.State)
		}
		if info.Detail != tt.detected.Detail {
			t.Errorf("%s detail: got %q, want %q", tt.name, info.Detail, tt.detected.Detail)
		}
	}
}
