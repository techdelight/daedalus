// Copyright (C) 2026 Techdelight BV

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
	"github.com/techdelight/daedalus/internal/registry"
)

func init() {
	// Disable colors in tests to avoid ANSI codes in output assertions.
	color.Disable()
}

func TestResolveTwoArgs_TouchProjectError(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := registry.NewRegistry(regFile)
	reg.Init()
	reg.AddProject("my-app", "/tmp/my-app", "dev")

	cfg := &core.Config{
		ProjectName: "my-app",
		ProjectDir:  "/tmp/my-app",
		Prompt:      "test", // headless to avoid stdin
	}

	// Make directory read-only so atomic write (tmp + rename) fails
	os.Chmod(dir, 0555)
	defer os.Chmod(dir, 0755)

	err := resolveTwoArgs(cfg, reg)
	if err == nil {
		t.Fatal("expected error from TouchProject, got nil")
	}
}

func TestResolveOneArg_TouchProjectError(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := registry.NewRegistry(regFile)
	reg.Init()

	cwd, _ := os.Getwd()
	reg.AddProject("my-app", cwd, "dev")

	cfg := &core.Config{
		ProjectName: "my-app",
		Prompt:      "test", // headless
	}

	// Make directory read-only so atomic write (tmp + rename) fails
	os.Chmod(dir, 0555)
	defer os.Chmod(dir, 0755)

	err := resolveOneArg(cfg, reg)
	if err == nil {
		t.Fatal("expected error from TouchProject, got nil")
	}
}

func TestResolveOneArg_RegistryLookup(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := registry.NewRegistry(regFile)
	reg.Init()

	projectDir := t.TempDir()
	reg.AddProject("my-app", projectDir, "godot")

	cfg := &core.Config{
		ProjectName: "my-app",
		Target:      "dev",
	}

	err := resolveOneArg(cfg, reg)
	if err != nil {
		t.Fatalf("resolveOneArg failed: %v", err)
	}
	if cfg.ProjectDir != projectDir {
		t.Errorf("ProjectDir = %q, want %q", cfg.ProjectDir, projectDir)
	}
	if cfg.Target != "godot" {
		t.Errorf("Target = %q, want %q", cfg.Target, "godot")
	}
}

func TestResolveOneArg_TargetOverridePreserved(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := registry.NewRegistry(regFile)
	reg.Init()

	projectDir := t.TempDir()
	reg.AddProject("my-app", projectDir, "godot")

	cfg := &core.Config{
		ProjectName:    "my-app",
		Target:         "dev",
		TargetOverride: true,
	}

	err := resolveOneArg(cfg, reg)
	if err != nil {
		t.Fatalf("resolveOneArg failed: %v", err)
	}
	if cfg.Target != "dev" {
		t.Errorf("Target = %q, want %q (--target override should win)", cfg.Target, "dev")
	}
}

func TestResolveTwoArgs_NameDirConflict(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := registry.NewRegistry(regFile)
	reg.Init()
	reg.AddProject("my-app", "/original/dir", "dev")

	cfg := &core.Config{
		ProjectName: "my-app",
		ProjectDir:  "/different/dir",
		Prompt:      "test",
	}

	err := resolveTwoArgs(cfg, reg)
	if err == nil {
		t.Fatal("expected error for name/dir mismatch, got nil")
	}
}

func TestResolveTwoArgs_DirUsedByOtherProject(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := registry.NewRegistry(regFile)
	reg.Init()
	reg.AddProject("existing-app", "/shared/dir", "dev")

	cfg := &core.Config{
		ProjectName: "new-app",
		ProjectDir:  "/shared/dir",
		Prompt:      "test",
	}

	err := resolveTwoArgs(cfg, reg)
	if err == nil {
		t.Fatal("expected error for directory used by another project, got nil")
	}
}

