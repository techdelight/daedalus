// Copyright (C) 2026 Techdelight BV

package executor

import (
	"errors"
	"testing"
)

func TestNewMockExecutor(t *testing.T) {
	// Arrange / Act
	m := NewMockExecutor()

	// Assert
	if m.Results == nil {
		t.Fatal("Results map should be initialized, got nil")
	}
	if len(m.Calls) != 0 {
		t.Errorf("Calls should be empty, got %d entries", len(m.Calls))
	}
}

func TestMockExecutor_Run(t *testing.T) {
	tests := []struct {
		name      string
		cmdName   string
		args      []string
		result    *MockResult
		wantErr   bool
		wantCalls int
	}{
		{
			name:      "records call and returns nil by default",
			cmdName:   "docker",
			args:      []string{"ps", "-a"},
			result:    nil,
			wantErr:   false,
			wantCalls: 1,
		},
		{
			name:      "returns configured error from Results",
			cmdName:   "docker",
			args:      []string{"build", "."},
			result:    &MockResult{Err: errors.New("build failed")},
			wantErr:   true,
			wantCalls: 1,
		},
		{
			name:      "records call with no args",
			cmdName:   "ls",
			args:      nil,
			result:    nil,
			wantErr:   false,
			wantCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			m := NewMockExecutor()
			if tt.result != nil {
				m.Results[tt.cmdName] = *tt.result
			}

			// Act
			err := m.Run(tt.cmdName, tt.args...)

			// Assert
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
			if len(m.Calls) != tt.wantCalls {
				t.Fatalf("expected %d call(s), got %d", tt.wantCalls, len(m.Calls))
			}
			if m.Calls[0].Name != tt.cmdName {
				t.Errorf("expected call name %q, got %q", tt.cmdName, m.Calls[0].Name)
			}
			if len(tt.args) > 0 {
				if len(m.Calls[0].Args) != len(tt.args) {
					t.Fatalf("expected %d args, got %d", len(tt.args), len(m.Calls[0].Args))
				}
				for i, arg := range tt.args {
					if m.Calls[0].Args[i] != arg {
						t.Errorf("arg[%d]: expected %q, got %q", i, arg, m.Calls[0].Args[i])
					}
				}
			}
		})
	}
}

func TestMockExecutor_RunWithEnv(t *testing.T) {
	tests := []struct {
		name    string
		env     []string
		cmdName string
		args    []string
		result  *MockResult
		wantErr bool
	}{
		{
			name:    "records call without env and returns nil",
			env:     []string{"FOO=bar"},
			cmdName: "make",
			args:    []string{"build"},
			result:  nil,
			wantErr: false,
		},
		{
			name:    "returns configured error",
			env:     []string{"DEBUG=1"},
			cmdName: "make",
			args:    []string{"test"},
			result:  &MockResult{Err: errors.New("test failed")},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			m := NewMockExecutor()
			if tt.result != nil {
				m.Results[tt.cmdName] = *tt.result
			}

			// Act
			err := m.RunWithEnv(tt.env, tt.cmdName, tt.args...)

			// Assert
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
			if len(m.Calls) != 1 {
				t.Fatalf("expected 1 call, got %d", len(m.Calls))
			}
			if m.Calls[0].Name != tt.cmdName {
				t.Errorf("expected call name %q, got %q", tt.cmdName, m.Calls[0].Name)
			}
			// Env is NOT stored in Call — verify args only
			if len(m.Calls[0].Args) != len(tt.args) {
				t.Fatalf("expected %d args, got %d", len(tt.args), len(m.Calls[0].Args))
			}
			for i, arg := range tt.args {
				if m.Calls[0].Args[i] != arg {
					t.Errorf("arg[%d]: expected %q, got %q", i, arg, m.Calls[0].Args[i])
				}
			}
		})
	}
}

func TestMockExecutor_Output(t *testing.T) {
	tests := []struct {
		name       string
		cmdName    string
		args       []string
		result     *MockResult
		wantOutput string
		wantErr    bool
	}{
		{
			name:       "returns empty string and nil by default",
			cmdName:    "git",
			args:       []string{"status"},
			result:     nil,
			wantOutput: "",
			wantErr:    false,
		},
		{
			name:       "returns configured output and nil error",
			cmdName:    "git",
			args:       []string{"rev-parse", "HEAD"},
			result:     &MockResult{Output: "abc123\n", Err: nil},
			wantOutput: "abc123\n",
			wantErr:    false,
		},
		{
			name:       "returns configured output and error",
			cmdName:    "git",
			args:       []string{"log"},
			result:     &MockResult{Output: "partial", Err: errors.New("interrupted")},
			wantOutput: "partial",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			m := NewMockExecutor()
			if tt.result != nil {
				m.Results[tt.cmdName] = *tt.result
			}

			// Act
			output, err := m.Output(tt.cmdName, tt.args...)

			// Assert
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
			if output != tt.wantOutput {
				t.Errorf("expected output %q, got %q", tt.wantOutput, output)
			}
			if len(m.Calls) != 1 {
				t.Fatalf("expected 1 call, got %d", len(m.Calls))
			}
			if m.Calls[0].Name != tt.cmdName {
				t.Errorf("expected call name %q, got %q", tt.cmdName, m.Calls[0].Name)
			}
		})
	}
}

