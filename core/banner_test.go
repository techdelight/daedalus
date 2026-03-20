// Copyright (C) 2026 Techdelight BV

package core

import (
	"testing"
)

func TestReadVersion(t *testing.T) {
	old := Version
	defer func() { Version = old }()

	Version = "3.2.1"
	if got := ReadVersion(); got != "3.2.1" {
		t.Errorf("ReadVersion() = %q, want %q", got, "3.2.1")
	}
}
