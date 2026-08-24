// Copyright (C) 2026 Techdelight BV

package control

import (
	"fmt"
	"strings"
)

// ReverifyRequest is the input to Service.ReverifyTask (and POST
// /tasks/{id}/reverify).
type ReverifyRequest struct {
	// Amended re-grades under a policy that has CHANGED since the artifact was
	// produced: the Task is re-pinned to the project's current target and its
	// acceptance policy re-frozen there, then the artifact is graded against it.
	//
	// Opt-in by name, for the same reason `retry --rebase` is: adopting a newer
	// oracle is a governance act, not a retry detail. Without it, re-verification
	// is a pure replay — the identical artifact against the identical frozen
	// policy — which is the mode that carries no trust question at all.
	Amended bool `json:"amended,omitempty"`
}

// ReverifyResult reports what re-verification did, and then what it found.
type ReverifyResult struct {
	Task Task `json:"task"`
	Job  Job  `json:"job"`
	// PreviousReason is the verdict being set aside — the answer to "what am I
	// correcting", which an operator should see before the new verdict.
	PreviousReason RejectionReason `json:"previous_reason,omitempty"`
	Amended        bool            `json:"amended"`
	Rebased        bool            `json:"rebased,omitempty"`
	BaseSHA        string          `json:"base_sha,omitempty"`
	// DefaultPolicy is true when the base this re-froze onto carries no
	// .daedalus/verify.json, so the BUILT-IN default applies. Reported because
	// re-freezing onto nothing looks exactly like re-freezing onto something, and
	// the verdict that follows is then about documents rather than the project.
	DefaultPolicy bool `json:"defaultPolicy,omitempty"`
	// Verify is the outcome of the grading itself.
	Verify VerifyResult `json:"verify"`
}

// unappealableReasons are the rejections a re-verification may NOT set aside,
// because they were statements about the ARTIFACT rather than about the harness
// or the oracle. Re-grading them would not correct a mistake; it would appeal a
// finding, and both findings here exist precisely to be unappealable.
//
// The integrity gate rejects a Job whose diff edits its own acceptance files.
// Allowing "grade that again" would let a self-grading diff through on the
// second ask, which is the entire failure the gate was built to prevent. The
// null-agent floor rejects head == base: there is no change to grade, and no
// number of re-gradings will produce one.
//
// Everything else is appealable, and deliberately so — including a plain
// verify_failed, which cannot be distinguished from the outside between "the
// code is wrong" and "the oracle was wrong". That ambiguity is why the operation
// is tiered and recorded rather than forbidden: the record is what makes a
// re-grading answerable later, and a human who re-verifies bad code simply gets
// the same verdict again at the cost of a cycle.
var unappealableReasons = map[RejectionReason]bool{
	ReasonIntegrityGate:  true,
	ReasonNullAgentFloor: true,
}

// ReverifyTask re-grades a rejected Task's existing artifact WITHOUT dispatching
// a new Job.
//
// The case for it: verification is a function of (artifact, policy, environment),
// and the artifact is immutable and content-addressed. A verdict can therefore be
// wrong for reasons that say nothing about the work — a verifier that never ran
// the check it reported on, or an acceptance policy that fails on an advisory
// finding. Before this existed the only remedy was `retry`, which dispatches a
// fresh Job and discards an artifact that was never in question; an operator with
// a broken harness had to spend an attempt to learn nothing.
//
// Nothing here re-runs the work, and nothing here can reach `verified` by a
// shorter path: the Task is returned to `candidate` and then graded by the ONE
// verification path, VerifyTask, with its every gate intact. That reuse is the
// design's load-bearing part — a second grading path would become the weaker
// oracle the moment the two drifted, and the whole point of the control plane is
// that there is exactly one thing that can say "verified".
func (s *Service) ReverifyTask(id string, req ReverifyRequest) (ReverifyResult, error) {
	return s.reverifyTask(Human(), id, req)
}

// reverifyTask is ReverifyTask with an explicit caller identity.
func (s *Service) reverifyTask(caller Caller, id string, req ReverifyRequest) (ReverifyResult, error) {
	res, err := s.prepareReverify(caller, id, req)
	if err != nil {
		return ReverifyResult{}, err
	}

	// The lock is released between returning the Task to `candidate` and grading
	// it, because VerifyTask takes s.mu itself and holds it across a container run.
	// The window is real and deliberately harmless: every operation that could act
	// on a `candidate` Task in between either grades it (which is what was asked
	// for) or cancels it, and a cancel makes the VerifyTask below refuse on state —
	// the correct outcome, reported as a refusal rather than a verdict. What cannot
	// happen is a verdict on an artifact nobody asked about, because the Task ID is
	// carried through rather than re-resolved.
	verify, err := s.VerifyTask(id, VerifyRequest{})
	if err != nil {
		return res, fmt.Errorf("re-verify %s: %w", id, err)
	}
	res.Verify = verify
	res.Task = verify.Task
	res.Job = verify.Job
	return res, nil
}

