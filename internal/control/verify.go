// Copyright (C) 2026 Techdelight BV

package control

import "context"

// VerifySpec is the input to a VerifyRunner: the committed artifact to check, its
// pinned base, and the frozen acceptance policy (the commands to run). In
// Sprint 57 the real runner checks out HeadSHA into a clean container and runs
// Policy.Checks; here it is a stub.
type VerifySpec struct {
	TaskID      string
	JobID       string
	Project     string
	RepoDir     string
	BaseSHA     string
	HeadSHA     string // the artifact's committed tree (output_snapshot)
	Branch      string
	Policy      AcceptancePolicy // frozen at base_sha
	ImageDigest string           // pinned project image (sha256:...); may be "" if uncaptured
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
