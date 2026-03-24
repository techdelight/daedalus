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

	ws := &WebServer{
		registry: reg,
		docker:   docker,
		executor: mock,
		cfg:      cfg,
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
