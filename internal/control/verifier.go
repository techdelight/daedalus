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
//  3. returns Passed only if every check exits zero (first failure short-circuits).
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

	checks := spec.Policy.Checks
	for i, check := range checks {
		args := v.Policy.DockerRunArgs(spec.ImageDigest, checkout, check)
		out, err := v.Exec.Output("docker", args...)
		if err != nil {
			return VerifyOutcome{Passed: false, Detail: fmt.Sprintf(
				"check %d/%d failed: %q: %v\n%s", i+1, len(checks), check, err, strings.TrimSpace(out))}
		}
	}
	return VerifyOutcome{Passed: true, Detail: fmt.Sprintf(
		"%d check(s) passed in a clean checkout of %s against %s", len(checks), shortSHA(spec.HeadSHA), spec.ImageDigest)}
}

// shortSHA abbreviates a sha for detail messages.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