func TestMockExecutor_Exec(t *testing.T) {
	tests := []struct {
		name    string
		cmdName string
		args    []string
		result  *MockResult
		wantErr bool
	}{
		{
			name:    "records call with exec: prefix and returns nil",
			cmdName: "tmux",
			args:    []string{"attach", "-t", "my-session"},
			result:  nil,
			wantErr: false,
		},
		{
			name:    "returns configured error for exec: prefixed key",
			cmdName: "tmux",
			args:    []string{"attach"},
			result:  &MockResult{Err: errors.New("no session")},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			m := NewMockExecutor()
			if tt.result != nil {
				m.Results["exec:"+tt.cmdName] = *tt.result
			}

			// Act
			err := m.Exec(tt.cmdName, tt.args...)

			// Assert
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
			if len(m.Calls) != 1 {
				t.Fatalf("expected 1 call, got %d", len(m.Calls))
			}
			expectedName := "exec:" + tt.cmdName
			if m.Calls[0].Name != expectedName {
				t.Errorf("expected call name %q, got %q", expectedName, m.Calls[0].Name)
			}
			if len(m.Calls[0].Args) != len(tt.args) {
				t.Fatalf("expected %d args, got %d", len(tt.args), len(m.Calls[0].Args))
			}
			for i, arg := range tt.args {
				if m.Calls[0].Args[i] != arg {
					t.Errorf("arg[%d]: expected %q, got %q", i, arg, m.Calls[0].Args[i])
				}
			}
		})
	}
}

func TestMockExecutor_ExecWithEnv(t *testing.T) {
	tests := []struct {
		name    string
		env     []string
		cmdName string
		args    []string
		result  *MockResult
		wantErr bool
	}{
		{
			name:    "records call with exec: prefix and returns nil",
			env:     []string{"TMUX_TMPDIR=/tmp"},
			cmdName: "tmux",
			args:    []string{"attach", "-t", "my-session"},
			result:  nil,
			wantErr: false,
		},
		{
			name:    "returns configured error for exec: prefixed key",
			env:     []string{"TMUX_TMPDIR=/tmp"},
			cmdName: "tmux",
			args:    []string{"attach"},
			result:  &MockResult{Err: errors.New("no session")},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			m := NewMockExecutor()
			if tt.result != nil {
				m.Results["exec:"+tt.cmdName] = *tt.result
			}

			// Act
			err := m.ExecWithEnv(tt.env, tt.cmdName, tt.args...)

			// Assert
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
			if len(m.Calls) != 1 {
				t.Fatalf("expected 1 call, got %d", len(m.Calls))
			}
			expectedName := "exec:" + tt.cmdName
			if m.Calls[0].Name != expectedName {
				t.Errorf("expected call name %q, got %q", expectedName, m.Calls[0].Name)
			}
			if len(m.Calls[0].Args) != len(tt.args) {
				t.Fatalf("expected %d args, got %d", len(tt.args), len(m.Calls[0].Args))
			}
			for i, arg := range tt.args {
				if m.Calls[0].Args[i] != arg {
					t.Errorf("arg[%d]: expected %q, got %q", i, arg, m.Calls[0].Args[i])
				}
			}
		})
	}
}

func TestMockExecutor_LookPath(t *testing.T) {
	tests := []struct {
		name       string
		binary     string
		result     *MockResult
		wantPath   string
		wantErr    bool
	}{
		{
			name:     "returns /usr/bin/<name> by default",
			binary:   "docker",
			result:   nil,
			wantPath: "/usr/bin/docker",
			wantErr:  false,
		},
		{
			name:     "returns configured output path",
			binary:   "go",
			result:   &MockResult{Output: "/usr/local/go/bin/go"},
			wantPath: "/usr/local/go/bin/go",
			wantErr:  false,
		},
		{
			name:     "returns configured error",
			binary:   "missing",
			result:   &MockResult{Output: "", Err: errors.New("not found")},
			wantPath: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			m := NewMockExecutor()
			if tt.result != nil {
				m.Results["lookpath:"+tt.binary] = *tt.result
			}

			// Act
			path, err := m.LookPath(tt.binary)

			// Assert
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
			if path != tt.wantPath {
				t.Errorf("expected path %q, got %q", tt.wantPath, path)
			}
			if len(m.Calls) != 1 {
				t.Fatalf("expected 1 call, got %d", len(m.Calls))
			}
			expectedName := "lookpath:" + tt.binary
			if m.Calls[0].Name != expectedName {
				t.Errorf("expected call name %q, got %q", expectedName, m.Calls[0].Name)
			}
		})
	}
}

