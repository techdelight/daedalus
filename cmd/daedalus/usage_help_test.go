// Copyright (C) 2026 Techdelight BV

package main

import (
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestUsage_MentionsEverySubcommandItDispatches DERIVES the requirement from
// main.go rather than restating a list.
//
// `programmes` and `coordinator` were dispatched for months and appeared nowhere
// in `--help`; `programmes` was rewritten into a control-plane client the day
// before this test was written and still went unmentioned. A help text is the
// only place a person looks to find out what a tool can do, so a command missing
// from it does not exist as far as they are concerned.
//
// Reading the source is the point: a hardcoded list here would need the same
// edit as the help text and would therefore be forgotten in the same breath.
func TestUsage_MentionsEverySubcommandItDispatches(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("reading main.go: %v", err)
	}
	// The dispatch switch is `switch cfg.Subcommand { case "x": … }`.
	body := string(src)
	i := strings.Index(body, "switch cfg.Subcommand {")
	if i < 0 {
		t.Fatal("the dispatch switch has moved; this test can no longer find what to check")
	}
	cases := regexp.MustCompile(`case "([a-z-]+)":`).FindAllStringSubmatch(body[i:], -1)
	if len(cases) < 5 {
		t.Fatalf("found %d subcommands, which cannot be right", len(cases))
	}

	// The check is that the name STARTS a line in the command table — not that it
	// appears somewhere in the text.
	//
	// A plain substring match is what this test used to do, and it was satisfied
	// by accident twice: "control" matched the phrase "control plane", and "build"
	// matched the flag "--build". Both commands were dispatched and neither had an
	// entry a reader could find, which is precisely what this test exists to
	// prevent. A test that cannot fail for the reason it was written is worse than
	// no test, because it is counted as coverage.
	help := captureUsage(t)
	listed := map[string]bool{}
	for _, line := range strings.Split(help, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		listed[fields[0]] = true
	}
	for _, m := range cases {
		name := m[1]
		if name == "help" {
			continue // `--help` is listed as a flag, not as a command
		}
		if !listed[name] {
			t.Errorf("`daedalus %s` is dispatched but begins no line of --help; "+
				"a command missing from the help does not exist to anybody reading it", name)
		}
	}
}

// TestUsage_NamesTheControlPlaneIdPrefixes.
//
// T-n, J-n, A-n, RV-n, P-n, PR-n are a vocabulary the whole control plane speaks
// and nothing explained. An operator reading "A-8" in their own terminal had to
// ask what it was.
func TestUsage_NamesTheControlPlaneIdPrefixes(t *testing.T) {
	help := captureUsage(t)
	for _, prefix := range []string{"T-n", "J-n", "A-n", "RV-n", "P-n", "PR-n"} {
		if !strings.Contains(help, prefix) {
			t.Errorf("--help does not say what %s is", prefix)
		}
	}
}

// captureUsage runs printUsage and returns what it wrote.
func captureUsage(t *testing.T) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	printUsage()
	w.Close()
	os.Stdout = saved
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
