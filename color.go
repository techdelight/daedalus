// Copyright (C) 2026 Techdelight BV

package main

import "os"

var noColor bool

func initColor() {
	if os.Getenv("NO_COLOR") != "" {
		noColor = true
	}
}

func colorRed(s string) string    { if noColor { return s }; return "\033[31m" + s + "\033[0m" }
func colorGreen(s string) string  { if noColor { return s }; return "\033[32m" + s + "\033[0m" }
func colorYellow(s string) string { if noColor { return s }; return "\033[33m" + s + "\033[0m" }
func colorCyan(s string) string   { if noColor { return s }; return "\033[36m" + s + "\033[0m" }
func colorBold(s string) string   { if noColor { return s }; return "\033[1m" + s + "\033[0m" }
func colorDim(s string) string    { if noColor { return s }; return "\033[2m" + s + "\033[0m" }