// prepareReverify is the locked half: every guard, the optional re-freeze, and
// the transition back to `candidate`. It never runs the verifier.
func (s *Service) prepareReverify(caller Caller, id string, req ReverifyRequest) (ReverifyResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, err := s.store.GetTask(id)
	if err != nil {
		return ReverifyResult{}, err
	}
	if task.State != StateRejected {
		return ReverifyResult{}, fmt.Errorf("%w: task %s is %s, not re-verifiable (want rejected)",
			ErrWrongState, id, task.State)
	}

	// What is being set aside. Read before anything moves, because it decides
	// whether anything may move at all.
	reason, err := s.store.LastRejectionReason(id)
	if err != nil {
		return ReverifyResult{}, err
	}
	if unappealableReasons[reason] {
		return ReverifyResult{}, s.refuse("task", id, EventReverify, ReasonUnappealable, fmt.Sprintf(
			"task %s was rejected as %s, which is a finding about the artifact and not about the way it was graded; "+
				"re-verifying it would appeal the finding rather than correct a fault — produce a different artifact with "+
				"`daedalus task retry %s` or `daedalus task replan %s`", id, reason, id, id))
	}

	job, ok, err := s.jobInState(id, StateRejected)
	if err != nil {
		return ReverifyResult{}, err
	}
	if !ok {
		return ReverifyResult{}, fmt.Errorf("%w: task %s has no rejected job whose artifact could be re-graded",
			ErrWrongState, id)
	}

	repoDir, err := s.projects.ProjectDir(task.Project)
	if err != nil {
		return ReverifyResult{}, err
	}

	// The artifact must still exist. Rejection removes the Job's worktree but never
	// its branch, so the commit is normally still reachable; a miss here means a
	// human deleted the branch or rewrote history. Refused rather than passed on to
	// the verifier, which would report a checkout failure as a verification
	// failure — an artifact that cannot be found has not been judged, and recording
	// otherwise would put a false verdict in an append-only log.
	if job.OutputSnapshot == "" {
		return ReverifyResult{}, fmt.Errorf("%w: job %s captured no output snapshot", ErrWrongState, job.ID)
	}
	if !commitExists(repoDir, job.OutputSnapshot) {
		return ReverifyResult{}, s.refuse("task", id, EventReverify, ReasonArtifactGone, fmt.Sprintf(
			"artifact %s of job %s is no longer reachable in %s (branch %s) — nothing is left to re-grade",
			shortSHA(job.OutputSnapshot), job.ID, task.Project, BranchName(id, job.ID)))
	}

	res := ReverifyResult{PreviousReason: reason, Amended: req.Amended, BaseSHA: task.BaseSHA}

	if req.Amended {
		updated, rebased, err := s.rebaseTaskToTip(caller, task, repoDir)
		if err != nil {
			return ReverifyResult{}, err
		}
		task, res.Rebased, res.BaseSHA = updated, rebased, updated.BaseSHA
		// Reported as well as recorded. The event note carries it for the record;
		// the operator running the command needs it NOW, because the next verdict
		// is the thing they are about to act on.
		if rebased {
			res.DefaultPolicy = !AcceptancePolicyPresentAt(repoDir, updated.BaseSHA)
		}
	}

	// Has the ORACLE moved since the verdict being set aside? A free replay is
	// justified only when nothing has: same artifact, same policy, same checks. An
	// amended check makes the next grading a NEW grading against a DIFFERENT
	// oracle, which is exactly what the `--amended` mode is charged for — and
	// without this, softening a check and replaying for free could be repeated
	// until something passed, with the budget never noticing.
	amended, err := s.store.ChecksAmendedSinceLastVerdict(id)
	if err != nil {
		return ReverifyResult{}, err
	}
	freeReplay := !req.Amended && !amended

	// The re-verify decision event. ONE place decides whether this grading is
	// discounted, and it is the `freeReplay` branch below: the discount is keyed
	// off the reason on this DECISION row (CountReviewCycles restricts its subquery
	// to `from_state = ''`), so the transitions further down cannot grant or
	// withhold it however they are annotated. Written any other way the variable
	// would read as the decision while the branch structure quietly made it, which
	// is exactly the kind of comment-shaped lie the rest of this package hunts.
	actor := governanceMetaFor(caller).Actor
	meta := EventMeta{Kind: EventReverify, Actor: actor}
	var note string
	switch {
	case freeReplay:
		meta.Reason = reason // the discount marker, and the only thing that grants it
		note = fmt.Sprintf("re-verify (replay): setting aside %s; artifact %s unchanged, "+
			"acceptance policy unchanged, checks unchanged", reason, shortSHA(job.OutputSnapshot))
	case req.Amended:
		note = fmt.Sprintf("re-verify (amended): setting aside %s; artifact %s graded under the "+
			"policy frozen at %s", reason, shortSHA(job.OutputSnapshot), shortSHA(task.BaseSHA))
	default:
		note = fmt.Sprintf("re-verify: setting aside %s; the per-task checks were amended since that "+
			"verdict, so this is a new grading against a changed oracle and costs a review cycle", reason)
	}
	// Logged BEFORE the transitions so that a crash between them leaves a record
	// that a re-grading was asked for, rather than a Task mysteriously back at
	// `candidate` with nothing to explain it.
	if err := s.store.LogDecision("task", id, meta, note); err != nil {
		return ReverifyResult{}, err
	}

	if _, err := s.store.TransitionJobWith(job.ID, StateCandidate, false, meta,
		"re-verify: artifact returned for grading"); err != nil {
		return ReverifyResult{}, err
	}
	tk, err := s.store.TransitionTaskWith(id, StateCandidate, false, meta,
		"re-verify: returned for grading")
	if err != nil {
		return ReverifyResult{}, err
	}
	res.Task, res.Job = tk, job
	return res, nil
}

