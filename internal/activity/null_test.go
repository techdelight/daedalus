// Copyright (C) 2026 Techdelight BV

package activity

import (
	"testing"

	"github.com/techdelight/daedalus/core"
)

func TestNullDetector_AlwaysIdle(t *testing.T) {
	d := &NullDetector{}
	info := d.Detect("/nonexistent")
	if info.State != core.ActivityIdle {
		t.Errorf("got %q, want %q", info.State, core.ActivityIdle)
	}
}
