// Copyright (C) 2026 Techdelight BV

package control

// The REAL reviewer: a separate agent that reads the diff and reports (M20).
//
// This is the rung that has shipped as StubReviewRunner since M15. It answers
// the question an exit code cannot — did this deliver what it promised, and was
// it worth doing — and it answers it as EVIDENCE. Nothing in the plane
// transitions on what it says (review.go's ReviewTask spells out why).
//
// WHAT MAKES IT INDEPENDENT, and it is three separate things:
//
//  1. A FRESH CHECKOUT of the artifact commit, not the Job's worktree. The Job's
//     tree can have anything in it that the agent left behind; only the commit is
//     what would land.
//  2. A DIFFERENT SESSION. It runs as its own throwaway project with its own
//     home, so it cannot read the Job's transcript. A reviewer that could see how
//     the work was argued for is not reviewing the work, it is being lobbied by
//     it — and at that point it is grading its own homework by proxy.
//  3. THE DIFF IS COMPUTED ON THE HOST and handed to it, rather than left for it
//     to derive. The reviewer is shown what would land, from the plane's own
//     reading of the commits, and cannot be pointed at something else.
//
// WHAT IT COSTS, said plainly rather than absorbed: unlike CleanVerifier this
// container has THE NETWORK AND CREDENTIALS. It must — it is a language model
// making a call. So the clean-room property the verifier has (network off,
// nothing mounted but the checkout, no ambient credentials) does NOT hold here,
// and the compensating control is that its output is advisory. A reviewer that
// could both reach the network and move plane state would be the lethal trifecta
// with the parts relabelled.
//
// HOST-ONLY, like CoordinatorRunner and CleanVerifier: it needs Docker and a
// logged-in project. The pure parts — the prompt, and reading the judgement back
// — are host-tested; the container path is exercised only on a real host.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/techdelight/daedalus/internal/executor"
)

// reviewJudgementFile is where the reviewing agent is told to leave its verdict,
// relative to the checkout root.
//
// A FILE, not stdout. An agent's stdout is prose with a judgement somewhere in
// it, and parsing that would make the plane's reading of a verdict depend on how
// chatty a model felt. The file is read on the HOST, from a directory the plane
// created — the same shape as capturing a Job's tree.
const reviewJudgementFile = ".daedalus/review.json"

// reviewPromptFile is where the agent is asked to READ its instructions from,
// rather than being handed them on the command line.
//
// WHY A FILE. The prompt carries the diff, and the diff is unbounded. Linux caps
// a SINGLE argv element at MAX_ARG_STRLEN — 32 pages, 128KB — regardless of how
// much room the rest of the command line has, so a review of a large change died
// with `fork/exec …: argument list too long` before the agent ran at all.
// Measured on T-19, whose second review pass produced no judgement for exactly
// this reason.
//
// It is the same move the judgement already makes in the other direction: that
// is read back from a file rather than scraped from stdout, because a channel
// with limits nobody controls is not a channel to put a review through. The
// input deserved the same treatment and did not have it.
const reviewPromptFile = ".daedalus/review-prompt.md"

// ReviewLogPath is where a review's own output is recorded: one file per Job
// reviewed, beside the Jobs' own logs. Empty dataDir yields "" — nowhere — the
// same degradation JobLogPath makes.
func ReviewLogPath(dataDir, jobID string) string {
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, ".daedalus", "reviews", jobID+".log")
}

// AgentReviewer is the REAL, HOST-ONLY ReviewRunner.
type AgentReviewer struct {
	Exec    executor.Executor
	BinPath string // path to the `daedalus` CLI
	DataDir string // where project homes live; needed to seed the reviewer's
	// Runner names the agent to review with. Empty means the project default.
	Runner string
}

// reviewProjectName is the throwaway project a review runs under. Keyed to the
// job so two reviews never collide, and distinct from JobProjectName so a review
// can never be mistaken for the work it is reading — in the registry, in the
// container list, or in a log.
func reviewProjectName(jobID string) string { return "daedalus-review-" + jobID }

