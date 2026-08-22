// Copyright (C) 2026 Techdelight BV

package control

import "context"

// VerifySpec is the input to a VerifyRunner: the committed artifact to check, its
// pinned base, and the frozen acceptance policy (the commands to run). In
// Sprint 57 the real runner checks out HeadSHA into a clean container and runs
// Policy.Checks; here it is a stub.
type VerifySpec struct {
	TaskID  string
	JobID   string
	Project string
	RepoDir string
	// BaseSHA is the JOB's base — the tree the Job was actually handed — not the
	// Task's, which `reverify --amended` can re-pin to a commit the Job never saw.
	// It is what the baseline in CleanVerifier measures against, and the whole
	// value of that baseline depends on it being the Job's starting point: against
	// a re-pinned Task base, a check that trunk fixed after the fact would read as
	// a check this change broke. Same reasoning as the integrity gate's, which
	// picked the Job's base for the same reason.
	BaseSHA string
	HeadSHA string // the artifact's committed tree (output_snapshot)
	Branch  string
	// Policy is the PROJECT's policy, frozen at base_sha. Its checks describe the
	// repository's health, so they are baselineable.
	Policy AcceptancePolicy
	// TaskChecks are the Task's OWN acceptance commands, kept apart from Policy
	// rather than appended to it — the split is the load-bearing part.
	//
	// A project check ("go test ./...") asserts something that was true before the
	// change and must still be true after it, so "it was already failing" is a real
	// answer. A per-task check asserts something the change was supposed to MAKE
	// true, so it is EXPECTED to fail at the base — that is what it is for.
	// Baselining one would read "the feature does not work at the base either" as
	// an excuse and pass a Job for not doing its job, which is the exact inverse of
	// the bug the baseline exists to fix.
	TaskChecks  []string
	ImageDigest string // pinned project image (sha256:...); may be "" if uncaptured
}

// ImageDigester captures a project's image identity as an immutable sha256:
// digest (not a mutable tag), so the verifier runs the artifact in the same
// environment it was authored against (§6). It is the Docker-dependent seam
// behind an interface: the real impl runs `docker image inspect`; tests inject a
// fake. Digest may return "" (with nil error) when no image is available yet.
type ImageDigester interface {
	Digest(project string) (string, error)
}

// VerifyOutcome is a VerifyRunner's verdict. Passed drives candidate/verifying →
// verified; otherwise → rejected. Detail is recorded on the transition event.
type VerifyOutcome struct {
	Passed bool
	Detail string
	// PreExisting names the project checks that failed against the artifact AND
	// against the base — already broken when the Job was handed the repository.
	//
	// They do not make Passed false, and that is the point: a verdict is a
	// statement about a change, and a check that was failing before the change
	// says nothing about it. They are not discarded either, because somebody still
	// has to fix them — they ride along as a fact about the repository, reported
	// next to the verdict rather than as one.
	PreExisting []string
	// OracleUnrestorable is set when the verifier could not put the frozen
	// acceptance files back before grading. It is NOT a statement about the change
	// — nothing was graded — so the caller rejects it as an integrity failure
	// rather than as a failed check, and that rejection stays unappealable: a tree
	// whose oracle could not be normalised is one whose verdict would mean nothing,
	// and re-grading it would produce the same nothing.
	OracleUnrestorable bool
}

// VerifyRunner runs the project's frozen verify policy against an artifact in an
// environment the worker cannot influence (§6). It is the seam the real
// clean-verifier container (Sprint 57) plugs into; a stub stands in for now, so
// the whole plane-owned verify flow is host-testable without Docker.
type VerifyRunner interface {
	Verify(ctx context.Context, spec VerifySpec) VerifyOutcome
}

// StubVerifyRunner is a Docker-free VerifyRunner for tests and the current
// daemon (the real verifier lands in Sprint 57). It returns a fixed verdict.
// Exported so the daemon can select it via DAEDALUS_CONTROL_FAKE_VERIFY.
type StubVerifyRunner struct {
	Pass   bool
	Detail string
}

// Verify implements VerifyRunner.
func (r StubVerifyRunner) Verify(_ context.Context, _ VerifySpec) VerifyOutcome {
	detail := r.Detail
	if detail == "" {
		if r.Pass {
			detail = "stub verifier: pass"
		} else {
			detail = "stub verifier: fail"
		}
	}
	return VerifyOutcome{Passed: r.Pass, Detail: detail}
}
