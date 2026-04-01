// Copyright (C) 2026 Techdelight BV

package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/techdelight/daedalus/core"
)

func TestRegistryInit_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)

	if err := reg.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if _, err := os.Stat(regFile); err != nil {
		t.Fatalf("registry file not created: %v", err)
	}

	b, _ := os.ReadFile(regFile)
	var data core.RegistryData
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if data.Version != core.CurrentRegistryVersion {
		t.Errorf("version = %d, want %d", data.Version, core.CurrentRegistryVersion)
	}
	if len(data.Projects) != 0 {
		t.Errorf("projects = %d, want 0", len(data.Projects))
	}
}

func TestRegistryInit_MigratesExisting(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "my-project"), 0755)

	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)

	if err := reg.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	has, err := reg.HasProject("my-project")
	if err != nil {
		t.Fatalf("HasProject failed: %v", err)
	}
	if !has {
		t.Error("migrated project not found in registry")
	}
}

func TestRegistryAddProject(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()

	err := reg.AddProject("test-app", "/path/to/app", "dev")
	if err != nil {
		t.Fatalf("AddProject failed: %v", err)
	}

	b, _ := os.ReadFile(regFile)
	var data core.RegistryData
	json.Unmarshal(b, &data)

	entry, ok := data.Projects["test-app"]
	if !ok {
		t.Fatal("project 'test-app' not found in registry")
	}
	if entry.Directory != "/path/to/app" {
		t.Errorf("directory = %q, want %q", entry.Directory, "/path/to/app")
	}
	if entry.Target != "dev" {
		t.Errorf("target = %q, want %q", entry.Target, "dev")
	}
	if entry.Created == "" {
		t.Error("created timestamp is empty")
	}
	if entry.LastUsed == "" {
		t.Error("lastUsed timestamp is empty")
	}
}

func TestRegistryHasProject(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("exists", "/tmp", "dev")

	has, _ := reg.HasProject("exists")
	if !has {
		t.Error("HasProject('exists') = false, want true")
	}

	has, _ = reg.HasProject("missing")
	if has {
		t.Error("HasProject('missing') = true, want false")
	}
}

func TestRegistryTouchProject(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("my-app", "/tmp", "dev")

	entry1, _, _ := reg.GetProject("my-app")
	initial := entry1.LastUsed

	err := reg.TouchProject("my-app")
	if err != nil {
		t.Fatalf("TouchProject failed: %v", err)
	}

	entry2, _, _ := reg.GetProject("my-app")
	updated := entry2.LastUsed

	if updated < initial {
		t.Errorf("lastUsed went backwards: %s < %s", updated, initial)
	}
}

func TestRegistryGetProject_Found(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("my-app", "/path/to/app", "dev")

	entry, found, err := reg.GetProject("my-app")
	if err != nil {
		t.Fatalf("GetProject failed: %v", err)
	}
	if !found {
		t.Fatal("GetProject returned found=false, want true")
	}
	if entry.Directory != "/path/to/app" {
		t.Errorf("directory = %q, want %q", entry.Directory, "/path/to/app")
	}
	if entry.Target != "dev" {
		t.Errorf("target = %q, want %q", entry.Target, "dev")
	}
}

func TestRegistryGetProject_NotFound(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()

	_, found, err := reg.GetProject("nonexistent")
	if err != nil {
		t.Fatalf("GetProject failed: %v", err)
	}
	if found {
		t.Error("GetProject returned found=true, want false")
	}
}

func TestRegistryFindProjectByDir_Found(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("my-app", "/path/to/app", "dev")
	reg.AddProject("other-app", "/path/to/other", "godot")

	name, entry, found, err := reg.FindProjectByDir("/path/to/app")
	if err != nil {
		t.Fatalf("FindProjectByDir failed: %v", err)
	}
	if !found {
		t.Fatal("FindProjectByDir returned found=false, want true")
	}
	if name != "my-app" {
		t.Errorf("name = %q, want %q", name, "my-app")
	}
	if entry.Target != "dev" {
		t.Errorf("target = %q, want %q", entry.Target, "dev")
	}
}

func TestRegistryFindProjectByDir_NotFound(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("my-app", "/path/to/app", "dev")

	_, _, found, err := reg.FindProjectByDir("/nonexistent/dir")
	if err != nil {
		t.Fatalf("FindProjectByDir failed: %v", err)
	}
	if found {
		t.Error("FindProjectByDir returned found=true, want false")
	}
}

