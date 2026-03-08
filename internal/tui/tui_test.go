// Copyright (C) 2026 Techdelight BV

package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/docker"
	"github.com/techdelight/daedalus/internal/executor"
	"github.com/techdelight/daedalus/internal/registry"

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

	reg := registry.NewRegistry(regPath)
	mock := executor.NewMockExecutor()
	mock.Results["docker"] = executor.MockResult{Output: "claude-run-alpha\n"}
	d := docker.NewDocker(mock, "/dev/null")

	cmd := loadProjects(reg, d)
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

	if loaded.projects[0].name != "alpha" {
		t.Errorf("first project = %q, want %q", loaded.projects[0].name, "alpha")
	}
	if !loaded.projects[0].running {
		t.Error("alpha should be running")
	}
	if loaded.projects[1].name != "beta" {
		t.Errorf("second project = %q, want %q", loaded.projects[1].name, "beta")
	}
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

	reg := registry.NewRegistry(regPath)
	mock := executor.NewMockExecutor()
	mock.Results["docker"] = executor.MockResult{Err: fmt.Errorf("Cannot connect to Docker daemon")}
	d := docker.NewDocker(mock, "/dev/null")

	cmd := loadProjects(reg, d)
	msg := cmd()

	loaded, ok := msg.(projectsLoadedMsg)
	if !ok {
		t.Fatalf("expected projectsLoadedMsg, got %T", msg)
	}
	if loaded.err != nil {
		t.Fatalf("unexpected fatal error: %v", loaded.err)
	}
	if loaded.dockerErr == nil {
		t.Fatal("expected dockerErr to be set")
	}
	if len(loaded.projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(loaded.projects))
	}
	if loaded.projects[0].running {
		t.Error("project should show as not running when Docker is unreachable")
	}
}

func TestLoadProjects_RegistryError(t *testing.T) {
	reg := registry.NewRegistry("/nonexistent/path/projects.json")
	mock := executor.NewMockExecutor()
	d := docker.NewDocker(mock, "/dev/null")

	cmd := loadProjects(reg, d)
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
		cursor: 2,
	}

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
		cursor: 0,
	}

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

	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = newM.(tuiModel)
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = newM.(tuiModel)

	if m.cursor != 2 {
		t.Errorf("cursor = %d, want 2 after two downs", m.cursor)
	}

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
	mock := executor.NewMockExecutor()

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
	mock := executor.NewMockExecutor()
	mock.Results["docker"] = executor.MockResult{Err: fmt.Errorf("no such container")}

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
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg, got %T", msg)
	}
}

func TestAttachSession_ReturnsRequestAttach(t *testing.T) {
	mock := executor.NewMockExecutor()
	mock.Results["tmux"] = executor.MockResult{}

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
	mock := executor.NewMockExecutor()
	mock.Results["tmux"] = executor.MockResult{Err: fmt.Errorf("no session")}

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

func TestHandleTUIResult_PendingAttach(t *testing.T) {
	m := tuiModel{pendingAttach: "claude-my-app"}
	got := handleTUIResult(m)
	if got != "claude-my-app" {
		t.Errorf("handleTUIResult() = %q, want %q", got, "claude-my-app")
	}
}

func TestHandleTUIResult_NormalQuit(t *testing.T) {
	m := tuiModel{}
	got := handleTUIResult(m)
	if got != "" {
		t.Errorf("handleTUIResult() = %q, want empty", got)
	}
}

func TestHandleTUIResult_InvalidModel(t *testing.T) {
	// A non-tuiModel value should be treated as normal quit.
	got := handleTUIResult(nil)
	if got != "" {
		t.Errorf("handleTUIResult(nil) = %q, want empty", got)
	}
}

func TestKillKey_NotRunning(t *testing.T) {
	m := tuiModel{
		projects: []projectRow{{name: "stopped-app", running: false}},
		cursor:   0,
	}

	newM, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDelete})
	updated := newM.(tuiModel)
	if cmd != nil {
		t.Error("expected nil command for kill on stopped project")
	}
	if updated.statusMsg != "stopped-app is not running" {
		t.Errorf("statusMsg = %q, want %q", updated.statusMsg, "stopped-app is not running")
	}
}

