// Copyright (C) 2026 Techdelight BV

package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/techdelight/daedalus/core"
)

// Registry manages the project registry file.
type Registry struct {
	FilePath string
}

// migrateFunc transforms registry data from one version to the next.
type migrateFunc func(data *core.RegistryData) error

// migrations maps source version → upgrade function.
var migrations = map[int]migrateFunc{
	1: migrateV1toV2,
}

// migrateV1toV2 upgrades the registry from v1 to v2.
// v2 adds DefaultFlags and Sessions fields to ProjectEntry.
// Zero values (nil) are correct — omitempty keeps JSON clean.
func migrateV1toV2(data *core.RegistryData) error {
	data.Version = 2
	return nil
}

// migrate applies all necessary migrations to bring data to CurrentRegistryVersion.
// Returns true if any migrations were applied.
func (r *Registry) migrate(data *core.RegistryData) (bool, error) {
	if data.Version > core.CurrentRegistryVersion {
		return false, fmt.Errorf("registry version %d is newer than supported version %d", data.Version, core.CurrentRegistryVersion)
	}
	changed := false
	for data.Version < core.CurrentRegistryVersion {
		fn, ok := migrations[data.Version]
		if !ok {
			return changed, fmt.Errorf("no migration from registry version %d", data.Version)
		}
		if err := fn(data); err != nil {
			return changed, fmt.Errorf("migrating from version %d: %w", data.Version, err)
		}
		changed = true
	}
	return changed, nil
}

// NewRegistry creates a Registry pointing at the given file path.
func NewRegistry(filePath string) *Registry {
	return &Registry{FilePath: filePath}
}

// Init ensures the registry file exists. If it doesn't, it creates one
// and migrates any existing .cache/*/ directories.
func (r *Registry) Init() error {
	dir := filepath.Dir(r.FilePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating registry directory: %w", err)
	}

	if _, err := os.Stat(r.FilePath); err == nil {
		return nil // already exists
	}

	data := core.RegistryData{
		Version:  core.CurrentRegistryVersion,
		Projects: make(map[string]core.ProjectEntry),
	}

	// Migrate existing .cache/*/ directories
	cacheDir := filepath.Dir(r.FilePath)
	entries, err := os.ReadDir(cacheDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				now := core.NowUTC()
				data.Projects[e.Name()] = core.ProjectEntry{
					Directory: "(unknown)",
					Target:    "dev",
					Created:   now,
					LastUsed:  now,
				}
			}
		}
	}

	if err := r.write(data); err != nil {
		return err
	}

	count := len(data.Projects)
	if count > 0 {
		fmt.Printf("Migrated %d existing project(s) into registry.\n", count)
	}
	return nil
}

// GetProject returns the entry for the named project and whether it was found.
func (r *Registry) GetProject(name string) (core.ProjectEntry, bool, error) {
	data, err := r.read()
	if err != nil {
		return core.ProjectEntry{}, false, err
	}
	entry, ok := data.Projects[name]
	return entry, ok, nil
}

// HasProject returns true if the named project exists in the registry.
func (r *Registry) HasProject(name string) (bool, error) {
	_, ok, err := r.GetProject(name)
	return ok, err
}

// FindProjectByDir returns the project name and entry whose Directory matches dir.
// Both the query dir and stored directories are resolved through symlinks
// before comparison, falling back to exact string comparison if resolution fails.
func (r *Registry) FindProjectByDir(dir string) (string, core.ProjectEntry, bool, error) {
	data, err := r.read()
	if err != nil {
		return "", core.ProjectEntry{}, false, err
	}
	resolvedDir := resolveSymlink(dir)
	for name, entry := range data.Projects {
		resolvedEntry := resolveSymlink(entry.Directory)
		if resolvedDir == resolvedEntry {
			return name, entry, true, nil
		}
	}
	return "", core.ProjectEntry{}, false, nil
}

// resolveSymlink attempts to resolve a path through symlinks.
// Returns the original path if resolution fails (e.g., path doesn't exist).
func resolveSymlink(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}

