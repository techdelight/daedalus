// Copyright (C) 2026 Techdelight BV

package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/techdelight/daedalus/core"
)

// writeDocs creates each named file in dir with placeholder content.
func writeDocs(t *testing.T, dir string, filenames ...string) {
	t.Helper()
	for _, f := range filenames {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("# "+f+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func getDocs(t *testing.T, ws *WebServer, project string) (*httptest.ResponseRecorder, docsJSON) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/docs", ws.handleDocs)
	req := httptest.NewRequest("GET", "/api/projects/"+project+"/docs", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var resp docsJSON
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return rec, resp
}

func TestHandleDocs_AllPresent(t *testing.T) {
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("full-app", projDir, "dev"); err != nil {
		t.Fatal(err)
	}
	for _, d := range core.RequiredDocs() {
		writeDocs(t, projDir, d.Filename)
	}

	rec, resp := getDocs(t, ws, "full-app")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if resp.Present != 8 || resp.Total != 8 {
		t.Errorf("present/total = %d/%d, want 8/8", resp.Present, resp.Total)
	}
	if len(resp.Missing) != 0 {
		t.Errorf("Missing = %v, want empty", resp.Missing)
	}
	for _, d := range resp.Docs {
		if !d.Present {
			t.Errorf("doc %q reported absent", d.Name)
		}
	}
}

func TestHandleDocs_SomeMissing(t *testing.T) {
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("partial-app", projDir, "dev"); err != nil {
		t.Fatal(err)
	}
	// Six of eight: no VISION.md, no CONTRIBUTING.md.
	writeDocs(t, projDir,
		"README.md", "ARCHITECTURE.md", "ROADMAP.md",
		"BACKLOG.md", "SPRINTS.md", "CHANGELOG.md")

	_, resp := getDocs(t, ws, "partial-app")
	if resp.Present != 6 || resp.Total != 8 {
		t.Errorf("present/total = %d/%d, want 6/8", resp.Present, resp.Total)
	}
	want := []string{"Vision", "Contributing"}
	if len(resp.Missing) != len(want) {
		t.Fatalf("Missing = %v, want %v", resp.Missing, want)
	}
	for i, w := range want {
		if resp.Missing[i] != w {
			t.Errorf("Missing[%d] = %q, want %q", i, resp.Missing[i], w)
		}
	}
}

func TestHandleDocs_NoDocs(t *testing.T) {
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("bare-app", projDir, "dev"); err != nil {
		t.Fatal(err)
	}

	_, resp := getDocs(t, ws, "bare-app")
	if resp.Present != 0 || resp.Total != 8 {
		t.Errorf("present/total = %d/%d, want 0/8", resp.Present, resp.Total)
	}
	if len(resp.Missing) != 8 {
		t.Errorf("len(Missing) = %d, want 8", len(resp.Missing))
	}
}

// `touch VISION.md` leaves a placeholder, not a vision. Counting it as
// present would hide the exact gap the badge exists to surface.
func TestHandleDocs_EmptyFileCountsAsMissing(t *testing.T) {
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("empty-vision", projDir, "dev"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "VISION.md"), nil, 0644); err != nil {
		t.Fatal(err)
	}

	_, resp := getDocs(t, ws, "empty-vision")
	if resp.Present != 0 {
		t.Errorf("Present = %d, want 0 (empty file must not count)", resp.Present)
	}
	for _, d := range resp.Docs {
		if d.Filename == "VISION.md" && d.Present {
			t.Error("empty VISION.md reported as present")
		}
	}
}

func TestHandleDocs_DirectoryDoesNotCount(t *testing.T) {
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("dir-doc", projDir, "dev"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(projDir, "README.md"), 0755); err != nil {
		t.Fatal(err)
	}

	_, resp := getDocs(t, ws, "dir-doc")
	if resp.Present != 0 {
		t.Errorf("Present = %d, want 0 (directory must not count)", resp.Present)
	}
}

func TestHandleDocs_ProjectNotFound(t *testing.T) {
	ws, _ := setupWebTest(t)

	rec, _ := getDocs(t, ws, "nope")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleVision_Success(t *testing.T) {
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("vision-app", projDir, "dev"); err != nil {
		t.Fatal(err)
	}
	content := "# Vision\n\nOrchestrate agents across a programme of projects.\n"
	if err := os.WriteFile(filepath.Join(projDir, "VISION.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/vision", ws.handleVision)
	req := httptest.NewRequest("GET", "/api/projects/vision-app/vision", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var resp visionJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Content != content {
		t.Errorf("Content = %q, want %q", resp.Content, content)
	}
}

// A project with no VISION.md is a 404, not 200-with-empty-string: the
// document is genuinely absent and the frontend should say so.
func TestHandleVision_NoFile(t *testing.T) {
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("no-vision", projDir, "dev"); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/vision", ws.handleVision)
	req := httptest.NewRequest("GET", "/api/projects/no-vision/vision", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

// A placeholder is not a vision: `touch VISION.md` must 404 exactly as an
// absent file does, so this endpoint and the docs badge agree on the file.
func TestHandleVision_EmptyFileIs404(t *testing.T) {
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("placeholder-vision", projDir, "dev"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "VISION.md"), nil, 0644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/vision", ws.handleVision)
	req := httptest.NewRequest("GET", "/api/projects/placeholder-vision/vision", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

// The docs badge and the vision endpoint must not disagree about the same
// placeholder file — one reporting absent while the other serves content.
func TestHandleVision_AgreesWithDocsBadge(t *testing.T) {
	ws, _ := setupWebTest(t)
	projDir := t.TempDir()
	if err := ws.registry.AddProject("agree-vision", projDir, "dev"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "VISION.md"), nil, 0644); err != nil {
		t.Fatal(err)
	}

	_, docs := getDocs(t, ws, "agree-vision")
	var visionPresent bool
	for _, d := range docs.Docs {
		if d.Filename == "VISION.md" {
			visionPresent = d.Present
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/vision", ws.handleVision)
	req := httptest.NewRequest("GET", "/api/projects/agree-vision/vision", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	servedOK := rec.Code == http.StatusOK
	if visionPresent != servedOK {
		t.Errorf("docs badge present=%v but vision endpoint served=%v; they must agree",
			visionPresent, servedOK)
	}
}

func TestHandleVision_ProjectNotFound(t *testing.T) {
	ws, _ := setupWebTest(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{name}/vision", ws.handleVision)
	req := httptest.NewRequest("GET", "/api/projects/nope/vision", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
