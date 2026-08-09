// Copyright (C) 2026 Techdelight BV

package control

// Go client for the daedalus-control daemon, the wire counterpart to daemon.go.
// It implements TaskAPI, so the CLI command handlers are identical whether given
// a live *Client (production) or an in-process *Service (tests).
//
// Domain sentinels survive the wire: the daemon maps them to status codes and an
// {"error": ...} envelope, and the client re-raises a matching sentinel via
// errors.Is where the CLI needs to branch (ErrNotFound, policy refusals).

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

// PlaneStatus implements TaskAPI.
func (c *Client) PlaneStatus() (PlaneStatus, error) {
	var st PlaneStatus
	return st, c.getJSON("/status", &st)
}

// AddDependency implements TaskAPI.
func (c *Client) AddDependency(taskID, dependsOn string) (DependencyEdge, error) {
	var edge DependencyEdge
	return edge, c.postJSON("/tasks/"+url.PathEscape(taskID)+"/dependencies",
		dependencyRequest{DependsOn: dependsOn}, &edge)
}

// TaskDependencies implements TaskAPI.
func (c *Client) TaskDependencies(taskID string) (DependencyView, error) {
	var view DependencyView
	return view, c.getJSON("/tasks/"+url.PathEscape(taskID)+"/dependencies", &view)
}

// ProgrammeBoard implements TaskAPI.
func (c *Client) ProgrammeBoard() (BoardView, error) {
	var view BoardView
	return view, c.getJSON("/board", &view)
}

// SteerJob implements TaskAPI.
func (c *Client) SteerJob(jobID, instruction string) (SteeringEvent, error) {
	var steer SteeringEvent
	return steer, c.postJSON("/jobs/"+url.PathEscape(jobID)+"/steer",
		steerRequest{Instruction: instruction}, &steer)
}

// JobSteering implements TaskAPI.
func (c *Client) JobSteering(jobID string) ([]SteeringEvent, error) {
	var out []SteeringEvent
	if err := c.getJSON("/jobs/"+url.PathEscape(jobID)+"/steering", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CancelSteering implements TaskAPI.
func (c *Client) CancelSteering(steerID string) (SteeringEvent, error) {
	req, _ := http.NewRequest(http.MethodDelete, c.baseURL+"/steering/"+url.PathEscape(steerID), nil)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return SteeringEvent{}, fmt.Errorf("control client: DELETE /steering/%s: %w", steerID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return SteeringEvent{}, decodeError(resp)
	}
	var steer SteeringEvent
	return steer, json.NewDecoder(resp.Body).Decode(&steer)
}

// ListProposals implements TaskAPI.
func (c *Client) ListProposals(state ProposalState) ([]Proposal, error) {
	path := "/proposals"
	if state != "" {
		path += "?state=" + url.QueryEscape(string(state))
	}
	var out []Proposal
	if err := c.getJSON(path, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ResolveProposal implements TaskAPI.
func (c *Client) ResolveProposal(id string, confirm bool, note string) (Proposal, error) {
	action := "deny"
	if confirm {
		action = "confirm"
	}
	var p Proposal
	return p, c.postJSON("/proposals/"+url.PathEscape(id)+"/"+action, approvalRequest{Note: note}, &p)
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
	// Malformed input survives the wire as the same sentinel, so a caller can tell
	// "you asked wrongly" from "the daemon broke" in-process and over the socket
	// alike.
	if resp.StatusCode == http.StatusBadRequest {
		return fmt.Errorf("%w: %s", ErrInvalidRequest, msg)
	}
	return fmt.Errorf("control daemon %d: %s", resp.StatusCode, msg)
}

// compile-time assertions: both the Service and the Client satisfy TaskAPI.
var (
	_ TaskAPI = (*Client)(nil)
	_ TaskAPI = (*Service)(nil)
)
