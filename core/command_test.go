// Copyright (C) 2026 Techdelight BV

package core

import (
	"strings"
	"testing"
)

func TestBuildClaudeArgs_NoFlags(t *testing.T) {
	cfg := &Config{}
	args := BuildClaudeArgs(cfg)
	if len(args) != 0 {
		t.Errorf("args = %v, want empty slice", args)
	}
}

func TestBuildClaudeArgs_WithDebug(t *testing.T) {
	cfg := &Config{Debug: true}
	args := BuildClaudeArgs(cfg)
	if len(args) != 1 || args[0] != "--debug" {
		t.Errorf("args = %v, want [--debug]", args)
	}
}

func TestBuildClaudeArgs_WithResume(t *testing.T) {
	cfg := &Config{Resume: "abc123"}
	args := BuildClaudeArgs(cfg)
	expected := []string{"--resume", "abc123"}
	if len(args) != len(expected) {
		t.Fatalf("len = %d, want %d", len(args), len(expected))
	}
	for i, a := range expected {
		if args[i] != a {
			t.Errorf("args[%d] = %q, want %q", i, args[i], a)
		}
	}
}

func TestBuildClaudeArgs_WithPrompt(t *testing.T) {
	cfg := &Config{Prompt: "fix bugs"}
	args := BuildClaudeArgs(cfg)
	expected := []string{"--print", "--verbose", "-p", "fix bugs"}
	if len(args) != len(expected) {
		t.Fatalf("len = %d, want %d", len(args), len(expected))
	}
	for i, a := range expected {
		if args[i] != a {
			t.Errorf("args[%d] = %q, want %q", i, args[i], a)
		}
	}
}

func TestBuildRunnerArgs_Copilot_NoFlags(t *testing.T) {
	cfg := &Config{Runner: "copilot"}
	args := BuildRunnerArgs(cfg)
	if len(args) != 0 {
		t.Errorf("args = %v, want empty slice", args)
	}
}

func TestBuildRunnerArgs_Copilot_WithPrompt(t *testing.T) {
	cfg := &Config{Runner: "copilot", Prompt: "fix bugs"}
	args := BuildRunnerArgs(cfg)
	// copilot has no prompt prefix, just -p
	expected := []string{"-p", "fix bugs"}
	if len(args) != len(expected) {
		t.Fatalf("len = %d, want %d; args = %v", len(args), len(expected), args)
	}
	for i, a := range expected {
		if args[i] != a {
			t.Errorf("args[%d] = %q, want %q", i, args[i], a)
		}
	}
}

func TestBuildRunnerArgs_Copilot_DebugIgnored(t *testing.T) {
	cfg := &Config{Runner: "copilot", Debug: true}
	args := BuildRunnerArgs(cfg)
	// copilot has no debug flag, so nothing emitted
	if len(args) != 0 {
		t.Errorf("args = %v, want empty (copilot has no debug flag)", args)
	}
}

func TestBuildRunnerArgs_Copilot_WithResume(t *testing.T) {
	cfg := &Config{Runner: "copilot", Resume: "sess-42"}
	args := BuildRunnerArgs(cfg)
	expected := []string{"--resume", "sess-42"}
	if len(args) != len(expected) {
		t.Fatalf("len = %d, want %d", len(args), len(expected))
	}
	for i, a := range expected {
		if args[i] != a {
			t.Errorf("args[%d] = %q, want %q", i, args[i], a)
		}
	}
}

func TestBuildRunnerArgs_Claude_DefaultBehavior(t *testing.T) {
	// No Runner field set — should behave exactly like original BuildClaudeArgs
	cfg := &Config{Debug: true, Prompt: "fix bugs"}
	args := BuildRunnerArgs(cfg)
	expected := []string{"--debug", "--print", "--verbose", "-p", "fix bugs"}
	if len(args) != len(expected) {
		t.Fatalf("len = %d, want %d; args = %v", len(args), len(expected), args)
	}
	for i, a := range expected {
		if args[i] != a {
			t.Errorf("args[%d] = %q, want %q", i, args[i], a)
		}
	}
}

func TestBuildExtraArgs_AlwaysMountsSkills(t *testing.T) {
	cfg := &Config{DataDir: "/data/daedalus"}
	args := BuildExtraArgs(cfg, nil, nil)
	if len(args) < 2 {
		t.Fatalf("args = %v, want at least 2 elements for skills mount", args)
	}
	if args[0] != "-v" {
		t.Errorf("args[0] = %q, want %q", args[0], "-v")
	}
	want := "/data/daedalus/skills:/opt/skills"
	if args[1] != want {
		t.Errorf("args[1] = %q, want %q", args[1], want)
	}
}

func TestBuildExtraArgs_MountsDaedalusDir(t *testing.T) {
	// Arrange
	cfg := &Config{DataDir: "/data", ProjectDir: "/home/user/myproject"}

	// Act
	args := BuildExtraArgs(cfg, nil, nil)

	// Assert — .daedalus mount should be at index 2-3 (after skills mount)
	if len(args) < 4 {
		t.Fatalf("args = %v, want at least 4 elements", args)
	}
	if args[2] != "-v" {
		t.Errorf("args[2] = %q, want %q", args[2], "-v")
	}
	want := "/home/user/myproject/.daedalus:/workspace/.daedalus"
	if args[3] != want {
		t.Errorf("args[3] = %q, want %q", args[3], want)
	}
}

