// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/techdelight/daedalus/internal/executor"
)

// VerifierEnvPolicy is the explicit, documented environment the clean verifier
// runs in (§6): the verifier is hermetic-ish, NOT the project's dev environment.
// The policy is expressed both by what it sets (network off) and — deliberately —
// by what it does NOT mount: no ambient credentials, no inherited /opt/tools, no
// project caches. Verification is a decision, not an accident.
type VerifierEnvPolicy struct {
	Network   string // docker --network; default "none" (off)
	Workspace string // container mount target for the clean checkout; default "/workspace"
}

// DefaultVerifierEnvPolicy is network-off, workspace at /workspace, nothing else
// mounted.
func DefaultVerifierEnvPolicy() VerifierEnvPolicy {
	return VerifierEnvPolicy{Network: "none", Workspace: "/workspace"}
}

// DockerRunArgs builds the `docker` argv to run one check command in the pinned
// image with the clean checkout mounted read-write at the policy workspace. Pure
// (no side effects) so the policy is host-testable without Docker: assert the
// isolation flags are present and that no credential/tools mounts leak in.
func (p VerifierEnvPolicy) DockerRunArgs(image, hostCheckoutDir, shellCmd string) []string {
	net := p.Network
	if net == "" {
		net = "none"
	}
	ws := p.Workspace
	if ws == "" {
		ws = "/workspace"
	}
	// Only the clean checkout is mounted. No -v for creds, no -v for /opt/tools,
	// no --env-file, no host home. `--rm` so nothing persists between checks.
	//
	// --entrypoint sh is load-bearing, not stylistic. The project image's
	// ENTRYPOINT is entrypoint.sh, which has no `exec "$@"` branch: it seeds
	// runner config, patches trust keys, injects MCP servers, and then execs the
	// AGENT with whatever arguments it was given. Without this override the check
	// command is not executed at all — it arrives as argv to `claude`, and the
	// verifier grades a container that never ran the check. The clean room wants
	// none of that startup work anyway: verification is a decision about a
	// checkout, not a session.
	return []string{
		"run", "--rm",
		"--network", net,
		"--entrypoint", "sh",
		"-v", hostCheckoutDir + ":" + ws,
		"-w", ws,
		image,
		"-c", shellCmd,
	}
}

// CleanVerifier is the REAL, HOST-ONLY VerifyRunner (§6). For each verify it:
//  1. checks out the artifact's HeadSHA into a FRESH, clean worktree — separate
//     from the Job's worktree, never the worker's mutable env;
//  2. runs each frozen policy.Checks command inside a container built from the
//     project's image PINNED BY DIGEST, with only that clean checkout mounted and
//     the env policy applied (network off, no creds, no /opt/tools);
//  3. returns Passed only if every check that the change is answerable for exits
//     zero.
//
// That last clause is the baseline, and it is what makes the verdict a statement
// about the CHANGE rather than about the repository. When a project check fails
// against the artifact, the same check is run against the Job's base. If it fails
// there too it was already broken, is reported as a fact about the repository and
// does not reject the Task; if it passes there, the change is what broke it and
// the Task is rejected saying so.
//
// Measured, five times: T-8 (rejected on a warning, CSS-only change), T-11, T-13,
// T-15 and T-16 were all rejected for the state of the repository they were handed
// rather than for the work they did — T-13 and T-16 on the same `daedalus docs
// lint` error, in a SPRINTS.md neither diff touched. Every one of those verdicts
// was true about the checkout and worthless about the change, and four earlier
// fixes each removed one instance without touching the shape.
//
// It needs a Docker daemon + git and is therefore NOT unit-tested here; the
// pure pieces (DockerRunArgs, the null-agent floor, digest plumbing) are
// host-tested, and a fake VerifyRunner drives the flow tests.
type CleanVerifier struct {
	Exec   executor.Executor
	Policy VerifierEnvPolicy
}

