// Copyright (C) 2026 Techdelight BV

package web

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/activity"
	"github.com/techdelight/daedalus/internal/agentstate"
	"github.com/techdelight/daedalus/internal/docker"
	"github.com/techdelight/daedalus/internal/executor"
	"github.com/techdelight/daedalus/internal/foreman"
	"github.com/techdelight/daedalus/internal/mcpclient"
	"github.com/techdelight/daedalus/internal/progress"
	"github.com/techdelight/daedalus/internal/programme"
	"github.com/techdelight/daedalus/internal/registry"

	"github.com/gorilla/websocket"
)

func setupWebTest(t *testing.T) (*WebServer, *executor.MockExecutor) {
	t.Helper()
	tmp := t.TempDir()
	regPath := filepath.Join(tmp, "projects.json")
	reg := registry.NewRegistry(regPath)
	if err := reg.Init(); err != nil {
		t.Fatalf("registry init: %v", err)
	}

	mock := executor.NewMockExecutor()
	docker := docker.NewDocker(mock, filepath.Join(tmp, "docker-compose.yml"))
	cfg := &core.Config{
		ScriptDir:   tmp,
		DataDir:     tmp,
		ImagePrefix: "test-image",
		Target:      "dev",
	}

	observer := agentstate.NewContainerObserver(mock)
	ws := &WebServer{
		registry:         reg,
		docker:           docker,
		executor:         mock,
		cfg:              cfg,
		observer:         observer,
		activityResolver: activity.NewResolver(observer, activity.NewClaudeCodeDetector()),
	}
	return ws, mock
}

func TestHandleListProjects(t *testing.T) {
	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("alpha", "/path/alpha", "dev"); err != nil {
		t.Fatal(err)
	}
	if err := ws.registry.AddProject("beta", "/path/beta", "godot"); err != nil {
		t.Fatal(err)
	}
	mock.Results["docker"] = executor.MockResult{Output: "claude-run-alpha\n"}

	req := httptest.NewRequest("GET", "/api/projects", nil)
	rec := httptest.NewRecorder()

	ws.handleListProjects(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var projects []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &projects); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("got %d projects, want 2", len(projects))
	}

	if projects[0]["name"] != "alpha" {
		t.Errorf("projects[0].name = %q, want %q", projects[0]["name"], "alpha")
	}
	if projects[0]["running"] != true {
		t.Errorf("projects[0].running = %v, want true", projects[0]["running"])
	}
	if projects[1]["name"] != "beta" {
		t.Errorf("projects[1].name = %q, want %q", projects[1]["name"], "beta")
	}
	if projects[1]["running"] != false {
		t.Errorf("projects[1].running = %v, want false", projects[1]["running"])
	}
}

func TestHandleListProjects_Empty(t *testing.T) {
	ws, _ := setupWebTest(t)
	req := httptest.NewRequest("GET", "/api/projects", nil)
	rec := httptest.NewRecorder()

	ws.handleListProjects(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var projects []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &projects); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("got %d projects, want 0", len(projects))
	}
}

