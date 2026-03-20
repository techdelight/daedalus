// Copyright (C) 2026 Techdelight BV

package core

// Version is set at compile time via -ldflags:
//
//	go build -ldflags "-X github.com/techdelight/daedalus/core.Version=0.5.2"
var Version = "unknown"

// ReadVersion returns the compile-time version baked into the binary.
func ReadVersion() string {
	return Version
}
