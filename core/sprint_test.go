// Copyright (C) 2026 Techdelight BV

package core

import "testing"

func TestSprintStatusConstants(t *testing.T) {
	// Arrange
	expectedPending := SprintStatus("")
	expectedDone := SprintStatus("Done")
	expectedInProgress := SprintStatus("In Progress")

	// Act & Assert
	if StatusPending != expectedPending {
		t.Errorf("StatusPending = %q, want %q", StatusPending, expectedPending)
	}
	if StatusDone != expectedDone {
		t.Errorf("StatusDone = %q, want %q", StatusDone, expectedDone)
	}
	if StatusInProgress != expectedInProgress {
		t.Errorf("StatusInProgress = %q, want %q", StatusInProgress, expectedInProgress)
	}
}
