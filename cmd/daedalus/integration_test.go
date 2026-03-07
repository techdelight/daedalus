// Copyright (C) 2026 Techdelight BV

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/completions"
	"github.com/techdelight/daedalus/internal/config"
	"github.com/techdelight/daedalus/internal/docker"
	"github.com/techdelight/daedalus/internal/executor"
	"github.com/techdelight/daedalus/internal/registry"
	"github.com/techdelight/daedalus/internal/web"
)

// ---------------------------------------------------------------------------
// Test 1: Full project lifecycle
// ParseArgs → config loading → registry init → project registration →
// list → config → remove
// ---------------------------------------------------------------------------

func TestIntegration_FullProjectLifecycle(t *testing.T) {
	tests := []struct {
		name string
		// project registration args
		projectName string
		projectDir  string // relative to temp dir; resolved to absolute
		target      string
	}{
		{
			name:        "standard project lifecycle",
			projectName: "lifecycle-app",
			projectDir:  "projects/lifecycle",
			target:      "dev",
		},
		{
			name:        "godot target lifecycle",
			projectName: "godot-game",
			projectDir:  "projects/game",
			target:      "godot",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			tmpDir := t.TempDir()
			cacheDir := filepath.Join(tmpDir, ".cache")
			if err := os.MkdirAll(cacheDir, 0755); err != nil {
				t.Fatalf("creating cache dir: %v", err)
			}

			projectDir := filepath.Join(tmpDir, tc.projectDir)
			if err := os.MkdirAll(projectDir, 0755); err != nil {
				t.Fatalf("creating project dir: %v", err)
			}

			regFile := filepath.Join(cacheDir, "projects.json")
			reg := registry.NewRegistry(regFile)
			if err := reg.Init(); err != nil {
				t.Fatalf("registry init: %v", err)
			}

			// Act: register project
			if err := reg.AddProject(tc.projectName, projectDir, tc.target); err != nil {
				t.Fatalf("AddProject: %v", err)
			}

			// Assert: project exists
			has, err := reg.HasProject(tc.projectName)
			if err != nil {
				t.Fatalf("HasProject: %v", err)
			}
			if !has {
				t.Fatal("project not found after registration")
			}

			// Act: list projects
			entries, err := reg.GetProjectEntries()
			if err != nil {
				t.Fatalf("GetProjectEntries: %v", err)
			}
			if len(entries) != 1 {
				t.Fatalf("expected 1 project, got %d", len(entries))
			}
			if entries[0].Name != tc.projectName {
				t.Errorf("project name = %q, want %q", entries[0].Name, tc.projectName)
			}
			if entries[0].Entry.Directory != projectDir {
				t.Errorf("directory = %q, want %q", entries[0].Entry.Directory, projectDir)
			}
			if entries[0].Entry.Target != tc.target {
				t.Errorf("target = %q, want %q", entries[0].Entry.Target, tc.target)
			}

			// Act: set config flags
			flags := map[string]string{"debug": "true", "dind": "true"}
			if err := reg.SetDefaultFlags(tc.projectName, flags); err != nil {
				t.Fatalf("SetDefaultFlags: %v", err)
			}

			// Assert: config display via showOrEditConfig
			cfg := &core.Config{
				ScriptDir:    tmpDir,
				DataDir:      cacheDir,
				ConfigTarget: tc.projectName,
			}

			old := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			err = showOrEditConfig(cfg)

			w.Close()
			var buf [4096]byte
			n, _ := r.Read(buf[:])
			os.Stdout = old

			if err != nil {
				t.Fatalf("showOrEditConfig: %v", err)
			}
			output := string(buf[:n])
			if !strings.Contains(output, tc.projectName) {
				t.Errorf("config output missing project name %q: %s", tc.projectName, output)
			}
			if !strings.Contains(output, "debug") {
				t.Errorf("config output missing 'debug' flag: %s", output)
			}

			// Act: remove project
			removeCfg := &core.Config{
				ScriptDir:     tmpDir,
				DataDir:       cacheDir,
				Prompt:        "test",
				Force:         true,
				RemoveTargets: []string{tc.projectName},
			}
			old = os.Stdout
			_, w, _ = os.Pipe()
			os.Stdout = w

			err = removeProjects(removeCfg)

			w.Close()
			os.Stdout = old

			if err != nil {
				t.Fatalf("removeProjects: %v", err)
			}

			// Assert: project removed
			has, err = reg.HasProject(tc.projectName)
			if err != nil {
				t.Fatalf("HasProject after remove: %v", err)
			}
			if has {
				t.Error("project still exists after removal")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test 2: Config precedence
// CLI flags > config.json > defaults
// ---------------------------------------------------------------------------

func TestIntegration_ConfigPrecedence(t *testing.T) {
	t.Run("defaults only (no config, no cli flags)", func(t *testing.T) {
		// Arrange: no config.json, default CLI args
		tmpDir := t.TempDir()
		appCfg, err := config.LoadAppConfig(tmpDir)
		if err != nil {
			t.Fatalf("LoadAppConfig: %v", err)
		}

		cfg, err := config.ParseArgs([]string{"my-project"})
		if err != nil {
			t.Fatalf("ParseArgs: %v", err)
		}

		// Act: apply (empty) app config
		core.ApplyAppConfig(cfg, appCfg)

		// Assert: all at defaults
		if cfg.Debug {
			t.Error("Debug = true, want false")
		}
		if cfg.NoTmux {
			t.Error("NoTmux = true, want false")
		}
	})

	t.Run("config.json enables debug", func(t *testing.T) {
		// Arrange
		tmpDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(`{"debug": true}`), 0644); err != nil {
			t.Fatalf("writing config.json: %v", err)
		}
		appCfg, err := config.LoadAppConfig(tmpDir)
		if err != nil {
			t.Fatalf("LoadAppConfig: %v", err)
		}

		cfg, err := config.ParseArgs([]string{"my-project"})
		if err != nil {
			t.Fatalf("ParseArgs: %v", err)
		}

		// Act
		core.ApplyAppConfig(cfg, appCfg)

		// Assert: config.json debug=true applied
		if !cfg.Debug {
			t.Error("Debug = false, want true (config.json should enable it)")
		}
	})

	t.Run("cli flag overrides config.json for no-tmux", func(t *testing.T) {
		// Arrange: config.json says no-tmux=false, CLI says --no-tmux
		tmpDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(`{"no-tmux": false}`), 0644); err != nil {
			t.Fatalf("writing config.json: %v", err)
		}
		appCfg, err := config.LoadAppConfig(tmpDir)
		if err != nil {
			t.Fatalf("LoadAppConfig: %v", err)
		}

		cfg, err := config.ParseArgs([]string{"--no-tmux", "my-project"})
		if err != nil {
			t.Fatalf("ParseArgs: %v", err)
		}

		// Act
		core.ApplyAppConfig(cfg, appCfg)

		// Assert: CLI wins
		if !cfg.NoTmux {
			t.Error("NoTmux = false, want true (CLI --no-tmux should win)")
		}
	})

	t.Run("cli debug flag wins over config false", func(t *testing.T) {
		// Arrange: config.json says debug=false, CLI says --debug
		tmpDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(`{"debug": false}`), 0644); err != nil {
			t.Fatalf("writing config.json: %v", err)
		}
		appCfg, err := config.LoadAppConfig(tmpDir)
		if err != nil {
			t.Fatalf("LoadAppConfig: %v", err)
		}

		cfg, err := config.ParseArgs([]string{"--debug", "my-project"})
		if err != nil {
			t.Fatalf("ParseArgs: %v", err)
		}

		// Act
		core.ApplyAppConfig(cfg, appCfg)

		// Assert: CLI --debug wins
		if !cfg.Debug {
			t.Error("Debug = false, want true (CLI --debug should win)")
		}
	})

	t.Run("config.json sets data-dir on fresh config", func(t *testing.T) {
		// Arrange: test ApplyAppConfig data-dir on a config with empty DataDir
		tmpDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(`{"data-dir": "/custom/data"}`), 0644); err != nil {
			t.Fatalf("writing config.json: %v", err)
		}
		appCfg, err := config.LoadAppConfig(tmpDir)
		if err != nil {
			t.Fatalf("LoadAppConfig: %v", err)
		}

		// Simulate pre-ParseArgs state: DataDir is empty (not yet defaulted)
		cfg := &core.Config{
			ProjectName: "my-project",
			Target:      "dev",
			ImagePrefix: "techdelight/claude-runner",
		}

		// Act
		core.ApplyAppConfig(cfg, appCfg)

		// Assert: config.json data-dir applied
		if cfg.DataDir != "/custom/data" {
			t.Errorf("DataDir = %q, want %q", cfg.DataDir, "/custom/data")
		}
	})

	t.Run("env var overrides config.json data-dir", func(t *testing.T) {
		// Arrange: config.json sets data-dir, but env var takes precedence
		tmpDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(`{"data-dir": "/from-config"}`), 0644); err != nil {
			t.Fatalf("writing config.json: %v", err)
		}
		appCfg, err := config.LoadAppConfig(tmpDir)
		if err != nil {
			t.Fatalf("LoadAppConfig: %v", err)
		}

		// Simulate env var already set DataDir before ApplyAppConfig runs
		cfg := &core.Config{
			DataDir: "/from-env",
		}

		// Act
		core.ApplyAppConfig(cfg, appCfg)

		// Assert: env var wins
		if cfg.DataDir != "/from-env" {
			t.Errorf("DataDir = %q, want %q (env var should win)", cfg.DataDir, "/from-env")
		}
	})

	t.Run("registry default flags apply to config", func(t *testing.T) {
		// Arrange: registry entry has default flags, CLI has no overrides
		entry := core.ProjectEntry{
			Directory:    "/tmp/my-project",
			Target:       "godot",
			DefaultFlags: map[string]string{"debug": "true", "dind": "true"},
		}
		cfg := &core.Config{
			ProjectName: "my-project",
			Target:      "dev",
		}

		// Act
		core.ApplyRegistryEntry(cfg, entry)

		// Assert: registry defaults applied
		if !cfg.Debug {
			t.Error("Debug = false, want true (from registry defaults)")
		}
		if !cfg.DinD {
			t.Error("DinD = false, want true (from registry defaults)")
		}
		if cfg.Target != "godot" {
			t.Errorf("Target = %q, want %q (from registry entry)", cfg.Target, "godot")
		}
	})

	t.Run("cli target override wins over registry", func(t *testing.T) {
		// Arrange: registry entry target=godot, CLI --target dev
		entry := core.ProjectEntry{
			Directory: "/tmp/my-project",
			Target:    "godot",
		}
		cfg := &core.Config{
			ProjectName:    "my-project",
			Target:         "dev",
			TargetOverride: true,
		}

		// Act
		core.ApplyRegistryEntry(cfg, entry)

		// Assert: CLI target wins
		if cfg.Target != "dev" {
			t.Errorf("Target = %q, want %q (CLI --target should win)", cfg.Target, "dev")
		}
	})
}

