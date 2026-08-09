// Copyright (C) 2026 Techdelight BV

package control

import (
	"context"
	"errors"
	"testing"

	"github.com/techdelight/daedalus/internal/executor"
)

// TestCoordinatorRunner_DeregistersThrowawayProject is the regression for the
// registry leak: the real runner registers a throwaway `daedalus-job-<id>`
// project to launch the agent, and before this fix never removed it — one dead
// entry per Job, forever, pointing at a worktree that gets reclaimed.
//
// The container run itself is host-only, but the *registration lifecycle* is
// pure command execution, so it is asserted here through the injected Executor.
func TestCoordinatorRunner_DeregistersThrowawayProject(t *testing.T) {
	tests := []struct {
		name       string
		runErr     error
		wantResult ExecutionResult
	}{
		{"after a successful run", nil, ExecSuccess},
		{"after a failed run", errors.New("agent exited 1"), ExecFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exec := executor.NewMockExecutor()
			if tc.runErr != nil {
				exec.Results["/bin/daedalus"] = executor.MockResult{Err: tc.runErr}
			}
			r := CoordinatorRunner{Exec: exec, BinPath: "/bin/daedalus"}
			out := r.Run(context.Background(), JobSpec{
				JobID: "J-7", Project: "app", Objective: "do work", WorktreeDir: "/wt",
			})
			if out.Result != tc.wantResult {
				t.Errorf("result = %q, want %q", out.Result, tc.wantResult)
			}
			name := JobProjectName("J-7")
			var launched, removed bool
			for _, c := range exec.Calls {
				if len(c.Args) >= 2 && c.Args[0] == name && c.Args[1] == "/wt" {
					launched = true
				}
				if len(c.Args) >= 3 && c.Args[0] == "remove" && c.Args[1] == name && c.Args[2] == "--force" {
					removed = true
				}
			}
			if !launched {
				t.Errorf("expected a launch under %s, got calls %+v", name, exec.Calls)
			}
			if !removed {
				t.Errorf("throwaway project %s was never deregistered — the registry leaks one entry per Job. Calls: %+v", name, exec.Calls)
			}
		})
	}
}

// TestJobProjectName is deterministic and job-scoped, so concurrent jobs never
// collide and a deregistration can never target another job's entry.
func TestJobProjectName(t *testing.T) {
	if a, b := JobProjectName("J-1"), JobProjectName("J-2"); a == b {
		t.Errorf("names collide: %s == %s", a, b)
	}
	if got := JobProjectName("J-1"); got != "daedalus-job-J-1" {
		t.Errorf("JobProjectName = %q, want daedalus-job-J-1", got)
	}
}
