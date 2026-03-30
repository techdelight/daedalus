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