// ---------------------------------------------------------------------------
// Test 3: Registry lifecycle
// Init → add → list → update flags → session tracking → remove → verify
// ---------------------------------------------------------------------------

func TestIntegration_RegistryLifecycle(t *testing.T) {
	tests := []struct {
		name        string
		projectName string
		directory   string
		target      string
		flags       map[string]string
		setFlags    map[string]string
		unsetFlags  []string
		resumeID    string
	}{
		{
			name:        "full lifecycle with default flags and sessions",
			projectName: "reg-test",
			directory:   "/tmp/reg-test",
			target:      "dev",
			flags:       map[string]string{"debug": "true", "dind": "true"},
			setFlags:    map[string]string{"no-tmux": "true"},
			unsetFlags:  []string{"debug"},
			resumeID:    "",
		},
		{
			name:        "lifecycle with resume session",
			projectName: "resume-proj",
			directory:   "/tmp/resume-proj",
			target:      "godot",
			flags:       map[string]string{"dind": "true"},
			setFlags:    map[string]string{"debug": "true"},
			unsetFlags:  nil,
			resumeID:    "resume-abc-123",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			tmpDir := t.TempDir()
			regFile := filepath.Join(tmpDir, "projects.json")
			reg := registry.NewRegistry(regFile)

			// Act: init
			if err := reg.Init(); err != nil {
				t.Fatalf("Init: %v", err)
			}

			// Act: add project
			if err := reg.AddProject(tc.projectName, tc.directory, tc.target); err != nil {
				t.Fatalf("AddProject: %v", err)
			}

			// Assert: project is listed
			entries, err := reg.GetProjectEntries()
			if err != nil {
				t.Fatalf("GetProjectEntries: %v", err)
			}
			if len(entries) != 1 {
				t.Fatalf("expected 1 entry, got %d", len(entries))
			}
			if entries[0].Name != tc.projectName {
				t.Errorf("name = %q, want %q", entries[0].Name, tc.projectName)
			}

			// Act: touch project (update last used)
			if err := reg.TouchProject(tc.projectName); err != nil {
				t.Fatalf("TouchProject: %v", err)
			}

			// Act: set default flags
			if err := reg.SetDefaultFlags(tc.projectName, tc.flags); err != nil {
				t.Fatalf("SetDefaultFlags: %v", err)
			}

			// Assert: flags are stored
			entry, found, err := reg.GetProject(tc.projectName)
			if err != nil {
				t.Fatalf("GetProject: %v", err)
			}
			if !found {
				t.Fatal("project not found after SetDefaultFlags")
			}
			for k, v := range tc.flags {
				if entry.DefaultFlags[k] != v {
					t.Errorf("DefaultFlags[%s] = %q, want %q", k, entry.DefaultFlags[k], v)
				}
			}

			// Act: update flags (set some, unset others)
			if err := reg.UpdateDefaultFlags(tc.projectName, tc.setFlags, tc.unsetFlags); err != nil {
				t.Fatalf("UpdateDefaultFlags: %v", err)
			}

			// Assert: flags updated correctly
			entry, _, err = reg.GetProject(tc.projectName)
			if err != nil {
				t.Fatalf("GetProject after update: %v", err)
			}
			for k, v := range tc.setFlags {
				if entry.DefaultFlags[k] != v {
					t.Errorf("after update: DefaultFlags[%s] = %q, want %q", k, entry.DefaultFlags[k], v)
				}
			}
			for _, k := range tc.unsetFlags {
				if _, ok := entry.DefaultFlags[k]; ok {
					t.Errorf("after update: DefaultFlags[%s] should be unset", k)
				}
			}

			// Act: session tracking - start
			sessionID, err := reg.StartSession(tc.projectName, tc.resumeID)
			if err != nil {
				t.Fatalf("StartSession: %v", err)
			}
			if sessionID != "1" {
				t.Errorf("sessionID = %q, want %q", sessionID, "1")
			}

			// Assert: session recorded
			entry, _, err = reg.GetProject(tc.projectName)
			if err != nil {
				t.Fatalf("GetProject after StartSession: %v", err)
			}
			if len(entry.Sessions) != 1 {
				t.Fatalf("sessions count = %d, want 1", len(entry.Sessions))
			}
			if entry.Sessions[0].Started == "" {
				t.Error("session Started is empty")
			}
			if tc.resumeID != "" && entry.Sessions[0].ResumeID != tc.resumeID {
				t.Errorf("resumeID = %q, want %q", entry.Sessions[0].ResumeID, tc.resumeID)
			}

			// Act: end session
			if err := reg.EndSession(tc.projectName, sessionID); err != nil {
				t.Fatalf("EndSession: %v", err)
			}

			// Assert: session ended
			entry, _, err = reg.GetProject(tc.projectName)
			if err != nil {
				t.Fatalf("GetProject after EndSession: %v", err)
			}
			if entry.Sessions[0].Ended == "" {
				t.Error("session Ended is empty after EndSession")
			}
			if entry.Sessions[0].Duration < 0 {
				t.Errorf("session Duration = %d, want >= 0", entry.Sessions[0].Duration)
			}

			// Act: remove project
			if err := reg.RemoveProject(tc.projectName); err != nil {
				t.Fatalf("RemoveProject: %v", err)
			}

			// Assert: project is gone
			has, err := reg.HasProject(tc.projectName)
			if err != nil {
				t.Fatalf("HasProject after remove: %v", err)
			}
			if has {
				t.Error("project still exists after removal")
			}

			// Assert: entry list is empty
			entries, err = reg.GetProjectEntries()
			if err != nil {
				t.Fatalf("GetProjectEntries after remove: %v", err)
			}
			if len(entries) != 0 {
				t.Errorf("expected 0 entries after remove, got %d", len(entries))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test 4: Docker command construction
// Config → BuildClaudeArgs + ComposeRunCommand → verify complete chain
// ---------------------------------------------------------------------------

func TestIntegration_DockerCommandConstruction(t *testing.T) {
	tests := []struct {
		name            string
		cfg             core.Config
		expectInArgs    []string // substrings expected in the joined command
		expectNotInArgs []string // substrings that must NOT appear
	}{
		{
			name: "basic project without flags",
			cfg: core.Config{
				ProjectName: "basic-app",
				ProjectDir:  "/home/user/project",
				Target:      "dev",
				ImagePrefix: "techdelight/claude-runner",
			},
			expectInArgs: []string{
				"docker", "compose", "run", "--rm",
				"--name", "claude-run-basic-app",
				"claude",
			},
			expectNotInArgs: []string{
				"--debug",
				"/var/run/docker.sock",
			},
		},
		{
			name: "project with dind flag",
			cfg: core.Config{
				ProjectName: "dind-app",
				ProjectDir:  "/home/user/dind",
				Target:      "dev",
				ImagePrefix: "techdelight/claude-runner",
				DinD:        true,
			},
			expectInArgs: []string{
				"docker", "compose", "run", "--rm",
				"--name", "claude-run-dind-app",
				"/var/run/docker.sock:/var/run/docker.sock",
				"claude",
			},
		},
		{
			name: "project with debug flag",
			cfg: core.Config{
				ProjectName: "debug-app",
				ProjectDir:  "/home/user/debug",
				Target:      "dev",
				ImagePrefix: "techdelight/claude-runner",
				Debug:       true,
			},
			expectInArgs: []string{
				"docker", "compose", "run", "--rm",
				"claude", "--debug",
			},
		},
		{
			name: "project with prompt (headless)",
			cfg: core.Config{
				ProjectName: "headless-app",
				ProjectDir:  "/home/user/headless",
				Target:      "dev",
				ImagePrefix: "techdelight/claude-runner",
				Prompt:      "Fix all bugs",
			},
			expectInArgs: []string{
				"claude", "--print", "--verbose", "-p", "Fix all bugs",
			},
		},
		{
			name: "project with resume",
			cfg: core.Config{
				ProjectName: "resume-app",
				ProjectDir:  "/home/user/resume",
				Target:      "godot",
				ImagePrefix: "techdelight/claude-runner",
				Resume:      "session-xyz",
			},
			expectInArgs: []string{
				"claude", "--resume", "session-xyz",
			},
		},
		{
			name: "project with debug and dind and prompt",
			cfg: core.Config{
				ProjectName: "combo-app",
				ProjectDir:  "/home/user/combo",
				Target:      "dev",
				ImagePrefix: "techdelight/claude-runner",
				Debug:       true,
				DinD:        true,
				Prompt:      "Run tests",
			},
			expectInArgs: []string{
				"/var/run/docker.sock:/var/run/docker.sock",
				"claude", "--debug", "--print", "--verbose", "-p", "Run tests",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			mock := executor.NewMockExecutor()
			composeFile := "/path/to/docker-compose.yml"
			d := docker.NewDocker(mock, composeFile)

			claudeArgs := core.BuildClaudeArgs(&tc.cfg)

			var extraArgs []string
			if tc.cfg.DinD {
				extraArgs = []string{"-v", "/var/run/docker.sock:/var/run/docker.sock"}
			}

			// Act
			cmd := d.ComposeRunCommand(tc.cfg.ContainerName(), claudeArgs, extraArgs)
			fullCmd := strings.Join(cmd, " ")

			// Assert: expected substrings present
			for _, expect := range tc.expectInArgs {
				if !strings.Contains(fullCmd, expect) {
					t.Errorf("command missing %q: %s", expect, fullCmd)
				}
			}

			// Assert: unexpected substrings absent
			for _, notExpect := range tc.expectNotInArgs {
				if strings.Contains(fullCmd, notExpect) {
					t.Errorf("command should not contain %q: %s", notExpect, fullCmd)
				}
			}

			// Assert: compose file is included
			if !strings.Contains(fullCmd, composeFile) {
				t.Errorf("command missing compose file %q: %s", composeFile, fullCmd)
			}

			// Assert: extraArgs appear before service name "claude"
			if tc.cfg.DinD {
				sockIdx := strings.Index(fullCmd, "/var/run/docker.sock")
				claudeServiceIdx := strings.LastIndex(fullCmd, "claude")
				if sockIdx > claudeServiceIdx {
					t.Errorf("extraArgs must appear before service name 'claude': %s", fullCmd)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test 5: Web API integration
// GET /api/projects, POST /api/projects/{name}/start,
// POST /api/projects/{name}/stop using httptest.NewServer
// ---------------------------------------------------------------------------

func TestIntegration_WebAPI(t *testing.T) {
	// Arrange: set up web server with registry and mock executor
	tmpDir := t.TempDir()
	regPath := filepath.Join(tmpDir, "projects.json")
	reg := registry.NewRegistry(regPath)
	if err := reg.Init(); err != nil {
		t.Fatalf("registry init: %v", err)
	}

	mock := executor.NewMockExecutor()
	d := docker.NewDocker(mock, filepath.Join(tmpDir, "docker-compose.yml"))
	cfg := &core.Config{
		ScriptDir:   tmpDir,
		DataDir:     tmpDir,
		ImagePrefix: "test-image",
		Target:      "dev",
	}

	ws := web.NewWebServerForTest(reg, d, mock, cfg)

	// Register test projects
	if err := reg.AddProject("alpha", "/path/alpha", "dev"); err != nil {
		t.Fatal(err)
	}
	if err := reg.AddProject("beta", "/path/beta", "godot"); err != nil {
		t.Fatal(err)
	}

	// Mock: alpha container is running
	mock.Results["docker"] = executor.MockResult{Output: "claude-run-alpha\n"}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects", ws.HandleListProjects)
	mux.HandleFunc("POST /api/projects/{name}/start", ws.HandleStartProject)
	mux.HandleFunc("POST /api/projects/{name}/stop", ws.HandleStopProject)

	server := httptest.NewServer(mux)
	defer server.Close()

	t.Run("GET /api/projects returns all projects with running status", func(t *testing.T) {
		// Act
		resp, err := http.Get(server.URL + "/api/projects")
		if err != nil {
			t.Fatalf("GET /api/projects: %v", err)
		}
		defer resp.Body.Close()

		// Assert
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}

		var projects []map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&projects); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(projects) != 2 {
			t.Fatalf("got %d projects, want 2", len(projects))
		}

		// Projects should be sorted by name
		if projects[0]["name"] != "alpha" {
			t.Errorf("projects[0].name = %q, want %q", projects[0]["name"], "alpha")
		}
		if projects[0]["running"] != true {
			t.Errorf("alpha should be running")
		}
		if projects[1]["name"] != "beta" {
			t.Errorf("projects[1].name = %q, want %q", projects[1]["name"], "beta")
		}
		if projects[1]["running"] != false {
			t.Errorf("beta should not be running")
		}
	})

	t.Run("POST /api/projects/unknown/start returns 404", func(t *testing.T) {
		// Act
		resp, err := http.Post(server.URL+"/api/projects/unknown/start", "", nil)
		if err != nil {
			t.Fatalf("POST start unknown: %v", err)
		}
		defer resp.Body.Close()

		// Assert
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
		}
	})

	t.Run("POST /api/projects/unknown/stop returns 404", func(t *testing.T) {
		// Act
		resp, err := http.Post(server.URL+"/api/projects/unknown/stop", "", nil)
		if err != nil {
			t.Fatalf("POST stop unknown: %v", err)
		}
		defer resp.Body.Close()

		// Assert
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
		}
	})

	t.Run("POST /api/projects/alpha/stop returns 200 for running container", func(t *testing.T) {
		// Act
		resp, err := http.Post(server.URL+"/api/projects/alpha/stop", "", nil)
		if err != nil {
			t.Fatalf("POST stop alpha: %v", err)
		}
		defer resp.Body.Close()

		// Assert
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}

		var result map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if result["status"] != "stopped" {
			t.Errorf("status = %q, want %q", result["status"], "stopped")
		}
	})
}

// ---------------------------------------------------------------------------
// Test 6: Headless mode detection
// Verify IsHeadless correctly detects -p flag and that headless mode skips tmux
// ---------------------------------------------------------------------------

func TestIntegration_HeadlessModeDetection(t *testing.T) {
	tests := []struct {
		name          string
		prompt        string
		noTmux        bool
		expectUseTmux bool
	}{
		{
			name:          "no prompt, no flags - tmux enabled",
			prompt:        "",
			noTmux:        false,
			expectUseTmux: true,
		},
		{
			name:          "with prompt - headless skips tmux",
			prompt:        "Fix all errors",
			noTmux:        false,
			expectUseTmux: false,
		},
		{
			name:          "no-tmux flag disables tmux",
			prompt:        "",
			noTmux:        true,
			expectUseTmux: false,
		},
		{
			name:          "prompt and no-tmux - both disable tmux",
			prompt:        "Run tests",
			noTmux:        true,
			expectUseTmux: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			cfg := &core.Config{
				Prompt: tc.prompt,
				NoTmux: tc.noTmux,
			}

			// Act
			headless := config.IsHeadless(cfg)
			useTmux := cfg.UseTmux()

			// Assert: prompt always triggers headless
			// Note: IsHeadless also checks stdin (piped in test runners),
			// so we only assert the prompt-based path deterministically.
			if tc.prompt != "" && !headless {
				t.Errorf("IsHeadless() = false, want true (prompt is set)")
			}

			// Assert: UseTmux respects both prompt and no-tmux
			if useTmux != tc.expectUseTmux {
				t.Errorf("UseTmux() = %v, want %v", useTmux, tc.expectUseTmux)
			}
		})
	}
}

// TestIntegration_HeadlessModeDetection_ParseArgsIntegration verifies that
// parsing -p flag correctly flows through to headless detection and tmux skip.
func TestIntegration_HeadlessModeDetection_ParseArgsIntegration(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		expectPrompt  string
		expectUseTmux bool
	}{
		{
			name:          "parse -p flag flows to headless",
			args:          []string{"-p", "Fix linting", "my-project"},
			expectPrompt:  "Fix linting",
			expectUseTmux: false,
		},
		{
			name:          "parse --no-tmux flag disables tmux",
			args:          []string{"--no-tmux", "my-project"},
			expectPrompt:  "",
			expectUseTmux: false,
		},
		{
			name:          "no flags keeps tmux enabled",
			args:          []string{"my-project"},
			expectPrompt:  "",
			expectUseTmux: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange & Act
			cfg, err := config.ParseArgs(tc.args)
			if err != nil {
				t.Fatalf("ParseArgs: %v", err)
			}

			// Assert
			if cfg.Prompt != tc.expectPrompt {
				t.Errorf("Prompt = %q, want %q", cfg.Prompt, tc.expectPrompt)
			}
			if cfg.UseTmux() != tc.expectUseTmux {
				t.Errorf("UseTmux() = %v, want %v", cfg.UseTmux(), tc.expectUseTmux)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test 7: Shell completions
// Generate completions for all 3 shells and verify they contain all
// subcommands and flags.
// ---------------------------------------------------------------------------

func TestIntegration_ShellCompletions(t *testing.T) {
	subcommands := []string{"list", "prune", "remove", "config", "tui", "web", "completion"}
	flags := []string{"build", "target", "no-tmux", "debug", "dind", "no-color", "force"}

	tests := []struct {
		shell         string
		shellSpecific []string // additional shell-specific assertions
	}{
		{
			shell:         "bash",
			shellSpecific: []string{"_daedalus", "complete -F"},
		},
		{
			shell:         "zsh",
			shellSpecific: []string{"#compdef daedalus", "_daedalus"},
		},
		{
			shell:         "fish",
			shellSpecific: []string{"complete -c daedalus"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.shell, func(t *testing.T) {
			// Arrange
			cfg := &core.Config{CompletionShell: tc.shell}

			old := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			// Act
			err := completions.Generate(cfg)

			w.Close()
			var buf [16384]byte
			n, _ := r.Read(buf[:])
			os.Stdout = old

			if err != nil {
				t.Fatalf("Generate(%s): %v", tc.shell, err)
			}

			output := string(buf[:n])

			// Assert: all subcommands present
			for _, sub := range subcommands {
				if !strings.Contains(output, sub) {
					t.Errorf("%s completion missing subcommand %q", tc.shell, sub)
				}
			}

			// Assert: all flags present
			for _, flag := range flags {
				if !strings.Contains(output, flag) {
					t.Errorf("%s completion missing flag %q", tc.shell, flag)
				}
			}

			// Assert: shell-specific content
			for _, specific := range tc.shellSpecific {
				if !strings.Contains(output, specific) {
					t.Errorf("%s completion missing shell-specific content %q", tc.shell, specific)
				}
			}
		})
	}
}

// TestIntegration_ShellCompletions_InvalidShell verifies that requesting
// completion for an unsupported shell returns an error.
func TestIntegration_ShellCompletions_InvalidShell(t *testing.T) {
	tests := []struct {
		name  string
		shell string
	}{
		{"empty shell", ""},
		{"powershell", "powershell"},
		{"unknown shell", "csh"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			cfg := &core.Config{CompletionShell: tc.shell}

			// Act
			err := completions.Generate(cfg)

			// Assert
			if err == nil {
				t.Fatalf("expected error for shell %q, got nil", tc.shell)
			}
		})
	}
}
