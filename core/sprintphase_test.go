// Copyright (C) 2026 Techdelight BV

package core

import "testing"

func items(statuses ...SprintStatus) []SprintItem {
	out := make([]SprintItem, len(statuses))
	for i, st := range statuses {
		out[i] = SprintItem{Number: i + 1, Description: "x", Status: st}
	}
	return out
}

func TestPhaseOf(t *testing.T) {
	tests := []struct {
		name string
		s    Sprint
		want SprintPhase
	}{
		{"shipped when version set", Sprint{Version: "v0.40.0", Items: items(StatusDone)}, PhaseShipped},
		{"shipped even if items look incomplete", Sprint{Version: "v0.40.0", Items: items(StatusDone, StatusPending)}, PhaseShipped},
		{"proposed when no items", Sprint{}, PhaseProposed},
		{"proposed when all pending", Sprint{Items: items(StatusPending, StatusPending)}, PhaseProposed},
		{"ready when all done, no version", Sprint{Items: items(StatusDone, StatusDone)}, PhaseReady},
		{"building when mixed done/pending", Sprint{Items: items(StatusDone, StatusPending)}, PhaseBuilding},
		{"building when an item is in progress", Sprint{Items: items(StatusInProgress, StatusPending)}, PhaseBuilding},
		{"building when in progress after some done", Sprint{Items: items(StatusDone, StatusInProgress)}, PhaseBuilding},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := PhaseOf(tc.s); got != tc.want {
				t.Errorf("PhaseOf = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSprintProgress(t *testing.T) {
	done, total := SprintProgress(Sprint{Items: items(StatusDone, StatusDone, StatusPending, StatusInProgress)})
	if done != 2 || total != 4 {
		t.Errorf("SprintProgress = (%d, %d), want (2, 4)", done, total)
	}
	if d, tot := SprintProgress(Sprint{}); d != 0 || tot != 0 {
		t.Errorf("SprintProgress(empty) = (%d, %d), want (0, 0)", d, tot)
	}
}
