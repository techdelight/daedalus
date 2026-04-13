// Copyright (C) 2026 Techdelight BV

package activity

import (
	"testing"

	"github.com/techdelight/daedalus/core"
)

func TestDetectorRegistry_RegisterAndGet(t *testing.T) {
	// Arrange
	reg := NewDetectorRegistry()
	mock := &mockDetector{info: core.ActivityInfo{State: core.ActivityBusy, Detail: "test"}}
	reg.Register("claude", mock)

	// Act
	got := reg.Get("claude")

	// Assert
	info := got.Detect("/tmp")
	if info.State != core.ActivityBusy {
		t.Errorf("got %q, want %q", info.State, core.ActivityBusy)
	}
}

func TestDetectorRegistry_UnknownFallsBackToNull(t *testing.T) {
	// Arrange
	reg := NewDetectorRegistry()
	reg.Register("claude", &mockDetector{info: core.ActivityInfo{State: core.ActivityBusy}})

	// Act — request unknown runner
	got := reg.Get("unknown-runner")

	// Assert — should get NullDetector (idle)
	info := got.Detect("/tmp")
	if info.State != core.ActivityIdle {
		t.Errorf("got %q, want %q for unknown runner", info.State, core.ActivityIdle)
	}
}

func TestDetectorRegistry_MultipleRunners(t *testing.T) {
	// Arrange
	reg := NewDetectorRegistry()
	reg.Register("claude", &mockDetector{info: core.ActivityInfo{State: core.ActivityBusy, Detail: "claude-busy"}})
	reg.Register("copilot", &mockDetector{info: core.ActivityInfo{State: core.ActivityIdle, Detail: "copilot-idle"}})

	// Act + Assert — each runner returns its own detector
	claudeInfo := reg.Get("claude").Detect("/tmp")
	if claudeInfo.Detail != "claude-busy" {
		t.Errorf("claude detail: got %q, want %q", claudeInfo.Detail, "claude-busy")
	}

	copilotInfo := reg.Get("copilot").Detect("/tmp")
	if copilotInfo.Detail != "copilot-idle" {
		t.Errorf("copilot detail: got %q, want %q", copilotInfo.Detail, "copilot-idle")
	}
}