// GetProjectEntries returns all projects sorted by name with full metadata.
func (r *Registry) GetProjectEntries() ([]core.ProjectInfo, error) {
	data, err := r.read()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(data.Projects))
	for name := range data.Projects {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]core.ProjectInfo, 0, len(names))
	for _, name := range names {
		entries = append(entries, core.ProjectInfo{Name: name, Entry: data.Projects[name]})
	}
	return entries, nil
}

// AddProject registers a new project with the given metadata.
func (r *Registry) AddProject(name, directory, target string) error {
	data, err := r.read()
	if err != nil {
		return err
	}
	now := core.NowUTC()
	data.Projects[name] = core.ProjectEntry{
		Directory: directory,
		Target:    target,
		Created:   now,
		LastUsed:  now,
	}
	return r.write(data)
}

// RenameProject changes a project's registry key from oldName to newName.
// Returns an error if oldName does not exist or newName already exists.
// The per-project cache directory is renamed best-effort (warning on failure).
func (r *Registry) RenameProject(oldName, newName string) error {
	data, err := r.read()
	if err != nil {
		return err
	}
	entry, ok := data.Projects[oldName]
	if !ok {
		return fmt.Errorf("project '%s' not found", oldName)
	}
	if _, exists := data.Projects[newName]; exists {
		return fmt.Errorf("project '%s' already exists", newName)
	}
	data.Projects[newName] = entry
	delete(data.Projects, oldName)
	if err := r.write(data); err != nil {
		return err
	}
	r.renameCache(oldName, newName)
	return nil
}

// renameCache renames the per-project cache directory.
// Failures are logged to stderr but not returned as errors.
func (r *Registry) renameCache(oldName, newName string) {
	baseDir := filepath.Dir(r.FilePath)
	oldDir := filepath.Join(baseDir, oldName)
	newDir := filepath.Join(baseDir, newName)
	if _, err := os.Stat(oldDir); os.IsNotExist(err) {
		return
	}
	if err := os.Rename(oldDir, newDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to rename cache directory '%s' to '%s': %v\n", oldDir, newDir, err)
	}
}

// RemoveProject deletes a project from the registry by name and cleans up
// its per-project cache directory (#23).
func (r *Registry) RemoveProject(name string) error {
	data, err := r.read()
	if err != nil {
		return err
	}
	if _, ok := data.Projects[name]; !ok {
		return fmt.Errorf("project '%s' not found", name)
	}
	delete(data.Projects, name)
	if err := r.write(data); err != nil {
		return err
	}
	r.cleanCache(name)
	return nil
}

// cleanCache removes the per-project cache directory.
// Failures are logged to stderr but not returned as errors.
func (r *Registry) cleanCache(name string) {
	cacheDir := filepath.Join(filepath.Dir(r.FilePath), name)
	if err := os.RemoveAll(cacheDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to remove cache directory '%s': %v\n", cacheDir, err)
	}
}

// RemoveProjects deletes multiple projects in a single read-modify-write cycle (#24).
// Missing names are silently skipped. Returns the list of actually removed names.
func (r *Registry) RemoveProjects(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	data, err := r.read()
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, name := range names {
		if _, ok := data.Projects[name]; ok {
			delete(data.Projects, name)
			removed = append(removed, name)
		}
	}
	if len(removed) == 0 {
		return nil, nil
	}
	if err := r.write(data); err != nil {
		return nil, err
	}
	for _, name := range removed {
		r.cleanCache(name)
	}
	return removed, nil
}

// SetDefaultFlags stores per-project default flags.
func (r *Registry) SetDefaultFlags(name string, flags map[string]string) error {
	data, err := r.read()
	if err != nil {
		return err
	}
	entry, ok := data.Projects[name]
	if !ok {
		return fmt.Errorf("project '%s' not found", name)
	}
	entry.DefaultFlags = flags
	data.Projects[name] = entry
	return r.write(data)
}

