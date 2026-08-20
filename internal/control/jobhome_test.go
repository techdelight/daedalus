// Copyright (C) 2026 Techdelight BV

package control

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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

// TestRemoteAccess_IsOffUnlessSomebodyWroteItDown.
//
// A Job container has the network and runs an objective the plane treats as
// untrusted. A push-capable key in it can write to every host that key reaches,
// so the grant has to be proven rather than assumed — and there are two separate
// silences that must both mean no: a plane with no policy source at all, and a
// policy source that has never heard of the question.
func TestRemoteAccess_IsOffUnlessSomebodyWroteItDown(t *testing.T) {
	repo := gitRepo(t)
	svc, _, _ := newService(t, mapResolver{"app": repo}, StubRunner{}, nil)

	// No policy source installed at all.
	if svc.remoteAccessFor("app") {
		t.Error("remote access granted with NO policy source; silence must mean no")
	}

	// A source that predates the question: StaticBudget knows nothing about it.
	svc.SetPolicySource(StaticBudget(DefaultBudget()))
	if svc.remoteAccessFor("app") {
		t.Error("remote access granted by a policy source that cannot express it")
	}

	// And a real policy that says no.
	svc.SetPolicySource(BudgetPolicy{RemoteAccess: RemoteAccessPolicy{
		Projects: map[string]bool{"other": true},
	}})
	if svc.remoteAccessFor("app") {
		t.Error("remote access granted to a project the policy does not name")
	}
	if !svc.remoteAccessFor("other") {
		t.Error("remote access refused to a project the policy explicitly grants")
	}

	// A blanket default is expressible, because some installations are one
	// person's laptop and the ceremony is not worth it there.
	svc.SetPolicySource(BudgetPolicy{RemoteAccess: RemoteAccessPolicy{Default: true}})
	if !svc.remoteAccessFor("anything") {
		t.Error("a default:true policy did not grant remote access")
	}
}

// TestSeedJobRemoteAccess_CopiesAnAllowListAndSaysWhenThereIsNothing.
//
// A narrow allow-list, not the whole of ~/.ssh: that directory routinely holds
// keys for machines with nothing to do with this project, and `config` can name
// hosts and forwardings the operator never meant to hand over.
func TestSeedJobRemoteAccess_CopiesAnAllowListAndSaysWhenThereIsNothing(t *testing.T) {
	dataDir := t.TempDir()
	home := filepath.Join(dataDir, "app", ".ssh")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(home, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("id_ed25519", "PRIVATE")
	write("id_ed25519.pub", "PUBLIC")
	write("known_hosts", "github.com ssh-ed25519 AAAA")
	write("config", "Host secret\n  IdentityFile ~/.ssh/other")
	write("id_other", "ANOTHER KEY")

	if err := SeedJobRemoteAccess(dataDir, "app", "daedalus-job-J-1"); err != nil {
		t.Fatalf("SeedJobRemoteAccess: %v", err)
	}
	jobSSH := filepath.Join(dataDir, "daedalus-job-J-1", ".ssh")
	for _, want := range []string{"id_ed25519", "id_ed25519.pub", "known_hosts"} {
		if _, err := os.Stat(filepath.Join(jobSSH, want)); err != nil {
			t.Errorf("%s was not copied: %v", want, err)
		}
	}
	for _, unwanted := range []string{"config", "id_other"} {
		if _, err := os.Stat(filepath.Join(jobSSH, unwanted)); err == nil {
			t.Errorf("%s was copied; the allow-list exists so that it is not", unwanted)
		}
	}
	// The private key must not be readable by anyone else — and ssh itself
	// refuses a key with loose permissions, so this is correctness, not hygiene.
	if fi, err := os.Stat(filepath.Join(jobSSH, "id_ed25519")); err == nil {
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("copied key mode = %v, want 0600", fi.Mode().Perm())
		}
	}

	// Granted but with nothing to copy is an ERROR, not a silent success: the Job
	// would otherwise fail at the push with the operator believing they had
	// granted something.
	if err := SeedJobRemoteAccess(t.TempDir(), "app", "daedalus-job-J-2"); err == nil {
		t.Error("seeding with no key present succeeded silently")
	}
}
