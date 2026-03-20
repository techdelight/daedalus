// Copyright (C) 2026 Techdelight BV

package main

import (
	"strings"
	"testing"
)

func TestGenerateManpage_StartsWithTH(t *testing.T) {
	// Arrange
	version := "0.8.2"
	date := "2026-03-07"

	// Act
	output := generateManpage(version, date)

	// Assert
	if !strings.HasPrefix(output, ".TH DAEDALUS 1") {
		t.Errorf("man page should start with .TH header, got: %q", output[:60])
	}
}

func TestGenerateManpage_ContainsVersion(t *testing.T) {
	// Arrange
	version := "1.2.3"
	date := "2026-01-15"

	// Act
	output := generateManpage(version, date)

	// Assert
	if !strings.Contains(output, "daedalus 1.2.3") {
		t.Error("man page should contain the version string")
	}
}

func TestGenerateManpage_ContainsDate(t *testing.T) {
	// Arrange
	version := "0.8.2"
	date := "2026-03-07"

	// Act
	output := generateManpage(version, date)

	// Assert
	if !strings.Contains(output, "2026-03-07") {
		t.Error("man page should contain the date")
	}
}

func TestGenerateManpage_ContainsAllSections(t *testing.T) {
	// Arrange
	version := "0.8.2"
	date := "2026-03-07"
	sections := []string{
		".SH NAME",
		".SH SYNOPSIS",
		".SH DESCRIPTION",
		".SH COMMANDS",
		".SH OPTIONS",
		".SH ENVIRONMENT",
		".SH CONFIGURATION",
		".SH EXAMPLES",
		".SH EXIT STATUS",
		".SH FILES",
		".SH SEE ALSO",
		".SH AUTHORS",
		".SH COPYRIGHT",
	}

	// Act
	output := generateManpage(version, date)

	// Assert
	for _, section := range sections {
		if !strings.Contains(output, section) {
			t.Errorf("man page missing section: %s", section)
		}
	}
}

func TestGenerateManpage_ContainsAllCommands(t *testing.T) {
	// Arrange
	version := "0.8.2"
	date := "2026-03-07"
	commands := []string{
		"list",
		"prune",
		"remove",
		"config",
		"tui",
		"web",
		"completion",
	}

	// Act
	output := generateManpage(version, date)

	// Assert
	for _, cmd := range commands {
		if !strings.Contains(output, cmd) {
			t.Errorf("man page missing command: %s", cmd)
		}
	}
}

func TestGenerateManpage_ContainsAllFlags(t *testing.T) {
	// Arrange
	version := "0.8.2"
	date := "2026-03-07"
	flags := []string{
		"\\-\\-build",
		"\\-\\-target",
		"\\-\\-resume",
		"\\-p",
		"\\-\\-no\\-tmux",
		"\\-\\-debug",
		"\\-\\-dind",
		"\\-\\-force",
		"\\-\\-no\\-color",
		"\\-\\-port",
		"\\-\\-host",
	}

	// Act
	output := generateManpage(version, date)

	// Assert
	for _, flag := range flags {
		if !strings.Contains(output, flag) {
			t.Errorf("man page missing flag: %s", flag)
		}
	}
}

func TestGenerateManpage_ContainsEnvironmentVars(t *testing.T) {
	// Arrange
	version := "0.8.2"
	date := "2026-03-07"
	envVars := []string{
		"DAEDALUS_DATA_DIR",
		"NO_COLOR",
	}

	// Act
	output := generateManpage(version, date)

	// Assert
	for _, env := range envVars {
		if !strings.Contains(output, env) {
			t.Errorf("man page missing environment variable: %s", env)
		}
	}
}

func TestGenerateManpage_ContainsConfigFields(t *testing.T) {
	// Arrange
	version := "0.8.2"
	date := "2026-03-07"
	fields := []string{
		"data-dir",
		"debug",
		"no-tmux",
		"image-prefix",
	}

	// Act
	output := generateManpage(version, date)

	// Assert
	for _, field := range fields {
		if !strings.Contains(output, field) {
			t.Errorf("man page missing config field: %s", field)
		}
	}
}

func TestGenerateManpage_ContainsSeeAlso(t *testing.T) {
	// Arrange
	version := "0.8.2"
	date := "2026-03-07"
	references := []string{
		"docker",
		"tmux",
		"claude",
	}

	// Act
	output := generateManpage(version, date)

	// Assert
	for _, ref := range references {
		if !strings.Contains(output, ref) {
			t.Errorf("man page missing see also reference: %s", ref)
		}
	}
}

func TestGenerateManpage_ContainsCopyright(t *testing.T) {
	// Arrange
	version := "0.8.2"
	date := "2026-03-07"

	// Act
	output := generateManpage(version, date)

	// Assert
	if !strings.Contains(output, "Copyright (C) 2026 Techdelight BV") {
		t.Error("man page missing copyright notice")
	}
	if !strings.Contains(output, "Apache License") {
		t.Error("man page missing Apache license reference")
	}
}

func TestGenerateManpage_ContainsExitStatus(t *testing.T) {
	// Arrange
	version := "0.8.2"
	date := "2026-03-07"

	// Act
	output := generateManpage(version, date)

	// Assert
	if !strings.Contains(output, "\\fB0\\fR") {
		t.Error("man page missing exit status 0")
	}
	if !strings.Contains(output, "\\fB1\\fR") {
		t.Error("man page missing exit status 1")
	}
}

func TestGenerateManpage_ContainsFiles(t *testing.T) {
	// Arrange
	version := "0.8.2"
	date := "2026-03-07"
	files := []string{
		"config.json",
		"projects.json",
		".cache/<project>/",
	}

	// Act
	output := generateManpage(version, date)

	// Assert
	for _, f := range files {
		if !strings.Contains(output, f) {
			t.Errorf("man page missing file reference: %s", f)
		}
	}
}

func TestGenerateManpage_NameSection(t *testing.T) {
	// Arrange
	version := "0.8.2"
	date := "2026-03-07"

	// Act
	output := generateManpage(version, date)

	// Assert
	if !strings.Contains(output, "daedalus \\- Docker environment for autonomous Claude Code") {
		t.Error("NAME section should contain the short description")
	}
}

func TestGenerateManpage_UsesRoffMacros(t *testing.T) {
	// Arrange
	version := "0.8.2"
	date := "2026-03-07"
	macros := []string{
		".TH",
		".SH",
		".TP",
		".BR",
		".B",
		".I",
		".PP",
		".RS",
		".RE",
	}

	// Act
	output := generateManpage(version, date)

	// Assert
	for _, macro := range macros {
		if !strings.Contains(output, macro) {
			t.Errorf("man page missing roff macro: %s", macro)
		}
	}
}

func TestWriteHeader_Format(t *testing.T) {
	// Arrange
	var b strings.Builder
	version := "2.0.0"
	date := "2026-06-15"

	// Act
	writeHeader(&b, version, date)

	// Assert
	expected := ".TH DAEDALUS 1 \"2026-06-15\" \"daedalus 2.0.0\" \"User Commands\"\n"
	if b.String() != expected {
		t.Errorf("header = %q, want %q", b.String(), expected)
	}
}
