// Copyright (C) 2026 Techdelight BV

package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	tokenBytes  = 16 // 128-bit token → 32 hex characters
	cookieName  = "daedalus_session"
	defaultExpiry = 24 // hours
)

// GenerateToken creates a cryptographically random hex token.
func GenerateToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating auth token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// EnsureToken loads the auth token from config.json, generating and saving one
// if absent. Returns the token string.
func EnsureToken(configDir string) (string, error) {
	path := filepath.Join(configDir, "config.json")
	raw := map[string]any{}

	data, err := os.ReadFile(path)
	if err == nil {
		json.Unmarshal(data, &raw) // ignore parse errors; we'll overwrite
	}

	if tok, ok := raw["auth-token"].(string); ok && tok != "" {
		return tok, nil
	}

	tok, err := GenerateToken()
	if err != nil {
		return "", err
	}
	raw["auth-token"] = tok

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshalling config: %w", err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0600); err != nil {
		return "", fmt.Errorf("writing config: %w", err)
	}
	return tok, nil
}

// Middleware returns an HTTP handler that enforces token authentication.
// Requests to /login and /static/ are exempt.
// Authenticated sessions use a cookie; the token can also be passed as a
// query parameter (for WebSocket connections).
func Middleware(token string, expiryHours int, next http.Handler) http.Handler {
	if expiryHours <= 0 {
		expiryHours = defaultExpiry
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Exempt paths
		if path == "/login" || path == "/static/favicon.svg" {
			next.ServeHTTP(w, r)
			return
		}
		if len(path) > 8 && path[:8] == "/static/" {
			next.ServeHTTP(w, r)
			return
		}

		// Check session cookie
		if cookie, err := r.Cookie(cookieName); err == nil {
			if subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(token)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
		}

		// Check query parameter (for WebSocket)
		if q := r.URL.Query().Get("token"); q != "" {
			if subtle.ConstantTimeCompare([]byte(q), []byte(token)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
		}

		// Not authenticated
		if path == "/" || path == "/{$}" {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	})
}

// LoginHandler returns a handler for GET /login (renders form) and
// POST /login (validates token, sets cookie, redirects).
func LoginHandler(token string, expiryHours int) http.HandlerFunc {
	if expiryHours <= 0 {
		expiryHours = defaultExpiry
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(loginPage("")))
			return
		}
		if r.Method == http.MethodPost {
			r.ParseForm()
			submitted := r.FormValue("token")
			if subtle.ConstantTimeCompare([]byte(submitted), []byte(token)) == 1 {
				http.SetCookie(w, &http.Cookie{
					Name:     cookieName,
					Value:    token,
					Path:     "/",
					MaxAge:   expiryHours * 3600,
					HttpOnly: true,
					SameSite: http.SameSiteStrictMode,
					Expires:  time.Now().Add(time.Duration(expiryHours) * time.Hour),
				})
				http.Redirect(w, r, "/", http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(loginPage("Invalid token")))
			return
		}
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func loginPage(errMsg string) string {
	errHTML := ""
	if errMsg != "" {
		errHTML = `<p style="color:#f7768e;margin-bottom:16px;">` + errMsg + `</p>`
	}
	return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Daedalus — Login</title>
<link rel="icon" type="image/svg+xml" href="/static/favicon.svg">
<style>
  body { background:#1a1b26; color:#c0caf5; font-family:system-ui,sans-serif; display:flex; align-items:center; justify-content:center; height:100vh; margin:0; }
  .card { background:#24283b; border:1px solid #292e42; border-radius:8px; padding:32px; width:320px; text-align:center; }
  h1 { font-size:24px; margin:0 0 8px; color:#7aa2f7; }
  p.sub { font-size:13px; color:#565f89; margin:0 0 24px; }
  input[type=text] { width:100%; padding:10px; border:1px solid #292e42; border-radius:4px; background:#1a1b26; color:#c0caf5; font-size:14px; box-sizing:border-box; margin-bottom:16px; text-align:center; font-family:monospace; }
  input[type=text]:focus { outline:none; border-color:#7aa2f7; }
  button { width:100%; padding:10px; border:none; border-radius:4px; background:#7aa2f7; color:#1a1b26; font-size:14px; font-weight:600; cursor:pointer; }
  button:hover { background:#89b4fa; }
</style>
</head>
<body>
<div class="card">
  <h1>Daedalus</h1>
  <p class="sub">Enter your access token to continue</p>
  ` + errHTML + `
  <form method="POST" action="/login">
    <input type="text" name="token" placeholder="Access token" autocomplete="off" autofocus>
    <button type="submit">Login</button>
  </form>
</div>
</body>
</html>`
}
