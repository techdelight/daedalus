// Copyright (C) 2026 Techdelight BV

package core

// ForemanState represents the Foreman's lifecycle state.
type ForemanState string

const (
	ForemanIdle       ForemanState = "idle"
	ForemanPlanning   ForemanState = "planning"
	ForemanMonitoring ForemanState = "monitoring"
	ForemanReporting  ForemanState = "reporting"
	ForemanStopped    ForemanState = "stopped"
)

// ForemanConfig holds configuration for the Foreman agent.
type ForemanConfig struct {
	Programme   string `json:"programme"`             // programme name to manage
	PollSeconds int    `json:"pollSeconds,omitempty"` // monitoring poll interval (default: 30)
}

// ForemanPlan represents the Foreman's current work plan.
type ForemanPlan struct {
	Programme      string           `json:"programme"`
	ActiveProjects []ForemanProject `json:"activeProjects"`
	Summary        string           `json:"summary,omitempty"`
}

// ForemanProject tracks a single project within the Foreman's plan.
type ForemanProject struct {
	Name          string  `json:"name"`
	ProgressPct   int     `json:"progressPct"`
	CurrentSprint *Sprint `json:"currentSprint,omitempty"`
	AgentState    string  `json:"agentState"`
}

// CascadeEventInfo is a summary of a cascade event for API responses.
type CascadeEventInfo struct {
	Upstream   string `json:"upstream"`
	Downstream string `json:"downstream"`
	Action     string `json:"action"`
	Message    string `json:"message"`
}

// ForemanStatus is a snapshot of the Foreman's state for API responses.
type ForemanStatus struct {
	State      ForemanState       `json:"state"`
	Plan       *ForemanPlan       `json:"plan,omitempty"`
	Message    string             `json:"message,omitempty"`
	CascadeLog []CascadeEventInfo `json:"cascadeLog,omitempty"`
}
