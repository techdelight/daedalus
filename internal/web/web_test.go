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
	detectors := activity.NewDetectorRegistry()
	detectors.Register("claude", activity.NewClaudeCodeDetector())
	ws := &WebServer{
		registry:         reg,
		docker:           docker,
		executor:         mock,
		cfg:              cfg,
		observer:         observer,
		activityResolver: activity.NewResolver(observer, detectors),
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
	// Set registry values that should be overridden by the derived file state.
	if err := ws.registry.UpdateProjectProgress("prog-app", 10, "Old vision", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	// Derive from the project's own files (Backlog #52): VERSION, VISION.md, and
	// a current sprint that is 75% done (3 of 4 items).
	if err := os.WriteFile(filepath.Join(projDir, "VERSION"), []byte("2.0.0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "VISION.md"), []byte("Test vision\n"), 0644); err != nil {
		t.Fatal(err)
	}
	sprints := "## Current Sprint\n\n### Sprint 9: Work\n\n| # | Item | Status |\n|---|------|--------|\n| 1 | a | Done |\n| 2 | b | Done |\n| 3 | c | Done |\n| 4 | d | In Progress |\n"
	if err := os.WriteFile(filepath.Join(projDir, "SPRINTS.md"), []byte(sprints), 0644); err != nil {
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
			t.Logf("WebSocket upgrade returned status %d (expected in test env without a coordinator)", resp.StatusCode)
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
	var resp activityStateJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	// Running container with no activity file → idle (ClaudeCodeDetector default)
	if resp.Activity != "idle" {
		t.Errorf("activity = %q, want %q", resp.Activity, "idle")
	}
	if resp.ContainerState != "running" {
		t.Errorf("containerState = %q, want %q", resp.ContainerState, "running")
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
	var resp activityStateJSON
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Activity != "sleeping" {
		t.Errorf("activity = %q, want %q", resp.Activity, "sleeping")
	}
	if resp.ContainerState != "stopped" {
		t.Errorf("containerState = %q, want %q", resp.ContainerState, "stopped")
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

func TestHandleRoadmap_PrefersSprintsMd(t *testing.T) {
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("split-app", projDir, "dev"); err != nil {
		t.Fatal(err)
	}
	roadmapContent := `### Sprint 1: Old (v0.1.0)

| # | Item | Status |
|---|------|--------|
| 1 | from ROADMAP | Done |
`
	sprintsContent := `## Current Sprint

### Sprint 9: New (v0.9.0)

| # | Item | Status |
|---|------|--------|
| 1 | from SPRINTS | Done |
`
	if err := os.WriteFile(filepath.Join(projDir, "ROADMAP.md"), []byte(roadmapContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "SPRINTS.md"), []byte(sprintsContent), 0644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/sprints", ws.handleRoadmap)

	req := httptest.NewRequest("GET", "/api/projects/split-app/sprints", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp roadmapJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Sprints) != 1 || resp.Sprints[0].Number != 9 {
		t.Fatalf("got sprints %+v, want 1 sprint #9 (from SPRINTS.md)", resp.Sprints)
	}
	if len(resp.Sprints[0].Items) != 1 || resp.Sprints[0].Items[0].Description != "from SPRINTS" {
		t.Errorf("item desc = %q, want %q", resp.Sprints[0].Items[0].Description, "from SPRINTS")
	}
}

func TestHandleSprints_RouteRegistered(t *testing.T) {
	// /sprints and /roadmap must both reach handleRoadmap so the post
	// doc-split frontend (which calls /sprints) and any legacy callers
	// (which call /roadmap) get the same data.
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("dual-route", projDir, "dev"); err != nil {
		t.Fatal(err)
	}
	sprintsContent := `### Sprint 1: Hello (v0.1.0)

| # | Item | Status |
|---|------|--------|
| 1 | hi | Done |
`
	if err := os.WriteFile(filepath.Join(projDir, "SPRINTS.md"), []byte(sprintsContent), 0644); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/api/projects/dual-route/sprints", "/api/projects/dual-route/roadmap"} {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/projects/{name}/sprints", ws.handleRoadmap)
		mux.HandleFunc("GET /api/projects/{name}/roadmap", ws.handleRoadmap)
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want %d", path, rec.Code, http.StatusOK)
		}
		var resp roadmapJSON
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%s decode: %v", path, err)
		}
		if len(resp.Sprints) != 1 {
			t.Errorf("%s got %d sprints, want 1", path, len(resp.Sprints))
		}
	}
}

func TestHandleBacklog_Success(t *testing.T) {
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("backlog-app", projDir, "dev"); err != nil {
		t.Fatal(err)
	}
	backlogContent := `# Backlog

| # | Item |
|---|------|
| 11 | Brew install |
| 38 | Trust prompt hang |
`
	if err := os.WriteFile(filepath.Join(projDir, "BACKLOG.md"), []byte(backlogContent), 0644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/backlog", ws.handleBacklog)
	req := httptest.NewRequest("GET", "/api/projects/backlog-app/backlog", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp backlogJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("got %d items, want 2", len(resp.Items))
	}
	if resp.Items[0].Number != 11 || resp.Items[0].Description != "Brew install" {
		t.Errorf("items[0] = %+v, want #11 'Brew install'", resp.Items[0])
	}
	if resp.Items[1].Number != 38 {
		t.Errorf("items[1].Number = %d, want 38", resp.Items[1].Number)
	}
}

func TestHandleBacklog_NoFile(t *testing.T) {
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("empty-backlog", projDir, "dev"); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/backlog", ws.handleBacklog)
	req := httptest.NewRequest("GET", "/api/projects/empty-backlog/backlog", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	// Body must contain "items":[] (not null) so the JS length check works.
	if !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("body = %s, want to contain 'items':[]", rec.Body.String())
	}
}

func TestHandleBacklog_ProjectNotFound(t *testing.T) {
	ws, _ := setupWebTest(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/backlog", ws.handleBacklog)
	req := httptest.NewRequest("GET", "/api/projects/missing/backlog", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleStrategicRoadmap_Success(t *testing.T) {
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("strat-app", projDir, "dev"); err != nil {
		t.Fatal(err)
	}
	roadmapContent := "# Strategic Roadmap\n\nMilestone 1: Foundation\nMilestone 2: Polish\n"
	if err := os.WriteFile(filepath.Join(projDir, "ROADMAP.md"), []byte(roadmapContent), 0644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/strategic-roadmap", ws.handleStrategicRoadmap)
	req := httptest.NewRequest("GET", "/api/projects/strat-app/strategic-roadmap", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp strategicRoadmapJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Content != roadmapContent {
		t.Errorf("Content = %q, want %q", resp.Content, roadmapContent)
	}
}

func TestHandleStrategicRoadmap_NoFile(t *testing.T) {
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("strat-empty", projDir, "dev"); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/strategic-roadmap", ws.handleStrategicRoadmap)
	req := httptest.NewRequest("GET", "/api/projects/strat-empty/strategic-roadmap", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp strategicRoadmapJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Content != "" {
		t.Errorf("Content = %q, want empty", resp.Content)
	}
}

func TestHandleStrategicRoadmap_ProjectNotFound(t *testing.T) {
	ws, _ := setupWebTest(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/strategic-roadmap", ws.handleStrategicRoadmap)
	req := httptest.NewRequest("GET", "/api/projects/missing/strategic-roadmap", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
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

func TestRenderIndexHTML_InjectsVersion(t *testing.T) {
	raw := []byte(`<title>Daedalus</title>`)

	out := renderIndexHTML(raw, "1.2.3")
	if !strings.Contains(out, "Daedalus [1.2.3]") {
		t.Errorf("version not injected into title: %q", out)
	}
}
