// Copyright (C) 2026 Techdelight BV

package core

import (
	"crypto/sha256"
	"encoding/hex"
)

// BuildFiles returns the list of filenames that affect the Docker image build.
// These files are checked for changes to determine if a rebuild is needed.
func BuildFiles() []string {
	return []string{"Dockerfile", "entrypoint.sh", "docker-compose.yml", "settings.json", "claude.json"}
}

// ComputeBuildChecksum returns a hex-encoded SHA-256 hash of the given
// contents. This is a pure function with no I/O.
func ComputeBuildChecksum(contents []byte) string {
	sum := sha256.Sum256(contents)
	return hex.EncodeToString(sum[:])
}
