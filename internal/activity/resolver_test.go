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
		reg := NewDetectorRegistry()
		reg.Register("claude", &mockDetector{info: core.ActivityInfo{State: core.ActivityBusy}})
		r := NewResolver(&mockObserver{state: containerState}, reg)

		// Act
		info := r.Resolve("claude-run-test", "/tmp/test", "claude")

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
		reg := NewDetectorRegistry()
		reg.Register("claude", &mockDetector{info: tt.detected})
		r := NewResolver(&mockObserver{state: agentstate.StateRunning}, reg)

		// Act
		info := r.Resolve("claude-run-test", "/tmp/test", "claude")

		// Assert
		if info.State != tt.detected.State {
			t.Errorf("%s: got %q, want %q", tt.name, info.State, tt.detected.State)
		}
		if info.Detail != tt.detected.Detail {
			t.Errorf("%s detail: got %q, want %q", tt.name, info.Detail, tt.detected.Detail)
		}
	}
}

func TestResolver_SelectsCorrectRunnerDetector(t *testing.T) {
	// Arrange
	reg := NewDetectorRegistry()
	reg.Register("claude", &mockDetector{info: core.ActivityInfo{State: core.ActivityBusy, Detail: "claude"}})
	reg.Register("copilot", &mockDetector{info: core.ActivityInfo{State: core.ActivityIdle, Detail: "copilot"}})
	r := NewResolver(&mockObserver{state: agentstate.StateRunning}, reg)

	// Act + Assert — claude runner
	claudeInfo := r.Resolve("claude-run-test", "/tmp/test", "claude")
	if claudeInfo.Detail != "claude" {
		t.Errorf("claude: got detail %q, want %q", claudeInfo.Detail, "claude")
	}

	// Act + Assert — copilot runner
	copilotInfo := r.Resolve("claude-run-test", "/tmp/test", "copilot")
	if copilotInfo.Detail != "copilot" {
		t.Errorf("copilot: got detail %q, want %q", copilotInfo.Detail, "copilot")
	}
}

func TestResolver_UnknownRunnerFallsBack(t *testing.T) {
	// Arrange
	reg := NewDetectorRegistry()
	reg.Register("claude", &mockDetector{info: core.ActivityInfo{State: core.ActivityBusy}})
	r := NewResolver(&mockObserver{state: agentstate.StateRunning}, reg)

	// Act — unknown runner
	info := r.Resolve("claude-run-test", "/tmp/test", "unknown")

	// Assert — NullDetector returns idle
	if info.State != core.ActivityIdle {
		t.Errorf("unknown runner: got %q, want %q", info.State, core.ActivityIdle)
	}
}
