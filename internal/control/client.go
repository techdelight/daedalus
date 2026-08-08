// Copyright (C) 2026 Techdelight BV

package control

// Go client for the daedalus-control daemon, the wire counterpart to daemon.go.
// It implements TaskAPI, so the CLI command handlers are identical whether given
// a live *Client (production) or an in-process *Service (tests).
//
// Domain sentinels survive the wire: the daemon maps them to status codes and an
// {"error": ...} envelope, and the client re-raises a matching sentinel via
// errors.Is where the CLI needs to branch (ErrNotFound, active-task conflict).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Client talks to the daemon over a Unix socket. Safe for concurrent use.
type Client struct {
	httpClient *http.Client
	baseURL    string
}

// NewClient dials the daemon at socketPath. No overall timeout: a dispatch can
// legitimately run a headless agent to completion.
func NewClient(socketPath string) *Client {
	return newClient("http://control", &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
	})
}

func newClient(baseURL string, httpClient *http.Client) *Client {
	return &Client{baseURL: baseURL, httpClient: httpClient}
}

// CreateTask implements TaskAPI.
func (c *Client) CreateTask(req CreateTaskRequest) (Task, error) {
	body, _ := json.Marshal(req)
	resp, err := c.httpClient.Post(c.baseURL+"/tasks", "application/json", bytes.NewReader(body))
	if err != nil {
		return Task{}, fmt.Errorf("control client: POST /tasks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return Task{}, decodeError(resp)
	}
	var t Task
	return t, json.NewDecoder(resp.Body).Decode(&t)
}

// ListTasks implements TaskAPI.
func (c *Client) ListTasks() ([]Task, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/tasks")
	if err != nil {
		return nil, fmt.Errorf("control client: GET /tasks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, decodeError(resp)
	}
	var tasks []Task
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		return nil, err
	}
	if tasks == nil {
		tasks = []Task{}
	}
	return tasks, nil
}

// TaskStatus implements TaskAPI.
func (c *Client) TaskStatus(id string) (StatusView, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/tasks/" + url.PathEscape(id))
	if err != nil {
		return StatusView{}, fmt.Errorf("control client: GET /tasks/%s: %w", id, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return StatusView{}, decodeError(resp)
	}
	var v StatusView
	return v, json.NewDecoder(resp.Body).Decode(&v)
}

// DispatchTask implements TaskAPI.
func (c *Client) DispatchTask(id string) (DispatchResult, error) {
	resp, err := c.httpClient.Post(c.baseURL+"/tasks/"+url.PathEscape(id)+"/dispatch", "application/json", nil)
	if err != nil {
		return DispatchResult{}, fmt.Errorf("control client: POST /tasks/%s/dispatch: %w", id, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return DispatchResult{}, decodeError(resp)
	}
	var res DispatchResult
	return res, json.NewDecoder(resp.Body).Decode(&res)
}

// CancelTask implements TaskAPI.
func (c *Client) CancelTask(id string) (Task, error) {
	req, _ := http.NewRequest(http.MethodDelete, c.baseURL+"/tasks/"+url.PathEscape(id), nil)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Task{}, fmt.Errorf("control client: DELETE /tasks/%s: %w", id, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Task{}, decodeError(resp)
	}
	var t Task
	return t, json.NewDecoder(resp.Body).Decode(&t)
}

// decodeError unpacks the {"error": ...} envelope and, where the CLI branches on
// them, re-raises a matching sentinel wrapped so errors.Is/As still work.
func decodeError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	var env struct {
		Error string `json:"error"`
	}
	msg := strings.TrimSpace(string(body))
	if err := json.Unmarshal(body, &env); err == nil && env.Error != "" {
		msg = env.Error
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: %s", ErrNotFound, msg)
	}
	return fmt.Errorf("control daemon %d: %s", resp.StatusCode, msg)
}

// compile-time assertions: both the Service and the Client satisfy TaskAPI.
var (
	_ TaskAPI = (*Client)(nil)
	_ TaskAPI = (*Service)(nil)
)
