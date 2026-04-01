// Copyright (C) 2026 Techdelight BV

package core

import "testing"

func TestForemanState_Constants(t *testing.T) {
	// Arrange
	tests := []struct {
		state ForemanState
		want  string
	}{
		{ForemanIdle, "idle"},
		{ForemanPlanning, "planning"},
		{ForemanMonitoring, "monitoring"},
		{ForemanReporting, "reporting"},
		{ForemanStopped, "stopped"},
	}

	for _, tt := range tests {
		// Act
		got := string(tt.state)

		// Assert
		if got != tt.want {
			t.Errorf("ForemanState = %q, want %q", got, tt.want)
		}
	}
}

func TestForemanConfig_ZeroValue(t *testing.T) {
	// Arrange / Act
	cfg := ForemanConfig{}

	// Assert
	if cfg.Programme != "" {
		t.Errorf("zero Programme = %q, want empty", cfg.Programme)
	}
	if cfg.PollSeconds != 0 {
		t.Errorf("zero PollSeconds = %d, want 0", cfg.PollSeconds)
	}
}

func TestForemanStatus_WithPlan(t *testing.T) {
	// Arrange
	plan := &ForemanPlan{
		Programme: "test-prog",
		ActiveProjects: []ForemanProject{
			{Name: "proj-a", ProgressPct: 50, AgentState: "running"},
		},
		Summary: "1 projects, 50% average progress",
	}

	// Act
	status := ForemanStatus{
		State:   ForemanMonitoring,
		Plan:    plan,
		Message: "monitoring test-prog",
	}

	// Assert
	if status.State != ForemanMonitoring {
		t.Errorf("State = %q, want %q", status.State, ForemanMonitoring)
	}
	if status.Plan.Programme != "test-prog" {
		t.Errorf("Plan.Programme = %q, want %q", status.Plan.Programme, "test-prog")
	}
	if len(status.Plan.ActiveProjects) != 1 {
		t.Fatalf("ActiveProjects len = %d, want 1", len(status.Plan.ActiveProjects))
	}
	if status.Plan.ActiveProjects[0].ProgressPct != 50 {
		t.Errorf("ProgressPct = %d, want 50", status.Plan.ActiveProjects[0].ProgressPct)
	}
}

func TestCascadeEventInfo_Fields(t *testing.T) {
	// Arrange
	event := CascadeEventInfo{
		Upstream:   "svc-a",
		Downstream: "svc-b",
		Action:     "propagate",
		Message:    "auto-propagate to svc-b",
	}

	// Assert
	if event.Upstream != "svc-a" {
		t.Errorf("Upstream = %q, want %q", event.Upstream, "svc-a")
	}
	if event.Downstream != "svc-b" {
		t.Errorf("Downstream = %q, want %q", event.Downstream, "svc-b")
	}
	if event.Action != "propagate" {
		t.Errorf("Action = %q, want %q", event.Action, "propagate")
	}
	if event.Message != "auto-propagate to svc-b" {
		t.Errorf("Message = %q, want %q", event.Message, "auto-propagate to svc-b")
	}
}

func TestForemanStatus_WithCascadeLog(t *testing.T) {
	// Arrange
	cascadeLog := []CascadeEventInfo{
		{Upstream: "A", Downstream: "B", Action: "propagate", Message: "auto"},
		{Upstream: "A", Downstream: "C", Action: "notify", Message: "notify"},
	}

	// Act
	status := ForemanStatus{
		State:      ForemanMonitoring,
		Message:    "monitoring",
		CascadeLog: cascadeLog,
	}

	// Assert
	if len(status.CascadeLog) != 2 {
		t.Fatalf("CascadeLog len = %d, want 2", len(status.CascadeLog))
	}
	if status.CascadeLog[0].Action != "propagate" {
		t.Errorf("CascadeLog[0].Action = %q, want %q", status.CascadeLog[0].Action, "propagate")
	}
	if status.CascadeLog[1].Downstream != "C" {
		t.Errorf("CascadeLog[1].Downstream = %q, want %q", status.CascadeLog[1].Downstream, "C")
	}
}

func TestForemanStatus_CascadeLogOmitted(t *testing.T) {
	// Arrange / Act
	status := ForemanStatus{
		State:   ForemanIdle,
		Message: "idle",
	}

	// Assert
	if status.CascadeLog != nil {
		t.Errorf("CascadeLog should be nil when not set, got %v", status.CascadeLog)
	}
}