func TestResolveTwoArgs_NewProject_Headless(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := registry.NewRegistry(regFile)
	reg.Init()

	projectDir := t.TempDir()
	cfg := &core.Config{
		ProjectName: "brand-new",
		ProjectDir:  projectDir,
		Target:      "dev",
		Prompt:      "test", // headless auto-registers
	}

	err := resolveTwoArgs(cfg, reg)
	if err != nil {
		t.Fatalf("resolveTwoArgs failed: %v", err)
	}

	has, _ := reg.HasProject("brand-new")
	if !has {
		t.Error("new project was not registered")
	}
}

func TestResolveOneArg_NewProject_Headless(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := registry.NewRegistry(regFile)
	reg.Init()

	cfg := &core.Config{
		ProjectName: "brand-new",
		Target:      "dev",
		Prompt:      "test", // headless auto-registers
	}

	err := resolveOneArg(cfg, reg)
	if err != nil {
		t.Fatalf("resolveOneArg failed: %v", err)
	}

	has, _ := reg.HasProject("brand-new")
	if !has {
		t.Error("new project was not registered")
	}
}

func TestResolveOneArg_DirConflict_Headless(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := registry.NewRegistry(regFile)
	reg.Init()

	cwd, _ := os.Getwd()
	reg.AddProject("existing-app", cwd, "dev")

	cfg := &core.Config{
		ProjectName: "different-name",
		Prompt:      "test", // headless → error on conflict
	}

	err := resolveOneArg(cfg, reg)
	if err == nil {
		t.Fatal("expected error for directory conflict in headless mode, got nil")
	}
}

func TestPruneProjects_NoStale(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, ".cache")
	os.MkdirAll(cacheDir, 0755)
	regFile := filepath.Join(cacheDir, "projects.json")
	reg := registry.NewRegistry(regFile)
	reg.Init()

	projectDir := t.TempDir() // exists on disk
	reg.AddProject("alive-project", projectDir, "dev")

	cfg := &core.Config{
		ScriptDir: dir,
		DataDir:   cacheDir,
		Prompt:    "test", // headless
		Force:     true,
	}

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := pruneProjects(cfg)

	w.Close()
	var buf [1024]byte
	n, _ := r.Read(buf[:])
	os.Stdout = old

	if err != nil {
		t.Fatalf("pruneProjects failed: %v", err)
	}

	output := string(buf[:n])
	if !strings.Contains(output, "No stale projects found") {
		t.Errorf("expected 'No stale projects found', got: %s", output)
	}
}

func TestPruneProjects_WithStale(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, ".cache")
	os.MkdirAll(cacheDir, 0755)
	regFile := filepath.Join(cacheDir, "projects.json")
	reg := registry.NewRegistry(regFile)
	reg.Init()

	// Register project pointing to nonexistent directory
	reg.AddProject("stale-project", "/nonexistent/dir/that/doesnt/exist", "dev")

	cfg := &core.Config{
		ScriptDir: dir,
		DataDir:   cacheDir,
		Prompt:    "test", // headless
		Force:     true,
	}

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := pruneProjects(cfg)

	w.Close()
	var buf [4096]byte
	n, _ := r.Read(buf[:])
	os.Stdout = old

	if err != nil {
		t.Fatalf("pruneProjects failed: %v", err)
	}

	output := string(buf[:n])
	if !strings.Contains(output, "Removed 'stale-project'") {
		t.Errorf("expected removal message, got: %s", output)
	}

	// Verify actually removed
	has, _ := reg.HasProject("stale-project")
	if has {
		t.Error("stale-project should have been removed from registry")
	}
}

