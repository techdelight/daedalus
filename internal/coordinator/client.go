// Copyright (C) 2026 Techdelight BV

package coordinator

// Go client for the coordinator daemon's HTTP-over-UDS surface. This
// is slice 3 of Sprint 40: the wire-level counterpart to daemon.go so
// UI processes (CLI, TUI, Web) can talk to a long-lived daemon
// instead of each running their own in-process Coordinator.
//
// Method shape mirrors Coordinator but every call returns an error —
// HTTP has transport failures that the in-process type simply cannot
// produce. Domain outcomes (ErrAlreadyRunning, ErrNotFound) survive
// the wire crossing as their original sentinels via errors.Is, so
// callers who used to switch on those from Coordinator keep working
// after the swap.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/techdelight/daedalus/core"
)

// Client is a Go wrapper over the daemon's HTTP API. Safe for
// concurrent use — the underlying http.Client is.
type Client struct {
	httpClient *http.Client
	baseURL    string
}

// NewClient constructs a Client that dials the daemon over the given
// Unix socket path. Requests block up to 90s to leave headroom for
// Coordinator.Start's 30s runner-socket wait plus retries.
func NewClient(socketPath string) *Client {
	return newClient("http://coordinator", &http.Client{
		Timeout: 90 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
	})
}

// newClient is the raw constructor. Reserved for tests that point at
// an httptest.Server instead of a Unix socket — production callers
// use NewClient.
func newClient(baseURL string, httpClient *http.Client) *Client {
	return &Client{baseURL: baseURL, httpClient: httpClient}
}

// Start asks the daemon to launch a new runner-attached container.
// Returns ErrAlreadyRunning if the daemon already tracks a session
// for cfg.ProjectName.
func (c *Client) Start(cfg *core.Config) (*Session, error) {
	body, err := json.Marshal(startRequestFromConfig(cfg))
	if err != nil {
		return nil, fmt.Errorf("coordinator client: marshal request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/sessions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("coordinator client: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("coordinator client: POST /sessions: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusCreated:
		return decodeSessionBody(resp.Body)
	case http.StatusConflict:
		return nil, ErrAlreadyRunning
	default:
		return nil, decodeErrorBody(resp)
	}
}

// List returns the sessions the daemon is tracking. An empty slice is
// returned when the daemon has no sessions, never nil.
func (c *Client) List() ([]Session, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/sessions")
	if err != nil {
		return nil, fmt.Errorf("coordinator client: GET /sessions: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, decodeErrorBody(resp)
	}
	var sessions []Session
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		return nil, fmt.Errorf("coordinator client: decode sessions: %w", err)
	}
	if sessions == nil {
		sessions = []Session{}
	}
	return sessions, nil
}

// Get returns the session for the given project name. Returns
// ErrNotFound if the daemon has no session tracked for it.
func (c *Client) Get(name string) (*Session, error) {
	u := c.baseURL + "/sessions/" + url.PathEscape(name)
	resp, err := c.httpClient.Get(u)
	if err != nil {
		return nil, fmt.Errorf("coordinator client: GET /sessions/%s: %w", name, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return decodeSessionBody(resp.Body)
	case http.StatusNotFound:
		return nil, ErrNotFound
	default:
		return nil, decodeErrorBody(resp)
	}
}

// Stop asks the daemon to stop the session for the given project.
// Returns ErrNotFound if no session is tracked; other errors surface
// the daemon-side failure (typically a docker error) verbatim.
func (c *Client) Stop(name string) error {
	u := c.baseURL + "/sessions/" + url.PathEscape(name)
	req, err := http.NewRequest(http.MethodDelete, u, nil)
	if err != nil {
		return fmt.Errorf("coordinator client: build request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("coordinator client: DELETE /sessions/%s: %w", name, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil
	case http.StatusNotFound:
		return ErrNotFound
	default:
		return decodeErrorBody(resp)
	}
}

// startRequestFromConfig extracts the fields the daemon needs. Kept
// close to the server-side toConfig (daemon.go) so the wire contract
// stays visible in one glance: if a field is added here it must land
// there too.
func startRequestFromConfig(cfg *core.Config) *StartRequest {
	return &StartRequest{
		ProjectName:     cfg.ProjectName,
		ProjectDir:      cfg.ProjectDir,
		DataDir:         cfg.DataDir,
		Target:          cfg.Target,
		ImagePrefix:     cfg.ImagePrefix,
		ContainerPrefix: cfg.ContainerPrefix,
		Runner:          cfg.Runner,
		Persona:         cfg.Persona,
		Debug:           cfg.Debug,
		Resume:          cfg.Resume,
		Prompt:          cfg.Prompt,
	}
}

func decodeSessionBody(body io.Reader) (*Session, error) {
	var s Session
	if err := json.NewDecoder(body).Decode(&s); err != nil {
		return nil, fmt.Errorf("coordinator client: decode session: %w", err)
	}
	return &s, nil
}

// decodeErrorBody unpacks the {"error": "..."} envelope the daemon
// sends on 4xx/5xx. If the body is malformed we still surface the
// status code so callers can distinguish outcomes.
func decodeErrorBody(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	var envelope struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Error != "" {
		return fmt.Errorf("coordinator daemon %d: %s", resp.StatusCode, envelope.Error)
	}
	return fmt.Errorf("coordinator daemon %d", resp.StatusCode)
}
