// Copyright (C) 2026 Techdelight BV

package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateToken(t *testing.T) {
	tok, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}
	if len(tok) != tokenBytes*2 {
		t.Errorf("token length = %d, want %d", len(tok), tokenBytes*2)
	}

	tok2, _ := GenerateToken()
	if tok == tok2 {
		t.Error("two calls returned the same token")
	}
}

func TestEnsureToken_GeneratesAndPersists(t *testing.T) {
	dir := t.TempDir()
	// Write minimal config.json
	os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0644)

	tok, err := EnsureToken(dir)
	if err != nil {
		t.Fatalf("EnsureToken() error = %v", err)
	}
	if tok == "" {
		t.Fatal("EnsureToken() returned empty token")
	}

	// Second call should return the same token
	tok2, err := EnsureToken(dir)
	if err != nil {
		t.Fatalf("EnsureToken() second call error = %v", err)
	}
	if tok2 != tok {
		t.Errorf("second call returned different token: %q vs %q", tok2, tok)
	}

	// Verify persisted in config.json
	data, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	var raw map[string]any
	json.Unmarshal(data, &raw)
	if raw["auth-token"] != tok {
		t.Errorf("config.json auth-token = %v, want %q", raw["auth-token"], tok)
	}
}

func TestEnsureToken_PreservesExistingConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := map[string]any{"version": "1.0.0", "debug": true}
	data, _ := json.Marshal(cfg)
	os.WriteFile(filepath.Join(dir, "config.json"), data, 0644)

	tok, err := EnsureToken(dir)
	if err != nil {
		t.Fatalf("EnsureToken() error = %v", err)
	}

	// Verify existing fields preserved
	out, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	var raw map[string]any
	json.Unmarshal(out, &raw)
	if raw["version"] != "1.0.0" {
		t.Errorf("version lost after EnsureToken")
	}
	if raw["auth-token"] != tok {
		t.Errorf("auth-token not saved")
	}
}

func TestEnsureToken_NoConfigFile(t *testing.T) {
	dir := t.TempDir()
	tok, err := EnsureToken(dir)
	if err != nil {
		t.Fatalf("EnsureToken() error = %v", err)
	}
	if tok == "" {
		t.Fatal("EnsureToken() returned empty token")
	}
}

func TestMiddleware_AllowsExemptPaths(t *testing.T) {
	handler := Middleware("secret", 24, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, path := range []string{"/login", "/static/style.css", "/static/favicon.svg"} {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("path %q: got %d, want 200", path, w.Code)
		}
	}
}

func TestMiddleware_RedirectsRootWithoutAuth(t *testing.T) {
	handler := Middleware("secret", 24, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Errorf("root without auth: got %d, want 302", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Errorf("redirect location = %q, want /login", loc)
	}
}

func TestMiddleware_RejectsAPIWithoutAuth(t *testing.T) {
	handler := Middleware("secret", 24, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/projects", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("API without auth: got %d, want 401", w.Code)
	}
}

func TestMiddleware_AcceptsCookie(t *testing.T) {
	handler := Middleware("secret", 24, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/projects", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "secret"})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("with valid cookie: got %d, want 200", w.Code)
	}
}

func TestMiddleware_RejectsWrongCookie(t *testing.T) {
	handler := Middleware("secret", 24, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/projects", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "wrong"})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("with wrong cookie: got %d, want 401", w.Code)
	}
}

func TestMiddleware_AcceptsQueryToken(t *testing.T) {
	handler := Middleware("secret", 24, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/projects/test/terminal?token=secret", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("with valid query token: got %d, want 200", w.Code)
	}
}

func TestLoginHandler_GET(t *testing.T) {
	handler := LoginHandler("secret", 24)
	req := httptest.NewRequest("GET", "/login", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("GET /login: got %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Access token") {
		t.Error("login page missing token input")
	}
}

func TestLoginHandler_POST_ValidToken(t *testing.T) {
	handler := LoginHandler("secret", 24)
	form := url.Values{"token": {"secret"}}
	req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("POST /login valid: got %d, want 302", w.Code)
	}
	cookies := w.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == cookieName && c.Value == "secret" {
			found = true
			if !c.HttpOnly {
				t.Error("cookie should be HttpOnly")
			}
		}
	}
	if !found {
		t.Error("session cookie not set after valid login")
	}
}

func TestLoginHandler_POST_InvalidToken(t *testing.T) {
	handler := LoginHandler("secret", 24)
	form := url.Values{"token": {"wrong"}}
	req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("POST /login invalid: got %d, want 401", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Invalid token") {
		t.Error("error message not shown for invalid token")
	}
}
