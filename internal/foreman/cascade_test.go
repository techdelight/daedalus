// Copyright (C) 2026 Techdelight BV

package foreman

import (
	"testing"

	"github.com/techdelight/daedalus/core"
)

func TestEvaluateCascade_AutoStrategy(t *testing.T) {
	// Arrange
	prog := core.Programme{
		Name:     "test-prog",
		Projects: []string{"A", "B"},
		Deps: []core.DependencyEdge{
			{Upstream: "A", Downstream: "B", Strategy: core.CascadeAuto},
		},
	}

	// Act
	result := EvaluateCascade(prog, "A", false)

	// Assert
	if len(result.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result.Events))
	}
	if result.Events[0].Action != "propagate" {
		t.Errorf("expected action %q, got %q", "propagate", result.Events[0].Action)
	}
	if result.Events[0].Strategy != core.CascadeAuto {
		t.Errorf("expected strategy %q, got %q", core.CascadeAuto, result.Events[0].Strategy)
	}
}

func TestEvaluateCascade_NotifyStrategy(t *testing.T) {
	// Arrange
	prog := core.Programme{
		Name:     "test-prog",
		Projects: []string{"A", "B"},
		Deps: []core.DependencyEdge{
			{Upstream: "A", Downstream: "B", Strategy: core.CascadeNotify},
		},
	}

	// Act
	result := EvaluateCascade(prog, "A", false)

	// Assert
	if len(result.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result.Events))
	}
	if result.Events[0].Action != "notify" {
		t.Errorf("expected action %q, got %q", "notify", result.Events[0].Action)
	}
	if result.Events[0].Strategy != core.CascadeNotify {
		t.Errorf("expected strategy %q, got %q", core.CascadeNotify, result.Events[0].Strategy)
	}
}

func TestEvaluateCascade_ManualStrategy(t *testing.T) {
	// Arrange
	prog := core.Programme{
		Name:     "test-prog",
		Projects: []string{"A", "B"},
		Deps: []core.DependencyEdge{
			{Upstream: "A", Downstream: "B", Strategy: core.CascadeManual},
		},
	}

	// Act
	result := EvaluateCascade(prog, "A", false)

	// Assert
	if len(result.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result.Events))
	}
	if result.Events[0].Action != "skip" {
		t.Errorf("expected action %q, got %q", "skip", result.Events[0].Action)
	}
	if result.Events[0].Strategy != core.CascadeManual {
		t.Errorf("expected strategy %q, got %q", core.CascadeManual, result.Events[0].Strategy)
	}
}

func TestEvaluateCascade_DefaultStrategy(t *testing.T) {
	// Arrange
	prog := core.Programme{
		Name:     "test-prog",
		Projects: []string{"A", "B"},
		Deps: []core.DependencyEdge{
			{Upstream: "A", Downstream: "B"}, // no strategy set
		},
	}

	// Act
	result := EvaluateCascade(prog, "A", false)

	// Assert
	if len(result.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result.Events))
	}
	if result.Events[0].Action != "notify" {
		t.Errorf("expected action %q, got %q", "notify", result.Events[0].Action)
	}
	if result.Events[0].Strategy != core.CascadeNotify {
		t.Errorf("expected strategy %q, got %q", core.CascadeNotify, result.Events[0].Strategy)
	}
}

func TestEvaluateCascade_MultipleDownstreams(t *testing.T) {
	// Arrange
	prog := core.Programme{
		Name:     "test-prog",
		Projects: []string{"A", "B", "C"},
		Deps: []core.DependencyEdge{
			{Upstream: "A", Downstream: "B", Strategy: core.CascadeAuto},
			{Upstream: "A", Downstream: "C", Strategy: core.CascadeNotify},
		},
	}

	// Act
	result := EvaluateCascade(prog, "A", false)

	// Assert
	if len(result.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(result.Events))
	}
	if result.Events[0].Downstream != "B" {
		t.Errorf("expected first downstream %q, got %q", "B", result.Events[0].Downstream)
	}
	if result.Events[1].Downstream != "C" {
		t.Errorf("expected second downstream %q, got %q", "C", result.Events[1].Downstream)
	}
}

func TestEvaluateCascade_NoDownstreams(t *testing.T) {
	// Arrange
	prog := core.Programme{
		Name:     "test-prog",
		Projects: []string{"A", "B"},
		Deps: []core.DependencyEdge{
			{Upstream: "A", Downstream: "B"},
		},
	}

	// Act
	result := EvaluateCascade(prog, "B", false) // B is a leaf

	// Assert
	if len(result.Events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(result.Events))
	}
	if result.Programme != "test-prog" {
		t.Errorf("expected programme %q, got %q", "test-prog", result.Programme)
	}
}

func TestEvaluateCascade_DryRun(t *testing.T) {
	// Arrange
	prog := core.Programme{
		Name:     "test-prog",
		Projects: []string{"A", "B"},
		Deps: []core.DependencyEdge{
			{Upstream: "A", Downstream: "B", Strategy: core.CascadeAuto},
		},
	}

	// Act
	result := EvaluateCascade(prog, "A", true)

	// Assert
	if !result.DryRun {
		t.Error("expected DryRun to be true")
	}
	if len(result.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result.Events))
	}
}
