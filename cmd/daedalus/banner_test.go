// Copyright (C) 2026 Techdelight BV

package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/techdelight/daedalus/core"
)

func TestPrintBanner(t *testing.T) {
	// Arrange
	dir := t.TempDir()

	logoContent := "=== LOGO ==="
	os.WriteFile(filepath.Join(dir, "logo.txt"), []byte(logoContent+"\n"), 0644)

	old := core.Version
	defer func() { core.Version = old }()
	core.Version = "1.2.3"

	// Capture stdout.
	stdold := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Act
	printBanner(dir)

	w.Close()
	os.Stdout = stdold
	out, _ := io.ReadAll(r)
	output := string(out)

	// Assert
	if !strings.Contains(output, logoContent) {
		t.Errorf("expected logo %q in output:\n%s", logoContent, output)
	}
	if !strings.Contains(output, "Version: 1.2.3") {
		t.Errorf("expected 'Version: 1.2.3' in output:\n%s", output)
	}
	if !strings.Contains(output, "Build:") {
		t.Errorf("expected 'Build:' in output:\n%s", output)
	}
}