// Review implements ReviewRunner.
func (r AgentReviewer) Review(ctx context.Context, spec ReviewSpec) ReviewOutcome {
	name := reviewProjectName(spec.JobID)
	// Deregister the throwaway project on every exit path, exactly as a Job does:
	// the registration describes something that outlives it by seconds.
	defer func() {
		_ = r.Exec.RunWithEnv(r.dataDirEnv(), r.BinPath, "remove", name, "--force")
	}()

	parent, err := os.MkdirTemp("", "daedalus-review-"+spec.JobID+"-")
	if err != nil {
		return reviewUnavailable("could not make a checkout directory: " + err.Error())
	}
	defer os.RemoveAll(parent)
	checkout := filepath.Join(parent, "checkout")

	// A clean checkout of the exact commit — never the Job's worktree.
	if out, err := runGit(spec.RepoDir, "worktree", "add", "--detach", checkout, spec.HeadSHA); err != nil {
		return reviewUnavailable("clean checkout failed: " + err.Error() + "\n" + strings.TrimSpace(out))
	}
	defer func() { _, _ = runGit(spec.RepoDir, "worktree", "remove", "--force", checkout) }()

	// The diff, computed by the plane. `--no-renames` so a moved file reads as
	// what it is to a reader; the verifier's integrity gate uses the same flag for
	// the same reason.
	diff, err := runGit(spec.RepoDir, "diff", "--no-renames", spec.BaseSHA+".."+spec.HeadSHA)
	if err != nil {
		return reviewUnavailable("could not read the diff: " + err.Error())
	}

	// Somewhere for the judgement to land, created by the plane so its absence
	// afterwards means the agent did not write one.
	if err := os.MkdirAll(filepath.Join(checkout, ".daedalus"), 0o755); err != nil {
		return reviewUnavailable("could not prepare the judgement path: " + err.Error())
	}

	// Its own home, with the owning project's credentials — a review is a real
	// agent invocation and dies on `Not logged in` without them, exactly as a Job
	// does. Its own home is also what keeps it out of the Job's transcript.
	seedJobHomeOrWarn(r.DataDir, spec.Project, name)

	// A log of its own, for the reason Backlog #77 gave Jobs one: without it the
	// agent's output reaches only the daemon's shared control.log, interleaved
	// with everything else and keyed by nothing. A review that produces no
	// judgement is exactly when somebody needs to read what the agent said, and
	// "seemed to do very little" is what it looks like when there is nowhere to
	// look.
	logPath := ReviewLogPath(r.DataDir, spec.JobID)
	var tee io.Writer
	if logPath != "" {
		if f, err := openJobLog(logPath); err == nil {
			defer f.Close()
			tee = f
		} else {
			log.Printf("control: opening review log %s: %v", logPath, err)
			logPath = ""
		}
	}
	where := ""
	if logPath != "" {
		where = " (its output is in " + logPath + ")"
	}

	// The prompt goes in the checkout and the agent is told to read it. Written
	// BEFORE the run and inside the worktree the agent already has mounted, so
	// there is no second path to agree about and nothing to clean up separately —
	// the whole worktree is removed when this returns.
	promptPath := filepath.Join(checkout, filepath.FromSlash(reviewPromptFile))
	if err := os.MkdirAll(filepath.Dir(promptPath), 0o755); err != nil {
		return reviewUnavailable("could not prepare the review prompt directory: " + err.Error())
	}
	if err := os.WriteFile(promptPath, []byte(ReviewPrompt(spec, diff)), 0o644); err != nil {
		return reviewUnavailable("could not write the review prompt: " + err.Error())
	}
	args := []string{name, checkout, "-p", reviewInstruction}
	if r.Runner != "" {
		args = append([]string{"--runner", r.Runner}, args...)
	}
	if err := r.Exec.RunWithEnvTee(r.dataDirEnv(), tee, r.BinPath, args...); err != nil {
		// The agent failing is not the artifact failing. Say which one happened.
		return reviewUnavailable("the reviewing agent exited with an error: " + err.Error() + where)
	}

	raw, err := os.ReadFile(filepath.Join(checkout, reviewJudgementFile))
	if err != nil {
		return reviewUnavailable("the reviewer ran but wrote no judgement to " + reviewJudgementFile + where)
	}
	out, err := ParseReviewJudgement(raw)
	if err != nil {
		return reviewUnavailable("the reviewer's judgement could not be read: " + err.Error() + where)
	}
	if out.Reviewer == "" {
		out.Reviewer = "agent:" + name
	}
	return out
}

// dataDirEnv pins the spawned CLI to the daemon's data dir, for the reason
// CoordinatorRunner.dataDirEnv exists: seeding writes to one data dir and an
// unpinned CLI resolves its own, so a divergence mounts a home nobody seeded.
func (r AgentReviewer) dataDirEnv() []string {
	if r.DataDir == "" {
		return nil
	}
	return []string{"DAEDALUS_DATA_DIR=" + r.DataDir}
}

