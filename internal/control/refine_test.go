// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestRefine_ContinuesFromTheArtifactButGradesFromTheBase.
//
// The two halves are the whole design, and getting either wrong breaks something
// different: starting from the base makes a refine a retry, and grading from the
// artifact would let an artifact carry itself past the verifier by being declared
// the new starting point.
func TestRefine_ContinuesFromTheArtifactButGradesFromTheBase(t *testing.T) {
	repo := gitRepo(t)
	svc, store, task := verifiedTask(t, repo, StubVerifyRunner{Pass: true}, "a.txt")

	before, _ := store.GetTask(task.ID)
	// The artifact the refine should continue from: the plane's own choice, via the
	// same helper the reviewer uses, so the test cannot disagree with the code
	// about which artifact "the latest" is.
	svc.mu.Lock()
	_, art, ok, err := svc.jobToReview(task.ID)
	svc.mu.Unlock()
	if err != nil || !ok {
		t.Fatalf("precondition: no artifact to continue from (%v)", err)
	}

	refined, err := svc.RefineTask(task.ID, RefineRequest{Note: "tighten the wording"})
	if err != nil {
		t.Fatalf("RefineTask: %v", err)
	}
	if refined.State != StatePlanned {
		t.Errorf("state = %q, want planned so the ladder is open", refined.State)
	}
	if refined.RefineFrom != art.HeadSHA {
		t.Errorf("refineFrom = %q, want the artifact %q", refined.RefineFrom, art.HeadSHA)
	}
	// THE OBJECTIVE IS UNTOUCHED. That is the difference from replan, and the
	// record needs it: "the work was nearly right" is a different fact from "I
	// asked for the wrong thing".
	if refined.Objective != before.Objective {
		t.Errorf("objective changed to %q; refine corrects work, replan corrects the instruction",
			refined.Objective)
	}
	// …and so is the base it will be GRADED from.
	if refined.BaseSHA != before.BaseSHA {
		t.Errorf("base moved from %q to %q — the original work must stay inside the diff "+
			"the oracle sees", before.BaseSHA, refined.BaseSHA)
	}
}

// The findings a human forwards reach the agent, and the reviewer's REASONING
// does not. The reasoning is where "here is what would persuade me" lives, and
// handing that to the party being graded is how an agent starts writing for the
// reviewer rather than for the change.
func TestRefine_ForwardsFindingsAndNotTheReasoning(t *testing.T) {
	repo := gitRepo(t)
	// The reasoning and the findings must be DISTINGUISHABLE, or the assertion
	// below cannot fail: recordingReviewer uses one string for both.
	rev := reviewerFunc(func(_ context.Context, _ ReviewSpec) ReviewOutcome {
		return ReviewOutcome{
			Passed:    false,
			Reviewer:  "test",
			Reasoning: "I would sign this off if the helper were renamed and the test tightened",
			Findings: []Finding{{
				Severity: SeverityBlocking, File: "a.txt", Line: 3,
				What: "this duplicates an existing helper",
				Why:  "it is already in helpers.go",
			}},
		}
	})
	svc, store, task := verifiedTask(t, repo, StubVerifyRunner{Pass: true}, "a.txt")
	svc.SetReviewRunner(rev)

	if _, err := svc.ReviewTask(task.ID); err != nil {
		t.Fatalf("ReviewTask: %v", err)
	}
	reviews, _ := store.ReviewsForTask(task.ID)
	if len(reviews) != 1 {
		t.Fatalf("%d reviews, want 1", len(reviews))
	}

	refined, err := svc.RefineTask(task.ID, RefineRequest{
		ReviewID: reviews[0].ID, Note: "and rename the helper",
	})
	if err != nil {
		t.Fatalf("RefineTask: %v", err)
	}

	svc.mu.Lock()
	prompt := svc.continuationFor(refined)
	svc.mu.Unlock()

	if !strings.Contains(prompt, "CONTINUING") {
		t.Errorf("the agent must be told it is continuing, not starting:\n%s", prompt)
	}
	if !strings.Contains(prompt, "and rename the helper") {
		t.Errorf("the human's own instruction must reach the agent:\n%s", prompt)
	}
	// The findings, with where to look and why.
	if !strings.Contains(prompt, "a.txt") || !strings.Contains(prompt, "it is already in helpers.go") {
		t.Errorf("the findings must reach the agent with their location and reason:\n%s", prompt)
	}
	// …but not the reviewer's reasoning.
	if strings.Contains(prompt, reviews[0].Reasoning) && reviews[0].Reasoning != "" {
		t.Errorf("the reviewer's REASONING must not reach the agent being graded:\n%s", prompt)
	}
}

// A continuation applies to the attempt it was asked for. A Task that failed and
// was retried must not silently inherit "you are fixing these findings" from an
// attempt two ago.
func TestRefine_IsConsumedByTheDispatchThatUsesIt(t *testing.T) {
	repo := gitRepo(t)
	svc, store, task := verifiedTask(t, repo, StubVerifyRunner{Pass: true}, "a.txt")

	if _, err := svc.RefineTask(task.ID, RefineRequest{Note: "fix it"}); err != nil {
		t.Fatalf("RefineTask: %v", err)
	}
	if armed, _ := store.GetTask(task.ID); armed.RefineFrom == "" {
		t.Fatal("precondition: the task should be armed to continue")
	}
	if _, err := svc.DispatchTask(task.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	after, _ := store.GetTask(task.ID)
	if after.RefineFrom != "" || after.RefineReview != "" || after.RefineNote != "" {
		t.Errorf("the continuation survived its dispatch (%+v) — the next attempt would "+
			"inherit instructions nobody gave it", after)
	}
}

// A review belonging to a DIFFERENT task must be refused: refining T-2 against
// T-1's findings would hand an agent instructions about code it is not looking
// at.
func TestRefine_RefusesAReviewOfAnotherTask(t *testing.T) {
	repo := gitRepo(t)
	svc, _, task := verifiedTask(t, repo, StubVerifyRunner{Pass: true}, "a.txt")

	_, err := svc.RefineTask(task.ID, RefineRequest{ReviewID: "RV-999"})
	if err == nil {
		t.Fatal("a review that is not this task's must be refused")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// Nothing to continue FROM is a dispatch, not a refine, and saying so is better
// than arming a continuation that behaves like an ordinary attempt.
func TestRefine_RefusesWithNoArtifact(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)
	task, err := svc.CreateTask(CreateTaskRequest{Project: "app", Objective: "do work"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RefineTask(task.ID, RefineRequest{Note: "fix it"}); err == nil {
		t.Fatal("a task with no artifact has nothing to continue from")
	}
}
