// Copyright (C) 2026 Techdelight BV

package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ProjectMountRoot is the ONE in-container directory under which a project's
// configured extra host directories appear: /mnt/<name>.
//
// The root is fixed on purpose. A project's registry entry names WHAT to mount
// and under WHICH name, never where — so a configured mount can never land on
// /workspace, /opt/tools, /home/claude or any other path the runner already
// owns, and reading `projects.json` tells you the full set of places host
// directories can appear without having to hold the container's layout in your
// head. It is the same shape as the Guild Master's /guild/<name> (guild.go),
// for the same reason.
const ProjectMountRoot = "/mnt"

// ProjectMount is one extra host directory a project asks to see inside its
// container, as configured in `projects.json`:
//
//	"mounts": [
//	  { "name": "datasets", "host": "/srv/datasets", "readOnly": true },
//	  { "name": "out",      "host": "/home/me/out" }
//	]
//
// Name is a single path segment, and the mount appears at /mnt/<name>.
//
// READONLY DEFAULTS TO FALSE — a configured mount is writable unless it says
// otherwise, which is what `-v host:target` means everywhere else and therefore
// what an operator writing this by hand will expect. The asymmetry with
// /guild/<name> (always :ro) is deliberate: those mounts are minted by Daedalus
// for an agent that must not write another project's files, these are named by
// the human who owns the machine.
type ProjectMount struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	ReadOnly bool   `json:"readOnly,omitempty"`
}

// Target returns where the mount appears inside the container.
func (m ProjectMount) Target() string { return ProjectMountRoot + "/" + m.Name }

// RejectedMount is a configured mount that will NOT be passed to docker, and
// why. Refusals are values rather than silence because the failure they guard
// against is invisible: a container that starts with an empty /mnt/<name> looks
// exactly like one that was never configured, and the agent inside it reports
// the directory as missing rather than as misconfigured.
type RejectedMount struct {
	Mount  ProjectMount
	Reason string
}

func (r RejectedMount) String() string {
	name := r.Mount.Name
	if name == "" {
		name = "(unnamed)"
	}
	return fmt.Sprintf("%q -> %s: %s", name, r.Mount.Host, r.Reason)
}

// validMountName is the alphabet of a mount name: the same one project names
// use (project.go), so an operator has one rule to remember rather than two.
var validMountName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// ValidateMountName reports whether name is usable as a single directory under
// ProjectMountRoot. It rejects the empty string, "." and "..", anything holding
// a path separator, and anything outside the slug alphabet — so a name can
// never walk out of /mnt, and cannot smuggle a colon into docker's `-v`
// argument (see ProjectMountArgs).
func ValidateMountName(name string) error {
	if name == "" {
		return fmt.Errorf("name is empty")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("name %q is not a directory name", name)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("name %q must be a single path segment, not a path", name)
	}
	// Unreachable while the separator check above stands — on this platform every
	// input that survives it is its own Base. Kept, and knowingly uncovered,
	// because it is the check that keeps holding if that one is ever narrowed;
	// the same belt-and-braces guild.go's sanitiseGuildMountName carries.
	if filepath.Base(name) != name {
		return fmt.Errorf("name %q is not a single path segment", name)
	}
	if !validMountName.MatchString(name) {
		return fmt.Errorf("name %q must start with a letter or digit and hold only [a-zA-Z0-9._-]", name)
	}
	return nil
}

// ProjectMountArgs turns a project's configured mounts into `docker run -v`
// arguments, and returns every mount it refused alongside the reason.
//
// It never returns an error: one bad row must not stop a project from opening,
// so a refusal costs that mount and nothing else. The caller reports the
// rejections — the CLI to the operator's terminal, the coordinator to its log.
//
// The four refusals, and why each is a refusal rather than a repair:
//
//  1. A name that is not a single safe segment. "../../etc" as a name would
//     mount over a host path outside /mnt entirely. See ValidateMountName.
//  2. A repeated name. Two `-v` flags targeting one path is a coin toss decided
//     by docker's argument order, so the first wins and the second is refused
//     loudly rather than shadowing it quietly.
//  3. A host path that is not absolute, or that contains a colon. Both are
//     about docker's own syntax: `-v foo:/mnt/x` does not mount the relative
//     directory `foo`, it creates a NAMED VOLUME called foo — an empty
//     directory that looks like a successful mount — and a colon inside the
//     host field re-splits the argument, so "/tmp/x:/etc" would mount /etc.
//  4. A host path that is missing, or is not a directory. Docker CREATES a
//     missing bind source, root-owned, which the container's claude user then
//     cannot write; and the container is not the place to discover that the
//     path was a typo. Requiring a directory also means this cannot be used to
//     hand a container a single sensitive FILE — /var/run/docker.sock included,
//     which stays behind --dind where an operator can see it.
//
// Order is preserved, so the argument list reads in the order projects.json
// does.
func ProjectMountArgs(mounts []ProjectMount) (args []string, rejected []RejectedMount) {
	seen := make(map[string]bool, len(mounts))
	for _, m := range mounts {
		if err := ValidateMountName(m.Name); err != nil {
			rejected = append(rejected, RejectedMount{m, err.Error()})
			continue
		}
		if seen[m.Name] {
			rejected = append(rejected, RejectedMount{m, fmt.Sprintf("duplicate mount name %q; the first one wins", m.Name)})
			continue
		}
		host := strings.TrimSpace(m.Host)
		if host == "" {
			rejected = append(rejected, RejectedMount{m, "host path is empty"})
			continue
		}
		if strings.Contains(host, ":") {
			rejected = append(rejected, RejectedMount{m, "host path contains ':', which docker reads as a field separator"})
			continue
		}
		if !filepath.IsAbs(host) {
			rejected = append(rejected, RejectedMount{m, "host path must be absolute (a relative one would create a named volume, not a bind mount)"})
			continue
		}
		host = filepath.Clean(host)
		fi, err := os.Stat(host)
		if err != nil {
			rejected = append(rejected, RejectedMount{m, "host path does not exist"})
			continue
		}
		if !fi.IsDir() {
			rejected = append(rejected, RejectedMount{m, "host path is not a directory"})
			continue
		}
		spec := host + ":" + m.Target()
		if m.ReadOnly {
			spec += ":ro"
		}
		seen[m.Name] = true
		args = append(args, "-v", spec)
	}
	return args, rejected
}