func TestF2Key_EntersRenameMode(t *testing.T) {
	m := tuiModel{
		projects: []projectRow{{name: "my-app", running: false}},
		cursor:   0,
	}

	newM, cmd := m.Update(tea.KeyMsg{Type: tea.KeyF2})
	updated := newM.(tuiModel)
	if !updated.renaming {
		t.Error("renaming = false, want true after F2")
	}
	if updated.renameInput != "" {
		t.Errorf("renameInput = %q, want empty", updated.renameInput)
	}
	if cmd != nil {
		t.Error("expected nil command on F2")
	}
}

func TestRenameMode_EscCancels(t *testing.T) {
	m := tuiModel{
		projects:    []projectRow{{name: "my-app", running: false}},
		cursor:      0,
		renaming:    true,
		renameInput: "new-name",
	}

	newM, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated := newM.(tuiModel)
	if updated.renaming {
		t.Error("renaming = true, want false after Esc")
	}
	if updated.renameInput != "" {
		t.Errorf("renameInput = %q, want empty after Esc", updated.renameInput)
	}
	if cmd != nil {
		t.Error("expected nil command on Esc")
	}
}

func TestRenameMode_EnterOnEmpty(t *testing.T) {
	m := tuiModel{
		projects:    []projectRow{{name: "my-app", running: false}},
		cursor:      0,
		renaming:    true,
		renameInput: "",
	}

	newM, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := newM.(tuiModel)
	// Should stay in rename mode and do nothing
	if cmd != nil {
		t.Error("expected nil command for Enter on empty input")
	}
	_ = updated
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

func TestView_TitleShowsVersion(t *testing.T) {
	old := core.Version
	defer func() { core.Version = old }()
	core.Version = "1.2.3"

	m := tuiModel{}
	view := stripAnsi(m.View())
	if !containsString(view, "Daedalus [1.2.3]") {
		t.Errorf("expected title 'Daedalus [1.2.3]' in view, got:\n%s", view)
	}
}

func TestWindowSizeMsg_UpdatesTermHeight(t *testing.T) {
	m := tuiModel{
		projects: []projectRow{{name: "a"}},
	}

	newM, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	updated := newM.(tuiModel)
	if updated.termHeight != 30 {
		t.Errorf("termHeight = %d, want 30", updated.termHeight)
	}
}

func TestScrollOffset_AdjustsOnCursorDown(t *testing.T) {
	projects := make([]projectRow, 20)
	for i := range projects {
		projects[i] = projectRow{name: fmt.Sprintf("proj-%d", i)}
	}
	m := tuiModel{
		projects:   projects,
		cursor:     0,
		termHeight: 12, // visibleRows = 12 - 7 = 5
	}

	// Move cursor down past visible area
	for i := 0; i < 6; i++ {
		newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		m = newM.(tuiModel)
	}

	if m.cursor != 6 {
		t.Errorf("cursor = %d, want 6", m.cursor)
	}
	// cursor 6 with 5 visible rows: scrollOffset should be 2 (6 - 5 + 1)
	if m.scrollOffset != 2 {
		t.Errorf("scrollOffset = %d, want 2", m.scrollOffset)
	}
}

func TestScrollOffset_AdjustsOnCursorUp(t *testing.T) {
	projects := make([]projectRow, 20)
	for i := range projects {
		projects[i] = projectRow{name: fmt.Sprintf("proj-%d", i)}
	}
	m := tuiModel{
		projects:     projects,
		cursor:       5,
		scrollOffset: 5,
		termHeight:   12, // visibleRows = 5
	}

	// Move cursor up past scroll offset
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = newM.(tuiModel)

	if m.cursor != 4 {
		t.Errorf("cursor = %d, want 4", m.cursor)
	}
	if m.scrollOffset != 4 {
		t.Errorf("scrollOffset = %d, want 4", m.scrollOffset)
	}
}

func TestView_ShowsScrollbar_WhenProjectsExceedViewport(t *testing.T) {
	projects := make([]projectRow, 20)
	for i := range projects {
		projects[i] = projectRow{
			name:   fmt.Sprintf("proj-%d", i),
			target: "dev",
		}
	}
	m := tuiModel{
		projects:   projects,
		cursor:     0,
		termHeight: 12, // visibleRows = 5, fewer than 20 projects
	}

	view := m.View()
	// Scrollbar should contain thumb (█) and track (░) characters
	if !containsString(view, "\u2588") {
		t.Error("expected scrollbar thumb (█) in view when projects exceed viewport")
	}
	if !containsString(view, "\u2591") {
		t.Error("expected scrollbar track (░) in view when projects exceed viewport")
	}
}

func TestView_NoScrollbar_WhenAllProjectsFit(t *testing.T) {
	m := tuiModel{
		projects: []projectRow{
			{name: "app1", target: "dev"},
			{name: "app2", target: "dev"},
		},
		cursor:     0,
		termHeight: 30, // visibleRows = 23, far more than 2 projects
	}

	view := m.View()
	if containsString(view, "\u2588") || containsString(view, "\u2591") {
		t.Error("expected no scrollbar characters when all projects fit in viewport")
	}
}

func TestVisibleRows_ClampedToProjectCount(t *testing.T) {
	m := tuiModel{
		projects:   []projectRow{{name: "a"}, {name: "b"}},
		termHeight: 50, // capacity = 43, but only 2 projects
	}
	if m.visibleRows() != 2 {
		t.Errorf("visibleRows() = %d, want 2", m.visibleRows())
	}
}

func TestVisibleRows_SmallTerminal(t *testing.T) {
	m := tuiModel{
		projects:   make([]projectRow, 20),
		termHeight: 5, // below chromeLines, should return 1
	}
	if m.visibleRows() != 1 {
		t.Errorf("visibleRows() = %d, want 1", m.visibleRows())
	}
}

func TestNKey_EntersCreateMode(t *testing.T) {
	m := tuiModel{
		projects: []projectRow{{name: "app"}},
		cursor:   0,
	}

	newM, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	updated := newM.(tuiModel)
	if !updated.creating {
		t.Error("creating = false, want true after n")
	}
	if updated.createStep != 0 {
		t.Errorf("createStep = %d, want 0", updated.createStep)
	}
	if updated.createName != "" {
		t.Errorf("createName = %q, want empty", updated.createName)
	}
	if cmd != nil {
		t.Error("expected nil command on n")
	}
}

func TestCreateMode_NameValidation_RejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "projects.json")
	reg := registry.NewRegistry(regPath)
	reg.Init()

	m := tuiModel{
		creating:   true,
		createStep: 0,
		createName: "!invalid",
		registry:   reg,
	}

	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := newM.(tuiModel)
	if updated.creating {
		t.Error("creating should be false after invalid name")
	}
	if !containsString(updated.statusMsg, "Error") {
		t.Errorf("statusMsg = %q, want error message", updated.statusMsg)
	}
}