func TestBuildExtraArgs_WithDinD(t *testing.T) {
	cfg := &Config{DataDir: "/data", DinD: true}
	base := len(RunnerVolumeArgs(cfg))
	args := BuildExtraArgs(cfg, nil, nil)
	// base runner volume mounts + DinD mount (2)
	if len(args) != base+2 {
		t.Fatalf("args = %v, want %d elements", args, base+2)
	}
	if args[base] != "-v" || args[base+1] != "/var/run/docker.sock:/var/run/docker.sock" {
		t.Errorf("DinD mount not found, got: %v", args[base:])
	}
}

func TestBuildExtraArgs_WithOverlay_ClaudeMd(t *testing.T) {
	cfg := &Config{DataDir: "/data"}
	base := len(RunnerVolumeArgs(cfg))
	overlay := &OverlayPaths{ClaudeMdPath: "/tmp/overlay/CLAUDE.md"}
	args := BuildExtraArgs(cfg, nil, overlay)
	if len(args) != base+2 {
		t.Fatalf("args = %v, want %d elements", args, base+2)
	}
	if args[base] != "-v" {
		t.Errorf("args[%d] = %q, want %q", base, args[base], "-v")
	}
	want := "/tmp/overlay/CLAUDE.md:/workspace/.claude/CLAUDE.md:ro"
	if args[base+1] != want {
		t.Errorf("args[%d] = %q, want %q", base+1, args[base+1], want)
	}
}

func TestBuildExtraArgs_WithOverlay_Settings(t *testing.T) {
	cfg := &Config{DataDir: "/data"}
	base := len(RunnerVolumeArgs(cfg))
	overlay := &OverlayPaths{SettingsPath: "/tmp/overlay/settings.json"}
	args := BuildExtraArgs(cfg, nil, overlay)
	if len(args) != base+2 {
		t.Fatalf("args = %v, want %d elements", args, base+2)
	}
	want := "/tmp/overlay/settings.json:/workspace/.claude/settings.json:ro"
	if args[base+1] != want {
		t.Errorf("args[%d] = %q, want %q", base+1, args[base+1], want)
	}
}

func TestBuildExtraArgs_WithOverlay_Env(t *testing.T) {
	cfg := &Config{DataDir: "/data"}
	base := len(RunnerVolumeArgs(cfg))
	overlay := &OverlayPaths{Env: map[string]string{"FOO": "bar"}}
	args := BuildExtraArgs(cfg, nil, overlay)
	if len(args) != base+2 {
		t.Fatalf("args = %v, want %d elements", args, base+2)
	}
	if args[base] != "-e" {
		t.Errorf("args[%d] = %q, want %q", base, args[base], "-e")
	}
	if args[base+1] != "FOO=bar" {
		t.Errorf("args[%d] = %q, want %q", base+1, args[base+1], "FOO=bar")
	}
}

func TestBuildExtraArgs_WithOverlay_Full(t *testing.T) {
	cfg := &Config{DataDir: "/data"}
	base := len(RunnerVolumeArgs(cfg))
	overlay := &OverlayPaths{
		ClaudeMdPath: "/tmp/CLAUDE.md",
		SettingsPath: "/tmp/settings.json",
		Env:          map[string]string{"KEY": "val"},
	}
	args := BuildExtraArgs(cfg, nil, overlay)
	// base + claudemd (2) + settings (2) + env (2)
	if len(args) != base+6 {
		t.Fatalf("args = %v, want %d elements", args, base+6)
	}
}

func TestBuildExtraArgs_NilOverlay(t *testing.T) {
	cfg := &Config{DataDir: "/data"}
	args := BuildExtraArgs(cfg, nil, nil)
	// no extras → exactly the base runner volume mounts
	if len(args) != len(RunnerVolumeArgs(cfg)) {
		t.Fatalf("args = %v, want %d elements", args, len(RunnerVolumeArgs(cfg)))
	}
}

func TestRunnerVolumeArgs_MountsSkillsDaedalusSharedTools(t *testing.T) {
	cfg := &Config{DataDir: "/data", ProjectName: "proj", ProjectDir: "/home/user/proj"}
	joined := strings.Join(RunnerVolumeArgs(cfg), " ")
	wants := []string{
		"/data/skills:/opt/skills",
		"/home/user/proj/.daedalus:/workspace/.daedalus",
		"/data/shared/claude-versions:/home/claude/.local/share/claude/versions",
		"/data/shared/m2:/home/claude/.m2",
		"/data/tools/proj:/opt/tools",
	}
	for _, w := range wants {
		if !strings.Contains(joined, w) {
			t.Errorf("RunnerVolumeArgs missing %q; got %v", w, RunnerVolumeArgs(cfg))
		}
	}
}

func TestRunnerVolumeHostDirs_CoverAllMounts(t *testing.T) {
	cfg := &Config{DataDir: "/data", ProjectName: "proj", ProjectDir: "/home/user/proj"}
	dirs := RunnerVolumeHostDirs(cfg)
	// one host dir per bind mount
	if got, want := len(dirs), len(RunnerVolumeArgs(cfg))/2; got != want {
		t.Errorf("host dirs = %d, want %d (one per mount)", got, want)
	}
}
