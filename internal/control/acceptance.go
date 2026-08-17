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
//	  "checks": ["go build ./...", "go test ./...", "daedalus docs lint --ci"],
//	  "acceptanceGlobs": ["**/*_test.go", "testdata/**", ".daedalus/verify.json"]
//	}
const acceptanceFile = ".daedalus/verify.json"

// AcceptancePolicy is a project's verify contract: the check commands the clean
// verifier will run (§6) and the acceptance-file globs whose edits must
// invalidate a Job (the test-integrity gate — "the oracle must live outside the
// agent's write scope").
type AcceptancePolicy struct {
	// Checks are the commands the (Sprint-57) clean verifier runs, in order.
	Checks []string `json:"checks"`
	// AcceptanceGlobs are paths whose edits (relative to base_sha) reject a Job
	// before the verifier is even consulted.
	AcceptanceGlobs []string `json:"acceptanceGlobs"`
}

// DefaultAcceptancePolicy is the built-in used when a project declares no
// .daedalus/verify.json.
//
// Checks: daedalus is language-agnostic, so it cannot know a project's build/test
// command generically — `daedalus docs lint --ci` is the one universally
// meaningful check, and projects are expected to declare build+tests in
// .daedalus/verify.json. (The check strings are inert until the Sprint-57
// verifier container runs them; only the globs are load-bearing this sprint.)
//
// AcceptanceGlobs: the conventional test/fixture locations plus the verify config
// itself — editing any of these in a Job is a self-grading attempt and rejects it.
func DefaultAcceptancePolicy() AcceptancePolicy {
	return AcceptancePolicy{
		Checks: []string{"daedalus docs lint --ci"},
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

// DiffTouchesAcceptanceFiles reports whether the diff base..head changes any path
// matching the frozen acceptance globs — the test-integrity gate. It is pure
// git-diff logic (shelling to the host git via the existing helper): a Job that
// edits its own tests/fixtures/verify-config is self-grading and must be rejected
// before the verifier is consulted.
//
// Returns (touched, matchedPaths, err). base==head short-circuits to false.
func DiffTouchesAcceptanceFiles(repoDir, baseSHA, headSHA string, globs []string) (bool, []string, error) {
	if baseSHA == "" || headSHA == "" || baseSHA == headSHA {
		return false, nil, nil
	}
	// --no-renames disables git's default rename detection so a rename surfaces as
	// delete(old)+add(new) — a renamed test file is then caught on its old path
	// too, closing the "rename an acceptance file to dodge the gate" hole.
	// --name-only lists every changed path.
	out, err := runGit(repoDir, "diff", "--no-renames", "--name-only", baseSHA, headSHA)
	if err != nil {
		return false, nil, wrapGit("git diff --name-only", out, err)
	}
	var matched []string
	for _, line := range strings.Split(out, "\n") {
		path := strings.TrimSpace(line)
		if path == "" {
			continue
		}
		if pathMatchesAny(path, globs) {
			matched = append(matched, path)
		}
	}
	return len(matched) > 0, matched, nil
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

// withTaskChecks returns the policy with a Task's own acceptance commands
// APPENDED. Two properties are load-bearing, and both are about what a per-task
// check may not do:
//
//   - **Append, never replace.** The project's checks always run; a Task can only
//     add to the bar it is graded against. There is no request shape that lowers
//     it, so "this task was graded more leniently" is not a state that exists.
//   - **Project checks run FIRST.** The verifier mounts the checkout read-write
//     and runs the checks in sequence, so a command could in principle mutate the
//     tree a later command sees. Ordering makes that harmless: by the time a
//     task-supplied check runs, the project's own checks have already passed
//     against an unmutated checkout. A task check can therefore only ever
//     sabotage itself, which is nobody else's problem.
//
// The receiver is untouched (Checks is copied), because the caller still needs
// the frozen policy for the drift comparison and the integrity gate.
func (p AcceptancePolicy) withTaskChecks(checks []string) AcceptancePolicy {
	if len(checks) == 0 {
		return p
	}
	combined := make([]string, 0, len(p.Checks)+len(checks))
	combined = append(combined, p.Checks...)
	combined = append(combined, checks...)
	p.Checks = combined
	return p
}
