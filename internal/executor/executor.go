// Copyright (C) 2026 Techdelight BV

package executor

import (
	"os"
	"os/exec"
	"syscall"
)

// Executor abstracts command execution for testability.
type Executor interface {
	// Run executes a command, inheriting stdout/stderr.
	Run(name string, args ...string) error
	// RunWithEnv executes a command with extra environment variables,
	// without polluting the parent process environment.
	RunWithEnv(env []string, name string, args ...string) error
	// Output executes and captures stdout.
	Output(name string, args ...string) (string, error)
	// Exec replaces the current process (syscall.Exec).
	Exec(name string, args ...string) error
	// ExecWithEnv replaces the current process with extra environment variables.
	ExecWithEnv(env []string, name string, args ...string) error
	// LookPath checks if a binary exists on PATH.
	LookPath(name string) (string, error)
}

// RealExecutor implements Executor using os/exec and syscall.
type RealExecutor struct{}

func (r *RealExecutor) Run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func (r *RealExecutor) RunWithEnv(env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = append(os.Environ(), env...)
	return cmd.Run()
}

func (r *RealExecutor) Output(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	return string(out), err
}

func (r *RealExecutor) Exec(name string, args ...string) error {
	binary, err := exec.LookPath(name)
	if err != nil {
		return err
	}
	argv := append([]string{name}, args...)
	return syscall.Exec(binary, argv, os.Environ())
}

func (r *RealExecutor) ExecWithEnv(env []string, name string, args ...string) error {
	binary, err := exec.LookPath(name)
	if err != nil {
		return err
	}
	argv := append([]string{name}, args...)
	return syscall.Exec(binary, argv, append(os.Environ(), env...))
}

func (r *RealExecutor) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

// MockExecutor records calls and returns canned results. Used in tests.
type MockExecutor struct {
	Calls   []Call
	Results map[string]MockResult
}

type Call struct {
	Name string
	Args []string
}

type MockResult struct {
	Output string
	Err    error
}

func NewMockExecutor() *MockExecutor {
	return &MockExecutor{
		Results: make(map[string]MockResult),
	}
}

func (m *MockExecutor) Run(name string, args ...string) error {
	m.Calls = append(m.Calls, Call{Name: name, Args: args})
	if r, ok := m.Results[name]; ok {
		return r.Err
	}
	return nil
}

func (m *MockExecutor) RunWithEnv(env []string, name string, args ...string) error {
	m.Calls = append(m.Calls, Call{Name: name, Args: args})
	if r, ok := m.Results[name]; ok {
		return r.Err
	}
	return nil
}

func (m *MockExecutor) Output(name string, args ...string) (string, error) {
	m.Calls = append(m.Calls, Call{Name: name, Args: args})
	if r, ok := m.Results[name]; ok {
		return r.Output, r.Err
	}
	return "", nil
}

func (m *MockExecutor) Exec(name string, args ...string) error {
	m.Calls = append(m.Calls, Call{Name: "exec:" + name, Args: args})
	if r, ok := m.Results["exec:"+name]; ok {
		return r.Err
	}
	return nil
}

func (m *MockExecutor) ExecWithEnv(env []string, name string, args ...string) error {
	m.Calls = append(m.Calls, Call{Name: "exec:" + name, Args: args})
	if r, ok := m.Results["exec:"+name]; ok {
		return r.Err
	}
	return nil
}

func (m *MockExecutor) LookPath(name string) (string, error) {
	m.Calls = append(m.Calls, Call{Name: "lookpath:" + name})
	if r, ok := m.Results["lookpath:"+name]; ok {
		return r.Output, r.Err
	}
	return "/usr/bin/" + name, nil
}

// HasCall checks if a call with the given name was recorded.
func (m *MockExecutor) HasCall(name string) bool {
	for _, c := range m.Calls {
		if c.Name == name {
			return true
		}
	}
	return false
}

// FindCall returns the first call matching the given name, or nil.
func (m *MockExecutor) FindCall(name string) *Call {
	for i := range m.Calls {
		if m.Calls[i].Name == name {
			return &m.Calls[i]
		}
	}
	return nil
}

// FindCalls returns all calls matching the given name.
func (m *MockExecutor) FindCalls(name string) []Call {
	var result []Call
	for _, c := range m.Calls {
		if c.Name == name {
			result = append(result, c)
		}
	}
	return result
}