func TestCreateMode_NameValidation_RejectsDuplicate(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "projects.json")
	reg := registry.NewRegistry(regPath)
	reg.Init()
	reg.AddProject("existing", "/tmp", "dev")

	m := tuiModel{
		creating:   true,
		createStep: 0,
		createName: "existing",
		registry:   reg,
	}

	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := newM.(tuiModel)
	if updated.creating {
		t.Error("creating should be false after duplicate name")
	}
	if !containsString(updated.statusMsg, "already exists") {
		t.Errorf("statusMsg = %q, want 'already exists' message", updated.statusMsg)
	}
}

func TestCreateMode_EscCancels_Step0(t *testing.T) {
	m := tuiModel{
		creating:   true,
		createStep: 0,
		createName: "partial",
	}

	newM, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated := newM.(tuiModel)
	if updated.creating {
		t.Error("creating = true, want false after Esc")
	}
	if updated.createName != "" {
		t.Errorf("createName = %q, want empty after Esc", updated.createName)
	}
	if cmd != nil {
		t.Error("expected nil command on Esc")
	}
}

func TestCreateMode_EscCancels_Step1(t *testing.T) {
	m := tuiModel{
		creating:   true,
		createStep: 1,
		createName: "my-app",
		createDir:  "/tmp",
	}

	newM, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated := newM.(tuiModel)
	if updated.creating {
		t.Error("creating = true, want false after Esc in step 1")
	}
	if cmd != nil {
		t.Error("expected nil command on Esc")
	}
}

