// Copyright (C) 2026 Techdelight BV

package control

import (
	"strings"
	"testing"
)

// TestReviewPrompt_CarriesThePromiseAndTheReason.
//
// A diff on its own only supports "does this compile", which is the question
// already answered elsewhere. The reviewer exists for "did this deliver what was
// promised" and "was it worth doing", and neither is askable unless the promise
// and the reason are in front of it.
func TestReviewPrompt_CarriesThePromiseAndTheReason(t *testing.T) {
	spec := ReviewSpec{
		TaskID: "T-1", JobID: "J-1", Branch: "daedalus/T-1/J-1",
		BaseSHA: "aaaaaaaaaaaa1111", HeadSHA: "bbbbbbbbbbbb2222",
		Objective:     "Add spaced-repetition intervals to the review queue",
		Rationale:     "the daily review is the habit everything else hangs off",
		RationaleBy:   CallerHuman,
		ProgrammeName: "fluency", ProgrammeFor: "get conversational by spring",
	}
	p := ReviewPrompt(spec, "diff --git a/x b/x\n+added")

	for _, want := range []string{
		spec.Objective,
		spec.Rationale,
		"human",              // the rationale's author, so its weight is legible
		"fluency",            // the programme
		"get conversational", // and what that programme is for
		"diff --git",         // the change itself
		reviewJudgementFile,  // where to put the answer
		"ADVISORY",           // and that it does not gate
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt does not carry %q:\n%s", want, p)
		}
	}
	// It must not invite the reviewer to fix anything: an agent that edits the
	// tree it is reviewing has stopped being a second party.
	if !strings.Contains(p, "not fixing it") {
		t.Error("the prompt does not tell the reviewer it is not here to fix the change")
	}
}

// An unattributed rationale must still say so, rather than reading as somebody's.
func TestReviewPrompt_SaysWhenTheReasonIsUnattributed(t *testing.T) {
	p := ReviewPrompt(ReviewSpec{Objective: "x", Rationale: "because"}, "")
	if !strings.Contains(p, "unattributed") {
		t.Errorf("prompt does not mark an unattributed rationale:\n%s", p)
	}
	// With no rationale at all, the section is absent rather than empty — an empty
	// heading reads as "there was a reason and it was blank".
	q := ReviewPrompt(ReviewSpec{Objective: "x"}, "")
	if strings.Contains(q, "WHY IT WAS ASKED FOR") {
		t.Error("a task with no rationale still got a reason section")
	}
}

// TestParseReviewJudgement_ReadsWhatAModelActuallyWrites: fenced blocks and
// surrounding prose are the two things models reliably do to JSON, and
// discarding a real judgement over punctuation would be the wrong trade.
func TestParseReviewJudgement_ReadsWhatAModelActuallyWrites(t *testing.T) {
	for _, raw := range []string{
		`{"passed":false,"reviewer":"claude","reasoning":"it misses the edge case",
		  "findings":[{"severity":"blocking","file":"a.go","line":12,"what":"no bound check","why":"panics on empty input"}]}`,
		"Here is my review:\n```json\n{\"passed\":false,\"reviewer\":\"claude\",\"reasoning\":\"it misses the edge case\"," +
			"\"findings\":[{\"severity\":\"blocking\",\"file\":\"a.go\",\"line\":12,\"what\":\"no bound check\",\"why\":\"panics on empty input\"}]}\n```\nHope that helps.",
	} {
		out, err := ParseReviewJudgement([]byte(raw))
		if err != nil {
			t.Fatalf("ParseReviewJudgement: %v\ninput: %s", err, raw)
		}
		if out.Passed {
			t.Error("passed = true, want the reviewer's actual verdict")
		}
		if out.Reviewer != "claude" || out.Reasoning == "" {
			t.Errorf("out = %+v, want the attribution and the reasoning", out)
		}
		if len(out.Findings) != 1 || out.Findings[0].Severity != SeverityBlocking ||
			out.Findings[0].Line != 12 || out.Findings[0].Why == "" {
			t.Errorf("findings = %+v, want the one blocking finding intact", out.Findings)
		}
		if out.Detail == "" || !strings.Contains(out.Detail, "1 finding") {
			t.Errorf("detail = %q, want a one-line summary for the queue", out.Detail)
		}
	}
}

// TestParseReviewJudgement_RefusesAJudgementThatSaysNothing.
//
// A file with no verdict must NOT read as a pass or a fail. Go's zero value for
// `passed` is false, so decoding straight into a bool would silently render "the
// model wrote nothing useful" as "the reviewer disapproved" — a harness failure
// dressed as a criticism of the work, which is the exact confusion this whole
// milestone exists to stop.
func TestParseReviewJudgement_RefusesAJudgementThatSaysNothing(t *testing.T) {
	for _, raw := range []string{
		`{"reviewer":"claude","reasoning":"looks fine to me"}`, // no verdict
		`not json at all`,
		``,
	} {
		if _, err := ParseReviewJudgement([]byte(raw)); err == nil {
			t.Errorf("ParseReviewJudgement(%q) = nil error, want a refusal", raw)
		}
	}
}

