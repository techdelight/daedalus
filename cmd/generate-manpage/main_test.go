// Copyright (C) 2026 Techdelight BV

package main

import (
	"os"
	"path/filepath"
	"regexp"
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

// TestGenerateManpage_ContainsEverySubcommandTheCLIDispatches DERIVES the
// requirement from the CLI's own dispatch switch instead of restating a list.
//
// The list it replaced named seven commands and was written when there were
// seven. The CLI grew to nineteen, and the man page shipped for fifteen releases
// describing a tool that no longer existed — no `task`, no `programmes`, no
// `docs`, no `version`, no daemons. A test that enumerates what it checks can
// only ever be as current as the day somebody last remembered it; this one fails
// the moment a command is added without an entry.
func TestGenerateManpage_ContainsEverySubcommandTheCLIDispatches(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "daedalus", "main.go"))
	if err != nil {
		t.Fatalf("reading the CLI's main.go: %v", err)
	}
	body := string(src)
	i := strings.Index(body, "switch cfg.Subcommand {")
	if i < 0 {
		t.Fatal("the dispatch switch has moved; this test can no longer find what to check")
	}
	cases := regexp.MustCompile(`case "([a-z-]+)":`).FindAllStringSubmatch(body[i:], -1)
	if len(cases) < 10 {
		t.Fatalf("found %d subcommands, which cannot be right", len(cases))
	}

	output := generateManpage("0.8.2", "2026-03-07")
	for _, m := range cases {
		name := m[1]
		if name == "help" {
			continue // documented as a flag, not a command
		}
		// The COMMANDS section marks each entry in bold, so this looks for the
		// entry rather than for the word appearing anywhere in the prose — which
		// is how the old check passed for commands that had no entry.
		if !strings.Contains(output, `\fB`+name+`\fR`) {
			t.Errorf("`daedalus %s` is dispatched but has no COMMANDS entry in the man page", name)
		}
	}
}

// The checked-in daedalus.1 must be what the generator produces.
//
// Nothing regenerates it — no build step, no workflow — so it is a build
// artifact committed by hand, and the two drifted apart for fifteen releases
// without anything noticing. Comparing them makes the file's staleness a test
// failure instead of a discovery.
//
// The .TH line carries the date and version, which move for reasons that are not
// drift, so the comparison regenerates using the values the checked-in file
// itself declares.
func TestCheckedInManpage_MatchesTheGenerator(t *testing.T) {
	onDisk, err := os.ReadFile(filepath.Join("..", "..", "daedalus.1"))
	if err != nil {
		t.Fatalf("reading daedalus.1: %v", err)
	}
	head := regexp.MustCompile(`^\.TH DAEDALUS 1 "([^"]+)" "daedalus ([^"]+)"`).
		FindStringSubmatch(string(onDisk))
	if head == nil {
		t.Fatal("daedalus.1 has no .TH header to read the date and version from")
	}
	want := generateManpage(head[2], head[1])
	if string(onDisk) != want {
		t.Errorf("daedalus.1 is not what the generator produces — regenerate it:\n" +
			"    go run ./cmd/generate-manpage > daedalus.1\n" +
			"(it is a committed build artifact and nothing regenerates it for you)")
	}
}

// Derived from the help text, for the same reason the command check above is:
// the hand-written list this replaced named twelve flags and had missed
// --auth, --no-auth and --container-log. A list of what to check is a list that
// has to be remembered, and it was not.
func TestGenerateManpage_ContainsEveryFlagTheHelpLists(t *testing.T) {
	usage, err := os.ReadFile(filepath.Join("..", "daedalus", "usage.go"))
	if err != nil {
		t.Fatalf("reading usage.go: %v", err)
	}
	flags := regexp.MustCompile(`"  (--[a-z-]+)`).FindAllStringSubmatch(string(usage), -1)
	if len(flags) < 8 {
		t.Fatalf("found %d flags in the help, which cannot be right", len(flags))
	}

	output := generateManpage("0.8.2", "2026-03-07")
	for _, m := range flags {
		// Hyphens are escaped in roff so they cannot be read as request syntax.
		roff := `\fB` + strings.ReplaceAll(m[1], "-", `\-`) + `\fR`
		if !strings.Contains(output, roff) {
			t.Errorf("`%s` is in --help but has no OPTIONS entry in the man page", m[1])
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
		"image-prefix",
		"runner",
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

// THE `task` SUBCOMMANDS ARE DERIVED, like the top-level ones.
//
// The man page named them in a hand-written sentence, and it was missing
// `refine` from the day it shipped (2026-08-22) — nothing noticed, because the
// existing derived test reads main.go's dispatch switch and `task refine` is one
// level below it. `budget` would have been the second.
//
// This is the repository's own recurring defect: the code moved, the thing that
// describes it did not, because what checked the description was a list somebody
// had to remember to update.
func TestGenerateManpage_NamesEveryTaskSubcommand(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "daedalus", "task.go"))
	if err != nil {
		t.Fatalf("reading task.go: %v", err)
	}
	body := string(src)
	i := strings.Index(body, "switch")
	if i < 0 {
		t.Fatal("task.go has no dispatch switch; this test cannot find what to check")
	}
	// Only the first switch — the subcommand dispatch. The flag parsers below it
	// are switches too, and their cases start with a dash.
	end := strings.Index(body[i:], "\n}")
	cases := regexp.MustCompile(`case "([a-z-]+)"`).FindAllStringSubmatch(body[i:i+end], -1)
	if len(cases) < 15 {
		t.Fatalf("found %d task subcommands, which cannot be right", len(cases))
	}

	output := generateManpage("0.8.2", "2026-03-07")
	for _, m := range cases {
		name := m[1]
		if !strings.Contains(output, `\fB`+name+`\fR`) {
			t.Errorf("`daedalus task %s` is dispatched but the man page never names it", name)
		}
	}
}
