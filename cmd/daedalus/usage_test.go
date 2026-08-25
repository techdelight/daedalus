// Copyright (C) 2026 Techdelight BV

package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// EVERY `task` SUBCOMMAND IS NAMED IN THE HELP, derived from the dispatch.
//
// The existing check reads main.go's switch, and `task` subcommands live one
// level below it — so `refine` was missing from the man page from the day it
// shipped, `budget` would have been the second, and the same help text listed
// `refine` twice because it was maintained by hand. An agent told it cannot do
// something will not try, and neither will an operator.
func TestUsage_NamesEveryTaskSubcommand(t *testing.T) {
	src, err := os.ReadFile("task.go")
	if err != nil {
		t.Fatalf("reading task.go: %v", err)
	}
	body := string(src)
	i := strings.Index(body, "switch")
	if i < 0 {
		t.Fatal("task.go has no dispatch switch; this test cannot find what to check")
	}
	end := strings.Index(body[i:], "\n}")
	cases := regexp.MustCompile(`case "([a-z-]+)"`).FindAllStringSubmatch(body[i:i+end], -1)
	if len(cases) < 15 {
		t.Fatalf("found %d task subcommands, which cannot be right", len(cases))
	}

	// TOKENS, not substrings. `strings.Contains(help, "budget")` passes on the
	// word "budgets" in a sentence three lines away — which is exactly how this
	// repository's earlier help tests passed for commands that had no entry
	// ("control" matched "control plane", "build" matched "--build"). The first
	// version of THIS test had the same flaw and was caught by reverting the
	// help text and watching it stay green.
	//
	// The task lines list subcommands separated by `|`, each optionally followed
	// by its arguments, so a token is the first word of a pipe-separated field.
	// Only the TASK lists. Every other command has a pipe-separated argument list
	// too — `coordinator [start | stop | status]` — and counting those made
	// `status` look like it appeared seven times. The task block is the run of
	// piped lines beginning at the one naming `daedalus task`.
	listed := map[string]int{}
	inTask := false
	for _, line := range strings.Split(captureUsage(t), "\n") {
		if strings.Contains(line, "daedalus task") || strings.HasPrefix(strings.TrimSpace(line), "task ") {
			inTask = true
		} else if !strings.Contains(line, "|") {
			inTask = false
		}
		if !inTask || !strings.Contains(line, "|") {
			continue
		}
		for _, field := range strings.Split(line, "|") {
			// The subcommand is the first word of the field that is not the binary,
			// not the word `task`, and not an argument. `daedalus task [create` and
			// `  task create` and ` status <id> ` all have to yield one name.
			for _, w := range strings.Fields(field) {
				w = strings.Trim(w, "[](),")
				if w == "" || w == "daedalus" || w == "task" {
					continue
				}
				if strings.HasPrefix(w, "<") || strings.HasPrefix(w, "-") {
					break // arguments start here; the name came first or not at all
				}
				listed[w]++
				break
			}
		}
	}
	for _, m := range cases {
		name := m[1]
		if listed[name] == 0 {
			t.Errorf("`daedalus task %s` is dispatched but is named nowhere in --help's task "+
				"lists; a command missing from the help does not exist to anybody reading it", name)
		}
		// …and named ONCE per list. A list maintained by hand grows duplicates,
		// which is how `refine` came to appear twice in the same line.
		// Twice is legitimate — the Usage block and the Commands table both list
		// them. Three times means one list says it twice, which is how `refine`
		// looked before this test existed.
		if listed[name] > 2 {
			t.Errorf("`task %s` is listed %d times; one of the help's lists repeats itself",
				name, listed[name])
		}
	}
}
