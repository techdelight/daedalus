// Copyright (C) 2026 Techdelight BV

package core

import (
	"testing"
)

func TestTopologicalSort_Linear(t *testing.T) {
	// Arrange
	projects := []string{"A", "B", "C"}
	edges := []DependencyEdge{
		{Upstream: "A", Downstream: "B"},
		{Upstream: "B", Downstream: "C"},
	}
	graph := NewDependencyGraph(projects, edges)

	// Act
	result, err := graph.TopologicalSort()

	// Assert
	if err != nil {
		t.Fatalf("TopologicalSort() returned error: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("TopologicalSort() returned %d items, want 3", len(result))
	}
	if result[0] != "A" || result[1] != "B" || result[2] != "C" {
		t.Errorf("TopologicalSort() = %v, want [A B C]", result)
	}
}

func TestTopologicalSort_Diamond(t *testing.T) {
	// Arrange
	projects := []string{"A", "B", "C", "D"}
	edges := []DependencyEdge{
		{Upstream: "A", Downstream: "B"},
		{Upstream: "A", Downstream: "C"},
		{Upstream: "B", Downstream: "D"},
		{Upstream: "C", Downstream: "D"},
	}
	graph := NewDependencyGraph(projects, edges)

	// Act
	result, err := graph.TopologicalSort()

	// Assert
	if err != nil {
		t.Fatalf("TopologicalSort() returned error: %v", err)
	}
	if len(result) != 4 {
		t.Fatalf("TopologicalSort() returned %d items, want 4", len(result))
	}
	if result[0] != "A" {
		t.Errorf("TopologicalSort()[0] = %q, want %q", result[0], "A")
	}
	if result[3] != "D" {
		t.Errorf("TopologicalSort()[3] = %q, want %q", result[3], "D")
	}
}

func TestTopologicalSort_NoDeps(t *testing.T) {
	// Arrange
	projects := []string{"X", "Y", "Z"}
	graph := NewDependencyGraph(projects, nil)

	// Act
	result, err := graph.TopologicalSort()

	// Assert
	if err != nil {
		t.Fatalf("TopologicalSort() returned error: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("TopologicalSort() returned %d items, want 3", len(result))
	}
	// All three projects must appear
	seen := make(map[string]bool)
	for _, p := range result {
		seen[p] = true
	}
	for _, p := range projects {
		if !seen[p] {
			t.Errorf("TopologicalSort() missing project %q", p)
		}
	}
}

func TestTopologicalSort_Cycle(t *testing.T) {
	// Arrange
	projects := []string{"A", "B", "C"}
	edges := []DependencyEdge{
		{Upstream: "A", Downstream: "B"},
		{Upstream: "B", Downstream: "C"},
		{Upstream: "C", Downstream: "A"},
	}
	graph := NewDependencyGraph(projects, edges)

	// Act
	_, err := graph.TopologicalSort()

	// Assert
	if err == nil {
		t.Error("TopologicalSort() = nil error, want cycle error")
	}
}

func TestDetectCycles_NoCycle(t *testing.T) {
	// Arrange
	projects := []string{"A", "B", "C"}
	edges := []DependencyEdge{
		{Upstream: "A", Downstream: "B"},
		{Upstream: "B", Downstream: "C"},
	}
	graph := NewDependencyGraph(projects, edges)

	// Act
	hasCycle := graph.DetectCycles()

	// Assert
	if hasCycle {
		t.Error("DetectCycles() = true, want false")
	}
}

func TestDetectCycles_WithCycle(t *testing.T) {
	// Arrange
	projects := []string{"A", "B", "C"}
	edges := []DependencyEdge{
		{Upstream: "A", Downstream: "B"},
		{Upstream: "B", Downstream: "C"},
		{Upstream: "C", Downstream: "A"},
	}
	graph := NewDependencyGraph(projects, edges)

	// Act
	hasCycle := graph.DetectCycles()

	// Assert
	if !hasCycle {
		t.Error("DetectCycles() = false, want true")
	}
}

func TestDownstreams(t *testing.T) {
	// Arrange
	projects := []string{"A", "B", "C", "D"}
	edges := []DependencyEdge{
		{Upstream: "A", Downstream: "B"},
		{Upstream: "A", Downstream: "C"},
		{Upstream: "B", Downstream: "D"},
	}
	graph := NewDependencyGraph(projects, edges)

	// Act
	result := graph.Downstreams("A")

	// Assert
	if len(result) != 2 {
		t.Fatalf("Downstreams(A) returned %d items, want 2", len(result))
	}
	if result[0] != "B" || result[1] != "C" {
		t.Errorf("Downstreams(A) = %v, want [B C]", result)
	}
}

func TestUpstreams(t *testing.T) {
	// Arrange
	projects := []string{"A", "B", "C", "D"}
	edges := []DependencyEdge{
		{Upstream: "A", Downstream: "D"},
		{Upstream: "B", Downstream: "D"},
		{Upstream: "C", Downstream: "D"},
	}
	graph := NewDependencyGraph(projects, edges)

	// Act
	result := graph.Upstreams("D")

	// Assert
	if len(result) != 3 {
		t.Fatalf("Upstreams(D) returned %d items, want 3", len(result))
	}
	if result[0] != "A" || result[1] != "B" || result[2] != "C" {
		t.Errorf("Upstreams(D) = %v, want [A B C]", result)
	}
}

func TestDownstreams_NoEdges(t *testing.T) {
	// Arrange
	projects := []string{"A", "B", "C"}
	graph := NewDependencyGraph(projects, nil)

	// Act
	result := graph.Downstreams("A")

	// Assert
	if result != nil {
		t.Errorf("Downstreams(A) = %v, want nil", result)
	}
}

func TestValidateProgrammeName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"my-programme", false},
		{"prog1", false},
		{"My.Programme", false},
		{"a", false},
		{"A", false},
		{"0start", false},
		{"test_programme", false},
		{"a-b.c_d", false},
		{"abc123", false},

		{"", true},
		{"-start", true},
		{".start", true},
		{"_start", true},
		{"has space", true},
		{"has/slash", true},
		{"has@sign", true},
		{"has:colon", true},
		{"has!bang", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProgrammeName(tt.name)
			if tt.wantErr && err == nil {
				t.Errorf("ValidateProgrammeName(%q) = nil, want error", tt.name)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidateProgrammeName(%q) = %v, want nil", tt.name, err)
			}
		})
	}
}
