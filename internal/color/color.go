// Copyright (C) 2026 Techdelight BV

package color

import "os"

var disabled bool

// Init checks the NO_COLOR environment variable and disables colors if set.
func Init() {
	if os.Getenv("NO_COLOR") != "" {
		disabled = true
	}
}

// Disable turns off all color output.
func Disable() {
	disabled = true
}

// Enable turns on color output.
func Enable() {
	disabled = false
}

func Red(s string) string    { if disabled { return s }; return "\033[31m" + s + "\033[0m" }
func Green(s string) string  { if disabled { return s }; return "\033[32m" + s + "\033[0m" }
func Yellow(s string) string { if disabled { return s }; return "\033[33m" + s + "\033[0m" }
func Cyan(s string) string   { if disabled { return s }; return "\033[36m" + s + "\033[0m" }
func Bold(s string) string   { if disabled { return s }; return "\033[1m" + s + "\033[0m" }
func Dim(s string) string    { if disabled { return s }; return "\033[2m" + s + "\033[0m" }
