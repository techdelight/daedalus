// Copyright (C) 2026 Techdelight BV

package control

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// acceptanceFile is the project-declared verify policy, read from a checkout at
// <checkout>/.daedalus/verify.json. It is deliberately JSON (stdlib, no new deps,
// trivially testable) rather than YAML.
//
// Example .daedalus/verify.json:
//
//	{
//	  "checks": ["go build ./...", "go test ./...", "daedalus docs lint"],
//	  "acceptanceGlobs": ["**/*_test.go", "testdata/**", ".daedalus/verify.json"]
//	}
const acceptanceFile = ".daedalus/verify.json"

// AcceptancePolicy is a project's verify contract: the check commands the clean
// verifier will run (§6) and the acceptance-file globs that make up the ORACLE —
// "the oracle must live outside the agent's write scope".
type AcceptancePolicy struct {
	// Checks are the commands the (Sprint-57) clean verifier runs, in order.
	Checks []string `json:"checks"`
	// AcceptanceGlobs are the paths that GRADE the work: tests, fixtures, and the
	// verify config itself.
	//
	// A Job's edits to them are undone in the verifier's clean checkout before any
	// check runs, so the artifact is judged by the oracle frozen at base_sha. They
	// used to reject the Job outright instead; the rule was the same and the
	// enforcement could not tell a test being added from a test being neutered.
	AcceptanceGlobs []string `json:"acceptanceGlobs"`
}

// DefaultAcceptancePolicy is the built-in used when a project declares no
// .daedalus/verify.json.
//
// Checks: daedalus is language-agnostic, so it cannot know a project's build/test
// command generically — `daedalus docs lint` is the one universally meaningful
// check, and projects are expected to declare build+tests in .daedalus/verify.json.
//
// It runs WITHOUT `--ci`, and the distinction is the whole point of this comment.
// The linter grades on two severities: an error means a document is malformed,
// a warning means it is well-formed but says something worth noticing. `--ci`
// collapses that distinction and fails on either. As the acceptance oracle for
// every project that has declared nothing, this policy must gate on what is
// broken, not on what is merely remarked upon — otherwise a Task is rejected for
// the state of the repository it was handed rather than for the work it did.
//
// Measured, 2026-08-18: with `--ci` this rejected T-8 on "no milestone is marked
// (In Progress)" — 0 errors, 1 warning — a supported, deliberate roadmap state
// that `docs lint` itself exits 0 on. The Task had changed only CSS. Every Task
// in the repository would have been rejected identically, and the verdict said
// nothing whatever about any of them.
//
// A project that genuinely wants warnings fatal can still say so: `--ci` in its
// own .daedalus/verify.json is one line. The default must not assume it.
//
// AcceptanceGlobs: the conventional test/fixture locations plus the verify config
// itself. A Job's edits to any of them are RESTORED to the base's version before
// grading, so the work is judged by the oracle it was given rather than by one it
// wrote.
//
// WHICH FILES BELONG IN THAT LIST, because getting it wrong is easy in both
// directions and this repository managed both at once.
//
// A glob should name a file that ENCODES THE REQUIREMENT INDEPENDENTLY of the
// work being graded. That is a narrower thing than "a file the checks read", and
// much narrower than "a file that looks like a test".
//
//   - `go test ./...` in the checks → `**/*_test.go` belongs. The test states the
//     requirement, and a Job that rewrites it has changed what it is being asked
//     to do.
//   - `daedalus docs lint` in the checks → ROADMAP.md and SPRINTS.md do NOT
//     belong, even though they are exactly what that check reads. The requirement
//     lives in the LINTER, not in the documents; a Job making the documents
//     well-formed is complying with the check, not evading it. Freezing them
//     would restore away the very work being asked for and leave the check
//     grading the base's documents — a green verdict about nothing.
//   - The policy file itself always belongs. Swapping the checks swaps the oracle
//     whatever the checks are.
//
// This default is a good guess for a project that will declare real build/test
// commands, and it is deliberately NOT a good guess for the default CHECK above
// — a project graded only by `docs lint` should declare a narrower set, as this
// repository now does in its own `.daedalus/verify.json`.
//
// The honest limit: a check implemented IN the repository can be weakened by the
// work. `docs lint`'s rules live in core/doclint.go and core/validate.go, so a
// Job could soften them and pass. Freezing them is not the answer — they are
// ordinary source that legitimate tasks change — and the real answer is a
// host-side held-out oracle the Job never sees (backlog #76). Recorded here
// rather than papered over.
func DefaultAcceptancePolicy() AcceptancePolicy {
	return AcceptancePolicy{
		Checks: []string{"daedalus docs lint"},
		AcceptanceGlobs: []string{
			"**/*_test.go",
			"**/test/**",
			"**/tests/**",
			"**/testdata/**",
			"**/*.spec.*",
			"**/*.test.*",
			".daedalus/verify.json",
		},
	}
}

