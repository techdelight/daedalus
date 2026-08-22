// Copyright (C) 2026 Techdelight BV

package control

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/techdelight/daedalus/core"
)

func TestReadAcceptancePolicy_DefaultWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	p, err := ReadAcceptancePolicy(dir)
	if err != nil {
		t.Fatalf("ReadAcceptancePolicy: %v", err)
	}
	if p.Hash() != DefaultAcceptancePolicy().Hash() {
		t.Errorf("absent policy should equal the default")
	}
}

func TestReadAcceptancePolicy_Declared(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".daedalus"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"checks":["go build ./...","go test ./..."],"acceptanceGlobs":["**/*_test.go","cfg.yaml"]}`
	if err := os.WriteFile(filepath.Join(dir, ".daedalus", "verify.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := ReadAcceptancePolicy(dir)
	if err != nil {
		t.Fatalf("ReadAcceptancePolicy: %v", err)
	}
	if len(p.Checks) != 2 || p.Checks[0] != "go build ./..." {
		t.Errorf("checks not parsed: %+v", p.Checks)
	}
	if p.Hash() == DefaultAcceptancePolicy().Hash() {
		t.Error("declared policy should differ from default")
	}
}

func TestReadAcceptancePolicy_PartialFillsDefaults(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".daedalus"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Only checks declared → globs fall back to the default set.
	body := `{"checks":["make ci"]}`
	if err := os.WriteFile(filepath.Join(dir, ".daedalus", "verify.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := ReadAcceptancePolicy(dir)
	if err != nil {
		t.Fatalf("ReadAcceptancePolicy: %v", err)
	}
	if len(p.AcceptanceGlobs) == 0 {
		t.Error("partial policy should inherit default globs")
	}
}

func TestPolicyHash_StableAndOrderIndependentGlobs(t *testing.T) {
	a := AcceptancePolicy{Checks: []string{"build", "test"}, AcceptanceGlobs: []string{"b", "a", "a"}}
	b := AcceptancePolicy{Checks: []string{"build", "test"}, AcceptanceGlobs: []string{"a", "b"}}
	if a.Hash() != b.Hash() {
		t.Errorf("glob order/dupes should not change the hash: %s vs %s", a.Hash(), b.Hash())
	}
	// Check order IS significant (build before test differs from test before build).
	c := AcceptancePolicy{Checks: []string{"test", "build"}, AcceptanceGlobs: []string{"a", "b"}}
	if a.Hash() == c.Hash() {
		t.Error("check order should be significant in the hash")
	}
}

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"**/*_test.go", "x_test.go", true},
		{"**/*_test.go", "a/b/x_test.go", true},
		{"**/*_test.go", "a/b/x.go", false},
		{"testdata/**", "testdata/x", true},
		{"testdata/**", "testdata/a/b.txt", true},
		{"testdata/**", "src/testdata/x", false},
		{"**/testdata/**", "src/testdata/x", true},
		{".daedalus/verify.json", ".daedalus/verify.json", true},
		{".daedalus/verify.json", ".daedalus/other.json", false},
		{"*.go", "main.go", true},
		{"*.go", "a/main.go", false}, // * does not cross a path separator
	}
	for _, c := range cases {
		if got := matchGlob(c.pattern, c.path); got != c.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

// --- integrity gate over real repos ------------------------------------------

// commitFile writes+commits a file in a repo and returns the new HEAD sha.
func commitFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "change "+name)
	out, err := runGit(dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return trim(out)
}

func TestAcceptanceFileChanges(t *testing.T) {
	repo := gitRepo(t)
	base, _ := ReadHeadSHA(repo)
	globs := DefaultAcceptancePolicy().AcceptanceGlobs

	// 1. A commit editing only non-test src → nothing to restore.
	headSrc := commitFile(t, repo, "internal/foo.go", "package foo\n")
	changes, err := AcceptanceFileChanges(repo, base, headSrc, globs)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("src-only diff touches no acceptance files, got %v", AcceptancePaths(changes))
	}

	// 2. A commit adding a _test.go → reported, and classified as ADDED so the
	// restoration removes it rather than trying to check it out from a base where
	// it does not exist.
	headTest := commitFile(t, repo, "internal/foo_test.go", "package foo\n")
	changes, err = AcceptanceFileChanges(repo, base, headTest, globs)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	var found *AcceptanceFileChange
	for i := range changes {
		if changes[i].Path == "internal/foo_test.go" {
			found = &changes[i]
		}
	}
	if found == nil {
		t.Fatalf("changes %v should include the test file", AcceptancePaths(changes))
	}
	if !found.Added {
		t.Error("a file absent at the base must be classified as added")
	}

	// 3. base == head → nothing.
	if c, _ := AcceptanceFileChanges(repo, base, base, globs); len(c) != 0 {
		t.Error("base==head touches nothing")
	}
}

// A rename is delete(old) + add(new) with --no-renames, and BOTH halves have to
// be handled: the old path restored, the new one removed. Rename detection would
// report one path and leave the other in place, which is a hole in the shape of a
// renamed test file.
func TestAcceptanceFileChanges_Rename(t *testing.T) {
	repo := gitRepo(t)
	commitFile(t, repo, "pkg/bar_test.go", "package pkg\n")
	base, _ := ReadHeadSHA(repo)
	git(t, repo, "mv", "pkg/bar_test.go", "pkg/renamed_test.go")
	git(t, repo, "commit", "-m", "rename test")
	head, _ := ReadHeadSHA(repo)

	changes, err := AcceptanceFileChanges(repo, base, head, DefaultAcceptancePolicy().AcceptanceGlobs)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	byPath := map[string]bool{}
	for _, c := range changes {
		byPath[c.Path] = c.Added
	}
	if added, ok := byPath["pkg/bar_test.go"]; !ok || added {
		t.Errorf("the old path should be reported as a deletion to restore: %v", changes)
	}
	if added, ok := byPath["pkg/renamed_test.go"]; !ok || !added {
		t.Errorf("the new path should be reported as an addition to remove: %v", changes)
	}
}

// The built-in policy is only as good as the container it runs in. A project
// that declares no `.daedalus/verify.json` is graded by `daedalus docs lint`
// inside the CLEAN VERIFIER — which mounts nothing but the checkout, so
// every command must come from the image. The CLI was missing from the image
// until 2026-08-17, which meant the default oracle could only ever exit 127 and
// reject an artifact for a reason that had nothing to do with it.
//
// This pins the two together: every buildable leaf stage that receives
// daedalus-runner must also receive the binary the default policy invokes.
func TestDefaultPolicyCommandShipsInTheImage(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Skipf("Dockerfile not readable from here: %v", err)
	}
	dockerfile := string(data)

	cmd := strings.Fields(DefaultAcceptancePolicy().Checks[0])[0]
	if cmd != "daedalus" {
		t.Fatalf("default policy now runs %q — this test pins the wrong binary", cmd)
	}

	runners := strings.Count(dockerfile, "/usr/local/bin/daedalus-runner")
	clis := strings.Count(dockerfile, "COPY --chown=claude:claude daedalus /usr/local/bin/daedalus\n")
	if clis != runners {
		t.Errorf("%d stage(s) COPY daedalus-runner but %d COPY the daedalus CLI; "+
			"a stage without it cannot run the built-in acceptance policy", runners, clis)
	}
}

// TestDefaultPolicyDoesNotGateOnAdvisoryFindings derives, from this repository's
// own documents, that the built-in acceptance policy must not treat a linter
// warning as a failure.
//
// The reasoning is deliberately not a comparison against a remembered string. It
// reads the real ROADMAP/SPRINTS, asks the linter what it actually finds, and
// only then makes a claim — so it says something true about the policy as an
// ORACLE rather than about the spelling of a constant. If the repository's docs
// ever carry a genuine error the premise no longer holds and the test stands
// down on its own, because a default policy SHOULD reject that.
//
// The defect this pins, measured 2026-08-18: the default check was `daedalus
// docs lint --ci`, `--ci` fails on warnings, and a roadmap between milestones —
// a supported state — emits exactly one warning and zero errors. Task T-8 was
// rejected by it having changed nothing but CSS, and every other Task in this
// repository would have been rejected identically.
func TestDefaultPolicyDoesNotGateOnAdvisoryFindings(t *testing.T) {
	roadmap, err := os.ReadFile(filepath.Join("..", "..", "ROADMAP.md"))
	if err != nil {
		t.Skipf("ROADMAP.md not readable from here: %v", err)
	}
	sprints, err := os.ReadFile(filepath.Join("..", "..", "SPRINTS.md"))
	if err != nil {
		t.Skipf("SPRINTS.md not readable from here: %v", err)
	}

	var errs, warns int
	for _, f := range core.ValidateDocs(
		core.ParseMilestones(string(roadmap)),
		core.ParseSprints(string(sprints)),
	) {
		switch f.Severity {
		case core.SeverityError:
			errs++
		case core.SeverityWarning:
			warns++
		}
	}
	if errs > 0 {
		t.Skipf("repo documents carry %d error(s) — a default policy is meant to fail on those", errs)
	}
	if warns == 0 {
		t.Skip("repo documents are warning-free right now — the premise cannot be observed")
	}

	// Premise established: this repository lints clean apart from advisory
	// findings, so the built-in oracle must verify it.
	for _, check := range DefaultAcceptancePolicy().Checks {
		if !strings.Contains(check, "docs lint") {
			continue
		}
		for _, fatal := range []string{"--ci", "--strict"} {
			if strings.Contains(check, fatal) {
				t.Errorf("default policy check %q carries %s, which fails on warnings; "+
					"this repository lints to %d warning(s) and %d error(s), so the "+
					"built-in oracle would reject every Task in it regardless of the "+
					"work the Task did", check, fatal, warns, errs)
			}
		}
	}
}

