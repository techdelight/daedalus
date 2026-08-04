// Copyright (C) 2026 Techdelight BV

package core

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The container image bakes its `claude` user at CLAUDE_UID = the host uid of
// whoever ran the build (see cmd/daedalus/build.go). The coordinator later
// creates the shared-cache and per-project tools host dirs at the uid it runs
// as. When those two uids differ — an image built by one user (or in CI) and
// run by another — the container's `claude` user cannot write the host dirs and
// the session fails with a cryptic "Permission denied". Recording the build uid
// lets the coordinator detect that drift and say so plainly (Sprint 43 item 2,
// the top permission risk).

// BuildUIDPath is the file recording the host uid the image was built with,
// kept next to the build-checksum under DataDir.
func BuildUIDPath(dataDir string) string {
	return filepath.Join(dataDir, "build-uid")
}

// WriteBuildUID records uid to BuildUIDPath. Diagnostic state only; callers
// treat a write failure as non-fatal.
func WriteBuildUID(dataDir string, uid int) error {
	return os.WriteFile(BuildUIDPath(dataDir), []byte(strconv.Itoa(uid)), 0o644)
}

// ReadBuildUID returns the recorded build uid. ok is false when the file is
// absent (no build recorded yet) or unparseable, in which case callers skip
// the mismatch check rather than guessing.
func ReadBuildUID(dataDir string) (uid int, ok bool) {
	data, err := os.ReadFile(BuildUIDPath(dataDir))
	if err != nil {
		return 0, false
	}
	uid, err = strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}
	return uid, true
}
