// Copyright (C) 2026 Techdelight BV

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/techdelight/daedalus/core"

	tea "github.com/charmbracelet/bubbletea"
)

func TestRelativeTime_JustNow(t *testing.T) {
	ts := time.Now().UTC().Add(-30 * time.Second).Format("2006-01-02T15:04:05Z")
	got := core.RelativeTime(ts)
	if got != "just now" {
		t.Errorf("RelativeTime(%q) = %q, want %q", ts, got, "just now")
	}
}

func TestRelativeTime_Minutes(t *testing.T) {
	ts := time.Now().UTC().Add(-5 * time.Minute).Format("2006-01-02T15:04:05Z")
	got := core.RelativeTime(ts)
	if got != "5 min ago" {
		t.Errorf("RelativeTime(%q) = %q, want %q", ts, got, "5 min ago")
	}
}

func TestRelativeTime_OneMinute(t *testing.T) {
	ts := time.Now().UTC().Add(-90 * time.Second).Format("2006-01-02T15:04:05Z")
	got := core.RelativeTime(ts)
	if got != "1 min ago" {
		t.Errorf("RelativeTime(%q) = %q, want %q", ts, got, "1 min ago")
	}
}

func TestRelativeTime_Hours(t *testing.T) {
	ts := time.Now().UTC().Add(-3 * time.Hour).Format("2006-01-02T15:04:05Z")
	got := core.RelativeTime(ts)
	if got != "3 hours ago" {
		t.Errorf("RelativeTime(%q) = %q, want %q", ts, got, "3 hours ago")
	}
}

func TestRelativeTime_OneHour(t *testing.T) {
	ts := time.Now().UTC().Add(-90 * time.Minute).Format("2006-01-02T15:04:05Z")
	got := core.RelativeTime(ts)
	if got != "1 hour ago" {
		t.Errorf("RelativeTime(%q) = %q, want %q", ts, got, "1 hour ago")
	}
}

func TestRelativeTime_Days(t *testing.T) {
	ts := time.Now().UTC().Add(-48 * time.Hour).Format("2006-01-02T15:04:05Z")
	got := core.RelativeTime(ts)
	if got != "2 days ago" {
		t.Errorf("RelativeTime(%q) = %q, want %q", ts, got, "2 days ago")
	}
}

func TestRelativeTime_OneDay(t *testing.T) {
	ts := time.Now().UTC().Add(-36 * time.Hour).Format("2006-01-02T15:04:05Z")
	got := core.RelativeTime(ts)
	if got != "1 day ago" {
		t.Errorf("RelativeTime(%q) = %q, want %q", ts, got, "1 day ago")
	}
}

func TestRelativeTime_InvalidFormat(t *testing.T) {
	got := core.RelativeTime("not-a-date")
	if got != "not-a-date" {
		t.Errorf("RelativeTime(invalid) = %q, want %q", got, "not-a-date")
	}
}

func TestLoadProjects_ReturnsRows(t *testing.T) {
	// Set up a temp registry with two projects
	dir := t.TempDir()
	regPath := filepath.Join(dir, "projects.json")

	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	data := core.RegistryData{
		Version: 1,
		Projects: map[string]core.ProjectEntry{
			"alpha": {Directory: "/tmp/alpha", Target: "dev", Created: now, LastUsed: now},
			"beta":  {Directory: "/tmp/beta", Target: "godot", Created: now, LastUsed: now},
		},
	}
	b, _ := json.Marshal(data)
	os.WriteFile(regPath, b, 0644)

	reg := NewRegistry(regPath)
	mock := NewMockExecutor()
	// docker ps returns "claude-run-alpha" so alpha is running
	mock.Results["docker"] = MockResult{Output: "claude-run-alpha\n"}
	docker := NewDocker(mock, "/dev/null")

	cmd := loadProjects(reg, docker)
	msg := cmd()

	loaded, ok := msg.(projectsLoadedMsg)
	if !ok {
		t.Fatalf("expected projectsLoadedMsg, got %T", msg)
	}
	if loaded.err != nil {
		t.Fatalf("unexpected error: %v", loaded.err)
	}
	if len(loaded.projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(loaded.projects))
	}

	// Projects are sorted by name (via GetProjectEntries)
	if loaded.projects[0].name != "alpha" {
		t.Errorf("first project = %q, want %q", loaded.projects[0].name, "alpha")
	}
	if !loaded.projects[0].running {
		t.Error("alpha should be running")
	}
	if loaded.projects[1].name != "beta" {
		t.Errorf("second project = %q, want %q", loaded.projects[1].name, "beta")
	}
	// beta: mock returns "claude-run-alpha" which doesn't match "claude-run-beta"
	if loaded.projects[1].running {
		t.Error("beta should not be running")
	}
}