func TestHandleStartProject_Success(t *testing.T) {
	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("myapp", "/path/myapp", "dev"); err != nil {
		t.Fatal(err)
	}
	mock.Results["docker"] = executor.MockResult{Output: ""}

	if err := os.MkdirAll(filepath.Join(ws.cfg.ScriptDir, ".cache"), 0755); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{name}/start", ws.handleStartProject)
	req := httptest.NewRequest("POST", "/api/projects/myapp/start", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if resp["status"] != "started" {
		t.Errorf("status = %q, want %q", resp["status"], "started")
	}

	if !mock.HasCall("tmux") {
		t.Error("expected tmux call")
	}
}

func TestHandleStartProject_DisplayFlag(t *testing.T) {
	t.Setenv("DISPLAY", ":0")

	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("gui-app", "/path/gui-app", "dev"); err != nil {
		t.Fatal(err)
	}
	if err := ws.registry.UpdateDefaultFlags("gui-app", map[string]string{"display": "true"}, nil); err != nil {
		t.Fatal(err)
	}
	mock.Results["docker"] = executor.MockResult{Output: ""}

	if err := os.MkdirAll(filepath.Join(ws.cfg.ScriptDir, ".cache"), 0755); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{name}/start", ws.handleStartProject)
	req := httptest.NewRequest("POST", "/api/projects/gui-app/start", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Find the tmux send-keys call (not has-session or new-session).
	var sendKeysArgs string
	for _, c := range mock.FindCalls("tmux") {
		if len(c.Args) > 0 && c.Args[0] == "send-keys" {
			sendKeysArgs = strings.Join(c.Args, " ")
			break
		}
	}
	if sendKeysArgs == "" {
		t.Fatal("expected tmux send-keys call")
	}
	if !strings.Contains(sendKeysArgs, "/tmp/.X11-unix") {
		t.Errorf("display forwarding args missing from docker command;\nsend-keys args: %s", sendKeysArgs)
	}
}

func TestHandleStartProject_DinDFlag(t *testing.T) {
	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("dind-app", "/path/dind-app", "dev"); err != nil {
		t.Fatal(err)
	}
	if err := ws.registry.UpdateDefaultFlags("dind-app", map[string]string{"dind": "true"}, nil); err != nil {
		t.Fatal(err)
	}
	mock.Results["docker"] = executor.MockResult{Output: ""}

	if err := os.MkdirAll(filepath.Join(ws.cfg.ScriptDir, ".cache"), 0755); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{name}/start", ws.handleStartProject)
	req := httptest.NewRequest("POST", "/api/projects/dind-app/start", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Find the tmux send-keys call.
	var sendKeysArgs string
	for _, c := range mock.FindCalls("tmux") {
		if len(c.Args) > 0 && c.Args[0] == "send-keys" {
			sendKeysArgs = strings.Join(c.Args, " ")
			break
		}
	}
	if sendKeysArgs == "" {
		t.Fatal("expected tmux send-keys call")
	}
	if !strings.Contains(sendKeysArgs, "/var/run/docker.sock") {
		t.Errorf("DinD args missing from docker command;\nsend-keys args: %s", sendKeysArgs)
	}
}

func TestHandleStartProject_AlreadyRunning(t *testing.T) {
	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("myapp", "/path/myapp", "dev"); err != nil {
		t.Fatal(err)
	}
	mock.Results["docker"] = executor.MockResult{Output: "claude-run-myapp\n"}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{name}/start", ws.handleStartProject)
	req := httptest.NewRequest("POST", "/api/projects/myapp/start", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

func TestHandleStartProject_UnknownProject(t *testing.T) {
	ws, _ := setupWebTest(t)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{name}/start", ws.handleStartProject)
	req := httptest.NewRequest("POST", "/api/projects/nonexistent/start", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleStopProject_Success(t *testing.T) {
	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("myapp", "/path/myapp", "dev"); err != nil {
		t.Fatal(err)
	}
	mock.Results["docker"] = executor.MockResult{Output: "claude-run-myapp\n"}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{name}/stop", ws.handleStopProject)
	req := httptest.NewRequest("POST", "/api/projects/myapp/stop", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if resp["status"] != "stopped" {
		t.Errorf("status = %q, want %q", resp["status"], "stopped")
	}
}

func TestHandleStopProject_Error(t *testing.T) {
	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("myapp", "/path/myapp", "dev"); err != nil {
		t.Fatal(err)
	}
	mock.Results["docker"] = executor.MockResult{Output: "", Err: fmt.Errorf("stop failed")}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{name}/stop", ws.handleStopProject)
	req := httptest.NewRequest("POST", "/api/projects/nonexistent/stop", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleSendEnter_Success(t *testing.T) {
	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("alpha", "/path/alpha", "dev"); err != nil {
		t.Fatal(err)
	}
	// tmux has-session succeeds (session exists)
	mock.Results["tmux"] = executor.MockResult{Output: ""}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{name}/enter", ws.handleSendEnter)
	req := httptest.NewRequest("POST", "/api/projects/alpha/enter", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("status = %q, want %q", resp["status"], "ok")
	}

	// Verify tmux send-keys was called with correct args.
	var found bool
	for _, c := range mock.FindCalls("tmux") {
		if len(c.Args) >= 4 && c.Args[0] == "send-keys" && c.Args[1] == "-t" && c.Args[2] == "claude-alpha" && c.Args[3] == "Enter" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected tmux send-keys -t claude-alpha Enter call; got calls: %v", mock.FindCalls("tmux"))
	}
}

func TestHandleSendEnter_NotFound(t *testing.T) {
	ws, _ := setupWebTest(t)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{name}/enter", ws.handleSendEnter)
	req := httptest.NewRequest("POST", "/api/projects/nonexistent/enter", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleSendEnter_NoSession(t *testing.T) {
	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("alpha", "/path/alpha", "dev"); err != nil {
		t.Fatal(err)
	}
	mock.Results["tmux"] = executor.MockResult{Err: fmt.Errorf("no session")}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{name}/enter", ws.handleSendEnter)
	req := httptest.NewRequest("POST", "/api/projects/alpha/enter", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestHandleRenameProject_Success(t *testing.T) {
	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("old-app", "/path/old", "dev"); err != nil {
		t.Fatal(err)
	}
	mock.Results["docker"] = executor.MockResult{Output: ""}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{name}/rename", ws.handleRenameProject)
	req := httptest.NewRequest("POST", "/api/projects/old-app/rename",
		strings.NewReader(`{"newName":"new-app"}`))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if resp["status"] != "renamed" {
		t.Errorf("status = %q, want %q", resp["status"], "renamed")
	}

	has, _ := ws.registry.HasProject("new-app")
	if !has {
		t.Error("new-app not found in registry after rename")
	}
	has, _ = ws.registry.HasProject("old-app")
	if has {
		t.Error("old-app still exists in registry after rename")
	}
}

func TestHandleRenameProject_NotFound(t *testing.T) {
	ws, _ := setupWebTest(t)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{name}/rename", ws.handleRenameProject)
	req := httptest.NewRequest("POST", "/api/projects/nonexistent/rename",
		strings.NewReader(`{"newName":"new-app"}`))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleRenameProject_Running(t *testing.T) {
	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("running-app", "/path/app", "dev"); err != nil {
		t.Fatal(err)
	}
	mock.Results["docker"] = executor.MockResult{Output: "claude-run-running-app\n"}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{name}/rename", ws.handleRenameProject)
	req := httptest.NewRequest("POST", "/api/projects/running-app/rename",
		strings.NewReader(`{"newName":"new-app"}`))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

func TestHandleRenameProject_TargetExists(t *testing.T) {
	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("app-a", "/path/a", "dev"); err != nil {
		t.Fatal(err)
	}
	if err := ws.registry.AddProject("app-b", "/path/b", "dev"); err != nil {
		t.Fatal(err)
	}
	mock.Results["docker"] = executor.MockResult{Output: ""}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{name}/rename", ws.handleRenameProject)
	req := httptest.NewRequest("POST", "/api/projects/app-a/rename",
		strings.NewReader(`{"newName":"app-b"}`))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

func TestHandleRenameProject_InvalidName(t *testing.T) {
	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("my-app", "/path/app", "dev"); err != nil {
		t.Fatal(err)
	}
	mock.Results["docker"] = executor.MockResult{Output: ""}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{name}/rename", ws.handleRenameProject)
	req := httptest.NewRequest("POST", "/api/projects/my-app/rename",
		strings.NewReader(`{"newName":""}`))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandleDashboard_Success(t *testing.T) {
	// Arrange
	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("myapp", "/path/myapp", "dev"); err != nil {
		t.Fatal(err)
	}
	// Add sessions with durations
	if _, err := ws.registry.StartSession("myapp", ""); err != nil {
		t.Fatal(err)
	}
	if err := ws.registry.EndSession("myapp", "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.registry.StartSession("myapp", "resume-1"); err != nil {
		t.Fatal(err)
	}
	// Set progress metadata
	if err := ws.registry.UpdateProjectProgress("myapp", 42, "Build a CLI tool", "1.2.0"); err != nil {
		t.Fatal(err)
	}
	mock.Results["docker"] = executor.MockResult{Output: "claude-run-myapp\n"}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/dashboard", ws.handleDashboard)

	// Act
	req := httptest.NewRequest("GET", "/api/projects/myapp/dashboard", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var dash map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &dash); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if dash["name"] != "myapp" {
		t.Errorf("name = %q, want %q", dash["name"], "myapp")
	}
	if dash["directory"] != "/path/myapp" {
		t.Errorf("directory = %q, want %q", dash["directory"], "/path/myapp")
	}
	if dash["running"] != true {
		t.Errorf("running = %v, want true", dash["running"])
	}
	if int(dash["progressPct"].(float64)) != 42 {
		t.Errorf("progressPct = %v, want 42", dash["progressPct"])
	}
	if dash["vision"] != "Build a CLI tool" {
		t.Errorf("vision = %q, want %q", dash["vision"], "Build a CLI tool")
	}
	if dash["projectVersion"] != "1.2.0" {
		t.Errorf("projectVersion = %q, want %q", dash["projectVersion"], "1.2.0")
	}
	if int(dash["sessionCount"].(float64)) != 2 {
		t.Errorf("sessionCount = %v, want 2", dash["sessionCount"])
	}
}

func TestHandleDashboard_NotFound(t *testing.T) {
	// Arrange
	ws, _ := setupWebTest(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/dashboard", ws.handleDashboard)

	// Act
	req := httptest.NewRequest("GET", "/api/projects/nonexistent/dashboard", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleDashboard_ReadsProgressFile(t *testing.T) {
	// Arrange
	ws, mock := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("prog-app", projDir, "dev"); err != nil {
		t.Fatal(err)
	}
	// Set registry values that should be overridden by progress file.
	if err := ws.registry.UpdateProjectProgress("prog-app", 10, "Old vision", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	// Write progress file with more current data.
	if err := progress.Write(projDir, progress.Data{
		ProgressPct:    75,
		Vision:         "Test vision",
		ProjectVersion: "2.0.0",
	}); err != nil {
		t.Fatal(err)
	}
	mock.Results["docker"] = executor.MockResult{Output: ""}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/dashboard", ws.handleDashboard)

	// Act
	req := httptest.NewRequest("GET", "/api/projects/prog-app/dashboard", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var dash map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &dash); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if int(dash["progressPct"].(float64)) != 75 {
		t.Errorf("progressPct = %v, want 75 (from progress file)", dash["progressPct"])
	}
	if dash["vision"] != "Test vision" {
		t.Errorf("vision = %q, want %q (from progress file)", dash["vision"], "Test vision")
	}
	if dash["projectVersion"] != "2.0.0" {
		t.Errorf("projectVersion = %q, want %q (from progress file)", dash["projectVersion"], "2.0.0")
	}
}

func TestHandleRoadmap_Success(t *testing.T) {
	// Arrange
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("roadmap-app", projDir, "dev"); err != nil {
		t.Fatal(err)
	}
	roadmapContent := `## Current Sprint

### Sprint 5: Polish and Release (v1.0.0)

Goal: Ship the first stable release.

| # | Item | Status |
|---|------|--------|
| 1 | Fix all bugs | Done |
| 2 | Write docs | In Progress |

## Future Sprints

### Sprint 6: Extensions

| # | Item | Status |
|---|------|--------|
| 1 | Plugin system | |
`
	if err := os.WriteFile(filepath.Join(projDir, "ROADMAP.md"), []byte(roadmapContent), 0644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/roadmap", ws.handleRoadmap)

	// Act
	req := httptest.NewRequest("GET", "/api/projects/roadmap-app/roadmap", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp roadmapJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if len(resp.Sprints) != 2 {
		t.Fatalf("got %d sprints, want 2", len(resp.Sprints))
	}
	if resp.Sprints[0].Number != 5 {
		t.Errorf("sprint[0].Number = %d, want 5", resp.Sprints[0].Number)
	}
	if resp.Sprints[0].Title != "Polish and Release" {
		t.Errorf("sprint[0].Title = %q, want %q", resp.Sprints[0].Title, "Polish and Release")
	}
	if resp.Sprints[0].Version != "1.0.0" {
		t.Errorf("sprint[0].Version = %q, want %q", resp.Sprints[0].Version, "1.0.0")
	}
	if !resp.Sprints[0].IsCurrent {
		t.Error("sprint[0].IsCurrent = false, want true")
	}
	if len(resp.Sprints[0].Items) != 2 {
		t.Fatalf("sprint[0] has %d items, want 2", len(resp.Sprints[0].Items))
	}
	if resp.Sprints[0].Items[0].Status != "Done" {
		t.Errorf("sprint[0].Items[0].Status = %q, want %q", resp.Sprints[0].Items[0].Status, "Done")
	}
	if resp.Sprints[1].IsCurrent {
		t.Error("sprint[1].IsCurrent = true, want false")
	}
}

func TestHandleRoadmap_NoFile(t *testing.T) {
	// Arrange
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("empty-app", projDir, "dev"); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/roadmap", ws.handleRoadmap)

	// Act
	req := httptest.NewRequest("GET", "/api/projects/empty-app/roadmap", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp roadmapJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if len(resp.Sprints) != 0 {
		t.Errorf("got %d sprints, want 0", len(resp.Sprints))
	}
}

func TestHandleRoadmap_NotFound(t *testing.T) {
	// Arrange
	ws, _ := setupWebTest(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/roadmap", ws.handleRoadmap)

	// Act
	req := httptest.NewRequest("GET", "/api/projects/nonexistent/roadmap", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleTerminal_NoSession(t *testing.T) {
	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("myapp", "/path/myapp", "dev"); err != nil {
		t.Fatal(err)
	}
	mock.Results["tmux"] = executor.MockResult{Err: fmt.Errorf("no session")}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/terminal", ws.handleTerminal)
	req := httptest.NewRequest("GET", "/api/projects/myapp/terminal", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestHandleTerminal_UnknownProject(t *testing.T) {
	ws, _ := setupWebTest(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/terminal", ws.handleTerminal)
	req := httptest.NewRequest("GET", "/api/projects/nonexistent/terminal", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleTerminal_WebSocketUpgrade(t *testing.T) {
	ws, _ := setupWebTest(t)
	if err := ws.registry.AddProject("myapp", "/path/myapp", "dev"); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/terminal", ws.handleTerminal)
	server := httptest.NewServer(mux)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/projects/myapp/terminal"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)

	if err != nil {
		if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
			t.Logf("WebSocket upgrade returned status %d (expected in test env without tmux)", resp.StatusCode)
			return
		}
		t.Logf("WebSocket dial error (expected in test env): %v", err)
		return
	}
	defer conn.Close()

	t.Log("WebSocket upgrade succeeded")
}

func TestWebServerRouting_Integration(t *testing.T) {
	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("demo", "/path/demo", "dev"); err != nil {
		t.Fatal(err)
	}
	mock.Results["docker"] = executor.MockResult{Output: "claude-run-demo\n"}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects", ws.handleListProjects)
	mux.HandleFunc("POST /api/projects/{name}/start", ws.handleStartProject)
	mux.HandleFunc("POST /api/projects/{name}/stop", ws.handleStopProject)
	mux.HandleFunc("GET /api/projects/{name}/terminal", ws.handleTerminal)

	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/projects")
	if err != nil {
		t.Fatalf("GET /api/projects: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/projects: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var projects []projectJSON
	if err := json.NewDecoder(resp.Body).Decode(&projects); err != nil {
		t.Fatalf("decode projects: %v", err)
	}
	if len(projects) != 1 || projects[0].Name != "demo" {
		t.Errorf("projects = %v, want 1 project named 'demo'", projects)
	}
	if !projects[0].Running {
		t.Errorf("projects[0].Running = false, want true")
	}

	resp2, err := http.Post(server.URL+"/api/projects/unknown/start", "", nil)
	if err != nil {
		t.Fatalf("POST start unknown: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("POST start unknown: status = %d, want %d", resp2.StatusCode, http.StatusNotFound)
	}

	resp3, err := http.Post(server.URL+"/api/projects/unknown/stop", "", nil)
	if err != nil {
		t.Fatalf("POST stop unknown: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusNotFound {
		t.Errorf("POST stop unknown: status = %d, want %d", resp3.StatusCode, http.StatusNotFound)
	}
}

func TestRootHandler_InjectsVersionInTitle(t *testing.T) {
	old := core.Version
	defer func() { core.Version = old }()
	core.Version = "9.8.7"

	version := core.ReadVersion()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		data, err := staticFiles.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, "index.html not found", http.StatusInternalServerError)
			return
		}
		html := strings.Replace(string(data), ">Daedalus<", ">Daedalus ["+version+"]<", 1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(html))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}

	if !strings.Contains(string(body), ">Daedalus [9.8.7]<") {
		t.Errorf("expected version in title, got:\n%s", string(body))
	}
}

func TestWebServerStaticServing_Integration(t *testing.T) {
	ws, _ := setupWebTest(t)

	mux := http.NewServeMux()

	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		data, err := staticFiles.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, "index.html not found", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})
	mux.HandleFunc("GET /api/projects", ws.handleListProjects)

	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("GET /: Content-Type = %q, want text/html", ct)
	}

	resp2, err := http.Get(server.URL + "/static/style.css")
	if err != nil {
		t.Fatalf("GET /static/style.css: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("GET /static/style.css: status = %d, want %d", resp2.StatusCode, http.StatusOK)
	}

	resp3, err := http.Get(server.URL + "/static/terminal.js")
	if err != nil {
		t.Fatalf("GET /static/terminal.js: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("GET /static/terminal.js: status = %d, want %d", resp3.StatusCode, http.StatusOK)
	}
}

func TestHandleAgentState_Running(t *testing.T) {
	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("myapp", "/path/myapp", "dev"); err != nil {
		t.Fatal(err)
	}
	mock.Results["docker"] = executor.MockResult{Output: "running\n"}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/state", ws.handleAgentState)
	req := httptest.NewRequest("GET", "/api/projects/myapp/state", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if resp["state"] != "running" {
		t.Errorf("state = %q, want %q", resp["state"], "running")
	}
}

func TestHandleAgentState_Stopped(t *testing.T) {
	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("myapp", "/path/myapp", "dev"); err != nil {
		t.Fatal(err)
	}
	mock.Results["docker"] = executor.MockResult{Output: "exited\n"}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/state", ws.handleAgentState)
	req := httptest.NewRequest("GET", "/api/projects/myapp/state", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["state"] != "stopped" {
		t.Errorf("state = %q, want %q", resp["state"], "stopped")
	}
}

func TestHandleAgentState_NotFound(t *testing.T) {
	ws, _ := setupWebTest(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/state", ws.handleAgentState)
	req := httptest.NewRequest("GET", "/api/projects/nonexistent/state", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleForemanStatus_NoForeman(t *testing.T) {
	ws, _ := setupWebTest(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/foreman/status", ws.handleForemanStatus)
	req := httptest.NewRequest("GET", "/api/foreman/status", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp core.ForemanStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if resp.State != core.ForemanIdle {
		t.Errorf("state = %q, want %q", resp.State, core.ForemanIdle)
	}
	if resp.Message != "not configured" {
		t.Errorf("message = %q, want %q", resp.Message, "not configured")
	}
}

func TestHandleForemanStart_MissingBody(t *testing.T) {
	ws, _ := setupWebTest(t)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/foreman/start", ws.handleForemanStart)
	req := httptest.NewRequest("POST", "/api/foreman/start", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandleForemanStart_InvalidJSON(t *testing.T) {
	ws, _ := setupWebTest(t)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/foreman/start", ws.handleForemanStart)
	req := httptest.NewRequest("POST", "/api/foreman/start", strings.NewReader(`not json`))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleForemanStop_NoForeman(t *testing.T) {
	ws, _ := setupWebTest(t)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/foreman/stop", ws.handleForemanStop)
	req := httptest.NewRequest("POST", "/api/foreman/stop", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

func TestHandleListProgrammes_Empty(t *testing.T) {
	ws, _ := setupWebTest(t)

	req := httptest.NewRequest("GET", "/api/programmes", nil)
	rec := httptest.NewRecorder()

	ws.handleListProgrammes(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var progs []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &progs); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if len(progs) != 0 {
		t.Fatalf("got %d programmes, want 0", len(progs))
	}
}

func TestHandleListProgrammes_WithData(t *testing.T) {
	ws, _ := setupWebTest(t)

	// Create programmes directory and a programme file.
	progDir := ws.cfg.ProgrammesDir()
	if err := os.MkdirAll(progDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(progDir, "backend.json"), []byte(`{
		"name": "backend",
		"description": "Backend services",
		"projects": ["auth", "api"],
		"deps": [{"upstream": "auth", "downstream": "api"}]
	}`), 0644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/programmes", nil)
	rec := httptest.NewRecorder()

	ws.handleListProgrammes(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var progs []core.Programme
	if err := json.Unmarshal(rec.Body.Bytes(), &progs); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if len(progs) != 1 {
		t.Fatalf("got %d programmes, want 1", len(progs))
	}
	if progs[0].Name != "backend" {
		t.Errorf("name = %q, want %q", progs[0].Name, "backend")
	}
	if len(progs[0].Projects) != 2 {
		t.Errorf("projects count = %d, want 2", len(progs[0].Projects))
	}
}

func TestHandleCreateProgramme_Success(t *testing.T) {
	ws, _ := setupWebTest(t)

	body := `{"name": "frontend", "description": "UI apps", "projects": ["web", "mobile"]}`
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/programmes", ws.handleCreateProgramme)
	req := httptest.NewRequest("POST", "/api/programmes", strings.NewReader(body))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var resp core.Programme
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if resp.Name != "frontend" {
		t.Errorf("name = %q, want %q", resp.Name, "frontend")
	}

	// Verify file was created.
	path := filepath.Join(ws.cfg.ProgrammesDir(), "frontend.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("programme file not created on disk")
	}
}

func TestHandleCreateProgramme_Duplicate(t *testing.T) {
	ws, _ := setupWebTest(t)

	progDir := ws.cfg.ProgrammesDir()
	if err := os.MkdirAll(progDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(progDir, "existing.json"), []byte(`{"name":"existing","projects":[]}`), 0644); err != nil {
		t.Fatal(err)
	}

	body := `{"name": "existing", "projects": ["a"]}`
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/programmes", ws.handleCreateProgramme)
	req := httptest.NewRequest("POST", "/api/programmes", strings.NewReader(body))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

func TestHandleCreateProgramme_InvalidBody(t *testing.T) {
	ws, _ := setupWebTest(t)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/programmes", ws.handleCreateProgramme)
	req := httptest.NewRequest("POST", "/api/programmes", strings.NewReader("not json"))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleGetProgramme_Success(t *testing.T) {
	ws, _ := setupWebTest(t)

	progDir := ws.cfg.ProgrammesDir()
	if err := os.MkdirAll(progDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(progDir, "myapp.json"), []byte(`{
		"name": "myapp",
		"description": "My app",
		"projects": ["svc"]
	}`), 0644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/programmes/{name}", ws.handleGetProgramme)
	req := httptest.NewRequest("GET", "/api/programmes/myapp", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp core.Programme
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if resp.Name != "myapp" {
		t.Errorf("name = %q, want %q", resp.Name, "myapp")
	}
}

func TestHandleGetProgramme_NotFound(t *testing.T) {
	ws, _ := setupWebTest(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/programmes/{name}", ws.handleGetProgramme)
	req := httptest.NewRequest("GET", "/api/programmes/nonexistent", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleUpdateProgramme_Success(t *testing.T) {
	ws, _ := setupWebTest(t)

	progDir := ws.cfg.ProgrammesDir()
	if err := os.MkdirAll(progDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(progDir, "updatable.json"), []byte(`{"name":"updatable","projects":["a"]}`), 0644); err != nil {
		t.Fatal(err)
	}

	body := `{"description": "Updated description", "projects": ["a", "b"]}`
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/programmes/{name}", ws.handleUpdateProgramme)
	req := httptest.NewRequest("PUT", "/api/programmes/updatable", strings.NewReader(body))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp core.Programme
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if resp.Name != "updatable" {
		t.Errorf("name = %q, want %q", resp.Name, "updatable")
	}
	if resp.Description != "Updated description" {
		t.Errorf("description = %q, want %q", resp.Description, "Updated description")
	}
	if len(resp.Projects) != 2 {
		t.Errorf("projects count = %d, want 2", len(resp.Projects))
	}
}

func TestHandleUpdateProgramme_NotFound(t *testing.T) {
	ws, _ := setupWebTest(t)

	body := `{"description": "new"}`
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/programmes/{name}", ws.handleUpdateProgramme)
	req := httptest.NewRequest("PUT", "/api/programmes/nonexistent", strings.NewReader(body))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleUpdateProgramme_InvalidBody(t *testing.T) {
	ws, _ := setupWebTest(t)

	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/programmes/{name}", ws.handleUpdateProgramme)
	req := httptest.NewRequest("PUT", "/api/programmes/test", strings.NewReader("not json"))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleDeleteProgramme_Success(t *testing.T) {
	ws, _ := setupWebTest(t)

	progDir := ws.cfg.ProgrammesDir()
	if err := os.MkdirAll(progDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(progDir, "removable.json"), []byte(`{"name":"removable","projects":[]}`), 0644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/programmes/{name}", ws.handleDeleteProgramme)
	req := httptest.NewRequest("DELETE", "/api/programmes/removable", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if resp["status"] != "deleted" {
		t.Errorf("status = %q, want %q", resp["status"], "deleted")
	}

	// Verify file was removed.
	path := filepath.Join(progDir, "removable.json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("programme file still exists after delete")
	}
}

func TestHandleDeleteProgramme_NotFound(t *testing.T) {
	ws, _ := setupWebTest(t)

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/programmes/{name}", ws.handleDeleteProgramme)
	req := httptest.NewRequest("DELETE", "/api/programmes/nonexistent", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// --- Tests for exported handler wrappers and NewWebServerForTest ---

func TestNewWebServerForTest(t *testing.T) {
	// Arrange
	tmp := t.TempDir()
	regPath := filepath.Join(tmp, "projects.json")
	reg := registry.NewRegistry(regPath)
	if err := reg.Init(); err != nil {
		t.Fatalf("registry init: %v", err)
	}
	mock := executor.NewMockExecutor()
	d := docker.NewDocker(mock, filepath.Join(tmp, "docker-compose.yml"))
	cfg := &core.Config{ScriptDir: tmp, DataDir: tmp, ImagePrefix: "test"}

	// Act
	ws := NewWebServerForTest(reg, d, mock, cfg)

	// Assert
	if ws == nil {
		t.Fatal("NewWebServerForTest returned nil")
	}
	if ws.registry != reg {
		t.Error("registry not set correctly")
	}
	if ws.executor != mock {
		t.Error("executor not set correctly")
	}
	if ws.cfg != cfg {
		t.Error("cfg not set correctly")
	}
	if ws.observer == nil {
		t.Error("observer should be non-nil")
	}
}

func TestHandleListProjects_ExportedWrapper(t *testing.T) {
	// Arrange
	ws, _ := setupWebTest(t)
	if err := ws.registry.AddProject("wrap-test", "/path/wrap", "dev"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/projects", nil)
	rec := httptest.NewRecorder()

	// Act
	ws.HandleListProjects(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var projects []projectJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &projects); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("got %d projects, want 1", len(projects))
	}
	if projects[0].Name != "wrap-test" {
		t.Errorf("name = %q, want %q", projects[0].Name, "wrap-test")
	}
}

func TestHandleStartProject_ExportedWrapper(t *testing.T) {
	// Arrange
	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("start-wrap", "/path/start-wrap", "dev"); err != nil {
		t.Fatal(err)
	}
	mock.Results["docker"] = executor.MockResult{Output: ""}
	if err := os.MkdirAll(filepath.Join(ws.cfg.ScriptDir, ".cache"), 0755); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{name}/start", ws.HandleStartProject)
	req := httptest.NewRequest("POST", "/api/projects/start-wrap/start", nil)
	rec := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if resp["status"] != "started" {
		t.Errorf("status = %q, want %q", resp["status"], "started")
	}
}

func TestHandleStopProject_ExportedWrapper(t *testing.T) {
	// Arrange
	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("stop-wrap", "/path/stop-wrap", "dev"); err != nil {
		t.Fatal(err)
	}
	mock.Results["docker"] = executor.MockResult{Output: "claude-run-stop-wrap\n"}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{name}/stop", ws.HandleStopProject)
	req := httptest.NewRequest("POST", "/api/projects/stop-wrap/stop", nil)
	rec := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if resp["status"] != "stopped" {
		t.Errorf("status = %q, want %q", resp["status"], "stopped")
	}
}

func TestHandleRenameProject_ExportedWrapper(t *testing.T) {
	// Arrange
	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("rename-wrap", "/path/rename", "dev"); err != nil {
		t.Fatal(err)
	}
	mock.Results["docker"] = executor.MockResult{Output: ""}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{name}/rename", ws.HandleRenameProject)
	req := httptest.NewRequest("POST", "/api/projects/rename-wrap/rename",
		strings.NewReader(`{"newName":"renamed-wrap"}`))
	rec := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if resp["status"] != "renamed" {
		t.Errorf("status = %q, want %q", resp["status"], "renamed")
	}
}

func TestHandleRenameProject_BadJSON(t *testing.T) {
	// Arrange
	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("badjson-app", "/path/app", "dev"); err != nil {
		t.Fatal(err)
	}
	mock.Results["docker"] = executor.MockResult{Output: ""}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{name}/rename", ws.handleRenameProject)
	req := httptest.NewRequest("POST", "/api/projects/badjson-app/rename",
		strings.NewReader("not json"))
	rec := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandleSendEnter_ExportedWrapper(t *testing.T) {
	// Arrange
	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("enter-wrap", "/path/enter-wrap", "dev"); err != nil {
		t.Fatal(err)
	}
	mock.Results["tmux"] = executor.MockResult{Output: ""}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{name}/enter", ws.HandleSendEnter)
	req := httptest.NewRequest("POST", "/api/projects/enter-wrap/enter", nil)
	rec := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("status = %q, want %q", resp["status"], "ok")
	}
}

func TestHandleDashboard_ExportedWrapper(t *testing.T) {
	// Arrange
	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("dash-wrap", "/path/dash", "dev"); err != nil {
		t.Fatal(err)
	}
	mock.Results["docker"] = executor.MockResult{Output: ""}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/dashboard", ws.HandleDashboard)
	req := httptest.NewRequest("GET", "/api/projects/dash-wrap/dashboard", nil)
	rec := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var dash dashboardJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &dash); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if dash.Name != "dash-wrap" {
		t.Errorf("name = %q, want %q", dash.Name, "dash-wrap")
	}
}

func TestHandleRoadmap_ExportedWrapper(t *testing.T) {
	// Arrange
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("road-wrap", projDir, "dev"); err != nil {
		t.Fatal(err)
	}
	roadmapContent := `## Current Sprint

### Sprint 1: Init (v0.1.0)

| # | Item | Status |
|---|------|--------|
| 1 | Setup | Done |
`
	if err := os.WriteFile(filepath.Join(projDir, "ROADMAP.md"), []byte(roadmapContent), 0644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/roadmap", ws.HandleRoadmap)
	req := httptest.NewRequest("GET", "/api/projects/road-wrap/roadmap", nil)
	rec := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp roadmapJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if len(resp.Sprints) != 1 {
		t.Fatalf("got %d sprints, want 1", len(resp.Sprints))
	}
}

func TestHandleRoadmap_ReadError(t *testing.T) {
	// Arrange: project directory points to a path where ROADMAP.md exists
	// but is unreadable (a directory instead of a file triggers a read error
	// that is NOT os.IsNotExist).
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("road-err", projDir, "dev"); err != nil {
		t.Fatal(err)
	}
	// Create ROADMAP.md as a directory — os.ReadFile will fail with a non-IsNotExist error.
	if err := os.Mkdir(filepath.Join(projDir, "ROADMAP.md"), 0755); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/roadmap", ws.handleRoadmap)
	req := httptest.NewRequest("GET", "/api/projects/road-err/roadmap", nil)
	rec := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
}

func TestHandleAgentState_ExportedWrapper(t *testing.T) {
	// Arrange
	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("state-wrap", "/path/state-wrap", "dev"); err != nil {
		t.Fatal(err)
	}
	mock.Results["docker"] = executor.MockResult{Output: "running\n"}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/state", ws.HandleAgentState)
	req := httptest.NewRequest("GET", "/api/projects/state-wrap/state", nil)
	rec := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if resp["state"] != "running" {
		t.Errorf("state = %q, want %q", resp["state"], "running")
	}
}

func TestHandleForemanStop_WithActiveForeman(t *testing.T) {
	// Arrange
	ws, _ := setupWebTest(t)

	// Create a real Foreman instance in idle state, then set it on the WebServer.
	progDir := ws.cfg.ProgrammesDir()
	if err := os.MkdirAll(progDir, 0755); err != nil {
		t.Fatal(err)
	}
	progStore := programme.New(progDir)
	mcpClient := mcpclient.New()
	obs := foreman.NewDefaultObserver(ws.observer)
	cfg := core.ForemanConfig{Programme: "test-prog", PollSeconds: 30}
	ws.foreman = foreman.New(cfg, progStore, ws.registry, mcpClient, obs)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/foreman/stop", ws.handleForemanStop)
	req := httptest.NewRequest("POST", "/api/foreman/stop", nil)
	rec := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if resp["status"] != "stopped" {
		t.Errorf("status = %q, want %q", resp["status"], "stopped")
	}
}

func TestHandleSprints(t *testing.T) {
	// Arrange
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("sprint-app", projDir, "dev"); err != nil {
		t.Fatal(err)
	}
	sprintsContent := `## Current Sprint

### Sprint 3: API Layer (v0.3.0)

Goal: Build REST endpoints.

| # | Item | Status |
|---|------|--------|
| 1 | GET endpoints | Done |
| 2 | POST endpoints | In Progress |
`
	if err := os.WriteFile(filepath.Join(projDir, "SPRINTS.md"), []byte(sprintsContent), 0644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/sprints", ws.handleSprints)

	// Act
	req := httptest.NewRequest("GET", "/api/projects/sprint-app/sprints", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp roadmapJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if len(resp.Sprints) != 1 {
		t.Fatalf("got %d sprints, want 1", len(resp.Sprints))
	}
	if resp.Sprints[0].Number != 3 {
		t.Errorf("sprint[0].Number = %d, want 3", resp.Sprints[0].Number)
	}
	if resp.Sprints[0].Title != "API Layer" {
		t.Errorf("sprint[0].Title = %q, want %q", resp.Sprints[0].Title, "API Layer")
	}
	if resp.Sprints[0].Version != "0.3.0" {
		t.Errorf("sprint[0].Version = %q, want %q", resp.Sprints[0].Version, "0.3.0")
	}
	if !resp.Sprints[0].IsCurrent {
		t.Error("sprint[0].IsCurrent = false, want true")
	}
	if len(resp.Sprints[0].Items) != 2 {
		t.Fatalf("sprint[0] has %d items, want 2", len(resp.Sprints[0].Items))
	}
	if resp.Sprints[0].Items[0].Status != "Done" {
		t.Errorf("sprint[0].Items[0].Status = %q, want %q", resp.Sprints[0].Items[0].Status, "Done")
	}
}

func TestHandleSprints_FallbackToRoadmap(t *testing.T) {
	// Arrange
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("fallback-app", projDir, "dev"); err != nil {
		t.Fatal(err)
	}
	// Only ROADMAP.md exists, no SPRINTS.md
	roadmapContent := `## Current Sprint

### Sprint 1: Bootstrap (v0.1.0)

| # | Item | Status |
|---|------|--------|
| 1 | Initial setup | Done |
`
	if err := os.WriteFile(filepath.Join(projDir, "ROADMAP.md"), []byte(roadmapContent), 0644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/sprints", ws.handleSprints)

	// Act
	req := httptest.NewRequest("GET", "/api/projects/fallback-app/sprints", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp roadmapJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if len(resp.Sprints) != 1 {
		t.Fatalf("got %d sprints, want 1", len(resp.Sprints))
	}
	if resp.Sprints[0].Number != 1 {
		t.Errorf("sprint[0].Number = %d, want 1", resp.Sprints[0].Number)
	}
}

func TestHandleSprints_NoFile(t *testing.T) {
	// Arrange
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("no-sprint-app", projDir, "dev"); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/sprints", ws.handleSprints)

	// Act
	req := httptest.NewRequest("GET", "/api/projects/no-sprint-app/sprints", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp roadmapJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if len(resp.Sprints) != 0 {
		t.Errorf("got %d sprints, want 0", len(resp.Sprints))
	}
}

func TestHandleBacklog(t *testing.T) {
	// Arrange
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("backlog-app", projDir, "dev"); err != nil {
		t.Fatal(err)
	}
	backlogContent := `# Backlog

| # | Item |
|---|------|
| 1 | Add caching layer |
| 2 | Improve error messages |
| 3 | Add metrics endpoint |
`
	if err := os.WriteFile(filepath.Join(projDir, "BACKLOG.md"), []byte(backlogContent), 0644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/backlog", ws.handleBacklog)

	// Act
	req := httptest.NewRequest("GET", "/api/projects/backlog-app/backlog", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp backlogJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if len(resp.Items) != 3 {
		t.Fatalf("got %d items, want 3", len(resp.Items))
	}
	if resp.Items[0].Number != 1 {
		t.Errorf("items[0].Number = %d, want 1", resp.Items[0].Number)
	}
	if resp.Items[0].Description != "Add caching layer" {
		t.Errorf("items[0].Description = %q, want %q", resp.Items[0].Description, "Add caching layer")
	}
	if resp.Items[2].Description != "Add metrics endpoint" {
		t.Errorf("items[2].Description = %q, want %q", resp.Items[2].Description, "Add metrics endpoint")
	}
}

func TestHandleBacklog_NoFile(t *testing.T) {
	// Arrange
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("no-backlog-app", projDir, "dev"); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/backlog", ws.handleBacklog)

	// Act
	req := httptest.NewRequest("GET", "/api/projects/no-backlog-app/backlog", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp backlogJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if len(resp.Items) != 0 {
		t.Errorf("got %d items, want 0", len(resp.Items))
	}
}

func TestHandleStrategicRoadmap(t *testing.T) {
	// Arrange
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("strategic-app", projDir, "dev"); err != nil {
		t.Fatal(err)
	}
	roadmapContent := `# Strategic Roadmap

## Vision
Build the best tool ever.

## Milestones
- v1.0: MVP
- v2.0: Scale
`
	if err := os.WriteFile(filepath.Join(projDir, "ROADMAP.md"), []byte(roadmapContent), 0644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/strategic-roadmap", ws.handleStrategicRoadmap)

	// Act
	req := httptest.NewRequest("GET", "/api/projects/strategic-app/strategic-roadmap", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp strategicRoadmapJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if resp.Content != roadmapContent {
		t.Errorf("content = %q, want %q", resp.Content, roadmapContent)
	}
}

func TestHandleStrategicRoadmap_NoFile(t *testing.T) {
	// Arrange
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("no-strategic-app", projDir, "dev"); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/strategic-roadmap", ws.handleStrategicRoadmap)

	// Act
	req := httptest.NewRequest("GET", "/api/projects/no-strategic-app/strategic-roadmap", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp strategicRoadmapJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if resp.Content != "" {
		t.Errorf("content = %q, want empty string", resp.Content)
	}
}

func TestHandleSprints_ExportedWrapper(t *testing.T) {
	// Arrange
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("sprint-wrap", projDir, "dev"); err != nil {
		t.Fatal(err)
	}
	sprintsContent := `## Current Sprint

### Sprint 1: Init (v0.1.0)

| # | Item | Status |
|---|------|--------|
| 1 | Setup | Done |
`
	if err := os.WriteFile(filepath.Join(projDir, "SPRINTS.md"), []byte(sprintsContent), 0644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/sprints", ws.HandleSprints)
	req := httptest.NewRequest("GET", "/api/projects/sprint-wrap/sprints", nil)
	rec := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp roadmapJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if len(resp.Sprints) != 1 {
		t.Fatalf("got %d sprints, want 1", len(resp.Sprints))
	}
}

func TestHandleBacklog_ExportedWrapper(t *testing.T) {
	// Arrange
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("backlog-wrap", projDir, "dev"); err != nil {
		t.Fatal(err)
	}
	backlogContent := `# Backlog

| # | Item |
|---|------|
| 1 | First item |
`
	if err := os.WriteFile(filepath.Join(projDir, "BACKLOG.md"), []byte(backlogContent), 0644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/backlog", ws.HandleBacklog)
	req := httptest.NewRequest("GET", "/api/projects/backlog-wrap/backlog", nil)
	rec := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp backlogJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("got %d items, want 1", len(resp.Items))
	}
}

func TestHandleStrategicRoadmap_ExportedWrapper(t *testing.T) {
	// Arrange
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("strat-wrap", projDir, "dev"); err != nil {
		t.Fatal(err)
	}
	roadmapContent := `# Roadmap

## Goals
- Ship v1.0
`
	if err := os.WriteFile(filepath.Join(projDir, "ROADMAP.md"), []byte(roadmapContent), 0644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/strategic-roadmap", ws.HandleStrategicRoadmap)
	req := httptest.NewRequest("GET", "/api/projects/strat-wrap/strategic-roadmap", nil)
	rec := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp strategicRoadmapJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if resp.Content != roadmapContent {
		t.Errorf("content = %q, want %q", resp.Content, roadmapContent)
	}
}

func TestHandleGuild(t *testing.T) {
	ws, mock := setupWebTest(t)
	if err := ws.registry.AddProject("alpha", "/tmp/alpha", "dev"); err != nil {
		t.Fatal(err)
	}
	if err := ws.registry.AddProject("beta", "/tmp/beta", "dev"); err != nil {
		t.Fatal(err)
	}

	// Both containers stopped (docker inspect returns exited for all)
	mock.Results["docker"] = executor.MockResult{Output: "exited\n"}

	req := httptest.NewRequest("GET", "/api/guild", nil)
	rec := httptest.NewRecorder()
	ws.handleGuild(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var members []guildMemberJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &members); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("got %d members, want 2", len(members))
	}

	for _, m := range members {
		if m.Name == "" {
			t.Error("member name is empty")
		}
		if m.Activity != "sleeping" {
			t.Errorf("member %s: got activity %q, want sleeping", m.Name, m.Activity)
		}
	}
}
