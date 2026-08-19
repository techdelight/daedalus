// Copyright (C) 2026 Techdelight BV

package core

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Git for a container whose /workspace is a LINKED WORKTREE.
//
// THE DEFECT THIS EXISTS FOR. A linked worktree's `.git` is not a directory —
// it is a one-line file holding an ABSOLUTE HOST PATH:
//
//	gitdir: /home/you/src/project/.git/worktrees/J-12
//
// Bind-mounting only the worktree therefore gives the container a checkout whose
// every git command fails identically, because the path in that pointer does not
// exist inside it:
//
//	fatal: not a git repository: /home/you/src/project/.git/worktrees/J-12
//
// This was inferred from the mount configuration and filed as a gap; a real
// control-plane Job then hit it and reported it verbatim. It is worse than "no
// git" because the checkout LOOKS like a repository: an agent opens it, finds
// every command fatal, and reasonably concludes it cannot do the work. The Job
// exits 0 having written nothing, the plane's capture finds a clean tree, and
// verification rejects it on the null-agent floor — a correct verdict about a
// Job that was blocked before it started, and one that says nothing about why.
//
// THE FIX. Mount the repository's common `.git` at a fixed container path and
// shadow `/workspace/.git` with a pointer file that names it. Two properties are
// worth stating:
//
//   - The container path is FIXED (/gitcommon), not the host path. Mounting the
//     common dir over its own host path would work too, and would put a host
//     directory layout inside a container that has no other reason to know one.
//     `commondir` inside the admin dir is relative (`../..`), so the common dir
//     relocates cleanly.
//   - The host's own `.git` pointer is NOT rewritten. It is a file inside the
//     bind-mounted worktree, so editing it would change what the HOST sees — and
//     the control plane runs `git -C <worktree>` on the host to capture the Job's
//     tree. Shadowing it with a separate file leaves both correct.
//
// READ-ONLY, AND THAT IS THE WHOLE POSTURE. The mount is of the developer's real
// object store and refs — the Job's branch already lives there. Read-only, every
// question an agent actually needs to ask still answers: status, diff, log, show,
// blame, ls-files, rev-parse. Writes are refused by the kernel, with git's own
// message ("insufficient permission for adding an object to repository
// database"). Nothing about the developer's repository is reachable for writing
// by a headless agent acting on an objective the plane treats as untrusted — no
// refs to move, no objects to add, no push. The commit is not the agent's job
// anyway: the plane captures the tree itself, on the host, when the Job ends.
//
// The ADMIN dir is left read-only with everything else. It was tested writable
// first, on the theory that the index would need refreshing; it does not — git
// degrades silently and correctly. Leaving it read-only also removes the one
// write that was left: an agent could otherwise move its worktree's HEAD to
// another branch, and the plane would capture onto a branch other than the one it
// recorded on the artifact.

// ContainerGitCommon is where a linked worktree's shared repository is mounted.
// Fixed, so nothing about the host's directory layout enters the container.
const ContainerGitCommon = "/gitcommon"

// LinkedWorktree is a checkout whose `.git` is a pointer file, resolved to the
// two host directories a container needs to see.
type LinkedWorktree struct {
	// AdminDir is <repo>/.git/worktrees/<name> — this worktree's own HEAD, index
	// and logs.
	AdminDir string
	// CommonDir is <repo>/.git — the shared object store and refs.
	CommonDir string
}

// ReadLinkedWorktree resolves dir as a linked worktree, reporting false for an
// ordinary checkout (whose `.git` is a directory), for a plain directory, and for
// anything it cannot make sense of.
//
// False is the safe answer in every one of those cases: it means "add no mounts",
// which is exactly what a normal project wants, and it is what the code did
// before this existed.
func ReadLinkedWorktree(dir string) (LinkedWorktree, bool) {
	pointer := filepath.Join(dir, ".git")
	info, err := os.Lstat(pointer)
	if err != nil || !info.Mode().IsRegular() {
		return LinkedWorktree{}, false // ordinary checkout, or no repository at all
	}
	raw, err := os.ReadFile(pointer)
	if err != nil {
		return LinkedWorktree{}, false
	}
	admin := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(raw)), "gitdir:"))
	if admin == "" || admin == strings.TrimSpace(string(raw)) {
		return LinkedWorktree{}, false // not a `gitdir:` pointer
	}
	if !filepath.IsAbs(admin) {
		admin = filepath.Join(dir, admin)
	}
	admin = filepath.Clean(admin)

	// `commondir` is what makes the common directory relocatable: git writes it
	// relative (`../..`), so resolving it here is also what proves this is a
	// worktree admin directory rather than some other file that happened to start
	// with "gitdir:".
	rel, err := os.ReadFile(filepath.Join(admin, "commondir"))
	if err != nil {
		return LinkedWorktree{}, false
	}
	common := strings.TrimSpace(string(rel))
	if !filepath.IsAbs(common) {
		common = filepath.Join(admin, common)
	}
	common = filepath.Clean(common)
	if info, err := os.Stat(common); err != nil || !info.IsDir() {
		return LinkedWorktree{}, false
	}
	return LinkedWorktree{AdminDir: admin, CommonDir: common}, true
}

// ContainerAdminDir is where this worktree's admin directory lands inside the
// container, once CommonDir is mounted at ContainerGitCommon.
//
// path.Join, not filepath.Join: this is a path in the CONTAINER, and a Windows
// host building it with backslashes would produce a pointer no Linux git can
// follow.
func (w LinkedWorktree) ContainerAdminDir() string {
	return path.Join(ContainerGitCommon, "worktrees", filepath.Base(w.AdminDir))
}

// Pointer is the `.git` file the container sees in place of the host's.
func (w LinkedWorktree) Pointer() string {
	return "gitdir: " + w.ContainerAdminDir() + "\n"
}

// WorktreeGitMounts returns the bind mounts that make git work inside the
// container, given a host file already written with Pointer() as its contents.
//
// pointerFile MUST live outside the worktree. Anything inside it is part of the
// tree the plane captures as the Job's artifact — `git add -A` would stage it,
// and the Job would ship a file it never wrote.
func WorktreeGitMounts(w LinkedWorktree, pointerFile string) []string {
	return []string{
		// The shared repository, read-only: history to read, nothing to damage.
		"-v", w.CommonDir + ":" + ContainerGitCommon + ":ro",
		// A pointer that resolves inside the container, shadowing the host's.
		"-v", pointerFile + ":/workspace/.git:ro",
	}
}
