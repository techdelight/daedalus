// Copyright (C) 2026 Techdelight BV

package foreman

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/agentstate"
	"github.com/techdelight/daedalus/internal/mcpclient"
	"github.com/techdelight/daedalus/internal/programme"
	"github.com/techdelight/daedalus/internal/registry"
)

// stubObserver is a test double for AgentObserver.
type stubObserver struct {
	states map[string]agentstate.State
}

func (o *stubObserver) IsActive(containerName string) bool {
	return o.GetState(containerName) == agentstate.StateRunning
}

func (o *stubObserver) GetState(containerName string) agentstate.State {
	if s, ok := o.states[containerName]; ok {
		return s
	}
	return agentstate.StateUnknown
}

// testDeps creates test dependencies with a programme and registry.
func testDeps(t *testing.T, progName string, projects []string) (*programme.Store, *registry.Registry, *mcpclient.Client, *stubObserver) {
	t.Helper()
	tmpDir := t.TempDir()

	// Programme store
	progDir := filepath.Join(tmpDir, "programmes")
	if err := os.MkdirAll(progDir, 0755); err != nil {
		t.Fatal(err)
	}
	progStore := programme.New(progDir)
	prog := core.Programme{
		Name:     progName,
		Projects: projects,
	}
	if err := progStore.Create(prog); err != nil {
		t.Fatalf("creating programme: %v", err)
	}

	// Registry
	regPath := filepath.Join(tmpDir, "registry.json")
	reg := registry.NewRegistry(regPath)
	if err := reg.Init(); err != nil {
		t.Fatalf("init registry: %v", err)
	}

	// Register projects with directories
	for _, p := range projects {
		projDir := filepath.Join(tmpDir, "projects", p)
		if err := os.MkdirAll(projDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := reg.AddProject(p, projDir, "dev"); err != nil {
			t.Fatalf("add project %s: %v", p, err)
		}
	}

	client := mcpclient.New()
	observer := &stubObserver{states: make(map[string]agentstate.State)}

	return progStore, reg, client, observer
}

func TestForeman_StartStop(t *testing.T) {
	// Arrange
	progStore, reg, client, observer := testDeps(t, "test-prog", []string{"proj-a", "proj-b"})
	cfg := core.ForemanConfig{
		Programme:   "test-prog",
		PollSeconds: 1,
	}
	f := New(cfg, progStore, reg, client, observer)

	// Act - start
	err := f.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for planning to complete
	time.Sleep(200 * time.Millisecond)

	// Assert - state should be monitoring after planning
	status := f.Status()
	if status.State != core.ForemanMonitoring {
		t.Errorf("state after start = %q, want %q", status.State, core.ForemanMonitoring)
	}

	// Act - stop
	f.Stop()

	// Assert - state should be stopped
	status = f.Status()
	if status.State != core.ForemanStopped {
		t.Errorf("state after stop = %q, want %q", status.State, core.ForemanStopped)
	}
}

func TestForeman_StartWhileRunning(t *testing.T) {
	// Arrange
	progStore, reg, client, observer := testDeps(t, "test-prog", []string{"proj-a"})
	cfg := core.ForemanConfig{
		Programme:   "test-prog",
		PollSeconds: 1,
	}
	f := New(cfg, progStore, reg, client, observer)
	if err := f.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer f.Stop()
	time.Sleep(200 * time.Millisecond)

	// Act
	err := f.Start()

	// Assert
	if err == nil {
		t.Error("expected error starting already-running foreman")
	}
}

func TestForeman_Status_Idle(t *testing.T) {
	// Arrange
	progStore, reg, client, observer := testDeps(t, "test-prog", []string{})
	cfg := core.ForemanConfig{Programme: "test-prog"}
	f := New(cfg, progStore, reg, client, observer)

	// Act
	status := f.Status()

	// Assert
	if status.State != core.ForemanIdle {
		t.Errorf("State = %q, want %q", status.State, core.ForemanIdle)
	}
	if status.Plan != nil {
		t.Error("Plan should be nil before start")
	}
}

func TestPlanner_BuildPlan(t *testing.T) {
	// Arrange
	progStore, reg, client, _ := testDeps(t, "my-prog", []string{"svc-a", "svc-b"})
	planner := NewPlanner(progStore, reg, client)

	// Act
	plan, err := planner.BuildPlan("my-prog")

	// Assert
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.Programme != "my-prog" {
		t.Errorf("Programme = %q, want %q", plan.Programme, "my-prog")
	}
	if len(plan.ActiveProjects) != 2 {
		t.Fatalf("ActiveProjects len = %d, want 2", len(plan.ActiveProjects))
	}
	if plan.ActiveProjects[0].Name != "svc-a" {
		t.Errorf("ActiveProjects[0].Name = %q, want %q", plan.ActiveProjects[0].Name, "svc-a")
	}
	if plan.ActiveProjects[1].Name != "svc-b" {
		t.Errorf("ActiveProjects[1].Name = %q, want %q", plan.ActiveProjects[1].Name, "svc-b")
	}
	if plan.Summary != "2 projects, 0% average progress" {
		t.Errorf("Summary = %q, want %q", plan.Summary, "2 projects, 0% average progress")
	}
}

func TestPlanner_BuildPlan_UnknownProgramme(t *testing.T) {
	// Arrange
	progStore, reg, client, _ := testDeps(t, "my-prog", []string{})
	planner := NewPlanner(progStore, reg, client)

	// Act
	_, err := planner.BuildPlan("nonexistent")

	// Assert
	if err == nil {
		t.Error("expected error for unknown programme")
	}
}

func TestPlanner_BuildPlan_EmptyProgramme(t *testing.T) {
	// Arrange
	progStore, reg, client, _ := testDeps(t, "empty-prog", []string{})
	planner := NewPlanner(progStore, reg, client)

	// Act
	plan, err := planner.BuildPlan("empty-prog")

	// Assert
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.Summary != "no projects in programme" {
		t.Errorf("Summary = %q, want %q", plan.Summary, "no projects in programme")
	}
}

func TestMonitor_UpdatePlan(t *testing.T) {
	// Arrange
	_, reg, client, observer := testDeps(t, "test-prog", []string{"proj-a"})
	observer.states["claude-run-proj-a"] = agentstate.StateRunning
	monitor := NewMonitor(reg, client, observer)
	plan := &core.ForemanPlan{
		Programme: "test-prog",
		ActiveProjects: []core.ForemanProject{
			{Name: "proj-a", AgentState: "unknown"},
		},
	}

	// Act
	updated, err := monitor.UpdatePlan(plan)

	// Assert
	if err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}
	if len(updated.ActiveProjects) != 1 {
		t.Fatalf("ActiveProjects len = %d, want 1", len(updated.ActiveProjects))
	}
	if updated.ActiveProjects[0].AgentState != "running" {
		t.Errorf("AgentState = %q, want %q", updated.ActiveProjects[0].AgentState, "running")
	}
	if updated.Summary != "1 projects, 0% avg progress, 1 running" {
		t.Errorf("Summary = %q, want %q", updated.Summary, "1 projects, 0% avg progress, 1 running")
	}
}
