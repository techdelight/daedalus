// Copyright (C) 2026 Techdelight BV

package core

import (
	"runtime/debug"
	"strings"
)

// Build identity — which build of Daedalus is actually running.
//
// `Version` alone cannot answer that. It is release granularity, set from the
// VERSION file, and this project routinely lands dozens of commits under one
// unreleased number: on 2026-08-21 it said 0.54.0 through twelve of them. An
// operator asking "is the thing I am looking at the code we just changed"
// therefore got an answer that was true, useless, and easy to mistake for
// useful — which is exactly how a Task came to be cancelled over a button that
// had not been written yet.
//
// The commit is what distinguishes builds, and Go records it for free:
// `go build` stamps vcs.revision into the binary unless `-buildvcs=false`. The
// ldflags path takes precedence for release builds, which do disable it.

// Commit is the source revision, set at compile time via -ldflags:
//
//	-X github.com/techdelight/daedalus/core.Commit=$(git rev-parse HEAD)
//
// Empty means "ask the build info", which is the ordinary case for a local build.
var Commit = ""

// BuildInfo is who this binary is.
type BuildInfo struct {
	Version string `json:"version"`
	// Commit is short, because it is read by humans comparing two of them.
	Commit string `json:"commit,omitempty"`
	// Modified reports a build made from a dirty working tree. It matters more
	// than it looks: a commit id from a dirty tree names something that was never
	// committed, so two binaries can share it and differ.
	Modified bool `json:"modified,omitempty"`
}

// ReadBuildInfo returns the running binary's identity.
func ReadBuildInfo() BuildInfo {
	out := BuildInfo{Version: Version, Commit: shortCommit(Commit)}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return out
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if out.Commit == "" {
				out.Commit = shortCommit(s.Value)
			}
		case "vcs.modified":
			out.Modified = s.Value == "true"
		}
	}
	return out
}

// String renders the build for a human: "0.54.0+46470c9", or "+dirty" when the
// tree was not clean, or just the version when there is no revision at all.
func (b BuildInfo) String() string {
	var sb strings.Builder
	sb.WriteString(b.Version)
	if b.Commit != "" {
		sb.WriteString("+" + b.Commit)
	}
	if b.Modified {
		sb.WriteString("+dirty")
	}
	return sb.String()
}

// BuildID is the build identity as one token, used to stamp asset URLs.
//
// Stamping with the VERSION alone was not enough and had already failed: a
// rebuilt binary at the same unreleased version served the same URLs, so a
// browser kept a cached script across a change it was supposed to pick up.
func BuildID() string { return ReadBuildInfo().String() }

func shortCommit(c string) string {
	c = strings.TrimSpace(c)
	if len(c) > 7 {
		return c[:7]
	}
	return c
}