func TestRegistryGetProjectEntries(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("beta", "/tmp/beta", "godot")
	reg.AddProject("alpha", "/tmp/alpha", "dev")
	reg.AddProject("gamma", "/tmp/gamma", "dev")

	entries, err := reg.GetProjectEntries()
	if err != nil {
		t.Fatalf("GetProjectEntries failed: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("len = %d, want 3", len(entries))
	}
	expectedNames := []string{"alpha", "beta", "gamma"}
	for i, e := range entries {
		if e.Name != expectedNames[i] {
			t.Errorf("entries[%d].Name = %q, want %q", i, e.Name, expectedNames[i])
		}
	}
	if entries[1].Entry.Directory != "/tmp/beta" {
		t.Errorf("beta directory = %q, want %q", entries[1].Entry.Directory, "/tmp/beta")
	}
	if entries[1].Entry.Target != "godot" {
		t.Errorf("beta target = %q, want %q", entries[1].Entry.Target, "godot")
	}
}

func TestRegistryFindProjectByDir_Symlink(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()

	realDir := filepath.Join(dir, "real-project")
	os.MkdirAll(realDir, 0755)
	symDir := filepath.Join(dir, "sym-project")
	if err := os.Symlink(realDir, symDir); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	reg.AddProject("my-app", realDir, "dev")

	name, _, found, err := reg.FindProjectByDir(symDir)
	if err != nil {
		t.Fatalf("FindProjectByDir failed: %v", err)
	}
	if !found {
		t.Fatal("FindProjectByDir did not resolve symlink to find project")
	}
	if name != "my-app" {
		t.Errorf("name = %q, want %q", name, "my-app")
	}
}

func TestRegistryFindProjectByDir_SymlinkInRegistry(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()

	realDir := filepath.Join(dir, "real-project")
	os.MkdirAll(realDir, 0755)
	symDir := filepath.Join(dir, "sym-project")
	if err := os.Symlink(realDir, symDir); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	reg.AddProject("my-app", symDir, "dev")

	name, _, found, err := reg.FindProjectByDir(realDir)
	if err != nil {
		t.Fatalf("FindProjectByDir failed: %v", err)
	}
	if !found {
		t.Fatal("FindProjectByDir did not resolve registry symlink to find project")
	}
	if name != "my-app" {
		t.Errorf("name = %q, want %q", name, "my-app")
	}
}

func TestRegistryRenameProject(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("old-app", "/path/to/app", "dev")

	err := reg.RenameProject("old-app", "new-app")
	if err != nil {
		t.Fatalf("RenameProject failed: %v", err)
	}

	has, _ := reg.HasProject("old-app")
	if has {
		t.Error("old name still exists after rename")
	}
	has, _ = reg.HasProject("new-app")
	if !has {
		t.Error("new name not found after rename")
	}
}

func TestRegistryRenameProject_NotFound(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()

	err := reg.RenameProject("nonexistent", "new-name")
	if err == nil {
		t.Fatal("expected error for renaming nonexistent project")
	}
}

func TestRegistryRenameProject_TargetExists(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("app-a", "/tmp/a", "dev")
	reg.AddProject("app-b", "/tmp/b", "dev")

	err := reg.RenameProject("app-a", "app-b")
	if err == nil {
		t.Fatal("expected error when target name already exists")
	}
}

func TestRegistryRenameProject_PreservesEntry(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("old-app", "/path/to/app", "godot")
	reg.SetDefaultFlags("old-app", map[string]string{"debug": "true"})

	err := reg.RenameProject("old-app", "new-app")
	if err != nil {
		t.Fatalf("RenameProject failed: %v", err)
	}

	entry, found, err := reg.GetProject("new-app")
	if err != nil {
		t.Fatalf("GetProject failed: %v", err)
	}
	if !found {
		t.Fatal("renamed project not found")
	}
	if entry.Directory != "/path/to/app" {
		t.Errorf("directory = %q, want %q", entry.Directory, "/path/to/app")
	}
	if entry.Target != "godot" {
		t.Errorf("target = %q, want %q", entry.Target, "godot")
	}
	if entry.DefaultFlags["debug"] != "true" {
		t.Errorf("defaultFlags[debug] = %q, want %q", entry.DefaultFlags["debug"], "true")
	}
}

func TestRegistryRenameProject_RenamesCacheDir(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("old-app", "/tmp/old", "dev")

	oldCache := filepath.Join(dir, "old-app")
	os.MkdirAll(oldCache, 0755)
	os.WriteFile(filepath.Join(oldCache, "data.txt"), []byte("test"), 0644)

	err := reg.RenameProject("old-app", "new-app")
	if err != nil {
		t.Fatalf("RenameProject failed: %v", err)
	}

	if _, err := os.Stat(oldCache); !os.IsNotExist(err) {
		t.Error("old cache directory still exists")
	}
	newCache := filepath.Join(dir, "new-app")
	if _, err := os.Stat(newCache); err != nil {
		t.Error("new cache directory does not exist")
	}
	data, err := os.ReadFile(filepath.Join(newCache, "data.txt"))
	if err != nil {
		t.Fatalf("reading data from renamed cache: %v", err)
	}
	if string(data) != "test" {
		t.Errorf("cache data = %q, want %q", string(data), "test")
	}
}

func TestRegistryRenameProject_NoCacheDir(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("old-app", "/tmp/old", "dev")

	// No cache dir created — rename should succeed without error
	err := reg.RenameProject("old-app", "new-app")
	if err != nil {
		t.Fatalf("RenameProject failed: %v", err)
	}

	has, _ := reg.HasProject("new-app")
	if !has {
		t.Error("new name not found after rename")
	}
}

func TestRegistryRemoveProject(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("to-remove", "/tmp/remove", "dev")

	err := reg.RemoveProject("to-remove")
	if err != nil {
		t.Fatalf("RemoveProject failed: %v", err)
	}

	has, _ := reg.HasProject("to-remove")
	if has {
		t.Error("project still exists after removal")
	}
}

func TestRegistryRemoveProject_NotFound(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()

	err := reg.RemoveProject("nonexistent")
	if err == nil {
		t.Fatal("expected error for removing nonexistent project")
	}
}

func TestRegistryRemoveProjects_Batch(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("alpha", "/tmp/alpha", "dev")
	reg.AddProject("beta", "/tmp/beta", "dev")
	reg.AddProject("gamma", "/tmp/gamma", "dev")

	removed, err := reg.RemoveProjects([]string{"alpha", "gamma"})
	if err != nil {
		t.Fatalf("RemoveProjects failed: %v", err)
	}
	if len(removed) != 2 {
		t.Errorf("removed count = %d, want 2", len(removed))
	}

	has, _ := reg.HasProject("beta")
	if !has {
		t.Error("beta should still exist")
	}
	has, _ = reg.HasProject("alpha")
	if has {
		t.Error("alpha should be removed")
	}
	has, _ = reg.HasProject("gamma")
	if has {
		t.Error("gamma should be removed")
	}
}

func TestRegistryRemoveProjects_Empty(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()

	removed, err := reg.RemoveProjects([]string{})
	if err != nil {
		t.Fatalf("RemoveProjects failed: %v", err)
	}
	if removed != nil {
		t.Errorf("removed = %v, want nil", removed)
	}
}

func TestRegistryRemoveProjects_SomeMissing(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("exists", "/tmp/exists", "dev")

	removed, err := reg.RemoveProjects([]string{"exists", "missing"})
	if err != nil {
		t.Fatalf("RemoveProjects failed: %v", err)
	}
	if len(removed) != 1 || removed[0] != "exists" {
		t.Errorf("removed = %v, want [exists]", removed)
	}
}

func TestRegistryRemoveProjects_CleansCache(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("app1", "/tmp/app1", "dev")
	reg.AddProject("app2", "/tmp/app2", "dev")

	cache1 := filepath.Join(dir, "app1")
	cache2 := filepath.Join(dir, "app2")
	os.MkdirAll(cache1, 0755)
	os.MkdirAll(cache2, 0755)

	_, err := reg.RemoveProjects([]string{"app1", "app2"})
	if err != nil {
		t.Fatalf("RemoveProjects failed: %v", err)
	}

	if _, err := os.Stat(cache1); !os.IsNotExist(err) {
		t.Error("cache dir for app1 still exists")
	}
	if _, err := os.Stat(cache2); !os.IsNotExist(err) {
		t.Error("cache dir for app2 still exists")
	}
}

func TestRegistryRemoveProject_CleansUpCacheDir(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("my-app", "/tmp/my-app", "dev")

	cacheDir := filepath.Join(dir, "my-app")
	os.MkdirAll(filepath.Join(cacheDir, "subdir"), 0755)
	os.WriteFile(filepath.Join(cacheDir, "file.txt"), []byte("data"), 0644)

	err := reg.RemoveProject("my-app")
	if err != nil {
		t.Fatalf("RemoveProject failed: %v", err)
	}

	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Error("cache directory still exists after RemoveProject")
	}
}

func TestRegistryAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()

	reg.AddProject("test", "/tmp", "dev")

	tmp := regFile + ".tmp"
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Error("temp file still exists after write")
	}

	has, _ := reg.HasProject("test")
	if !has {
		t.Error("project not found after atomic write")
	}
}

func TestRegistryStartSession(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("my-app", "/tmp/my-app", "dev")

	id, err := reg.StartSession("my-app", "")
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if id != "1" {
		t.Errorf("session ID = %q, want %q", id, "1")
	}

	entry, _, _ := reg.GetProject("my-app")
	if len(entry.Sessions) != 1 {
		t.Fatalf("sessions count = %d, want 1", len(entry.Sessions))
	}
	if entry.Sessions[0].Started == "" {
		t.Error("session Started is empty")
	}
}

func TestRegistryEndSession(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("my-app", "/tmp/my-app", "dev")

	id, _ := reg.StartSession("my-app", "")
	err := reg.EndSession("my-app", id)
	if err != nil {
		t.Fatalf("EndSession failed: %v", err)
	}

	entry, _, _ := reg.GetProject("my-app")
	if entry.Sessions[0].Ended == "" {
		t.Error("session Ended is empty after EndSession")
	}
	if entry.Sessions[0].Duration < 0 {
		t.Errorf("session Duration = %d, want >= 0", entry.Sessions[0].Duration)
	}
}