// rebaseTaskToTip re-pins a Task to its project's current integration target and
// re-freezes the acceptance policy there, returning the updated Task and whether
// anything moved.
//
// Extracted from prepareRetry so `retry --rebase` and `reverify --amended` cannot
// drift: both adopt a newer oracle, so both must refuse a self-authored tip and
// both must record the same lineage. A second copy of this would be a second
// place for the Sprint-59 laundering fix to be forgotten.
func (s *Service) rebaseTaskToTip(caller Caller, task Task, repoDir string) (Task, bool, error) {
	// A pure read. The Task exists, so CreateTask adopted a target for its
	// repository already; a miss here is a fault, and adopting one would be
	// actively dangerous — re-freezing the acceptance policy against a target
	// invented from the checkout HEAD would re-open oracle laundering on precisely
	// the path Sprint 59 closed it on.
	target, err := s.Target(task.Project)
	if err != nil {
		return task, false, err
	}
	tip := target.SHA
	if tip == task.BaseSHA {
		return task, false, nil
	}
	// DEFENCE IN DEPTH, no longer the mechanism. The rebase target is now the
	// plane-owned integration ref, which a worker cannot write, so the attack this
	// guard was built for is closed structurally (target.go). The check stays
	// because it costs one merge-base and would catch a target that became
	// self-authored some other way — e.g. an operator resyncing onto a commit a Job
	// had planted on the branch.
	if err := s.refuseSelfAuthoredRebase(task, repoDir, tip); err != nil {
		return task, false, err
	}
	policy, err := ReadAcceptancePolicyAt(repoDir, tip)
	if err != nil {
		return task, false, err
	}
	// The lineage is written into the note, not just the new value. A verdict
	// produced under a policy amended AFTER the artifact existed is weaker than one
	// produced under the policy the artifact faced, and the log is the only place
	// that difference can still be seen once the task has moved on.
	note := fmt.Sprintf("rebase: %s → %s (acceptance policy re-frozen at the new base: %s → %s)",
		shortSHA(task.BaseSHA), shortSHA(tip), shortHash(task.AcceptanceHash), shortHash(policy.Hash()))
	// SAY IT WHEN THE NEW BASE CARRIES NO POLICY. This operation exists to adopt a
	// corrected oracle, and adopting the built-in default by accident is the one
	// outcome it must not perform silently — the default grades documents, so the
	// verdict that follows says nothing about the project's own bar. Recorded in
	// the note rather than refused: a project that genuinely has no policy is
	// entitled to the default, and only the operator knows which case this is.
	if !AcceptancePolicyPresentAt(repoDir, tip) {
		note += " — WARNING: " + acceptanceFile + " is not committed at the new base, so the " +
			"BUILT-IN DEFAULT policy applies and the verdict will be about that, not about this " +
			"project's own checks"
	}
	updated, err := s.store.RebaseTask(task.ID, tip, policy.Hash(), governanceMetaFor(caller), note)
	if err != nil {
		return task, false, err
	}
	return updated, true, nil
}

// commitExists reports whether sha names a commit object in repoDir.
func commitExists(repoDir, sha string) bool {
	_, err := runGit(repoDir, "cat-file", "-e", sha+"^{commit}")
	return err == nil
}

// shortHash abbreviates a policy hash for a log line, keeping the algorithm
// prefix so it stays recognisable as one.
func shortHash(h string) string {
	if h == "" {
		return "(none)"
	}
	alg, digest, found := strings.Cut(h, ":")
	if !found || len(digest) <= 12 {
		return h
	}
	return alg + ":" + digest[:12]
}
