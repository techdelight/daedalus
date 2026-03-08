// Copyright (C) 2026 Techdelight BV

package core

import "testing"

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