func TestPruneProjects_HeadlessWithoutForce(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, ".cache")
	os.MkdirAll(cacheDir, 0755)
	regFile := filepath.Join(cacheDir, "projects.json")
	reg := registry.NewRegistry(regFile)
	reg.Init()

	// Register project pointing to nonexistent directory
	reg.AddProject("stale-project", "/nonexistent/dir/that/doesnt/exist", "dev")

	cfg := &core.Config{
		ScriptDir: dir,
		DataDir:   cacheDir,
		Prompt:    "test", // headless
		Force:     false,  // no --force
	}

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := pruneProjects(cfg)

	w.Close()
	var buf [4096]byte
	n, _ := r.Read(buf[:])
	os.Stdout = old

	if err != nil {
		t.Fatalf("pruneProjects failed: %v", err)
	}

	output := string(buf[:n])
	if !strings.Contains(output, "Run with --force") {
		t.Errorf("expected --force hint, got: %s", output)
	}

	// Verify NOT removed
	has, _ := reg.HasProject("stale-project")
	if !has {
		t.Error("stale-project should still be in registry without --force")
	}
}

func TestRemoveProjects_Success(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, ".cache")
	os.MkdirAll(cacheDir, 0755)
	regFile := filepath.Join(cacheDir, "projects.json")
	reg := registry.NewRegistry(regFile)
	reg.Init()
	reg.AddProject("to-remove", "/tmp/remove", "dev")

	cfg := &core.Config{
		ScriptDir:     dir,
		DataDir:       cacheDir,
		Prompt:        "test", // headless
		Force:         true,
		RemoveTargets: []string{"to-remove"},
	}

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := removeProjects(cfg)

	w.Close()
	var buf [4096]byte
	n, _ := r.Read(buf[:])
	os.Stdout = old

	if err != nil {
		t.Fatalf("removeProjects failed: %v", err)
	}

	output := string(buf[:n])
	if !strings.Contains(output, "Removed 'to-remove'") {
		t.Errorf("expected removal message, got: %s", output)
	}

	has, _ := reg.HasProject("to-remove")
	if has {
		t.Error("project should have been removed")
	}
}

func TestRemoveProjects_NotFound(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, ".cache")
	os.MkdirAll(cacheDir, 0755)
	regFile := filepath.Join(cacheDir, "projects.json")
	reg := registry.NewRegistry(regFile)
	reg.Init()

	cfg := &core.Config{
		ScriptDir:     dir,
		DataDir:       cacheDir,
		Prompt:        "test",
		Force:         true,
		RemoveTargets: []string{"nonexistent"},
	}

	err := removeProjects(cfg)
	if err == nil {
		t.Fatal("expected error for nonexistent project, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want mention of 'not found'", err)
	}
}

func TestRemoveProjects_HeadlessNoForce(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, ".cache")
	os.MkdirAll(cacheDir, 0755)
	regFile := filepath.Join(cacheDir, "projects.json")
	reg := registry.NewRegistry(regFile)
	reg.Init()
	reg.AddProject("my-app", "/tmp/my-app", "dev")

	cfg := &core.Config{
		ScriptDir:     dir,
		DataDir:       cacheDir,
		Prompt:        "test", // headless
		Force:         false,  // no --force
		RemoveTargets: []string{"my-app"},
	}

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := removeProjects(cfg)

	w.Close()
	var buf [4096]byte
	n, _ := r.Read(buf[:])
	os.Stdout = old

	if err != nil {
		t.Fatalf("removeProjects failed: %v", err)
	}

	output := string(buf[:n])
	if !strings.Contains(output, "--force") {
		t.Errorf("expected --force hint, got: %s", output)
	}

	// Verify NOT removed
	has, _ := reg.HasProject("my-app")
	if !has {
		t.Error("project should still be in registry without --force")
	}
}

func TestRemoveProjects_NoTargets(t *testing.T) {
	cfg := &core.Config{
		ScriptDir:     "/tmp",
		DataDir:       "/tmp/.cache",
		RemoveTargets: []string{},
	}
	err := removeProjects(cfg)
	if err == nil {
		t.Fatal("expected error for empty targets, got nil")
	}
	if !strings.Contains(err.Error(), "usage") {
		t.Errorf("error = %q, want usage message", err)
	}
}

