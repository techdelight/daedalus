// Copyright (C) 2026 Techdelight BV

package core

import (
	"encoding/json"
	"testing"
)

func TestValidateProjectName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"my-app", false},
		{"app1", false},
		{"My.Project", false},
		{"a", false},
		{"A", false},
		{"0start", false},
		{"test_project", false},
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
			err := ValidateProjectName(tt.name)
			if tt.wantErr && err == nil {
				t.Errorf("ValidateProjectName(%q) = nil, want error", tt.name)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidateProjectName(%q) = %v, want nil", tt.name, err)
			}
		})
	}
}

func TestProjectEntry_MarshalWithProgressFields(t *testing.T) {
	// Arrange
	entry := ProjectEntry{
		Directory:      "/tmp/my-app",
		Target:         "dev",
		Created:        "2026-01-01T00:00:00Z",
		LastUsed:       "2026-03-29T12:00:00Z",
		ProgressPct:    42,
		Vision:         "Build the best CLI tool",
		ProjectVersion: "1.2.3",
	}

	// Act
	b, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var roundtrip ProjectEntry
	if err := json.Unmarshal(b, &roundtrip); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Assert
	if roundtrip.ProgressPct != 42 {
		t.Errorf("ProgressPct = %d, want 42", roundtrip.ProgressPct)
	}
	if roundtrip.Vision != "Build the best CLI tool" {
		t.Errorf("Vision = %q, want %q", roundtrip.Vision, "Build the best CLI tool")
	}
	if roundtrip.ProjectVersion != "1.2.3" {
		t.Errorf("ProjectVersion = %q, want %q", roundtrip.ProjectVersion, "1.2.3")
	}
}

func TestProjectEntry_MarshalOmitsZeroValueProgressFields(t *testing.T) {
	// Arrange
	entry := ProjectEntry{
		Directory: "/tmp/my-app",
		Target:    "dev",
		Created:   "2026-01-01T00:00:00Z",
		LastUsed:  "2026-03-29T12:00:00Z",
	}

	// Act
	b, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Assert — zero-valued fields must not appear in JSON
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("Unmarshal to map failed: %v", err)
	}
	for _, key := range []string{"progressPct", "vision", "projectVersion"} {
		if _, present := raw[key]; present {
			t.Errorf("expected %q to be omitted from JSON, but it was present", key)
		}
	}
}
