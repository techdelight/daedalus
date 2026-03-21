// Copyright (C) 2026 Techdelight BV

package core

import (
	"embed"
	"io/fs"
	"path/filepath"
)

//go:embed starter_skills/*.md
var starterSkills embed.FS

// StarterSkills returns the embedded starter skill files as name-content pairs.
// Names are without the directory prefix (e.g., "commit.md").
func StarterSkills() (map[string][]byte, error) {
	skills := make(map[string][]byte)
	err := fs.WalkDir(starterSkills, "starter_skills", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := starterSkills.ReadFile(path)
		if err != nil {
			return err
		}
		skills[filepath.Base(path)] = data
		return nil
	})
	return skills, err
}