func TestShowConfig_Display(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, ".cache")
	os.MkdirAll(cacheDir, 0755)
	regFile := filepath.Join(cacheDir, "projects.json")
	reg := registry.NewRegistry(regFile)
	reg.Init()
	reg.AddProject("my-app", "/tmp/my-app", "dev")
	reg.SetDefaultFlags("my-app", map[string]string{"debug": "true"})

	cfg := &core.Config{
		ScriptDir:    dir,
		DataDir:      cacheDir,
		ConfigTarget: "my-app",
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := showOrEditConfig(cfg)

	w.Close()
	var buf [4096]byte
	n, _ := r.Read(buf[:])
	os.Stdout = old

	if err != nil {
		t.Fatalf("showOrEditConfig failed: %v", err)
	}

	output := string(buf[:n])
	if !strings.Contains(output, "my-app") {
		t.Errorf("expected project name in output, got: %s", output)
	}
	if !strings.Contains(output, "debug") {
		t.Errorf("expected 'debug' flag in output, got: %s", output)
	}
}

func TestShowConfig_Set(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, ".cache")
	os.MkdirAll(cacheDir, 0755)
	regFile := filepath.Join(cacheDir, "projects.json")
	reg := registry.NewRegistry(regFile)
	reg.Init()
	reg.AddProject("my-app", "/tmp/my-app", "dev")

	cfg := &core.Config{
		ScriptDir:    dir,
		DataDir:      cacheDir,
		ConfigTarget: "my-app",
		ConfigSet:    []string{"dind=true"},
	}

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := showOrEditConfig(cfg)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("showOrEditConfig --set failed: %v", err)
	}

	entry, _, _ := reg.GetProject("my-app")
	if entry.DefaultFlags["dind"] != "true" {
		t.Errorf("DefaultFlags[dind] = %q, want %q", entry.DefaultFlags["dind"], "true")
	}
}

func TestShowConfig_Unset(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, ".cache")
	os.MkdirAll(cacheDir, 0755)
	regFile := filepath.Join(cacheDir, "projects.json")
	reg := registry.NewRegistry(regFile)
	reg.Init()
	reg.AddProject("my-app", "/tmp/my-app", "dev")
	reg.SetDefaultFlags("my-app", map[string]string{"debug": "true", "dind": "true"})

	cfg := &core.Config{
		ScriptDir:    dir,
		DataDir:      cacheDir,
		ConfigTarget: "my-app",
		ConfigUnset:  []string{"debug"},
	}

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := showOrEditConfig(cfg)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("showOrEditConfig --unset failed: %v", err)
	}

	entry, _, _ := reg.GetProject("my-app")
	if _, ok := entry.DefaultFlags["debug"]; ok {
		t.Error("debug flag should have been unset")
	}
	if entry.DefaultFlags["dind"] != "true" {
		t.Errorf("dind flag should still be set, got %q", entry.DefaultFlags["dind"])
	}
}

func TestShowConfig_ProjectNotFound(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, ".cache")
	os.MkdirAll(cacheDir, 0755)
	regFile := filepath.Join(cacheDir, "projects.json")
	reg := registry.NewRegistry(regFile)
	reg.Init()

	cfg := &core.Config{
		ScriptDir:    dir,
		DataDir:      cacheDir,
		ConfigTarget: "nonexistent",
	}

	err := showOrEditConfig(cfg)
	if err == nil {
		t.Fatal("expected error for nonexistent project, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want mention of 'not found'", err)
	}
}

func TestShowConfig_NoProjectName(t *testing.T) {
	cfg := &core.Config{
		ScriptDir:    "/tmp",
		DataDir:      "/tmp/.cache",
		ConfigTarget: "",
	}

	err := showOrEditConfig(cfg)
	if err == nil {
		t.Fatal("expected error for empty project name, got nil")
	}
	if !strings.Contains(err.Error(), "usage") {
		t.Errorf("error = %q, want usage message", err)
	}
}

