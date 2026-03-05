// Copyright (C) 2026 Techdelight BV

package main

import (
	"strings"
	"testing"
)

func TestColorRed_Enabled(t *testing.T) {
	noColor = false
	result := colorRed("error")
	if !strings.Contains(result, "\033[31m") {
		t.Errorf("expected ANSI red code, got %q", result)
	}
	if !strings.Contains(result, "error") {
		t.Errorf("expected 'error' in output, got %q", result)
	}
}

func TestColorRed_Disabled(t *testing.T) {
	noColor = true
	defer func() { noColor = false }()
	result := colorRed("error")
	if strings.Contains(result, "\033[") {
		t.Errorf("expected no ANSI codes, got %q", result)
	}
	if result != "error" {
		t.Errorf("expected %q, got %q", "error", result)
	}
}

func TestColorGreen_Enabled(t *testing.T) {
	noColor = false
	result := colorGreen("ok")
	if !strings.Contains(result, "\033[32m") {
		t.Errorf("expected ANSI green code, got %q", result)
	}
}

func TestColorYellow_Enabled(t *testing.T) {
	noColor = false
	result := colorYellow("warn")
	if !strings.Contains(result, "\033[33m") {
		t.Errorf("expected ANSI yellow code, got %q", result)
	}
}

func TestColorCyan_Enabled(t *testing.T) {
	noColor = false
	result := colorCyan("hint")
	if !strings.Contains(result, "\033[36m") {
		t.Errorf("expected ANSI cyan code, got %q", result)
	}
}

func TestColorBold_Enabled(t *testing.T) {
	noColor = false
	result := colorBold("title")
	if !strings.Contains(result, "\033[1m") {
		t.Errorf("expected ANSI bold code, got %q", result)
	}
}

func TestColorDim_Enabled(t *testing.T) {
	noColor = false
	result := colorDim("dim")
	if !strings.Contains(result, "\033[2m") {
		t.Errorf("expected ANSI dim code, got %q", result)
	}
}

func TestAllColors_Disabled(t *testing.T) {
	noColor = true
	defer func() { noColor = false }()

	tests := []struct {
		name string
		fn   func(string) string
	}{
		{"green", colorGreen},
		{"yellow", colorYellow},
		{"cyan", colorCyan},
		{"bold", colorBold},
		{"dim", colorDim},
	}
	for _, tt := range tests {
		result := tt.fn("text")
		if strings.Contains(result, "\033[") {
			t.Errorf("%s: expected no ANSI codes when noColor=true, got %q", tt.name, result)
		}
		if result != "text" {
			t.Errorf("%s: expected %q, got %q", tt.name, "text", result)
		}
	}
}

func TestInitColor_NOCOLOREnv(t *testing.T) {
	noColor = false
	t.Setenv("NO_COLOR", "1")
	initColor()
	if !noColor {
		t.Error("noColor should be true when NO_COLOR env is set")
	}
	noColor = false // reset
}