func TestRegistryStartSession_WithResumeID(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("my-app", "/tmp/my-app", "dev")

	id, err := reg.StartSession("my-app", "resume-abc")
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if id != "1" {
		t.Errorf("session ID = %q, want %q", id, "1")
	}

	entry, _, _ := reg.GetProject("my-app")
	if entry.Sessions[0].ResumeID != "resume-abc" {
		t.Errorf("resumeID = %q, want %q", entry.Sessions[0].ResumeID, "resume-abc")
	}
}

func TestRegistryStartSession_Cap(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("my-app", "/tmp/my-app", "dev")

	for i := 0; i < 55; i++ {
		_, err := reg.StartSession("my-app", "")
		if err != nil {
			t.Fatalf("StartSession %d failed: %v", i, err)
		}
	}

	entry, _, _ := reg.GetProject("my-app")
	if len(entry.Sessions) != 50 {
		t.Errorf("sessions count = %d, want 50 (capped)", len(entry.Sessions))
	}
}

func TestRegistryEndSession_NotFound(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("my-app", "/tmp/my-app", "dev")

	err := reg.EndSession("my-app", "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent session, got nil")
	}
}

func TestRegistryStartSession_ProjectNotFound(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()

	_, err := reg.StartSession("nonexistent", "")
	if err == nil {
		t.Fatal("expected error for nonexistent project, got nil")
	}
}

func TestRegistrySetDefaultFlags(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("my-app", "/tmp/my-app", "dev")

	flags := map[string]string{"debug": "true", "dind": "true"}
	err := reg.SetDefaultFlags("my-app", flags)
	if err != nil {
		t.Fatalf("SetDefaultFlags failed: %v", err)
	}

	entry, _, _ := reg.GetProject("my-app")
	if entry.DefaultFlags == nil {
		t.Fatal("DefaultFlags is nil after SetDefaultFlags")
	}
	if entry.DefaultFlags["debug"] != "true" {
		t.Errorf("DefaultFlags[debug] = %q, want %q", entry.DefaultFlags["debug"], "true")
	}
	if entry.DefaultFlags["dind"] != "true" {
		t.Errorf("DefaultFlags[dind] = %q, want %q", entry.DefaultFlags["dind"], "true")
	}
}

