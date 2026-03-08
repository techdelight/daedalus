// Copyright (C) 2026 Techdelight BV

package core

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadVersion(t *testing.T) {
	old := Version
	defer func() { Version = old }()

	Version = "3.2.1"
	if got := ReadVersion(); got != "3.2.1" {
		t.Errorf("ReadVersion() = %q, want %q", got, "3.2.1")
	}
}

func TestPrintBanner(t *testing.T) {
	dir := t.TempDir()

	logoContent := "=== LOGO ==="
	os.WriteFile(filepath.Join(dir, "logo.txt"), []byte(logoContent+"\n"), 0644)

	old := Version
	defer func() { Version = old }()
	Version = "1.2.3"

	// Capture stdout.
	stdold := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	PrintBanner(dir)

	w.Close()
	os.Stdout = stdold
	out, _ := io.ReadAll(r)
	output := string(out)

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
