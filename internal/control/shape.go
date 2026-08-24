// Copyright (C) 2026 Techdelight BV

package control

import (
	"fmt"
	"strings"
)

// How a Task is SHAPED, as advice rather than as a rule.
//
// The complaint this answers, in the operator's own words: "the tasks provided
// to the ledger are a big blob of text about what to do for a milestone with no
// clear deliverables". They were. `Objective` is one free-text field and it was
// carrying a milestone, because nothing anywhere said it should not.
//
// NOTHING HERE REFUSES ANYTHING, and that is deliberate. A length limit on an
// objective would be a rule about prose enforced by counting characters, which
// is the kind of check that is wrong on the useful cases and right on none: a
// long objective for genuinely intricate work is fine, and a two-line objective
// naming four unrelated features is not, and no counter can tell them apart.
// What a counter CAN do is notice the shape that is almost always a milestone
// and say so to whoever is about to create it. Then a person decides.
//
// It is one function so that the CLI, the MCP tool and the Ledger give the same
// advice. Three surfaces each with their own opinion about what a good Task
// looks like is how they end up disagreeing.

// objectiveSoftLimit is where an objective stops reading like one thing to do.
//
// 300 characters is about three sentences of plain prose — roughly the point at
// which the plain-language guidance (sentences of 15–20 words, one topic per
// paragraph) says a reader has been given more than one idea. It is a threshold
// for a HINT, so being approximately right is the whole requirement.
const objectiveSoftLimit = 300

// ObjectiveAdvice returns a note for whoever is creating a Task, or "" when the
// Task looks like a Task.
//
// Two separate observations, because they have different remedies: an objective
// long enough to be a milestone wants SPLITTING, and a Task with nothing it
// promises to produce wants a deliverable written. A Task can have either
// problem without the other.
func ObjectiveAdvice(objective string, deliverables []string) string {
	var notes []string
	if n := len([]rune(strings.TrimSpace(objective))); n > objectiveSoftLimit {
		notes = append(notes, fmt.Sprintf(
			"the objective is %d characters, which usually means it is a milestone rather than a task — "+
				"consider splitting it into tasks that each deliver one thing", n))
	}
	if len(cleanLines(deliverables)) == 0 {
		notes = append(notes,
			"no deliverables: nothing on this task says what will exist when it is done, so the "+
				"reviewer and the person at the approval gate have only the prose to go on")
	}
	return strings.Join(notes, "; ")
}