func TestLoadProjects_DockerError_SurfacesWarning(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "projects.json")

	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	data := core.RegistryData{
		Version: 1,
		Projects: map[string]core.ProjectEntry{
			"alpha": {Directory: "/tmp/alpha", Target: "dev", Created: now, LastUsed: now},
		},
	}
	b, _ := json.Marshal(data)
	os.WriteFile(regPath, b, 0644)

	reg := NewRegistry(regPath)
	mock := NewMockExecutor()
	// Docker fails
	mock.Results["docker"] = MockResult{Err: fmt.Errorf("Cannot connect to Docker daemon")}
	docker := NewDocker(mock, "/dev/null")

	cmd := loadProjects(reg, docker)
	msg := cmd()

	loaded, ok := msg.(projectsLoadedMsg)
	if !ok {
		t.Fatalf("expected projectsLoadedMsg, got %T", msg)
	}
	// Fatal error should be nil — projects are still returned
	if loaded.err != nil {
		t.Fatalf("unexpected fatal error: %v", loaded.err)
	}
	// Docker error should be surfaced
	if loaded.dockerErr == nil {
		t.Fatal("expected dockerErr to be set")
	}
	// Projects should still be listed (just with running=false)
	if len(loaded.projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(loaded.projects))
	}
	if loaded.projects[0].running {
		t.Error("project should show as not running when Docker is unreachable")
	}
}

func TestLoadProjects_RegistryError(t *testing.T) {
	reg := NewRegistry("/nonexistent/path/projects.json")
	mock := NewMockExecutor()
	docker := NewDocker(mock, "/dev/null")

	cmd := loadProjects(reg, docker)
	msg := cmd()

	loaded, ok := msg.(projectsLoadedMsg)
	if !ok {
		t.Fatalf("expected projectsLoadedMsg, got %T", msg)
	}
	if loaded.err == nil {
		t.Fatal("expected error for missing registry")
	}
}

func TestCursorBounds_Down(t *testing.T) {
	m := tuiModel{
		projects: []projectRow{
			{name: "a"}, {name: "b"}, {name: "c"},
		},
		cursor: 2, // at last item
	}

	// Press down — cursor should not go past last item
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	updated := newM.(tuiModel)
	if updated.cursor != 2 {
		t.Errorf("cursor = %d, want 2 (should not exceed last index)", updated.cursor)
	}
}

func TestCursorBounds_Up(t *testing.T) {
	m := tuiModel{
		projects: []projectRow{
			{name: "a"}, {name: "b"}, {name: "c"},
		},
		cursor: 0, // at first item
	}

	// Press up — cursor should not go below 0
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	updated := newM.(tuiModel)
	if updated.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (should not go below 0)", updated.cursor)
	}
}

func TestCursorBounds_Navigate(t *testing.T) {
	m := tuiModel{
		projects: []projectRow{
			{name: "a"}, {name: "b"}, {name: "c"},
		},
		cursor: 0,
	}

	// Move down twice
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = newM.(tuiModel)
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = newM.(tuiModel)

	if m.cursor != 2 {
		t.Errorf("cursor = %d, want 2 after two downs", m.cursor)
	}

	// Move up once
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = newM.(tuiModel)
	if m.cursor != 1 {
		t.Errorf("cursor = %d, want 1 after one up", m.cursor)
	}
}

func TestCursorClamp_OnProjectsLoaded(t *testing.T) {
	m := tuiModel{
		projects: []projectRow{
			{name: "a"}, {name: "b"}, {name: "c"},
		},
		cursor: 2,
	}

	// Simulate projects shrinking to 1 item
	msg := projectsLoadedMsg{
		projects: []projectRow{{name: "a"}},
	}
	newM, _ := m.Update(msg)
	updated := newM.(tuiModel)
	if updated.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (clamped to new list length)", updated.cursor)
	}
}