func TestListDirs_ReturnsSortedSubdirs(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "charlie"), 0755)
	os.MkdirAll(filepath.Join(dir, "alpha"), 0755)
	os.MkdirAll(filepath.Join(dir, "bravo"), 0755)
	// Create a file — should not appear in listing
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hi"), 0644)
	// Create a hidden dir — should not appear
	os.MkdirAll(filepath.Join(dir, ".hidden"), 0755)

	items := listDirs(dir)

	expected := []string{"..", "alpha", "bravo", "charlie"}
	if len(items) != len(expected) {
		t.Fatalf("listDirs returned %d items, want %d: %v", len(items), len(expected), items)
	}
	for i, want := range expected {
		if items[i] != want {
			t.Errorf("items[%d] = %q, want %q", i, items[i], want)
		}
	}
}

func TestAddProject_RegistersProject(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "projects.json")
	reg := registry.NewRegistry(regPath)
	reg.Init()

	cmd := addProject(reg, "new-app", "/tmp/new-app")
	msg := cmd()

	result, ok := msg.(actionResultMsg)
	if !ok {
		t.Fatalf("expected actionResultMsg, got %T", msg)
	}
	if result.err != nil {
		t.Fatalf("unexpected error: %v", result.err)
	}
	if result.msg != "Created project new-app" {
		t.Errorf("msg = %q, want %q", result.msg, "Created project new-app")
	}

	exists, err := reg.HasProject("new-app")
	if err != nil {
		t.Fatalf("HasProject error: %v", err)
	}
	if !exists {
		t.Error("project should exist in registry after addProject")
	}
}

func TestView_CreateMode_Step0(t *testing.T) {
	m := tuiModel{
		creating:   true,
		createStep: 0,
		createName: "my-app",
	}
	view := stripAnsi(m.View())
	if !containsString(view, "New project name: my-app") {
		t.Errorf("expected name prompt in view, got:\n%s", view)
	}
	if !containsString(view, "enter to continue") {
		t.Errorf("expected help text in view, got:\n%s", view)
	}
}

func TestView_CreateMode_Step1(t *testing.T) {
	m := tuiModel{
		creating:       true,
		createStep:     1,
		createName:     "my-app",
		createDir:      "/home/user/projects",
		createDirItems: []string{"..", "subfolder-a", "subfolder-b"},
		createDirIdx:   1,
	}
	view := stripAnsi(m.View())
	if !containsString(view, "New project: my-app") {
		t.Errorf("expected project name in view, got:\n%s", view)
	}
	if !containsString(view, "Select directory: /home/user/projects") {
		t.Errorf("expected directory path in view, got:\n%s", view)
	}
	if !containsString(view, "> subfolder-a") {
		t.Errorf("expected cursor on subfolder-a, got:\n%s", view)
	}
	if !containsString(view, "s=select") {
		t.Errorf("expected help text in view, got:\n%s", view)
	}
}

func stripAnsi(s string) string {
	result := make([]byte, 0, len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' {
			// Skip until we find a letter that terminates the sequence
			i++
			for i < len(s) && !((s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= 'a' && s[i] <= 'z')) {
				i++
			}
			if i < len(s) {
				i++ // skip the terminating letter
			}
		} else {
			result = append(result, s[i])
			i++
		}
	}
	return string(result)
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
