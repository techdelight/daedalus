// Copyright (C) 2026 Techdelight BV

package control

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// projectHome builds a project home under dataDir holding the given relative
// files, and returns the data dir.
func projectHome(t *testing.T, dataDir, project string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dataDir, project, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSeedJobHome_CopiesCredentialsAndConfig(t *testing.T) {
	data := t.TempDir()
	projectHome(t, data, "my-app", map[string]string{
		filepath.Join(claudeConfigDirName, ".claude.json"):      `{"mcpServers":{}}`,
		filepath.Join(claudeConfigDirName, "settings.json"):     `{"theme":"dark"}`,
		filepath.Join(claudeConfigDirName, ".credentials.json"): `{"token":"secret"}`,
	})

	if err := SeedJobHome(data, "my-app", "daedalus-job-J-9"); err != nil {
		t.Fatalf("SeedJobHome: %v", err)
	}

	jobHome := filepath.Join(data, "daedalus-job-J-9")
	for _, rel := range []string{".claude.json", "settings.json", ".credentials.json"} {
		p := filepath.Join(jobHome, claudeConfigDirName, rel)
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("%s not seeded: %v", rel, err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600 — credentials must not be readable by others", rel, fi.Mode().Perm())
		}
	}
	got, err := os.ReadFile(filepath.Join(jobHome, claudeConfigDirName, ".credentials.json"))
	if err != nil || string(got) != `{"token":"secret"}` {
		t.Errorf("credentials content = %q (err %v), want the project's", got, err)
	}
}

// Only the allow-list travels. A Job must not inherit the project's session
// transcripts, caches, or anything else the agent wrote — it is a fresh attempt
// in a clean worktree, and its home should say so.
func TestSeedJobHome_CopiesNothingOutsideTheAllowList(t *testing.T) {
	data := t.TempDir()
	projectHome(t, data, "my-app", map[string]string{
		filepath.Join(claudeConfigDirName, ".credentials.json"):     `{"token":"secret"}`,
		filepath.Join(claudeConfigDirName, "projects", "hist.json"): `["a previous session"]`,
		"notes.md":                         "scratch work from an earlier session",
		filepath.Join(".cache", "big.bin"): "cache",
	})

	if err := SeedJobHome(data, "my-app", "daedalus-job-J-1"); err != nil {
		t.Fatalf("SeedJobHome: %v", err)
	}

	jobHome := filepath.Join(data, "daedalus-job-J-1")
	for _, rel := range []string{
		"notes.md",
		filepath.Join(".cache", "big.bin"),
		filepath.Join(claudeConfigDirName, "projects", "hist.json"),
	} {
		if _, err := os.Stat(filepath.Join(jobHome, rel)); err == nil {
			t.Errorf("%s was copied into the job home; only the allow-list may travel", rel)
		}
	}
}

// The fallback location: a login that landed under ~/.claude rather than
// CLAUDE_CONFIG_DIR must still be found, because the alternative is a Job that
// dies on "Not logged in" for a reason no operator would guess.
func TestSeedJobHome_FindsCredentialsUnderDotClaude(t *testing.T) {
	data := t.TempDir()
	projectHome(t, data, "my-app", map[string]string{
		filepath.Join(".claude", ".credentials.json"): `{"token":"elsewhere"}`,
	})

	if err := SeedJobHome(data, "my-app", "daedalus-job-J-2"); err != nil {
		t.Fatalf("SeedJobHome: %v", err)
	}
	p := filepath.Join(data, "daedalus-job-J-2", ".claude", ".credentials.json")
	if _, err := os.Stat(p); err != nil {
		t.Errorf("fallback credentials not seeded: %v", err)
	}
}

// A project with no credentials at all is reported, and the message must name
// the human action that fixes it — this is the error an operator meets after a
// two-second job failure, so "no such file" would be a wasted opportunity.
func TestSeedJobHome_UnseededProjectSaysHowToFixIt(t *testing.T) {
	data := t.TempDir()
	projectHome(t, data, "my-app", map[string]string{"notes.md": "no credentials here"})

	err := SeedJobHome(data, "my-app", "daedalus-job-J-3")
	if err == nil {
		t.Fatal("expected an error for a project with no credentials")
	}
	if !strings.Contains(err.Error(), "daedalus my-app") {
		t.Errorf("error should name the fix (`daedalus my-app`), got: %v", err)
	}
}

// Seeding never blocks a dispatch: the warn wrapper swallows every failure.
func TestSeedJobHomeOrWarn_NeverPanicsOnAMissingProject(t *testing.T) {
	seedJobHomeOrWarn(t.TempDir(), "nonexistent", "daedalus-job-J-4")
	seedJobHomeOrWarn("", "my-app", "daedalus-job-J-5") // no data dir configured
}

// The host writes these paths; the CONTAINER reads them from CLAUDE_CONFIG_DIR,
// which the Dockerfile sets. If someone changes the Dockerfile, seeding would
// silently write to a directory the agent never looks in and every Job would go
// back to failing on "Not logged in" — so the two are pinned together here.
func TestJobHomeSeedPathsMatchTheDockerfile(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Skipf("Dockerfile not readable from here: %v", err)
	}
	m := regexp.MustCompile(`ENV CLAUDE_CONFIG_DIR="?([^"\s]+)"?`).FindSubmatch(data)
	if m == nil {
		t.Fatal("no ENV CLAUDE_CONFIG_DIR in the Dockerfile — seeding has no target to mirror")
	}
	if got := filepath.Base(string(m[1])); got != claudeConfigDirName {
		t.Errorf("Dockerfile CLAUDE_CONFIG_DIR basename = %q, but jobhome.go seeds %q", got, claudeConfigDirName)
	}
}

