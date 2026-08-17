// Copyright (C) 2026 Techdelight BV

package executor

import (
	"io"
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
	// RunWithEnvTee is RunWithEnv with the child's output additionally copied to
	// w. A nil w is exactly RunWithEnv, so a caller with nowhere to tee does not
	// have to branch. See RealExecutor.RunWithEnvTee for why stdout and stderr
	// are merged into one stream.
	RunWithEnvTee(env []string, w io.Writer, name string, args ...string) error
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

// RunWithEnvTee runs the child with its output copied to w as well as to this
// process's stdout, so a caller can keep a private record of a run whose output
// also has to keep flowing to wherever it always went.
//
// The SAME io.Writer value is assigned to both cmd.Stdout and cmd.Stderr, and
// that is deliberate rather than incidental. os/exec special-cases the two being
// equal (`interfaceEqual(c.Stderr, c.Stdout)`) by handing the child ONE pipe for
// both, which buys two things: the child's interleaving is preserved in the
// order it actually happened, and there is exactly one copying goroutine, so no
// two writers race on w. Building two separate MultiWriters would lose both.
//
// The cost is that the child's stderr arrives on this process's stdout rather
// than its stderr. For the control plane that is a non-event — the daemon sends
// both to the same control.log — and a merged log is what a reader wants anyway.
func (r *RealExecutor) RunWithEnvTee(env []string, w io.Writer, name string, args ...string) error {
	if w == nil {
		return r.RunWithEnv(env, name, args...)
	}
	cmd := exec.Command(name, args...)
	merged := io.MultiWriter(os.Stdout, w)
	cmd.Stdout = merged
	cmd.Stderr = merged
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
	// Env is the extra environment a RunWithEnv/ExecWithEnv call passed, nil for
	// the plain forms. Recorded because for some callers the environment IS the
	// contract — the control plane pins a spawned CLI's data dir through it, and
	// a test that could not see it could not tell a pinned launch from an
	// unpinned one.
	Env []string
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
	m.Calls = append(m.Calls, Call{Name: name, Args: args, Env: env})
	if r, ok := m.Results[name]; ok {
		return r.Err
	}
	return nil
}

// RunWithEnvTee records the call and writes the canned MockResult.Output to w,
// standing in for the child's output. Without that a test could assert only that
// a tee was requested, not that anything ever reached it — and "the log file is
// created but always empty" is precisely the failure worth catching.
func (m *MockExecutor) RunWithEnvTee(env []string, w io.Writer, name string, args ...string) error {
	m.Calls = append(m.Calls, Call{Name: name, Args: args, Env: env})
	r, ok := m.Results[name]
	if ok && w != nil && r.Output != "" {
		_, _ = io.WriteString(w, r.Output)
	}
	if ok {
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
