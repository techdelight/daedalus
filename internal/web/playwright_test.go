// Copyright (C) 2026 Techdelight BV

package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/executor"
)

func TestPlaywright(t *testing.T) {
	if os.Getenv("RUN_PLAYWRIGHT") != "1" {
		t.Skip("RUN_PLAYWRIGHT not set — skipping Playwright tests")
	}

	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not found — skipping Playwright tests")
	}

	ws, mock := setupWebTest(t)

	// Seed test projects: alpha (running), beta (stopped), gamma (stopped).
	for _, p := range []struct{ name, dir, target string }{
		{"alpha", "/path/alpha", "dev"},
		{"beta", "/path/beta", "dev"},
		{"gamma", "/path/gamma", "godot"},
	} {
		if err := ws.registry.AddProject(p.name, p.dir, p.target); err != nil {
			t.Fatalf("add project %s: %v", p.name, err)
		}
	}

	// Docker ps returns only alpha — makes alpha running, others stopped.
	mock.Results["docker"] = executor.MockResult{Output: "claude-run-alpha\n"}

	// Set a known version for title injection.
	old := core.Version
	core.Version = "0.13.0-test"
	defer func() { core.Version = old }()

	version := core.ReadVersion()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects", ws.handleListProjects)
	mux.HandleFunc("POST /api/projects/{name}/start", ws.handleStartProject)
	mux.HandleFunc("POST /api/projects/{name}/stop", ws.handleStopProject)
	mux.HandleFunc("POST /api/projects/{name}/rename", ws.handleRenameProject)
	mux.HandleFunc("GET /api/projects/{name}/terminal", ws.handleTerminal)

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
		html := strings.Replace(string(data), ">Daedalus<", ">Daedalus ["+version+"]<", 1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(html))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	cmd := exec.Command("npx", "playwright", "test")
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "DAEDALUS_TEST_URL="+server.URL)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("playwright tests failed: %v", err)
	}
}