// ReadAcceptancePolicy reads the verify policy from a checkout directory (its
// working tree). A missing .daedalus/verify.json yields the default policy (not
// an error). This is the pure reader the sprint requires.
func ReadAcceptancePolicy(checkoutDir string) (AcceptancePolicy, error) {
	data, err := os.ReadFile(filepath.Join(checkoutDir, filepath.FromSlash(acceptanceFile)))
	if os.IsNotExist(err) {
		return DefaultAcceptancePolicy(), nil
	}
	if err != nil {
		return AcceptancePolicy{}, err
	}
	return parseAcceptance(data)
}

// ReadAcceptancePolicyAt reads the verify policy as committed at a specific sha,
// via `git show <sha>:<file>` — independent of any later working-tree edits. This
// is what freezes the policy at base_sha (the file at that commit is immutable),
// giving the freeze semantics. A file absent at that sha yields the default.
func ReadAcceptancePolicyAt(repoDir, sha string) (AcceptancePolicy, error) {
	out, err := runGit(repoDir, "show", sha+":"+acceptanceFile)
	if err != nil {
		// Most common: the path does not exist at that commit — fall back to the
		// default policy. (Any real git failure also degrades to default here;
		// the frozen hash still captures "the default policy at base_sha".)
		return DefaultAcceptancePolicy(), nil
	}
	return parseAcceptance([]byte(out))
}

// parseAcceptance unmarshals the policy JSON, filling empty fields from the
// default so a partial file (e.g. only "checks") still gets sane globs.
func parseAcceptance(data []byte) (AcceptancePolicy, error) {
	var p AcceptancePolicy
	if err := json.Unmarshal(data, &p); err != nil {
		return AcceptancePolicy{}, err
	}
	def := DefaultAcceptancePolicy()
	if len(p.Checks) == 0 {
		p.Checks = def.Checks
	}
	if len(p.AcceptanceGlobs) == 0 {
		p.AcceptanceGlobs = def.AcceptanceGlobs
	}
	return p, nil
}

// normalized returns a canonical form: each entry trimmed and non-empty; checks
// keep declared order (sequence is semantically meaningful — build before test);
// globs are sorted + de-duplicated (a set). Used for a stable hash.
func (p AcceptancePolicy) normalized() AcceptancePolicy {
	return AcceptancePolicy{
		Checks:          trimNonEmpty(p.Checks),
		AcceptanceGlobs: sortedUnique(trimNonEmpty(p.AcceptanceGlobs)),
	}
}