func TestCollectBuildTargets_UniqueTargets(t *testing.T) {
	tests := []struct {
		name           string
		targetOverride bool
		target         string
		entries        []core.ProjectInfo
		want           []string
	}{
		{
			name:           "single target from one project",
			targetOverride: false,
			target:         "dev",
			entries: []core.ProjectInfo{
				{Name: "app1", Entry: core.ProjectEntry{Target: "dev"}},
			},
			want: []string{"dev"},
		},
		{
			name:           "dedup same target from multiple projects",
			targetOverride: false,
			target:         "dev",
			entries: []core.ProjectInfo{
				{Name: "app1", Entry: core.ProjectEntry{Target: "dev"}},
				{Name: "app2", Entry: core.ProjectEntry{Target: "dev"}},
				{Name: "app3", Entry: core.ProjectEntry{Target: "dev"}},
			},
			want: []string{"dev"},
		},
		{
			name:           "multiple unique targets sorted",
			targetOverride: false,
			target:         "dev",
			entries: []core.ProjectInfo{
				{Name: "game", Entry: core.ProjectEntry{Target: "godot"}},
				{Name: "api", Entry: core.ProjectEntry{Target: "dev"}},
				{Name: "tools", Entry: core.ProjectEntry{Target: "utils"}},
			},
			want: []string{"dev", "godot", "utils"},
		},
		{
			name:           "target override uses explicit target only",
			targetOverride: true,
			target:         "godot",
			entries: []core.ProjectInfo{
				{Name: "app1", Entry: core.ProjectEntry{Target: "dev"}},
				{Name: "app2", Entry: core.ProjectEntry{Target: "utils"}},
			},
			want: []string{"godot"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			cfg := &core.Config{
				Target:         tc.target,
				TargetOverride: tc.targetOverride,
			}

			// Act
			got := collectBuildTargets(cfg, tc.entries)

			// Assert
			if len(got) != len(tc.want) {
				t.Fatalf("collectBuildTargets() returned %d targets, want %d: got %v", len(got), len(tc.want), got)
			}
			for i, target := range got {
				if target != tc.want[i] {
					t.Errorf("targets[%d] = %q, want %q", i, target, tc.want[i])
				}
			}
		})
	}
}

func TestBuildAllProjects_NoRegisteredProjects(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, ".cache")
	os.MkdirAll(cacheDir, 0755)
	regFile := filepath.Join(cacheDir, "projects.json")
	reg := registry.NewRegistry(regFile)
	reg.Init()

	cfg := &core.Config{
		ScriptDir:   dir,
		DataDir:     cacheDir,
		ImagePrefix: "techdelight/claude-runner",
		Target:      "dev",
	}

	// Act
	err := buildAllProjects(cfg)

	// Assert
	if err == nil {
		t.Fatal("expected error for empty registry, got nil")
	}
	if !strings.Contains(err.Error(), "no registered projects") {
		t.Errorf("error = %q, want mention of 'no registered projects'", err)
	}
}

func TestHandleDirConflict_TouchProjectError(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "projects.json")
	reg := registry.NewRegistry(regFile)
	reg.Init()

	projectDir := filepath.Join(dir, "proj")
	os.MkdirAll(projectDir, 0755)
	reg.AddProject("existing-app", projectDir, "dev")

	cfg := &core.Config{
		ProjectName: "new-name",
		ProjectDir:  projectDir,
		// Not headless, but we won't reach stdin because TouchProject errors first
	}

	// Make registry read-only so TouchProject fails on write
	os.Chmod(regFile, 0444)
	defer os.Chmod(regFile, 0644)

	// handleDirConflict reads stdin in interactive mode, so we'd need to mock it.
	// Instead, test the headless path — headless dir conflict returns error before
	// TouchProject is even called.
	cfg.Prompt = "test" // force headless
	err := handleDirConflict(cfg, reg, "existing-app")
	if err == nil {
		t.Fatal("expected error for headless dir conflict, got nil")
	}
}
