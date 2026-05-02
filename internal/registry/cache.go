// Copyright (C) 2026 Techdelight BV

package registry

import (
	"fmt"
	"os"
	"path/filepath"
)

// renameCache renames the per-project cache directory.
// Uses copy+remove instead of os.Rename to avoid cross-device and
// WSL2/bind-mount issues with directory renames.
// Failures are logged to stderr but not returned as errors.
func (r *Registry) renameCache(oldName, newName string) {
	baseDir := filepath.Dir(r.FilePath)
	oldDir := filepath.Join(baseDir, oldName)
	newDir := filepath.Join(baseDir, newName)
	if _, err := os.Stat(oldDir); os.IsNotExist(err) {
		return
	}
	if err := copyDir(oldDir, newDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to copy cache directory '%s' to '%s': %v\n", oldDir, newDir, err)
		return
	}
	if err := os.RemoveAll(oldDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to remove old cache directory '%s': %v\n", oldDir, err)
	}
}

// cleanCache removes the per-project cache directory.
// Failures are logged to stderr but not returned as errors.
func (r *Registry) cleanCache(name string) {
	cacheDir := filepath.Join(filepath.Dir(r.FilePath), name)
	if err := os.RemoveAll(cacheDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to remove cache directory '%s': %v\n", cacheDir, err)
	}
}

// copyDir recursively copies a directory tree from src to dst.
// Symlinks are recreated (preserving the link target) rather than followed.
func copyDir(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		// Use Lstat to detect symlinks without following them
		info, err := os.Lstat(srcPath)
		if err != nil {
			return err
		}

		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(srcPath)
			if err != nil {
				return err
			}
			if err := os.Symlink(link, dstPath); err != nil {
				return err
			}
			continue
		}

		if info.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}

		data, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dstPath, data, info.Mode()); err != nil {
			return err
		}
	}
	return nil
}