// Hash is a stable content hash of the normalized policy (commands + globs). It
// is what gets frozen on the Task at base_sha; a later working-tree edit to the
// policy does not change this value.
func (p AcceptancePolicy) Hash() string {
	data, _ := json.Marshal(p.normalized())
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// AcceptanceFileChange is one acceptance-file path the Job's diff touched, with
// what it did to it.
type AcceptanceFileChange struct {
	Path string
	// Added is true when the path does not exist at the base at all. The
	// distinction matters only to the restoration below — an added file is
	// removed, an edited or deleted one is put back — and NOT to whether the Job
	// is trusted, because that question is no longer asked.
	Added bool
}

// AcceptanceFileChanges lists every acceptance-file path the diff base..head
// touches, classified for restoration.
//
// WHY THIS REPLACED A REFUSAL. The integrity gate used to reject any Job whose
// diff touched a frozen acceptance file, on the argument that a Job which can
// edit the files that grade it can pass by changing the grader. The argument is
// right; the enforcement was reading a diff and guessing intent from it, and a
// diff cannot tell "added a test that pins the fix" from "deleted the assertion
// that was failing" — they are the same operation to anything reading file names.
//
// So the gate refused BOTH, which made the repository's own practice (every
// change lands with a test) unlandable by any Job, while a determined agent lost
// nothing it could not have achieved by simply not touching the tests.
//
// The fix is to stop deciding from the diff. The verifier now RESTORES every
// acceptance file to its base state before grading, so the artifact is judged by
// the oracle as it was frozen: neutering a test has no effect because the
// neutered file is not what runs, and adding one has no effect either. Cheating
// becomes ineffective rather than forbidden, which is the same protection
// without the collateral refusal.
func AcceptanceFileChanges(repoDir, baseSHA, headSHA string, globs []string) ([]AcceptanceFileChange, error) {
	if baseSHA == "" || headSHA == "" || baseSHA == headSHA {
		return nil, nil
	}
	// --no-renames so a rename surfaces as delete(old)+add(new): the old path is
	// then restored and the new one removed, which is what "the oracle as frozen"
	// means for a renamed acceptance file. Rename detection would report one path
	// and leave the other unhandled.
	out, err := runGit(repoDir, "diff", "--no-renames", "--name-status", baseSHA, headSHA)
	if err != nil {
		return nil, wrapGit("git diff --name-status", out, err)
	}
	var changes []AcceptanceFileChange
	for _, line := range strings.Split(out, "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), "\t", 2)
		if len(fields) != 2 {
			continue
		}
		status, path := strings.TrimSpace(fields[0]), strings.TrimSpace(fields[1])
		if path == "" || !pathMatchesAny(path, globs) {
			continue
		}
		changes = append(changes, AcceptanceFileChange{
			Path: path, Added: strings.HasPrefix(status, "A"),
		})
	}
	return changes, nil
}

// Paths returns just the touched paths, for reporting.
func AcceptancePaths(changes []AcceptanceFileChange) []string {
	out := make([]string, 0, len(changes))
	for _, c := range changes {
		out = append(out, c.Path)
	}
	return out
}

// RestoreAcceptanceFiles rewrites a checkout so its acceptance files are exactly
// the base's: edited and deleted ones are put back, added ones are removed.
//
// The graded tree is then "the Job's work, judged by the frozen oracle" —
// which is what the acceptance freeze always claimed to mean and previously
// achieved by forbidding the edit instead of undoing it.
//
// ADDED files are removed rather than kept, and that is the subtle half. An
// added test looks harmless — it can only add failures to a suite, not remove
// them — but "add a file that changes how the suite runs" is a real move in most
// languages: a Go TestMain that exits 0 without running anything, a pytest
// conftest.py, a jest setup file. Keeping additions would leave exactly the hole
// the freeze exists to close, so the rule is the simple one: at grade time the
// oracle is the base's, entirely.
//
// Any failure here is reported to the caller and must fail the verification
// closed. A tree we could not normalise is a tree whose verdict means nothing.
func RestoreAcceptanceFiles(checkoutDir, baseSHA string, changes []AcceptanceFileChange) error {
	var restore []string
	for _, c := range changes {
		if c.Added {
			// os.Remove rather than `git rm`: the checks read FILES, and the index of
			// a throwaway worktree we are about to delete is not worth a subprocess.
			if err := os.Remove(filepath.Join(checkoutDir, filepath.FromSlash(c.Path))); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("removing added acceptance file %s: %w", c.Path, err)
			}
			continue
		}
		restore = append(restore, c.Path)
	}
	if len(restore) == 0 {
		return nil
	}
	args := append([]string{"checkout", baseSHA, "--"}, restore...)
	if out, err := runGit(checkoutDir, args...); err != nil {
		return wrapGit("git checkout <base> -- <acceptance files>", out, err)
	}
	return nil
}

// pathMatchesAny reports whether path matches any glob.
func pathMatchesAny(path string, globs []string) bool {
	for _, g := range globs {
		if matchGlob(g, path) {
			return true
		}
	}
	return false
}

// matchGlob matches a path against a glob supporting `**` (any path segments),
// `*` (within a segment), and `?`. Implemented by translating to an anchored
// regexp — filepath.Match lacks `**` support.
func matchGlob(pattern, name string) bool {
	re, err := regexp.Compile(globToRegexp(pattern))
	if err != nil {
		return false
	}
	return re.MatchString(name)
}

// globToRegexp translates a `**`-aware glob into an anchored regexp string.
func globToRegexp(glob string) string {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); {
		c := glob[i]
		switch c {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				if i+2 < len(glob) && glob[i+2] == '/' {
					b.WriteString("(?:.*/)?") // `**/` → optional leading dirs
					i += 3
				} else {
					b.WriteString(".*") // trailing/bare `**`
					i += 2
				}
			} else {
				b.WriteString("[^/]*") // `*` stays within a path segment
				i++
			}
		case '?':
			b.WriteString("[^/]")
			i++
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '[', ']', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	b.WriteString("$")
	return b.String()
}

func trimNonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func sortedUnique(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// wrapGit produces a readable error from a failed git invocation.
func wrapGit(what, out string, err error) error {
	msg := strings.TrimSpace(out)
	if msg == "" {
		return fmt.Errorf("%s: %v", what, err)
	}
	return fmt.Errorf("%s: %v\n%s", what, err, msg)
}

// A Task's own acceptance commands used to be APPENDED to the project policy
// here, by withTaskChecks. They now travel beside it as VerifySpec.TaskChecks,
// because the baseline has to tell the two apart — a project check may excusably
// have been failing before the change, and a per-task check may not.
//
// Both properties that merge carried are preserved by the split, in
// CleanVerifier.Verify's two loops:
//
//   - **Append, never replace.** Both loops always run, and TaskChecks cannot
//     reach Policy.Checks from a different field. There is still no request shape
//     that lowers the bar, so "this task was graded more leniently" remains a
//     state that does not exist.
//   - **Project checks run FIRST.** The verifier mounts the checkout read-write
//     and runs checks in sequence, so a command could in principle mutate the tree
//     a later one sees. The project loop precedes the task loop, so a task-supplied
//     check can still only ever sabotage itself.
