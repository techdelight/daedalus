// Copyright (C) 2026 Techdelight BV

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/personas"
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

func TestCollectBuildSpecs(t *testing.T) {
	tests := []struct {
		name           string
		targetOverride bool
		target         string
		agent          string
		entries        []core.ProjectInfo
		wantImages     []string
		wantTargets    []string
	}{
		{
			name:           "single claude project",
			targetOverride: false,
			target:         "dev",
			entries: []core.ProjectInfo{
				{Name: "app1", Entry: core.ProjectEntry{Target: "dev"}},
			},
			wantImages:  []string{"techdelight/claude-runner:dev"},
			wantTargets: []string{"dev"},
		},
		{
			name:           "dedup same target from multiple projects",
			targetOverride: false,
			target:         "dev",
			entries: []core.ProjectInfo{
				{Name: "app1", Entry: core.ProjectEntry{Target: "dev"}},
				{Name: "app2", Entry: core.ProjectEntry{Target: "dev"}},
			},
			wantImages:  []string{"techdelight/claude-runner:dev"},
			wantTargets: []string{"dev"},
		},
		{
			name:           "multiple unique targets sorted by image",
			targetOverride: false,
			target:         "dev",
			entries: []core.ProjectInfo{
				{Name: "game", Entry: core.ProjectEntry{Target: "godot"}},
				{Name: "api", Entry: core.ProjectEntry{Target: "dev"}},
			},
			wantImages:  []string{"techdelight/claude-runner:dev", "techdelight/claude-runner:godot"},
			wantTargets: []string{"dev", "godot"},
		},
		{
			name:           "target override uses explicit target only",
			targetOverride: true,
			target:         "godot",
			entries: []core.ProjectInfo{
				{Name: "app1", Entry: core.ProjectEntry{Target: "dev"}},
			},
			wantImages:  []string{"techdelight/claude-runner:godot"},
			wantTargets: []string{"godot"},
		},
		{
			name:           "copilot agent produces copilot-runner image",
			targetOverride: false,
			target:         "dev",
			entries: []core.ProjectInfo{
				{Name: "app1", Entry: core.ProjectEntry{
					Target:       "dev",
					DefaultFlags: map[string]string{"agent": "copilot"},
				}},
			},
			wantImages:  []string{"techdelight/copilot-runner:dev"},
			wantTargets: []string{"copilot-dev"},
		},
		{
			name:           "mixed agents produce separate images",
			targetOverride: false,
			target:         "dev",
			entries: []core.ProjectInfo{
				{Name: "api", Entry: core.ProjectEntry{Target: "dev"}},
				{Name: "copilot-app", Entry: core.ProjectEntry{
					Target:       "dev",
					DefaultFlags: map[string]string{"agent": "copilot"},
				}},
			},
			wantImages:  []string{"techdelight/claude-runner:dev", "techdelight/copilot-runner:dev"},
			wantTargets: []string{"dev", "copilot-dev"},
		},
		{
			name:           "target override with copilot agent",
			targetOverride: true,
			target:         "dev",
			agent:          "copilot",
			entries: []core.ProjectInfo{
				{Name: "app1", Entry: core.ProjectEntry{Target: "dev"}},
			},
			wantImages:  []string{"techdelight/copilot-runner:dev"},
			wantTargets: []string{"copilot-dev"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &core.Config{
				ImagePrefix:    "techdelight/claude-runner",
				Target:         tc.target,
				TargetOverride: tc.targetOverride,
				Runner:         tc.agent,
			}

			got := collectBuildSpecs(cfg, tc.entries)

			if len(got) != len(tc.wantImages) {
				t.Fatalf("collectBuildSpecs() returned %d specs, want %d: got %v", len(got), len(tc.wantImages), got)
			}
			for i, spec := range got {
				if spec.imageName != tc.wantImages[i] {
					t.Errorf("specs[%d].imageName = %q, want %q", i, spec.imageName, tc.wantImages[i])
				}
				if spec.dockerTarget != tc.wantTargets[i] {
					t.Errorf("specs[%d].dockerTarget = %q, want %q", i, spec.dockerTarget, tc.wantTargets[i])
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

func TestPrintBuildDebugInfo(t *testing.T) {
	tests := []struct {
		name      string
		scriptDir string
		target    string
		image     string
		wantParts []string
	}{
		{
			name:      "prints all expected fields",
			scriptDir: "/opt/daedalus",
			target:    "dev",
			image:     "techdelight/claude-runner:dev",
			wantParts: []string{
				"--- Build Debug Info ---",
				"/opt/daedalus/Dockerfile",
				"/opt/daedalus/docker-compose.yml",
				"Target:           dev",
				"Image:            techdelight/claude-runner:dev",
				"Environment variables:",
				"--- End Build Debug Info ---",
			},
		},
		{
			name:      "uses correct paths for custom script dir",
			scriptDir: "/home/user/daedalus",
			target:    "godot",
			image:     "techdelight/claude-runner:godot",
			wantParts: []string{
				"/home/user/daedalus/Dockerfile",
				"/home/user/daedalus/docker-compose.yml",
				"Target:           godot",
				"Image:            techdelight/claude-runner:godot",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			cfg := &core.Config{
				ScriptDir: tc.scriptDir,
			}

			// Set a known env var so we can verify env output
			t.Setenv("DAEDALUS_TEST_VAR", "test_value_123")

			// Capture stdout
			old := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			// Act
			printBuildDebugInfo(cfg, tc.target, tc.image)

			w.Close()
			var buf [65536]byte
			n, _ := r.Read(buf[:])
			os.Stdout = old

			output := string(buf[:n])

			// Assert
			for _, part := range tc.wantParts {
				if !strings.Contains(output, part) {
					t.Errorf("output missing %q\ngot:\n%s", part, output)
				}
			}

			// Verify env vars are included
			if !strings.Contains(output, "DAEDALUS_TEST_VAR=test_value_123") {
				t.Errorf("output missing test env var\ngot:\n%s", output)
			}
		})
	}
}

func TestPrintBuildDebugInfo_EnvVarsSorted(t *testing.T) {
	// Arrange
	cfg := &core.Config{
		ScriptDir: "/opt/daedalus",
	}

	t.Setenv("DAEDALUS_AAA", "first")
	t.Setenv("DAEDALUS_ZZZ", "last")

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Act
	printBuildDebugInfo(cfg, "dev", "img:dev")

	w.Close()
	var buf [65536]byte
	n, _ := r.Read(buf[:])
	os.Stdout = old

	output := string(buf[:n])

	// Assert: AAA should appear before ZZZ
	aaaIdx := strings.Index(output, "DAEDALUS_AAA")
	zzzIdx := strings.Index(output, "DAEDALUS_ZZZ")
	if aaaIdx == -1 || zzzIdx == -1 {
		t.Fatalf("expected both DAEDALUS_AAA and DAEDALUS_ZZZ in output, got:\n%s", output)
	}
	if aaaIdx >= zzzIdx {
		t.Errorf("DAEDALUS_AAA (at %d) should appear before DAEDALUS_ZZZ (at %d)", aaaIdx, zzzIdx)
	}
}

func TestCollectDefaultFlags_DisplayFlag(t *testing.T) {
	tests := []struct {
		name    string
		cfg     core.Config
		wantKey string
		wantVal string
		wantNil bool
	}{
		{
			name:    "display true is collected",
			cfg:     core.Config{Display: true},
			wantKey: "display",
			wantVal: "true",
		},
		{
			name:    "display false not collected",
			cfg:     core.Config{Display: false},
			wantNil: true,
		},
		{
			name:    "display and dind both collected",
			cfg:     core.Config{Display: true, DinD: true},
			wantKey: "display",
			wantVal: "true",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			cfg := &tc.cfg

			// Act
			flags := collectDefaultFlags(cfg)

			// Assert
			if tc.wantNil {
				if flags != nil {
					t.Errorf("flags = %v, want nil", flags)
				}
				return
			}
			if flags[tc.wantKey] != tc.wantVal {
				t.Errorf("flags[%q] = %q, want %q", tc.wantKey, flags[tc.wantKey], tc.wantVal)
			}
		})
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

// --- Runner tests ---

func TestListRunners(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := listRunners()

	w.Close()
	var buf [4096]byte
	n, _ := r.Read(buf[:])
	os.Stdout = old

	if err != nil {
		t.Fatalf("listRunners failed: %v", err)
	}
	output := string(buf[:n])
	if !strings.Contains(output, "claude") {
		t.Error("output should contain 'claude'")
	}
	if !strings.Contains(output, "copilot") {
		t.Error("output should contain 'copilot'")
	}
}

func TestShowRunner_Claude(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := showRunner("claude")

	w.Close()
	var buf [4096]byte
	n, _ := r.Read(buf[:])
	os.Stdout = old

	if err != nil {
		t.Fatalf("showRunner(claude) failed: %v", err)
	}
	output := string(buf[:n])
	if !strings.Contains(output, "/opt/claude/bin/claude") {
		t.Error("output should show binary path")
	}
}

func TestShowRunner_Copilot(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := showRunner("copilot")

	w.Close()
	var buf [4096]byte
	n, _ := r.Read(buf[:])
	os.Stdout = old

	if err != nil {
		t.Fatalf("showRunner(copilot) failed: %v", err)
	}
	output := string(buf[:n])
	if !strings.Contains(output, "copilot") {
		t.Error("output should contain 'copilot'")
	}
}

func TestShowRunner_Unknown(t *testing.T) {
	err := showRunner("gpt")
	if err == nil {
		t.Fatal("expected error for unknown runner")
	}
	if !strings.Contains(err.Error(), "unknown runner") {
		t.Errorf("error = %q, want mention of 'unknown runner'", err)
	}
}

func TestManageRunners_UnknownSubcommand(t *testing.T) {
	cfg := &core.Config{RunnersArgs: []string{"create"}}
	err := manageRunners(cfg)
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown runners command") {
		t.Errorf("error = %q, want mention of 'unknown runners command'", err)
	}
}

func TestManageRunners_ShowMissingName(t *testing.T) {
	cfg := &core.Config{RunnersArgs: []string{"show"}}
	err := manageRunners(cfg)
	if err == nil {
		t.Fatal("expected error for show without name")
	}
	if !strings.Contains(err.Error(), "usage:") {
		t.Errorf("error = %q, want usage hint", err)
	}
}

// --- Persona configuration tests ---

func TestListPersonas_Empty(t *testing.T) {
	dir := t.TempDir()
	personasDir := filepath.Join(dir, "personas")
	os.MkdirAll(personasDir, 0755)
	store := personas.New(personasDir)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := listPersonas(store)

	w.Close()
	var buf [4096]byte
	n, _ := r.Read(buf[:])
	os.Stdout = old

	if err != nil {
		t.Fatalf("listPersonas failed: %v", err)
	}
	output := string(buf[:n])
	if !strings.Contains(output, "No personas defined") {
		t.Error("output should show empty-state message")
	}
	// Should NOT list built-in runners
	if strings.Contains(output, "claude") {
		t.Error("output should not contain built-in runners")
	}
}

func TestListPersonas_WithUserDefined(t *testing.T) {
	dir := t.TempDir()
	personasDir := filepath.Join(dir, "personas")
	os.MkdirAll(personasDir, 0755)
	store := personas.New(personasDir)

	cfg := core.PersonaConfig{
		Name:        "reviewer",
		Description: "Code review specialist",
		BaseRunner:  "claude",
	}
	if err := store.Create(cfg); err != nil {
		t.Fatal(err)
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := listPersonas(store)

	w.Close()
	var buf [4096]byte
	n, _ := r.Read(buf[:])
	os.Stdout = old

	if err != nil {
		t.Fatalf("listPersonas failed: %v", err)
	}
	output := string(buf[:n])
	if !strings.Contains(output, "reviewer") {
		t.Error("output should contain 'reviewer'")
	}
	if !strings.Contains(output, "Code review specialist") {
		t.Error("output should contain description")
	}
}

func TestShowPersona_BuiltIn_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	store := personas.New(dir)

	err := showPersona(store, "claude")
	if err == nil {
		t.Fatal("expected error for built-in runner name")
	}
	if !strings.Contains(err.Error(), "built-in runner") {
		t.Errorf("error = %q, want mention of 'built-in runner'", err)
	}
}

func TestShowPersona_UserDefined(t *testing.T) {
	dir := t.TempDir()
	personasDir := filepath.Join(dir, "personas")
	os.MkdirAll(personasDir, 0755)
	store := personas.New(personasDir)

	cfg := core.PersonaConfig{
		Name:       "reviewer",
		BaseRunner: "claude",
		ClaudeMd:   "You review code.",
	}
	store.Create(cfg)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := showPersona(store, "reviewer")

	w.Close()
	var buf [4096]byte
	n, _ := r.Read(buf[:])
	os.Stdout = old

	if err != nil {
		t.Fatalf("showPersona(reviewer) failed: %v", err)
	}
	output := string(buf[:n])

	var result core.PersonaConfig
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, output)
	}
	if result.Name != "reviewer" {
		t.Errorf("Name = %q, want %q", result.Name, "reviewer")
	}
}

func TestShowPersona_NotFound(t *testing.T) {
	dir := t.TempDir()
	store := personas.New(dir)

	err := showPersona(store, "nonexistent")
	if err == nil {
		t.Fatal("showPersona(nonexistent) = nil, want error")
	}
}

func TestRemovePersona_Success(t *testing.T) {
	dir := t.TempDir()
	personasDir := filepath.Join(dir, "personas")
	os.MkdirAll(personasDir, 0755)
	store := personas.New(personasDir)

	cfg := core.PersonaConfig{Name: "reviewer", BaseRunner: "claude"}
	store.Create(cfg)

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := removePersona(store, "reviewer")

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("removePersona failed: %v", err)
	}

	// Verify it's gone
	if _, err := store.Read("reviewer"); err == nil {
		t.Error("persona should be removed but Read succeeded")
	}
}

func TestRemovePersona_BuiltIn(t *testing.T) {
	dir := t.TempDir()
	store := personas.New(dir)

	err := removePersona(store, "claude")
	if err == nil {
		t.Fatal("removePersona(claude) = nil, want error")
	}
	if !strings.Contains(err.Error(), "cannot remove built-in") {
		t.Errorf("error = %q, want mention of 'cannot remove built-in'", err)
	}
}

func TestRemovePersona_NotFound(t *testing.T) {
	dir := t.TempDir()
	store := personas.New(dir)

	err := removePersona(store, "nonexistent")
	if err == nil {
		t.Fatal("removePersona(nonexistent) = nil, want error")
	}
}

func TestManagePersonas_UnknownSubcommand(t *testing.T) {
	dir := t.TempDir()
	cfg := &core.Config{
		DataDir:      dir,
		PersonasArgs: []string{"invalid"},
	}

	err := managePersonas(cfg)
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown personas command") {
		t.Errorf("error = %q, want mention of 'unknown personas command'", err)
	}
}

func TestManagePersonas_ShowMissingName(t *testing.T) {
	dir := t.TempDir()
	cfg := &core.Config{
		DataDir:      dir,
		PersonasArgs: []string{"show"},
	}

	err := managePersonas(cfg)
	if err == nil {
		t.Fatal("expected error for missing name")
	}
	if !strings.Contains(err.Error(), "usage") {
		t.Errorf("error = %q, want usage hint", err)
	}
}

func TestManagePersonas_CreateMissingName(t *testing.T) {
	dir := t.TempDir()
	cfg := &core.Config{
		DataDir:      dir,
		PersonasArgs: []string{"create"},
	}

	err := managePersonas(cfg)
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestManagePersonas_RemoveMissingName(t *testing.T) {
	dir := t.TempDir()
	cfg := &core.Config{
		DataDir:      dir,
		PersonasArgs: []string{"remove"},
	}

	err := managePersonas(cfg)
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestResolvePersonaOverlay_NoPersona(t *testing.T) {
	dir := t.TempDir()
	cfg := &core.Config{
		DataDir:     dir,
		Runner:      "claude",
		ProjectName: "test",
	}
	overlay, err := resolvePersonaOverlay(cfg)
	if err != nil {
		t.Fatalf("resolvePersonaOverlay failed: %v", err)
	}
	if overlay != nil {
		t.Error("overlay should be nil when no persona is set")
	}
}

func TestResolvePersonaOverlay_UserDefined(t *testing.T) {
	dir := t.TempDir()
	personasDir := filepath.Join(dir, "personas")
	os.MkdirAll(personasDir, 0755)
	projectCache := filepath.Join(dir, "test")
	os.MkdirAll(projectCache, 0755)

	store := personas.New(personasDir)
	store.Create(core.PersonaConfig{
		Name:       "reviewer",
		BaseRunner: "claude",
		ClaudeMd:   "You are a reviewer.",
		Env:        map[string]string{"MODE": "review"},
		Settings:   json.RawMessage(`{"permissions":{"allow":["Read"]}}`),
	})

	cfg := &core.Config{
		DataDir:     dir,
		Persona:     "reviewer",
		ProjectName: "test",
	}
	overlay, err := resolvePersonaOverlay(cfg)
	if err != nil {
		t.Fatalf("resolvePersonaOverlay failed: %v", err)
	}
	if overlay == nil {
		t.Fatal("overlay should not be nil for user-defined persona")
	}
	if overlay.ClaudeMdPath == "" {
		t.Error("ClaudeMdPath should be set")
	}
	if overlay.SettingsPath == "" {
		t.Error("SettingsPath should be set")
	}
	if overlay.Env["MODE"] != "review" {
		t.Errorf("Env[MODE] = %q, want %q", overlay.Env["MODE"], "review")
	}
	// resolvePersonaOverlay should set Runner from persona's BaseRunner
	if cfg.Runner != "claude" {
		t.Errorf("Runner = %q, want %q (should be set from persona's BaseRunner)", cfg.Runner, "claude")
	}

	// Verify files were written
	data, err := os.ReadFile(overlay.ClaudeMdPath)
	if err != nil {
		t.Fatalf("reading CLAUDE.md: %v", err)
	}
	if string(data) != "You are a reviewer." {
		t.Errorf("CLAUDE.md content = %q, want %q", string(data), "You are a reviewer.")
	}
}

func TestResolvePersonaOverlay_SetsRunnerFromPersona(t *testing.T) {
	dir := t.TempDir()
	personasDir := filepath.Join(dir, "personas")
	os.MkdirAll(personasDir, 0755)
	projectCache := filepath.Join(dir, "test")
	os.MkdirAll(projectCache, 0755)

	store := personas.New(personasDir)
	store.Create(core.PersonaConfig{
		Name:       "copilot-reviewer",
		BaseRunner: "copilot",
		ClaudeMd:   "Review with copilot.",
	})

	cfg := &core.Config{
		DataDir:     dir,
		Persona:     "copilot-reviewer",
		ProjectName: "test",
	}
	_, err := resolvePersonaOverlay(cfg)
	if err != nil {
		t.Fatalf("resolvePersonaOverlay failed: %v", err)
	}
	if cfg.Runner != "copilot" {
		t.Errorf("Runner = %q, want %q (from persona's BaseRunner)", cfg.Runner, "copilot")
	}
}

func TestResolvePersonaOverlay_ExplicitRunnerNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	personasDir := filepath.Join(dir, "personas")
	os.MkdirAll(personasDir, 0755)
	projectCache := filepath.Join(dir, "test")
	os.MkdirAll(projectCache, 0755)

	store := personas.New(personasDir)
	store.Create(core.PersonaConfig{
		Name:       "reviewer",
		BaseRunner: "copilot",
		ClaudeMd:   "Review.",
	})

	cfg := &core.Config{
		DataDir:     dir,
		Runner:      "claude",
		Persona:     "reviewer",
		ProjectName: "test",
	}
	_, err := resolvePersonaOverlay(cfg)
	if err != nil {
		t.Fatalf("resolvePersonaOverlay failed: %v", err)
	}
	// Explicit --runner should not be overwritten by persona's BaseRunner
	if cfg.Runner != "claude" {
		t.Errorf("Runner = %q, want %q (explicit runner should win)", cfg.Runner, "claude")
	}
}

func TestResolvePersonaOverlay_NotFound(t *testing.T) {
	dir := t.TempDir()
	projectCache := filepath.Join(dir, "test")
	os.MkdirAll(projectCache, 0755)

	cfg := &core.Config{
		DataDir:     dir,
		Persona:     "nonexistent",
		ProjectName: "test",
	}
	_, err := resolvePersonaOverlay(cfg)
	if err == nil {
		t.Fatal("expected error for nonexistent persona")
	}
}

func TestResolvePersonaOverlay_EmptyPersona(t *testing.T) {
	dir := t.TempDir()
	cfg := &core.Config{
		DataDir:     dir,
		ProjectName: "test",
		// Both Runner and Persona empty — no overlay
	}
	overlay, err := resolvePersonaOverlay(cfg)
	if err != nil {
		t.Fatalf("resolvePersonaOverlay failed: %v", err)
	}
	if overlay != nil {
		t.Error("overlay should be nil when persona is empty")
	}
}
