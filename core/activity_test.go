// Copyright (C) 2026 Techdelight BV

package core

import (
	"encoding/json"
	"testing"
)

func TestActivityStateConstants(t *testing.T) {
	tests := []struct {
		state ActivityState
		want  string
	}{
		{ActivityBusy, "busy"},
		{ActivityIdle, "idle"},
		{ActivitySleeping, "sleeping"},
	}
	for _, tt := range tests {
		if string(tt.state) != tt.want {
			t.Errorf("got %q, want %q", tt.state, tt.want)
		}
	}
}

func TestActivityInfoJSON(t *testing.T) {
	info := ActivityInfo{
		State:     ActivityBusy,
		UpdatedAt: "2026-04-09T12:00:00Z",
		Detail:    "tool_use",
	}
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got ActivityInfo
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.State != ActivityBusy {
		t.Errorf("state: got %q, want %q", got.State, ActivityBusy)
	}
	if got.UpdatedAt != "2026-04-09T12:00:00Z" {
		t.Errorf("updatedAt: got %q", got.UpdatedAt)
	}
	if got.Detail != "tool_use" {
		t.Errorf("detail: got %q", got.Detail)
	}
}