// TestSeedJobHome_ReportsALoginThatCannotBeRefreshed.
//
// A Job inherits a COPY of the project's home, so an expired login is copied as
// faithfully as a live one and seeding reports success either way. That is how
// T-15 died: seeded, dispatched, dead in four seconds with an authentication
// error, and nothing between the operator and a container's log.
func TestSeedJobHome_ReportsALoginThatCannotBeRefreshed(t *testing.T) {
	write := func(t *testing.T, dataDir, project string, refreshExpiry time.Time) {
		t.Helper()
		dir := filepath.Join(dataDir, project, claudeConfigDirName)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf(
			`{"claudeAiOauth":{"accessToken":"x","expiresAt":%d,"refreshTokenExpiresAt":%d}}`,
			// The ACCESS token is expired in both cases, deliberately: that is the
			// ordinary state of affairs and must never be what triggers a warning.
			time.Now().Add(-time.Hour).UnixMilli(), refreshExpiry.UnixMilli())
		if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// A live refresh token: seeded without comment, even though the access token
	// has expired. Warning here would cry wolf on every dispatch.
	live := t.TempDir()
	write(t, live, "app", time.Now().Add(30*24*time.Hour))
	if err := SeedJobHome(live, "app", "daedalus-job-J-1"); err != nil {
		t.Errorf("a refreshable login was reported as a problem: %v", err)
	}

	// A dead one: named, dated, with the fix in the message.
	dead := t.TempDir()
	write(t, dead, "app", time.Now().Add(-24*time.Hour))
	err := SeedJobHome(dead, "app", "daedalus-job-J-2")
	if err == nil {
		t.Fatal("an expired login seeded silently; the Job would die in seconds with no cause on the record")
	}
	for _, want := range []string{"expired", "/login", "app"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the warning does not mention %q: %v", want, err)
		}
	}
	// It still SEEDS. Refusing to dispatch over a credential the plane cannot
	// really adjudicate would be worse than a Job that fails with a named cause.
	if _, statErr := os.Stat(filepath.Join(dead, "daedalus-job-J-2",
		claudeConfigDirName, ".credentials.json")); statErr != nil {
		t.Errorf("the credentials were not copied: %v", statErr)
	}
}

// A credentials file whose shape we do not recognise gets NO opinion. The format
// belongs to the CLI, not to us, and refusing to seed because a field moved
// would break dispatch over somebody else's schema change.
func TestSeedJobHome_SaysNothingAboutCredentialsItCannotRead(t *testing.T) {
	for _, body := range []string{
		`{"claudeAiOauth":{"accessToken":"x"}}`, // no expiry field at all
		`{"somethingElse":true}`,
		`not json`,
	} {
		dir := t.TempDir()
		cfg := filepath.Join(dir, "app", claudeConfigDirName)
		if err := os.MkdirAll(cfg, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfg, ".credentials.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := SeedJobHome(dir, "app", "daedalus-job-J-3"); err != nil {
			t.Errorf("seeding complained about credentials it cannot read (%s): %v", body, err)
		}
	}
}
