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

func TestDiffTouchesAcceptanceFiles(t *testing.T) {
	repo := gitRepo(t)
	base, _ := ReadHeadSHA(repo)
	globs := DefaultAcceptancePolicy().AcceptanceGlobs

	// 1. A commit editing only non-test src → not touched.
	headSrc := commitFile(t, repo, "internal/foo.go", "package foo\n")
	touched, files, err := DiffTouchesAcceptanceFiles(repo, base, headSrc, globs)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if touched {
		t.Errorf("src-only diff should not touch acceptance files, matched %v", files)
	}

	// 2. A commit adding a _test.go → touched.
	headTest := commitFile(t, repo, "internal/foo_test.go", "package foo\n")
	touched, files, err = DiffTouchesAcceptanceFiles(repo, base, headTest, globs)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !touched {
		t.Error("adding a _test.go should touch acceptance files")
	}
	found := false
	for _, f := range files {
		if f == "internal/foo_test.go" {
			found = true
		}
	}
	if !found {
		t.Errorf("matched files %v should include the test file", files)
	}

	// 3. base == head → false.
	if touched, _, _ := DiffTouchesAcceptanceFiles(repo, base, base, globs); touched {
		t.Error("base==head should never be touched")
	}
}

func TestDiffTouchesAcceptanceFiles_Rename(t *testing.T) {
	repo := gitRepo(t)
	// Seed a test file, then rename it away — the rename still counts as touching
	// an acceptance path (the old path matched).
	commitFile(t, repo, "pkg/bar_test.go", "package pkg\n")
	base, _ := ReadHeadSHA(repo)
	git(t, repo, "mv", "pkg/bar_test.go", "pkg/renamed.go")
	git(t, repo, "commit", "-m", "rename test away")
	head, _ := ReadHeadSHA(repo)

	touched, files, err := DiffTouchesAcceptanceFiles(repo, base, head, DefaultAcceptancePolicy().AcceptanceGlobs)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !touched {
		t.Errorf("renaming a _test.go should be caught, matched=%v", files)
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