func TestRegistryUpdateDefaultFlags(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("my-app", "/tmp/my-app", "dev")
	reg.SetDefaultFlags("my-app", map[string]string{"debug": "true", "dind": "true"})

	err := reg.UpdateDefaultFlags("my-app", map[string]string{"no-tmux": "true"}, []string{"debug"})
	if err != nil {
		t.Fatalf("UpdateDefaultFlags failed: %v", err)
	}

	entry, _, _ := reg.GetProject("my-app")
	if _, ok := entry.DefaultFlags["debug"]; ok {
		t.Error("debug should have been unset")
	}
	if entry.DefaultFlags["dind"] != "true" {
		t.Errorf("dind = %q, want %q", entry.DefaultFlags["dind"], "true")
	}
	if entry.DefaultFlags["no-tmux"] != "true" {
		t.Errorf("no-tmux = %q, want %q", entry.DefaultFlags["no-tmux"], "true")
	}
}

func TestRegistryUpdateDefaultFlags_ProjectNotFound(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()

	err := reg.UpdateDefaultFlags("nonexistent", map[string]string{"debug": "true"}, nil)
	if err == nil {
		t.Fatal("expected error for nonexistent project, got nil")
	}
}

func TestRegistryUpdateDefaultFlags_AllUnset(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("my-app", "/tmp/my-app", "dev")
	reg.SetDefaultFlags("my-app", map[string]string{"debug": "true"})

	err := reg.UpdateDefaultFlags("my-app", nil, []string{"debug"})
	if err != nil {
		t.Fatalf("UpdateDefaultFlags failed: %v", err)
	}

	entry, _, _ := reg.GetProject("my-app")
	if entry.DefaultFlags != nil {
		t.Errorf("DefaultFlags = %v, want nil (cleaned up)", entry.DefaultFlags)
	}
}

func TestRegistrySetDefaultFlags_ProjectNotFound(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()

	err := reg.SetDefaultFlags("nonexistent", map[string]string{"debug": "true"})
	if err == nil {
		t.Fatal("expected error for nonexistent project, got nil")
	}
}

func TestRegistryUpdateProjectProgress(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("my-app", "/tmp/my-app", "dev")

	// Act
	err := reg.UpdateProjectProgress("my-app", 42, "Build a CLI tool", "1.2.0")

	// Assert
	if err != nil {
		t.Fatalf("UpdateProjectProgress failed: %v", err)
	}
	entry, _, _ := reg.GetProject("my-app")
	if entry.ProgressPct != 42 {
		t.Errorf("ProgressPct = %d, want 42", entry.ProgressPct)
	}
	if entry.Vision != "Build a CLI tool" {
		t.Errorf("Vision = %q, want %q", entry.Vision, "Build a CLI tool")
	}
	if entry.ProjectVersion != "1.2.0" {
		t.Errorf("ProjectVersion = %q, want %q", entry.ProjectVersion, "1.2.0")
	}
}

func TestRegistryUpdateProjectProgress_PartialUpdate(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("my-app", "/tmp/my-app", "dev")
	reg.UpdateProjectProgress("my-app", 50, "Original vision", "0.1.0")

	// Act — update only pct
	err := reg.UpdateProjectProgress("my-app", 75, "", "")

	// Assert
	if err != nil {
		t.Fatalf("UpdateProjectProgress failed: %v", err)
	}
	entry, _, _ := reg.GetProject("my-app")
	if entry.ProgressPct != 75 {
		t.Errorf("ProgressPct = %d, want 75", entry.ProgressPct)
	}
	if entry.Vision != "Original vision" {
		t.Errorf("Vision = %q, want %q", entry.Vision, "Original vision")
	}
	if entry.ProjectVersion != "0.1.0" {
		t.Errorf("ProjectVersion = %q, want %q", entry.ProjectVersion, "0.1.0")
	}
}

func TestRegistryUpdateProjectProgress_ProjectNotFound(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()

	// Act
	err := reg.UpdateProjectProgress("nonexistent", 50, "vision", "1.0.0")

	// Assert
	if err == nil {
		t.Fatal("expected error for nonexistent project, got nil")
	}
}

func TestRegistryUpdateProjectProgress_ClampsPct(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("my-app", "/tmp/my-app", "dev")

	// Act
	err := reg.UpdateProjectProgress("my-app", 150, "", "")

	// Assert
	if err != nil {
		t.Fatalf("UpdateProjectProgress failed: %v", err)
	}
	entry, _, _ := reg.GetProject("my-app")
	if entry.ProgressPct != 100 {
		t.Errorf("ProgressPct = %d, want 100 (clamped)", entry.ProgressPct)
	}
}

