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

// VerifyTask implements TaskAPI.
func (c *Client) VerifyTask(id string) (VerifyResult, error) {
	resp, err := c.httpClient.Post(c.baseURL+"/tasks/"+url.PathEscape(id)+"/verify", "application/json", nil)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("control client: POST /tasks/%s/verify: %w", id, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return VerifyResult{}, decodeError(resp)
	}
	var res VerifyResult
	return res, json.NewDecoder(resp.Body).Decode(&res)
}

// RetryTask implements TaskAPI.
func (c *Client) RetryTask(id string, req RetryRequest) (RetryResult, error) {
	var res RetryResult
	return res, c.postJSON("/tasks/"+url.PathEscape(id)+"/retry", req, &res)
}

// ReplanTask implements TaskAPI.
func (c *Client) ReplanTask(id string, req ReplanRequest) (Task, error) {
	var t Task
	return t, c.postJSON("/tasks/"+url.PathEscape(id)+"/replan", req, &t)
}

// TaskEvents implements TaskAPI. Read-only: the client has no counterpart that
// writes to the log, because the daemon exposes no route that would accept one.
func (c *Client) TaskEvents(id string) ([]Event, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/tasks/" + url.PathEscape(id) + "/events")
	if err != nil {
		return nil, fmt.Errorf("control client: GET /tasks/%s/events: %w", id, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, decodeError(resp)
	}
	var events []Event
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		return nil, err
	}
	return events, nil
}

// postJSON posts body to path and decodes a 200 response into out.
func (c *Client) postJSON(path string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Post(c.baseURL+path, "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("control client: POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decodeError(resp)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ReviewTask implements TaskAPI.
func (c *Client) ReviewTask(id string) (ReviewResult, error) {
	var res ReviewResult
	return res, c.postJSON("/tasks/"+url.PathEscape(id)+"/review", struct{}{}, &res)
}

// ApproveTask implements TaskAPI.
func (c *Client) ApproveTask(id, note string) (Task, error) {
	var t Task
	return t, c.postJSON("/tasks/"+url.PathEscape(id)+"/approve", approvalRequest{Note: note}, &t)
}

// RejectApproval implements TaskAPI.
func (c *Client) RejectApproval(id, note string) (Task, error) {
	var t Task
	return t, c.postJSON("/tasks/"+url.PathEscape(id)+"/reject", approvalRequest{Note: note}, &t)
}

// IntegrateTask implements TaskAPI.
func (c *Client) IntegrateTask(id string) (IntegrationResult, error) {
	var res IntegrationResult
	return res, c.postJSON("/tasks/"+url.PathEscape(id)+"/integrate", struct{}{}, &res)
}

// PendingApprovals implements TaskAPI.
func (c *Client) PendingApprovals() ([]Task, error) {
	var tasks []Task
	if err := c.getJSON("/approvals", &tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

// ProjectTargets implements TaskAPI.
func (c *Client) ProjectTargets() ([]TargetView, error) {
	var targets []TargetView
	if err := c.getJSON("/targets", &targets); err != nil {
		return nil, err
	}
	return targets, nil
}

// SyncTarget implements TaskAPI.
func (c *Client) SyncTarget(project string) (Target, error) {
	var t Target
	return t, c.postJSON("/targets/"+url.PathEscape(project)+"/sync", struct{}{}, &t)
}

// getJSON fetches path and decodes a 200 response into out.
func (c *Client) getJSON(path string, out any) error {
	resp, err := c.httpClient.Get(c.baseURL + path)
	if err != nil {
		return fmt.Errorf("control client: GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decodeError(resp)
	}
	return json.NewDecoder(resp.Body).Decode(out)
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
		Error   string          `json:"error"`
		Reason  RejectionReason `json:"reason"`
		Message string          `json:"message"`
	}
	msg := strings.TrimSpace(string(body))
	if err := json.Unmarshal(body, &env); err == nil && env.Error != "" {
		msg = env.Error
	}
	// A policy refusal is rebuilt as the same typed error the Service raised, so
	// errors.As(&RejectionError) works identically in-process and over the socket
	// — and the CLI's "refused" exit code does not depend on where the logic ran.
	if resp.StatusCode == http.StatusUnprocessableEntity && IsValidRejectionReason(env.Reason) {
		detail := env.Message
		if detail == "" {
			detail = msg
		}
		return &RejectionError{Reason: env.Reason, Message: detail}
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
