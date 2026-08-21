// Copyright (C) 2026 Techdelight BV

package core

import (
	"strings"
	"testing"
)

// TestBuildInfo_String renders what an operator compares.
//
// The version alone is release granularity and this project routinely lands
// dozens of commits under one unreleased number — it said 0.54.0 through twelve
// of them on the day this was written. A build identity has to distinguish
// builds, or it answers "is what I am looking at the code we just changed" with
// something true and useless.
func TestBuildInfo_String(t *testing.T) {
	for _, tc := range []struct {
		in   BuildInfo
		want string
	}{
		{BuildInfo{Version: "0.54.0", Commit: "46470c9"}, "0.54.0+46470c9"},
		{BuildInfo{Version: "0.54.0", Commit: "46470c9", Modified: true}, "0.54.0+46470c9+dirty"},
		// No revision at all is honest rather than invented: a release build with
		// -buildvcs=false and no -X Commit genuinely does not know.
		{BuildInfo{Version: "0.54.0"}, "0.54.0"},
	} {
		if got := tc.in.String(); got != tc.want {
			t.Errorf("BuildInfo%+v.String() = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The commit is shortened because two of them get compared by eye.
func TestShortCommit(t *testing.T) {
	if got := shortCommit("46470c9417a7982ff3907a3cc59ea345cf490d75"); got != "46470c9" {
		t.Errorf("shortCommit = %q, want 46470c9", got)
	}
	if got := shortCommit("  abc  "); got != "abc" {
		t.Errorf("shortCommit trims: got %q", got)
	}
	if shortCommit("") != "" {
		t.Error("an absent commit must stay absent rather than becoming a short nothing")
	}
}

// TestReadBuildInfo_PrefersLdflags: a release build disables -buildvcs, so the
// linker flag is the only source it has. It must win over anything the runtime
// reports, or a release would identify itself by whatever happened to be stamped.
func TestReadBuildInfo_PrefersLdflags(t *testing.T) {
	saved := Commit
	t.Cleanup(func() { Commit = saved })

	Commit = "deadbeefcafe"
	if got := ReadBuildInfo().Commit; got != "deadbeef"[:7] && got != "deadbee" {
		t.Errorf("commit = %q, want the ldflags value, shortened", got)
	}

	// And with nothing set, it falls back to the build's own VCS stamp. A test
	// binary built from this module carries one; if the toolchain ever stops
	// providing it the answer is an empty commit, which is honest — so this
	// asserts the shape rather than a value.
	Commit = ""
	info := ReadBuildInfo()
	if info.Version == "" {
		t.Error("the version is always known; it is compiled in")
	}
	if info.Commit != "" && len(info.Commit) != 7 {
		t.Errorf("commit = %q, want it shortened to 7", info.Commit)
	}
	if !strings.HasPrefix(info.String(), info.Version) {
		t.Errorf("String() = %q, want it to start with the version", info.String())
	}
}