// RESTORATION IS THE PROTECTION, and this is the test that carries the argument.
//
// A Job that deletes the assertion failing it must gain nothing. Before this, the
// plane refused such a Job by reading its diff — which also refused the Job that
// added a test, because a diff cannot tell the two apart. Now the edit is simply
// undone before grading, so the neutered file is not what runs.
func TestRestoreAcceptanceFiles_UndoesWhatTheJobDidToTheOracle(t *testing.T) {
	repo := gitRepo(t)
	commitFile(t, repo, "pkg/keep_test.go", "package pkg\n// the assertion that fails\n")
	base, _ := ReadHeadSHA(repo)

	// The Job: neuters one test, deletes another, adds a third, and edits real
	// source. Only the last of those should survive into the graded tree.
	commitFile(t, repo, "pkg/keep_test.go", "package pkg\n// gutted\n")
	commitFile(t, repo, "pkg/added_test.go", "package pkg\n// a new test\n")
	commitFile(t, repo, "pkg/real.go", "package pkg\n// the actual work\n")
	git(t, repo, "rm", "-q", "pkg/keep_test.go")
	git(t, repo, "commit", "-m", "job: delete the inconvenient test")
	head, _ := ReadHeadSHA(repo)

	checkout := filepath.Join(t.TempDir(), "checkout")
	if out, err := runGit(repo, "worktree", "add", "--detach", checkout, head); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}
	defer runGit(repo, "worktree", "remove", "--force", checkout)

	changes, err := AcceptanceFileChanges(repo, base, head, DefaultAcceptancePolicy().AcceptanceGlobs)
	if err != nil {
		t.Fatal(err)
	}
	if err := RestoreAcceptanceFiles(checkout, base, changes); err != nil {
		t.Fatalf("RestoreAcceptanceFiles: %v", err)
	}

	// The deleted test is back, with its original content — not the gutted one.
	restored, err := os.ReadFile(filepath.Join(checkout, "pkg", "keep_test.go"))
	if err != nil {
		t.Fatalf("the deleted acceptance file was not restored: %v", err)
	}
	if !strings.Contains(string(restored), "the assertion that fails") {
		t.Errorf("restored content is the Job's, not the base's:\n%s", restored)
	}

	// The added test is gone. It is removed rather than kept because "add a file
	// that changes how the suite runs" is a real move — a Go TestMain that exits 0,
	// a conftest.py, a jest setup file — and keeping additions would leave exactly
	// the hole the freeze exists to close.
	if _, err := os.Stat(filepath.Join(checkout, "pkg", "added_test.go")); !os.IsNotExist(err) {
		t.Error("an added acceptance file must not survive into the graded tree")
	}

	// The actual work is untouched. This is the half the old gate destroyed: the
	// change is still graded, in full.
	work, err := os.ReadFile(filepath.Join(checkout, "pkg", "real.go"))
	if err != nil || !strings.Contains(string(work), "the actual work") {
		t.Errorf("non-oracle work must survive restoration: %v %s", err, work)
	}
}

// Restoring nothing is not an error, and must not cost a git call. The common
// case is a Job that never touched an acceptance file at all.
func TestRestoreAcceptanceFiles_NoChangesIsANoop(t *testing.T) {
	if err := RestoreAcceptanceFiles(t.TempDir(), "deadbeef", nil); err != nil {
		t.Errorf("no changes should be a no-op, got %v", err)
	}
}