// UpdateDefaultFlags merges set values and removes unset keys from per-project flags.
func (r *Registry) UpdateDefaultFlags(name string, set map[string]string, unset []string) error {
	data, err := r.read()
	if err != nil {
		return err
	}
	entry, ok := data.Projects[name]
	if !ok {
		return fmt.Errorf("project '%s' not found", name)
	}
	if entry.DefaultFlags == nil {
		entry.DefaultFlags = make(map[string]string)
	}
	for k, v := range set {
		entry.DefaultFlags[k] = v
	}
	for _, k := range unset {
		delete(entry.DefaultFlags, k)
	}
	// Clean up empty map
	if len(entry.DefaultFlags) == 0 {
		entry.DefaultFlags = nil
	}
	data.Projects[name] = entry
	return r.write(data)
}

// TouchProject updates the lastUsed timestamp for an existing project.
func (r *Registry) TouchProject(name string) error {
	data, err := r.read()
	if err != nil {
		return err
	}
	entry, ok := data.Projects[name]
	if !ok {
		return fmt.Errorf("project '%s' not found", name)
	}
	entry.LastUsed = core.NowUTC()
	data.Projects[name] = entry
	return r.write(data)
}

// maxSessionHistory is the maximum number of session records kept per project.
const maxSessionHistory = 50

// StartSession records a new session start for the named project.
// Returns the session ID (monotonic counter based on len(Sessions)+1).
// Caps history at maxSessionHistory by trimming oldest entries.
func (r *Registry) StartSession(projectName, resumeID string) (string, error) {
	data, err := r.read()
	if err != nil {
		return "", err
	}
	entry, ok := data.Projects[projectName]
	if !ok {
		return "", fmt.Errorf("project '%s' not found", projectName)
	}

	sessionID := fmt.Sprintf("%d", len(entry.Sessions)+1)
	rec := core.SessionRecord{
		ID:       sessionID,
		Started:  core.NowUTC(),
		ResumeID: resumeID,
	}
	entry.Sessions = append(entry.Sessions, rec)

	// Cap at maxSessionHistory
	if len(entry.Sessions) > maxSessionHistory {
		entry.Sessions = entry.Sessions[len(entry.Sessions)-maxSessionHistory:]
	}

	data.Projects[projectName] = entry
	if err := r.write(data); err != nil {
		return "", err
	}
	return sessionID, nil
}

// EndSession records the end of a session with timestamp and duration.
func (r *Registry) EndSession(projectName, sessionID string) error {
	data, err := r.read()
	if err != nil {
		return err
	}
	entry, ok := data.Projects[projectName]
	if !ok {
		return fmt.Errorf("project '%s' not found", projectName)
	}

	found := false
	for i := len(entry.Sessions) - 1; i >= 0; i-- {
		if entry.Sessions[i].ID == sessionID {
			now := core.NowUTC()
			entry.Sessions[i].Ended = now
			// Calculate duration from Started to now
			startTime, err := core.ParseUTC(entry.Sessions[i].Started)
			if err == nil {
				endTime, err2 := core.ParseUTC(now)
				if err2 == nil {
					entry.Sessions[i].Duration = int(endTime.Sub(startTime).Seconds())
				}
			}
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("session '%s' not found for project '%s'", sessionID, projectName)
	}

	data.Projects[projectName] = entry
	return r.write(data)
}

// read loads the registry from disk, migrating if needed.
func (r *Registry) read() (core.RegistryData, error) {
	var data core.RegistryData
	b, err := os.ReadFile(r.FilePath)
	if err != nil {
		return data, fmt.Errorf("reading registry: %w", err)
	}
	if err := json.Unmarshal(b, &data); err != nil {
		return data, fmt.Errorf("parsing registry: %w", err)
	}
	if data.Projects == nil {
		data.Projects = make(map[string]core.ProjectEntry)
	}
	if changed, err := r.migrate(&data); err != nil {
		return data, fmt.Errorf("migrating registry: %w", err)
	} else if changed {
		if err := r.write(data); err != nil {
			return data, fmt.Errorf("persisting migrated registry: %w", err)
		}
	}
	return data, nil
}

// write atomically writes the registry to disk (tmp + rename).
func (r *Registry) write(data core.RegistryData) error {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling registry: %w", err)
	}
	b = append(b, '\n')

	tmp := r.FilePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return fmt.Errorf("writing temp registry: %w", err)
	}
	if err := os.Rename(tmp, r.FilePath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("renaming registry: %w", err)
	}
	return nil
}