// reviewUnavailable is the outcome when no judgement could be obtained.
//
// It is deliberately NOT rendered as disapproval. "The reviewer did not deliver a
// reading" and "the reviewer read this and disliked it" are different facts, and
// a harness failure that reads as a criticism of the work is exactly the
// confusion the whole verification arc has been fixing. It comes back as a
// CONCERN — worth a human's attention before deciding — which is precisely true.
func reviewUnavailable(why string) ReviewOutcome {
	return ReviewOutcome{
		Passed: false,
		Detail: "no judgement: " + why,
		Reasoning: "The review did not produce a verdict. This says nothing about the " +
			"change; it says the reviewer could not be made to report on it.",
		Findings: []Finding{{
			Severity: SeverityConcern,
			What:     "no review judgement was produced",
			Why:      why,
		}},
	}
}

// reviewInstruction is the whole command line the agent gets: a pointer to the
// prompt, and nothing that grows with the size of the change.
//
// Deliberately imperative about reading the file FIRST. An agent handed a
// one-line instruction and a mounted checkout can otherwise start exploring the
// tree and never open the brief.
const reviewInstruction = "Read " + reviewPromptFile + " in this directory. It is your complete " +
	"brief — the change to review, what was asked for, and the exact file you must write your " +
	"judgement to. Follow it precisely and do not modify any other file."

// ReviewPrompt builds what the reviewing agent is asked.
//
// Pure, and exported, so the thing most likely to need changing is the thing
// easiest to read and test. Three properties it must have:
//
//   - It carries the PROMISE (the objective), the REASON (the rationale, with its
//     author) and the PROGRAMME, because a diff alone answers only "does this
//     compile", which is the question already answered elsewhere.
//   - It names the schema exactly, because the plane reads a file and not prose.
//   - It says what the reviewer is NOT for: it does not fix anything, and its
//     verdict does not gate. An agent that believes it is the gate will hedge.
func ReviewPrompt(spec ReviewSpec, diff string) string {
	var b strings.Builder
	b.WriteString("You are reviewing a change that another agent wrote. You are not its author, " +
		"and you are not fixing it: your entire job is to read it and report.\n\n")

	b.WriteString("WHAT WAS ASKED FOR\n")
	b.WriteString(spec.Objective + "\n\n")

	// The roll call. "Does this deliver what was asked for" is an essay question
	// against prose and a checkable one against a list — and it is the question
	// the reviewer exists to answer, so it is worth giving it something it can be
	// wrong about.
	if d := cleanLines(spec.Deliverables); len(d) > 0 {
		b.WriteString("WHAT IT SAID WOULD EXIST WHEN DONE\n")
		for _, item := range d {
			b.WriteString("  - " + item + "\n")
		}
		b.WriteString("\n")
	}

	if spec.Rationale != "" {
		by := string(spec.RationaleBy)
		if by == "" {
			by = "unattributed"
		}
		b.WriteString("WHY IT WAS ASKED FOR (stated by: " + by + ")\n")
		b.WriteString(spec.Rationale + "\n\n")
	}
	if spec.ProgrammeName != "" {
		b.WriteString("THE PROGRAMME THIS SERVES\n")
		b.WriteString(spec.ProgrammeName)
		if spec.ProgrammeFor != "" {
			b.WriteString(" — " + spec.ProgrammeFor)
		}
		b.WriteString("\n\n")
	}

	b.WriteString("THE CHANGE\n")
	b.WriteString("Base " + shortSHA(spec.BaseSHA) + " → head " + shortSHA(spec.HeadSHA) +
		" on " + spec.Branch + ". The full checkout is at /workspace; the diff is:\n\n")
	b.WriteString("```diff\n" + diff + "\n```\n\n")

	b.WriteString("WHAT TO ANSWER\n")
	if len(cleanLines(spec.Deliverables)) > 0 {
		b.WriteString("1. Take the list above one item at a time: does each thing actually exist in " +
			"this change, and does it work? Say which ones do not. A deliverable that is present but " +
			"inert is missing.\n")
	} else {
		b.WriteString("1. Does this deliver what was asked for? Name what is missing, not just what is present.\n")
	}
	if spec.Rationale != "" {
		b.WriteString("2. Does it serve the reason it was asked for? A change can do exactly what the " +
			"objective said and still not advance the reason behind it.\n")
	}
	b.WriteString("3. What would you want the operator to look at before they decide?\n\n")

	b.WriteString("HOW TO ANSWER\n")
	b.WriteString("Write your judgement to " + reviewJudgementFile + " and change nothing else. Exactly this shape:\n\n")
	b.WriteString(`{
  "passed": true,
  "reviewer": "who you are",
  "findings": [
    {"severity": "blocking|concern|note", "file": "path", "line": 0,
     "what": "the defect, one sentence",
     "why": "the consequence, one sentence",
     "fix": "the action, one imperative sentence"}
  ],
  "reasoning": "how you read the change — at most three sentences"
}` + "\n\n")

	// THE LENGTH LIMIT IS THE FEATURE. Without it every field came back as a
	// paragraph and a five-finding review was a page of prose: the operator it is
	// written for had to read it twice to work out what to do, and said so. The
	// fields are one sentence each for the same reason an XP story goes on an
	// index card — the size of the space is what does the editing.
	b.WriteString("KEEP IT SHORT, and mean it:\n")
	b.WriteString("  - `what` is ONE sentence naming the defect. Under 20 words. Do not describe " +
		"the code back to me; say what is wrong with it.\n")
	b.WriteString("  - `why` is ONE sentence: what goes wrong for someone if this ships.\n")
	b.WriteString("  - `fix` is ONE sentence in the imperative: the action you would take.\n")
	b.WriteString("  - `reasoning` is at most three sentences on how you read the change, and it " +
		"goes LAST because it is the part a reader only wants once they disagree with you.\n")
	b.WriteString("  - At most five `note` findings. If you have more, keep the five that matter " +
		"and say how many you dropped in `reasoning`.\n")
	b.WriteString("  - One finding per defect. Two problems in one entry cannot be fixed one at a " +
		"time, and cannot be given different severities.\n\n")

	b.WriteString("Your verdict is ADVISORY. It is shown to a human who decides; it does not block " +
		"anything on its own. So say what you actually think rather than what is safe: a hedge " +
		"helps nobody, a finding with no `why` is an opinion, and a finding with no `fix` leaves " +
		"the work you were asked to do to the person reading you.\n")
	return b.String()
}