func TestMockExecutor_HasCall(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(m *MockExecutor)
		query    string
		expected bool
	}{
		{
			name: "returns true when call exists",
			setup: func(m *MockExecutor) {
				_ = m.Run("docker", "ps")
			},
			query:    "docker",
			expected: true,
		},
		{
			name:     "returns false when no calls recorded",
			setup:    func(m *MockExecutor) {},
			query:    "docker",
			expected: false,
		},
		{
			name: "returns false when name does not match",
			setup: func(m *MockExecutor) {
				_ = m.Run("git", "status")
			},
			query:    "docker",
			expected: false,
		},
		{
			name: "matches exec: prefixed calls",
			setup: func(m *MockExecutor) {
				_ = m.Exec("tmux", "attach")
			},
			query:    "exec:tmux",
			expected: true,
		},
		{
			name: "matches lookpath: prefixed calls",
			setup: func(m *MockExecutor) {
				_, _ = m.LookPath("docker")
			},
			query:    "lookpath:docker",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			m := NewMockExecutor()
			tt.setup(m)

			// Act
			result := m.HasCall(tt.query)

			// Assert
			if result != tt.expected {
				t.Errorf("HasCall(%q) = %v, want %v", tt.query, result, tt.expected)
			}
		})
	}
}

func TestMockExecutor_FindCall(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(m *MockExecutor)
		query    string
		wantNil  bool
		wantArgs []string
	}{
		{
			name: "returns pointer to first matching call",
			setup: func(m *MockExecutor) {
				_ = m.Run("docker", "ps")
				_ = m.Run("docker", "build", ".")
			},
			query:    "docker",
			wantNil:  false,
			wantArgs: []string{"ps"},
		},
		{
			name:    "returns nil when not found",
			setup:   func(m *MockExecutor) {},
			query:   "docker",
			wantNil: true,
		},
		{
			name: "returns nil when name does not match",
			setup: func(m *MockExecutor) {
				_ = m.Run("git", "log")
			},
			query:   "docker",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			m := NewMockExecutor()
			tt.setup(m)

			// Act
			result := m.FindCall(tt.query)

			// Assert
			if tt.wantNil {
				if result != nil {
					t.Fatalf("expected nil, got %+v", result)
				}
				return
			}
			if result == nil {
				t.Fatal("expected non-nil result, got nil")
			}
			if result.Name != tt.query {
				t.Errorf("expected name %q, got %q", tt.query, result.Name)
			}
			if len(result.Args) != len(tt.wantArgs) {
				t.Fatalf("expected %d args, got %d", len(tt.wantArgs), len(result.Args))
			}
			for i, arg := range tt.wantArgs {
				if result.Args[i] != arg {
					t.Errorf("arg[%d]: expected %q, got %q", i, arg, result.Args[i])
				}
			}
		})
	}
}

func TestMockExecutor_FindCalls(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(m *MockExecutor)
		query     string
		wantCount int
	}{
		{
			name: "returns all matching calls",
			setup: func(m *MockExecutor) {
				_ = m.Run("docker", "ps")
				_ = m.Run("git", "status")
				_ = m.Run("docker", "build", ".")
				_ = m.Run("docker", "push", "img")
			},
			query:     "docker",
			wantCount: 3,
		},
		{
			name:      "returns empty slice when none match",
			setup:     func(m *MockExecutor) {},
			query:     "docker",
			wantCount: 0,
		},
		{
			name: "returns empty slice when name does not match",
			setup: func(m *MockExecutor) {
				_ = m.Run("git", "log")
				_ = m.Run("make", "build")
			},
			query:     "docker",
			wantCount: 0,
		},
		{
			name: "returns single matching call",
			setup: func(m *MockExecutor) {
				_ = m.Run("git", "status")
				_ = m.Run("docker", "ps")
				_ = m.Run("make", "build")
			},
			query:     "docker",
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			m := NewMockExecutor()
			tt.setup(m)

			// Act
			results := m.FindCalls(tt.query)

			// Assert
			if len(results) != tt.wantCount {
				t.Fatalf("expected %d call(s), got %d", tt.wantCount, len(results))
			}
			for _, c := range results {
				if c.Name != tt.query {
					t.Errorf("expected all calls to have name %q, got %q", tt.query, c.Name)
				}
			}
		})
	}
}