// Verify implements VerifyRunner.
func (v CleanVerifier) Verify(_ context.Context, spec VerifySpec) VerifyOutcome {
	if spec.ImageDigest == "" {
		return VerifyOutcome{Passed: false,
			Detail: "no pinned image digest for project " + spec.Project + " — build the project image first"}
	}
	// A fresh checkout dir that does NOT pre-exist (git worktree add wants to
	// create it). Parent is a temp dir we clean up entirely.
	parent, err := os.MkdirTemp("", "daedalus-verify-"+spec.JobID+"-")
	if err != nil {
		return VerifyOutcome{Passed: false, Detail: "mktemp: " + err.Error()}
	}
	defer os.RemoveAll(parent)
	checkout := filepath.Join(parent, "checkout")

	// Clean checkout of the exact committed artifact — reproducible, isolated.
	if out, err := runGit(spec.RepoDir, "worktree", "add", "--detach", checkout, spec.HeadSHA); err != nil {
		return VerifyOutcome{Passed: false, Detail: "clean checkout failed: " + err.Error() + "\n" + strings.TrimSpace(out)}
	}
	defer func() { _, _ = runGit(spec.RepoDir, "worktree", "remove", "--force", checkout) }()

	run := func(dir, check string) (string, error) {
		return v.Exec.Output("docker", v.Policy.DockerRunArgs(spec.ImageDigest, dir, check)...)
	}

	// The base checkout is built at most once, and only if a check actually fails
	// — the common case (everything passes) costs exactly what it did before.
	var baseDir string
	defer func() {
		if baseDir != "" {
			_, _ = runGit(spec.RepoDir, "worktree", "remove", "--force", baseDir)
		}
	}()
	baseline := newBaseline(spec, parent, &baseDir)

	total := len(spec.Policy.Checks) + len(spec.TaskChecks)
	var preExisting []string

	// PROJECT checks — baselineable. See VerifySpec.TaskChecks for why the two
	// loops cannot be one.
	for i, check := range spec.Policy.Checks {
		out, err := run(checkout, check)
		if err == nil {
			continue
		}
		failed := fmt.Sprintf("check %d/%d failed: %q: %v\n%s",
			i+1, total, check, err, strings.TrimSpace(out))

		dir, berr := baseline()
		if berr != nil {
			// No baseline, so no evidence either way. The failure stands as a verdict
			// — the gate is not dropped on a maybe — but the detail says the
			// comparison could not be made, because "this change broke it" and "we
			// could not tell" must not read identically to whoever picks this up.
			return VerifyOutcome{Passed: false, PreExisting: preExisting, Detail: failed + fmt.Sprintf(
				"\n\nGRADED AGAINST THIS CHANGE, but the comparison could not be made: the same check "+
					"could not be run against the base %s (%v). If it was already failing there, this "+
					"verdict is about the repository, not about the work.", shortSHA(spec.BaseSHA), berr)}
		}
		if _, err := run(dir, check); err != nil {
			// Failing at the base too: a fact about the repository the Job was handed.
			// Recorded, not charged.
			preExisting = append(preExisting, check)
			continue
		}
		return VerifyOutcome{Passed: false, PreExisting: preExisting, Detail: failed + fmt.Sprintf(
			"\n\nThis check PASSES at the base %s, so the change is what broke it.", shortSHA(spec.BaseSHA))}
	}

	// PER-TASK checks — never baselined, and a failure here is always the verdict.
	for j, check := range spec.TaskChecks {
		out, err := run(checkout, check)
		if err != nil {
			return VerifyOutcome{Passed: false, PreExisting: preExisting, Detail: fmt.Sprintf(
				"check %d/%d failed: %q: %v\n%s\n\nThis is one of the TASK's own acceptance checks — "+
					"it describes what this change was supposed to make true, so there is no earlier "+
					"state in which failing it would be excusable.",
				len(spec.Policy.Checks)+j+1, total, check, err, strings.TrimSpace(out))}
		}
	}

	detail := fmt.Sprintf("%d check(s) passed in a clean checkout of %s against %s",
		total-len(preExisting), shortSHA(spec.HeadSHA), spec.ImageDigest)
	if len(preExisting) > 0 {
		detail += fmt.Sprintf("\n\n%d check(s) failed against BOTH the artifact and the base %s — "+
			"already broken when this Job was handed the repository, so not counted against it: %s",
			len(preExisting), shortSHA(spec.BaseSHA), strings.Join(preExisting, "; "))
	}
	return VerifyOutcome{Passed: true, Detail: detail, PreExisting: preExisting}
}

// newBaseline returns a function that yields a clean checkout of the Job's base,
// creating it on first call and reporting the same answer to every later one.
//
// Lazy because the base is only ever needed to answer "was this already broken",
// a question that only arises once something has broken; memoised because several
// checks can fail in one run and a second `git worktree add` of the same commit
// would be pure cost. dir is written through so the caller's deferred cleanup can
// see it without this owning the lifetime.
func newBaseline(spec VerifySpec, parent string, dir *string) func() (string, error) {
	var done bool
	var err error
	return func() (string, error) {
		if done {
			return *dir, err
		}
		done = true
		if spec.BaseSHA == "" || spec.BaseSHA == spec.HeadSHA {
			// head == base is the null-agent floor's business and is rejected long
			// before here; an empty base means the Job never recorded one.
			err = fmt.Errorf("the job has no distinct base to compare against")
			return "", err
		}
		candidate := filepath.Join(parent, "base")
		if out, gerr := runGit(spec.RepoDir, "worktree", "add", "--detach", candidate, spec.BaseSHA); gerr != nil {
			err = fmt.Errorf("clean checkout of the base failed: %v: %s", gerr, strings.TrimSpace(out))
			return "", err
		}
		*dir = candidate
		return candidate, nil
	}
}

// shortSHA abbreviates a sha for detail messages.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