func TestKillContainer_CallsDockerStop(t *testing.T) {
	mock := NewMockExecutor()

	cmd := killContainer(mock, "my-app")
	msg := cmd()

	result, ok := msg.(actionResultMsg)
	if !ok {
		t.Fatalf("expected actionResultMsg, got %T", msg)
	}
	if result.err != nil {
		t.Fatalf("unexpected error: %v", result.err)
	}
	if result.msg != "Stopped my-app" {
		t.Errorf("msg = %q, want %q", result.msg, "Stopped my-app")
	}

	call := mock.FindCall("docker")
	if call == nil {
		t.Fatal("expected docker call")
	}
	if len(call.Args) < 2 || call.Args[0] != "stop" || call.Args[1] != "claude-run-my-app" {
		t.Errorf("docker args = %v, want [stop claude-run-my-app]", call.Args)
	}
}

func TestKillContainer_Error(t *testing.T) {
	mock := NewMockExecutor()
	mock.Results["docker"] = MockResult{Err: fmt.Errorf("no such container")}

	cmd := killContainer(mock, "ghost")
	msg := cmd()

	result, ok := msg.(actionResultMsg)
	if !ok {
		t.Fatalf("expected actionResultMsg, got %T", msg)
	}
	if result.err == nil {
		t.Fatal("expected error")
	}
}

func TestQuitKey(t *testing.T) {
	m := tuiModel{
		projects: []projectRow{{name: "a"}},
	}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("expected quit command, got nil")
	}
	// Execute the command — tea.Quit returns a QuitMsg
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg, got %T", msg)
	}
}

func TestAttachSession_ReturnsRequestAttach(t *testing.T) {
	mock := NewMockExecutor()
	// tmux has-session succeeds — session exists
	mock.Results["tmux"] = MockResult{}

	cmd := attachToSession(mock, "my-app")
	msg := cmd()

	attach, ok := msg.(requestAttachMsg)
	if !ok {
		t.Fatalf("expected requestAttachMsg, got %T", msg)
	}
	if attach.sessionName != "claude-my-app" {
		t.Errorf("sessionName = %q, want %q", attach.sessionName, "claude-my-app")
	}
}

func TestAttachSession_NoSession(t *testing.T) {
	mock := NewMockExecutor()
	// tmux has-session fails — session doesn't exist
	mock.Results["tmux"] = MockResult{Err: fmt.Errorf("no session")}

	cmd := attachToSession(mock, "ghost")
	msg := cmd()

	result, ok := msg.(actionResultMsg)
	if !ok {
		t.Fatalf("expected actionResultMsg, got %T", msg)
	}
	if result.msg != "No tmux session for ghost" {
		t.Errorf("msg = %q, want %q", result.msg, "No tmux session for ghost")
	}
}

func TestRequestAttachMsg_QuitsWithPendingAttach(t *testing.T) {
	m := tuiModel{
		projects: []projectRow{{name: "a"}},
	}

	newM, cmd := m.Update(requestAttachMsg{sessionName: "claude-a"})
	updated := newM.(tuiModel)

	if updated.pendingAttach != "claude-a" {
		t.Errorf("pendingAttach = %q, want %q", updated.pendingAttach, "claude-a")
	}
	if cmd == nil {
		t.Fatal("expected quit command, got nil")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg, got %T", msg)
	}
}

func TestKillKey_NotRunning(t *testing.T) {
	m := tuiModel{
		projects: []projectRow{{name: "stopped-app", running: false}},
		cursor:   0,
	}

	newM, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("K")})
	updated := newM.(tuiModel)
	if cmd != nil {
		t.Error("expected nil command for kill on stopped project")
	}
	if updated.statusMsg != "stopped-app is not running" {
		t.Errorf("statusMsg = %q, want %q", updated.statusMsg, "stopped-app is not running")
	}
}

func TestView_EmptyProjects(t *testing.T) {
	m := tuiModel{}
	view := m.View()
	if !containsString(view, "No registered projects") {
		t.Error("expected 'No registered projects' in view for empty model")
	}
}

func TestView_WithProjects(t *testing.T) {
	m := tuiModel{
		projects: []projectRow{
			{name: "app", target: "dev", running: true, lastUsed: time.Now().UTC().Format("2006-01-02T15:04:05Z")},
		},
		cursor: 0,
	}
	view := m.View()
	if !containsString(view, "app") {
		t.Error("expected project name 'app' in view")
	}
	if !containsString(view, "running") {
		t.Error("expected 'running' status in view")
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
