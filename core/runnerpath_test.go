// Copyright (C) 2026 Techdelight BV

package core

import "testing"

func TestUseRunner(t *testing.T) {
	tests := []struct {
		name       string
		useRunner  string // DAEDALUS_USE_RUNNER
		useTmux    string // DAEDALUS_USE_TMUX
		wantRunner bool
	}{
		{"default is runner", "", "", true},
		{"explicit runner on", "1", "", true},
		{"tmux opt-out wins", "1", "1", false},
		{"runner 0 opts out", "0", "", false},
		{"runner false opts out", "false", "", false},
		{"runner off opts out", "off", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DAEDALUS_USE_RUNNER", tc.useRunner)
			t.Setenv("DAEDALUS_USE_TMUX", tc.useTmux)
			if got := UseRunner(); got != tc.wantRunner {
				t.Errorf("UseRunner() = %v, want %v (USE_RUNNER=%q USE_TMUX=%q)", got, tc.wantRunner, tc.useRunner, tc.useTmux)
			}
		})
	}
}
