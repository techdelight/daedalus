// Copyright (C) 2026 Techdelight BV

package color

import (
	"strings"
	"testing"
)

func TestRed_Enabled(t *testing.T) {
	Enable()
	result := Red("error")
	if !strings.Contains(result, "\033[31m") {
		t.Errorf("expected ANSI red code, got %q", result)
	}
	if !strings.Contains(result, "error") {
		t.Errorf("expected 'error' in output, got %q", result)
	}
}

func TestRed_Disabled(t *testing.T) {
	Disable()
	defer Enable()
	result := Red("error")
	if strings.Contains(result, "\033[") {
		t.Errorf("expected no ANSI codes, got %q", result)
	}
	if result != "error" {
		t.Errorf("expected %q, got %q", "error", result)
	}
}

func TestGreen_Enabled(t *testing.T) {
	Enable()
	result := Green("ok")
	if !strings.Contains(result, "\033[32m") {
		t.Errorf("expected ANSI green code, got %q", result)
	}
}

func TestYellow_Enabled(t *testing.T) {
	Enable()
	result := Yellow("warn")
	if !strings.Contains(result, "\033[33m") {
		t.Errorf("expected ANSI yellow code, got %q", result)
	}
}

func TestCyan_Enabled(t *testing.T) {
	Enable()
	result := Cyan("hint")
	if !strings.Contains(result, "\033[36m") {
		t.Errorf("expected ANSI cyan code, got %q", result)
	}
}

func TestBold_Enabled(t *testing.T) {
	Enable()
	result := Bold("title")
	if !strings.Contains(result, "\033[1m") {
		t.Errorf("expected ANSI bold code, got %q", result)
	}
}

func TestDim_Enabled(t *testing.T) {
	Enable()
	result := Dim("dim")
	if !strings.Contains(result, "\033[2m") {
		t.Errorf("expected ANSI dim code, got %q", result)
	}
}

func TestAllColors_Disabled(t *testing.T) {
	Disable()
	defer Enable()

	tests := []struct {
		name string
		fn   func(string) string
	}{
		{"green", Green},
		{"yellow", Yellow},
		{"cyan", Cyan},
		{"bold", Bold},
		{"dim", Dim},
	}
	for _, tt := range tests {
		result := tt.fn("text")
		if strings.Contains(result, "\033[") {
			t.Errorf("%s: expected no ANSI codes when disabled, got %q", tt.name, result)
		}
		if result != "text" {
			t.Errorf("%s: expected %q, got %q", tt.name, "text", result)
		}
	}
}

func TestInit_NOCOLOREnv(t *testing.T) {
	Enable()
	t.Setenv("NO_COLOR", "1")
	Init()
	if !disabled {
		t.Error("disabled should be true when NO_COLOR env is set")
	}
	Enable() // reset
}
