// Copyright (C) 2026 Techdelight BV

package platform

import (
	"testing"
)

func TestDisplayArgs(t *testing.T) {
	tests := []struct {
		name             string
		display          string
		waylandDisplay   string
		xdgRuntimeDir   string
		wantArgCount     int
		wantWarnCount    int
		wantArgs         []string
		wantWarnContains string
	}{
		{
			name:             "no display set",
			display:          "",
			waylandDisplay:   "",
			xdgRuntimeDir:   "",
			wantArgCount:     0,
			wantWarnCount:    1,
			wantWarnContains: "neither DISPLAY nor WAYLAND_DISPLAY is set",
		},
		{
			name:           "X11 only",
			display:        ":0",
			waylandDisplay: "",
			xdgRuntimeDir:  "",
			wantArgCount:   4,
			wantWarnCount:  0,
			wantArgs:       []string{"-v", "/tmp/.X11-unix:/tmp/.X11-unix", "-e", "DISPLAY=:0"},
		},
		{
			name:           "Wayland only",
			display:        "",
			waylandDisplay: "wayland-0",
			xdgRuntimeDir:  "/run/user/1000",
			wantArgCount:   6,
			wantWarnCount:  0,
			wantArgs: []string{
				"-v", "/run/user/1000/wayland-0:/tmp/wayland-0",
				"-e", "WAYLAND_DISPLAY=wayland-0",
				"-e", "XDG_RUNTIME_DIR=/tmp",
			},
		},
		{
			name:           "both X11 and Wayland",
			display:        ":0",
			waylandDisplay: "wayland-0",
			xdgRuntimeDir:  "/run/user/1000",
			wantArgCount:   10,
			wantWarnCount:  0,
			wantArgs: []string{
				"-v", "/tmp/.X11-unix:/tmp/.X11-unix",
				"-e", "DISPLAY=:0",
				"-v", "/run/user/1000/wayland-0:/tmp/wayland-0",
				"-e", "WAYLAND_DISPLAY=wayland-0",
				"-e", "XDG_RUNTIME_DIR=/tmp",
			},
		},
		{
			name:             "Wayland without XDG_RUNTIME_DIR",
			display:          "",
			waylandDisplay:   "wayland-0",
			xdgRuntimeDir:   "",
			wantArgCount:     0,
			wantWarnCount:    1,
			wantWarnContains: "WAYLAND_DISPLAY is set but XDG_RUNTIME_DIR is empty",
		},
		{
			name:           "X11 with custom display",
			display:        "192.168.1.100:0",
			waylandDisplay: "",
			xdgRuntimeDir:  "",
			wantArgCount:   4,
			wantWarnCount:  0,
			wantArgs:       []string{"-v", "/tmp/.X11-unix:/tmp/.X11-unix", "-e", "DISPLAY=192.168.1.100:0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			display := tt.display
			waylandDisplay := tt.waylandDisplay
			xdgRuntimeDir := tt.xdgRuntimeDir

			// Act
			args, warnings := DisplayArgs(display, waylandDisplay, xdgRuntimeDir)

			// Assert — argument count
			if len(args) != tt.wantArgCount {
				t.Errorf("got %d args, want %d; args=%v", len(args), tt.wantArgCount, args)
			}

			// Assert — warning count
			if len(warnings) != tt.wantWarnCount {
				t.Errorf("got %d warnings, want %d; warnings=%v", len(warnings), tt.wantWarnCount, warnings)
			}

			// Assert — exact args match
			if tt.wantArgs != nil {
				if len(args) != len(tt.wantArgs) {
					t.Fatalf("arg length mismatch: got %v, want %v", args, tt.wantArgs)
				}
				for i, want := range tt.wantArgs {
					if args[i] != want {
						t.Errorf("args[%d] = %q, want %q", i, args[i], want)
					}
				}
			}

			// Assert — warning message contains expected substring
			if tt.wantWarnContains != "" && len(warnings) > 0 {
				found := false
				for _, w := range warnings {
					if containsSubstring(w, tt.wantWarnContains) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("no warning contains %q; warnings=%v", tt.wantWarnContains, warnings)
				}
			}
		})
	}
}

// containsSubstring returns true if s contains substr.
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

// searchSubstring is a simple substring search without importing strings.
func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
