// Copyright (C) 2026 Techdelight BV

package core

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrintBanner(t *testing.T) {
	dir := t.TempDir()

	logoContent := "=== LOGO ==="
	os.WriteFile(filepath.Join(dir, "logo.txt"), []byte(logoContent+"\n"), 0644)
	os.WriteFile(filepath.Join(dir, "VERSION"), []byte("1.2.3\n"), 0644)

	// Capture stdout.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	PrintBanner(dir)

	w.Close()
	os.Stdout = old
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