// ParseReviewJudgement reads what the reviewer wrote.
//
// Tolerant of the two things models reliably do to JSON — wrapping it in a fenced
// block, and adding prose around it — because the alternative is discarding a
// real judgement over punctuation. It is NOT tolerant of a missing verdict: a
// file that parses but says nothing is reported as no judgement rather than as a
// pass, since a default-zero `passed` would silently read as disapproval.
func ParseReviewJudgement(raw []byte) (ReviewOutcome, error) {
	text := strings.TrimSpace(string(raw))
	if i := strings.Index(text, "```"); i >= 0 {
		rest := text[i+3:]
		if j := strings.Index(rest, "\n"); j >= 0 {
			rest = rest[j+1:]
		}
		if k := strings.Index(rest, "```"); k >= 0 {
			rest = rest[:k]
		}
		text = strings.TrimSpace(rest)
	}
	if start := strings.Index(text, "{"); start > 0 {
		text = text[start:]
	}
	if end := strings.LastIndex(text, "}"); end >= 0 && end < len(text)-1 {
		text = text[:end+1]
	}

	var j struct {
		Passed    *bool     `json:"passed"`
		Reviewer  string    `json:"reviewer"`
		Reasoning string    `json:"reasoning"`
		Findings  []Finding `json:"findings"`
	}
	if err := json.Unmarshal([]byte(text), &j); err != nil {
		return ReviewOutcome{}, fmt.Errorf("not valid JSON: %w", err)
	}
	if j.Passed == nil {
		return ReviewOutcome{}, fmt.Errorf("no `passed` verdict in the judgement")
	}
	out := ReviewOutcome{
		Passed: *j.Passed, Reviewer: j.Reviewer, Reasoning: j.Reasoning,
	}
	for _, f := range j.Findings {
		if strings.TrimSpace(f.What) == "" {
			continue // a finding with nothing in it is noise, not a finding
		}
		if !validSeverity(f.Severity) {
			// An unrecognised severity becomes a note rather than being dropped: the
			// observation is still worth having, and guessing it is BLOCKING would
			// overstate what the reviewer said.
			f.Severity = SeverityNote
		}
		out.Findings = append(out.Findings, f)
	}
	// Bottom line up front. Sorted here, once, so every surface that renders a
	// review shows the blocking findings first without each one deciding to —
	// the alternative is three renderers agreeing by hand and one of them
	// eventually not. Stable, so a reviewer's own ordering survives within a
	// severity: it listed them in the order it read the diff, which is useful.
	sort.SliceStable(out.Findings, func(i, j int) bool {
		return severityRank(out.Findings[i].Severity) < severityRank(out.Findings[j].Severity)
	})
	out.Detail = summariseReview(out)
	return out, nil
}

func validSeverity(s Severity) bool {
	return s == SeverityBlocking || s == SeverityConcern || s == SeverityNote
}

// summariseReview is the one line an operator sees in a queue.
func summariseReview(o ReviewOutcome) string {
	verdict := "reviewer had concerns"
	if o.Passed {
		verdict = "reviewer found no blocker"
	}
	if len(o.Findings) == 0 {
		return verdict + ", no findings"
	}
	return fmt.Sprintf("%s — %d finding(s), %d blocking", verdict, len(o.Findings), o.Blocking())
}
