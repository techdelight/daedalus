// Copyright (C) 2026 Techdelight BV

package docker

import (
	"os"
	"path/filepath"

	"github.com/techdelight/daedalus/core"
)

// ReadBuildFilesContent reads and concatenates the contents of all build-relevant
// files from the given directory. Missing files are skipped.
func ReadBuildFilesContent(dir string) ([]byte, error) {
	var combined []byte
	for _, name := range core.BuildFiles() {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		combined = append(combined, data...)
	}
	return combined, nil
}

// ReadStoredChecksum reads the stored build checksum from the given path.
// Returns empty string if the file does not exist or cannot be read.
func ReadStoredChecksum(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// WriteChecksum writes the checksum string to the given file path.
func WriteChecksum(path, checksum string) error {
	return os.WriteFile(path, []byte(checksum), 0644)
}

// NeedsRebuild returns true if the current build files differ from the stored checksum.
// Returns true if no stored checksum exists (first run) or if the checksums differ.
func NeedsRebuild(scriptDir, checksumPath string) bool {
	stored := ReadStoredChecksum(checksumPath)
	if stored == "" {
		return true
	}
	content, err := ReadBuildFilesContent(scriptDir)
	if err != nil {
		return true
	}
	current := core.ComputeBuildChecksum(content)
	return current != stored
}
