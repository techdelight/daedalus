// Copyright (C) 2026 Techdelight BV

// Package registry manages the on-disk project registry. It is split into
// topic files: registry.go (CRUD + read/write), migrate.go (schema
// versioning), cache.go (per-project cache directory lifecycle), and
// sessions.go (session start/end records).
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

// EnsureGuildMaster makes the always-present built-in Guild Master project
// exist. It is idempotent: if the entry is already registered it does nothing;
// otherwise it scaffolds a conformant doc set into the Daedalus-owned workspace
// (workspaceDir, from core.Config.GuildMasterDir) and registers the project at
// the default "dev" target. Like the rest of the registry it is a
// read-modify-write, so repeated/interleaved calls converge on a single entry.
func (r *Registry) EnsureGuildMaster(workspaceDir string) error {
	data, err := r.read()
	if err != nil {
		return err
	}
	if _, ok := data.Projects[core.GuildMasterName]; ok {
		// Registered already, so there is nothing to scaffold — but the ROLE DOC is
		// still checked, and that is the point of not returning here.
		//
		// It used to return immediately, which meant the role doc was consulted
		// exactly once in a workspace's life: at creation. An agent whose tools grew
		// afterwards kept instructions describing the tools it had on the day it was
		// made, forever, on every existing install. The M12 text said in as many
		// words that controlling anything was "impossible by design"; by M21 the
		// Guild Master could propose a programme.
		return refreshGuildMasterRoleDoc(workspaceDir)
	}
	// First create: scaffold the workspace so `docs lint` passes on it from the
	// start. force=false leaves any pre-existing files untouched.
	if _, _, err := core.ScaffoldDocs(workspaceDir, false); err != nil {
		return fmt.Errorf("scaffolding Guild Master workspace: %w", err)
	}
	if err := refreshGuildMasterRoleDoc(workspaceDir); err != nil {
		return err
	}
	now := core.NowUTC()
	data.Projects[core.GuildMasterName] = core.ProjectEntry{
		Directory: workspaceDir,
		Target:    "dev",
		Created:   now,
		LastUsed:  now,
	}
	return r.write(data)
}

// refreshGuildMasterRoleDoc keeps the Guild Master's CLAUDE.md describing the
// tools it actually has.
//
// THE RULE IT MUST NOT BREAK is "never clobber user edits", and it does not:
// a doc is replaced only when it matches a version Daedalus itself wrote, byte
// for byte. Such a file contains nothing of anybody's, so replacing it destroys
// nothing. Anything else is the operator's document and is left exactly as it is
// — reported by GuildMasterRoleDocStale so the human launch path can say so,
// because an agent running on instructions that predate its tools is a quiet
// failure, not a visible one.
func refreshGuildMasterRoleDoc(workspaceDir string) error {
	rolePath := filepath.Join(workspaceDir, "CLAUDE.md")
	existing, err := os.ReadFile(rolePath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("reading Guild Master role doc: %w", err)
		}
		if err := os.WriteFile(rolePath, []byte(core.GuildMasterRoleDoc), 0o644); err != nil {
			return fmt.Errorf("writing Guild Master role doc: %w", err)
		}
		return nil
	}
	if core.ClassifyGuildMasterRoleDoc(string(existing)) != core.RoleDocOutdated {
		return nil // current, or the user's own — either way, hands off
	}
	if err := os.WriteFile(rolePath, []byte(core.GuildMasterRoleDoc), 0o644); err != nil {
		return fmt.Errorf("updating Guild Master role doc: %w", err)
	}
	return nil
}

// GuildMasterRoleDocStale reports whether the workspace's CLAUDE.md is a
// customised doc that predates the current one — the only case refresh cannot
// fix, and therefore the only one worth telling a human about. A missing file,
// a current one, or one that was refreshed all answer false.
func GuildMasterRoleDocStale(workspaceDir string) bool {
	existing, err := os.ReadFile(filepath.Join(workspaceDir, "CLAUDE.md"))
	if err != nil {
		return false
	}
	return core.ClassifyGuildMasterRoleDoc(string(existing)) == core.RoleDocCustom
}

// RenameProject changes a project's registry key from oldName to newName.
// Returns an error if oldName does not exist or newName already exists.
// The per-project cache directory is renamed best-effort (warning on failure).
func (r *Registry) RenameProject(oldName, newName string) error {
	data, err := r.read()
	if err != nil {
		return err
	}
	if core.IsGuildMaster(oldName) {
		return fmt.Errorf("cannot rename the built-in Guild Master")
	}
	if core.IsGuildMaster(newName) {
		return fmt.Errorf("cannot rename to the reserved built-in Guild Master name")
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

// RemoveProject deletes a project from the registry by name and cleans up
// its per-project cache directory (#23).
func (r *Registry) RemoveProject(name string) error {
	if core.IsGuildMaster(name) {
		return fmt.Errorf("cannot remove the built-in Guild Master")
	}
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
		// The Guild Master is protected: skip it so the rest of the batch still
		// proceeds, rather than aborting or silently deleting it. Callers should
		// surface it as protected (see cmd/daedalus removeProjects).
		if core.IsGuildMaster(name) {
			continue
		}
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
	if len(entry.DefaultFlags) == 0 {
		entry.DefaultFlags = nil
	}
	data.Projects[name] = entry
	return r.write(data)
}

// UpdateProjectTarget changes the build target for an existing project.
func (r *Registry) UpdateProjectTarget(name, target string) error {
	data, err := r.read()
	if err != nil {
		return err
	}
	entry, ok := data.Projects[name]
	if !ok {
		return fmt.Errorf("project '%s' not found", name)
	}
	entry.Target = target
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

// UpdateProjectProgress updates progress metadata for the named project.
// Only non-zero/non-empty values are applied (partial update).
func (r *Registry) UpdateProjectProgress(name string, pct int, vision, projectVersion string) error {
	data, err := r.read()
	if err != nil {
		return err
	}
	entry, ok := data.Projects[name]
	if !ok {
		return fmt.Errorf("project '%s' not found", name)
	}
	if pct > 0 {
		if pct > 100 {
			pct = 100
		}
		entry.ProgressPct = pct
	}
	if vision != "" {
		entry.Vision = vision
	}
	if projectVersion != "" {
		entry.ProjectVersion = projectVersion
	}
	data.Projects[name] = entry
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