func TestRegistryUpdateProjectTarget(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("my-app", "/tmp/my-app", "dev")

	err := reg.UpdateProjectTarget("my-app", "godot")
	if err != nil {
		t.Fatalf("UpdateProjectTarget failed: %v", err)
	}
	entry, _, _ := reg.GetProject("my-app")
	if entry.Target != "godot" {
		t.Errorf("Target = %q, want %q", entry.Target, "godot")
	}
}

func TestRegistryUpdateProjectTarget_NotFound(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()

	err := reg.UpdateProjectTarget("nonexistent", "dev")
	if err == nil {
		t.Fatal("expected error for nonexistent project")
	}
}

func TestRegistryMigrate_V1toV2(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")

	v1Data := core.RegistryData{
		Version:  1,
		Projects: map[string]core.ProjectEntry{"app": {Directory: "/tmp", Target: "dev", Created: "2026-01-01T00:00:00Z", LastUsed: "2026-01-01T00:00:00Z"}},
	}
	b, _ := json.MarshalIndent(v1Data, "", "  ")
	os.WriteFile(regFile, append(b, '\n'), 0644)

	reg := NewRegistry(regFile)
	_, _, err := reg.GetProject("app")
	if err != nil {
		t.Fatalf("GetProject after migration failed: %v", err)
	}

	raw, _ := os.ReadFile(regFile)
	var ondisk core.RegistryData
	json.Unmarshal(raw, &ondisk)
	if ondisk.Version != core.CurrentRegistryVersion {
		t.Errorf("on-disk version = %d, want %d", ondisk.Version, core.CurrentRegistryVersion)
	}
}

func TestRegistryMigrate_V2toV3(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")

	v2Data := core.RegistryData{
		Version: 2,
		Projects: map[string]core.ProjectEntry{
			"app": {
				Directory: "/tmp",
				Target:    "dev",
				Created:   "2026-01-01T00:00:00Z",
				LastUsed:  "2026-01-01T00:00:00Z",
			},
		},
	}
	b, _ := json.MarshalIndent(v2Data, "", "  ")
	os.WriteFile(regFile, append(b, '\n'), 0644)

	// Act
	reg := NewRegistry(regFile)
	entry, found, err := reg.GetProject("app")

	// Assert
	if err != nil {
		t.Fatalf("GetProject after migration failed: %v", err)
	}
	if !found {
		t.Fatal("project not found after migration")
	}
	if entry.Directory != "/tmp" {
		t.Errorf("directory = %q, want %q", entry.Directory, "/tmp")
	}

	raw, _ := os.ReadFile(regFile)
	var ondisk core.RegistryData
	json.Unmarshal(raw, &ondisk)
	if ondisk.Version != core.CurrentRegistryVersion {
		t.Errorf("on-disk version = %d, want %d", ondisk.Version, core.CurrentRegistryVersion)
	}
}

func TestRegistryMigrate_AlreadyCurrent(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := NewRegistry(regFile)
	reg.Init()
	reg.AddProject("app", "/tmp", "dev")

	info1, _ := os.Stat(regFile)
	mod1 := info1.ModTime()

	_, found, err := reg.GetProject("app")
	if err != nil {
		t.Fatalf("GetProject failed: %v", err)
	}
	if !found {
		t.Error("project not found")
	}

	raw, _ := os.ReadFile(regFile)
	var data core.RegistryData
	json.Unmarshal(raw, &data)
	if data.Version != core.CurrentRegistryVersion {
		t.Errorf("version = %d, want %d", data.Version, core.CurrentRegistryVersion)
	}
	_ = mod1
}

func TestRegistryMigrate_UnknownVersion(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")

	futureData := core.RegistryData{
		Version:  999,
		Projects: map[string]core.ProjectEntry{},
	}
	b, _ := json.MarshalIndent(futureData, "", "  ")
	os.WriteFile(regFile, append(b, '\n'), 0644)

	reg := NewRegistry(regFile)
	_, _, err := reg.GetProject("anything")
	if err == nil {
		t.Fatal("expected error for unknown version, got nil")
	}
}
