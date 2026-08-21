// Copyright (C) 2026 Techdelight BV

package control

// Seeding a Job's container home with its project's agent credentials.
//
// Every Job runs as a THROWAWAY project (`daedalus-job-<id>`), and the launch
// path mounts `<DataDir>/<project>/` as the container's /home/claude. A fresh
// project therefore gets a fresh, empty home — which the entrypoint seeds from
// the image defaults, credentials excluded, because there are none to copy.
//
// The consequence, found on a real host on 2026-08-16 and not before: every Job
// ever dispatched died in about two seconds with
//
//	Not logged in · Please run /login
//	Error: runner exit code 1
//
// The control plane recorded that faithfully as execution_result=failed, so
// nothing lied — but no Job had ever executed successfully outside a fake runner,
// because the agent could not authenticate. The unit tests, the M13/M14 verify
// scripts and CI all use StubRunner or DAEDALUS_CONTROL_FAKE_RUNNER, none of
// which needs credentials, so the gap was invisible everywhere it was looked for.
//
// The fix is to copy the OWNING project's agent config into the Job home before
// launch: the Job runs as that project's agent, which is what it is supposed to
// be, and inherits its login. It stays a copy rather than a shared mount so
// concurrent Jobs on one project cannot race each other's config writes, and so
// the Job's home dies with the Job.

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

// claudeConfigDirName is the container's CLAUDE_CONFIG_DIR, relative to the
// agent's home. It is set in the Dockerfile (`ENV CLAUDE_CONFIG_DIR=
// "/home/claude/.claude-config"`) and mirrored here because the host side has to
// know where the container will look. TestJobHomeSeedPathsMatchTheDockerfile
// fails if the two ever drift.
const claudeConfigDirName = ".claude-config"

// jobHomeSeedFiles are the paths — relative to the project's home — copied into
// a Job's home. An ALLOW-LIST, deliberately: the home also holds session
// transcripts, caches and whatever the agent wrote, none of which a fresh
// attempt should inherit. Only what the agent needs in order to *be* this
// project's agent is copied. A path that does not exist is skipped, so a
// project that authenticates some other way (an API key in the environment,
// say) is not an error here.
var jobHomeSeedFiles = []string{
	filepath.Join(claudeConfigDirName, ".claude.json"),
	filepath.Join(claudeConfigDirName, "settings.json"),
	filepath.Join(claudeConfigDirName, ".credentials.json"),
	// Belt and braces: depending on CLI version and whether CLAUDE_CONFIG_DIR was
	// honoured when the login happened, the OAuth credentials may sit under
	// ~/.claude instead. Copying both costs nothing and removes a version
	// dependency from the one thing that must work.
	filepath.Join(".claude", ".credentials.json"),
}

// SeedJobHome copies the owning project's agent credentials and config from
// <dataDir>/<project>/ into <dataDir>/<jobProject>/, creating the destination.
//
// It reports what it could not do rather than failing the dispatch. A Job whose
// home could not be seeded will very likely fail with "Not logged in", which is
// a worse error message than this warning but an honest outcome — and refusing
// to dispatch would be wrong for a project that needs no seeded credentials at
// all. The warning names the fix, because the fix is a human action ("log in
// once with `daedalus <project>`").
func SeedJobHome(dataDir, project, jobProject string) error {
	if dataDir == "" || project == "" || jobProject == "" {
		return fmt.Errorf("seed job home: dataDir, project and jobProject are required")
	}
	src := filepath.Join(dataDir, project)
	dst := filepath.Join(dataDir, jobProject)

	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("project home %s is not readable: %w", src, err)
	}

	var copied int
	for _, rel := range jobHomeSeedFiles {
		from := filepath.Join(src, rel)
		fi, err := os.Stat(from)
		if err != nil || fi.IsDir() {
			continue // absent is normal; a directory here is not what we mean
		}
		to := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
			return fmt.Errorf("creating %s: %w", filepath.Dir(to), err)
		}
		// Credentials keep their own mode; everything else lands 0600 anyway.
		// Nothing here is worth leaving group- or world-readable.
		if err := copyFileMode(from, to, 0o600); err != nil {
			return fmt.Errorf("copying %s: %w", rel, err)
		}
		copied++
	}
	if copied == 0 {
		return fmt.Errorf("no agent credentials found under %s — "+
			"log in once with `daedalus %s` so Jobs can inherit it", src, project)
	}
	return nil
}

// seedJobHomeOrWarn is the call-site policy: seed, and turn any failure into a
// log line that names the likely symptom, so an operator reading control.log
// after a two-second Job failure finds the cause above it rather than inferring
// it from "exit status 1".
func seedJobHomeOrWarn(dataDir, project, jobProject string) {
	if dataDir == "" {
		return // an adapter constructed without a data dir: nothing to copy from
	}
	if err := SeedJobHome(dataDir, project, jobProject); err != nil {
		log.Printf("control: seeding %s's agent home for %s: %v "+
			"(the job may fail with `Not logged in`)", project, jobProject, err)
	}
}

func copyFileMode(from, to string, mode os.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
