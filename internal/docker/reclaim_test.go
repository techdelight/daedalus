// Copyright (C) 2026 Techdelight BV

package docker

import (
	"errors"
	"strings"
	"testing"

	"github.com/techdelight/daedalus/internal/executor"
)

// buildExec scripts successive answers to `docker image inspect`, which is the
// only way to exercise a reclaim: the decision is made by comparing the id
// BEFORE the build with the id AFTER it, and a mock keyed by command name alone
// cannot answer the same question two different ways.
type buildExec struct {
	*executor.MockExecutor
	inspectIDs []string // successive ids; "" means "no such image"
	inspects   int
	buildErr   error
}

func newBuildExec(ids ...string) *buildExec {
	return &buildExec{MockExecutor: executor.NewMockExecutor(), inspectIDs: ids}
}

func (b *buildExec) Output(name string, args ...string) (string, error) {
	b.Calls = append(b.Calls, executor.Call{Name: name, Args: args})
	if len(args) >= 2 && args[0] == "image" && args[1] == "inspect" {
		i := b.inspects
		b.inspects++
		if i < len(b.inspectIDs) && b.inspectIDs[i] != "" {
			return b.inspectIDs[i] + "\n", nil
		}
		return "", errors.New("Error: No such image")
	}
	return "", nil
}

func (b *buildExec) Run(name string, args ...string) error {
	b.Calls = append(b.Calls, executor.Call{Name: name, Args: args})
	if len(args) > 0 && args[0] == "build" {
		return b.buildErr
	}
	return nil
}

// removed returns the ids passed to `docker rmi`.
func (b *buildExec) removed(t *testing.T) []string {
	t.Helper()
	var ids []string
	for _, c := range b.Calls {
		if c.Name == "docker" && len(c.Args) >= 2 && c.Args[0] == "rmi" {
			for _, a := range c.Args[1:] {
				if a == "-f" || a == "--force" {
					t.Errorf("rmi was forced (%v) — a refusal is the answer we want, not something to push past", c.Args)
				}
			}
			ids = append(ids, c.Args[len(c.Args)-1])
		}
	}
	return ids
}

// TestBuild_ReclaimsTheImageItSuperseded.
//
// The tag is constant for a runner+target, so a rebuild retags it and the old
// image is left carrying no tag — a `<none>` entry nothing removed. Rebuilds run
// automatically whenever the build files change, and a runner image is
// gigabytes, so this was the most expensive thing the tool leaked.
func TestBuild_ReclaimsTheImageItSuperseded(t *testing.T) {
	exec := newBuildExec("sha256:old", "sha256:new")
	d := NewDocker(exec, "/compose.yml")

	if err := d.Build("dev", "techdelight/claude-runner:dev", "1000", "/ctx"); err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := exec.removed(t)
	if len(got) != 1 || got[0] != "sha256:old" {
		t.Errorf("removed %v, want [sha256:old] — the superseded image was left dangling", got)
	}
}

// TestBuild_DoesNotRemoveTheImageItJustBuilt.
//
// The dangerous case, and the reason the check is "did the id move" rather than
// "did we rebuild". A rebuild with every layer cached produces the SAME image id,
// so a reclaim keyed on "we ran a build" would delete the image the user just
// asked for — turning a disk saving into a broken install.
func TestBuild_DoesNotRemoveTheImageItJustBuilt(t *testing.T) {
	exec := newBuildExec("sha256:same", "sha256:same")
	d := NewDocker(exec, "/compose.yml")

	if err := d.Build("dev", "techdelight/claude-runner:dev", "1000", "/ctx"); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := exec.removed(t); len(got) != 0 {
		t.Errorf("removed %v — that is the image the build just produced", got)
	}
}

// A first build has nothing to supersede, and must not try.
func TestBuild_FirstEverBuildReclaimsNothing(t *testing.T) {
	exec := newBuildExec("", "sha256:new") // no image before the build
	d := NewDocker(exec, "/compose.yml")

	if err := d.Build("dev", "techdelight/claude-runner:dev", "1000", "/ctx"); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := exec.removed(t); len(got) != 0 {
		t.Errorf("removed %v on a first build", got)
	}
}

// A failed build supersedes nothing: the old image is still the working one, and
// removing it would leave the project with no image at all.
func TestBuild_AFailedBuildReclaimsNothing(t *testing.T) {
	exec := newBuildExec("sha256:old", "sha256:new")
	exec.buildErr = errors.New("build failed")
	d := NewDocker(exec, "/compose.yml")

	if err := d.Build("dev", "techdelight/claude-runner:dev", "1000", "/ctx"); err == nil {
		t.Fatal("Build should have returned the build error")
	}
	if got := exec.removed(t); len(got) != 0 {
		t.Errorf("removed %v after a FAILED build — the old image is still the working one", got)
	}
}

// A cleanup failure must not fail a build that succeeded: the image the caller
// asked for exists either way.
func TestBuild_SurvivesAFailedReclaim(t *testing.T) {
	exec := &failingRmi{buildExec: newBuildExec("sha256:old", "sha256:new")}
	d := NewDocker(exec, "/compose.yml")

	if err := d.Build("dev", "techdelight/claude-runner:dev", "1000", "/ctx"); err != nil {
		t.Errorf("Build returned %v — a failed cleanup must not fail the build", err)
	}
}

type failingRmi struct{ *buildExec }

func (f *failingRmi) Output(name string, args ...string) (string, error) {
	if len(args) > 0 && args[0] == "rmi" {
		f.Calls = append(f.Calls, executor.Call{Name: name, Args: args})
		return "", errors.New("image is being used by running container abc123")
	}
	return f.buildExec.Output(name, args...)
}

// The reclaim must stay scoped to the image daedalus replaced. `docker image
// prune` would reclaim dangling images belonging to everything else on the
// machine, which is not this tool's to delete.
func TestBuild_NeverPrunesTheWholeMachine(t *testing.T) {
	exec := newBuildExec("sha256:old", "sha256:new")
	d := NewDocker(exec, "/compose.yml")

	if err := d.Build("dev", "techdelight/claude-runner:dev", "1000", "/ctx"); err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, c := range exec.Calls {
		if c.Name == "docker" && len(c.Args) >= 2 && c.Args[0] == "image" && c.Args[1] == "prune" {
			t.Fatalf("daedalus ran a machine-wide image prune: %v", c.Args)
		}
		if c.Name == "docker" && len(c.Args) >= 1 && c.Args[0] == "system" {
			t.Fatalf("daedalus ran `docker system %v`", strings.Join(c.Args, " "))
		}
	}
}