// An unrecognised severity is downgraded to a note, never promoted: guessing that
// something is BLOCKING overstates what the reviewer said, and dropping it loses
// an observation that was worth making.
func TestParseReviewJudgement_UnknownSeverityBecomesANote(t *testing.T) {
	out, err := ParseReviewJudgement([]byte(
		`{"passed":true,"findings":[{"severity":"critical","what":"odd naming"},{"severity":"note","what":""}]}`))
	if err != nil {
		t.Fatalf("ParseReviewJudgement: %v", err)
	}
	if len(out.Findings) != 1 {
		t.Fatalf("findings = %+v, want the empty one dropped and the other kept", out.Findings)
	}
	if out.Findings[0].Severity != SeverityNote {
		t.Errorf("severity = %q, want it downgraded to note", out.Findings[0].Severity)
	}
	if out.Blocking() != 0 {
		t.Error("an unknown severity was counted as blocking")
	}
}

// TestReviewUnavailable_IsNotDisapproval.
//
// "The reviewer could not be made to report" and "the reviewer read this and
// disliked it" are different facts. Rendering the first as the second is how a
// broken harness ends up looking like a criticism of the work — which is what
// every verify verdict in this project's history did before M20.
func TestReviewUnavailable_IsNotDisapproval(t *testing.T) {
	out := reviewUnavailable("the agent exited 1")
	if out.Blocking() != 0 {
		t.Error("a harness failure produced a BLOCKING finding; it is a concern, not a criticism")
	}
	if len(out.Findings) != 1 || out.Findings[0].Severity != SeverityConcern {
		t.Fatalf("findings = %+v, want exactly one concern", out.Findings)
	}
	if !strings.Contains(out.Detail, "no judgement") {
		t.Errorf("detail = %q, want it to say no judgement was produced", out.Detail)
	}
	if !strings.Contains(out.Reasoning, "says nothing about the") {
		t.Errorf("reasoning = %q, want it to disclaim any statement about the change", out.Reasoning)
	}
}

// The reviewer's throwaway project must never collide with the Job's, in the
// registry or in a log. A review that ran under the Job's name would be
// indistinguishable from the work in every record that mentions it.
func TestReviewProjectName_IsDistinctFromTheJobs(t *testing.T) {
	if reviewProjectName("J-1") == JobProjectName("J-1") {
		t.Fatal("the review and the Job share a project name")
	}
	if !strings.Contains(reviewProjectName("J-1"), "J-1") {
		t.Error("the review project is not keyed to its job; two reviews could collide")
	}
}

// TestReviewLogPath_IsKeyedByJobAndBesideTheJobLogs.
//
// A review had no log of its own: the reviewing agent's output went to the
// daemon's shared control.log, interleaved and keyed by nothing — the exact
// defect Backlog #77 fixed for Jobs, reproduced one component later. A review
// that produces no judgement is precisely when somebody needs to read what the
// agent actually said, and "it seemed to do very little" is what it looks like
// when there is nowhere to look.
func TestReviewLogPath_IsKeyedByJobAndBesideTheJobLogs(t *testing.T) {
	got := ReviewLogPath("/data", "J-12")
	if !strings.Contains(got, "J-12") {
		t.Errorf("ReviewLogPath = %q, want it keyed by the job", got)
	}
	if got == JobLogPath("/data", "J-12") {
		t.Error("a review writes over the Job's own log; they are different accounts of different runs")
	}
	// No data dir means nowhere, not a path relative to the working directory —
	// the same degradation JobLogPath makes.
	if ReviewLogPath("", "J-12") != "" {
		t.Error("ReviewLogPath with no data dir should be empty")
	}
}

// TestReviewInstruction_DoesNotGrowWithTheChange.
//
// The bug this pins: the whole prompt, diff included, was one argv element, and
// Linux caps a SINGLE argument at 128KB whatever room the rest of the command
// line has. A review of a large change therefore died with `fork/exec …:
// argument list too long` BEFORE the agent ran — measured on T-19, whose second
// pass produced no judgement for exactly this reason.
//
// The command line must now be constant. It is the same move the judgement
// already makes in the other direction: read from a file, not scraped from a
// channel with limits nobody controls.
func TestReviewInstruction_DoesNotGrowWithTheChange(t *testing.T) {
	// Comfortably past MAX_ARG_STRLEN (32 pages), which is what broke.
	huge := strings.Repeat("+ a line of diff\n", 40_000)
	if len(huge) <= 131072 {
		t.Fatalf("the fixture is %d bytes; it must exceed the 128KB single-argument limit", len(huge))
	}

	if len(reviewInstruction) > 1024 {
		t.Errorf("the command line is %d bytes and must be a fixed pointer to the prompt file",
			len(reviewInstruction))
	}
	if !strings.Contains(reviewInstruction, reviewPromptFile) {
		t.Error("the instruction must name the file the brief is in, or the agent has nothing to read")
	}
	// And the PROMPT still carries the whole diff. It is a file now, so there is
	// no limit to respect and nothing to be gained by cutting it: a reviewer shown
	// part of a change reports on the part as though it were the whole, and the
	// verdict it returns is a boolean about the whole artifact either way.
	prompt := ReviewPrompt(ReviewSpec{Objective: "x", BaseSHA: "a", HeadSHA: "b"}, huge)
	if !strings.Contains(prompt, huge) {
		t.Error("the diff must reach the reviewer in full — truncating it trades a possible " +
			"problem (the agent runs out of context) for a certain one (it never saw the rest)")
	}
}
